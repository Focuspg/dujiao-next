package epayadapter

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	paymentcontract "github.com/dujiao-next/internal/modules/payment/contract"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/modules/payment/infrastructure/gateway/epay"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

const (
	epayCurrency   = "CNY"
	epayTimeLayout = "2006-01-02 15:04:05"
)

// epay 网关时间为北京时间，不依赖运行环境的时区数据库。
var epayTimeZone = time.FixedZone("CST", 8*60*60)

var (
	_ paymentcontract.GatewayOrderQuerier = (*epayAdapter)(nil)
	_ paymentcontract.GatewayRefunder     = (*epayAdapter)(nil)
)

// QueryPaymentByOrderNo 按商户支付单号查单（仅 V2）。只有 status=1 且无退款金额才视为支付成功；
// 已退款、冻结、预授权或部分退款都标记 Abnormal，保持 pending 交人工核对。
func (a *epayAdapter) QueryPaymentByOrderNo(ctx context.Context, raw jsonmap.JSON, orderNo string) (*paymentcontract.GatewayOrderQueryResult, error) {
	cfg, err := a.parseConfig(raw)
	if err != nil {
		return nil, err
	}
	result, err := epay.QueryOrder(ctx, cfg, epay.QueryInput{OutTradeNo: orderNo})
	if err != nil {
		return nil, mapEpayError(err)
	}
	amount, err := decimal.NewFromString(result.Money)
	if err != nil {
		return nil, fmt.Errorf("%w: epay query money", paymentcontract.ErrGatewayResponseInvalid)
	}
	out := &paymentcontract.GatewayOrderQueryResult{
		GatewayCallbackResult: paymentcontract.GatewayCallbackResult{
			OrderNo:     result.OutTradeNo,
			ProviderRef: result.TradeNo,
			Status:      constants.PaymentStatusPending,
			Amount:      money.FromDecimal(amount),
			Currency:    epayCurrency,
			Payload:     jsonmap.JSON(result.Raw),
		},
		GatewayStatus: strconv.Itoa(result.Status),
	}
	switch {
	case result.Status == epay.QueryStatusPaid && !hasRefund(result.RefundMoney):
		out.Status = constants.PaymentStatusSuccess
		out.PaidAt = parseEpayTime(result.EndTime)
	case result.Status == epay.QueryStatusUnpaid:
	default:
		out.Abnormal = true
	}
	return out, nil
}

// Refund 原路退款（仅 V2）。金额必须是当初提交给网关的人民币金额。
func (a *epayAdapter) Refund(ctx context.Context, raw jsonmap.JSON, input paymentcontract.GatewayRefundInput) (*paymentcontract.GatewayRefundResult, error) {
	cfg, err := a.parseConfig(raw)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(strings.TrimSpace(input.Currency), epayCurrency) {
		return nil, fmt.Errorf("%w: epay refund currency must be %s", paymentcontract.ErrGatewayConfigInvalid, epayCurrency)
	}
	native := epay.RefundInput{
		OutRefundNo: input.RefundNo,
		Money:       input.Amount.Decimal.String(),
	}
	// 只带一种单号：商户单号始终存在，网关单号作为兜底。
	if orderNo := strings.TrimSpace(input.OrderNo); orderNo != "" {
		native.OutTradeNo = orderNo
	} else {
		native.TradeNo = input.ProviderRef
	}
	result, err := epay.Refund(ctx, cfg, native)
	if err != nil {
		return nil, mapEpayError(err)
	}
	return newRefundResult(result)
}

// QueryRefund 按商户退款单号查询退款结果（仅 V2）。
func (a *epayAdapter) QueryRefund(ctx context.Context, raw jsonmap.JSON, refundNo string) (*paymentcontract.GatewayRefundResult, error) {
	cfg, err := a.parseConfig(raw)
	if err != nil {
		return nil, err
	}
	result, err := epay.QueryRefund(ctx, cfg, refundNo)
	if err != nil {
		return nil, mapEpayError(err)
	}
	return newRefundResult(result)
}

func newRefundResult(result *epay.RefundResult) (*paymentcontract.GatewayRefundResult, error) {
	amount, err := decimal.NewFromString(result.Money)
	if err != nil {
		return nil, fmt.Errorf("%w: epay refund money", paymentcontract.ErrGatewayResponseInvalid)
	}
	status := paymentcontract.GatewayRefundStatusFailed
	if result.Succeeded {
		status = paymentcontract.GatewayRefundStatusSucceeded
	}
	return &paymentcontract.GatewayRefundResult{
		RefundNo:          result.OutRefundNo,
		ProviderRefundRef: result.RefundNo,
		ProviderRef:       result.TradeNo,
		Status:            status,
		Amount:            money.FromDecimal(amount),
		Payload:           jsonmap.JSON(result.Raw),
	}, nil
}

func hasRefund(refundMoney string) bool {
	refunded, err := decimal.NewFromString(strings.TrimSpace(refundMoney))
	return err == nil && refunded.IsPositive()
}

func parseEpayTime(raw string) *time.Time {
	parsed, err := time.ParseInLocation(epayTimeLayout, strings.TrimSpace(raw), epayTimeZone)
	if err != nil {
		return nil
	}
	return &parsed
}
