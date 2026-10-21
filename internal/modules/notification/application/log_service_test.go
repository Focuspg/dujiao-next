package application

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/modules/notification/application/format"
	"github.com/dujiao-next/internal/modules/notification/contract"
	"github.com/dujiao-next/internal/modules/notification/domain"
	settingsmessaging "github.com/dujiao-next/internal/modules/settings/schema/messaging"
	settingsstorefront "github.com/dujiao-next/internal/modules/settings/schema/storefront"
	"github.com/dujiao-next/internal/queue"
	"github.com/shopspring/decimal"
)

type notificationSettingsStub struct {
	notification settingsmessaging.NotificationCenterSetting
	dashboard    settingsstorefront.DashboardSetting
}

func (s notificationSettingsStub) GetNotificationCenterSetting() (settingsmessaging.NotificationCenterSetting, error) {
	return s.notification, nil
}

func (s notificationSettingsStub) GetDashboardSetting() (settingsstorefront.DashboardSetting, error) {
	return s.dashboard, nil
}

type notificationEmailStub struct{}

func (notificationEmailStub) SendCustomEmail(recipient, _, _ string) error {
	if strings.Contains(recipient, "failure") {
		return errors.New("simulated email failure")
	}
	return nil
}

type notificationFeishuCall struct {
	appID         string
	appSecret     string
	receiveIDType string
	receiveID     string
	message       string
}

type notificationFeishuStub struct {
	calls []notificationFeishuCall
}

func (s *notificationFeishuStub) SendMessage(_ context.Context, appID, appSecret, receiveIDType, receiveID, message string) error {
	s.calls = append(s.calls, notificationFeishuCall{
		appID:         appID,
		appSecret:     appSecret,
		receiveIDType: receiveIDType,
		receiveID:     receiveID,
		message:       message,
	})
	if strings.Contains(receiveID, "failure") {
		return errors.New("simulated feishu failure")
	}
	return nil
}

type notificationLogRepositoryStub struct {
	items []domain.NotificationLog
}

func (r *notificationLogRepositoryStub) Create(item *domain.NotificationLog) error {
	if item != nil {
		copy := *item
		copy.ID = uint(len(r.items) + 1)
		r.items = append(r.items, copy)
	}
	return nil
}

func (r *notificationLogRepositoryStub) ListAdmin(filter contract.LogListFilter) ([]domain.NotificationLog, int64, error) {
	result := make([]domain.NotificationLog, 0, len(r.items))
	for _, item := range r.items {
		if filter.EventType != "" && item.EventType != filter.EventType {
			continue
		}
		if filter.IsTest != nil && item.IsTest != *filter.IsTest {
			continue
		}
		result = append(result, item)
	}
	return result, int64(len(result)), nil
}

func setupLogService(t *testing.T) (*Service, *LogService) {
	t.Helper()
	logService := NewLogService(&notificationLogRepositoryStub{})
	setting := settingsmessaging.NotificationCenterSetting{
		DefaultLocale: constants.LocaleEnUS,
		Scenes: settingsmessaging.NotificationSceneSetting{
			OrderPaidSuccess: true,
			ExceptionAlert:   true,
		},
		Channels: settingsmessaging.NotificationChannelsSetting{
			Email: settingsmessaging.NotificationChannelSetting{
				Enabled:    true,
				Recipients: []string{"success@example.com", "failure@example.com"},
			},
		},
		Templates: settingsmessaging.NotificationTemplatesSetting{
			OrderPaidSuccess: settingsmessaging.NotificationSceneTemplate{
				ENUS: settingsmessaging.NotificationLocalizedTemplate{
					Title: "Order {{order_no}}",
					Body:  "Customer {{customer_email}}",
				},
			},
			ExceptionAlert: settingsmessaging.NotificationSceneTemplate{
				ENUS: settingsmessaging.NotificationLocalizedTemplate{
					Title: "Alert {{message}}",
					Body:  "{{message}}",
				},
			},
		},
	}
	service := NewService(notificationSettingsStub{notification: setting}, notificationEmailStub{}, nil, nil, logService, nil, nil)
	return service, logService
}

