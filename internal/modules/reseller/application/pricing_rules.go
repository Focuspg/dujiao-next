package application

import (
	"strings"

	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"

	resellerdomain "github.com/dujiao-next/internal/modules/reseller/domain"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"

	"github.com/shopspring/decimal"
)

const (
	RuleSourceSKU     = "sku"
	RuleSourceProduct = "product"
	RuleSourceProfile = "profile"
	RuleSourceInherit = "inherit"
)

// PricingRule 描述分销改价规则解析结果。
type PricingRule struct {
	Mode              string
	Source            string
	SettingID         *uint
	MarkupPercent     decimal.Decimal
	FixedMarkupAmount decimal.Decimal
	FixedPriceAmount  decimal.Decimal
}

// ResolveUnitAmount 按 SKU → 商品 → 资料默认加价 → 继承底价的优先级解析成交单价。
func ResolveUnitAmount(profile *resellerdomain.Profile, productSetting *resellerdomain.ProductSetting, skuSetting *resellerdomain.ProductSetting, baseUnit decimal.Decimal) (decimal.Decimal, PricingRule, error) {
	if skuSetting != nil && strings.TrimSpace(skuSetting.PricingMode) != resellerdomain.PricingModeInherit {
		return ApplyPricingRule(*skuSetting, RuleSourceSKU, baseUnit)
	}
	if productSetting != nil && strings.TrimSpace(productSetting.PricingMode) != resellerdomain.PricingModeInherit {
		return ApplyPricingRule(*productSetting, RuleSourceProduct, baseUnit)
	}
	if profile != nil && profile.DefaultMarkupPercent.Decimal.GreaterThan(decimal.Zero) {
		rule := PricingRule{
			Mode:          resellerdomain.PricingModeMarkupPercent,
			Source:        RuleSourceProfile,
			MarkupPercent: profile.DefaultMarkupPercent.Decimal.Round(2),
		}
		return ApplyMarkupPercent(baseUnit, rule.MarkupPercent), rule, nil
	}
	return baseUnit.Round(2), PricingRule{Mode: resellerdomain.PricingModeInherit, Source: RuleSourceInherit}, nil
}

// ApplyPricingRule 按单条分销配置计算单价。
func ApplyPricingRule(setting resellerdomain.ProductSetting, source string, baseUnit decimal.Decimal) (decimal.Decimal, PricingRule, error) {
	settingID := setting.ID
	rule := PricingRule{
		Mode:              strings.TrimSpace(setting.PricingMode),
		Source:            source,
		SettingID:         &settingID,
		MarkupPercent:     setting.MarkupPercent.Decimal.Round(2),
		FixedMarkupAmount: setting.FixedMarkupAmount.Decimal.Round(2),
		FixedPriceAmount:  setting.FixedPriceAmount.Decimal.Round(2),
	}
	switch rule.Mode {
	case resellerdomain.PricingModeMarkupPercent:
		return ApplyMarkupPercent(baseUnit, rule.MarkupPercent), rule, nil
	case resellerdomain.PricingModeFixedMarkup:
		return baseUnit.Add(rule.FixedMarkupAmount).Round(2), rule, nil
	case resellerdomain.PricingModeFixedPrice:
		return rule.FixedPriceAmount.Round(2), rule, nil
	case resellerdomain.PricingModeInherit:
		return baseUnit.Round(2), rule, nil
	default:
		return decimal.Zero, rule, resellercontract.ErrPricingModeInvalid
	}
}

// ApplyMarkupPercent 按百分比加价。
func ApplyMarkupPercent(baseUnit decimal.Decimal, percent decimal.Decimal) decimal.Decimal {
	return baseUnit.Mul(decimal.NewFromInt(100).Add(percent)).Div(decimal.NewFromInt(100)).Round(2)
}

// ValidateUnitAmount 校验分销单价不低于底价/成本价，且不超过最大加价比例。
func ValidateUnitAmount(profile *resellerdomain.Profile, sku *productdomain.ProductSKU, baseUnit decimal.Decimal, resellerUnit decimal.Decimal) error {
	baseUnit = baseUnit.Round(2)
	resellerUnit = resellerUnit.Round(2)
	if resellerUnit.LessThanOrEqual(decimal.Zero) || resellerUnit.LessThan(baseUnit) {
		return resellercontract.ErrPriceBelowBase
	}
	if sku != nil && sku.CostPriceAmount.Decimal.GreaterThan(decimal.Zero) && resellerUnit.LessThan(sku.CostPriceAmount.Decimal.Round(2)) {
		return resellercontract.ErrPriceBelowBase
	}
	if profile != nil && profile.MaxMarkupPercent.Decimal.GreaterThan(decimal.Zero) && baseUnit.GreaterThan(decimal.Zero) {
		implicit := resellerUnit.Sub(baseUnit).Div(baseUnit).Mul(decimal.NewFromInt(100)).Round(4)
		if implicit.GreaterThan(profile.MaxMarkupPercent.Decimal.Round(4)) {
			return resellercontract.ErrMarkupExceeded
		}
	}
	return nil
}

// IndexAgentPrices 按 SKU ID 索引分销商拿货价。
func IndexAgentPrices(rows []resellerdomain.AgentPrice) map[uint]decimal.Decimal {
	out := make(map[uint]decimal.Decimal, len(rows))
	for _, row := range rows {
		if row.SKUID == 0 {
			continue
		}
		out[row.SKUID] = row.PriceAmount.Decimal.Round(2)
	}
	return out
}

// ResolveCostUnit 返回分销商的拿货单价：设置了大于 0 且不高于零售价的代理价时取代理价，否则按零售价。
// 零售价后来被调到代理价以下时按零售价计，保证利润不为负。
func ResolveCostUnit(baseUnit decimal.Decimal, agentPrices map[uint]decimal.Decimal, skuID uint) decimal.Decimal {
	baseUnit = baseUnit.Round(2)
	agent, ok := agentPrices[skuID]
	if !ok || skuID == 0 || agent.LessThanOrEqual(decimal.Zero) || agent.GreaterThan(baseUnit) {
		return baseUnit
	}
	return agent.Round(2)
}
