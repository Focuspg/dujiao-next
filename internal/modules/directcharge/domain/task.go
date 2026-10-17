package domain

import (
	"errors"
	"time"
)

// 充值任务状态（需求说明书 8.2）
const (
	TaskAwaitingPayment    = "awaiting_payment"
	TaskReady              = "ready"
	TaskPreflighting       = "preflighting"
	TaskWaitingCredential  = "waiting_credential"
	TaskSubmitting         = "submitting"
	TaskSubmitUnknown      = "submit_unknown"
	TaskProcessing         = "processing"
	TaskRequiresAction     = "requires_action"
	TaskReview             = "review"
	TaskSucceeded          = "succeeded"
	TaskFailedConfirmed    = "failed_confirmed"
	TaskCancelledConfirmed = "cancelled_confirmed"
	// TaskClosed 未付款即关闭（订单取消或过期），不进入履约
	TaskClosed = "closed"
)

// ErrTransitionNotAllowed 状态迁移不被允许
var ErrTransitionNotAllowed = errors.New("directcharge: task transition not allowed")

var taskTransitions = map[string][]string{
	TaskAwaitingPayment:   {TaskReady, TaskClosed, TaskReview},
	TaskReady:             {TaskPreflighting, TaskSubmitting, TaskWaitingCredential, TaskReview},
	TaskPreflighting:      {TaskReady, TaskSubmitting, TaskWaitingCredential, TaskReview, TaskFailedConfirmed},
	TaskWaitingCredential: {TaskReady, TaskReview},
	TaskSubmitting:        {TaskProcessing, TaskSubmitUnknown, TaskWaitingCredential, TaskReady, TaskReview, TaskFailedConfirmed},
	TaskSubmitUnknown:     {TaskProcessing, TaskReady, TaskReview, TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed},
	TaskProcessing:        {TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskRequiresAction, TaskReview},
	TaskRequiresAction:    {TaskProcessing, TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskReview},
	TaskReview:            {TaskReady, TaskProcessing, TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskWaitingCredential},
	// 终态：只允许人工核验时转入 review（例如对账发现“已退款却充值成功”）
	TaskSucceeded:          {TaskReview},
	TaskFailedConfirmed:    {TaskReview},
	TaskCancelledConfirmed: {TaskReview},
	TaskClosed:             {TaskReview},
}

// CanTransition 判断任务状态迁移是否合法
func CanTransition(from, to string) bool {
	for _, next := range taskTransitions[from] {
		if next == to {
			return true
		}
	}
	return false
}

// IsTerminal 是否终态
func IsTerminal(status string) bool {
	switch status {
	case TaskSucceeded, TaskFailedConfirmed, TaskCancelledConfirmed, TaskClosed:
		return true
	}
	return false
}

// HoldsAccountLock 该状态是否占用目标账号（同一账号同时只允许一个进行中的充值）
func HoldsAccountLock(status string) bool {
	return !IsTerminal(status)
}

// MayHaveUpstreamAttempt 该状态下是否可能存在已提交或结果不明的上游尝试，此时不能新建尝试
func MayHaveUpstreamAttempt(status string) bool {
	switch status {
	case TaskSubmitting, TaskSubmitUnknown, TaskProcessing, TaskRequiresAction:
		return true
	}
	return false
}

