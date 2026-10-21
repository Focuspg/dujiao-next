package zovocard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	c, err := New(Config{BaseURL: srv.URL + "/openapi/v1", APIKey: "sk_test", AppID: "ak_test", Transport: srv.Client().Transport, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return c
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func TestNewRejectsNonHTTPS(t *testing.T) {
	for _, base := range []string{"http://zovocard.com/openapi/v1", "zovocard.com", ""} {
		if _, err := New(Config{BaseURL: base, APIKey: "sk"}); err == nil {
			t.Fatalf("expected error for %q", base)
		}
	}
	if _, err := New(Config{BaseURL: SandboxBaseURL}); err == nil {
		t.Fatal("expected error for missing api key")
	}
}

func TestInvalidProductNeverReachesUpstream(t *testing.T) {
	called := false
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	ctx := context.Background()
	cred := Credential{Mode: CredentialModeSession, Session: "s"}
	if _, err := c.GetPlans(ctx, "cluade"); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("plans: %v", err)
	}
	if _, err := c.Preflight(ctx, PreflightRequest{Product: "", Credential: cred}); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("preflight: %v", err)
	}
	if _, err := c.CreateOrder(ctx, CreateOrderRequest{Product: "GPT", Plan: "plus", CardID: 1, Credential: cred, ClientRequestID: "r1"}); !errors.Is(err, ErrInvalidProduct) {
		t.Fatalf("create: %v", err)
	}
	if called {
		t.Fatal("request with invalid product must not be sent")
	}
}

func TestCreateOrderSendsFixedIdentifiersAndReports202(t *testing.T) {
	var got map[string]any
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/openapi/v1/gpt-direct/orders" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-API-Key") != "sk_test" || r.Header.Get("X-App-Id") != "ak_test" {
			t.Errorf("missing auth headers")
		}
		if r.Header.Get("Idempotency-Key") != "rc-1-a1" {
			t.Errorf("idempotency key = %q", r.Header.Get("Idempotency-Key"))
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		writeJSON(w, http.StatusAccepted, `{"code":0,"msg":"accepted","data":{"id":981,"plan":"plus","status":"queued","quoted_amount_minor":98214,"currency":"PHP","client_request_id":"rc-1-a1"}}`)
	})
	res, err := c.CreateOrder(context.Background(), CreateOrderRequest{
		Product: ProductGPT, Plan: "plus", CardID: 12, PaymentCountry: "PH", PaymentCurrency: "PHP",
		Credential: Credential{Mode: CredentialModeSession, Session: "secret-session"}, PreflightToken: "pt",
		ClientRequestID: "rc-1-a1", PricingVersion: 2, NoAutoCardSwitch: true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !res.Accepted || res.Order.ID != 981 || res.Order.Outcome() != OutcomePending {
		t.Fatalf("unexpected result: %+v", res)
	}
	if got["product"] != "gpt" || got["card_id"].(float64) != 12 || got["client_request_id"] != "rc-1-a1" {
		t.Fatalf("unexpected body: %v", got)
	}
	if _, ok := got["raw_card"]; ok {
		t.Fatal("raw_card must never be sent")
	}
}

func TestCreateOrderValidatesRequestID(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { t.Error("should not be called") })
	cred := Credential{Mode: CredentialModeSession, Session: "s"}
	for _, id := range []string{"", strings.Repeat("a", 81), "has space"} {
		if _, err := c.CreateOrder(context.Background(), CreateOrderRequest{Product: ProductGPT, Plan: "plus", CardID: 1, Credential: cred, ClientRequestID: id}); err == nil {
			t.Fatalf("expected error for %q", id)
		}
	}
	if _, err := c.CreateOrder(context.Background(), CreateOrderRequest{Product: ProductGPT, Plan: "plus", Credential: cred, ClientRequestID: "x"}); err == nil {
		t.Fatal("expected error for missing card id")
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		status  int
		body    string
		kind    Kind
		unknown bool
	}{
		{400, `{"code":400,"error_code":"GPT_SESSION_INVALID","msg":"x"}`, KindCredential, false},
		{400, `{"code":400,"error_code":"GPT_PLAN_ALREADY_ACTIVE","msg":"x"}`, KindNotEligible, false},
		{400, `{"code":400,"error_code":"INSUFFICIENT_BALANCE","msg":"x"}`, KindFunds, false},
		{400, `{"code":400,"error_code":"GPT_PRICE_UNCONFIRMED","msg":"x"}`, KindRequote, false},
		{403, `{"code":403,"error_code":"GPT_DIRECT_ACCESS_DENIED","msg":"x"}`, KindConfig, false},
		{403, `{"code":403,"msg":"当前 IP 不在白名单内"}`, KindConfig, false},
		{503, `{"code":503,"error_code":"GPT_DIRECT_PAUSED","msg":"x"}`, KindUnavailable, false},
		{429, `{"code":429,"msg":"x"}`, KindRateLimited, false},
		{409, `{"code":409,"msg":"processing"}`, KindConflict, true},
		{500, `{"code":500,"error_code":"internal_error","msg":"x"}`, KindServer, true},
		{502, `<html>bad gateway</html>`, KindServer, true},
		{200, `{"code":404,"msg":"not found"}`, KindNotFound, false},
	}
	for _, tc := range cases {
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { writeJSON(w, tc.status, tc.body) })
		_, err := c.CreateOrder(context.Background(), CreateOrderRequest{Product: ProductGPT, Plan: "plus", CardID: 1, Credential: Credential{Mode: CredentialModeSession, Session: "s"}, ClientRequestID: "r"})
		if err == nil {
			t.Fatalf("%d %s: expected error", tc.status, tc.body)
		}
		if got := Classify(err); got != tc.kind {
			t.Errorf("%d %s: kind = %s, want %s", tc.status, tc.body, got, tc.kind)
		}
		if got := ResultUnknown(err); got != tc.unknown {
			t.Errorf("%d %s: unknown = %v, want %v", tc.status, tc.body, got, tc.unknown)
		}
		if strings.Contains(err.Error(), "html") {
			t.Errorf("error must not echo response body: %v", err)
		}
	}
}

