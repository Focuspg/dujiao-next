package domain

import "time"

// Credential 凭据保险箱记录。只保存密文，明文只在请求处理内存中短暂存在。
type Credential struct {
	ID          uint       `gorm:"primarykey"`
	OwnerUserID uint       `gorm:"index;not null"`
	TaskID      *uint      `gorm:"index"`
	Mode        string     `gorm:"type:varchar(16);not null"`
	Ciphertext  string     `gorm:"type:text;not null"`
	KeyVersion  int        `gorm:"not null;default:1"`
	ExpiresAt   time.Time  `gorm:"index;not null"`
	DestroyedAt *time.Time `gorm:"index"`
	CreatedAt   time.Time
}

func (Credential) TableName() string { return "directcharge_credentials" }

// Usable 凭据是否仍可解密使用
func (c *Credential) Usable(now time.Time) bool {
	return c != nil && c.DestroyedAt == nil && c.Ciphertext != "" && now.Before(c.ExpiresAt)
}

// 预检记录状态
const (
	PreflightOK       = "ok"
	PreflightFailed   = "failed"
	PreflightConsumed = "consumed" // 已用于创建订单
)

// Preflight 一次账号预检的结果。预检令牌是敏感信息，与凭据一起加密存放在 Credential 中。
type Preflight struct {
	ID                 uint       `gorm:"primarykey" json:"id"`
	Ref                string     `gorm:"type:varchar(64);uniqueIndex;not null" json:"ref"` // 返回给前端的随机引用
	UserID             uint       `gorm:"index;not null" json:"-"`
	SpecID             uint       `gorm:"index;not null" json:"-"`
	SpecVersion        int        `gorm:"not null;default:0" json:"-"`
	SKUID              uint       `gorm:"column:sku_id;not null" json:"sku_id"`
	Status             string     `gorm:"type:varchar(16);index;not null" json:"status"`
	AccountFingerprint string     `gorm:"type:varchar(64);index;not null;default:''" json:"-"`
	AccountMasked      string     `gorm:"type:varchar(128);not null;default:''" json:"account_masked"`
	CurrentPlan        string     `gorm:"type:varchar(64);not null;default:''" json:"current_plan"`
	QuoteCurrency      string     `gorm:"type:varchar(8);not null;default:''" json:"quote_currency"`
	QuoteAmountMinor   int64      `gorm:"not null;default:0" json:"quote_amount_minor"`
	QuoteExponent      int        `gorm:"not null;default:2" json:"quote_exponent"`
	PricingVersion     int64      `gorm:"not null;default:0" json:"-"`
	CredentialID       *uint      `json:"-"`
	TokenExpiresAt     *time.Time `json:"token_expires_at,omitempty"`
	ErrorKind          string     `gorm:"type:varchar(32);not null;default:''" json:"error_kind"`
	ExpiresAt          time.Time  `gorm:"index;not null" json:"expires_at"` // 该预检结果可用于下单的截止时间
	ConsumedAt         *time.Time `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
}

func (Preflight) TableName() string { return "directcharge_preflights" }

// 回调收件箱处理状态
const (
	EventPending   = "pending"
	EventProcessed = "processed"
	EventUnmatched = "unmatched" // 暂未找到对应任务（回调早于建单响应）
	EventIgnored   = "ignored"
)

// WebhookEvent 上游回调收件箱：先落库再快速返回 2xx，由后台按查单结果推进状态。
type WebhookEvent struct {
	ID              uint       `gorm:"primarykey" json:"id"`
	Source          string     `gorm:"type:varchar(32);uniqueIndex:idx_directcharge_event_source_key;not null" json:"source"`
	DedupKey        string     `gorm:"type:varchar(191);uniqueIndex:idx_directcharge_event_source_key;not null" json:"dedup_key"`
	EventType       string     `gorm:"type:varchar(64);not null;default:''" json:"event_type"`
	BodyHash        string     `gorm:"type:varchar(64);not null" json:"body_hash"`
	UpstreamOrderID int64      `gorm:"index;not null;default:0" json:"upstream_order_id"`
	ClientRequestID string     `gorm:"type:varchar(80);index;not null;default:''" json:"client_request_id"`
	UpstreamStatus  string     `gorm:"type:varchar(32);not null;default:''" json:"upstream_status"`
	TaskID          *uint      `gorm:"index" json:"task_id,omitempty"`
	Status          string     `gorm:"type:varchar(16);index;not null" json:"status"`
	Attempts        int        `gorm:"not null;default:0" json:"attempts"`
	ReceivedAt      time.Time  `gorm:"index;not null" json:"received_at"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
}

func (WebhookEvent) TableName() string { return "directcharge_webhook_events" }
