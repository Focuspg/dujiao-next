package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// 后台查单结果
const (
	ReconcileWoken      = "woken"       // 任务仍在自动推进，已安排立即查单
	ReconcileUpdated    = "updated"     // 已按上游最新结果更新任务
	ReconcileNotFound   = "not_found"   // 上游没有找到该请求号的订单
	ReconcileNoProgress = "no_progress" // 查到了，但状态没有变化
)

// 人工处理结论
const (
	ResolveSucceeded = "succeeded"
	ResolveFailed    = "failed"
)

const reconcileFindPages = 5

// ReconcileResult 后台查单结果与最新详情
type ReconcileResult struct {
	Result string      `json:"result"`
	Detail *TaskDetail `json:"detail"`
}

func adminActor(adminID uint) string { return fmt.Sprintf("admin:%d", adminID) }

func (s *Service) adminTask(id uint) (*directchargedomain.Task, error) {
	if !s.Available() {
		return nil, directchargecontract.ErrDisabled
	}
	task, err := s.store.TaskByID(id)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, directchargecontract.ErrNotFound
	}
	return task, nil
}

// Reconcile 后台“查询原单”：进行中的任务安排立即查单；待核验任务直接向上游查询，
// 按查到的结果推进（仍在处理则交回自动查单）。不会创建新的上游尝试。
func (s *Service) Reconcile(ctx context.Context, id, adminID uint) (*ReconcileResult, error) {
	task, err := s.adminTask(id)
	if err != nil {
		return nil, err
	}
	result := ReconcileNoProgress
	switch task.Status {
	case directchargedomain.TaskProcessing, directchargedomain.TaskRequiresAction, directchargedomain.TaskSubmitUnknown:
		if err := s.store.WakeTask(task.ID, s.now()); err != nil {
			return nil, err
		}
		result = ReconcileWoken
	case directchargedomain.TaskReview:
		attempt := s.currentAttempt(task)
		if attempt == nil {
			return nil, directchargecontract.ErrActionNotAllowed
		}
		if result, err = s.reconcileReview(ctx, task, attempt, adminID); err != nil {
			return nil, err
		}
	default:
		return nil, directchargecontract.ErrActionNotAllowed
	}
	detail, err := s.TaskDetail(id)
	if err != nil {
		return nil, err
	}
	return &ReconcileResult{Result: result, Detail: detail}, nil
}

func (s *Service) reconcileReview(ctx context.Context, task *directchargedomain.Task, attempt *directchargedomain.Attempt, adminID uint) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, upstreamCallTimeout)
	defer cancel()
	if attempt.UpstreamOrderID <= 0 {
		if !attempt.IsOpen() {
			// 上游从未受理：没有原单可查，应重试或退款
			return "", directchargecontract.ErrActionNotAllowed
		}
		order, err := s.upstream.FindOrder(callCtx, attempt.ClientRequestID, reconcileFindPages)
		if err != nil {
			return "", directchargecontract.ErrUpstreamBusy
		}
		if order == nil {
			return ReconcileNotFound, nil
		}
		version := task.Version
		s.afterSubmit(task, attempt, order, nil, true)
		return reconcileOutcome(task, version), nil
	}
	order, err := s.upstream.GetOrder(callCtx, attempt.UpstreamOrderID)
	if err != nil {
		return "", directchargecontract.ErrUpstreamBusy
	}
	version := task.Version
	if order.Outcome == directchargecontract.UpstreamOutcomePending {
		// 上游仍在处理：交回自动查单，并重新开始超时计时
		now := s.now()
		fillAttempt(attempt, order, now)
		change := s.change(task, directchargedomain.TaskProcessing, "admin reconcile: upstream still processing")
		change.Actor = adminActor(adminID)
		change.Attempt = attempt
		change.Fields["next_check_at"] = now
		change.Fields["check_count"] = 0
		change.Fields["submitted_at"] = now
		change.Fields["alerted_at"] = nil
		s.apply(task, change)
		return reconcileOutcome(task, version), nil
	}
	s.applyUpstreamOrder(task, attempt, order)
	return reconcileOutcome(task, version), nil
}

func reconcileOutcome(task *directchargedomain.Task, versionBefore int) string {
	if task.Version != versionBefore {
		return ReconcileUpdated
	}
	return ReconcileNoProgress
}

// Retry 后台“重放内部处理”：仅限待核验任务，且上一次尝试已确认未被上游受理或已确认失败；
// 任务回到待调度，由后台用新的尝试号重新预检并提交。必须填写理由。
func (s *Service) Retry(id uint, reason string, adminID uint) (*TaskDetail, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, directchargecontract.ErrReasonRequired
	}
	task, err := s.adminTask(id)
	if err != nil {
		return nil, err
	}
	if task.Status != directchargedomain.TaskReview || task.RefundLocked || task.RefundStatus == directchargedomain.RefundCompleted {
		return nil, directchargecontract.ErrActionNotAllowed
	}
	if attempt := s.currentAttempt(task); attempt != nil {
		if attempt.IsOpen() || attempt.Status == directchargedomain.AttemptSucceeded {
			return nil, directchargecontract.ErrAttemptOpen
		}
	}
	change := s.change(task, directchargedomain.TaskReady, "admin retry: "+reason)
	change.Actor = adminActor(adminID)
	change.Fields["next_check_at"] = s.now()
	change.Fields["error_kind"] = ""
	change.Fields["error_message"] = ""
	if !s.apply(task, change) {
		return nil, directchargecontract.ErrActionNotAllowed
	}
	return s.TaskDetail(id)
}