func TestTransportErrorOnWriteIsUnknownOutcome(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { time.Sleep(3 * time.Second) })
	_, err := c.CreateOrder(context.Background(), CreateOrderRequest{Product: ProductGPT, Plan: "plus", CardID: 1, Credential: Credential{Mode: CredentialModeSession, Session: "secret-session"}, ClientRequestID: "r"})
	if Classify(err) != KindTransport || !ResultUnknown(err) {
		t.Fatalf("expected unknown transport error, got %v", err)
	}
	if strings.Contains(err.Error(), "secret-session") {
		t.Fatal("error leaked credential")
	}
	_, err = c.GetBalance(context.Background())
	if Classify(err) != KindTransport || ResultUnknown(err) {
		t.Fatalf("read timeout must not be unknown outcome: %v", err)
	}
}

func TestPreflightAndPlans(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/openapi/v1/gpt-direct/plans":
			if r.URL.Query().Get("product") != "gpt" {
				t.Errorf("product query missing")
			}
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"version":214,
				"plans":{"plus":{"key":"plus","enabled":true,"serviceFeeUsdMinor":15},"pro_5x":{"key":"pro_5x","enabled":false},"go":{"key":"go","enabled":true}},
				"registry":[{"key":"plus","product":"gpt","acc_plan_key":"plus","purchasable":true,"checkout_currency":"PHP","checkout_amount_minor":98214},
				            {"key":"pro_5x","product":"gpt","acc_plan_key":"pro_5x","purchasable":true},
				            {"key":"go","product":"gpt","acc_plan_key":"go","purchasable":false},
				            {"key":"pro","product":"claude","acc_plan_key":"claude_pro","purchasable":true}]}}`)
		case "/openapi/v1/gpt-direct/preflight":
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"email":"u***@example.com","currentPlan":"free","preflight_token":"tok","preflight_expires_at":"2026-06-04T02:10:00Z","quotes":{"plus":{"currency":"PHP","plan":"plus","amountMinor":98214,"minorUnitExponent":2}},"quote_error":""}}`)
		}
	})
	plans, err := c.GetPlans(context.Background(), ProductGPT)
	if err != nil {
		t.Fatalf("plans: %v", err)
	}
	if sp, ok := plans.Sellable("plus"); !ok || sp.ServiceFeeUsdMinor != 15 || sp.CheckoutAmountMinor != 98214 {
		t.Fatalf("plus should be sellable: %+v", sp)
	}
	for _, key := range []string{"pro_5x", "go", "pro", "missing"} {
		if _, ok := plans.Sellable(key); ok {
			t.Errorf("%s must not be sellable", key)
		}
	}
	pf, err := c.Preflight(context.Background(), PreflightRequest{Product: ProductGPT, Credential: Credential{Mode: CredentialModeSession, Session: "s"}})
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if pf.PreflightToken != "tok" || pf.Quotes["plus"].AmountMinor != 98214 || pf.PreflightExpiresAt == nil {
		t.Fatalf("unexpected preflight: %+v", pf)
	}
}

