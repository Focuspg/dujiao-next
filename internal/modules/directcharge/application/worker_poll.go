package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// poll 查询已受理的上游订单并推进任务；只有查单结果能决定成功或失败
func (s *Service) poll(ctx context.Context, task *directchargedomain.Task) {
	attempt := s.currentAttempt(task)
	if attempt == nil || attempt.UpstreamOrderID <= 0 {
		s.toReview(task, "processing task without upstream order id")
		return
	}
	callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
	order, err := s.upstream.GetOrder(callCtx, attempt.UpstreamOrderID)
	cancel()
	if err != nil {
		var upstreamErr *directchargecontract.UpstreamError
		if errors.As(err, &upstreamErr) && upstreamErr.Kind == directchargecontract.UpstreamKindNotFound {
			s.toReview(task, "upstream order not found")
			return
		}
		s.deferTask(task, pollDelay(task.CheckCount), "")
		return
	}
	s.applyUpstreamOrder(task, attempt, order)
}

func (s *Service) applyUpstreamOrder(task *directchargedomain.Task, attempt *directchargedomain.Attempt, order *directchargecontract.UpstreamOrder) {
	now := s.now()
	if order == nil || (order.ClientRequestID != "" && order.ClientRequestID != attempt.ClientRequestID) ||
		(order.ID > 0 && order.ID != attempt.UpstreamOrderID) {
		s.toReview(task, "upstream order does not match attempt")
		return
	}
	fillAttempt(attempt, order, now)
	chargedFailure := order.FinalAmountMinor > 0 && !order.FailedBeforeCharge

	switch order.Outcome {
	case directchargecontract.UpstreamOutcomePending:
		s.keepWaiting(task, attempt, now)
	case directchargecontract.UpstreamOutcomeSucceeded:
		// 先交付本地订单（幂等），成功后再把任务记为成功；交付失败下一轮重试
		if err := s.orders.Deliver(task.OrderID, deliveryPayload(task, order)); err != nil {
			logger.Warnw("directcharge_deliver_failed", "task_id", task.ID, "order_id", task.OrderID, "error", err)
			change := s.change(task, task.Status, "")
			change.Attempt = attempt
			change.Fields["next_check_at"] = now.Add(retryShort)
			s.apply(task, change)
			return
		}
		attempt.Status = directchargedomain.AttemptSucceeded
		attempt.CompletedAt = &now
		change := s.change(task, directchargedomain.TaskSucceeded, "upstream completed")
		change.Attempt = attempt
		change.Fields["completed_at"] = now
		change.Fields["next_check_at"] = nil
		change.Fields["error_kind"] = ""
		change.Fields["error_message"] = ""
		s.apply(task, change)
	case directchargecontract.UpstreamOutcomeFailed, directchargecontract.UpstreamOutcomeCancelled:
		attempt.CompletedAt = &now
		attempt.Status = directchargedomain.AttemptFailed
		target := directchargedomain.TaskFailedConfirmed
		if order.Outcome == directchargecontract.UpstreamOutcomeCancelled {
			attempt.Status = directchargedomain.AttemptCancelled
			target = directchargedomain.TaskCancelledConfirmed
		}
		if chargedFailure {
			// 上游报告失败但有实付金额：不能直接退款，转人工核对
			s.toReviewWithAttempt(task, attempt, "upstream "+order.Status+" with final amount, needs reconciliation")
			return
		}
		reason := "upstream " + order.Status
		change := s.change(task, target, reason)
		change.Attempt = attempt
		change.Fields["completed_at"] = now
		change.Fields["next_check_at"] = nil
		change.Fields["error_message"] = truncateText(strings.TrimSpace(reason+" "+order.Message), 500)
		if s.apply(task, change) {
			s.alertTask(task, alertFailed, directchargecontract.AlertWarning, "代充失败且上游确认未扣款（"+order.Status+"），请审批退款")
		}
	default:
		s.toReviewWithAttempt(task, attempt, "upstream status not recognized: "+order.Status)
	}
}

// keepWaiting 上游仍在处理：按退避间隔继续查单，超过 10 分钟提醒一次，超过 30 分钟转人工
func (s *Service) keepWaiting(task *directchargedomain.Task, attempt *directchargedomain.Attempt, now time.Time) {
	started := task.UpdatedAt
	if task.SubmittedAt != nil {
		started = *task.SubmittedAt
	}
	elapsed := now.Sub(started)
	if elapsed >= processingReviewAfter {
		s.toReviewWithAttempt(task, attempt, "upstream still processing after 30 minutes")
		return
	}
	change := s.change(task, task.Status, "")
	change.Attempt = attempt
	change.Fields["check_count"] = task.CheckCount + 1
	change.Fields["next_check_at"] = now.Add(pollDelay(task.CheckCount))
	warn := elapsed >= processingWarnAfter && task.AlertedAt == nil
	if warn {
		change.Fields["alerted_at"] = now
	}
	if s.apply(task, change) && warn {
		s.alertTask(task, alertSlow, directchargecontract.AlertWarning, "代充任务已处理超过 10 分钟仍未完成，请留意上游状态")
	}
}

// fillAttempt 用上游订单快照更新尝试记录
func fillAttempt(attempt *directchargedomain.Attempt, order *directchargecontract.UpstreamOrder, now time.Time) {
	if order.ID > 0 {
		attempt.UpstreamOrderID = order.ID
	}
	attempt.UpstreamStatus = truncateText(order.Status, 32)
	attempt.UpstreamStage = truncateText(order.Stage, 64)
	attempt.UpstreamMessage = truncateText(order.Message, 500)
	if order.Currency != "" {
		attempt.Currency = truncateText(order.Currency, 8)
	}
	if order.QuotedAmountMinor > 0 {
		attempt.QuotedAmountMinor = order.QuotedAmountMinor
	}
	attempt.FinalAmountMinor = order.FinalAmountMinor
	attempt.ServiceFeeMinor = order.ServiceFeeMinor
	attempt.ServiceFeeStatus = truncateText(order.ServiceFeeStatus, 32)
	attempt.FundingHoldStatus = truncateText(order.FundingHoldStatus, 32)
	attempt.LastCheckedAt = &now
	attempt.UpdatedAt = now
}

// deliveryPayload 交付记录里给用户看的内容，不含凭据，不依赖语言
func deliveryPayload(task *directchargedomain.Task, order *directchargecontract.UpstreamOrder) string {
	return fmt.Sprintf("%s %s · %s · #%d", strings.ToUpper(task.Product), task.Plan, task.AccountMasked, order.ID)
}
