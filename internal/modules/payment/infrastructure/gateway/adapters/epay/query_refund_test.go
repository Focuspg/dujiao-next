package epayadapter

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	paymentcontract "github.com/dujiao-next/internal/modules/payment/contract"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

// newSignedEpayServer 返回一个用平台私钥签名响应的易支付 V2 模拟网关，以及指向它的渠道配置。
func newSignedEpayServer(t *testing.T, respond func(path string, form map[string]string) map[string]string) jsonmap.JSON {
	t.Helper()
	merchantPriv, _ := generateAdapterRSAKeyPair(t)
	platformPriv, platformPub := generateAdapterRSAKeyPair(t)
	block, _ := pem.Decode([]byte(platformPriv))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse platform key: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
			return
		}
		form := map[string]string{}
		for k, v := range r.PostForm {
			form[k] = v[0]
		}
		fields := respond(r.URL.Path, form)
		hashed := sha256.Sum256([]byte(buildEpayAdapterSignContent(fields)))
		sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, hashed[:])
		if err != nil {
			t.Errorf("sign: %v", err)
		}
		body := map[string]string{"sign": base64.StdEncoding.EncodeToString(sig), "sign_type": "RSA"}
		for k, v := range fields {
			body[k] = v
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(server.Close)
	return jsonmap.JSON{
		"gateway_url":         server.URL,
		"epay_version":        "v2",
		"merchant_id":         "1002",
		"private_key":         merchantPriv,
		"platform_public_key": platformPub,
		"notify_url":          "https://api.example.com/api/v1/payments/callback",
		"return_url":          "https://shop.example.com/pay",
	}
}

func queryFields(status, refundMoney string) map[string]string {
	return map[string]string{
		"code": "0", "pid": "1002", "trade_no": "T100", "out_trade_no": "DJP-1", "status": status,
		"money": "20.00", "refundmoney": refundMoney, "endtime": "2024-01-01 12:00:00",
	}
}

func TestEpayAdapter_QueryPaymentByOrderNo(t *testing.T) {
	cases := []struct {
		name         string
		status       string
		refundMoney  string
		wantStatus   string
		wantAbnormal bool
	}{
		{"paid", "1", "0.00", constants.PaymentStatusSuccess, false},
		{"unpaid", "0", "", constants.PaymentStatusPending, false},
		{"partially refunded", "1", "5.00", constants.PaymentStatusPending, true},
		{"refunded", "2", "20.00", constants.PaymentStatusPending, true},
		{"frozen", "3", "", constants.PaymentStatusPending, true},
		{"preauthorized", "4", "", constants.PaymentStatusPending, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := newSignedEpayServer(t, func(path string, form map[string]string) map[string]string {
				if path != "/api/pay/query" || form["out_trade_no"] != "DJP-1" {
					t.Errorf("unexpected query %s %v", path, form)
				}
				return queryFields(tc.status, tc.refundMoney)
			})
			querier := NewEpayAdapter().(paymentcontract.GatewayOrderQuerier)
			result, err := querier.QueryPaymentByOrderNo(context.Background(), raw, "DJP-1")
			if err != nil {
				t.Fatalf("query: %v", err)
			}
			if result.Status != tc.wantStatus || result.Abnormal != tc.wantAbnormal || result.GatewayStatus != tc.status {
				t.Fatalf("result = %+v", result)
			}
			if result.OrderNo != "DJP-1" || result.ProviderRef != "T100" || result.Currency != "CNY" ||
				!result.Amount.Decimal.Equal(decimal.RequireFromString("20")) {
				t.Fatalf("facts = %+v", result.GatewayCallbackResult)
			}
			if tc.wantStatus == constants.PaymentStatusSuccess {
				want := time.Date(2024, 1, 1, 4, 0, 0, 0, time.UTC)
				if result.PaidAt == nil || !result.PaidAt.Equal(want) {
					t.Fatalf("paid at = %v, want %v", result.PaidAt, want)
				}
			}
		})
	}
}

