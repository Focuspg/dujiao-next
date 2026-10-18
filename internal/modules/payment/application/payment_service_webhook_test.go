package application

import (
	"errors"
	"testing"
	"time"

	paymentcontract "github.com/dujiao-next/internal/modules/payment/contract"

	paymentdomain "github.com/dujiao-next/internal/modules/payment/domain"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

func TestSupportsBlindWebhookCandidateMatching(t *testing.T) {
	cases := []struct {
		channelType string
		want        bool
	}{
		{constants.PaymentChannelTypeWechat, true},
		{constants.PaymentChannelTypeStripe, true},
		{constants.PaymentChannelTypePaypal, false},
		{constants.PaymentChannelTypeAlipay, false},
		{"", false},
		{"  WECHAT  ", true},
	}
	for _, tc := range cases {
		if got := supportsBlindWebhookCandidateMatching(tc.channelType); got != tc.want {
			t.Fatalf("channelType=%q: got %v, want %v", tc.channelType, got, tc.want)
		}
	}
}

// TestHandleWechatWebhookFallbackNoCandidate 验证 channel_id 为 0 且没有任何 active wechat
// channel 时,wechat webhook 返回 ErrPaymentChannelNotFound(而非 ErrPaymentInvalid);
// 这是相对 67bbc47 重构后回归行为(硬性拒绝 channel_id==0)的关键修复。
func TestHandleWechatWebhookFallbackNoCandidate(t *testing.T) {
	svc, _ := setupPaymentServiceWalletTest(t)

	_, _, err := svc.HandleWechatWebhook(WebhookCallbackInput{
		ChannelID: 0,
		Body:      []byte(`{"resource":{}}`),
		Headers:   map[string]string{},
	})
	if !errors.Is(err, ErrPaymentChannelNotFound) {
		t.Fatalf("expected ErrPaymentChannelNotFound for empty wechat candidate list, got: %v", err)
	}
}

// TestHandlePaypalWebhookRequiresChannelID 验证 paypal webhook 必须带 channel_id,
// 不具备盲匹配能力;channel_id==0 直接走 ErrPaymentInvalid。
func TestHandlePaypalWebhookRequiresChannelID(t *testing.T) {
	svc, _ := setupPaymentServiceWalletTest(t)

	_, _, err := svc.HandlePaypalWebhook(WebhookCallbackInput{
		ChannelID: 0,
		Body:      []byte(`{}`),
		Headers:   map[string]string{},
	})
	if !errors.Is(err, ErrPaymentInvalid) {
		t.Fatalf("expected ErrPaymentInvalid for paypal without channel_id, got: %v", err)
	}
}

// fixedCallbackVerifier 跳过验签，直接返回预设的回调事实。
type fixedCallbackVerifier struct {
	emptyProviderRefProvider
	result paymentcontract.GatewayCallbackResult
}

func (v fixedCallbackVerifier) VerifyCallback(jsonmap.JSON, map[string][]string, []byte) (*paymentcontract.GatewayCallbackResult, error) {
	result := v.result
	return &result, nil
}

func TestEpaySyncCallbackRejectsProviderRefMismatch(t *testing.T) {
	svc, db := setupPaymentServiceWalletTest(t)
	channel, order := createFeePolicyOrderFixture(t, db, "Epay Gateway", "DJ-EPAY-REF")
	if err := db.Model(channel).Updates(map[string]interface{}{
		"provider_type": constants.PaymentProviderEpay, "channel_type": constants.PaymentChannelTypeAlipay,
	}).Error; err != nil {
		t.Fatalf("switch channel to epay: %v", err)
	}
	channel.ProviderType = constants.PaymentProviderEpay
	channel.ChannelType = constants.PaymentChannelTypeAlipay
	now := time.Now()
	payment := &paymentdomain.Payment{
		OrderID: order.ID, ChannelID: channel.ID, ProviderType: channel.ProviderType, ChannelType: channel.ChannelType,
		InteractionMode: channel.InteractionMode, Amount: money.FromDecimal(decimal.NewFromInt(100)),
		Currency: "CNY", Status: constants.PaymentStatusPending, GatewayOrderNo: "DJP-EPAY-REF",
		ProviderRef: "T-CREATED", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(payment).Error; err != nil {
		t.Fatalf("create payment: %v", err)
	}
	callback := paymentcontract.GatewayCallbackResult{
		OrderNo: "DJP-EPAY-REF", ProviderRef: "T-OTHER", Status: constants.PaymentStatusSuccess,
		Amount: payment.Amount, Currency: "CNY",
	}
	registerTestGateway(t, svc, channel.ProviderType, channel.ChannelType, fixedCallbackVerifier{result: callback})

	if _, err := svc.HandleSyncCallback(channel, nil, nil); !errors.Is(err, ErrPaymentInvalid) {
		t.Fatalf("mismatched trade_no: err = %v, want ErrPaymentInvalid", err)
	}
	var stored paymentdomain.Payment
	if err := db.First(&stored, payment.ID).Error; err != nil {
		t.Fatalf("reload payment: %v", err)
	}
	if stored.Status != constants.PaymentStatusPending || stored.ProviderRef != "T-CREATED" {
		t.Fatalf("payment changed after rejected callback: %+v", stored)
	}

	callback.ProviderRef = "T-CREATED"
	registerTestGateway(t, svc, channel.ProviderType, channel.ChannelType, fixedCallbackVerifier{result: callback})
	updated, err := svc.HandleSyncCallback(channel, nil, nil)
	if err != nil {
		t.Fatalf("matching trade_no: %v", err)
	}
	if updated.Status != constants.PaymentStatusSuccess {
		t.Fatalf("status = %s", updated.Status)
	}
}

// 跳转模式下单时拿不到平台 trade_no，ProviderRef 兜底存的是商户单号；
// 回调带来的平台 trade_no 不算冲突，应正常入账并回写为平台单号。
func TestEpaySyncCallbackAcceptsRedirectModeFallbackProviderRef(t *testing.T) {
	svc, db := setupPaymentServiceWalletTest(t)
	channel, order := createFeePolicyOrderFixture(t, db, "Epay Redirect", "DJ-EPAY-REDIRECT")
	if err := db.Model(channel).Updates(map[string]interface{}{
		"provider_type": constants.PaymentProviderEpay, "channel_type": constants.PaymentChannelTypeAlipay,
	}).Error; err != nil {
		t.Fatalf("switch channel to epay: %v", err)
	}
	channel.ProviderType = constants.PaymentProviderEpay
	channel.ChannelType = constants.PaymentChannelTypeAlipay
	now := time.Now()
	payment := &paymentdomain.Payment{
		OrderID: order.ID, ChannelID: channel.ID, ProviderType: channel.ProviderType, ChannelType: channel.ChannelType,
		InteractionMode: constants.PaymentInteractionRedirect, Amount: money.FromDecimal(decimal.NewFromInt(100)),
		Currency: "CNY", Status: constants.PaymentStatusPending, GatewayOrderNo: "DJP-EPAY-REDIRECT",
		ProviderRef: "DJP-EPAY-REDIRECT", CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(payment).Error; err != nil {
		t.Fatalf("create payment: %v", err)
	}
	callback := paymentcontract.GatewayCallbackResult{
		OrderNo: "DJP-EPAY-REDIRECT", ProviderRef: "2026101820541355285", Status: constants.PaymentStatusSuccess,
		Amount: payment.Amount, Currency: "CNY",
	}
	registerTestGateway(t, svc, channel.ProviderType, channel.ChannelType, fixedCallbackVerifier{result: callback})

	updated, err := svc.HandleSyncCallback(channel, nil, nil)
	if err != nil {
		t.Fatalf("redirect-mode callback rejected: %v", err)
	}
	if updated.Status != constants.PaymentStatusSuccess || updated.ProviderRef != "2026101820541355285" {
		t.Fatalf("expected success with platform trade_no, got status=%s ref=%s", updated.Status, updated.ProviderRef)
	}
}
