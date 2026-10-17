package epay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dujiao-next/internal/constants"
)

// mockV2Gateway 模拟易支付 V2：用商户公钥校验请求签名，用平台私钥签名响应。
type mockV2Gateway struct {
	t           *testing.T
	merchantPub string
	platformKey string
	// respond 返回响应字段；sign 为 false 时不签名。
	respond  func(path string, form map[string]string) (fields map[string]interface{}, sign bool)
	lastPath string
	lastForm map[string]string
}

func newMockV2Gateway(t *testing.T) (*mockV2Gateway, *Config, *httptest.Server) {
	t.Helper()
	merchantPriv, merchantPub := generateEpayRSAKeyPair(t)
	platformPriv, platformPub := generateEpayRSAKeyPair(t)
	gw := &mockV2Gateway{t: t, merchantPub: merchantPub, platformKey: platformPriv}
	server := httptest.NewServer(http.HandlerFunc(gw.serve))
	t.Cleanup(server.Close)
	cfg := &Config{
		GatewayURL:  server.URL,
		EpayVersion: VersionV2,
		MerchantID:  "1002",
		PrivateKey:  merchantPriv,
		PublicKey:   platformPub,
		NotifyURL:   "https://api.example.com/api/v1/payments/callback",
		ReturnURL:   "https://shop.example.com/pay",
	}
	cfg.Normalize()
	return gw, cfg, server
}

func (g *mockV2Gateway) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		g.t.Errorf("parse form: %v", err)
		return
	}
	form := make(map[string]string, len(r.PostForm))
	for key, values := range r.PostForm {
		if len(values) != 1 {
			g.t.Errorf("request param %s sent %d times", key, len(values))
		}
		form[key] = values[0]
	}
	if form["pid"] == "" || form["timestamp"] == "" || form["sign_type"] != epaySignTypeRSA {
		g.t.Errorf("request missing pid/timestamp/sign_type: %v", form)
	}
	if err := verifyRSA(buildSignContent(form), form["sign"], g.merchantPub); err != nil {
		g.t.Errorf("request signature invalid on %s", r.URL.Path)
	}
	g.lastPath = r.URL.Path
	g.lastForm = form
	fields, sign := g.respond(r.URL.Path, form)
	if sign {
		fields = g.sign(fields)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(fields)
}

func (g *mockV2Gateway) sign(fields map[string]interface{}) map[string]interface{} {
	g.t.Helper()
	params := map[string]string{}
	for key, value := range fields {
		switch v := value.(type) {
		case string:
			params[key] = v
		case int:
			params[key] = jsonNumberText(v)
		}
	}
	sign, err := signRSA(buildSignContent(params), g.platformKey)
	if err != nil {
		g.t.Fatalf("sign response: %v", err)
	}
	signed := make(map[string]interface{}, len(fields)+2)
	for key, value := range fields {
		signed[key] = value
	}
	signed["sign"] = sign
	signed["sign_type"] = epaySignTypeRSA
	return signed
}

func jsonNumberText(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func createInput() CreateInput {
	return CreateInput{
		OrderNo:     "DJP-V2-100",
		Amount:      "20.00",
		Subject:     "测试订单",
		ChannelType: constants.PaymentChannelTypeAlipay,
		ClientIP:    "127.0.0.1",
		NotifyURL:   "https://api.example.com/api/v1/payments/callback",
		ReturnURL:   "https://shop.example.com/pay",
	}
}

func TestCreateV2AcceptsSignedJumpAndQRCode(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	for payType, wantURL := range map[string]bool{"jump": true, "qrcode": false} {
		gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
			return map[string]interface{}{
				"code": 0, "msg": "", "trade_no": "T100", "pay_type": payType,
				"pay_info": "https://pay.example.com/" + payType, "timestamp": "1700000000",
			}, true
		}
		result, err := CreatePayment(context.Background(), cfg, createInput())
		if err != nil {
			t.Fatalf("%s: create failed: %v", payType, err)
		}
		if gw.lastPath != epayAPIPathV2 || gw.lastForm["method"] != cfg.Method {
			t.Fatalf("%s: unexpected request %s %v", payType, gw.lastPath, gw.lastForm)
		}
		got := result.QRCode
		if wantURL {
			got = result.PayURL
		}
		if got != "https://pay.example.com/"+payType || result.TradeNo != "T100" {
			t.Fatalf("%s: unexpected result %+v", payType, result)
		}
	}
}

