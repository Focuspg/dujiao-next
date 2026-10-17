package directcharge_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

func (f *fakeUpstream) CreateOrder(_ context.Context, req directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
	f.created = append(f.created, req)
	if f.createFn == nil {
		return nil, errors.New("create not expected")
	}
	return f.createFn(req)
}

func (f *fakeUpstream) GetOrder(_ context.Context, id int64) (*directchargecontract.UpstreamOrder, error) {
	if f.getFn == nil {
		return nil, errors.New("get not expected")
	}
	return f.getFn(id)
}

func (f *fakeUpstream) FindOrder(_ context.Context, clientRequestID string, _ int) (*directchargecontract.UpstreamOrder, error) {
	if f.findFn == nil {
		return nil, nil
	}
	return f.findFn(clientRequestID)
}

func (f *fakeUpstream) ParseWebhook(_ []byte, signature string) (*directchargecontract.UpstreamEvent, error) {
	if signature != "good" || f.event == nil {
		return nil, directchargecontract.ErrWebhookSignature
	}
	copied := *f.event
	return &copied, nil
}

func (f *fakeOrders) Deliver(orderID uint, payload string) error {
	if f.deliverErr != nil {
		return f.deliverErr
	}
	f.delivered[orderID] = payload
	f.status[orderID] = constants.OrderStatusDelivered
	return nil
}

type fakeAlerter struct{ alerts []directchargecontract.Alert }

func (f *fakeAlerter) Alert(alert directchargecontract.Alert) error {
	f.alerts = append(f.alerts, alert)
	return nil
}

func (f *fakeAlerter) has(alertType string) bool {
	for _, alert := range f.alerts {
		if alert.Type == alertType {
			return true
		}
	}
	return false
}

func acceptedOrder(id int64, status string) func(req directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
	return func(req directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, ClientRequestID: req.ClientRequestID, Status: status, Outcome: directchargecontract.UpstreamOutcomePending}, nil
	}
}

// paidTask 走完预检、下单并把子订单标记为已付款
func (e *env) paidTask(t *testing.T) directchargedomain.Task {
	t.Helper()
	view := e.preflight(t, 1)
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); err != nil {
		t.Fatalf("create order: %v", err)
	}
	task := e.task(t)
	e.orders.status[task.OrderID] = constants.OrderStatusPaid
	return task
}

func (e *env) task(t *testing.T) directchargedomain.Task {
	t.Helper()
	var task directchargedomain.Task
	if err := e.db.Order("id DESC").First(&task).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	return task
}

func (e *env) cycle(t *testing.T, advance time.Duration) directchargedomain.Task {
	t.Helper()
	e.now = e.now.Add(advance)
	if err := e.service.RunCycle(context.Background()); err != nil {
		t.Fatalf("run cycle: %v", err)
	}
	return e.task(t)
}

func (e *env) credentialDestroyed(t *testing.T, task directchargedomain.Task) bool {
	t.Helper()
	var credential directchargedomain.Credential
	if err := e.db.First(&credential, *task.CredentialID).Error; err != nil {
		t.Fatalf("load credential: %v", err)
	}
	return credential.DestroyedAt != nil && credential.Ciphertext == ""
}

