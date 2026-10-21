package zovocard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 直充产品白名单。上游对未知 product 会静默当成 gpt 并真实扣款，必须在本地拦截。
const (
	ProductGPT    = "gpt"
	ProductClaude = "claude"
	ProductGrok   = "grok"
)

// 凭据模式。本站不收集邮箱密码，因此不提供 mailbox 模式。
const (
	CredentialModeSession     = "session"
	CredentialModeAccessToken = "access_token"
)

// ErrInvalidProduct product 不在白名单内
var ErrInvalidProduct = errors.New("zovocard: product must be one of gpt, claude, grok")

var idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,80}$`)

// ValidProduct 判断 product 是否为允许的字面量（区分大小写，不做归一化）
func ValidProduct(product string) bool {
	switch product {
	case ProductGPT, ProductClaude, ProductGrok:
		return true
	}
	return false
}

// Credential 目标账号凭据。String/GoString 均脱敏，防止误打日志。
type Credential struct {
	Mode        string `json:"mode"`
	Session     string `json:"session,omitempty"`
	AccessToken string `json:"accessToken,omitempty"`
}

func (c Credential) String() string   { return "Credential{mode=" + c.Mode + ", redacted}" }
func (c Credential) GoString() string { return c.String() }

func (c Credential) validate() error {
	switch c.Mode {
	case CredentialModeSession:
		if strings.TrimSpace(c.Session) == "" {
			return errors.New("zovocard: session is required")
		}
	case CredentialModeAccessToken:
		if strings.TrimSpace(c.AccessToken) == "" {
			return errors.New("zovocard: access token is required")
		}
	default:
		return fmt.Errorf("zovocard: unsupported credential mode %q", c.Mode)
	}
	return nil
}

// Balance 账户余额（美元），接单判断只看 SpendableBalance
type Balance struct {
	Balance              json.Number `json:"balance"`
	SpendableBalance     json.Number `json:"spendable_balance"`
	AccountReserveAmount json.Number `json:"account_reserve_amount"`
	Currency             string      `json:"currency"`
}

// GetBalance GET /balance
func (c *Client) GetBalance(ctx context.Context) (*Balance, error) {
	r, err := c.do(ctx, http.MethodGet, "/balance", nil, nil, "")
	if err != nil {
		return nil, err
	}
	var out Balance
	if err := decodeData(r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Card 付款卡（GET /cards/{id}）。AvailableAmount 为美元可用额度；
// BalanceStale 为 true 表示上游尚未刷新，余额可能不是最新值。
type Card struct {
	ID              int64           `json:"id"`
	AvailableAmount json.Number     `json:"available_amount"`
	BalanceStale    bool            `json:"balance_stale"`
	BalanceAsOf     json.RawMessage `json:"balance_as_of"`
}

// BalanceAsOfText 返回 balance_as_of 的文本形式：字符串去掉引号，null 或缺失返回空字符串
func (c Card) BalanceAsOfText() string {
	raw := strings.TrimSpace(string(c.BalanceAsOf))
	if raw == "" || raw == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(c.BalanceAsOf, &text); err == nil {
		return strings.TrimSpace(text)
	}
	return raw
}

// GetCard GET /cards/{id}
func (c *Client) GetCard(ctx context.Context, id int64) (*Card, error) {
	if id <= 0 {
		return nil, errors.New("zovocard: card id is required")
	}
	r, err := c.do(ctx, http.MethodGet, "/cards/"+strconv.FormatInt(id, 10), nil, nil, "")
	if err != nil {
		return nil, err
	}
	var out Card
	if err := decodeData(r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PlanConfig plans 中的一档（ACC 执行层定价配置）
type PlanConfig struct {
	Key                 string `json:"key"`
	Label               string `json:"label"`
	Currency            string `json:"currency"`
	Enabled             bool   `json:"enabled"`
	ServiceFeeUsdMinor  int64  `json:"serviceFeeUsdMinor"`
	ExpectedAmountMinor int64  `json:"expectedAmountMinor"`
	MinAmountMinor      int64  `json:"minAmountMinor"`
	MaxAmountMinor      int64  `json:"maxAmountMinor"`
}

// RegistryItem 卡台注册表中的一档；下单的 plan 取 Key
type RegistryItem struct {
	Key                        string `json:"key"`
	Product                    string `json:"product"`
	AccPlanKey                 string `json:"acc_plan_key"`
	Label                      string `json:"label"`
	Funding                    string `json:"funding"`
	CheckoutCurrency           string `json:"checkout_currency"`
	CheckoutAmountMinor        int64  `json:"checkout_amount_minor"`
	ServiceFeeUsdMinor         int64  `json:"service_fee_usd_minor"`
	Purchasable                bool   `json:"purchasable"`
	IsCredit                   bool   `json:"is_credit"`
	RequiresActiveSubscription bool   `json:"requires_active_subscription"`
	Tier                       int    `json:"tier"`
	SortOrder                  int    `json:"sort_order"`
}

// PaymentRegion 可用付款地区
type PaymentRegion struct {
	Country  string `json:"country"`
	Currency string `json:"currency"`
}

// Plans GET /gpt-direct/plans 的结果
type Plans struct {
	Product        string                `json:"-"`
	Version        int64                 `json:"version"`
	Plans          map[string]PlanConfig `json:"plans"`
	Registry       []RegistryItem        `json:"registry"`
	PaymentRegions []PaymentRegion       `json:"payment_regions"`
}

// GetPlans GET /gpt-direct/plans?product=...
func (c *Client) GetPlans(ctx context.Context, product string) (*Plans, error) {
	if !ValidProduct(product) {
		return nil, ErrInvalidProduct
	}
	r, err := c.do(ctx, http.MethodGet, "/gpt-direct/plans", url.Values{"product": {product}}, nil, "")
	if err != nil {
		return nil, err
	}
	var out Plans
	if err := decodeData(r, &out); err != nil {
		return nil, err
	}
	out.Product = product
	return &out, nil
}

// SellablePlan 当前真正可以售卖的一档
type SellablePlan struct {
	Plan                RegistryItem
	Config              PlanConfig
	ServiceFeeUsdMinor  int64
	CheckoutCurrency    string
	CheckoutAmountMinor int64
}

// Sellable 判断某档当前是否可售：注册表中存在、purchasable、且 plans[acc_plan_key].enabled，三条同时成立。
// 注册表行的 product 与请求的 product 不一致时也视为不可售（防止 product 未生效时把全部档位当可售）。
func (p *Plans) Sellable(planKey string) (*SellablePlan, bool) {
	if p == nil {
		return nil, false
	}
	for _, item := range p.Registry {
		if item.Key != planKey {
			continue
		}
		if item.Product != "" && item.Product != p.Product {
			return nil, false
		}
		if !item.Purchasable {
			return nil, false
		}
		accKey := item.AccPlanKey
		if accKey == "" {
			accKey = item.Key
		}
		cfg, ok := p.Plans[accKey]
		if !ok || !cfg.Enabled {
			return nil, false
		}
		fee := item.ServiceFeeUsdMinor
		if fee == 0 {
			fee = cfg.ServiceFeeUsdMinor
		}
		return &SellablePlan{
			Plan:                item,
			Config:              cfg,
			ServiceFeeUsdMinor:  fee,
			CheckoutCurrency:    item.CheckoutCurrency,
			CheckoutAmountMinor: item.CheckoutAmountMinor,
		}, true
	}
	return nil, false
}

// PreflightRequest 预检请求
type PreflightRequest struct {
	Product         string     `json:"product"`
	Credential      Credential `json:"credential"`
	PaymentCountry  string     `json:"payment_country,omitempty"`
	PaymentCurrency string     `json:"payment_currency,omitempty"`
}

// Quote 某档实时报价
type Quote struct {
	Country           string `json:"country"`
	Currency          string `json:"currency"`
	Plan              string `json:"plan"`
	AmountMinor       int64  `json:"amountMinor"`
	MinorUnitExponent int    `json:"minorUnitExponent"`
	FetchedAt         int64  `json:"fetchedAt"`
}

// PreflightResult 预检结果。PreflightToken 为敏感信息（一次性、约 10 分钟有效），GPT 才会返回。
type PreflightResult struct {
	Email              string           `json:"email"`
	CurrentPlan        string           `json:"currentPlan"`
	CredentialMode     string           `json:"credentialMode"`
	PreflightToken     string           `json:"preflight_token"`
	PreflightExpiresAt *time.Time       `json:"preflight_expires_at"`
	PaymentCountry     string           `json:"payment_country"`
	PaymentCurrency    string           `json:"payment_currency"`
	Quotes             map[string]Quote `json:"quotes"`
	QuoteError         string           `json:"quote_error"`
}

// Preflight POST /gpt-direct/preflight
func (c *Client) Preflight(ctx context.Context, req PreflightRequest) (*PreflightResult, error) {
	if !ValidProduct(req.Product) {
		return nil, ErrInvalidProduct
	}
	if err := req.Credential.validate(); err != nil {
		return nil, err
	}
	r, err := c.do(ctx, http.MethodPost, "/gpt-direct/preflight", nil, req, "")
	if err != nil {
		return nil, err
	}
	var out PreflightResult
	if err := decodeData(r, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreateOrderRequest 直充建单请求。只支持 card_id 选卡，不提供 raw_card。
type CreateOrderRequest struct {
	Product          string     `json:"product"`
	Plan             string     `json:"plan"`
	CardID           int64      `json:"card_id"`
	PaymentCountry   string     `json:"payment_country,omitempty"`
	PaymentCurrency  string     `json:"payment_currency,omitempty"`
	Credential       Credential `json:"credential"`
	PreflightToken   string     `json:"preflight_token,omitempty"`
	ClientRequestID  string     `json:"client_request_id"`
	PricingVersion   int64      `json:"pricing_version,omitempty"`
	NoAutoCardSwitch bool       `json:"no_auto_card_switch,omitempty"`
	IdempotencyKey   string     `json:"-"`
}

func (r CreateOrderRequest) validate() error {
	if !ValidProduct(r.Product) {
		return ErrInvalidProduct
	}
	if strings.TrimSpace(r.Plan) == "" {
		return errors.New("zovocard: plan is required")
	}
	if r.CardID <= 0 {
		return errors.New("zovocard: card_id is required")
	}
	if !idempotencyKeyPattern.MatchString(r.ClientRequestID) {
		return errors.New("zovocard: client_request_id must match [A-Za-z0-9._:-]{1,80}")
	}
	if r.IdempotencyKey != "" && !idempotencyKeyPattern.MatchString(r.IdempotencyKey) {
		return errors.New("zovocard: idempotency key must match [A-Za-z0-9._:-]{1,80}")
	}
	return r.Credential.validate()
}

// Order 直充订单（公开字段子集）。Raw 保留完整 JSON 供核对。
type Order struct {
	ID                int64           `json:"id"`
	Product           string          `json:"product"`
	Plan              string          `json:"plan"`
	Source            string          `json:"source"`
	Status            string          `json:"status"`
	Stage             string          `json:"stage"`
	Message           string          `json:"message"`
	ClientRequestID   string          `json:"client_request_id"`
	AccountEmail      string          `json:"account_email"`
	CardID            int64           `json:"card_id"`
	Currency          string          `json:"currency"`
	QuotedAmountMinor int64           `json:"quoted_amount_minor"`
	FinalAmountMinor  int64           `json:"final_amount_minor"`
	ServiceFeeMinor   int64           `json:"service_fee_minor"`
	ServiceFeeStatus  string          `json:"service_fee_status"`
	FundingState      string          `json:"funding_state"`
	FundingHoldStatus string          `json:"funding_hold_status"`
	PricingVersion    int64           `json:"pricing_version"`
	RenewalStatus     string          `json:"renewal_status"`
	Raw               json.RawMessage `json:"-"`
}

// CreateOrderResult 建单结果。HTTP 202 只表示受理，不代表开通成功。
type CreateOrderResult struct {
	Order    Order
	Accepted bool // HTTP 202
	Replayed bool // 幂等重放返回的首次结果
}

// CreateOrder POST /gpt-direct/orders。
// 结果不明（ResultUnknown）时必须用完全相同的请求重放：同一 client_request_id 上游会返回原订单。
func (c *Client) CreateOrder(ctx context.Context, req CreateOrderRequest) (*CreateOrderResult, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	key := req.IdempotencyKey
	if key == "" {
		key = req.ClientRequestID
	}
	r, err := c.do(ctx, http.MethodPost, "/gpt-direct/orders", nil, req, key)
	if err != nil {
		return nil, err
	}
	order, err := decodeOrder(r.data)
	if err != nil {
		return nil, err
	}
	return &CreateOrderResult{Order: *order, Accepted: r.httpStatus == http.StatusAccepted, Replayed: r.replayed}, nil
}

// OrderEvent 订单公开时间线事件
type OrderEvent map[string]any

// OrderDetail GET /gpt-direct/orders/{id} 的结果
type OrderDetail struct {
	Order  Order
	Events []OrderEvent
}

// GetOrder GET /gpt-direct/orders/{id}
func (c *Client) GetOrder(ctx context.Context, id int64) (*OrderDetail, error) {
	if id <= 0 {
		return nil, errors.New("zovocard: order id is required")
	}
	r, err := c.do(ctx, http.MethodGet, "/gpt-direct/orders/"+strconv.FormatInt(id, 10), nil, nil, "")
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Order  json.RawMessage `json:"order"`
		Events []OrderEvent    `json:"events"`
	}
	if err := decodeData(r, &wrapper); err != nil {
		return nil, err
	}
	order, err := decodeOrder(wrapper.Order)
	if err != nil {
		return nil, err
	}
	return &OrderDetail{Order: *order, Events: wrapper.Events}, nil
}

// OrderPage 订单列表的一页
type OrderPage struct {
	List  []Order
	Total int64
}

// ListOrders GET /gpt-direct/orders?page=&page_size=（只支持分页，不支持按 client_request_id 过滤）
func (c *Client) ListOrders(ctx context.Context, page, pageSize int) (*OrderPage, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 100
	}
	q := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(pageSize)}}
	r, err := c.do(ctx, http.MethodGet, "/gpt-direct/orders", q, nil, "")
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		List  []json.RawMessage `json:"list"`
		Total int64             `json:"total"`
	}
	if err := decodeData(r, &wrapper); err != nil {
		return nil, err
	}
	out := &OrderPage{Total: wrapper.Total, List: make([]Order, 0, len(wrapper.List))}
	for _, raw := range wrapper.List {
		order, err := decodeOrder(raw)
		if err != nil {
			return nil, err
		}
		out.List = append(out.List, *order)
	}
	return out, nil
}

// CancelOrder POST /gpt-direct/orders/{id}/cancel。
// 只有 queued / awaiting_card / funding_pending 可取消；已开始支付返回 409（KindConflict）。
func (c *Client) CancelOrder(ctx context.Context, id int64) (*Order, error) {
	if id <= 0 {
		return nil, errors.New("zovocard: order id is required")
	}
	r, err := c.do(ctx, http.MethodPost, "/gpt-direct/orders/"+strconv.FormatInt(id, 10)+"/cancel", nil, struct{}{}, "")
	if err != nil {
		return nil, err
	}
	if len(r.data) == 0 || string(r.data) == "null" {
		return nil, nil
	}
	return decodeOrder(r.data)
}

func decodeOrder(raw json.RawMessage) (*Order, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("zovocard: empty order")
	}
	var out Order
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("zovocard: decode order: %w", err)
	}
	if out.ID == 0 {
		// 部分接口在 data.order_id 返回编号
		var alt struct {
			OrderID int64 `json:"order_id"`
		}
		_ = json.Unmarshal(raw, &alt)
		out.ID = alt.OrderID
	}
	out.Raw = append(json.RawMessage(nil), raw...)
	return &out, nil
}
