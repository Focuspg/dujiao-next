package application

import (
	"context"
	"fmt"
	"strings"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// ResupplyCredentialInput 用户为已付款订单补充凭据
type ResupplyCredentialInput struct {
	UserID     uint
	OrderNo    string
	Mode       string
	Credential string
}

// ResupplyCredential 付款后凭据失效时，订单拥有者补充同一账号的新凭据。
// 重新预检并核对仍是下单时的账号，通过后任务重新排队提交。
func (s *Service) ResupplyCredential(ctx context.Context, input ResupplyCredentialInput) (*TaskView, error) {
	if !s.Available() {
		return nil, directchargecontract.ErrDisabled
	}
	task, err := s.ownedTask(input.UserID, input.OrderNo)
	if err != nil {
		return nil, err
	}
	if task.Status != directchargedomain.TaskWaitingCredential || task.RefundLocked || task.RefundStatus != "" {
		return nil, directchargecontract.ErrCredentialNotRequired
	}
	mode := normalizeMode(input.Mode)
	credential := strings.TrimSpace(input.Credential)
	if credential == "" {
		return nil, directchargecontract.ErrCredentialInvalid
	}
	if len(credential) > s.maxCredentialBytes {
		return nil, directchargecontract.ErrCredentialUnsupported
	}
	spec, err := s.store.SpecByID(task.SpecID)
	if err != nil {
		return nil, err
	}
	if spec == nil || !spec.AllowsCredentialMode(mode) {
		return nil, directchargecontract.ErrCredentialUnsupported
	}

	result, err := s.upstream.Preflight(ctx, directchargecontract.UpstreamPreflightRequest{
		Product:         task.Product,
		Plan:            spec.UpstreamPlan,
		PaymentCountry:  spec.PaymentCountry,
		PaymentCurrency: spec.PaymentCurrency,
		Mode:            mode,
		Credential:      credential,
	})
	if err != nil {
		return nil, err
	}
	accountID := strings.TrimSpace(result.AccountID)
	if accountID == "" {
		return nil, directchargecontract.ErrCredentialInvalid
	}
	if s.vault.Fingerprint(task.Product, accountID) != task.AccountFingerprint {
		return nil, directchargecontract.ErrAccountMismatch
	}
	quote, err := checkPreflight(spec, result)
	if err != nil {
		return nil, err
	}
	ciphertext, keyVersion, err := s.vault.SealCredential(mode, credential, result.PreflightToken)
	if err != nil {
		return nil, err
	}
	now := s.now()
	actor := fmt.Sprintf("user:%d", input.UserID)
	change := s.change(task, directchargedomain.TaskReady, "credential resupplied by owner")
	change.Actor = actor
	change.NewCredential = &directchargedomain.Credential{
		OwnerUserID: input.UserID,
		Mode:        mode,
		Ciphertext:  ciphertext,
		KeyVersion:  keyVersion,
		ExpiresAt:   now.Add(s.credentialTTL),
		CreatedAt:   now,
	}
	ref, err := newRef()
	if err != nil {
		return nil, err
	}
	change.NewPreflight = &directchargedomain.Preflight{
		Ref:                ref,
		UserID:             input.UserID,
		SpecID:             spec.ID,
		SpecVersion:        spec.Version,
		SKUID:              spec.SKUID,
		Status:             directchargedomain.PreflightConsumed,
		AccountFingerprint: task.AccountFingerprint,
		AccountMasked:      task.AccountMasked,
		CurrentPlan:        strings.TrimSpace(result.CurrentPlan),
		QuoteCurrency:      quote.Currency,
		QuoteAmountMinor:   quote.AmountMinor,
		QuoteExponent:      quote.Exponent,
		TokenExpiresAt:     result.TokenExpiresAt,
		ExpiresAt:          now.Add(s.preflightTTL),
		ConsumedAt:         &now,
		CreatedAt:          now,
	}
	change.Fields["next_check_at"] = now
	change.Fields["error_kind"] = ""
	change.Fields["error_message"] = ""
	change.Fields["action_message"] = ""
	if !s.apply(task, change) {
		return nil, directchargecontract.ErrCredentialNotRequired
	}
	return s.GetOrder(input.UserID, input.OrderNo)
}

// RequestRefund 订单拥有者申请售后退款；只登记申请并提醒站长，退款由后台审批执行
func (s *Service) RequestRefund(userID uint, orderNo string) (*TaskView, error) {
	if s == nil || s.store == nil {
		return nil, directchargecontract.ErrNotFound
	}
	task, err := s.ownedTask(userID, orderNo)
	if err != nil {
		return nil, err
	}
	if !refundRequestable(task) {
		return nil, directchargecontract.ErrRefundNotAllowed
	}
	now := s.now()
	change := s.change(task, task.Status, "refund requested by owner")
	change.Actor = fmt.Sprintf("user:%d", userID)
	change.Fields["refund_status"] = directchargedomain.RefundRequested
	change.Fields["refund_requested_at"] = now
	if !s.apply(task, change) {
		return nil, directchargecontract.ErrRefundNotAllowed
	}
	s.alertTask(task, alertRefundRequested, directchargecontract.AlertWarning, "用户申请代充订单退款，请在后台核对后处理")
	return s.GetOrder(userID, orderNo)
}

// refundRequestable 用户可在充值失败、需要补凭据、需处理账号或核验中时申请退款；处理中不可申请
func refundRequestable(task *directchargedomain.Task) bool {
	if task.RefundStatus != "" {
		return false
	}
	switch task.Status {
	case directchargedomain.TaskFailedConfirmed, directchargedomain.TaskCancelledConfirmed,
		directchargedomain.TaskWaitingCredential, directchargedomain.TaskRequiresAction, directchargedomain.TaskReview:
		return true
	}
	return false
}

func (s *Service) ownedTask(userID uint, orderNo string) (*directchargedomain.Task, error) {
	orderNo = strings.TrimSpace(orderNo)
	if userID == 0 || orderNo == "" {
		return nil, directchargecontract.ErrNotFound
	}
	task, err := s.store.TaskByParentOrderNo(userID, orderNo)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, directchargecontract.ErrNotFound
	}
	return task, nil
}
