package application

import (
	"errors"
	"fmt"
	"time"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// CreateOrderInput 用户确认预检结果后创建待支付订单
type CreateOrderInput struct {
	UserID       uint
	PreflightRef string
	ClientIP     string
}

// CreateOrderResult 已创建（或幂等返回）的订单
type CreateOrderResult struct {
	OrderNo   string     `json:"order_no"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Replayed  bool       `json:"replayed"`
}

// CreateOrder 用预检引用创建固定价格的待支付订单。
// 一个预检引用只能创建一个订单：重复提交返回同一订单，预检引用即幂等键。
func (s *Service) CreateOrder(input CreateOrderInput) (*CreateOrderResult, error) {
	if !s.Available() {
		return nil, directchargecontract.ErrDisabled
	}
	preflight, err := s.store.PreflightByRef(input.PreflightRef)
	if err != nil {
		return nil, err
	}
	if preflight == nil || preflight.UserID != input.UserID || input.UserID == 0 {
		return nil, directchargecontract.ErrPreflightExpired
	}
	if preflight.Status == directchargedomain.PreflightConsumed {
		return s.replayOrder(preflight.ID)
	}
	now := s.now()
	if preflight.Status != directchargedomain.PreflightOK || !now.Before(preflight.ExpiresAt) || preflight.CredentialID == nil {
		return nil, directchargecontract.ErrPreflightExpired
	}
	spec, err := s.store.SpecByID(preflight.SpecID)
	if err != nil {
		return nil, err
	}
	if spec == nil || !spec.Enabled || spec.SKUID != preflight.SKUID {
		return nil, directchargecontract.ErrProductUnavailable
	}
	if spec.Version != preflight.SpecVersion {
		return nil, directchargecontract.ErrPreflightExpired
	}
	if err := s.ensureAccountFree(preflight.AccountFingerprint); err != nil {
		return nil, err
	}

	claimed, err := s.store.ClaimPreflight(preflight.ID, now)
	if err != nil {
		return nil, err
	}
	if !claimed {
		return s.replayOrder(preflight.ID)
	}

	ref, err := s.orders.CreateDirectChargeOrder(directchargecontract.OrderRequest{
		UserID:    input.UserID,
		ProductID: spec.ProductID,
		SKUID:     spec.SKUID,
		ClientIP:  input.ClientIP,
	})
	if err != nil {
		if releaseErr := s.store.ReleasePreflight(preflight.ID); releaseErr != nil {
			logger.Warnw("directcharge_preflight_release_failed", "preflight_id", preflight.ID, "error", releaseErr)
		}
		return nil, err
	}

	task := &directchargedomain.Task{
		OrderID:            ref.ChildID,
		OrderNo:            ref.ChildNo,
		ParentOrderID:      ref.ParentID,
		ParentOrderNo:      ref.ParentNo,
		UserID:             input.UserID,
		SpecID:             spec.ID,
		SKUID:              spec.SKUID,
		Status:             directchargedomain.TaskAwaitingPayment,
		Version:            1,
		Product:            spec.UpstreamProduct,
		Plan:               spec.UpstreamPlan,
		AccountFingerprint: preflight.AccountFingerprint,
		AccountMasked:      preflight.AccountMasked,
		CredentialID:       preflight.CredentialID,
		PreflightID:        &preflight.ID,
		QuoteCurrency:      preflight.QuoteCurrency,
		QuoteAmountMinor:   preflight.QuoteAmountMinor,
		QuoteExponent:      preflight.QuoteExponent,
		PricingVersion:     preflight.PricingVersion,
		SpecVersion:        spec.Version,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	// 凭据要覆盖付款窗口和付款后的履约时间
	credentialExpiresAt := now.Add(s.credentialTTL)
	if ref.ExpiresAt != nil && ref.ExpiresAt.Add(s.credentialTTL).After(credentialExpiresAt) {
		credentialExpiresAt = ref.ExpiresAt.Add(s.credentialTTL)
	}
	if err := s.store.CreateTask(task, credentialExpiresAt, fmt.Sprintf("user:%d", input.UserID)); err != nil {
		if cancelErr := s.orders.CancelUnpaid(ref.ParentID, input.UserID); cancelErr != nil {
			logger.Errorw("directcharge_order_cancel_after_task_failure_failed",
				"order_no", ref.ParentNo, "error", cancelErr)
		}
		if errors.Is(err, directchargecontract.ErrAccountBusy) {
			return nil, directchargecontract.ErrAccountBusy
		}
		logger.Errorw("directcharge_task_create_failed", "order_no", ref.ParentNo, "error", err)
		return nil, directchargecontract.ErrOrderCreateFailed
	}
	return &CreateOrderResult{OrderNo: ref.ParentNo, ExpiresAt: ref.ExpiresAt}, nil
}

func (s *Service) replayOrder(preflightID uint) (*CreateOrderResult, error) {
	task, err := s.store.TaskByPreflight(preflightID)
	if err != nil {
		return nil, err
	}
	if task == nil || task.Status == directchargedomain.TaskClosed {
		return nil, directchargecontract.ErrPreflightExpired
	}
	return &CreateOrderResult{OrderNo: task.ParentOrderNo, Replayed: true}, nil
}
