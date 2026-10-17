package directcharge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

func rejectWith(kind string) func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
	return func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
		return nil, &directchargecontract.UpstreamError{Kind: kind, Code: kind}
	}
}

func TestOwnerResuppliesCredentialForSameAccount(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = rejectWith(directchargecontract.UpstreamKindCredential)
	e.paidTask(t)
	task := e.cycle(t, 0)
	if task.Status != directchargedomain.TaskWaitingCredential {
		t.Fatalf("expected waiting_credential, got %s", task.Status)
	}
	view, err := e.service.GetOrder(1, task.ParentOrderNo)
	if err != nil || !view.CanResupply || !view.CanRequestRefund || view.Status != directchargeapp.DisplayCredentialRequired {
		t.Fatalf("owner should be offered resupply: %+v %v", view, err)
	}
	oldCredential := *task.CredentialID

	input := directchargeapp.ResupplyCredentialInput{UserID: 1, OrderNo: task.ParentOrderNo, Credential: "fresh-session"}
	if _, err := e.service.ResupplyCredential(context.Background(), directchargeapp.ResupplyCredentialInput{UserID: 2, OrderNo: task.ParentOrderNo, Credential: "x"}); !errors.Is(err, directchargecontract.ErrNotFound) {
		t.Fatalf("other users must not resupply, got %v", err)
	}
	e.upstream.result.AccountID = "someone-else@example.com"
	if _, err := e.service.ResupplyCredential(context.Background(), input); !errors.Is(err, directchargecontract.ErrAccountMismatch) {
		t.Fatalf("different account must be rejected, got %v", err)
	}
	e.upstream.result.AccountID = "buyer@example.com"
	e.upstream.result.PreflightToken = "pf-token-2"
	if _, err := e.service.ResupplyCredential(context.Background(), input); err != nil {
		t.Fatalf("resupply: %v", err)
	}
	task = e.task(t)
	if task.Status != directchargedomain.TaskReady || *task.CredentialID == oldCredential {
		t.Fatalf("task should be ready with a new credential: %+v", task)
	}
	var old directchargedomain.Credential
	e.db.First(&old, oldCredential)
	if old.DestroyedAt == nil || old.Ciphertext != "" {
		t.Fatalf("old credential must be destroyed")
	}
	if _, err := e.service.ResupplyCredential(context.Background(), input); !errors.Is(err, directchargecontract.ErrCredentialNotRequired) {
		t.Fatalf("second resupply should be refused, got %v", err)
	}

	e.upstream.createFn = acceptedOrder(301, "queued")
	task = e.cycle(t, time.Second)
	if task.Status != directchargedomain.TaskProcessing {
		t.Fatalf("task should be resubmitted, got %s", task.Status)
	}
	last := e.upstream.created[len(e.upstream.created)-1]
	if last.Credential != "fresh-session" || last.PreflightToken != "pf-token-2" || task.AttemptCount != 2 {
		t.Fatalf("resubmit must use the new credential and a new attempt: attempt=%d", task.AttemptCount)
	}
}

func TestRefundRequestAndAdminRefund(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = rejectWith(directchargecontract.UpstreamKindCredential)
	e.paidTask(t)
	task := e.cycle(t, 0)

	view, err := e.service.RequestRefund(1, task.ParentOrderNo)
	if err != nil || view.RefundStatus != directchargedomain.RefundRequested || view.CanRequestRefund || view.CanResupply {
		t.Fatalf("refund request: %+v %v", view, err)
	}
	if !e.alerts.has("direct_charge_refund_requested") {
		t.Fatalf("owner must be alerted about the refund request")
	}
	if _, err := e.service.RequestRefund(1, task.ParentOrderNo); !errors.Is(err, directchargecontract.ErrRefundNotAllowed) {
		t.Fatalf("duplicate request should be refused, got %v", err)
	}
	if _, err := e.service.Refund(task.ID, " ", 9); !errors.Is(err, directchargecontract.ErrReasonRequired) {
		t.Fatalf("refund needs a voucher remark, got %v", err)
	}

	e.orders.refundErr = errors.New("refund window expired")
	if _, err := e.service.Refund(task.ID, "alipay 2026101700001", 9); !errors.Is(err, directchargecontract.ErrRefundFailed) {
		t.Fatalf("order refund failure must surface, got %v", err)
	}
	task = e.task(t)
	if task.Status != directchargedomain.TaskFailedConfirmed || !task.RefundLocked || task.RefundStatus == directchargedomain.RefundCompleted {
		t.Fatalf("task must be locked and ended even if recording failed: %+v", task)
	}
	// 加锁后任务不会再被提交
	e.cycle(t, 10*time.Minute)
	if len(e.upstream.created) != 1 {
		t.Fatalf("locked task must not be submitted again")
	}

	e.orders.refundErr = nil
	detail, err := e.service.Refund(task.ID, "alipay 2026101700001", 9)
	if err != nil {
		t.Fatalf("refund retry: %v", err)
	}
	if detail.Task.RefundStatus != directchargedomain.RefundCompleted || detail.Task.RefundedAt == nil || e.orders.refunded[task.OrderID] == "" {
		t.Fatalf("refund should be recorded: %+v", detail.Task)
	}
	var locks int64
	e.db.Model(&directchargedomain.AccountLock{}).Count(&locks)
	if locks != 0 {
		t.Fatalf("refunded task must release the account")
	}
	if _, err := e.service.Refund(task.ID, "again", 9); err != nil {
		t.Fatalf("refund must be idempotent, got %v", err)
	}
	view, _ = e.service.GetOrder(1, task.ParentOrderNo)
	if view.RefundStatus != directchargedomain.RefundCompleted || view.Status != directchargeapp.DisplayFailed {
		t.Fatalf("owner should see the refund: %+v", view)
	}
}