func TestGetListAndCancelOrder(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/openapi/v1/gpt-direct/orders/981":
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"order":{"id":981,"status":"completed","final_amount_minor":98214,"client_request_id":"r"},"events":[{"step":"paid"}]}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/openapi/v1/gpt-direct/orders":
			if r.URL.Query().Get("page_size") != "100" {
				t.Errorf("page_size = %s", r.URL.Query().Get("page_size"))
			}
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"list":[{"id":1,"status":"queued","client_request_id":"a"},{"id":2,"status":"weird_new_state"}],"total":2}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/openapi/v1/gpt-direct/orders/5/cancel":
			writeJSON(w, 409, `{"code":409,"msg":"已开始支付"}`)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	detail, err := c.GetOrder(context.Background(), 981)
	if err != nil || detail.Order.Outcome() != OutcomeSucceeded || len(detail.Events) != 1 || len(detail.Order.Raw) == 0 {
		t.Fatalf("get order: %+v %v", detail, err)
	}
	page, err := c.ListOrders(context.Background(), 1, 500)
	if err != nil || page.Total != 2 || page.List[1].Outcome() != OutcomeUnrecognized {
		t.Fatalf("list: %+v %v", page, err)
	}
	if _, err := c.CancelOrder(context.Background(), 5); Classify(err) != KindConflict {
		t.Fatalf("cancel conflict: %v", err)
	}
}

func TestCredentialIsRedactedWhenFormatted(t *testing.T) {
	cred := Credential{Mode: CredentialModeSession, Session: "super-secret"}
	for _, s := range []string{cred.String(), cred.GoString()} {
		if strings.Contains(s, "super-secret") {
			t.Fatalf("credential leaked: %s", s)
		}
	}
}

func TestGetBalanceAndCard(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/openapi/v1/balance":
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"balance":"52.30","spendable_balance":"50.15","account_reserve_amount":"2.15","currency":"USD"}}`)
		case "/openapi/v1/cards/525944":
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"id":525944,"available_amount":"12.34","balance_stale":true,"balance_as_of":"2026-10-21T08:00:00Z"}}`)
		case "/openapi/v1/cards/7":
			writeJSON(w, 200, `{"code":0,"msg":"ok","data":{"id":7,"available_amount":3,"balance_stale":false,"balance_as_of":null}}`)
		case "/openapi/v1/cards/404":
			writeJSON(w, 404, `{"code":404,"msg":"card not found","error_code":"CARD_NOT_FOUND"}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	ctx := context.Background()
	balance, err := c.GetBalance(ctx)
	if err != nil {
		t.Fatalf("balance: %v", err)
	}
	if balance.SpendableBalance.String() != "50.15" || balance.Currency != "USD" {
		t.Fatalf("balance = %+v", balance)
	}

	card, err := c.GetCard(ctx, 525944)
	if err != nil {
		t.Fatalf("card: %v", err)
	}
	if card.ID != 525944 || card.AvailableAmount.String() != "12.34" || !card.BalanceStale || card.BalanceAsOfText() != "2026-10-21T08:00:00Z" {
		t.Fatalf("card = %+v as_of=%q", card, card.BalanceAsOfText())
	}

	card, err = c.GetCard(ctx, 7)
	if err != nil {
		t.Fatalf("numeric card: %v", err)
	}
	if card.AvailableAmount.String() != "3" || card.BalanceStale || card.BalanceAsOfText() != "" {
		t.Fatalf("numeric card = %+v", card)
	}

	if _, err := c.GetCard(ctx, 404); err == nil {
		t.Fatal("missing card must return error")
	}
	if _, err := c.GetCard(ctx, 0); err == nil {
		t.Fatal("zero card id must be rejected locally")
	}
}
