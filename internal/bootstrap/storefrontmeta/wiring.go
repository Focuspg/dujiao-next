package storefrontmeta

import (
	"github.com/dujiao-next/internal/app/container"
	productapplication "github.com/dujiao-next/internal/modules/catalog/product/application"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	"github.com/dujiao-next/internal/web"
)

// productAdapter 组合商品查询服务与分销站隐藏商品列表，与公开商品详情接口一致。
type productAdapter struct {
	products *productapplication.Service
	hidden   productapplication.HiddenProductRepository
}

func (a productAdapter) GetPublicBySlugForTenant(tenant resellercontract.TenantContext, slug string) (*productdomain.Product, error) {
	return a.products.GetPublicBySlugForTenant(tenant, a.hidden, slug)
}

// NewFromContainer 用容器中的设置、分销域名解析、分销站点与商品服务装配入口页元信息提供者。
func NewFromContainer(c *container.Container) web.PageMetaProvider {
	if c == nil || c.SettingService == nil {
		return nil
	}
	deps := Dependencies{Settings: c.SettingService}
	if c.Config != nil {
		deps.Reseller = c.Config.Reseller
	}
	if c.ResellerDomainResolver != nil {
		deps.Tenants = c.ResellerDomainResolver
	}
	if c.ResellerStore != nil {
		deps.Resellers = c.ResellerStore
	}
	if c.ProductReadService != nil {
		adapter := productAdapter{products: c.ProductReadService}
		if c.ResellerStore != nil {
			adapter.hidden = c.ResellerStore
		}
		deps.Products = adapter
	}
	if c.ResellerPricingResolver != nil {
		deps.Pricer = c.ResellerPricingResolver
	}
	return New(deps).PageMeta
}