// Resolve 人工核验后结案：确认成功时交付订单，确认失败时任务终结并可执行退款。必须填写理由。
func (s *Service) Resolve(id uint, outcome, reason string, adminID uint) (*TaskDetail, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, directchargecontract.ErrReasonRequired
	}
	task, err := s.adminTask(id)
	if err != nil {
		return nil, err
	}
	if task.Status != directchargedomain.TaskReview {
		return nil, directchargecontract.ErrActionNotAllowed
	}
	now := s.now()
	attempt := s.currentAttempt(task)
	var change directchargecontract.TaskChange
	switch outcome {
	case ResolveSucceeded:
		if task.RefundLocked || task.RefundStatus == directchargedomain.RefundCompleted {
			return nil, directchargecontract.ErrActionNotAllowed
		}
		upstreamID := int64(0)
		if attempt != nil {
			upstreamID = attempt.UpstreamOrderID
		}
		payload := deliveryPayload(task, &directchargecontract.UpstreamOrder{ID: upstreamID})
		if err := s.orders.Deliver(task.OrderID, payload); err != nil {
			logger.Warnw("directcharge_admin_deliver_failed", "task_id", task.ID, "error", err)
			return nil, err
		}
		change = s.change(task, directchargedomain.TaskSucceeded, "admin confirmed success: "+reason)
		if attempt != nil {
			attempt.Status = directchargedomain.AttemptSucceeded
		}
	case ResolveFailed:
		change = s.change(task, directchargedomain.TaskFailedConfirmed, "admin confirmed failure: "+reason)
		change.Fields["error_message"] = truncateText("admin confirmed failure: "+reason, 500)
		if attempt != nil && attempt.Status != directchargedomain.AttemptRejected && attempt.Status != directchargedomain.AttemptCancelled {
			attempt.Status = directchargedomain.AttemptFailed
		}
	default:
		return nil, directchargecontract.ErrActionNotAllowed
	}
	if attempt != nil {
		attempt.CompletedAt = &now
		attempt.UpdatedAt = now
		change.Attempt = attempt
	}
	change.Actor = adminActor(adminID)
	change.Fields["completed_at"] = now
	change.Fields["next_check_at"] = nil
	if !s.apply(task, change) {
		return nil, directchargecontract.ErrActionNotAllowed
	}
	return s.TaskDetail(id)
}

// Refund 后台审批退款：先加退款锁并终结任务（阻止任何后续提交），再按订单剩余可退金额登记退款。
// 首版退款由财务在支付平台原路退回后登记，remark 填写退款流水号等凭证。
func (s *Service) Refund(id uint, remark string, adminID uint) (*TaskDetail, error) {
	remark = strings.TrimSpace(remark)
	if remark == "" {
		return nil, directchargecontract.ErrReasonRequired
	}
	task, err := s.adminTask(id)
	if err != nil {
		return nil, err
	}
	if task.RefundStatus == directchargedomain.RefundCompleted {
		return s.TaskDetail(id)
	}
	target := task.Status
	switch task.Status {
	case directchargedomain.TaskFailedConfirmed, directchargedomain.TaskCancelledConfirmed:
	case directchargedomain.TaskReview, directchargedomain.TaskWaitingCredential, directchargedomain.TaskReady:
		if attempt := s.currentAttempt(task); attempt != nil && (attempt.IsOpen() || attempt.Status == directchargedomain.AttemptSucceeded) {
			return nil, directchargecontract.ErrAttemptOpen
		}
		target = directchargedomain.TaskFailedConfirmed
	default:
		return nil, directchargecontract.ErrRefundNotAllowed
	}
	actor := adminActor(adminID)
	now := s.now()
	lock := s.change(task, target, "admin approved refund: "+remark)
	lock.Actor = actor
	lock.Fields["refund_locked"] = true
	lock.Fields["refund_remark"] = truncateText(remark, 500)
	if target != task.Status {
		lock.Fields["completed_at"] = now
		lock.Fields["next_check_at"] = nil
	}
	if !s.apply(task, lock) {
		return nil, directchargecontract.ErrActionNotAllowed
	}
	if err := s.orders.Refund(task.OrderID, fmt.Sprintf("direct charge refund %s: %s", task.ParentOrderNo, remark)); err != nil {
		logger.Warnw("directcharge_refund_record_failed", "task_id", task.ID, "order_no", task.OrderNo, "error", err)
		return nil, fmt.Errorf("%w: %v", directchargecontract.ErrRefundFailed, err)
	}
	done := s.change(task, task.Status, "refund recorded")
	done.Actor = actor
	done.Fields["refund_status"] = directchargedomain.RefundCompleted
	done.Fields["refunded_at"] = s.now()
	if !s.apply(task, done) {
		logger.Errorw("directcharge_refund_status_write_failed", "task_id", task.ID)
	}
	return s.TaskDetail(id)
}
