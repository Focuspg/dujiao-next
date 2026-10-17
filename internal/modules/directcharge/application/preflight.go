package application

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// PreflightInput 用户提交的待验证账号凭据
type PreflightInput struct {
	UserID     uint
	SKUID      uint
	Mode       string
	Credential string
}

// PreflightView 返回给用户确认的脱敏预检结果
type PreflightView struct {
	Ref              string    `json:"preflight_ref"`
	SKUID            uint      `json:"sku_id"`
	Product          string    `json:"product"`
	Plan             string    `json:"plan"`
	AccountMasked    string    `json:"account_masked"`
	CurrentPlan      string    `json:"current_plan"`
	QuoteCurrency    string    `json:"quote_currency"`
	QuoteAmountMinor int64     `json:"quote_amount_minor"`
	QuoteExponent    int       `json:"quote_exponent"`
	ExpiresAt        time.Time `json:"expires_at"`
}

// Preflight 校验凭据、调用上游预检，并把凭据密文与预检结果保存为短期引用。
// 凭据明文只在本次调用内存中出现，不写日志。
func (s *Service) Preflight(ctx context.Context, input PreflightInput) (*PreflightView, error) {
	if !s.Available() {
		return nil, directchargecontract.ErrDisabled
	}
	mode := normalizeMode(input.Mode)
	credential := strings.TrimSpace(input.Credential)
	if credential == "" {
		return nil, directchargecontract.ErrCredentialInvalid
	}
	if len(credential) > s.maxCredentialBytes {
		return nil, directchargecontract.ErrCredentialUnsupported
	}
	spec, err := s.loadSellableSpec(input.SKUID)
	if err != nil {
		return nil, err
	}
	if !spec.AllowsCredentialMode(mode) {
		return nil, directchargecontract.ErrCredentialUnsupported
	}

	result, err := s.upstream.Preflight(ctx, directchargecontract.UpstreamPreflightRequest{
		Product:         spec.UpstreamProduct,
		PaymentCountry:  spec.PaymentCountry,
		PaymentCurrency: spec.PaymentCurrency,
		Mode:            mode,
		Credential:      credential,
	})
	if err != nil {
		return nil, err
	}
	quote, err := checkPreflight(spec, result)
	if err != nil {
		return nil, err
	}
	accountID := strings.TrimSpace(result.AccountID)
	fingerprint := s.vault.Fingerprint(spec.UpstreamProduct, accountID)
	if fingerprint == "" {
		return nil, directchargecontract.ErrCredentialInvalid
	}
	if err := s.ensureAccountFree(fingerprint); err != nil {
		return nil, err
	}

	ciphertext, keyVersion, err := s.vault.SealCredential(mode, credential, result.PreflightToken)
	if err != nil {
		return nil, err
	}
	ref, err := newRef()
	if err != nil {
		return nil, err
	}
	now := s.now()
	record := &directchargedomain.Preflight{
		Ref:                ref,
		UserID:             input.UserID,
		SpecID:             spec.ID,
		SpecVersion:        spec.Version,
		SKUID:              spec.SKUID,
		Status:             directchargedomain.PreflightOK,
		AccountFingerprint: fingerprint,
		AccountMasked:      s.vault.Mask(accountID),
		CurrentPlan:        strings.TrimSpace(result.CurrentPlan),
		QuoteCurrency:      quote.Currency,
		QuoteAmountMinor:   quote.AmountMinor,
		QuoteExponent:      quote.Exponent,
		TokenExpiresAt:     result.TokenExpiresAt,
		ExpiresAt:          now.Add(s.preflightTTL),
		CreatedAt:          now,
	}
	vaultRecord := &directchargedomain.Credential{
		OwnerUserID: input.UserID,
		Mode:        mode,
		Ciphertext:  ciphertext,
		KeyVersion:  keyVersion,
		ExpiresAt:   now.Add(s.credentialTTL),
		CreatedAt:   now,
	}
	if err := s.store.CreatePreflight(vaultRecord, record); err != nil {
		return nil, err
	}
	return &PreflightView{
		Ref:              record.Ref,
		SKUID:            record.SKUID,
		Product:          spec.UpstreamProduct,
		Plan:             spec.UpstreamPlan,
		AccountMasked:    record.AccountMasked,
		CurrentPlan:      record.CurrentPlan,
		QuoteCurrency:    record.QuoteCurrency,
		QuoteAmountMinor: record.QuoteAmountMinor,
		QuoteExponent:    record.QuoteExponent,
		ExpiresAt:        record.ExpiresAt,
	}, nil
}

// checkPreflight 按规格判断预检结果是否可售，返回目标套餐报价
func checkPreflight(spec *directchargedomain.Spec, result *directchargecontract.UpstreamPreflight) (directchargecontract.UpstreamQuote, error) {
	if result == nil || strings.TrimSpace(result.AccountID) == "" {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrCredentialInvalid
	}
	if !spec.AllowsCurrentPlan(result.CurrentPlan) {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrNotEligible
	}
	if strings.TrimSpace(result.QuoteError) != "" {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrProductUnavailable
	}
	quote, ok := result.Quotes[spec.UpstreamPlan]
	if !ok || quote.AmountMinor <= 0 {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrProductUnavailable
	}
	if spec.PaymentCurrency != "" && !strings.EqualFold(quote.Currency, spec.PaymentCurrency) {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrProductUnavailable
	}
	if spec.MaxQuoteMinor > 0 && quote.AmountMinor > spec.MaxQuoteMinor {
		return directchargecontract.UpstreamQuote{}, directchargecontract.ErrQuoteExceeded
	}
	return quote, nil
}

// ensureAccountFree 账号被占用时，先尝试关闭已取消/过期订单留下的任务，仍被占用则拒绝
func (s *Service) ensureAccountFree(fingerprint string) error {
	holder, err := s.store.LockHolder(fingerprint)
	if err != nil {
		return err
	}
	if holder == nil {
		return nil
	}
	released, err := s.closeIfOrderCanceled(holder)
	if err != nil {
		return err
	}
	if !released {
		return directchargecontract.ErrAccountBusy
	}
	return nil
}

// closeIfOrderCanceled 未付款任务的订单已取消或不存在时关闭任务并释放账号，返回是否已关闭
func (s *Service) closeIfOrderCanceled(task *directchargedomain.Task) (bool, error) {
	if task == nil || task.Status != directchargedomain.TaskAwaitingPayment {
		return false, nil
	}
	status, err := s.orders.Status(task.OrderID)
	if err != nil {
		return false, err
	}
	if status != "" && status != constants.OrderStatusCanceled {
		return false, nil
	}
	closed, err := s.store.CloseUnpaidTask(task.ID, task.Version, "order canceled before payment", "system", s.now())
	if err != nil {
		return false, err
	}
	if closed {
		logger.Infow("directcharge_task_closed_unpaid", "task_id", task.ID, "order_no", task.OrderNo)
	}
	return closed, nil
}

func newRef() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "pf_" + hex.EncodeToString(buf), nil
}
