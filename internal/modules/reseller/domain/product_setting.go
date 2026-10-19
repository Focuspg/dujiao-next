package domain

import (
	"time"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	"github.com/dujiao-next/internal/shared/money"
)

// ProductSetting 分销商商品配置。
type ProductSetting struct {
	ID                uint         `gorm:"primarykey" json:"id"`
	ResellerID        uint         `gorm:"not null;index;uniqueIndex:idx_reseller_product_setting_scope,priority:1,where:deleted_at IS NULL" json:"reseller_id"`
	ProductID         uint         `gorm:"not null;index;uniqueIndex:idx_reseller_product_setting_scope,priority:2,where:deleted_at IS NULL" json:"product_id"`
	SKUID             uint         `gorm:"column:sku_id;not null;default:0;index;uniqueIndex:idx_reseller_product_setting_scope,priority:3,where:deleted_at IS NULL" json:"sku_id"`
	IsListed          bool         `gorm:"not null;default:true" json:"is_listed"`
	PricingMode       string       `gorm:"type:varchar(32);not null;default:'inherit'" json:"pricing_mode"`
	MarkupPercent     money.Amount `gorm:"type:decimal(10,2);not null;default:0" json:"markup_percent"`
	FixedMarkupAmount money.Amount `gorm:"type:decimal(20,2);not null;default:0" json:"fixed_markup_amount"`
	FixedPriceAmount  money.Amount `gorm:"type:decimal(20,2);not null;default:0" json:"fixed_price_amount"`
	SortOrder         int          `gorm:"not null;default:0;index" json:"sort_order"`
	CreatedAt         time.Time    `gorm:"index" json:"created_at"`
	UpdatedAt         time.Time    `gorm:"index" json:"updated_at"`
	DeletedAt         *time.Time   `gorm:"index" json:"-"`

	Product *productdomain.Product `gorm:"foreignKey:ProductID" json:"product,omitempty"`
	Profile *Profile               `gorm:"foreignKey:ResellerID" json:"profile,omitempty"`
}

func (ProductSetting) TableName() string { return "reseller_product_settings" }

// DefaultAgentPriceResellerID 统一代理价的作用域：reseller_id 为 0 的行对所有分销商生效，
// 分销商单独设置的代理价优先于统一代理价。
const DefaultAgentPriceResellerID uint = 0

// AgentPrice 后台给分销商单独设置的 SKU 拿货价（代理价）；ResellerID 为 0 时是统一代理价。
// 未设置时分销商按零售价拿货；分销利润 = 分站成交价 − 拿货价。
type AgentPrice struct {
	ID          uint         `gorm:"primarykey" json:"id"`
	ResellerID  uint         `gorm:"not null;uniqueIndex:idx_reseller_agent_price_scope,priority:1" json:"reseller_id"`
	ProductID   uint         `gorm:"not null;index" json:"product_id"`
	SKUID       uint         `gorm:"column:sku_id;not null;uniqueIndex:idx_reseller_agent_price_scope,priority:2" json:"sku_id"`
	PriceAmount money.Amount `gorm:"type:decimal(20,2);not null;default:0" json:"price_amount"`
	CreatedAt   time.Time    `gorm:"index" json:"created_at"`
	UpdatedAt   time.Time    `gorm:"index" json:"updated_at"`
}

func (AgentPrice) TableName() string { return "reseller_agent_prices" }