func TestWorkerSubmitsPaidTaskAndDeliversOnSuccess(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = acceptedOrder(501, "queued")
	task := e.paidTask(t)

	task = e.cycle(t, 0)
	if task.Status != directchargedomain.TaskProcessing || task.AttemptCount != 1 || task.SubmittedAt == nil {
		t.Fatalf("task should be processing after submit: %+v", task)
	}
	if len(e.upstream.created) != 1 {
		t.Fatalf("expected one create call, got %d", len(e.upstream.created))
	}
	req := e.upstream.created[0]
	wantID := directchargedomain.BuildClientRequestID("t1", task.ID, 1)
	if req.ClientRequestID != wantID || req.CardID != 1 || req.Credential != testSession || req.PreflightToken != "pf-token" || req.Plan != "plus" {
		t.Fatalf("unexpected create request: id=%s card=%d plan=%s", req.ClientRequestID, req.CardID, req.Plan)
	}
	if e.upstream.calls != 1 {
		t.Fatalf("fresh preflight token must be reused, got %d preflight calls", e.upstream.calls)
	}
	if !e.credentialDestroyed(t, task) {
		t.Fatalf("credential must be destroyed once upstream accepted the order")
	}

	// 上游仍在处理：继续等待
	e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, ClientRequestID: wantID, Status: "processing", Outcome: directchargecontract.UpstreamOutcomePending}, nil
	}
	task = e.cycle(t, 20*time.Second)
	if task.Status != directchargedomain.TaskProcessing || task.CheckCount != 1 || task.NextCheckAt == nil || !task.NextCheckAt.After(e.now) {
		t.Fatalf("pending order should be re-checked later: %+v", task)
	}

	e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, ClientRequestID: wantID, Status: "completed", Outcome: directchargecontract.UpstreamOutcomeSucceeded, Currency: "PHP", FinalAmountMinor: 98214}, nil
	}
	task = e.cycle(t, 3*time.Minute)
	if task.Status != directchargedomain.TaskSucceeded || task.CompletedAt == nil {
		t.Fatalf("task should succeed: %+v", task)
	}
	payload := e.orders.delivered[task.OrderID]
	if !strings.Contains(payload, "#501") || strings.Contains(payload, testSession) {
		t.Fatalf("unexpected delivery payload %q", payload)
	}
	var attempt directchargedomain.Attempt
	e.db.First(&attempt, *task.CurrentAttemptID)
	if attempt.Status != directchargedomain.AttemptSucceeded || attempt.FinalAmountMinor != 98214 || attempt.UpstreamOrderID != 501 {
		t.Fatalf("attempt not updated: %+v", attempt)
	}
	var locks int64
	e.db.Model(&directchargedomain.AccountLock{}).Count(&locks)
	if locks != 0 {
		t.Fatalf("account lock must be released after success")
	}
}

func TestWorkerReplaysUnknownSubmitWithSameRequestID(t *testing.T) {
	e := setup(t)
	calls := 0
	e.upstream.createFn = func(req directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
		calls++
		if calls == 1 {
			return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindTransient, Unknown: true}
		}
		return acceptedOrder(777, "queued")(req)
	}
	e.paidTask(t)

	task := e.cycle(t, 0)
	if task.Status != directchargedomain.TaskSubmitUnknown {
		t.Fatalf("timeout should leave task in submit_unknown, got %s", task.Status)
	}
	if e.credentialDestroyed(t, task) {
		t.Fatalf("credential must be kept for an identical replay")
	}

	task = e.cycle(t, 10*time.Second)
	if task.Status != directchargedomain.TaskSubmitUnknown || len(e.upstream.created) != 1 {
		t.Fatalf("replay must wait for next_check_at, got status=%s calls=%d", task.Status, len(e.upstream.created))
	}

	task = e.cycle(t, time.Minute)
	if task.Status != directchargedomain.TaskProcessing || task.AttemptCount != 1 {
		t.Fatalf("replay should link the original order: %+v", task)
	}
	if len(e.upstream.created) != 2 || e.upstream.created[0].ClientRequestID != e.upstream.created[1].ClientRequestID {
		t.Fatalf("replay must reuse the same client_request_id")
	}
	var attempts int64
	e.db.Model(&directchargedomain.Attempt{}).Count(&attempts)
	if attempts != 1 {
		t.Fatalf("unknown result must never create a new attempt, got %d", attempts)
	}
}

func TestWorkerUnknownSubmitGoesToReviewAfterTimeout(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
		return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindTransient, Unknown: true}
	}
	e.paidTask(t)
	e.cycle(t, 0)
	for i := 0; i < 40; i++ {
		e.cycle(t, time.Minute)
	}
	task := e.task(t)
	if task.Status != directchargedomain.TaskReview || !e.alerts.has("direct_charge_review") {
		t.Fatalf("long unknown submit should go to review with alert, got %s", task.Status)
	}
	var attempts int64
	e.db.Model(&directchargedomain.Attempt{}).Count(&attempts)
	if attempts != 1 {
		t.Fatalf("expected a single attempt, got %d", attempts)
	}
}

