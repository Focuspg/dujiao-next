package epay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const (
	epayQueryPathV2       = "/api/pay/query"
	epayRefundPathV2      = "/api/pay/refund"
	epayRefundQueryPathV2 = "/api/pay/refundquery"

	epayPayTypeJump = "jump"

	// V2 查单状态：0 未支付，1 已支付，2 已退款，3 已冻结，4 预授权。
	QueryStatusUnpaid     = 0
	QueryStatusPaid       = 1
	QueryStatusRefunded   = 2
	QueryStatusFrozen     = 3
	QueryStatusPreauthed  = 4
	RefundQueryStatusFail = 0
	RefundQueryStatusOK   = 1

	epayMaxResponseBytes = 1 << 20
	epayMaxErrorMsgRunes = 200
)

var (
	// ErrRejected 网关明确返回 code != 0。与 ErrResponseInvalid 同时成立，调用方可据此区分"网关拒绝"与"传输失败"。
	ErrRejected = errors.New("epay request rejected")
	// ErrPayTypeUnsupported 网关返回了站内无法安全展示的 pay_type（html/jsapi/urlscheme 等），应把渠道 method 配成 jump。
	ErrPayTypeUnsupported = errors.New("epay pay_type unsupported")
	// ErrVersionUnsupported 该能力只有 V2 接口提供。
	ErrVersionUnsupported = errors.New("epay capability requires v2")
)

// QueryInput V2 查单输入，OutTradeNo 与 TradeNo 至少填一个；两个都填时都会核对。
type QueryInput struct {
	OutTradeNo string
	TradeNo    string
}

// QueryResult V2 查单结果，已验签并核对商户号与单号。
type QueryResult struct {
	TradeNo     string
	OutTradeNo  string
	APITradeNo  string
	Type        string
	Status      int
	Money       string
	RefundMoney string
	EndTime     string
	Raw         map[string]interface{}
}

// RefundInput V2 退款输入。OutRefundNo 必填，作为幂等键：同一笔退款重试必须复用。
type RefundInput struct {
	OutTradeNo  string
	TradeNo     string
	OutRefundNo string
	Money       string
}

// RefundResult V2 退款或退款查询结果，已验签并核对退款单号。
type RefundResult struct {
	RefundNo    string
	OutRefundNo string
	TradeNo     string
	OutTradeNo  string
	Money       string
	ReduceMoney string
	// Succeeded 退款接口 code=0 或退款查询 status=1 时为 true。
	Succeeded bool
	Raw       map[string]interface{}
}

// QueryOrder 调用 V2 /api/pay/query 主动查单。
func QueryOrder(ctx context.Context, cfg *Config, input QueryInput) (*QueryResult, error) {
	outTradeNo := strings.TrimSpace(input.OutTradeNo)
	tradeNo := strings.TrimSpace(input.TradeNo)
	if outTradeNo == "" && tradeNo == "" {
		return nil, fmt.Errorf("%w: out_trade_no or trade_no is required", ErrConfigInvalid)
	}
	params := map[string]string{
		"out_trade_no": outTradeNo,
		"trade_no":     tradeNo,
	}
	raw, err := callV2(ctx, cfg, epayQueryPathV2, params)
	if err != nil {
		return nil, err
	}
	if err := expectResponseMerchant(cfg, raw); err != nil {
		return nil, err
	}
	result := &QueryResult{
		TradeNo:     responseValue(raw, "trade_no"),
		OutTradeNo:  responseValue(raw, "out_trade_no"),
		APITradeNo:  responseValue(raw, "api_trade_no"),
		Type:        responseValue(raw, "type"),
		Money:       responseValue(raw, "money"),
		RefundMoney: responseValue(raw, "refundmoney"),
		EndTime:     responseValue(raw, "endtime"),
		Raw:         raw,
	}
	if outTradeNo != "" && result.OutTradeNo != outTradeNo {
		return nil, fmt.Errorf("%w: out_trade_no mismatch", ErrResponseInvalid)
	}
	if tradeNo != "" && result.TradeNo != tradeNo {
		return nil, fmt.Errorf("%w: trade_no mismatch", ErrResponseInvalid)
	}
	if result.OutTradeNo == "" || result.TradeNo == "" {
		return nil, fmt.Errorf("%w: order number missing", ErrResponseInvalid)
	}
	status, err := strconv.Atoi(responseValue(raw, "status"))
	if err != nil {
		return nil, fmt.Errorf("%w: status invalid", ErrResponseInvalid)
	}
	result.Status = status
	if _, err := parseMoney(result.Money); err != nil {
		return nil, fmt.Errorf("%w: money invalid", ErrResponseInvalid)
	}
	return result, nil
}

