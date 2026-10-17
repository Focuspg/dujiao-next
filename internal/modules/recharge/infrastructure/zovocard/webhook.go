package zovocard

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidSignature 回调签名不匹配
var ErrInvalidSignature = errors.New("zovocard: invalid webhook signature")

// SignatureHeader 回调签名请求头
const SignatureHeader = "X-Signature"

// VerifySignature 校验 X-Signature = hex(HMAC-SHA256(webhook_secret, 原始请求体))，常量时间比较。
// 必须传入未经反序列化的原始 body。
func VerifySignature(secret string, rawBody []byte, signature string) error {
	if strings.TrimSpace(secret) == "" {
		return errors.New("zovocard: webhook secret is not configured")
	}
	got, err := hex.DecodeString(strings.ToLower(strings.TrimSpace(signature)))
	if err != nil || len(got) != sha256.Size {
		return ErrInvalidSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	if !hmac.Equal(mac.Sum(nil), got) {
		return ErrInvalidSignature
	}
	return nil
}

// Sign 计算签名（测试与联调工具使用）
func Sign(secret string, rawBody []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	return hex.EncodeToString(mac.Sum(nil))
}

// DirectEvent GPT 直充生命周期回调（type 以 gpt_direct. 开头）。
// 回调只是通知：处理时以查单结果为准，不直接据此推进终态。
type DirectEvent struct {
	Type              string `json:"type"`
	EventID           string `json:"event_id"`
	OrderID           int64  `json:"order_id"`
	ClientRequestID   string `json:"client_request_id"`
	Source            string `json:"source"`
	AppID             string `json:"app_id"`
	Plan              string `json:"plan"`
	Status            string `json:"status"`
	Stage             string `json:"stage"`
	FromStatus        string `json:"from_status"`
	FromStage         string `json:"from_stage"`
	Message           string `json:"message"`
	AccountEmail      string `json:"account_email"`
	Currency          string `json:"currency"`
	QuotedAmountMinor int64  `json:"quoted_amount_minor"`
	FinalAmountMinor  int64  `json:"final_amount_minor"`
	ServiceFeeStatus  string `json:"service_fee_status"`
	FundingHoldStatus string `json:"funding_hold_status"`
	OccurredAt        string `json:"occurred_at"`
}

// IsDirect 是否为直充事件
func (e DirectEvent) IsDirect() bool { return strings.HasPrefix(e.Type, "gpt_direct.") }

// DedupKey 稳定的去重键：优先 event_id；缺失时用业务字段组合（不含接收时间）
func (e DirectEvent) DedupKey(rawBodyHash string) string {
	if id := strings.TrimSpace(e.EventID); id != "" {
		return id
	}
	if e.Type != "" && e.OrderID > 0 && e.Status != "" {
		return fmt.Sprintf("%s:%d:%s:%s:%s", e.Type, e.OrderID, e.Status, e.Stage, e.OccurredAt)
	}
	return "sha256:" + rawBodyHash
}

// BodyHash 原始请求体的 sha256（十六进制）
func BodyHash(rawBody []byte) string {
	sum := sha256.Sum256(rawBody)
	return hex.EncodeToString(sum[:])
}

// ParseDirectEvent 解析回调；非直充事件返回 ok=false 由调用方忽略
func ParseDirectEvent(rawBody []byte) (DirectEvent, bool, error) {
	var evt DirectEvent
	if err := json.Unmarshal(rawBody, &evt); err != nil {
		return DirectEvent{}, false, fmt.Errorf("zovocard: decode webhook: %w", err)
	}
	return evt, evt.IsDirect(), nil
}