func TestEpayAdapter_QueryPaymentByOrderNoRequiresV2(t *testing.T) {
	raw := jsonmap.JSON{
		"gateway_url": "https://epay.example.com", "epay_version": "v1", "merchant_id": "1001",
		"merchant_key": "k", "notify_url": "https://a.example.com/n", "return_url": "https://a.example.com/r",
	}
	_, err := NewEpayAdapter().(paymentcontract.GatewayOrderQuerier).QueryPaymentByOrderNo(context.Background(), raw, "DJP-1")
	if !errors.Is(err, paymentcontract.ErrGatewayConfigInvalid) {
		t.Fatalf("err = %v", err)
	}
}

func TestEpayAdapter_RefundAndQueryRefund(t *testing.T) {
	raw := newSignedEpayServer(t, func(path string, form map[string]string) map[string]string {
		switch path {
		case "/api/pay/refund":
			if form["out_trade_no"] != "DJP-1" || form["out_refund_no"] != "R-1" || form["money"] != "20.00" {
				t.Errorf("unexpected refund %v", form)
			}
			if _, sent := form["trade_no"]; sent {
				t.Errorf("refund should send a single order number")
			}
			return map[string]string{"code": "0", "refund_no": "RF1", "out_refund_no": "R-1", "trade_no": "T100", "money": "20.00", "reducemoney": "20.00"}
		case "/api/pay/refundquery":
			return map[string]string{"code": "0", "refund_no": "RF1", "out_refund_no": "R-1", "trade_no": "T100", "out_trade_no": "DJP-1", "money": "20.00", "status": "0"}
		}
		t.Errorf("unexpected path %s", path)
		return nil
	})
	refunder := NewEpayAdapter().(paymentcontract.GatewayRefunder)
	input := paymentcontract.GatewayRefundInput{
		OrderNo: "DJP-1", ProviderRef: "T100", RefundNo: "R-1", Currency: "CNY",
		Amount: money.FromDecimal(decimal.RequireFromString("20")),
	}
	result, err := refunder.Refund(context.Background(), raw, input)
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if result.Status != paymentcontract.GatewayRefundStatusSucceeded || result.RefundNo != "R-1" || result.ProviderRefundRef != "RF1" {
		t.Fatalf("refund result = %+v", result)
	}

	queried, err := refunder.QueryRefund(context.Background(), raw, "R-1")
	if err != nil {
		t.Fatalf("query refund: %v", err)
	}
	if queried.Status != paymentcontract.GatewayRefundStatusFailed {
		t.Fatalf("query refund status = %s", queried.Status)
	}

	input.Currency = "USD"
	if _, err := refunder.Refund(context.Background(), raw, input); !errors.Is(err, paymentcontract.ErrGatewayConfigInvalid) {
		t.Fatalf("non-CNY refund: err = %v", err)
	}
}

func TestEpayAdapter_VerifyCallbackV2OnlyTradeSuccess(t *testing.T) {
	merchantPriv, _ := generateAdapterRSAKeyPair(t)
	platformPriv, platformPub := generateAdapterRSAKeyPair(t)
	block, _ := pem.Decode([]byte(platformPriv))
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("parse platform key: %v", err)
	}
	raw := jsonmap.JSON{
		"gateway_url": "https://epay.example.com", "epay_version": "v2", "merchant_id": "1002",
		"private_key": merchantPriv, "platform_public_key": platformPub,
		"notify_url": "https://a.example.com/n", "return_url": "https://a.example.com/r",
	}
	for tradeStatus, want := range map[string]string{
		constants.EpayTradeStatusSuccess: constants.PaymentStatusSuccess,
		"TRADE_FINISHED":                 constants.PaymentStatusPending,
	} {
		params := map[string]string{
			"pid": "1002", "out_trade_no": "DJP-1", "trade_no": "T100", "money": "20.00",
			"trade_status": tradeStatus, "timestamp": "1700000000",
		}
		hashed := sha256.Sum256([]byte(buildEpayAdapterSignContent(params)))
		sig, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, hashed[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		form := map[string][]string{"sign": {base64.StdEncoding.EncodeToString(sig)}, "sign_type": {"RSA"}}
		for k, v := range params {
			form[k] = []string{v}
		}
		result, err := NewEpayAdapter().(paymentcontract.GatewayCallbackVerifier).VerifyCallback(raw, form, nil)
		if err != nil {
			t.Fatalf("%s: verify: %v", tradeStatus, err)
		}
		if result.Status != want {
			t.Fatalf("%s: status = %s, want %s", tradeStatus, result.Status, want)
		}
	}
}