func TestCreateV2RejectsUnsafeResponses(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	ok := map[string]interface{}{"code": 0, "trade_no": "T100", "pay_type": "jump", "pay_info": "https://pay.example.com/x"}
	cases := []struct {
		name    string
		fields  map[string]interface{}
		sign    bool
		tamper  func(map[string]interface{})
		wantErr error
	}{
		{name: "unsigned", fields: ok, sign: false, wantErr: ErrSignatureInvalid},
		{name: "tampered pay_info", fields: ok, sign: true, tamper: func(m map[string]interface{}) { m["pay_info"] = "https://evil.example.com" }, wantErr: ErrSignatureInvalid},
		{name: "injected unknown field", fields: ok, sign: true, tamper: func(m map[string]interface{}) { m["extra"] = "x" }, wantErr: ErrSignatureInvalid},
		{name: "html pay_type", fields: map[string]interface{}{"code": 0, "trade_no": "T100", "pay_type": "html", "pay_info": "<form></form>"}, sign: true, wantErr: ErrPayTypeUnsupported},
		{name: "urlscheme pay_type", fields: map[string]interface{}{"code": 0, "trade_no": "T100", "pay_type": "urlscheme", "pay_info": "alipays://x"}, sign: true, wantErr: ErrPayTypeUnsupported},
		{name: "missing trade_no", fields: map[string]interface{}{"code": 0, "pay_type": "jump", "pay_info": "https://pay.example.com/x"}, sign: true, wantErr: ErrResponseInvalid},
		{name: "rejected", fields: map[string]interface{}{"code": -1, "msg": "商户未开通"}, sign: false, wantErr: ErrRejected},
	}
	for _, tc := range cases {
		gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
			fields := tc.fields
			if tc.sign {
				fields = gw.sign(fields)
			}
			if tc.tamper != nil {
				copied := map[string]interface{}{}
				for k, v := range fields {
					copied[k] = v
				}
				tc.tamper(copied)
				fields = copied
			}
			return fields, false
		}
		_, err := CreatePayment(context.Background(), cfg, createInput())
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
	}
}

func TestCreateV2VerifiesUnknownFieldsSignedByPlatform(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
		return map[string]interface{}{
			"code": 0, "trade_no": "T100", "pay_type": "jump", "pay_info": "https://pay.example.com/x",
			"new_field_from_platform": "v", "nested": map[string]interface{}{"ignored": "x"},
		}, true
	}
	if _, err := CreatePayment(context.Background(), cfg, createInput()); err != nil {
		t.Fatalf("create failed: %v", err)
	}
}

