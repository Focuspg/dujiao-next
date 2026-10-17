package application

import (
	"github.com/dujiao-next/internal/logger"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

const webhookSourceZovoCard = "zovocard"

// HandleWebhook 校验签名后把回调写入收件箱（按事件去重），并唤醒对应任务立即查单。
// 回调本身不改变任务结果，成功与失败只以查单为准。
func (s *Service) HandleWebhook(rawBody []byte, signature string) error {
	if !s.Available() {
		return directchargecontract.ErrDisabled
	}
	event, err := s.upstream.ParseWebhook(rawBody, signature)
	if err != nil {
		return err
	}
	now := s.now()
	record := &directchargedomain.WebhookEvent{
		Source:          webhookSourceZovoCard,
		DedupKey:        truncateText(event.DedupKey, 191),
		EventType:       truncateText(event.Type, 64),
		BodyHash:        event.BodyHash,
		UpstreamOrderID: event.UpstreamOrderID,
		ClientRequestID: truncateText(event.ClientRequestID, 80),
		UpstreamStatus:  truncateText(event.Status, 32),
		Status:          directchargedomain.EventPending,
		ReceivedAt:      now,
	}
	if !event.Relevant {
		record.Status = directchargedomain.EventIgnored
	}
	created, err := s.store.SaveWebhookEvent(record)
	if err != nil {
		return err
	}
	if !created || !event.Relevant {
		return nil
	}
	s.matchEvent(record)
	return nil
}

// rematchWebhookEvents 回调可能早于建单响应到达，每轮重新匹配最近未匹配的事件
func (s *Service) rematchWebhookEvents() {
	events, err := s.store.UnmatchedWebhookEvents(s.now().Add(-webhookRematchWindow), cycleTaskLimit)
	if err != nil {
		logger.Warnw("directcharge_webhook_rematch_list_failed", "error", err)
		return
	}
	for i := range events {
		s.matchEvent(&events[i])
	}
}

func (s *Service) matchEvent(event *directchargedomain.WebhookEvent) {
	var attempt *directchargedomain.Attempt
	var err error
	if event.ClientRequestID != "" {
		attempt, err = s.store.AttemptByClientRequestID(event.ClientRequestID)
	}
	if err == nil && attempt == nil && event.UpstreamOrderID > 0 {
		attempt, err = s.store.AttemptByUpstreamOrderID(event.UpstreamOrderID)
	}
	if err != nil {
		logger.Warnw("directcharge_webhook_match_failed", "event_id", event.ID, "error", err)
		return
	}
	now := s.now()
	status := directchargedomain.EventProcessed
	var taskID *uint
	switch {
	case attempt == nil:
		status = directchargedomain.EventUnmatched
	case attempt.UpstreamOrderID > 0 && event.UpstreamOrderID > 0 && attempt.UpstreamOrderID != event.UpstreamOrderID:
		// 请求号相同但上游订单号不同：不采信，记录后忽略
		logger.Warnw("directcharge_webhook_order_mismatch", "event_id", event.ID, "attempt_id", attempt.ID)
		status = directchargedomain.EventIgnored
	default:
		id := attempt.TaskID
		taskID = &id
		if err := s.store.WakeTask(attempt.TaskID, now); err != nil {
			logger.Warnw("directcharge_webhook_wake_failed", "event_id", event.ID, "task_id", attempt.TaskID, "error", err)
			return
		}
	}
	if status == event.Status && taskID == nil {
		return
	}
	if err := s.store.MarkWebhookEvent(event.ID, status, taskID, now); err != nil {
		logger.Warnw("directcharge_webhook_mark_failed", "event_id", event.ID, "error", err)
	}
}