func TestServiceSendTestRecordsSuccessLog(t *testing.T) {
	service, logService := setupLogService(t)
	if err := service.SendTest(context.Background(), contract.TestSendInput{
		Channel: "email",
		Target:  "success@example.com",
		Scene:   constants.NotificationEventOrderPaidSuccess,
		Locale:  constants.LocaleEnUS,
	}); err != nil {
		t.Fatalf("SendTest failed: %v", err)
	}

	isTest := true
	items, total, err := logService.ListForAdmin(contract.LogListFilter{
		Page: 1, PageSize: 10, EventType: constants.NotificationEventOrderPaidSuccess, IsTest: &isTest,
	})
	if err != nil {
		t.Fatalf("list notification logs failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 test log, total=%d len=%d", total, len(items))
	}
	if items[0].Status != notificationLogStatusSuccess || !items[0].IsTest {
		t.Fatalf("unexpected log: %#v", items[0])
	}
	if !strings.Contains(items[0].Title, "DJ202603230001") {
		t.Fatalf("title should include sample order no, got %s", items[0].Title)
	}
}

func TestServiceDispatchSingleEventRecordsPerRecipientResult(t *testing.T) {
	service, logService := setupLogService(t)
	setting, err := service.settingService.GetNotificationCenterSetting()
	if err != nil {
		t.Fatalf("get notification center setting failed: %v", err)
	}
	dispatchErr := service.dispatchSingleEvent(context.Background(), setting, queue.NotificationDispatchPayload{
		EventType: constants.NotificationEventOrderPaidSuccess,
		BizType:   constants.NotificationBizTypeOrder,
		BizID:     88,
		Locale:    constants.LocaleEnUS,
		Force:     true,
		Data: map[string]interface{}{
			"order_no":       "DJ-LOG-88",
			"customer_email": "member@example.com",
		},
	})
	if !errors.Is(dispatchErr, contract.ErrSendFailed) {
		t.Fatalf("expected send failure, got %v", dispatchErr)
	}

	items, total, err := logService.ListForAdmin(contract.LogListFilter{Page: 1, PageSize: 10, EventType: constants.NotificationEventOrderPaidSuccess})
	if err != nil {
		t.Fatalf("list notification logs failed: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected 2 recipient logs, total=%d len=%d", total, len(items))
	}
	statuses := map[string]string{}
	for _, item := range items {
		statuses[item.Recipient] = item.Status
	}
	if statuses["success@example.com"] != notificationLogStatusSuccess {
		t.Fatalf("success recipient status mismatch: %v", statuses)
	}
	if statuses["failure@example.com"] != notificationLogStatusFailed {
		t.Fatalf("failure recipient status mismatch: %v", statuses)
	}
}

func TestServiceDispatchSingleEventSendsFeishuAndRecordsEachRecipient(t *testing.T) {
	logService := NewLogService(&notificationLogRepositoryStub{})
	feishuSender := &notificationFeishuStub{}
	setting := settingsmessaging.NotificationCenterDefaultSetting()
	setting.DefaultLocale = constants.LocaleZhCN
	setting.Channels.Feishu = settingsmessaging.FeishuNotificationChannelSetting{
		Enabled:       true,
		AppID:         "cli_demo",
		AppSecret:     "secret",
		ReceiveIDType: settingsmessaging.FeishuReceiveIDTypeChatID,
		Recipients:    []string{"oc_success", "oc_failure"},
	}
	service := NewService(notificationSettingsStub{notification: setting}, nil, nil, nil, logService, nil, feishuSender)

	err := service.dispatchSingleEvent(context.Background(), setting, queue.NotificationDispatchPayload{
		EventType: constants.NotificationEventOrderPaidSuccess,
		BizType:   constants.NotificationBizTypeOrder,
		BizID:     99,
		Locale:    constants.LocaleZhCN,
		Force:     true,
		Data: map[string]interface{}{
			"order_no":       "DJ-FEISHU-99",
			"customer_email": "member@example.com",
		},
	})
	if !errors.Is(err, contract.ErrSendFailed) {
		t.Fatalf("expected one feishu failure, got %v", err)
	}
	if len(feishuSender.calls) != 2 {
		t.Fatalf("expected two feishu calls, got %d", len(feishuSender.calls))
	}
	firstCall := feishuSender.calls[0]
	if firstCall.appID != "cli_demo" || firstCall.appSecret != "secret" || firstCall.receiveIDType != settingsmessaging.FeishuReceiveIDTypeChatID {
		t.Fatalf("unexpected feishu credentials or id type: %#v", firstCall)
	}
	if !strings.Contains(firstCall.message, "DJ-FEISHU-99") {
		t.Fatalf("rendered feishu message missing order number: %q", firstCall.message)
	}

	items, total, listErr := logService.ListForAdmin(contract.LogListFilter{
		Page: 1, PageSize: 10, Channel: constants.NotificationChannelFeishu,
	})
	if listErr != nil {
		t.Fatalf("list feishu notification logs: %v", listErr)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected two feishu logs, total=%d len=%d", total, len(items))
	}
	statuses := map[string]string{}
	for _, item := range items {
		statuses[item.Recipient] = item.Status
	}
	if statuses["oc_success"] != notificationLogStatusSuccess || statuses["oc_failure"] != notificationLogStatusFailed {
		t.Fatalf("unexpected feishu log statuses: %#v", statuses)
	}
}

