package zovocard

import (
	"errors"
	"fmt"
	"net/http"
)

// 上游稳定的 error_code（只按 error_code 判断，不匹配 msg 文案）
const (
	CodeAccessDenied           = "GPT_DIRECT_ACCESS_DENIED"
	CodeRechargeRequired       = "RECHARGE_REQUIRED"
	CodeAccountUnavailable     = "ACCOUNT_UNAVAILABLE"
	CodeDirectUnavailable      = "GPT_DIRECT_UNAVAILABLE"
	CodeDirectPaused           = "GPT_DIRECT_PAUSED"
	CodeProductDisabled        = "DIRECT_PRODUCT_DISABLED"
	CodeInvalidRequest         = "INVALID_REQUEST"
	CodeInsufficientBalance    = "INSUFFICIENT_BALANCE"
	CodePrecheckRejected       = "PRECHECK_REJECTED"
	CodePlanAlreadyActive      = "GPT_PLAN_ALREADY_ACTIVE"
	CodePriceUnconfirmed       = "GPT_PRICE_UNCONFIRMED"
	CodeGPTSessionInvalid      = "GPT_SESSION_INVALID"
	CodeClaudeSessionInvalid   = "CLAUDE_SESSION_INVALID"
	CodeSessionRequired        = "SESSION_REQUIRED"
	CodeCreditNeedsSub         = "GPT_CREDIT_REQUIRES_SUBSCRIPTION"
	CodeRenewTargetNotPro      = "GPT_RENEW_TARGET_NOT_PRO"
	CodeRenewSubExpired        = "GPT_RENEW_SUBSCRIPTION_EXPIRED"
	CodePreflightRateLimited   = "GPT_PREFLIGHT_RATE_LIMITED"
	CodeOrderRejected          = "GPT_DIRECT_ORDER_REJECTED"
	CodeChannelUnavailable     = "channel_unavailable"
	CodeLowerInsufficientFunds = "insufficient_balance"
)

// Kind 错误归类，决定调用方的处理方式
type Kind string

const (
	// KindCredential 凭据无效或缺失：需要用户补充凭据
	KindCredential Kind = "credential"
	// KindNotEligible 目标账号不适用该套餐（已有有效套餐、需要订阅等）：确定性失败，不要重试
	KindNotEligible Kind = "not_eligible"
	// KindRequote 报价或价格版本过期：需要重新预检
	KindRequote Kind = "requote"
	// KindFunds 上游可用余额不足：暂停接单并告警
	KindFunds Kind = "funds"
	// KindUnavailable 上游服务/产品暂停或熔断：稍后重试
	KindUnavailable Kind = "unavailable"
	// KindRateLimited 频率超限：退避重试
	KindRateLimited Kind = "rate_limited"
	// KindConfig 密钥、白名单、权限等配置问题：需要站主处理
	KindConfig Kind = "config"
	// KindConflict 冲突（同一幂等键仍在处理、订单已开始支付不可取消）：退避后查状态
	KindConflict Kind = "conflict"
	// KindNotFound 对象不存在
	KindNotFound Kind = "not_found"
	// KindRejected 其它业务拒绝
	KindRejected Kind = "rejected"
	// KindServer 上游服务端错误
	KindServer Kind = "server"
	// KindTransport 网络层错误，未拿到响应
	KindTransport Kind = "transport"
)

// APIError 上游返回了可解析的失败响应
type APIError struct {
	HTTPStatus int
	Write      bool
	ErrorCode  string
	Message    string
}

func (e *APIError) Error() string {
	if e.ErrorCode != "" {
		return fmt.Sprintf("zovocard: http %d %s: %s", e.HTTPStatus, e.ErrorCode, e.Message)
	}
	return fmt.Sprintf("zovocard: http %d: %s", e.HTTPStatus, e.Message)
}

// TransportError 请求未得到响应（超时、连接中断等）
type TransportError struct {
	Op    string
	Write bool
	Err   error
}

func (e *TransportError) Error() string { return fmt.Sprintf("zovocard: %s: %v", e.Op, e.Err) }

func (e *TransportError) Unwrap() error { return e.Err }

// Classify 把错误归类；非本包错误返回空字符串
func Classify(err error) Kind {
	var te *TransportError
	if errors.As(err, &te) {
		return KindTransport
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		return ""
	}
	switch ae.ErrorCode {
	case CodeGPTSessionInvalid, CodeClaudeSessionInvalid, CodeSessionRequired:
		return KindCredential
	case CodePlanAlreadyActive, CodeCreditNeedsSub, CodeRenewTargetNotPro, CodeRenewSubExpired, CodePrecheckRejected:
		return KindNotEligible
	case CodePriceUnconfirmed:
		return KindRequote
	case CodeInsufficientBalance, CodeLowerInsufficientFunds:
		return KindFunds
	case CodeDirectUnavailable, CodeDirectPaused, CodeProductDisabled, CodeChannelUnavailable:
		return KindUnavailable
	case CodePreflightRateLimited:
		return KindRateLimited
	case CodeAccessDenied, CodeRechargeRequired, CodeAccountUnavailable:
		return KindConfig
	}
	switch {
	case ae.HTTPStatus == http.StatusTooManyRequests:
		return KindRateLimited
	case ae.HTTPStatus == http.StatusUnauthorized || ae.HTTPStatus == http.StatusForbidden:
		return KindConfig
	case ae.HTTPStatus == http.StatusConflict:
		return KindConflict
	case ae.HTTPStatus == http.StatusNotFound:
		return KindNotFound
	case ae.HTTPStatus == http.StatusServiceUnavailable:
		return KindUnavailable
	case ae.HTTPStatus >= 500:
		return KindServer
	}
	return KindRejected
}

// ResultUnknown 写请求的结果是否不明：上游可能已经执行。
// 调用方必须用同一 client_request_id / Idempotency-Key 重放或查单，不能换号重建。
func ResultUnknown(err error) bool {
	var te *TransportError
	if errors.As(err, &te) {
		return te.Write
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Write && (ae.HTTPStatus >= 500 && ae.HTTPStatus != http.StatusServiceUnavailable || ae.HTTPStatus == http.StatusConflict)
	}
	return false
}

// Retryable 是否可以用相同参数和相同幂等键稍后重试
func Retryable(err error) bool {
	switch Classify(err) {
	case KindTransport, KindServer, KindRateLimited, KindUnavailable, KindConflict:
		return true
	}
	return false
}
