// Package domain 定义账号代充（Session 直充）上下文的实体与状态规则。
package domain

import (
	"time"

	"github.com/dujiao-next/internal/shared/jsonslice"
)

// SupplierZovoCard 当前唯一支持的供应商
const SupplierZovoCard = "zovocard"

// Spec 充值规格：把一个销售 SKU 映射到上游的产品与套餐。
// 实际发往上游的 product/plan/地区/卡全部来自这里，不采信前端字段。
type Spec struct {
	ID              uint              `gorm:"primarykey" json:"id"`
	ProductID       uint              `gorm:"index;not null" json:"product_id"`
	SKUID           uint              `gorm:"column:sku_id;uniqueIndex;not null" json:"sku_id"`
	Supplier        string            `gorm:"type:varchar(32);not null;default:'zovocard'" json:"supplier"`
	UpstreamProduct string            `gorm:"type:varchar(16);not null" json:"upstream_product"`
	UpstreamPlan    string            `gorm:"type:varchar(64);not null" json:"upstream_plan"`
	PaymentCountry  string            `gorm:"type:varchar(8);not null;default:''" json:"payment_country"`
	PaymentCurrency string            `gorm:"type:varchar(8);not null;default:''" json:"payment_currency"`
	CredentialModes jsonslice.Strings `gorm:"type:json" json:"credential_modes"`
	// CardID 用于支付的上游卡 ID（只用 card_id，不用 raw_card）
	CardID int64 `gorm:"not null;default:0" json:"card_id"`
	// MaxCostUsdMinor 单笔上游成本上限（订阅实付折美元 + 服务费，美分）；0 表示不限制
	MaxCostUsdMinor int64 `gorm:"not null;default:0" json:"max_cost_usd_minor"`
	// MaxQuoteMinor 上游报价上限（报价币种最小单位）；0 表示不限制
	MaxQuoteMinor int64 `gorm:"not null;default:0" json:"max_quote_minor"`
	// ConcurrencyLimit 同时处于提交/处理中的任务上限
	ConcurrencyLimit int       `gorm:"not null;default:1" json:"concurrency_limit"`
	Enabled          bool      `gorm:"not null;default:false;index" json:"enabled"`
	Version          int       `gorm:"not null;default:1" json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (Spec) TableName() string { return "directcharge_specs" }

// AllowsCredentialMode 规格是否接受该凭据模式；未配置时只接受 session
func (s *Spec) AllowsCredentialMode(mode string) bool {
	if s == nil {
		return false
	}
	if len(s.CredentialModes) == 0 {
		return mode == CredentialModeSession
	}
	for _, m := range s.CredentialModes {
		if m == mode {
			return true
		}
	}
	return false
}

// 凭据模式（与上游保持一致；本站不收集邮箱密码）
const (
	CredentialModeSession     = "session"
	CredentialModeAccessToken = "access_token"
)