func TestQueryOrderV2(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	paid := map[string]interface{}{
		"code": 0, "msg": "succ", "pid": "1002", "trade_no": "T100", "out_trade_no": "DJP-1",
		"api_trade_no": "2024000", "type": "alipay", "status": 1, "money": "20.00",
		"refundmoney": "0.00", "endtime": "2024-01-01 12:00:00",
	}
	gw.respond = func(path string, form map[string]string) (map[string]interface{}, bool) {
		if path != epayQueryPathV2 || form["out_trade_no"] != "DJP-1" {
			t.Errorf("unexpected query %s %v", path, form)
		}
		if _, sent := form["trade_no"]; sent {
			t.Errorf("empty trade_no must not be sent")
		}
		return paid, true
	}
	result, err := QueryOrder(context.Background(), cfg, QueryInput{OutTradeNo: "DJP-1"})
	if err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if result.Status != QueryStatusPaid || result.Money != "20.00" || result.TradeNo != "T100" {
		t.Fatalf("unexpected result %+v", result)
	}

	mutate := func(key string, value interface{}) {
		gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
			fields := map[string]interface{}{}
			for k, v := range paid {
				fields[k] = v
			}
			fields[key] = value
			return fields, true
		}
	}
	for _, tc := range []struct {
		key   string
		value interface{}
	}{
		{"pid", "9999"},
		{"out_trade_no", "DJP-OTHER"},
		{"status", "x"},
		{"money", ""},
	} {
		mutate(tc.key, tc.value)
		if _, err := QueryOrder(context.Background(), cfg, QueryInput{OutTradeNo: "DJP-1"}); !errors.Is(err, ErrResponseInvalid) {
			t.Fatalf("%s=%v: err = %v, want ErrResponseInvalid", tc.key, tc.value, err)
		}
	}
	mutate("trade_no", "T100")
	if _, err := QueryOrder(context.Background(), cfg, QueryInput{OutTradeNo: "DJP-1", TradeNo: "T200"}); !errors.Is(err, ErrResponseInvalid) {
		t.Fatalf("trade_no mismatch: err = %v", err)
	}
	if _, err := QueryOrder(context.Background(), cfg, QueryInput{}); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("empty input: err = %v", err)
	}
}

func TestQueryOrderRequiresV2AndPlatformKey(t *testing.T) {
	_, cfg, _ := newMockV2Gateway(t)
	v1 := *cfg
	v1.EpayVersion = VersionV1
	if _, err := QueryOrder(context.Background(), &v1, QueryInput{OutTradeNo: "DJP-1"}); !errors.Is(err, ErrVersionUnsupported) {
		t.Fatalf("v1: err = %v", err)
	}
	noKey := *cfg
	noKey.PublicKey = ""
	if _, err := QueryOrder(context.Background(), &noKey, QueryInput{OutTradeNo: "DJP-1"}); !errors.Is(err, ErrConfigInvalid) {
		t.Fatalf("missing platform key: err = %v", err)
	}
}

func TestQueryOrderRejectsWrongPlatformKey(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	_, otherPub := generateEpayRSAKeyPair(t)
	cfg.PublicKey = otherPub
	gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
		return map[string]interface{}{"code": 0, "pid": "1002", "trade_no": "T1", "out_trade_no": "DJP-1", "status": 1, "money": "1.00"}, true
	}
	if _, err := QueryOrder(context.Background(), cfg, QueryInput{OutTradeNo: "DJP-1"}); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestRefundV2(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	gw.respond = func(path string, form map[string]string) (map[string]interface{}, bool) {
		if path != epayRefundPathV2 || form["money"] != "10.00" || form["out_refund_no"] != "R-1" || form["out_trade_no"] != "DJP-1" {
			t.Errorf("unexpected refund request %s %v", path, form)
		}
		return map[string]interface{}{
			"code": 0, "refund_no": "RF1", "out_refund_no": "R-1", "trade_no": "T100",
			"money": "10.00", "reducemoney": "10.00",
		}, true
	}
	result, err := Refund(context.Background(), cfg, RefundInput{OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "10"})
	if err != nil {
		t.Fatalf("refund failed: %v", err)
	}
	if !result.Succeeded || result.RefundNo != "RF1" {
		t.Fatalf("unexpected result %+v", result)
	}

	for name, input := range map[string]RefundInput{
		"missing out_refund_no": {OutTradeNo: "DJP-1", Money: "10"},
		"missing order":         {OutRefundNo: "R-1", Money: "10"},
		"zero money":            {OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "0"},
		"three decimals":        {OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "1.001"},
	} {
		if _, err := Refund(context.Background(), cfg, input); !errors.Is(err, ErrConfigInvalid) {
			t.Fatalf("%s: err = %v, want ErrConfigInvalid", name, err)
		}
	}

	gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
		return map[string]interface{}{"code": 0, "refund_no": "RF1", "out_refund_no": "R-1", "money": "9.00"}, true
	}
	if _, err := Refund(context.Background(), cfg, RefundInput{OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "10"}); !errors.Is(err, ErrResponseInvalid) {
		t.Fatalf("money mismatch: err = %v", err)
	}
	gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
		return map[string]interface{}{"code": 0, "refund_no": "RF1", "out_refund_no": "R-2", "money": "10.00"}, true
	}
	if _, err := Refund(context.Background(), cfg, RefundInput{OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "10"}); !errors.Is(err, ErrResponseInvalid) {
		t.Fatalf("refund no mismatch: err = %v", err)
	}
}

