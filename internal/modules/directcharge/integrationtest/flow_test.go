package directcharge_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/gormstore"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/vault"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const testSession = "session-token-plaintext-for-test"

type fakeUpstream struct {
	result *directchargecontract.UpstreamPreflight
	err    error
	calls  int

	createFn func(req directchargecontract.UpstreamOrderRequest) (*directchargecontract.UpstreamOrder, error)
	created  []directchargecontract.UpstreamOrderRequest
	getFn    func(id int64) (*directchargecontract.UpstreamOrder, error)
	findFn   func(clientRequestID string) (*directchargecontract.UpstreamOrder, error)
	event    *directchargecontract.UpstreamEvent
}

func (f *fakeUpstream) Preflight(_ context.Context, req directchargecontract.UpstreamPreflightRequest) (*directchargecontract.UpstreamPreflight, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if req.Product != "gpt" || req.Credential == "" {
		return nil, directchargecontract.ErrCredentialInvalid
	}
	copied := *f.result
	return &copied, nil
}

type fakeOrders struct {
	nextID     uint
	status     map[uint]string
	createErr  error
	canceled   []uint
	delivered  map[uint]string
	deliverErr error
	refunded   map[uint]string
	refundErr  error
}

func (f *fakeOrders) CreateDirectChargeOrder(req directchargecontract.OrderRequest) (*directchargecontract.OrderRef, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	f.nextID += 2
	parent, child := f.nextID-1, f.nextID
	f.status[parent] = constants.OrderStatusPendingPayment
	f.status[child] = constants.OrderStatusPendingPayment
	expires := time.Now().Add(15 * time.Minute)
	return &directchargecontract.OrderRef{
		ParentID: parent, ParentNo: fmt.Sprintf("DJ%d", parent),
		ChildID: child, ChildNo: fmt.Sprintf("DJ%d-01", parent),
		ExpiresAt: &expires,
	}, nil
}

func (f *fakeOrders) CancelUnpaid(parentID, _ uint) error {
	f.canceled = append(f.canceled, parentID)
	f.status[parentID] = constants.OrderStatusCanceled
	f.status[parentID+1] = constants.OrderStatusCanceled
	return nil
}

func (f *fakeOrders) Status(orderID uint) (string, error) { return f.status[orderID], nil }

type env struct {
	db       *gorm.DB
	service  *directchargeapp.Service
	upstream *fakeUpstream
	orders   *fakeOrders
	alerts   *fakeAlerter
	spec     *directchargedomain.Spec
	now      time.Time
}