type recordedEmail struct {
	to      string
	subject string
	body    string
}

type recordingEmailStub struct {
	sent []recordedEmail
}

func (s *recordingEmailStub) SendCustomEmail(toEmail, subject, body string) error {
	s.sent = append(s.sent, recordedEmail{to: toEmail, subject: subject, body: body})
	return nil
}

type upstreamBalanceReaderStub struct {
	account      contract.UpstreamAccountBalance
	accountErr   error
	cards        map[int64]contract.UpstreamCardBalance
	cardErrs     map[int64]error
	cardIDs      []int64
	accountCalls int
	cardCalls    int
}

func (s *upstreamBalanceReaderStub) AccountBalance(context.Context) (contract.UpstreamAccountBalance, error) {
	s.accountCalls++
	return s.account, s.accountErr
}

func (s *upstreamBalanceReaderStub) MonitoredCardIDs(context.Context) ([]int64, error) {
	return s.cardIDs, nil
}

func (s *upstreamBalanceReaderStub) CardBalance(_ context.Context, cardID int64) (contract.UpstreamCardBalance, error) {
	s.cardCalls++
	if err := s.cardErrs[cardID]; err != nil {
		return contract.UpstreamCardBalance{CardID: cardID}, err
	}
	return s.cards[cardID], nil
}

type upstreamBalanceTestClock struct {
	now time.Time
}

func (c *upstreamBalanceTestClock) Now() time.Time { return c.now }

func newUpstreamBalanceTestService(t *testing.T, reader contract.UpstreamBalanceReader) (*Service, *recordingEmailStub, *upstreamBalanceTestClock, settingsmessaging.NotificationCenterSetting) {
	t.Helper()
	setting := settingsmessaging.NotificationCenterDefaultSetting()
	setting.DefaultLocale = constants.LocaleZhCN
	setting.Channels.Email = settingsmessaging.NotificationChannelSetting{Enabled: true, Recipients: []string{"owner@example.com"}}
	setting.UpstreamBalanceAlertEnabled = true
	setting.UpstreamBalanceAlertThresholdUSD = "5.00"
	setting.UpstreamCardBalanceAlertThresholdUSD = "20.00"
	setting.UpstreamBalanceAlertCheckMinutes = 30
	setting.UpstreamBalanceAlertRepeatHours = 6

	email := &recordingEmailStub{}
	service := NewService(notificationSettingsStub{notification: setting}, email, nil, nil, NewLogService(&notificationLogRepositoryStub{}), nil, nil)
	clock := &upstreamBalanceTestClock{now: time.Date(2026, 10, 21, 8, 0, 0, 0, time.UTC)}
	locks := newCacheAlertLocks()
	locks.now = clock.Now
	service.alertLocks = locks
	service.SetUpstreamBalanceReader(reader)
	return service, email, clock, setting
}

func upstreamBalanceCheckPayload() queue.NotificationDispatchPayload {
	return queue.NotificationDispatchPayload{
		EventType: constants.NotificationEventExceptionAlertCheck,
		BizType:   constants.NotificationBizTypeDashboardAlert,
		Data:      map[string]interface{}{"message": "scheduled_inventory_alert_check"},
	}
}

func TestUpstreamBalanceCheckSkipsWhenDisabledOrUnconfigured(t *testing.T) {
	reader := &upstreamBalanceReaderStub{account: contract.UpstreamAccountBalance{Spendable: decimal.RequireFromString("1")}}
	service, email, _, setting := newUpstreamBalanceTestService(t, reader)

	disabled := setting
	disabled.UpstreamBalanceAlertEnabled = false
	if err := service.dispatchUpstreamBalanceCheck(context.Background(), disabled, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("disabled check: %v", err)
	}
	noThresholds := setting
	noThresholds.UpstreamBalanceAlertThresholdUSD = ""
	noThresholds.UpstreamCardBalanceAlertThresholdUSD = ""
	if err := service.dispatchUpstreamBalanceCheck(context.Background(), noThresholds, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("no threshold check: %v", err)
	}
	if reader.accountCalls != 0 || reader.cardCalls != 0 || len(email.sent) != 0 {
		t.Fatalf("disabled/unconfigured check must not query upstream or send, calls=%d/%d sent=%d", reader.accountCalls, reader.cardCalls, len(email.sent))
	}

	unwired, unwiredEmail, _, _ := newUpstreamBalanceTestService(t, nil)
	if err := unwired.dispatchUpstreamBalanceCheck(context.Background(), setting, upstreamBalanceCheckPayload()); err != nil || len(unwiredEmail.sent) != 0 {
		t.Fatalf("unconfigured ZovoCard must be skipped silently, err=%v sent=%d", err, len(unwiredEmail.sent))
	}
}