// Refund 调用 V2 /api/pay/refund 发起原路退款。
//
// 返回 ErrRequestFailed 时退款结果未知，必须先用 QueryRefund(out_refund_no) 确认，再决定是否用同一 out_refund_no 重试。
func Refund(ctx context.Context, cfg *Config, input RefundInput) (*RefundResult, error) {
	outRefundNo := strings.TrimSpace(input.OutRefundNo)
	outTradeNo := strings.TrimSpace(input.OutTradeNo)
	tradeNo := strings.TrimSpace(input.TradeNo)
	if outRefundNo == "" {
		return nil, fmt.Errorf("%w: out_refund_no is required", ErrConfigInvalid)
	}
	if outTradeNo == "" && tradeNo == "" {
		return nil, fmt.Errorf("%w: out_trade_no or trade_no is required", ErrConfigInvalid)
	}
	amount, err := parseMoney(input.Money)
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Round(2)) {
		return nil, fmt.Errorf("%w: refund money invalid", ErrConfigInvalid)
	}
	params := map[string]string{
		"out_trade_no":  outTradeNo,
		"trade_no":      tradeNo,
		"out_refund_no": outRefundNo,
		"money":         amount.StringFixed(2),
	}
	raw, err := callV2(ctx, cfg, epayRefundPathV2, params)
	if err != nil {
		return nil, err
	}
	result := newRefundResult(raw)
	if result.OutRefundNo != outRefundNo {
		return nil, fmt.Errorf("%w: out_refund_no mismatch", ErrResponseInvalid)
	}
	if tradeNo != "" && result.TradeNo != "" && result.TradeNo != tradeNo {
		return nil, fmt.Errorf("%w: trade_no mismatch", ErrResponseInvalid)
	}
	if got, err := parseMoney(result.Money); err != nil || !got.Equal(amount) {
		return nil, fmt.Errorf("%w: refund money mismatch", ErrResponseInvalid)
	}
	result.Succeeded = true
	return result, nil
}

// QueryRefund 调用 V2 /api/pay/refundquery 按商户退款单号查询退款结果。
func QueryRefund(ctx context.Context, cfg *Config, outRefundNo string) (*RefundResult, error) {
	outRefundNo = strings.TrimSpace(outRefundNo)
	if outRefundNo == "" {
		return nil, fmt.Errorf("%w: out_refund_no is required", ErrConfigInvalid)
	}
	raw, err := callV2(ctx, cfg, epayRefundQueryPathV2, map[string]string{"out_refund_no": outRefundNo})
	if err != nil {
		return nil, err
	}
	result := newRefundResult(raw)
	if result.OutRefundNo != outRefundNo {
		return nil, fmt.Errorf("%w: out_refund_no mismatch", ErrResponseInvalid)
	}
	if _, err := parseMoney(result.Money); err != nil {
		return nil, fmt.Errorf("%w: refund money invalid", ErrResponseInvalid)
	}
	switch responseValue(raw, "status") {
	case strconv.Itoa(RefundQueryStatusOK):
		result.Succeeded = true
	case strconv.Itoa(RefundQueryStatusFail):
		result.Succeeded = false
	default:
		return nil, fmt.Errorf("%w: refund status invalid", ErrResponseInvalid)
	}
	return result, nil
}

func newRefundResult(raw map[string]interface{}) *RefundResult {
	return &RefundResult{
		RefundNo:    responseValue(raw, "refund_no"),
		OutRefundNo: responseValue(raw, "out_refund_no"),
		TradeNo:     responseValue(raw, "trade_no"),
		OutTradeNo:  responseValue(raw, "out_trade_no"),
		Money:       responseValue(raw, "money"),
		ReduceMoney: responseValue(raw, "reducemoney"),
		Raw:         raw,
	}
}