func setup(t *testing.T) *env {
	t.Helper()
	dsn := fmt.Sprintf("file:directcharge_test_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{DisableForeignKeyConstraintWhenMigrating: true})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&directchargedomain.Spec{}, &directchargedomain.Task{}, &directchargedomain.AccountLock{},
		&directchargedomain.TaskLog{}, &directchargedomain.Credential{}, &directchargedomain.Preflight{},
		&directchargedomain.Attempt{}, &directchargedomain.WebhookEvent{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	spec := &directchargedomain.Spec{
		ProductID: 7, SKUID: 70, Supplier: directchargedomain.SupplierZovoCard,
		UpstreamProduct: "gpt", UpstreamPlan: "plus", PaymentCountry: "PH", PaymentCurrency: "PHP",
		CardID: 1, MaxQuoteMinor: 150000, Enabled: true, Version: 1,
	}
	if err := db.Create(spec).Error; err != nil {
		t.Fatalf("create spec: %v", err)
	}
	v, err := vault.New("vault-key-0123456789", "fingerprint-key-0123456789")
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	e := &env{
		db:   db,
		spec: spec,
		now:  time.Now(),
		upstream: &fakeUpstream{result: &directchargecontract.UpstreamPreflight{
			AccountID: "Buyer@Example.com", CurrentPlan: "free", PreflightToken: "pf-token",
			Quotes: map[string]directchargecontract.UpstreamQuote{"plus": {Currency: "PHP", AmountMinor: 98214, Exponent: 2}},
		}},
		orders: &fakeOrders{status: map[uint]string{}, delivered: map[uint]string{}, refunded: map[uint]string{}},
		alerts: &fakeAlerter{},
	}
	e.service = directchargeapp.NewService(directchargeapp.Options{
		Enabled: true, Store: gormstore.New(db), Upstream: e.upstream, Vault: v, Orders: e.orders,
		Alerter: e.alerts, SitePrefix: "t1", Now: func() time.Time { return e.now },
	})
	return e
}

func (e *env) preflight(t *testing.T, userID uint) *directchargeapp.PreflightView {
	t.Helper()
	view, err := e.service.Preflight(context.Background(), directchargeapp.PreflightInput{
		UserID: userID, SKUID: 70, Mode: "session", Credential: testSession,
	})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	return view
}

func TestPreflightThenCreateOrderIsIdempotent(t *testing.T) {
	e := setup(t)
	view := e.preflight(t, 1)
	if view.AccountMasked != "B***@Example.com" || view.QuoteAmountMinor != 98214 || view.Plan != "plus" {
		t.Fatalf("unexpected preflight view: %+v", view)
	}

	var credential directchargedomain.Credential
	if err := e.db.First(&credential).Error; err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if strings.Contains(credential.Ciphertext, testSession) || strings.Contains(credential.Ciphertext, "pf-token") {
		t.Fatalf("credential stored in plaintext")
	}

	first, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}
	again, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref})
	if err != nil {
		t.Fatalf("replay create order: %v", err)
	}
	if again.OrderNo != first.OrderNo || !again.Replayed {
		t.Fatalf("replay should return same order, got %+v vs %+v", again, first)
	}

	var tasks []directchargedomain.Task
	e.db.Find(&tasks)
	if len(tasks) != 1 || tasks[0].Status != directchargedomain.TaskAwaitingPayment || tasks[0].ParentOrderNo != first.OrderNo {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
	var locks int64
	e.db.Model(&directchargedomain.AccountLock{}).Count(&locks)
	if locks != 1 {
		t.Fatalf("expected one account lock, got %d", locks)
	}
	e.db.First(&credential, credential.ID)
	if credential.TaskID == nil || *credential.TaskID != tasks[0].ID || !credential.ExpiresAt.After(e.now.Add(30*time.Minute)) {
		t.Fatalf("credential not bound or not extended: %+v", credential)
	}

	got, err := e.service.GetOrder(1, first.OrderNo)
	if err != nil || got.Status != directchargeapp.DisplayPendingPayment {
		t.Fatalf("get order: %+v %v", got, err)
	}
	e.orders.status[tasks[0].OrderID] = constants.OrderStatusFulfilling
	got, _ = e.service.GetOrder(1, first.OrderNo)
	if got.Status != directchargeapp.DisplayQueued {
		t.Fatalf("paid order should display queued, got %s", got.Status)
	}
	if _, err := e.service.GetOrder(2, first.OrderNo); !errors.Is(err, directchargecontract.ErrNotFound) {
		t.Fatalf("other user must not see the order, got %v", err)
	}
}

func TestSameAccountIsBlockedUntilOrderCanceled(t *testing.T) {
	e := setup(t)
	view := e.preflight(t, 1)
	order, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref})
	if err != nil {
		t.Fatalf("create order: %v", err)
	}

	_, err = e.service.Preflight(context.Background(), directchargeapp.PreflightInput{UserID: 2, SKUID: 70, Credential: "another-session"})
	if !errors.Is(err, directchargecontract.ErrAccountBusy) {
		t.Fatalf("expected account busy, got %v", err)
	}

	var task directchargedomain.Task
	e.db.First(&task)
	e.orders.status[task.OrderID] = constants.OrderStatusCanceled

	second := e.preflight(t, 2)
	if second.Ref == "" {
		t.Fatal("expected a new preflight after cancel")
	}
	e.db.First(&task, task.ID)
	if task.Status != directchargedomain.TaskClosed {
		t.Fatalf("task should be closed, got %s", task.Status)
	}
	var credential directchargedomain.Credential
	e.db.Where("task_id = ?", task.ID).First(&credential)
	if credential.DestroyedAt == nil || credential.Ciphertext != "" {
		t.Fatalf("credential should be destroyed: %+v", credential)
	}
	got, err := e.service.GetOrder(1, order.OrderNo)
	if err != nil || got.Status != directchargeapp.DisplayClosed {
		t.Fatalf("closed order view: %+v %v", got, err)
	}
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); !errors.Is(err, directchargecontract.ErrPreflightExpired) {
		t.Fatalf("closed preflight must not replay, got %v", err)
	}
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 2, PreflightRef: second.Ref}); err != nil {
		t.Fatalf("second user should be able to order now: %v", err)
	}
}