func TestUpstreamBalanceCheckGatesQueriesAndRepeatsAlerts(t *testing.T) {
	reader := &upstreamBalanceReaderStub{account: contract.UpstreamAccountBalance{
		Spendable: decimal.RequireFromString("3.2"),
		Balance:   decimal.RequireFromString("4"),
		Currency:  "USD",
	}}
	service, email, clock, setting := newUpstreamBalanceTestService(t, reader)
	setting.UpstreamCardBalanceAlertThresholdUSD = ""
	ctx := context.Background()
	run := func() {
		t.Helper()
		if err := service.dispatchUpstreamBalanceCheck(ctx, setting, upstreamBalanceCheckPayload()); err != nil {
			t.Fatalf("check: %v", err)
		}
	}

	run()
	if reader.accountCalls != 1 || len(email.sent) != 1 {
		t.Fatalf("first low check must alert once, calls=%d sent=%d", reader.accountCalls, len(email.sent))
	}
	body := email.sent[0].body
	for _, want := range []string{"ZovoCard 余额不足", "3.20 USD", "5.00 USD", "账面余额 4.00 USD", "6 小时"} {
		if !strings.Contains(body, want) {
			t.Fatalf("alert body missing %q: %s", want, body)
		}
	}

	clock.now = clock.now.Add(time.Minute)
	run()
	if reader.accountCalls != 1 {
		t.Fatalf("scheduler tick inside check interval must not query upstream, calls=%d", reader.accountCalls)
	}

	clock.now = clock.now.Add(31 * time.Minute)
	run()
	if reader.accountCalls != 2 || len(email.sent) != 1 {
		t.Fatalf("still low inside repeat interval must not re-alert, calls=%d sent=%d", reader.accountCalls, len(email.sent))
	}

	clock.now = clock.now.Add(6 * time.Hour)
	run()
	if len(email.sent) != 2 {
		t.Fatalf("still low after repeat interval must re-alert, sent=%d", len(email.sent))
	}

	reader.account.Spendable = decimal.RequireFromString("5")
	clock.now = clock.now.Add(31 * time.Minute)
	run()
	if len(email.sent) != 2 {
		t.Fatalf("balance equal to threshold is not low, sent=%d", len(email.sent))
	}

	reader.account.Spendable = decimal.RequireFromString("4.99")
	clock.now = clock.now.Add(31 * time.Minute)
	run()
	if len(email.sent) != 3 {
		t.Fatalf("recovery must reset the repeat interval so the next drop alerts immediately, sent=%d", len(email.sent))
	}
}