// Task 一个订单对应一个逻辑充值任务
type Task struct {
	ID      uint   `gorm:"primarykey" json:"id"`
	OrderID uint   `gorm:"uniqueIndex;not null" json:"order_id"` // 子订单（实际含充值商品的订单）
	OrderNo string `gorm:"type:varchar(64);index;not null" json:"order_no"`
	// ParentOrderID/ParentOrderNo 用户支付和查看的父订单
	ParentOrderID uint   `gorm:"index;not null;default:0" json:"parent_order_id"`
	ParentOrderNo string `gorm:"type:varchar(64);index;not null;default:''" json:"parent_order_no"`
	UserID        uint   `gorm:"index;not null" json:"user_id"`
	SpecID        uint   `gorm:"index;not null" json:"spec_id"`
	SKUID         uint   `gorm:"column:sku_id;index;not null" json:"sku_id"`
	Status        string `gorm:"type:varchar(32);index;not null" json:"status"`
	Version       int    `gorm:"not null;default:1" json:"version"` // 乐观锁版本，所有状态更新都比较后写入
	Product       string `gorm:"type:varchar(16);not null" json:"product"`
	Plan          string `gorm:"type:varchar(64);not null" json:"plan"`
	// AccountFingerprint 目标账号指纹（服务端密钥 HMAC），用于识别同一账号，不可逆
	AccountFingerprint string `gorm:"type:varchar(64);index;not null" json:"-"`
	AccountMasked      string `gorm:"type:varchar(128);not null;default:''" json:"account_masked"`
	CredentialID       *uint  `gorm:"index" json:"-"`
	PreflightID        *uint  `gorm:"index" json:"-"`
	// 下单时的上游报价快照
	QuoteCurrency    string `gorm:"type:varchar(8);not null;default:''" json:"quote_currency"`
	QuoteAmountMinor int64  `gorm:"not null;default:0" json:"quote_amount_minor"`
	QuoteExponent    int    `gorm:"not null;default:2" json:"quote_exponent"`
	PricingVersion   int64  `gorm:"not null;default:0" json:"pricing_version"`
	SpecVersion      int    `gorm:"not null;default:0" json:"spec_version"`
	CurrentAttemptID *uint  `gorm:"index" json:"current_attempt_id,omitempty"`
	AttemptCount     int    `gorm:"not null;default:0" json:"attempt_count"`
	// RefundLocked 退款锁：置位后任何路径都不得再提交上游
	RefundLocked  bool       `gorm:"not null;default:false;index" json:"refund_locked"`
	NextCheckAt   *time.Time `gorm:"index" json:"next_check_at,omitempty"`
	CheckCount    int        `gorm:"not null;default:0" json:"check_count"`
	LeaseOwner    string     `gorm:"type:varchar(64);not null;default:''" json:"-"`
	LeaseUntil    *time.Time `gorm:"index" json:"-"`
	ErrorKind     string     `gorm:"type:varchar(32);not null;default:''" json:"error_kind"`
	ErrorMessage  string     `gorm:"type:varchar(500);not null;default:''" json:"error_message"`
	ActionMessage string     `gorm:"type:varchar(500);not null;default:''" json:"action_message"`
	// AlertedAt 处理超时已提醒站长的时间，避免每轮重复告警
	AlertedAt   *time.Time `json:"-"`
	PaidAt      *time.Time `json:"paid_at,omitempty"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	CreatedAt   time.Time  `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time  `gorm:"index" json:"updated_at"`
}

func (Task) TableName() string { return "directcharge_tasks" }

// AccountLock 目标账号占用。fingerprint 唯一，保证同一账号跨用户只有一个进行中的任务。
type AccountLock struct {
	ID                 uint      `gorm:"primarykey"`
	AccountFingerprint string    `gorm:"type:varchar(64);uniqueIndex;not null"`
	TaskID             uint      `gorm:"uniqueIndex;not null"`
	CreatedAt          time.Time `gorm:"not null"`
}

func (AccountLock) TableName() string { return "directcharge_account_locks" }

// TaskLog 任务状态变化与重试的审计记录
type TaskLog struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	TaskID     uint      `gorm:"index;not null" json:"task_id"`
	FromStatus string    `gorm:"type:varchar(32);not null;default:''" json:"from_status"`
	ToStatus   string    `gorm:"type:varchar(32);not null;default:''" json:"to_status"`
	Reason     string    `gorm:"type:varchar(500);not null;default:''" json:"reason"`
	Actor      string    `gorm:"type:varchar(64);not null;default:''" json:"actor"` // system / worker / webhook / admin:<id> / user:<id>
	CreatedAt  time.Time `gorm:"index" json:"created_at"`
}

func (TaskLog) TableName() string { return "directcharge_task_logs" }
