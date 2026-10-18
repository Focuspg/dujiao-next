package container

import (
	"context"
	"fmt"
	"strings"

	"github.com/dujiao-next/internal/config"
	fulfillmentapp "github.com/dujiao-next/internal/modules/fulfillment/application"
	reseller "github.com/dujiao-next/internal/modules/reseller/application"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
)

// wireServiceDependencies 收口构造后才能建立的双向或延迟依赖。
func (c *Container) wireServiceDependencies() {
	c.UserAuthService.SetMemberLevelService(c.MemberLevelService)
	c.OrderRefundService.SetResellerAccounting(c.ResellerAccountingLedger)
	c.PaymentService.SetMemberLevelService(c.MemberLevelService)
	c.PaymentService.SetProcurementService(c.ProcurementOrderService)
	c.PaymentService.SetDownstreamCallbackService(c.DownstreamCallbackService)
	c.FulfillmentService.SetDownstreamCallbackService(c.DownstreamCallbackService)
	c.OrderService.SetResellerActivationChecker(c.ResellerManagementService)
	c.PaymentService.SetResellerActivationFulfiller(&resellerActivationFulfiller{
		management:  c.ResellerManagementService,
		fulfillment: c.FulfillmentService,
		cfg:         c.Config.Reseller,
	})
}

// resellerActivationFulfiller 串联"开通代理 → 交付订单"：先开通分销商并分配二级域名，再把分站地址作为交付内容。
type resellerActivationFulfiller struct {
	management  *reseller.ManagementService
	fulfillment *fulfillmentapp.Service
	cfg         config.ResellerConfig
}

func (f *resellerActivationFulfiller) FulfillResellerActivation(orderID, userID uint, subdomain string) error {
	domain, err := f.management.ActivatePaidReseller(context.Background(), userID, subdomain)
	if err != nil {
		return err
	}
	if domain == nil {
		return resellercontract.ErrDomainInvalid
	}
	_, err = f.fulfillment.CreateResellerActivation(orderID, resellerActivationPayload(domain.Domain, f.cfg.MainHosts))
	return err
}

func resellerActivationPayload(storefrontHost string, mainHosts []string) string {
	lines := []string{
		"代理已开通 / Reseller activated",
		"分站地址 / Storefront: https://" + storefrontHost,
	}
	for _, host := range mainHosts {
		if host = resellercontract.NormalizeHost(host); host != "" {
			lines = append(lines, fmt.Sprintf("代理控制台 / Reseller console: https://%s/reseller", host))
			break
		}
	}
	lines = append(lines, "登录主站后可在代理控制台设置商品售价、分站名称和提现 / Sign in on the main site to manage prices, storefront and withdrawals.")
	return strings.Join(lines, "\n")
}