func TestUpstreamBalanceCheckCardsAndQueryFailures(t *testing.T) {
	reader := &upstreamBalanceReaderStub{
		account:    contract.UpstreamAccountBalance{Spendable: decimal.RequireFromString("50"), Balance: decimal.RequireFromString("50"), Currency: "USD"},
		accountErr: errors.New("zovocard: http 503: upstream unavailable"),
		cardIDs:    []int64{525944, 7001},
		cards: map[int64]contract.UpstreamCardBalance{
			525944: {CardID: 525944, Available: decimal.RequireFromString("12.5"), Stale: true, AsOf: "2026-10-21T07:00:00Z"},
			7001:   {CardID: 7001, Available: decimal.RequireFromString("100")},
		},
	}
	service, email, clock, setting := newUpstreamBalanceTestService(t, reader)
	ctx := context.Background()

	if err := service.dispatchUpstreamBalanceCheck(ctx, setting, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("check: %v", err)
	}
	if reader.cardCalls != 2 || len(email.sent) != 2 {
		t.Fatalf("expected one query-failed and one low-card alert, cardCalls=%d sent=%d", reader.cardCalls, len(email.sent))
	}
	var failed, card string
	for _, sent := range email.sent {
		switch {
		case strings.Contains(sent.body, "ZovoCard 余额查询失败"):
			failed = sent.body
		case strings.Contains(sent.body, "ZovoCard 余额不足"):
			card = sent.body
		}
	}
	if failed == "" || !strings.Contains(failed, "不代表余额不足") || !strings.Contains(failed, "503") {
		t.Fatalf("query failure must be reported separately, not as low balance: %q", failed)
	}
	for _, want := range []string{"****5944", "12.50 USD", "20.00 USD", "可能不是最新值", "2026-10-21T07:00:00Z"} {
		if !strings.Contains(card, want) {
			t.Fatalf("card alert missing %q: %s", want, card)
		}
	}
	if strings.Contains(card, "525944") {
		t.Fatalf("card id must be masked: %s", card)
	}

	clock.now = clock.now.Add(31 * time.Minute)
	if err := service.dispatchUpstreamBalanceCheck(ctx, setting, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if len(email.sent) != 2 {
		t.Fatalf("query failure and low card must be rate limited by the repeat interval, sent=%d", len(email.sent))
	}

	reader.accountErr = nil
	clock.now = clock.now.Add(31 * time.Minute)
	if err := service.dispatchUpstreamBalanceCheck(ctx, setting, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("third check: %v", err)
	}
	reader.accountErr = errors.New("zovocard: timeout")
	clock.now = clock.now.Add(31 * time.Minute)
	if err := service.dispatchUpstreamBalanceCheck(ctx, setting, upstreamBalanceCheckPayload()); err != nil {
		t.Fatalf("fourth check: %v", err)
	}
	if len(email.sent) != 3 {
		t.Fatalf("a successful query must reset the failure interval, sent=%d", len(email.sent))
	}
}

func TestUpstreamBalanceAlertFormatting(t *testing.T) {
	if format.UpstreamBalanceIsLow(decimal.RequireFromString("5"), decimal.RequireFromString("5")) {
		t.Fatal("balance equal to threshold is not low")
	}
	if !format.UpstreamBalanceIsLow(decimal.RequireFromString("4.99"), decimal.RequireFromString("5")) {
		t.Fatal("balance below threshold is low")
	}
	if format.UpstreamBalanceIsLow(decimal.Zero, decimal.Zero) {
		t.Fatal("zero threshold disables the check")
	}
	if got := format.MaskUpstreamCardID(525944); got != "****5944" {
		t.Fatalf("mask = %q", got)
	}

	setting := settingsmessaging.NotificationCenterDefaultSetting()
	input := format.UpstreamBalanceAlertInput{
		CardID:      525944,
		Value:       decimal.RequireFromString("1"),
		Threshold:   decimal.RequireFromString("20"),
		RepeatHours: 6,
	}
	for locale, want := range map[string][]string{
		constants.LocaleZhCN: {"ZovoCard 余额不足", "ZovoCard 付款卡 ****5944 可用额度为 1.00 USD"},
		constants.LocaleZhTW: {"ZovoCard 餘額不足", "ZovoCard 付款卡 ****5944 可用額度為 1.00 USD"},
		constants.LocaleEnUS: {"ZovoCard balance low", "ZovoCard funding card ****5944 available balance is 1.00 USD"},
	} {
		payload := upstreamBalanceCheckPayload()
		payload.Locale = locale
		item := format.BuildUpstreamBalanceLowDispatchPayload(setting, payload, input)
		if item.EventType != constants.NotificationEventExceptionAlert || item.Data["alert_type_key"] != constants.NotificationAlertTypeUpstreamBalanceLow {
			t.Fatalf("%s: unexpected payload %+v", locale, item)
		}
		if item.Data["alert_type"] != want[0] || !strings.Contains(item.Data["message"].(string), want[1]) {
			t.Fatalf("%s: label=%v message=%v", locale, item.Data["alert_type"], item.Data["message"])
		}
	}
}

func TestUpstreamBalanceTestSceneUsesBalanceSample(t *testing.T) {
	for locale, want := range map[string]string{
		constants.LocaleZhCN: "ZovoCard 付款卡 ****5944 可用额度为 3.20 USD，低于告警阈值 5.00 USD",
		constants.LocaleZhTW: "ZovoCard 付款卡 ****5944 可用額度為 3.20 USD，低於告警門檻 5.00 USD",
		constants.LocaleEnUS: "ZovoCard funding card ****5944 available balance is 3.20 USD, below the alert threshold of 5.00 USD",
	} {
		vars := format.BuildTestVariables(constants.NotificationTestSceneUpstreamBalanceAlert, locale)
		if vars["alert_type_key"] != constants.NotificationAlertTypeUpstreamBalanceLow {
			t.Fatalf("%s: alert_type_key = %v", locale, vars["alert_type_key"])
		}
		if msg, _ := vars["message"].(string); !strings.Contains(msg, want) {
			t.Fatalf("%s: message = %q", locale, msg)
		}
	}
}
