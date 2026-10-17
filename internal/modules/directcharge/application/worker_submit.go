package application

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// submit 已付款任务：复核规格与凭据，必要时重新预检，固定一次上游尝试后提交
func (s *Service) submit(ctx context.Context, task *directchargedomain.Task) {
	if task.RefundLocked {
		// 退款流程已接管，任何路径都不得再提交上游
		return
	}
	spec, err := s.store.SpecByID(task.SpecID)
	if err != nil {
		logger.Warnw("directcharge_submit_spec_load_failed", "task_id", task.ID, "error", err)
		s.deferTask(task, retryShort, "")
		return
	}
	if spec == nil || !spec.Enabled || spec.UpstreamProduct != task.Product || spec.UpstreamPlan != task.Plan {
		s.toReview(task, "spec disabled or plan changed after payment")
		return
	}
	if spec.CardID <= 0 {
		s.toReview(task, "spec has no card_id configured")
		return
	}
	limit := spec.ConcurrencyLimit
	if limit < 1 {
		limit = 1
	}
	active, err := s.store.CountActive(spec.ID)
	if err != nil {
		logger.Warnw("directcharge_submit_count_active_failed", "task_id", task.ID, "error", err)
		s.deferTask(task, retryShort, "")
		return
	}
	if active >= int64(limit) {
		s.deferTask(task, pollDelays[1], "")
		return
	}

	secret, ok := s.openTaskCredential(task)
	if !ok {
		return
	}
	if !s.ensurePreflightToken(ctx, task, spec, &secret) {
		return
	}

	attemptNo := task.AttemptCount + 1
	request := directchargecontract.UpstreamOrderRequest{
		Product:         task.Product,
		Plan:            task.Plan,
		CardID:          spec.CardID,
		PaymentCountry:  spec.PaymentCountry,
		PaymentCurrency: spec.PaymentCurrency,
		Mode:            secret.Mode,
		Credential:      secret.Credential,
		PreflightToken:  secret.PreflightToken,
		ClientRequestID: directchargedomain.BuildClientRequestID(s.sitePrefix, task.ID, attemptNo),
		PricingVersion:  task.PricingVersion,
	}
	now := s.now()
	attempt := &directchargedomain.Attempt{
		TaskID:            task.ID,
		AttemptNo:         attemptNo,
		Supplier:          directchargedomain.SupplierZovoCard,
		ClientRequestID:   request.ClientRequestID,
		IdempotencyKey:    request.ClientRequestID,
		RequestDigest:     requestDigest(request, task.AccountFingerprint),
		CardID:            spec.CardID,
		Status:            directchargedomain.AttemptPrepared,
		Currency:          task.QuoteCurrency,
		QuotedAmountMinor: task.QuoteAmountMinor,
		Reason:            fmt.Sprintf("attempt %d after payment", attemptNo),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	change := s.change(task, directchargedomain.TaskSubmitting, fmt.Sprintf("submit attempt %d", attemptNo))
	change.NewAttempt = attempt
	change.Fields["error_kind"] = ""
	change.Fields["error_message"] = ""
	if !s.apply(task, change) {
		return
	}
	task.CurrentAttemptID = &attempt.ID
	task.AttemptCount = attemptNo

	callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
	order, err := s.upstream.CreateOrder(callCtx, request)
	cancel()
	s.afterSubmit(task, attempt, order, err, false)
}

// openTaskCredential 解密任务绑定的凭据；凭据已过期或被清理时任务等待用户补充
func (s *Service) openTaskCredential(task *directchargedomain.Task) (directchargecontract.OpenedCredential, bool) {
	if task.CredentialID == nil {
		s.waitCredential(task, "credential missing")
		return directchargecontract.OpenedCredential{}, false
	}
	credential, err := s.store.CredentialByID(*task.CredentialID)
	if err != nil {
		logger.Warnw("directcharge_credential_load_failed", "task_id", task.ID, "error", err)
		s.deferTask(task, retryShort, "")
		return directchargecontract.OpenedCredential{}, false
	}
	if !credential.Usable(s.now()) {
		s.waitCredential(task, "credential expired before submit")
		return directchargecontract.OpenedCredential{}, false
	}
	opened, err := s.vault.OpenCredential(credential.Ciphertext, credential.KeyVersion)
	if err != nil {
		logger.Errorw("directcharge_credential_open_failed", "task_id", task.ID, "key_version", credential.KeyVersion)
		s.toReview(task, "credential cannot be decrypted")
		return directchargecontract.OpenedCredential{}, false
	}
	return opened, true
}

func (s *Service) waitCredential(task *directchargedomain.Task, reason string) {
	change := s.change(task, directchargedomain.TaskWaitingCredential, reason)
	change.Fields["error_kind"] = directchargecontract.UpstreamKindCredential
	if s.apply(task, change) {
		s.alertTask(task, alertCredentialRequired, directchargecontract.AlertWarning, "代充任务需要新的有效凭据（"+reason+"），请联系用户或转退款")
	}
}

// ensurePreflightToken 下单时的预检令牌一次性且约 10 分钟有效；首次提交且令牌未过期时直接使用，
// 否则重新预检，并核对仍是同一账号、仍适用该套餐、报价未超限。
func (s *Service) ensurePreflightToken(ctx context.Context, task *directchargedomain.Task, spec *directchargedomain.Spec, secret *directchargecontract.OpenedCredential) bool {
	now := s.now()
	if task.AttemptCount == 0 && task.ErrorKind != directchargecontract.UpstreamKindRequote && secret.PreflightToken != "" && task.PreflightID != nil {
		preflight, err := s.store.PreflightByID(*task.PreflightID)
		if err == nil && preflight != nil {
			expires := preflight.CreatedAt.Add(tokenFallbackLifetime)
			if preflight.TokenExpiresAt != nil {
				expires = *preflight.TokenExpiresAt
			}
			if now.Add(tokenSafetyMargin).Before(expires) {
				return true
			}
		}
	}

	if !s.apply(task, s.change(task, directchargedomain.TaskPreflighting, "refresh preflight before submit")) {
		return false
	}
	callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
	result, err := s.upstream.Preflight(callCtx, directchargecontract.UpstreamPreflightRequest{
		Product:         spec.UpstreamProduct,
		PaymentCountry:  spec.PaymentCountry,
		PaymentCurrency: spec.PaymentCurrency,
		Mode:            secret.Mode,
		Credential:      secret.Credential,
	})
	cancel()
	if err != nil {
		switch {
		case errors.Is(err, directchargecontract.ErrCredentialInvalid):
			s.waitCredential(task, "credential rejected by preflight")
		case errors.Is(err, directchargecontract.ErrNotEligible):
			s.failConfirmed(task, "account no longer eligible (preflight)")
		default:
			change := s.change(task, directchargedomain.TaskReady, "preflight unavailable, retry later")
			change.Fields["next_check_at"] = s.now().Add(retryShort)
			s.apply(task, change)
		}
		return false
	}
	quote, err := checkPreflight(spec, result)
	if err != nil {
		if errors.Is(err, directchargecontract.ErrNotEligible) {
			s.failConfirmed(task, "account no longer eligible (current plan)")
		} else {
			s.toReview(task, "preflight quote not acceptable after payment: "+err.Error())
		}
		return false
	}
	if s.vault.Fingerprint(spec.UpstreamProduct, strings.TrimSpace(result.AccountID)) != task.AccountFingerprint {
		s.toReview(task, "preflight account differs from ordered account")
		return false
	}
	if !strings.EqualFold(quote.Currency, task.QuoteCurrency) {
		s.toReview(task, "preflight quote currency changed")
		return false
	}
	if quote.AmountMinor != task.QuoteAmountMinor {
		logger.Infow("directcharge_quote_changed_within_limit", "task_id", task.ID, "ordered_minor", task.QuoteAmountMinor, "current_minor", quote.AmountMinor)
	}
	ciphertext, keyVersion, err := s.vault.SealCredential(secret.Mode, secret.Credential, result.PreflightToken)
	if err != nil {
		s.toReview(task, "credential cannot be re-sealed")
		return false
	}
	preflightID := uint(0)
	if task.PreflightID != nil {
		preflightID = *task.PreflightID
	}
	if err := s.store.ReplaceCredentialSecret(*task.CredentialID, ciphertext, keyVersion, preflightID, result.TokenExpiresAt); err != nil {
		logger.Warnw("directcharge_credential_reseal_failed", "task_id", task.ID, "error", err)
		change := s.change(task, directchargedomain.TaskReady, "credential update failed, retry later")
		change.Fields["next_check_at"] = s.now().Add(retryShort)
		s.apply(task, change)
		return false
	}
	secret.PreflightToken = result.PreflightToken
	return true
}

func (s *Service) failConfirmed(task *directchargedomain.Task, reason string) {
	change := s.change(task, directchargedomain.TaskFailedConfirmed, reason)
	change.Fields["error_message"] = truncateText(reason, 500)
	change.Fields["completed_at"] = s.now()
	if s.apply(task, change) {
		s.alertTask(task, alertFailed, directchargecontract.AlertWarning, "代充未执行且已确认失败（"+reason+"），请审批退款")
	}
}

// afterSubmit 处理一次提交或重放的结果。replay 为 true 时任务处于结果待核验。
func (s *Service) afterSubmit(task *directchargedomain.Task, attempt *directchargedomain.Attempt, order *directchargecontract.UpstreamOrder, err error, replay bool) {
	now := s.now()
	attempt.UpdatedAt = now
	if attempt.SentAt == nil {
		attempt.SentAt = &now
	}
	if err == nil && order != nil {
		if order.ClientRequestID != "" && order.ClientRequestID != attempt.ClientRequestID {
			attempt.Status = directchargedomain.AttemptUnknown
			s.toReview(task, "upstream returned a different client_request_id")
			return
		}
		fillAttempt(attempt, order, now)
		attempt.Status = directchargedomain.AttemptSubmitted
		change := s.change(task, directchargedomain.TaskProcessing, "accepted by upstream")
		change.Attempt = attempt
		change.DestroyCredential = true
		change.Fields["next_check_at"] = now
		change.Fields["check_count"] = 0
		change.Fields["error_kind"] = ""
		change.Fields["error_message"] = ""
		if task.SubmittedAt == nil {
			change.Fields["submitted_at"] = now
		}
		s.apply(task, change)
		return
	}

	var upstreamErr *directchargecontract.UpstreamError
	if !errors.As(err, &upstreamErr) {
		// 写请求出现非预期错误：按结果不明处理，只能用原请求号核验
		upstreamErr = &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindTransient, Unknown: true}
	}
	if upstreamErr.Unknown || replay {
		attempt.Status = directchargedomain.AttemptUnknown
		attempt.ErrorKind = upstreamErr.Kind
		attempt.ErrorMessage = upstreamErr.Code
		if replay && !upstreamErr.Unknown && !retryableKind(upstreamErr.Kind) {
			s.toReviewWithAttempt(task, attempt, "replay rejected by upstream: "+upstreamErr.Kind+" "+upstreamErr.Code)
			return
		}
		change := s.change(task, directchargedomain.TaskSubmitUnknown, "submit result unknown")
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		change.Fields["next_check_at"] = now.Add(pollDelays[1])
		if task.SubmittedAt == nil {
			change.Fields["submitted_at"] = now
		}
		s.apply(task, change)
		return
	}

	// 明确的业务拒绝：上游没有建单，可以按类别决定下一步
	attempt.Status = directchargedomain.AttemptRejected
	attempt.ErrorKind = upstreamErr.Kind
	attempt.ErrorMessage = upstreamErr.Code
	attempt.CompletedAt = &now
	reason := "upstream rejected: " + upstreamErr.Kind + " " + upstreamErr.Code
	switch upstreamErr.Kind {
	case directchargecontract.UpstreamKindCredential:
		change := s.change(task, directchargedomain.TaskWaitingCredential, reason)
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		if s.apply(task, change) {
			s.alertTask(task, alertCredentialRequired, directchargecontract.AlertWarning, "上游判定凭据无效，代充任务需要新的有效凭据，请联系用户或转退款")
		}
	case directchargecontract.UpstreamKindNotEligible:
		change := s.change(task, directchargedomain.TaskFailedConfirmed, reason)
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		change.Fields["completed_at"] = now
		if s.apply(task, change) {
			s.alertTask(task, alertFailed, directchargecontract.AlertWarning, "目标账号不适用该套餐，上游未建单，请审批退款")
		}
	case directchargecontract.UpstreamKindRequote:
		change := s.change(task, directchargedomain.TaskReady, reason)
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		change.Fields["next_check_at"] = now
		s.apply(task, change)
	case directchargecontract.UpstreamKindFunds, directchargecontract.UpstreamKindConfig:
		change := s.change(task, directchargedomain.TaskReady, reason)
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		change.Fields["next_check_at"] = now.Add(retryLong)
		if s.apply(task, change) {
			s.alertTask(task, alertUpstreamBlocked, directchargecontract.AlertCritical, "上游拒绝接单（"+upstreamErr.Kind+" "+upstreamErr.Code+"）：请检查 ZovoCard 余额、密钥与 IP 白名单，任务会自动重试")
		}
	case directchargecontract.UpstreamKindUnavailable, directchargecontract.UpstreamKindTransient:
		change := s.change(task, directchargedomain.TaskReady, reason)
		change.Attempt = attempt
		change.Fields["error_kind"] = upstreamErr.Kind
		change.Fields["next_check_at"] = now.Add(retryShort)
		s.apply(task, change)
	default:
		s.toReviewWithAttempt(task, attempt, reason)
	}
}