func TestRefundV2TransportFailureIsUnknownOutcome(t *testing.T) {
	_, cfg, server := newMockV2Gateway(t)
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := Refund(context.Background(), cfg, RefundInput{OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "10"})
	if !errors.Is(err, ErrRequestFailed) || errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRequestFailed only", err)
	}

	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", epayMaxResponseBytes+10)))
	})
	if _, err := Refund(context.Background(), cfg, RefundInput{OutTradeNo: "DJP-1", OutRefundNo: "R-1", Money: "10"}); !errors.Is(err, ErrRequestFailed) {
		t.Fatalf("oversized body: err = %v", err)
	}
}

func TestQueryRefundV2(t *testing.T) {
	gw, cfg, _ := newMockV2Gateway(t)
	status := 1
	gw.respond = func(path string, form map[string]string) (map[string]interface{}, bool) {
		if path != epayRefundQueryPathV2 || form["out_refund_no"] != "R-1" {
			t.Errorf("unexpected refund query %s %v", path, form)
		}
		return map[string]interface{}{
			"code": 0, "refund_no": "RF1", "out_refund_no": "R-1", "trade_no": "T100",
			"out_trade_no": "DJP-1", "money": "10.00", "reducemoney": "10.00", "status": status,
			"addtime": "2024-01-01 12:00:00",
		}, true
	}
	result, err := QueryRefund(context.Background(), cfg, "R-1")
	if err != nil || !result.Succeeded {
		t.Fatalf("query refund: %+v, %v", result, err)
	}
	status = 0
	result, err = QueryRefund(context.Background(), cfg, "R-1")
	if err != nil || result.Succeeded {
		t.Fatalf("failed refund: %+v, %v", result, err)
	}
	status = 7
	if _, err := QueryRefund(context.Background(), cfg, "R-1"); !errors.Is(err, ErrResponseInvalid) {
		t.Fatalf("unknown status: err = %v", err)
	}

	gw.respond = func(string, map[string]string) (map[string]interface{}, bool) {
		return map[string]interface{}{"code": -1, "msg": "退款记录不存在"}, false
	}
	if _, err := QueryRefund(context.Background(), cfg, "R-1"); !errors.Is(err, ErrRejected) {
		t.Fatalf("not found: err = %v", err)
	}
}

func TestVerifyCallbackRejectsDuplicateParams(t *testing.T) {
	privateKeyPEM, publicKeyPEM := generateEpayRSAKeyPair(t)
	cfg := &Config{EpayVersion: VersionV2, MerchantID: "2001", PrivateKey: privateKeyPEM, PublicKey: publicKeyPEM}
	form := signEpayV2CallbackForm(t, cfg, map[string]string{
		"pid": "2001", "out_trade_no": "ORDER-1", "trade_no": "T1", "money": "10.00",
		"trade_status": constants.EpayTradeStatusSuccess,
	})
	if err := VerifyCallback(cfg, form); err != nil {
		t.Fatalf("baseline verify failed: %v", err)
	}
	form["money"] = append(form["money"], "0.01")
	if err := VerifyCallback(cfg, form); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("duplicate param: err = %v", err)
	}
}
