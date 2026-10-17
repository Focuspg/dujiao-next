package contract

import "time"

// UpstreamPreflightRequest 发往上游的预检请求；产品、地区、币种全部来自后台规格
type UpstreamPreflightRequest struct {
	Product         string
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