func (s *Service) toReviewWithAttempt(task *directchargedomain.Task, attempt *directchargedomain.Attempt, reason string) {
	change := s.change(task, directchargedomain.TaskReview, reason)
	change.Attempt = attempt
	change.Fields["error_message"] = truncateText(reason, 500)
	if s.apply(task, change) {
		s.alertTask(task, alertReview, directchargecontract.AlertCritical, "代充任务转人工核验："+reason)
	}
}

// recoverUnknown 提交结果不明：先用原请求号原样重放（上游对同一请求号返回原订单），
// 凭据已清理或参数已变时翻订单列表兜底；长时间无法确认转人工，绝不换号重建。
func (s *Service) recoverUnknown(ctx context.Context, task *directchargedomain.Task) {
	attempt := s.currentAttempt(task)
	if attempt == nil {
		s.toReview(task, "submit_unknown without attempt")
		return
	}
	if attempt.UpstreamOrderID > 0 {
		change := s.change(task, directchargedomain.TaskProcessing, "upstream order linked")
		change.Fields["next_check_at"] = s.now()
		s.apply(task, change)
		return
	}

	if request, ok := s.replayRequest(task, attempt); ok {
		callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
		order, err := s.upstream.CreateOrder(callCtx, request)
		cancel()
		s.afterSubmit(task, attempt, order, err, true)
		if err == nil || task.Status != directchargedomain.TaskSubmitUnknown {
			return
		}
	} else {
		callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
		order, err := s.upstream.FindOrder(callCtx, attempt.ClientRequestID, findOrderPages)
		cancel()
		if err == nil && order != nil {
			s.afterSubmit(task, attempt, order, nil, true)
			return
		}
		s.deferTask(task, retryShort, "")
	}

	since := task.UpdatedAt
	if attempt.SentAt != nil {
		since = *attempt.SentAt
	}
	if task.Status == directchargedomain.TaskSubmitUnknown && s.now().Sub(since) >= unknownReviewAfter {
		s.toReview(task, "submit result still unknown after 30 minutes")
	}
}