// callV2 补齐 pid/timestamp 并签名后请求 V2 接口；只有 code=0 且平台签名有效的响应才会返回。
func callV2(ctx context.Context, cfg *Config, apiPath string, params map[string]string) (map[string]interface{}, error) {
	if cfg == nil {
		return nil, ErrConfigInvalid
	}
	if cfg.EpayVersion != VersionV2 {
		return nil, ErrVersionUnsupported
	}
	if strings.TrimSpace(cfg.GatewayURL) == "" || strings.TrimSpace(cfg.MerchantID) == "" || strings.TrimSpace(cfg.PrivateKey) == "" {
		return nil, ErrConfigInvalid
	}
	if strings.TrimSpace(cfg.PublicKey) == "" {
		return nil, fmt.Errorf("%w: platform_public_key is required for v2", ErrConfigInvalid)
	}
	signed := make(map[string]string, len(params)+4)
	for key, value := range params {
		signed[key] = value
	}
	signed["pid"] = cfg.MerchantID
	signed["timestamp"] = strconv.FormatInt(time.Now().Unix(), 10)
	sign, err := signRSA(buildSignContent(signed), cfg.PrivateKey)
	if err != nil {
		return nil, ErrSignatureGenerate
	}
	signed["sign"] = sign
	signed["sign_type"] = cfg.SignType

	respBytes, err := postForm(ctx, buildEndpoint(cfg.GatewayURL, apiPath), signed)
	if err != nil {
		return nil, ErrRequestFailed
	}
	raw, err := decodeV2Response(respBytes)
	if err != nil {
		return nil, ErrResponseInvalid
	}
	if code := responseValue(raw, "code"); code != "0" {
		return nil, fmt.Errorf("%w: %w: code=%s msg=%s", ErrResponseInvalid, ErrRejected, code, truncateMsg(responseValue(raw, "msg")))
	}
	if err := verifyV2Response(cfg, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeV2Response(body []byte) (map[string]interface{}, error) {
	body, err := normalizeResponseBody(body)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var raw map[string]interface{}
	if err := decoder.Decode(&raw); err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, ErrResponseInvalid
	}
	return raw, nil
}

// verifyV2Response 用平台公钥校验响应签名。参与签名的是全部非空标量字段（与平台 PHP 端一致：
// 跳过数组/对象、null、空串和 sign/sign_type），未知字段同样参与，不能先过滤再验签。
func verifyV2Response(cfg *Config, raw map[string]interface{}) error {
	sign := responseValue(raw, "sign")
	if sign == "" {
		return fmt.Errorf("%w: response sign missing", ErrSignatureInvalid)
	}
	params := make(map[string]string, len(raw))
	for key, value := range raw {
		text, ok := signableValue(value)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		params[key] = text
	}
	return verifyRSA(buildSignContent(params), sign, cfg.PublicKey)
}

func signableValue(value interface{}) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case json.Number:
		return v.String(), true
	case bool:
		// PHP 字符串化：true → "1"，false → ""（空值不参与签名）。
		if v {
			return "1", true
		}
		return "", false
	default:
		return "", false
	}
}

func responseValue(raw map[string]interface{}, key string) string {
	text, _ := signableValue(raw[key])
	return strings.TrimSpace(text)
}

func expectResponseMerchant(cfg *Config, raw map[string]interface{}) error {
	if responseValue(raw, "pid") != strings.TrimSpace(cfg.MerchantID) {
		return fmt.Errorf("%w: pid mismatch", ErrResponseInvalid)
	}
	return nil
}

func parseMoney(raw string) (decimal.Decimal, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return decimal.Decimal{}, ErrResponseInvalid
	}
	return decimal.NewFromString(raw)
}

func truncateMsg(msg string) string {
	runes := []rune(msg)
	if len(runes) > epayMaxErrorMsgRunes {
		return string(runes[:epayMaxErrorMsgRunes])
	}
	return msg
}
