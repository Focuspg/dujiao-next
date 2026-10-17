package contract

import "errors"

var (
	// ErrDisabled 代充未启用或未完成配置
	ErrDisabled = errors.New("direct charge disabled")
	// ErrProductUnavailable SKU 未映射、规格未启用，或上游没有给出该套餐报价
	ErrProductUnavailable = errors.New("direct charge product unavailable")
	// ErrCredentialUnsupported 凭据模式不被该规格接受，或内容超长
	ErrCredentialUnsupported = errors.New("direct charge credential unsupported")
	// ErrCredentialInvalid 上游判定凭据无效或已过期，或无法识别目标账号
	ErrCredentialInvalid = errors.New("direct charge credential invalid")
	// ErrNotEligible 目标账号当前套餐不适用
	ErrNotEligible = errors.New("direct charge account not eligible")
	// ErrUpstreamBusy 上游维护、限流、超时或资金不足等暂时性问题
	ErrUpstreamBusy = errors.New("direct charge upstream busy")
	// ErrQuoteExceeded 报价超出规格允许的范围
	ErrQuoteExceeded = errors.New("direct charge quote exceeded")
	// ErrPreflightExpired 预检引用不存在、不属于该用户、已过期或已被使用
	ErrPreflightExpired = errors.New("direct charge preflight expired")
	// ErrAccountBusy 目标账号已有进行中的任务
	ErrAccountBusy = errors.New("direct charge account busy")
	// ErrNotFound 代充订单不存在
	ErrNotFound = errors.New("direct charge task not found")
	// ErrOrderCreateFailed 本地订单创建失败
	ErrOrderCreateFailed = errors.New("direct charge order create failed")
)

// OrderRejectedError 订单模块拒绝创建订单（风控、商品下架等），MessageKey 为可直接返回给用户的 i18n key
type OrderRejectedError struct {
	MessageKey string
	TooMany    bool
	Err        error
}

func (e *OrderRejectedError) Error() string { return "direct charge order rejected: " + e.MessageKey }

func (e *OrderRejectedError) Unwrap() error { return e.Err }

// 上游错误类别（付款后的提交与查单使用，决定任务下一步）
const (
	UpstreamKindCredential  = "credential"   // 凭据失效：等待用户补充
	UpstreamKindNotEligible = "not_eligible" // 账号不适用：确定失败
	UpstreamKindRequote     = "requote"      // 报价过期：重新预检
	UpstreamKindFunds       = "funds"        // 上游余额不足：稍后重试并告警
	UpstreamKindConfig      = "config"       // 密钥、白名单等配置问题：稍后重试并告警
	UpstreamKindUnavailable = "unavailable"  // 维护、限流、熔断：稍后重试
	UpstreamKindNotFound    = "not_found"
	UpstreamKindRejected    = "rejected" // 其它业务拒绝：人工核验
	UpstreamKindTransient   = "transient"
)

// UpstreamError 上游调用失败。Unknown 为 true 表示写请求可能已在上游执行，只能用原请求号重放或查单。
type UpstreamError struct {
	Kind    string
	Code    string
	Unknown bool
}

func (e *UpstreamError) Error() string {
	text := "direct charge upstream error: " + e.Kind
	if e.Code != "" {
		text += " (" + e.Code + ")"
	}
	if e.Unknown {
		text += ", result unknown"
	}
	return text
}

// ErrWebhookSignature 回调签名无效
var ErrWebhookSignature = errors.New("direct charge webhook signature invalid")
