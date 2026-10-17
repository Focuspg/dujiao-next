package directchargehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"

	"github.com/gin-gonic/gin"
)

type fakeWebhookService struct {
	err       error
	body      string
	signature string
}

func (f *fakeWebhookService) HandleWebhook(rawBody []byte, signature string) error {
	f.body, f.signature = string(rawBody), signature
	return f.err
}

func TestWebhookHandlerPassesRawBodyAndMapsErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name string
		err  error
		body string
		want int
	}{
		{name: "accepted", want: http.StatusOK, body: `{"event":"x"}`},
		{name: "bad signature", err: directchargecontract.ErrWebhookSignature, want: http.StatusUnauthorized, body: `{}`},
		{name: "disabled", err: directchargecontract.ErrDisabled, want: http.StatusServiceUnavailable, body: `{}`},
		{name: "store failure", err: errors.New("db"), want: http.StatusInternalServerError, body: `{}`},
		{name: "too large", want: http.StatusRequestEntityTooLarge, body: strings.Repeat("a", maxWebhookBody+1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := &fakeWebhookService{err: tc.err}
			router := gin.New()
			RegisterWebhookRoutes(router, NewWebhookHandler(service))
			req := httptest.NewRequest(http.MethodPost, "/webhooks/zovocard", strings.NewReader(tc.body))
			req.Header.Set("X-Signature", "sig")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want != http.StatusRequestEntityTooLarge && (service.body != tc.body || service.signature != "sig") {
				t.Fatalf("raw body and signature must reach the service unchanged")
			}
		})
	}
}
