package contract

import (
	"context"
	"time"

	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"
)

// GatewayPayloadFiatCurrencySent 标记创建支付时实际提交给网关的法币币种。
// 回调兼容逻辑用它区分具备新版事实快照的支付与升级前创建的在途支付。
const GatewayPayloadFiatCurrencySent = "_dujiao_next_fiat_currency_sent"

// GatewayCreateInput 是统一支付网关创建输入。
type GatewayCreateInput struct {
	PaymentID      uint
	OrderID        uint
	OrderNo        string
	Subject        string
	Amount         money.Amount
	Currency       string
	Email          string
	NotifyURL      string
	ReturnURL      string
	ReturnURLQuery map[string]string
	ClientIP       string
	ChannelType    string
	Extra          jsonmap.JSON
}

// GatewayCreateResult 是统一支付网关创建结果。
type GatewayCreateResult struct {
	ProviderRef        string
	RedirectURL        string
	QRCodeURL          string
	Payload            jsonmap.JSON
	DisplayChannelType string
	AmountSent         string
	CurrencySent       string
}

// GatewayQueryResult 是主动查询网关支付状态的结果。
type GatewayQueryResult struct {
	ProviderRef string
	Status      string
	Amount      money.Amount
	Currency    string
	PaidAt      *time.Time
	Payload     jsonmap.JSON
}

// GatewayOrderQueryResult 是按商户支付单号主动查单的结果。
// Abnormal 为 true 时网关状态既非待支付也非已支付（已退款、冻结、预授权等），
// Status 保持 pending，调用方不得据此推进订单，应转人工核对。
type GatewayOrderQueryResult struct {
	GatewayCallbackResult
	GatewayStatus string
	Abnormal      bool
}

// 网关退款状态。
const (
	GatewayRefundStatusSucceeded = "succeeded"
	GatewayRefundStatusFailed    = "failed"
)

// GatewayRefundInput 是原路退款输入。RefundNo 是商户退款单号，同一笔退款重试必须复用。
// Amount/Currency 必须是当初实际提交给网关的金额与币种（payment.Amount/Currency）。
type GatewayRefundInput struct {
	OrderNo     string
	ProviderRef string
	RefundNo    string
	Amount      money.Amount
	Currency    string
}

// GatewayRefundResult 是退款或退款查询结果。
type GatewayRefundResult struct {
	RefundNo          string
	ProviderRefundRef string
	ProviderRef       string
	Status            string
	Amount            money.Amount
	Payload           jsonmap.JSON
}

// GatewayCallbackResult 是同步回调或异步 Webhook 的标准结果。
type GatewayCallbackResult struct {
	OrderNo     string
	ProviderRef string
	Status      string
	Amount      money.Amount
	Currency    string
	PaidAt      *time.Time
	Payload     jsonmap.JSON
}

// GatewaySecurityTestResult 是支付网关安全能力的只读诊断结果。
// 结果只包含可公开的校验事实，不包含请求体、私钥或 API 密钥。
type GatewaySecurityTestResult struct {
	VerificationMode         string `json:"verification_mode"`
	ResponseSerial           string `json:"response_serial"`
	RequestSignatureAccepted bool   `json:"request_signature_accepted"`
	ResponseSignatureValid   bool   `json:"response_signature_valid"`
	EchoMessageMatched       bool   `json:"echo_message_matched"`
}

// GatewayProvider 是所有支付网关适配器必须实现的最小能力。
type GatewayProvider interface {
	Type() string
	ValidateConfig(cfg jsonmap.JSON, channelType string) error
	CreatePayment(ctx context.Context, cfg jsonmap.JSON, input GatewayCreateInput) (*GatewayCreateResult, error)
}

type GatewayCapturer interface {
	GatewayProvider
	QueryPayment(ctx context.Context, cfg jsonmap.JSON, providerRef string) (*GatewayQueryResult, error)
}

// GatewayOrderQuerier 按商户支付单号（GatewayOrderNo）主动查单，用于补偿丢失或延迟的异步通知。
type GatewayOrderQuerier interface {
	GatewayProvider
	QueryPaymentByOrderNo(ctx context.Context, cfg jsonmap.JSON, orderNo string) (*GatewayOrderQueryResult, error)
}

// GatewayRefunder 原路退款能力。Refund 返回 ErrGatewayRequestFailed（且不含 ErrGatewayRejected）时结果未知，
// 必须先 QueryRefund 确认，再决定是否用同一 RefundNo 重试。
type GatewayRefunder interface {
	GatewayProvider
	Refund(ctx context.Context, cfg jsonmap.JSON, input GatewayRefundInput) (*GatewayRefundResult, error)
	QueryRefund(ctx context.Context, cfg jsonmap.JSON, refundNo string) (*GatewayRefundResult, error)
}

type GatewayWebhooker interface {
	GatewayProvider
	ParseWebhook(ctx context.Context, cfg jsonmap.JSON, headers map[string]string, body []byte, now time.Time) (*GatewayCallbackResult, error)
}

// GatewaySecurityTester 是支持非交易安全诊断的网关可选能力。
type GatewaySecurityTester interface {
	GatewayProvider
	TestSecurity(ctx context.Context, cfg jsonmap.JSON) (*GatewaySecurityTestResult, error)
}

type GatewayCallbackVerifier interface {
	GatewayProvider
	VerifyCallback(cfg jsonmap.JSON, form map[string][]string, body []byte) (*GatewayCallbackResult, error)
}

// GatewayRegistry 是应用层使用的只读网关注册表端口。
type GatewayRegistry interface {
	Lookup(providerType, channelType string) (GatewayProvider, bool)
}