// replayRequest 重建与原尝试完全相同的请求；凭据不可用或业务参数已变化时返回 false
func (s *Service) replayRequest(task *directchargedomain.Task, attempt *directchargedomain.Attempt) (directchargecontract.UpstreamOrderRequest, bool) {
	if task.CredentialID == nil {
		return directchargecontract.UpstreamOrderRequest{}, false
	}
	credential, err := s.store.CredentialByID(*task.CredentialID)
	if err != nil || !credential.Usable(s.now()) {
		return directchargecontract.UpstreamOrderRequest{}, false
	}
	spec, err := s.store.SpecByID(task.SpecID)
	if err != nil || spec == nil {
		return directchargecontract.UpstreamOrderRequest{}, false
	}
	secret, err := s.vault.OpenCredential(credential.Ciphertext, credential.KeyVersion)
	if err != nil {
		return directchargecontract.UpstreamOrderRequest{}, false
	}
	request := directchargecontract.UpstreamOrderRequest{
		Product:         task.Product,
		Plan:            task.Plan,
		CardID:          attempt.CardID,
		PaymentCountry:  spec.PaymentCountry,
		PaymentCurrency: spec.PaymentCurrency,
		Mode:            secret.Mode,
		Credential:      secret.Credential,
		PreflightToken:  secret.PreflightToken,
		ClientRequestID: attempt.ClientRequestID,
		PricingVersion:  task.PricingVersion,
	}
	if requestDigest(request, task.AccountFingerprint) != attempt.RequestDigest {
		// 旧请求号不能绑定新参数
		return directchargecontract.UpstreamOrderRequest{}, false
	}
	return request, true
}

// requestDigest 业务参数摘要（不含凭据与令牌），确认重放的是同一语义请求
func requestDigest(request directchargecontract.UpstreamOrderRequest, fingerprint string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%s|%d|%s|%s|%s|%d|%s|%s",
		request.Product, request.Plan, request.CardID, request.PaymentCountry, request.PaymentCurrency,
		request.Mode, request.PricingVersion, request.ClientRequestID, fingerprint)))
	return hex.EncodeToString(sum[:])
}

func retryableKind(kind string) bool {
	switch kind {
	case directchargecontract.UpstreamKindUnavailable, directchargecontract.UpstreamKindTransient,
		directchargecontract.UpstreamKindFunds, directchargecontract.UpstreamKindConfig:
		return true
	}
	return false
}
