package directchargehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

type fakeService struct {
	preflightErr error
	createErr    error
	gotInput     directchargeapp.PreflightInput
	called       bool
}

func (f *fakeService) Preflight(_ context.Context, input directchargeapp.PreflightInput) (*directchargeapp.PreflightView, error) {
	f.called = true
	f.gotInput = input
	if f.preflightErr != nil {
		return nil, f.preflightErr
	}
	return &directchargeapp.PreflightView{Ref: "pf_x", AccountMasked: "u***@example.com"}, nil
}

func (f *fakeService) CreateOrder(directchargeapp.CreateOrderInput) (*directchargeapp.CreateOrderResult, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &directchargeapp.CreateOrderResult{OrderNo: "DJ1"}, nil
}

func (f *fakeService) GetOrder(uint, string) (*directchargeapp.TaskView, error) {
	return nil, directchargecontract.ErrNotFound
}

func newRouter(service UserService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", uint(9)) })
	handler := NewUserHandler(service)
	RegisterUserReadRoutes(r, handler)
	RegisterUserWriteRoutes(r, handler)
	return r
}

func do(r *gin.Engine, method, path, body string) (*httptest.ResponseRecorder, response.Response) {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var out response.Response
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

func TestPreflightPassesCredentialAndSetsNoStore(t *testing.T) {
	service := &fakeService{}
	w, out := do(newRouter(service), http.MethodPost, "/direct-charge/preflights", `{"sku_id":70,"mode":"session","credential":"secret"}`)
	if out.StatusCode != response.CodeOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected response: %d %s %q", out.StatusCode, w.Body.String(), w.Header().Get("Cache-Control"))
	}
	if service.gotInput.UserID != 9 || service.gotInput.SKUID != 70 || service.gotInput.Credential != "secret" {
		t.Fatalf("unexpected input: %+v", service.gotInput)
	}
}

func TestPreflightRejectsOversizedBodyWithoutCallingService(t *testing.T) {
	service := &fakeService{}
	body := `{"sku_id":70,"credential":"` + strings.Repeat("a", maxPreflightBody) + `"}`
	_, out := do(newRouter(service), http.MethodPost, "/direct-charge/preflights", body)
	if out.StatusCode != response.CodeBadRequest || service.called {
		t.Fatalf("oversized body must be rejected before the service: %+v called=%v", out, service.called)
	}
	if strings.Contains(out.Msg, "aaaa") {
		t.Fatal("response must not echo the credential")
	}
}

func TestErrorMapping(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{directchargecontract.ErrAccountBusy, response.CodeBadRequest},
		{directchargecontract.ErrUpstreamBusy, response.CodeTooManyRequests},
		{&directchargecontract.OrderRejectedError{MessageKey: "error.risk_order_rate_limited", TooMany: true}, response.CodeTooManyRequests},
		{directchargecontract.ErrOrderCreateFailed, response.CodeInternal},
	}
	for _, tc := range cases {
		_, out := do(newRouter(&fakeService{createErr: tc.err}), http.MethodPost, "/direct-charge/orders", `{"preflight_ref":"pf_x"}`)
		if out.StatusCode != tc.code {
			t.Errorf("%v: want %d got %d", tc.err, tc.code, out.StatusCode)
		}
	}
	_, out := do(newRouter(&fakeService{}), http.MethodGet, "/direct-charge/orders/DJ1", "")
	if out.StatusCode != response.CodeNotFound {
		t.Fatalf("missing order: %+v", out)
	}
	_, out = do(newRouter(&fakeService{}), http.MethodPost, "/direct-charge/orders", `{}`)
	if out.StatusCode != response.CodeBadRequest {
		t.Fatalf("empty ref: %+v", out)
	}
}