func TestPreflightRejections(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(e *env, input *directchargeapp.PreflightInput)
		want   error
	}{
		{"plan already active", func(e *env, _ *directchargeapp.PreflightInput) { e.upstream.result.CurrentPlan = "plus" }, directchargecontract.ErrNotEligible},
		{"quote above limit", func(e *env, _ *directchargeapp.PreflightInput) {
			e.upstream.result.Quotes["plus"] = directchargecontract.UpstreamQuote{Currency: "PHP", AmountMinor: 200000}
		}, directchargecontract.ErrQuoteExceeded},
		{"quote missing", func(e *env, _ *directchargeapp.PreflightInput) { delete(e.upstream.result.Quotes, "plus") }, directchargecontract.ErrProductUnavailable},
		{"quote error", func(e *env, _ *directchargeapp.PreflightInput) { e.upstream.result.QuoteError = "x" }, directchargecontract.ErrProductUnavailable},
		{"currency mismatch", func(e *env, _ *directchargeapp.PreflightInput) {
			e.upstream.result.Quotes["plus"] = directchargecontract.UpstreamQuote{Currency: "USD", AmountMinor: 2000}
		}, directchargecontract.ErrProductUnavailable},
		{"no account id", func(e *env, _ *directchargeapp.PreflightInput) { e.upstream.result.AccountID = "" }, directchargecontract.ErrCredentialInvalid},
		{"spec disabled", func(e *env, _ *directchargeapp.PreflightInput) {
			e.db.Model(e.spec).Update("enabled", false)
		}, directchargecontract.ErrProductUnavailable},
		{"unknown sku", func(_ *env, in *directchargeapp.PreflightInput) { in.SKUID = 999 }, directchargecontract.ErrProductUnavailable},
		{"mode not allowed", func(_ *env, in *directchargeapp.PreflightInput) { in.Mode = "access_token" }, directchargecontract.ErrCredentialUnsupported},
		{"credential too long", func(_ *env, in *directchargeapp.PreflightInput) {
			in.Credential = strings.Repeat("a", directchargeapp.DefaultMaxCredentialBytes+1)
		}, directchargecontract.ErrCredentialUnsupported},
		{"empty credential", func(_ *env, in *directchargeapp.PreflightInput) { in.Credential = "  " }, directchargecontract.ErrCredentialInvalid},
		{"upstream busy", func(e *env, _ *directchargeapp.PreflightInput) { e.upstream.err = directchargecontract.ErrUpstreamBusy }, directchargecontract.ErrUpstreamBusy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := setup(t)
			input := directchargeapp.PreflightInput{UserID: 1, SKUID: 70, Mode: "session", Credential: testSession}
			tc.mutate(e, &input)
			if _, err := e.service.Preflight(context.Background(), input); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			var count int64
			e.db.Model(&directchargedomain.Preflight{}).Count(&count)
			if count != 0 {
				t.Fatalf("rejected preflight must not be stored")
			}
		})
	}
}

