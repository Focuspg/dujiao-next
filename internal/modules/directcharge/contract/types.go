package contract

import (
	"time"

	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// UpstreamPreflightRequest 发往上游的预检请求；产品、地区、币种全部来自后台规格
type UpstreamPreflightRequest struct {
	Product         string
	Plan            string // 目标套餐；预检不返回该档实时报价时（如 Codex 点数）用于补齐价格表报价
	PaymentCountry  string
	PaymentCurrency string
	Mode            string
	Credential      string
}

// UpstreamQuote 上游对某个套餐的报价
type UpstreamQuote struct {
	Currency    string
	AmountMinor int64
	Exponent    int
}

// UpstreamPreflight 上游预检结果
type UpstreamPreflight struct {
	AccountID      string
	CurrentPlan    string
	PreflightToken string
	TokenExpiresAt *time.Time
	Quotes         map[string]UpstreamQuote
	QuoteError     string
}

// OrderRequest 创建代充本地订单
type OrderRequest struct {
	UserID    uint
	ProductID uint
	SKUID     uint
	ClientIP  string
}

// OrderRef 已创建的本地订单
type OrderRef struct {
	ParentID  uint
	ParentNo  string
	ChildID   uint
	ChildNo   string
	ExpiresAt *time.Time
}

// 上游订单结论（归一化）
const (
	UpstreamOutcomePending   = "pending"
	UpstreamOutcomeSucceeded = "succeeded"
	UpstreamOutcomeFailed    = "failed"
	UpstreamOutcomeCancelled = "cancelled"
	// UpstreamOutcomeUnknown 不认识的上游状态：转人工核验，不能当成失败
	UpstreamOutcomeUnknown = "unknown"
)

// UpstreamOrderRequest 付款后提交给上游的直充请求。业务参数全部来自规格与任务快照。
type UpstreamOrderRequest struct {
	Product         string
	Plan            string
	CardID          int64
	PaymentCountry  string
	PaymentCurrency string
	Mode            string
	Credential      string
	PreflightToken  string
	// ClientRequestID 固定的尝试标识，同时用作幂等键；重放必须完全相同
	ClientRequestID string
	PricingVersion  int64
}

// UpstreamOrder 上游直充订单的公开状态
type UpstreamOrder struct {
	ID                 int64
	ClientRequestID    string
	Status             string
	Stage              string
	Message            string
	Outcome            string
	FailedBeforeCharge bool
	Currency           string
	QuotedAmountMinor  int64
	FinalAmountMinor   int64
	ServiceFeeMinor    int64
	ServiceFeeStatus   string
	FundingHoldStatus  string
}

// OpenedCredential 解密后的凭据，只在一次处理的内存中使用。String 脱敏，防止误打日志。
type OpenedCredential struct {
	Mode           string
	Credential     string
	PreflightToken string
}

func (c OpenedCredential) String() string   { return "OpenedCredential{mode=" + c.Mode + ", redacted}" }
func (c OpenedCredential) GoString() string { return c.String() }

// UpstreamEvent 已验签的上游回调。回调只用来唤醒查单，不直接推进状态。
type UpstreamEvent struct {
	DedupKey        string
	Type            string
	BodyHash        string
	UpstreamOrderID int64
	ClientRequestID string
	Status          string
	// Relevant 是否为直充生命周期事件；其它事件只记录不处理
	Relevant bool
}

// OrphanOrder 已付款但没有充值任务的代充订单（下单时任务创建失败等）
type OrphanOrder struct {
	OrderID uint
	OrderNo string
	Status  string
}

// TaskChange 一次比较版本后写入的任务变化。ToStatus 可与 FromStatus 相同（只更新字段）。
// 任务进入终态时会在同一事务内释放账号占用并销毁凭据。
type TaskChange struct {
	TaskID      uint
	FromStatus  string
	FromVersion int
	ToStatus    string
	Reason      string
	Actor       string
	Now         time.Time
	// Fields 额外更新的列（不得包含 status/version）
	Fields map[string]interface{}
	// NewAttempt 非空时在同一事务内创建，并设为任务的当前尝试
	NewAttempt *directchargedomain.Attempt
	// Attempt 非空时在同一事务内保存
	Attempt           *directchargedomain.Attempt
	DestroyCredential bool
	// NewCredential 非空时在同一事务内销毁任务旧凭据、保存新凭据（绑定到任务）与 NewPreflight，并改为任务当前凭据
	NewCredential *directchargedomain.Credential
	NewPreflight  *directchargedomain.Preflight
}

// TaskFilter 后台任务列表筛选
type TaskFilter struct {
	Status       string
	OrderNo      string // 父订单号或子订单号
	RefundStatus string
	Page         int
	PageSize     int
}

// 告警级别
const (
	AlertWarning  = "warning"
	AlertCritical = "critical"
)

// Alert 给站长的运营告警（邮件）。Message 只写原因类别，不含凭据或上游原文。
type Alert struct {
	Type    string
	Level   string
	TaskID  uint
	OrderNo string
	Message string
}
