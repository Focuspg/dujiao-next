// Package upstream 把 ZovoCard 客户端适配为代充用例需要的供应商端口，并把上游错误归类为模块错误。
package upstream

import (
	"context"
	"errors"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/zovocard"
)

type Gateway struct {
	client        *zovocard.Client
	webhookSecret string
}

var _ directchargecontract.Upstream = (*Gateway)(nil)

// New 返回供应商端口；client 为 nil（未配置）时返回 nil，用例会按未启用处理。
// webhookSecret 为空时拒绝所有回调。
func New(client *zovocard.Client, webhookSecret string) directchargecontract.Upstream {
	if client == nil {
		return nil
	}
	return &Gateway{client: client, webhookSecret: webhookSecret}
}

func toCredential(mode, value string) (zovocard.Credential, error) {
	credential := zovocard.Credential{Mode: mode}
	switch mode {
	case zovocard.CredentialModeSession:
		credential.Session = value
	case zovocard.CredentialModeAccessToken:
		credential.AccessToken = value
	default:
		return zovocard.Credential{}, directchargecontract.ErrCredentialUnsupported
	}
	return credential, nil
}

func (g *Gateway) Preflight(ctx context.Context, request directchargecontract.UpstreamPreflightRequest) (*directchargecontract.UpstreamPreflight, error) {
	credential, err := toCredential(request.Mode, request.Credential)
	if err != nil {
		return nil, err
	}
	result, err := g.client.Preflight(ctx, zovocard.PreflightRequest{
		Product:         request.Product,
		Credential:      credential,
		PaymentCountry:  request.PaymentCountry,
		PaymentCurrency: request.PaymentCurrency,
	})
	if err != nil {
		return nil, mapError("preflight", err)
	}
	quotes := make(map[string]directchargecontract.UpstreamQuote, len(result.Quotes))
	for plan, quote := range result.Quotes {
		quotes[plan] = directchargecontract.UpstreamQuote{
			Currency:    quote.Currency,
			AmountMinor: quote.AmountMinor,
			Exponent:    quote.MinorUnitExponent,
		}
	}
	return &directchargecontract.UpstreamPreflight{
		AccountID:      result.Email,
		CurrentPlan:    result.CurrentPlan,
		PreflightToken: result.PreflightToken,
		TokenExpiresAt: result.PreflightExpiresAt,
		Quotes:         quotes,
		QuoteError:     result.QuoteError,
	}, nil
}

// mapError 把上游错误归为对用户可解释的几类；只记录错误码，不记录响应正文
func mapError(operation string, err error) error {
	kind := zovocard.Classify(err)
	code := ""
	var apiErr *zovocard.APIError
	if errors.As(err, &apiErr) {
		code = apiErr.ErrorCode
	}
	switch kind {
	case zovocard.KindCredential, zovocard.KindRejected:
		return directchargecontract.ErrCredentialInvalid
	case zovocard.KindNotEligible:
		return directchargecontract.ErrNotEligible
	case zovocard.KindConfig, zovocard.KindFunds:
		logger.Errorw("directcharge_upstream_needs_operator", "operation", operation, "kind", string(kind), "error_code", code)
	default:
		logger.Warnw("directcharge_upstream_unavailable", "operation", operation, "kind", string(kind), "error_code", code)
	}
	return directchargecontract.ErrUpstreamBusy
}
