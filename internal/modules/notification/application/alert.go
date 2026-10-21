package application

import (
	"context"
	"time"

	"github.com/dujiao-next/internal/logger"
	"github.com/dujiao-next/internal/modules/notification/application/format"
	settingsmessaging "github.com/dujiao-next/internal/modules/settings/schema/messaging"
	"github.com/dujiao-next/internal/queue"
	"github.com/shopspring/decimal"
)

// upstreamBalanceQueryTimeout 单次上游余额查询超时，避免拖住整轮巡检
const upstreamBalanceQueryTimeout = 15 * time.Second

func (s *Service) dispatchExceptionAlertCheck(ctx context.Context, setting settingsmessaging.NotificationCenterSetting, payload queue.NotificationDispatchPayload) error {
	if s.dashboardSvc == nil || s.settingService == nil {
		return nil
	}

	dashboardSetting, err := s.settingService.GetDashboardSetting()
	if err != nil {
		return err
	}

	var firstErr error
	inventoryAlerts, err := s.dashboardSvc.GetInventoryAlertItems(ctx, dashboardSetting.Alert.LowStockThreshold)
	if err != nil {
		return err
	}
	for _, itemPayload := range format.BuildInventoryAlertDispatchPayloads(setting, dashboardSetting, payload, inventoryAlerts) {
		allowed, intervalErr := acquireInventoryAlertInterval(ctx, setting.InventoryAlertIntervalSeconds, itemPayload)
		if intervalErr != nil {
			logger.Warnw("notification_inventory_alert_interval_failed", "error", intervalErr)
		}
		if intervalErr == nil && !allowed {
			continue
		}
		if err := s.dispatchSingleEvent(ctx, setting, itemPayload); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	paymentOrderAlertNow := time.Now()
	paymentOrderAlertStart := paymentOrderAlertNow.Add(-time.Duration(setting.PaymentOrderAlertCheckSeconds) * time.Second)
	paymentOrderCounts, err := s.dashboardSvc.GetPaymentOrderAlertCounts(ctx, paymentOrderAlertStart, paymentOrderAlertNow)
	if err != nil {
		return err
	}

	for _, itemPayload := range format.BuildPaymentOrderAlertDispatchPayloads(setting, dashboardSetting, payload, paymentOrderCounts) {
		allowed, intervalErr := acquirePaymentOrderAlertInterval(ctx, setting.PaymentOrderAlertIntervalSeconds, itemPayload)
		if intervalErr != nil {
			logger.Warnw("notification_payment_order_alert_interval_failed", "error", intervalErr)
		}
		if intervalErr == nil && !allowed {
			continue
		}
		if err := s.dispatchSingleEvent(ctx, setting, itemPayload); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	if err := s.dispatchUpstreamBalanceCheck(ctx, setting, payload); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// dispatchUpstreamBalanceCheck 巡检 ZovoCard 账户可消费余额与付款卡可用额度。
// 不在任何数据库事务内执行；查询频率由“查询间隔”门控，同一项告警按“重复提醒间隔”限流，
// 余额恢复或查询恢复后立即释放对应限流，下次再出问题会马上提醒。
func (s *Service) dispatchUpstreamBalanceCheck(ctx context.Context, setting settingsmessaging.NotificationCenterSetting, payload queue.NotificationDispatchPayload) error {
	if s.upstreamBalance == nil || s.alertLocks == nil || !setting.UpstreamBalanceAlertEnabled {
		return nil
	}
	accountThreshold, checkAccount := setting.UpstreamAccountBalanceThreshold()
	cardThreshold, checkCards := setting.UpstreamCardBalanceThreshold()
	if !checkAccount && !checkCards {
		return nil
	}

	checkInterval := time.Duration(settingsmessaging.NormalizeNotificationUpstreamBalanceAlertCheckMinutes(setting.UpstreamBalanceAlertCheckMinutes)) * time.Minute
	due, err := s.alertLocks.Acquire(ctx, upstreamBalanceCheckLockKey, checkInterval)
	if err != nil {
		logger.Warnw("notification_upstream_balance_check_gate_failed", "error", err)
	}
	if err == nil && !due {
		return nil
	}

	var firstErr error
	record := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if checkAccount {
		queryCtx, cancel := context.WithTimeout(ctx, upstreamBalanceQueryTimeout)
		balance, queryErr := s.upstreamBalance.AccountBalance(queryCtx)
		cancel()
		input := format.UpstreamBalanceAlertInput{
			Value:     balance.Spendable,
			Threshold: accountThreshold,
			Total:     balance.Balance,
			HasTotal:  true,
		}
		record(s.handleUpstreamBalanceResult(ctx, setting, payload, input, queryErr))
	}
	if checkCards {
		cardIDs, listErr := s.upstreamBalance.MonitoredCardIDs(ctx)
		if listErr != nil {
			logger.Warnw("notification_upstream_card_list_failed", "error", listErr)
		}
		for _, cardID := range cardIDs {
			if cardID <= 0 {
				continue
			}
			queryCtx, cancel := context.WithTimeout(ctx, upstreamBalanceQueryTimeout)
			card, queryErr := s.upstreamBalance.CardBalance(queryCtx, cardID)
			cancel()
			input := format.UpstreamBalanceAlertInput{
				CardID:    cardID,
				Value:     card.Available,
				Threshold: cardThreshold,
				Stale:     card.Stale,
				AsOf:      card.AsOf,
			}
			record(s.handleUpstreamBalanceResult(ctx, setting, payload, input, queryErr))
		}
	}
	return firstErr
}

// handleUpstreamBalanceResult 根据一项查询结果决定发送“余额不足”“查询失败”告警或释放限流。
func (s *Service) handleUpstreamBalanceResult(
	ctx context.Context,
	setting settingsmessaging.NotificationCenterSetting,
	payload queue.NotificationDispatchPayload,
	input format.UpstreamBalanceAlertInput,
	queryErr error,
) error {
	target := format.UpstreamBalanceTargetKey(input.CardID)
	repeatHours := settingsmessaging.NormalizeNotificationUpstreamBalanceAlertRepeatHours(setting.UpstreamBalanceAlertRepeatHours)
	input.RepeatHours = repeatHours
	repeatInterval := time.Duration(repeatHours) * time.Hour
	lowKey := upstreamBalanceLowLockPrefix + target
	failureKey := upstreamBalanceFailureLockPrefix + target

	if queryErr != nil {
		// 查询失败不等于余额不足：单独告警，不动余额不足的限流
		logger.Warnw("notification_upstream_balance_query_failed", "target", target, "error", queryErr)
		input.QueryError = queryErr.Error()
		input.Value = decimal.Zero
		return s.dispatchUpstreamBalanceAlert(ctx, setting, failureKey, repeatInterval,
			format.BuildUpstreamBalanceQueryFailedDispatchPayload(setting, payload, input))
	}
	s.releaseAlertLock(ctx, failureKey)

	if !format.UpstreamBalanceIsLow(input.Value, input.Threshold) {
		s.releaseAlertLock(ctx, lowKey)
		return nil
	}
	return s.dispatchUpstreamBalanceAlert(ctx, setting, lowKey, repeatInterval,
		format.BuildUpstreamBalanceLowDispatchPayload(setting, payload, input))
}

// dispatchUpstreamBalanceAlert 在限流窗口外发送告警；发送失败时释放限流，下次巡检重试。
func (s *Service) dispatchUpstreamBalanceAlert(
	ctx context.Context,
	setting settingsmessaging.NotificationCenterSetting,
	lockKey string,
	repeatInterval time.Duration,
	itemPayload queue.NotificationDispatchPayload,
) error {
	allowed, err := s.alertLocks.Acquire(ctx, lockKey, repeatInterval)
	if err != nil {
		logger.Warnw("notification_upstream_balance_alert_interval_failed", "key", lockKey, "error", err)
	}
	if err == nil && !allowed {
		return nil
	}
	// 本项已过限流窗口，跳过通用短时去重，避免恢复后再次下跌的同值告警被吞掉
	itemPayload.Force = true
	if sendErr := s.dispatchSingleEvent(ctx, setting, itemPayload); sendErr != nil {
		s.releaseAlertLock(ctx, lockKey)
		return sendErr
	}
	return nil
}

func (s *Service) releaseAlertLock(ctx context.Context, key string) {
	if err := s.alertLocks.Release(ctx, key); err != nil {
		logger.Warnw("notification_upstream_balance_alert_release_failed", "key", key, "error", err)
	}
}
