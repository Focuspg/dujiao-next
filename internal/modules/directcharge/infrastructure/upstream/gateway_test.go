package upstream

import (
	"errors"
	"net/http"
	"testing"

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