func TestCreateOrderRejectsExpiredForeignOrChangedPreflight(t *testing.T) {
	e := setup(t)
	view := e.preflight(t, 1)
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 2, PreflightRef: view.Ref}); !errors.Is(err, directchargecontract.ErrPreflightExpired) {
		t.Fatalf("foreign ref: %v", err)
	}
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: "pf_unknown"}); !errors.Is(err, directchargecontract.ErrPreflightExpired) {
		t.Fatalf("unknown ref: %v", err)
	}
	e.db.Model(e.spec).Update("version", 2)
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); !errors.Is(err, directchargecontract.ErrPreflightExpired) {
		t.Fatalf("spec changed: %v", err)
	}
	e.db.Model(e.spec).Update("version", 1)
	e.now = e.now.Add(11 * time.Minute)
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); !errors.Is(err, directchargecontract.ErrPreflightExpired) {
		t.Fatalf("expired: %v", err)
	}
	if len(e.orders.status) != 0 {
		t.Fatal("no order should be created")
	}
}

func TestCreateOrderFailureReleasesPreflight(t *testing.T) {
	e := setup(t)
	view := e.preflight(t, 1)
	rejected := &directchargecontract.OrderRejectedError{MessageKey: "error.risk_order_rate_limited", TooMany: true}
	e.orders.createErr = rejected
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); !errors.As(err, &rejected) {
		t.Fatalf("expected order rejection, got %v", err)
	}
	e.orders.createErr = nil
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: view.Ref}); err != nil {
		t.Fatalf("retry after released preflight: %v", err)
	}
}

func TestSecondBuyerBlockedBeforeOrderCreated(t *testing.T) {
	e := setup(t)
	first := e.preflight(t, 1)
	second := e.preflight(t, 2)
	if _, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: first.Ref}); err != nil {
		t.Fatalf("first order: %v", err)
	}
	// 第二个预检在账号被占用前完成；此时下单必须被拒绝且不留下可支付订单
	_, err := e.service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 2, PreflightRef: second.Ref})
	if !errors.Is(err, directchargecontract.ErrAccountBusy) {
		t.Fatalf("expected account busy, got %v", err)
	}
	if len(e.orders.canceled) != 0 {
		t.Fatalf("busy account should be detected before creating an order, canceled=%v", e.orders.canceled)
	}
}

func TestDisabledService(t *testing.T) {
	service := directchargeapp.NewService(directchargeapp.Options{})
	if _, err := service.Preflight(context.Background(), directchargeapp.PreflightInput{UserID: 1, SKUID: 1, Credential: "x"}); !errors.Is(err, directchargecontract.ErrDisabled) {
		t.Fatalf("expected disabled, got %v", err)
	}
	if _, err := service.CreateOrder(directchargeapp.CreateOrderInput{UserID: 1, PreflightRef: "x"}); !errors.Is(err, directchargecontract.ErrDisabled) {
		t.Fatalf("expected disabled, got %v", err)
	}
}

func TestStoreCreateTaskRejectsHeldAccount(t *testing.T) {
	e := setup(t)
	store := gormstore.New(e.db)
	newTask := func(orderID uint) *directchargedomain.Task {
		return &directchargedomain.Task{
			OrderID: orderID, OrderNo: fmt.Sprintf("C%d", orderID), UserID: 1, SpecID: e.spec.ID, SKUID: 70,
			Status: directchargedomain.TaskAwaitingPayment, Version: 1, Product: "gpt", Plan: "plus",
			AccountFingerprint: "fp-1", CreatedAt: e.now, UpdatedAt: e.now,
		}
	}
	if err := store.CreateTask(newTask(1), e.now.Add(time.Hour), "test"); err != nil {
		t.Fatalf("first task: %v", err)
	}
	if err := store.CreateTask(newTask(2), e.now.Add(time.Hour), "test"); !errors.Is(err, directchargecontract.ErrAccountBusy) {
		t.Fatalf("expected account busy, got %v", err)
	}
	var count int64
	e.db.Model(&directchargedomain.Task{}).Count(&count)
	if count != 1 {
		t.Fatalf("conflicting task must be rolled back, got %d tasks", count)
	}
}
