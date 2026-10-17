package upstream

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/zovocard"
)

func TestMapError(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{&zovocard.APIError{HTTPStatus: http.StatusBadRequest, ErrorCode: zovocard.CodeGPTSessionInvalid}, directchargecontract.ErrCredentialInvalid},
		{&zovocard.APIError{HTTPStatus: http.StatusBadRequest, ErrorCode: zovocard.CodePlanAlreadyActive}, directchargecontract.ErrNotEligible},
		{&zovocard.APIError{HTTPStatus: http.StatusBadRequest, ErrorCode: "SOMETHING_ELSE"}, directchargecontract.ErrCredentialInvalid},
		{&zovocard.APIError{HTTPStatus: http.StatusTooManyRequests}, directchargecontract.ErrUpstreamBusy},
		{&zovocard.APIError{HTTPStatus: http.StatusForbidden}, directchargecontract.ErrUpstreamBusy},
		{&zovocard.APIError{HTTPStatus: http.StatusBadGateway}, directchargecontract.ErrUpstreamBusy},
		{&zovocard.TransportError{Err: errors.New("timeout")}, directchargecontract.ErrUpstreamBusy},
	}
	for _, tc := range cases {
		if got := mapError("preflight", tc.err); !errors.Is(got, tc.want) {
			t.Errorf("%v: want %v, got %v", tc.err, tc.want, got)
		}
	}
}

func TestNewWithoutClientIsNil(t *testing.T) {
	if New(nil, "") != nil {
		t.Fatal("unconfigured upstream must be nil so the service reports disabled")
	}
}

func newTestGateway(t *testing.T, preflight, plans string) (*Gateway, *int) {
	t.Helper()
	planCalls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/openapi/v1/gpt-direct/preflight":
			_, _ = io.WriteString(w, preflight)
		case "/openapi/v1/gpt-direct/plans":
			planCalls++
			_, _ = io.WriteString(w, plans)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	client, err := zovocard.New(zovocard.Config{BaseURL: srv.URL + "/openapi/v1", APIKey: "sk_test", Transport: srv.Client().Transport, Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return &Gateway{client: client}, &planCalls
}

const testPlans = `{"code":0,"msg":"ok","data":{"version":263,
	"plans":{"plus":{"key":"plus","enabled":true},"credit250":{"key":"credit250","enabled":true,"serviceFeeUsdMinor":15},"credit500":{"key":"credit500","enabled":false}},
	"registry":[{"key":"plus","product":"gpt","acc_plan_key":"plus","purchasable":true,"checkout_currency":"PHP","checkout_amount_minor":98214},
	            {"key":"credit250","product":"gpt","acc_plan_key":"credit250","purchasable":true,"is_credit":true,"checkout_currency":"PHP","checkout_amount_minor":56500},
	            {"key":"credit500","product":"gpt","acc_plan_key":"credit500","purchasable":true,"is_credit":true,"checkout_currency":"PHP","checkout_amount_minor":113000}]}}`

const testPreflight = `{"code":0,"msg":"ok","data":{"email":"u@example.com","currentPlan":"plus","preflight_token":"tok",
	"payment_country":"PH","payment_currency":"PHP",
	"quotes":{"plus":{"currency":"PHP","plan":"plus","amountMinor":98214,"minorUnitExponent":2}},"quote_error":""}}`

func preflightRequest(plan string) directchargecontract.UpstreamPreflightRequest {
	return directchargecontract.UpstreamPreflightRequest{Product: "gpt", Plan: plan, PaymentCountry: "PH", PaymentCurrency: "PHP", Mode: "session", Credential: "s"}
}

func TestPreflightFillsCreditQuoteFromPlans(t *testing.T) {
	g, _ := newTestGateway(t, testPreflight, testPlans)
	result, err := g.Preflight(context.Background(), preflightRequest("credit250"))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	quote, ok := result.Quotes["credit250"]
	if !ok || quote.AmountMinor != 56500 || quote.Currency != "PHP" || quote.Exponent != 2 {
		t.Fatalf("credit250 quote should come from the price list: %+v", result.Quotes)
	}
}

func TestPreflightDoesNotInventQuotes(t *testing.T) {
	cases := []struct {
		name      string
		plan      string
		preflight string
	}{
		{"subscription plan keeps live quote only", "pro_5x", testPreflight},
		{"credit plan not purchasable", "credit500", testPreflight},
		{"quote error from upstream", "credit250", `{"code":0,"msg":"ok","data":{"email":"u@example.com","currentPlan":"plus","payment_currency":"PHP","quotes":{},"quote_error":"pricing unavailable"}}`},
		{"payment currency differs", "credit250", `{"code":0,"msg":"ok","data":{"email":"u@example.com","currentPlan":"plus","payment_currency":"USD","quotes":{}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, _ := newTestGateway(t, tc.preflight, testPlans)
			result, err := g.Preflight(context.Background(), preflightRequest(tc.plan))
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			if _, ok := result.Quotes[tc.plan]; ok {
				t.Fatalf("%s must not get a quote: %+v", tc.plan, result.Quotes)
			}
		})
	}
}

func TestPreflightSkipsPlansWhenLiveQuoteExists(t *testing.T) {
	g, planCalls := newTestGateway(t, testPreflight, testPlans)
	result, err := g.Preflight(context.Background(), preflightRequest("plus"))
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if result.Quotes["plus"].AmountMinor != 98214 || *planCalls != 0 {
		t.Fatalf("live quote must be used without reading the price list: %+v calls=%d", result.Quotes, *planCalls)
	}
}
