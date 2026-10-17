package domain

import (
	"fmt"
	"time"
)

// 上游尝试状态
const (
	AttemptPrepared  = "prepared"  // 已落库，尚未发出
	AttemptSubmitted = "submitted" // 上游已受理（拿到上游订单 ID）
	AttemptUnknown   = "unknown"   // 已发出但结果不明
	AttemptSucceeded = "succeeded"
	AttemptFailed    = "failed"
	AttemptCancelled = "cancelled"
	AttemptRejected  = "rejected" // 上游明确拒绝建单（未创建上游订单）
)

// Attempt 一次上游直充尝试。client_request_id 与幂等键在落库时固定，之后任何重试都沿用。
type Attempt struct {
	ID              uint   `gorm:"primarykey" json:"id"`
	TaskID          uint   `gorm:"uniqueIndex:idx_recharge_attempt_task_no;not null" json:"task_id"`
	AttemptNo       int    `gorm:"uniqueIndex:idx_recharge_attempt_task_no;not null" json:"attempt_no"`
	Supplier        string `gorm:"type:varchar(32);not null" json:"supplier"`
	ClientRequestID string `gorm:"type:varchar(80);uniqueIndex;not null" json:"client_request_id"`
	IdempotencyKey  string `gorm:"type:varchar(80);not null" json:"-"`
	// RequestDigest 业务参数摘要（不含凭据），用于确认重放的是同一语义请求
	RequestDigest     string     `gorm:"type:varchar(64);not null" json:"-"`
	CardID            int64      `gorm:"not null;default:0" json:"card_id"`
	Status            string     `gorm:"type:varchar(32);index;not null" json:"status"`
	UpstreamOrderID   int64      `gorm:"index;not null;default:0" json:"upstream_order_id"`
	UpstreamStatus    string     `gorm:"type:varchar(32);not null;default:''" json:"upstream_status"`
	UpstreamStage     string     `gorm:"type:varchar(64);not null;default:''" json:"upstream_stage"`
	UpstreamMessage   string     `gorm:"type:varchar(500);not null;default:''" json:"upstream_message"`
	Currency          string     `gorm:"type:varchar(8);not null;default:''" json:"currency"`
	QuotedAmountMinor int64      `gorm:"not null;default:0" json:"quoted_amount_minor"`
	FinalAmountMinor  int64      `gorm:"not null;default:0" json:"final_amount_minor"`
	ServiceFeeMinor   int64      `gorm:"not null;default:0" json:"service_fee_minor"`
	ServiceFeeStatus  string     `gorm:"type:varchar(32);not null;default:''" json:"service_fee_status"`
	FundingHoldStatus string     `gorm:"type:varchar(32);not null;default:''" json:"funding_hold_status"`
	Reason            string     `gorm:"type:varchar(500);not null;default:''" json:"reason"` // 新建该尝试的理由
	ErrorKind         string     `gorm:"type:varchar(32);not null;default:''" json:"error_kind"`
	ErrorMessage      string     `gorm:"type:varchar(500);not null;default:''" json:"error_message"`
	SentAt            *time.Time `json:"sent_at,omitempty"`
	LastCheckedAt     *time.Time `json:"last_checked_at,omitempty"`
	CompletedAt       *time.Time `json:"completed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

func (Attempt) TableName() string { return "recharge_attempts" }

// IsOpen 尝试是否仍可能在上游产生扣款（不能并行新建尝试）
func (a *Attempt) IsOpen() bool {
	if a == nil {
		return false
	}
	switch a.Status {
	case AttemptPrepared, AttemptSubmitted, AttemptUnknown:
		return true
	}
	return false
}

// BuildClientRequestID 生成固定的上游请求号：rc-<任务ID>-<尝试序号>-<站点随机前缀>。
// 站点前缀避免测试库与生产库的任务 ID 重叠时在同一上游账号下撞号。
func BuildClientRequestID(sitePrefix string, taskID uint, attemptNo int) string {
	if sitePrefix == "" {
		sitePrefix = "s"
	}
	return fmt.Sprintf("rc-%s-%d-%d", sitePrefix, taskID, attemptNo)
}
