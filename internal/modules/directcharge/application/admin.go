package application

import (
	"regexp"
	"strings"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
	"github.com/dujiao-next/internal/shared/jsonslice"
)

const maxSpecConcurrency = 20

var (
	upstreamCodePattern = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	regionCodePattern   = regexp.MustCompile(`^[A-Z]{0,8}$`)
)

// SpecInput 后台新建或修改充值规格
type SpecInput struct {
	ProductID           uint     `json:"product_id"`
	SKUID               uint     `json:"sku_id"`
	UpstreamProduct     string   `json:"upstream_product"`
	UpstreamPlan        string   `json:"upstream_plan"`
	PaymentCountry      string   `json:"payment_country"`
	PaymentCurrency     string   `json:"payment_currency"`
	CredentialModes     []string `json:"credential_modes"`
	AllowedCurrentPlans []string `json:"allowed_current_plans"`
	CardID              int64    `json:"card_id"`
	MaxCostUsdMinor     int64    `json:"max_cost_usd_minor"`
	MaxQuoteMinor       int64    `json:"max_quote_minor"`
	ConcurrencyLimit    int      `json:"concurrency_limit"`
	Enabled             bool     `json:"enabled"`
	SubmitPaused        bool     `json:"submit_paused"`
}

// TaskDetail 后台任务详情：任务、全部上游尝试、状态日志与关联回调。不含凭据。
type TaskDetail struct {
	Task     directchargedomain.Task           `json:"task"`
	Attempts []directchargedomain.Attempt      `json:"attempts"`
	Logs     []directchargedomain.TaskLog      `json:"logs"`
	Events   []directchargedomain.WebhookEvent `json:"events"`
}

func (s *Service) ListSpecs() ([]directchargedomain.Spec, error) {
	if s == nil || s.store == nil {
		return nil, directchargecontract.ErrDisabled
	}
	return s.store.ListSpecs()
}

// SaveSpec 新建（id 为 0）或修改规格。修改上游套餐、卡或报价上限会提升版本，已付款任务提交前按新规格复核。
func (s *Service) SaveSpec(id uint, input SpecInput) (*directchargedomain.Spec, error) {
	if s == nil || s.store == nil {
		return nil, directchargecontract.ErrDisabled
	}
	spec := &directchargedomain.Spec{Supplier: directchargedomain.SupplierZovoCard, Version: 1}
	if id > 0 {
		existing, err := s.store.SpecByID(id)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, directchargecontract.ErrNotFound
		}
		spec = existing
	}
	normalized, err := normalizeSpecInput(input)
	if err != nil {
		return nil, err
	}
	if id > 0 && specTermsChanged(spec, normalized) {
		spec.Version++
	}
	spec.ProductID = normalized.ProductID
	spec.SKUID = normalized.SKUID
	spec.UpstreamProduct = normalized.UpstreamProduct
	spec.UpstreamPlan = normalized.UpstreamPlan
	spec.PaymentCountry = normalized.PaymentCountry
	spec.PaymentCurrency = normalized.PaymentCurrency
	spec.CredentialModes = jsonslice.Strings(normalized.CredentialModes)
	spec.AllowedCurrentPlans = jsonslice.Strings(normalized.AllowedCurrentPlans)
	spec.CardID = normalized.CardID
	spec.MaxCostUsdMinor = normalized.MaxCostUsdMinor
	spec.MaxQuoteMinor = normalized.MaxQuoteMinor
	spec.ConcurrencyLimit = normalized.ConcurrencyLimit
	spec.Enabled = normalized.Enabled
	spec.SubmitPaused = normalized.SubmitPaused
	spec.UpdatedAt = s.now()
	if spec.ID == 0 {
		spec.CreatedAt = spec.UpdatedAt
	}
	if err := s.store.SaveSpec(spec); err != nil {
		return nil, err
	}
	return spec, nil
}

func normalizeSpecInput(input SpecInput) (SpecInput, error) {
	input.UpstreamProduct = strings.ToLower(strings.TrimSpace(input.UpstreamProduct))
	input.UpstreamPlan = strings.ToLower(strings.TrimSpace(input.UpstreamPlan))
	input.PaymentCountry = strings.ToUpper(strings.TrimSpace(input.PaymentCountry))
	input.PaymentCurrency = strings.ToUpper(strings.TrimSpace(input.PaymentCurrency))
	if input.ProductID == 0 || input.SKUID == 0 || input.CardID <= 0 ||
		!upstreamCodePattern.MatchString(input.UpstreamProduct) || !upstreamCodePattern.MatchString(input.UpstreamPlan) ||
		!regionCodePattern.MatchString(input.PaymentCountry) || !regionCodePattern.MatchString(input.PaymentCurrency) ||
		input.MaxCostUsdMinor < 0 || input.MaxQuoteMinor < 0 {
		return input, directchargecontract.ErrInvalidSpec
	}
	if input.ConcurrencyLimit < 1 {
		input.ConcurrencyLimit = 1
	}
	if input.ConcurrencyLimit > maxSpecConcurrency {
		return input, directchargecontract.ErrInvalidSpec
	}
	modes := make([]string, 0, len(input.CredentialModes))
	for _, mode := range input.CredentialModes {
		mode = strings.ToLower(strings.TrimSpace(mode))
		if mode != directchargedomain.CredentialModeSession && mode != directchargedomain.CredentialModeAccessToken {
			return input, directchargecontract.ErrInvalidSpec
		}
		if !containsString(modes, mode) {
			modes = append(modes, mode)
		}
	}
	input.CredentialModes = modes
	plans := make([]string, 0, len(input.AllowedCurrentPlans))
	for _, plan := range input.AllowedCurrentPlans {
		plan = strings.ToLower(strings.TrimSpace(plan))
		if plan == "" {
			continue
		}
		if !upstreamCodePattern.MatchString(plan) {
			return input, directchargecontract.ErrInvalidSpec
		}
		if !containsString(plans, plan) {
			plans = append(plans, plan)
		}
	}
	input.AllowedCurrentPlans = plans
	return input, nil
}

func specTermsChanged(spec *directchargedomain.Spec, input SpecInput) bool {
	return spec.UpstreamProduct != input.UpstreamProduct || spec.UpstreamPlan != input.UpstreamPlan ||
		spec.PaymentCountry != input.PaymentCountry || spec.PaymentCurrency != input.PaymentCurrency ||
		spec.CardID != input.CardID || spec.MaxCostUsdMinor != input.MaxCostUsdMinor || spec.MaxQuoteMinor != input.MaxQuoteMinor
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (s *Service) ListTasks(filter directchargecontract.TaskFilter) ([]directchargedomain.Task, int64, error) {
	if s == nil || s.store == nil {
		return nil, 0, directchargecontract.ErrDisabled
	}
	return s.store.ListTasks(filter)
}

func (s *Service) TaskDetail(id uint) (*TaskDetail, error) {
	if s == nil || s.store == nil {
		return nil, directchargecontract.ErrDisabled
	}
	task, err := s.store.TaskByID(id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, directchargecontract.ErrNotFound
	}
	attempts, err := s.store.AttemptsByTask(id)
	if err != nil {
		return nil, err
	}
	logs, err := s.store.TaskLogs(id)
	if err != nil {
		return nil, err
	}
	events, err := s.store.WebhookEventsByTask(id)
	if err != nil {
		return nil, err
	}
	return &TaskDetail{Task: *task, Attempts: attempts, Logs: logs, Events: events}, nil
}
