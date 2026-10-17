package upstream

import (
	"context"
	"errors"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/zovocard"
)

const findOrderPageSize = 100

// CreateOrder 提交直充。请求号同时作为 Idempotency-Key，重放时上游返回原订单。
func (g *Gateway) CreateOrder(ctx context.Context, request directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
	credential, err := toCredential(request.Mode, request.Credential)
	if err != nil {
		return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindCredential}
	}
	result, err := g.client.CreateOrder(ctx, zovocard.CreateOrderRequest{
		Product:         request.Product,
		Plan:            request.Plan,
		CardID:          request.CardID,
		PaymentCountry:  request.PaymentCountry,
		PaymentCurrency: request.PaymentCurrency,
		Credential:      credential,
		PreflightToken:  request.PreflightToken,
		ClientRequestID: request.ClientRequestID,
		PricingVersion:  request.PricingVersion,
		IdempotencyKey:  request.ClientRequestID,
	})
	if err != nil {
		return nil, classify("create_order", err)
	}
	return toOrder(result.Order), nil
}

func (g *Gateway) GetOrder(ctx context.Context, upstreamOrderID int64) (*directchargecontract.UpstreamOrder, error) {
	detail, err := g.client.GetOrder(ctx, upstreamOrderID)
	if err != nil {
		return nil, classify("get_order", err)
	}
	return toOrder(detail.Order), nil
}

// FindOrder 翻最近几页订单列表按请求号匹配；列表接口不支持按请求号筛选，只作兜底
func (g *Gateway) FindOrder(ctx context.Context, clientRequestID string, maxPages int) (*directchargecontract.UpstreamOrder, error) {
	if maxPages < 1 {
		maxPages = 1
	}
	for page := 1; page <= maxPages; page++ {
		result, err := g.client.ListOrders(ctx, page, findOrderPageSize)
		if err != nil {
			return nil, classify("list_orders", err)
		}
		for _, order := range result.List {
			if order.ClientRequestID == clientRequestID {
				return toOrder(order), nil
			}
		}
		if int64(page*findOrderPageSize) >= result.Total || len(result.List) == 0 {
			break
		}
	}
	return nil, nil
}

func (g *Gateway) ParseWebhook(rawBody []byte, signature string) (*directchargecontract.UpstreamEvent, error) {
	if err := zovocard.VerifySignature(g.webhookSecret, rawBody, signature); err != nil {
		return nil, directchargecontract.ErrWebhookSignature
	}
	event, relevant, err := zovocard.ParseDirectEvent(rawBody)
	if err != nil {
		return nil, err
	}
	hash := zovocard.BodyHash(rawBody)
	return &directchargecontract.UpstreamEvent{
		DedupKey:        event.DedupKey(hash),
		Type:            event.Type,
		BodyHash:        hash,
		UpstreamOrderID: event.OrderID,
		ClientRequestID: event.ClientRequestID,
		Status:          event.Status,
		Relevant:        relevant,
	}, nil
}

func toOrder(order zovocard.Order) *directchargecontract.UpstreamOrder {
	outcome := directchargecontract.UpstreamOutcomeUnknown
	switch order.Outcome() {
	case zovocard.OutcomePending:
		outcome = directchargecontract.UpstreamOutcomePending
	case zovocard.OutcomeSucceeded:
		outcome = directchargecontract.UpstreamOutcomeSucceeded
	case zovocard.OutcomeFailed:
		outcome = directchargecontract.UpstreamOutcomeFailed
	case zovocard.OutcomeCancelled:
		outcome = directchargecontract.UpstreamOutcomeCancelled
	}
	return &directchargecontract.UpstreamOrder{
		ID:                 order.ID,
		ClientRequestID:    order.ClientRequestID,
		Status:             order.Status,
		Stage:              order.Stage,
		Message:            order.Message,
		Outcome:            outcome,
		FailedBeforeCharge: order.FailedBeforeCharge(),
		Currency:           order.Currency,
		QuotedAmountMinor:  order.QuotedAmountMinor,
		FinalAmountMinor:   order.FinalAmountMinor,
		ServiceFeeMinor:    order.ServiceFeeMinor,
		ServiceFeeStatus:   order.ServiceFeeStatus,
		FundingHoldStatus:  order.FundingHoldStatus,
	}
}

// classify 把上游错误归类为任务可处理的类别；只记录错误码，不记录响应正文
func classify(operation string, err error) error {
	code := ""
	var apiErr *zovocard.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode
	}
	out := &directchargecontract.UpstreamError{Code: code, Unknown: zovocard.ResultUnknown(err)}
	switch zovocard.Classify(err) {
	case zovocard.KindCredential:
		out.Kind = directchargecontract.UpstreamKindCredential
	case zovocard.KindNotEligible:
		out.Kind = directchargecontract.UpstreamKindNotEligible
	case zovocard.KindRequote:
		out.Kind = directchargecontract.UpstreamKindRequote
	case zovocard.KindFunds:
		out.Kind = directchargecontract.UpstreamKindFunds
	case zovocard.KindConfig:
		out.Kind = directchargecontract.UpstreamKindConfig
	case zovocard.KindUnavailable, zovocard.KindRateLimited:
		out.Kind = directchargecontract.UpstreamKindUnavailable
	case zovocard.KindNotFound:
		out.Kind = directchargecontract.UpstreamKindNotFound
	case zovocard.KindRejected:
		out.Kind = directchargecontract.UpstreamKindRejected
	case zovocard.KindTransport, zovocard.KindServer, zovocard.KindConflict:
		out.Kind = directchargecontract.UpstreamKindTransient
	default:
		// 本地校验失败等非上游错误：请求没有发出
		out.Kind = directchargecontract.UpstreamKindRejected
		out.Unknown = false
	}
	logger.Warnw("directcharge_upstream_call_failed", "operation", operation, "kind", out.Kind, "error_code", code, "result_unknown", out.Unknown)
	return out
}