func TestAdminRefundBlockedWhileUpstreamMayCharge(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = func(directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error) {
		return nil, &directchargecontract.UpstreamError{Kind: directchargecontract.UpstreamKindTransient, Unknown: true}
	}
	e.paidTask(t)
	task := e.cycle(t, 0)
	if _, err := e.service.Refund(task.ID, "voucher", 9); !errors.Is(err, directchargecontract.ErrRefundNotAllowed) {
		t.Fatalf("submit_unknown must not be refundable, got %v", err)
	}
	for i := 0; i < 35; i++ {
		e.cycle(t, time.Minute)
	}
	task = e.task(t)
	if task.Status != directchargedomain.TaskReview {
		t.Fatalf("expected review, got %s", task.Status)
	}
	if _, err := e.service.Refund(task.ID, "voucher", 9); !errors.Is(err, directchargecontract.ErrAttemptOpen) {
		t.Fatalf("review with unknown attempt must not be refundable, got %v", err)
	}
	if _, err := e.service.Retry(task.ID, "try again", 9); !errors.Is(err, directchargecontract.ErrAttemptOpen) {
		t.Fatalf("retry must not create a new attempt while the old one is unknown, got %v", err)
	}

	// 查原单：上游找到原订单后交回自动查单
	e.upstream.findFn = func(crid string) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: 404, ClientRequestID: crid, Status: "processing", Outcome: directchargecontract.UpstreamOutcomePending}, nil
	}
	createsBefore := len(e.upstream.created)
	result, err := e.service.Reconcile(context.Background(), task.ID, 9)
	if err != nil || result.Result != directchargeapp.ReconcileUpdated || result.Detail.Task.Status != directchargedomain.TaskProcessing {
		t.Fatalf("reconcile should link the original order: %+v %v", result, err)
	}
	if len(e.upstream.created) != createsBefore {
		t.Fatalf("reconcile must never resubmit")
	}
	for _, req := range e.upstream.created {
		if req.ClientRequestID != e.upstream.created[0].ClientRequestID {
			t.Fatalf("unknown result may only be replayed with the original request id")
		}
	}
}

func TestAdminRetryAndResolveFromReview(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = rejectWith(directchargecontract.UpstreamKindRejected)
	e.paidTask(t)
	task := e.cycle(t, 0)
	if task.Status != directchargedomain.TaskReview {
		t.Fatalf("unclassified rejection should go to review, got %s", task.Status)
	}
	if _, err := e.service.Retry(task.ID, "", 9); !errors.Is(err, directchargecontract.ErrReasonRequired) {
		t.Fatalf("retry needs a reason, got %v", err)
	}
	if _, err := e.service.Reconcile(context.Background(), task.ID, 9); !errors.Is(err, directchargecontract.ErrActionNotAllowed) {
		t.Fatalf("nothing to reconcile for a rejected attempt, got %v", err)
	}
	detail, err := e.service.Retry(task.ID, "upstream fixed the plan", 9)
	if err != nil || detail.Task.Status != directchargedomain.TaskReady {
		t.Fatalf("retry: %+v %v", detail, err)
	}
	if last := detail.Logs[len(detail.Logs)-1]; last.Actor != "admin:9" {
		t.Fatalf("admin action must be attributed, got %q", last.Actor)
	}

	e.upstream.createFn = acceptedOrder(77, "queued")
	e.cycle(t, time.Second)
	e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, Status: "failed", Outcome: directchargecontract.UpstreamOutcomeFailed, FinalAmountMinor: 500}, nil
	}
	task = e.cycle(t, time.Minute)
	if task.Status != directchargedomain.TaskReview {
		t.Fatalf("charged failure should go to review, got %s", task.Status)
	}
	if _, err := e.service.Resolve(task.ID, "maybe", "x", 9); !errors.Is(err, directchargecontract.ErrActionNotAllowed) {
		t.Fatalf("unknown outcome must be rejected, got %v", err)
	}

	// 上游后来显示成功：查原单直接推进到成功并交付
	e.upstream.getFn = func(id int64) (*directchargecontract.UpstreamOrder, error) {
		return &directchargecontract.UpstreamOrder{ID: id, Status: "completed", Outcome: directchargecontract.UpstreamOutcomeSucceeded}, nil
	}
	result, err := e.service.Reconcile(context.Background(), task.ID, 9)
	if err != nil || result.Detail.Task.Status != directchargedomain.TaskSucceeded || e.orders.delivered[task.OrderID] == "" {
		t.Fatalf("reconcile should complete the task: %+v %v", result, err)
	}
	if _, err := e.service.Refund(task.ID, "voucher", 9); !errors.Is(err, directchargecontract.ErrRefundNotAllowed) {
		t.Fatalf("succeeded task must not be refunded here, got %v", err)
	}
}