func TestWorkerConfirmedRejectionsAndFailures(t *testing.T) {
	t.Run("not eligible fails without charge", func(t *testing.T) {
		e := setup(t)
		e.upstream.createFn = func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
			return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindNotEligible, Code: "plan_not_eligible"}
		}
		e.paidTask(t)
		task := e.cycle(t, 0)
		if task.Status != directchargedomain.TaskFailedConfirmed || !e.alerts.has("direct_charge_failed") || !e.credentialDestroyed(t, task) {
			t.Fatalf("not eligible should fail confirmed: %+v", task)
		}
	})
	t.Run("insufficient funds retries later", func(t *testing.T) {
		e := setup(t)
		e.upstream.createFn = func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
			return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindFunds, Code: "insufficient_balance"}
		}
		e.paidTask(t)
		task := e.cycle(t, 0)
		if task.Status != directchargedomain.TaskReady || task.NextCheckAt == nil || !task.NextCheckAt.After(e.now.Add(4*time.Minute)) || !e.alerts.has("direct_charge_upstream_blocked") {
			t.Fatalf("funds error should retry later with alert: %+v", task)
		}
		// 第二次提交使用新的尝试号，并重新预检拿新令牌
		e.upstream.createFn = acceptedOrder(9, "queued")
		task = e.cycle(t, 6*time.Minute)
		if task.Status != directchargedomain.TaskProcessing || task.AttemptCount != 2 || e.upstream.calls != 2 {
			t.Fatalf("retry should submit attempt 2 after fresh preflight: %+v calls=%d", task, e.upstream.calls)
		}
		if e.upstream.created[1].ClientRequestID == e.upstream.created[0].ClientRequestID {
			t.Fatalf("a rejected attempt may be retried only with a new request id")
		}
	})
	t.Run("failure with charge goes to review", func(t *testing.T) {
		e := setup(t)
		e.upstream.createFn = acceptedOrder(42, "queued")
		e.paidTask(t)
		e.cycle(t, 0)
		e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
			return &directchargecontract.UpstreamOrder{ID: id, Status: "failed", Outcome: directchargecontract.UpstreamOutcomeFailed, FinalAmountMinor: 100}, nil
		}
		task := e.cycle(t, 20*time.Second)
		if task.Status != directchargedomain.TaskReview {
			t.Fatalf("charged failure must go to review, got %s", task.Status)
		}
	})
	t.Run("precharge failure is confirmed", func(t *testing.T) {
		e := setup(t)
		e.upstream.createFn = acceptedOrder(43, "queued")
		e.paidTask(t)
		e.cycle(t, 0)
		e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
			return &directchargecontract.UpstreamOrder{ID: id, Status: "failed_precharge", Outcome: directchargecontract.UpstreamOutcomeFailed, FailedBeforeCharge: true}, nil
		}
		task := e.cycle(t, 20*time.Second)
		if task.Status != directchargedomain.TaskFailedConfirmed {
			t.Fatalf("precharge failure should be confirmed, got %s", task.Status)
		}
	})
	t.Run("delivery failure keeps polling", func(t *testing.T) {
		e := setup(t)
		e.upstream.createFn = acceptedOrder(44, "queued")
		e.paidTask(t)
		e.cycle(t, 0)
		e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
			return &directchargecontract.UpstreamOrder{ID: id, Status: "completed", Outcome: directchargecontract.UpstreamOutcomeSucceeded}, nil
		}
		e.orders.deliverErr = errors.New("db busy")
		task := e.cycle(t, 20*time.Second)
		if task.Status != directchargedomain.TaskProcessing {
			t.Fatalf("task must stay processing until delivered, got %s", task.Status)
		}
		e.orders.deliverErr = nil
		task = e.cycle(t, 2*time.Minute)
		if task.Status != directchargedomain.TaskSucceeded || e.orders.delivered[task.OrderID] == "" {
			t.Fatalf("delivery retry should succeed: %+v", task)
		}
	})
}

func TestWorkerRecoversInterruptedSubmit(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = acceptedOrder(55, "queued")
	task := e.paidTask(t)
	e.cycle(t, 0)
	// 模拟进程在发出请求后退出：任务停在 submitting
	e.db.Model(&directchargedomain.Task{}).Where("id = ?", task.ID).Updates(map[string]interface{}{"status": directchargedomain.TaskSubmitting})
	e.db.Model(&directchargedomain.Attempt{}).Where("task_id = ?", task.ID).Updates(map[string]interface{}{"status": directchargedomain.AttemptPrepared, "upstream_order_id": 0})
	e.upstream.findFn = func(clientRequestID string) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: 55, ClientRequestID: clientRequestID, Status: "processing", Outcome: directchargecontract.UpstreamOutcomePending}, nil
	}
	// 同一轮内先转为结果待核验，再因凭据已销毁改为翻订单列表找回原单
	task = e.cycle(t, 3*time.Minute)
	var logs int64
	e.db.Model(&directchargedomain.TaskLog{}).Where("task_id = ? AND to_status = ?", task.ID, directchargedomain.TaskSubmitUnknown).Count(&logs)
	if logs != 1 {
		t.Fatalf("stale submitting should pass through submit_unknown")
	}
	if task.Status != directchargedomain.TaskProcessing || len(e.upstream.created) != 1 {
		t.Fatalf("original order should be found without resubmitting: status=%s creates=%d", task.Status, len(e.upstream.created))
	}
}

