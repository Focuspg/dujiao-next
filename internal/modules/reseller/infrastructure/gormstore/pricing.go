package gormstore

import (
	"errors"
	"time"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	resellerdomain "github.com/dujiao-next/internal/modules/reseller/domain"

	"gorm.io/gorm"
)

// ListProductSettingsForPricing 批量获取分销定价所需的商品级与 SKU 级配置。
func (r *Store) ListProductSettingsForPricing(resellerID uint, productIDs []uint, skuIDs []uint) ([]resellerdomain.ProductSetting, error) {
	if resellerID == 0 || len(productIDs) == 0 {
		return []resellerdomain.ProductSetting{}, nil
	}
	productIDs = uniqueUintSlice(productIDs)
	skuIDs = uniqueUintSlice(skuIDs)

	query := r.db.Where("reseller_id = ? AND product_id IN ? AND deleted_at IS NULL", resellerID, productIDs)
	if len(skuIDs) > 0 {
		query = query.Where("(sku_id = 0 OR sku_id IN ?)", skuIDs)
	} else {
		query = query.Where("sku_id = 0")
	}

	var rows []resellerdomain.ProductSetting
	if err := query.Order("product_id ASC, sku_id ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListHiddenProductIDs 返回分销前台列表应在查询层排除的商品 ID。
func (r *Store) ListHiddenProductIDs(resellerID uint) ([]uint, error) {
	if resellerID == 0 {
		return []uint{}, nil
	}

	hidden := map[uint]struct{}{}
	var productHidden []uint
	if err := r.db.Model(&resellerdomain.ProductSetting{}).
		Where("reseller_id = ? AND sku_id = 0 AND is_listed = ? AND deleted_at IS NULL", resellerID, false).
		Pluck("product_id", &productHidden).Error; err != nil {
		return nil, err
	}
	for _, id := range productHidden {
		if id != 0 {
			hidden[id] = struct{}{}
		}
	}

	var skuHidden []uint
	if err := r.db.Model(&productdomain.ProductSKU{}).
		Select("product_skus.product_id").
		Joins(
			"JOIN reseller_product_settings rps ON rps.product_id = product_skus.product_id AND rps.sku_id = product_skus.id AND rps.reseller_id = ? AND rps.is_listed = ? AND rps.deleted_at IS NULL",
			resellerID,
			false,
		).
		Where("product_skus.is_active = ? AND product_skus.deleted_at IS NULL", true).
		Group("product_skus.product_id").
		Having("COUNT(product_skus.id) = (SELECT COUNT(1) FROM product_skus ps2 WHERE ps2.product_id = product_skus.product_id AND ps2.is_active = ? AND ps2.deleted_at IS NULL)", true).
		Pluck("product_skus.product_id", &skuHidden).Error; err != nil {
		return nil, err
	}
	for _, id := range skuHidden {
		if id != 0 {
			hidden[id] = struct{}{}
		}
	}

	ids := make([]uint, 0, len(hidden))
	for id := range hidden {
		ids = append(ids, id)
	}
	return ids, nil
}

func uniqueUintSlice(values []uint) []uint {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

// ListAgentPrices 获取分销商的 SKU 拿货价；resellerID 为 0 时返回统一代理价；skuIDs 为 nil 时返回该作用域的全部设置。
func (r *Store) ListAgentPrices(resellerID uint, skuIDs []uint) ([]resellerdomain.AgentPrice, error) {
	return r.listAgentPrices([]uint{resellerID}, skuIDs)
}

func (r *Store) listAgentPrices(resellerIDs []uint, skuIDs []uint) ([]resellerdomain.AgentPrice, error) {
	query := r.db.Where("reseller_id IN ?", resellerIDs)
	if skuIDs != nil {
		skuIDs = uniqueUintSlice(skuIDs)
		if len(skuIDs) == 0 {
			return []resellerdomain.AgentPrice{}, nil
		}
		query = query.Where("sku_id IN ?", skuIDs)
	}
	var rows []resellerdomain.AgentPrice
	if err := query.Order("product_id ASC, sku_id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ListAgentPricesForPricing 下单与前台展示定价时批量读取拿货价：同时返回统一代理价和该分销商的单独代理价，
// 由 IndexAgentPrices 按“单独价优先”合并。
func (r *Store) ListAgentPricesForPricing(resellerID uint, skuIDs []uint) ([]resellerdomain.AgentPrice, error) {
	if resellerID == 0 {
		return []resellerdomain.AgentPrice{}, nil
	}
	if skuIDs == nil {
		skuIDs = []uint{}
	}
	return r.listAgentPrices([]uint{resellerdomain.DefaultAgentPriceResellerID, resellerID}, skuIDs)
}

// SaveAgentPrice 新增或更新某分销商某 SKU 的拿货价；ResellerID 为 0 时保存统一代理价。
func (r *Store) SaveAgentPrice(price resellerdomain.AgentPrice) error {
	if price.ProductID == 0 || price.SKUID == 0 {
		return errors.New("reseller agent price scope is invalid")
	}
	var existing resellerdomain.AgentPrice
	err := r.db.Where("reseller_id = ? AND sku_id = ?", price.ResellerID, price.SKUID).First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return r.db.Create(&price).Error
	}
	if err != nil {
		return err
	}
	return r.db.Model(&resellerdomain.AgentPrice{}).
		Where("id = ?", existing.ID).
		Updates(map[string]interface{}{
			"product_id":   price.ProductID,
			"price_amount": price.PriceAmount,
			"updated_at":   time.Now(),
		}).Error
}

// DeleteAgentPrice 清除某分销商某 SKU 的拿货价；resellerID 为 0 时清除统一代理价。
func (r *Store) DeleteAgentPrice(resellerID, skuID uint) error {
	if skuID == 0 {
		return nil
	}
	return r.db.Where("reseller_id = ? AND sku_id = ?", resellerID, skuID).Delete(&resellerdomain.AgentPrice{}).Error
}
