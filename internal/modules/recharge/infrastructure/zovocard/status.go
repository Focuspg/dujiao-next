package zovocard

import "strings"

// Outcome 上游订单状态归一化后的结论
type Outcome string

const (
	// OutcomePending 仍在处理中（含排队、等卡、注资、支付中）
	OutcomePending Outcome = "pending"
	// OutcomeSucceeded 已开通
	OutcomeSucceeded Outcome = "succeeded"
	// OutcomeFailed 上游确认失败
	OutcomeFailed Outcome = "failed"
	// OutcomeCancelled 上游确认已取消
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeUnrecognized 不认识的状态：进入人工核验，不能默认当成失败
	OutcomeUnrecognized Outcome = "unknown"
)

// 已知的上游订单状态
const (
	StatusQueued          = "queued"
	StatusAwaitingCard    = "awaiting_card"
	StatusFundingPending  = "funding_pending"
	StatusProcessing      = "processing"
	StatusCompleted       = "completed"
	StatusDeclined        = "declined"
	StatusFailedPrecharge = "failed_precharge"
	StatusFailed          = "failed"
	StatusCancelled       = "cancelled"
)

var pendingStatuses = map[string]bool{
	StatusQueued:         true,
	StatusAwaitingCard:   true,
	StatusFundingPending: true,
	StatusProcessing:     true,
	"pending":            true,
	"submitted":          true,
	"running":            true,
	"paying":             true,
}

// OutcomeOf 归一化上游状态
func OutcomeOf(status string) Outcome {
	s := strings.ToLower(strings.TrimSpace(status))
	switch {
	case s == StatusCompleted:
		return OutcomeSucceeded
	case s == StatusDeclined || s == StatusFailedPrecharge || s == StatusFailed:
		return OutcomeFailed
	case s == StatusCancelled:
		return OutcomeCancelled
	case pendingStatuses[s]:
		return OutcomePending
	}
	return OutcomeUnrecognized
}

// Outcome 订单当前结论
func (o Order) Outcome() Outcome { return OutcomeOf(o.Status) }

// FailedBeforeCharge 是否为确定未扣订阅款的失败（failed_precharge）或取消
func (o Order) FailedBeforeCharge() bool {
	s := strings.ToLower(strings.TrimSpace(o.Status))
	return s == StatusFailedPrecharge || s == StatusCancelled
}

// HasChargeEvidence 是否有上游实付金额，失败单带实付时需要人工核验
func (o Order) HasChargeEvidence() bool { return o.FinalAmountMinor > 0 }

// Cancellable 上游是否允许取消（尚未开始支付）
func (o Order) Cancellable() bool {
	switch strings.ToLower(strings.TrimSpace(o.Status)) {
	case StatusQueued, StatusAwaitingCard, StatusFundingPending:
		return true
	}
	return false
}