func TestAdminResolveFailedThenRefund(t *testing.T) {
	e := setup(t)
	e.upstream.createFn = rejectWith(directchargecontract.UpstreamKindRejected)
	e.paidTask(t)
	task := e.cycle(t, 0)
	detail, err := e.service.Resolve(task.ID, directchargeapp.ResolveFailed, "confirmed with supplier", 9)
	if err != nil || detail.Task.Status != directchargedomain.TaskFailedConfirmed {
		t.Fatalf("resolve failed: %+v %v", detail, err)
	}
	if _, err := e.service.Refund(task.ID, "wechat 42", 9); err != nil {
		t.Fatalf("refund after failure: %v", err)
	}
}

func TestSpecAdminValidationPauseAndVersion(t *testing.T) {
	e := setup(t)
	input := directchargeapp.SpecInput{
		ProductID: 8, SKUID: 80, UpstreamProduct: "GPT", UpstreamPlan: "plus", PaymentCountry: "ph", PaymentCurrency: "php",
		CredentialModes: []string{"session", "session"}, CardID: 2, ConcurrencyLimit: 2, Enabled: true,
	}
	spec, err := e.service.SaveSpec(0, input)
	if err != nil || spec.UpstreamProduct != "gpt" || spec.PaymentCountry != "PH" || len(spec.CredentialModes) != 1 || spec.Version != 1 {
		t.Fatalf("create spec: %+v %v", spec, err)
	}
	if _, err := e.service.SaveSpec(0, input); !errors.Is(err, directchargecontract.ErrSpecDuplicate) {
		t.Fatalf("duplicate sku should be rejected, got %v", err)
	}
	bad := input
	bad.SKUID, bad.CardID = 81, 0
	if _, err := e.service.SaveSpec(0, bad); !errors.Is(err, directchargecontract.ErrInvalidSpec) {
		t.Fatalf("card id is required, got %v", err)
	}
	input.Enabled = false
	if spec, err = e.service.SaveSpec(spec.ID, input); err != nil || spec.Version != 1 || spec.Enabled {
		t.Fatalf("toggling sale must not bump version: %+v %v", spec, err)
	}
	input.CardID = 3
	if spec, err = e.service.SaveSpec(spec.ID, input); err != nil || spec.Version != 2 {
		t.Fatalf("changing card must bump version: %+v %v", spec, err)
	}

	// 暂停提交：已付款任务排队，不转人工
	paused := directchargeapp.SpecInput{
		ProductID: e.spec.ProductID, SKUID: e.spec.SKUID, UpstreamProduct: "gpt", UpstreamPlan: "plus",
		PaymentCountry: "PH", PaymentCurrency: "PHP", CardID: 1, MaxQuoteMinor: 150000, Enabled: true, SubmitPaused: true,
	}
	if _, err := e.service.SaveSpec(e.spec.ID, paused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	e.upstream.createFn = acceptedOrder(1, "queued")
	e.paidTask(t)
	task := e.cycle(t, 0)
	if task.Status != directchargedomain.TaskReady || len(e.upstream.created) != 0 {
		t.Fatalf("paused spec must hold tasks in ready: %s creates=%d", task.Status, len(e.upstream.created))
	}
	paused.SubmitPaused = false
	if _, err := e.service.SaveSpec(e.spec.ID, paused); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if task = e.cycle(t, 2*time.Minute); task.Status != directchargedomain.TaskProcessing {
		t.Fatalf("resumed spec should submit, got %s", task.Status)
	}
}
