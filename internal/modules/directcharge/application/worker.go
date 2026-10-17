package application

import (
	"context"
	"math/rand"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// 后台推进参数（需求 14.2 的建议值）
const (
	cycleTaskLimit        = 10
	activationLimit       = 100
	orphanScanLimit       = 20
	staleAfter            = 2 * time.Minute
	processingWarnAfter   = 10 * time.Minute
	processingReviewAfter = 30 * time.Minute
	unknownReviewAfter    = 30 * time.Minute
	tokenSafetyMargin     = 30 * time.Second
	tokenFallbackLifetime = 9 * time.Minute
	findOrderPages        = 3
	upstreamCallTimeout   = 45 * time.Second
	webhookRematchWindow  = 24 * time.Hour

	retryShort = time.Minute
	retryLong  = 5 * time.Minute

	actorWorker  = "worker"
	actorWebhook = "webhook"
)

// 上游查单间隔：15 秒起，逐步放宽到 2 分钟，再加随机抖动
var pollDelays = []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second, 120 * time.Second}

// 告警类型
const (
	alertCredentialRequired = "direct_charge_credential_required"
	alertReview             = "direct_charge_review"
	alertFailed             = "direct_charge_failed"
	alertSlow               = "direct_charge_slow"
	alertUpstreamBlocked    = "direct_charge_upstream_blocked"
	alertOrphanOrder        = "direct_charge_orphan_order"
)

var dueStatuses = []string{
	directchargedomain.TaskReady,
	directchargedomain.TaskSubmitUnknown,
	directchargedomain.TaskProcessing,
	directchargedomain.TaskRequiresAction,
}

// RunCycle 执行一轮后台推进：激活已付款任务、恢复中断的任务、提交/重放/查单、补偿扫描。
// 由队列定时任务调用；同一进程内不会重叠执行，每个任务的写入都比较版本。
func (s *Service) RunCycle(ctx context.Context) error {
	if !s.Available() {
		return nil
	}
	if !s.cycleMu.TryLock() {
		return nil
	}
	defer s.cycleMu.Unlock()

	s.activatePaidTasks()
	s.recoverStaleTasks()
	s.rematchWebhookEvents()

	tasks, err := s.store.TasksDue(dueStatuses, s.now(), cycleTaskLimit)
	if err != nil {
		return err
	}
	for i := range tasks {
		if ctx.Err() != nil {
			break
		}
		task := tasks[i]
		switch task.Status {
		case directchargedomain.TaskReady:
			s.submit(ctx, &task)
		case directchargedomain.TaskSubmitUnknown:
			s.recoverUnknown(ctx, &task)
		default:
			s.poll(ctx, &task)
		}
	}
	s.scanOrphanOrders()
	return nil
}

// activatePaidTasks 订单付款后把任务从待付款推进到待调度；订单取消则关闭任务
func (s *Service) activatePaidTasks() {
	tasks, err := s.store.TasksByStatus(directchargedomain.TaskAwaitingPayment, activationLimit)
	if err != nil {
		logger.Warnw("directcharge_activate_list_failed", "error", err)
		return
	}
	for i := range tasks {
		task := &tasks[i]
		status, err := s.orders.Status(task.OrderID)
		if err != nil {
			logger.Warnw("directcharge_activate_order_status_failed", "task_id", task.ID, "error", err)
			continue
		}
		switch status {
		case constants.OrderStatusPendingPayment:
		case constants.OrderStatusPaid, constants.OrderStatusFulfilling:
			now := s.now()
			change := s.change(task, directchargedomain.TaskReady, "payment confirmed")
			change.Fields["paid_at"] = now
			change.Fields["next_check_at"] = now
			s.apply(task, change)
		case constants.OrderStatusCanceled, "":
			if _, err := s.closeIfOrderCanceled(task); err != nil {
				logger.Warnw("directcharge_activate_close_failed", "task_id", task.ID, "error", err)
			}
		default:
			s.toReview(task, "order status "+status+" before task activation")
		}
	}
}

// recoverStaleTasks 进程在提交或预检途中退出时，任务会停在中间状态：
// 提交中的转为“结果待核验”（请求可能已发出），预检中的退回待调度。
func (s *Service) recoverStaleTasks() {
	before := s.now().Add(-staleAfter)
	submitting, err := s.store.StaleTasks(directchargedomain.TaskSubmitting, before, cycleTaskLimit)
	if err != nil {
		logger.Warnw("directcharge_stale_list_failed", "error", err)
	}
	for i := range submitting {
		task := &submitting[i]
		change := s.change(task, directchargedomain.TaskSubmitUnknown, "submit interrupted, result unknown")
		change.Fields["next_check_at"] = s.now()
		if task.SubmittedAt == nil {
			change.Fields["submitted_at"] = s.now()
		}
		if attempt := s.currentAttempt(task); attempt != nil && attempt.Status == directchargedomain.AttemptPrepared {
			attempt.Status = directchargedomain.AttemptUnknown
			attempt.UpdatedAt = s.now()
			change.Attempt = attempt
		}
		s.apply(task, change)
	}
	preflighting, err := s.store.StaleTasks(directchargedomain.TaskPreflighting, before, cycleTaskLimit)
	if err != nil {
		logger.Warnw("directcharge_stale_list_failed", "error", err)
	}
	for i := range preflighting {
		task := &preflighting[i]
		change := s.change(task, directchargedomain.TaskReady, "preflight interrupted")
		change.Fields["next_check_at"] = s.now()
		s.apply(task, change)
	}
}