func TestWebhookWakesTaskAndDedupes(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = acceptedOrder(88, "queued")
	e.paidTask(t)
	task := e.cycle(t, 0)
	e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, Status: "processing", Outcome: directchargecontract.UpstreamOutcomePending}, nil
	}
	task = e.cycle(t, 20*time.Second)
	if task.NextCheckAt == nil || !task.NextCheckAt.After(e.now) {
		t.Fatalf("expected a future check: %+v", task.NextCheckAt)
	}

	if err := e.service.HandleWebhook([]byte(`{}`), "bad"); !errors.Is(err, directchargecontract.ErrWebhookSignature) {
		t.Fatalf("bad signature must be rejected, got %v", err)
	}
	crid := directchargedomain.BuildClientRequestID("t1", task.ID, 1)
	e.upstream.event = &directchargecontract.UpstreamEvent{DedupKey: "evt-1", Type: "direct_order.completed", BodyHash: "h", UpstreamOrderID: 88, ClientRequestID: crid, Status: "completed", Relevant: true}
	for i := 0; i < 2; i++ {
		if err := e.service.HandleWebhook([]byte(`{}`), "good"); err != nil {
			t.Fatalf("webhook: %v", err)
		}
	}
	var events []directchargedomain.WebhookEvent
	e.db.Find(&events)
	if len(events) != 1 || events[0].Status != directchargedomain.EventProcessed || events[0].TaskID == nil || *events[0].TaskID != task.ID {
		t.Fatalf("webhook should be stored once and matched: %+v", events)
	}
	task = e.task(t)
	if task.NextCheckAt == nil || task.NextCheckAt.After(e.now) {
		t.Fatalf("webhook should wake the task for an immediate check")
	}
	if task.Status != directchargedomain.TaskProcessing {
		t.Fatalf("webhook alone must not change the result, got %s", task.Status)
	}

	e.upstream.event = &directchargecontract.UpstreamEvent{DedupKey: "evt-2", Type: "direct_order.completed", BodyHash: "h2", UpstreamOrderID: 999, ClientRequestID: "rc-other-1-1", Status: "completed", Relevant: true}
	if err := e.service.HandleWebhook([]byte(`{}`), "good"); err != nil {
		t.Fatalf("webhook: %v", err)
	}
	var unmatched directchargedomain.WebhookEvent
	e.db.Where("dedup_key = ?", "evt-2").First(&unmatched)
	if unmatched.Status != directchargedomain.EventUnmatched {
		t.Fatalf("unknown order should stay unmatched, got %s", unmatched.Status)
	}
}

func TestOrphanPaidOrderAlertsOnce(t *testing.T) {
	e := setup(t)
	for _, stmt := range []string{
		`CREATE TABLE orders (id integer primary key, order_no text, status text, parent_id integer, deleted_at datetime)`,
		`CREATE TABLE order_items (id integer primary key, order_id integer, fulfillment_type text)`,
		`INSERT INTO orders (id, order_no, status, parent_id) VALUES (500, 'DJ499-01', 'paid', 499)`,
		`INSERT INTO order_items (order_id, fulfillment_type) VALUES (500, 'direct_charge')`,
		`INSERT INTO orders (id, order_no, status, parent_id) VALUES (600, 'DJ599-01', 'paid', 599)`,
		`INSERT INTO order_items (order_id, fulfillment_type) VALUES (600, 'manual')`,
	} {
		if err := e.db.Exec(stmt).Error; err != nil {
			t.Fatalf("prepare orders: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := e.service.RunCycle(context.Background()); err != nil {
			t.Fatalf("run cycle: %v", err)
		}
	}
	count := 0
	for _, alert := range e.alerts.alerts {
		if alert.Type == "direct_charge_orphan_order" {
			count++
			if alert.OrderNo != "DJ499-01" {
				t.Fatalf("unexpected orphan %s", alert.OrderNo)
			}
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one orphan alert, got %d", count)
	}
}
