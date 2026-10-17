package admincmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dujiao-next/internal/config"
	"github.com/dujiao-next/internal/modules/recharge/infrastructure/zovocard"
)

// zovocardCheck 联调检查：余额、套餐可售情况，以及可选的一次账号预检。
// 只做只读调用，不创建直充订单。Session 从文件读取，避免出现在命令行历史里。
func zovocardCheck(cfg *config.Config, product, sessionFile string) {
	if cfg == nil {
		fmt.Fprintln(os.Stderr, "config not loaded")
		os.Exit(1)
	}
	zc := cfg.Recharge.ZovoCard
	if product == "" {
		product = zovocard.ProductGPT
	}
	if !zovocard.ValidProduct(product) {
		fmt.Fprintln(os.Stderr, "--product must be one of gpt, claude, grok")
		os.Exit(1)
	}
	client, err := zovocard.New(zovocard.Config{
		BaseURL: zc.BaseURL,
		APIKey:  zc.APIKey,
		AppID:   zc.AppID,
		Timeout: time.Duration(zc.TimeoutSeconds) * time.Second,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "配置无效: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	failed := false
	fmt.Printf("接口地址: %s\n", zc.BaseURL)
	fmt.Printf("回调签名密钥: %s\n", map[bool]string{true: "已配置", false: "未配置（无法校验回调）"}[strings.TrimSpace(zc.WebhookSecret) != ""])

	balance, err := client.GetBalance(ctx)
	if err != nil {
		failed = true
		fmt.Printf("[失败] 余额: %v（类别 %s）\n", err, zovocard.Classify(err))
	} else {
		fmt.Printf("[通过] 余额: 可消费 %s %s（账面 %s，保证金 %s）\n", balance.SpendableBalance, balance.Currency, balance.Balance, balance.AccountReserveAmount)
	}

	plans, err := client.GetPlans(ctx, product)
	if err != nil {
		failed = true
		fmt.Printf("[失败] 套餐: %v（类别 %s）\n", err, zovocard.Classify(err))
	} else {
		fmt.Printf("[通过] 套餐: 价格版本 %d，%s 共 %d 档\n", plans.Version, product, len(plans.Registry))
		for _, item := range plans.Registry {
			if item.Product != "" && item.Product != product {
				continue
			}
			state := "不可售"
			if sp, ok := plans.Sellable(item.Key); ok {
				state = fmt.Sprintf("可售，服务费 %d 美分，参考价 %d %s(最小单位)", sp.ServiceFeeUsdMinor, sp.CheckoutAmountMinor, sp.CheckoutCurrency)
			}
			fmt.Printf("    plan=%-16s %-20s %s\n", item.Key, item.Label, state)
		}
		for _, region := range plans.PaymentRegions {
			fmt.Printf("    付款地区 %s/%s\n", region.Country, region.Currency)
		}
	}

	if sessionFile != "" {
		raw, err := os.ReadFile(sessionFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 session 文件失败: %v\n", err)
			os.Exit(1)
		}
		req := zovocard.PreflightRequest{
			Product:    product,
			Credential: zovocard.Credential{Mode: zovocard.CredentialModeSession, Session: strings.TrimSpace(string(raw))},
		}
		if product != zovocard.ProductGPT {
			req.PaymentCountry, req.PaymentCurrency = "US", "USD"
		}
		pf, err := client.Preflight(ctx, req)
		if err != nil {
			failed = true
			fmt.Printf("[失败] 预检: %v（类别 %s）\n", err, zovocard.Classify(err))
		} else {
			fmt.Printf("[通过] 预检: 账号 %s，当前套餐 %s，地区 %s/%s，预检票 %s\n", maskEmail(pf.Email), pf.CurrentPlan, pf.PaymentCountry, pf.PaymentCurrency,
				map[bool]string{true: "已返回", false: "无"}[pf.PreflightToken != ""])
			if pf.QuoteError != "" {
				fmt.Printf("    报价错误: %s\n", pf.QuoteError)
			}
			for plan, q := range pf.Quotes {
				fmt.Printf("    报价 plan=%-16s %d %s(最小单位, 指数 %d)\n", plan, q.AmountMinor, q.Currency, q.MinorUnitExponent)
			}
		}
	}

	if failed {
		os.Exit(1)
	}
}

func maskEmail(email string) string {
	at := strings.Index(email, "@")
	if at <= 0 {
		if len(email) <= 2 {
			return "***"
		}
		return email[:1] + "***"
	}
	return email[:1] + "***" + email[at:]
}