// scanOrphanOrders 已付款却没有可推进任务的代充订单（建任务失败、任务已按未付款关闭后才到账等）只能人工处理，提醒站长一次
func (s *Service) scanOrphanOrders() {
	orphans, err := s.store.PaidOrdersWithoutTask(orphanScanLimit)
	if err != nil {
		logger.Warnw("directcharge_orphan_scan_failed", "error", err)
		return
	}
	for _, orphan := range orphans {
		if s.alertedOrphans[orphan.OrderID] {
			continue
		}
		s.alertedOrphans[orphan.OrderID] = true
		logger.Warnw("directcharge_orphan_order", "order_id", orphan.OrderID, "order_no", orphan.OrderNo, "status", orphan.Status)
		s.sendAlert(directchargecontract.Alert{
			Type: alertOrphanOrder, Level: directchargecontract.AlertCritical, OrderNo: orphan.OrderNo,
			Message: "代充订单已付款但没有可执行的充值任务，请人工核对后充值或退款",
		})
	}
}

func (s *Service) currentAttempt(task *directchargedomain.Task) *directchargedomain.Attempt {
	if task.CurrentAttemptID == nil {
		return nil
	}
	attempt, err := s.store.AttemptByID(*task.CurrentAttemptID)
	if err != nil {
		logger.Warnw("directcharge_attempt_load_failed", "task_id", task.ID, "error", err)
		return nil
	}
	return attempt
}

func (s *Service) change(task *directchargedomain.Task, to, reason string) directchargecontract.TaskChange {
	return directchargecontract.TaskChange{
		TaskID: task.ID, FromStatus: task.Status, FromVersion: task.Version, ToStatus: to,
		Reason: reason, Actor: actorWorker, Now: s.now(), Fields: map[string]interface{}{},
	}
}

// apply 校验状态迁移后比较版本写入；成功时同步内存中的任务
func (s *Service) apply(task *directchargedomain.Task, change directchargecontract.TaskChange) bool {
	if change.FromStatus != change.ToStatus && !directchargedomain.CanTransition(change.FromStatus, change.ToStatus) {
		logger.Errorw("directcharge_transition_not_allowed", "task_id", task.ID, "from", change.FromStatus, "to", change.ToStatus)
		return false
	}
	ok, err := s.store.TransitionTask(change)
	if err != nil {
		logger.Warnw("directcharge_transition_failed", "task_id", task.ID, "from", change.FromStatus, "to", change.ToStatus, "error", err)
		return false
	}
	if !ok {
		logger.Infow("directcharge_transition_skipped_version_changed", "task_id", task.ID, "from", change.FromStatus, "to", change.ToStatus)
		return false
	}
	if change.FromStatus != change.ToStatus {
		logger.Infow("directcharge_task_transition", "task_id", task.ID, "order_no", task.OrderNo, "from", change.FromStatus, "to", change.ToStatus, "reason", change.Reason)
	}
	task.Status = change.ToStatus
	task.Version = change.FromVersion + 1
	return true
}

// deferTask 保持当前状态，稍后再检查
func (s *Service) deferTask(task *directchargedomain.Task, after time.Duration, reason string) {
	change := s.change(task, task.Status, reason)
	change.Fields["next_check_at"] = s.now().Add(after)
	s.apply(task, change)
}

// toReview 转人工核验并提醒站长
func (s *Service) toReview(task *directchargedomain.Task, reason string) {
	change := s.change(task, directchargedomain.TaskReview, reason)
	change.Fields["error_message"] = truncateText(reason, 500)
	if s.apply(task, change) {
		s.alertTask(task, alertReview, directchargecontract.AlertCritical, "代充任务转人工核验："+reason)
	}
}

func (s *Service) alertTask(task *directchargedomain.Task, alertType, level, message string) {
	s.sendAlert(directchargecontract.Alert{Type: alertType, Level: level, TaskID: task.ID, OrderNo: task.ParentOrderNo, Message: message})
}

func (s *Service) sendAlert(alert directchargecontract.Alert) {
	logger.Warnw("directcharge_alert", "type", alert.Type, "level", alert.Level, "task_id", alert.TaskID, "order_no", alert.OrderNo, "message", alert.Message)
	if s.alerter == nil {
		return
	}
	if err := s.alerter.Alert(alert); err != nil {
		logger.Warnw("directcharge_alert_enqueue_failed", "type", alert.Type, "task_id", alert.TaskID, "error", err)
	}
}

// pollDelay 第 n 次查单后的等待时间，带 ±20% 抖动
func pollDelay(checkCount int) time.Duration {
	if checkCount < 0 {
		checkCount = 0
	}
	if checkCount >= len(pollDelays) {
		checkCount = len(pollDelays) - 1
	}
	base := pollDelays[checkCount]
	jitter := time.Duration(rand.Int63n(int64(base)*2/5+1)) - base/5
	return base + jitter
}

func truncateText(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max])
}
