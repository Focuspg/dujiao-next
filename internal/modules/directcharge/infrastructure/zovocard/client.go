// Package zovocard 是 ZovoCard 开放 API（直充）的服务端客户端。
//
// 只覆盖账号代充需要的接口：余额、套餐、预检、直充建单/查单/取消。
// 约束（见需求说明书第 10 节）：
//   - product 必须显式传入且只允许白名单字面量，上游会把未知值静默当成 gpt 下单；
//   - 只用 card_id 选卡，不提供 raw_card；
//   - 凭据只在请求体里出现，错误信息与日志中不得带出请求体。
package zovocard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ProductionBaseURL 生产环境基地址
	ProductionBaseURL = "https://zovocard.com/openapi/v1"
	// SandboxBaseURL 沙盒环境基地址（账号、密钥、数据与生产隔离）
	SandboxBaseURL = "https://sandbox.zovocard.com/openapi/v1"

	defaultTimeout       = 30 * time.Second
	maxResponseBodyBytes = 2 << 20
	maxClientRequestID   = 80
)

// Config 客户端配置
type Config struct {
	BaseURL   string
	APIKey    string // app_secret，sk_ 开头
	AppID     string // 可选，ak_ 开头，用于双重校验
	Timeout   time.Duration
	Transport http.RoundTripper
}

// Client ZovoCard 开放 API 客户端，可并发使用
type Client struct {
	baseURL string
	apiKey  string
	appID   string
	http    *http.Client
}

// New 创建客户端；BaseURL 必须是 https，防止把密钥发往明文地址
func New(cfg Config) (*Client, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, errors.New("zovocard: base url is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, fmt.Errorf("zovocard: base url must be an absolute https url")
	}
	key := strings.TrimSpace(cfg.APIKey)
	if key == "" {
		return nil, errors.New("zovocard: api key is required")
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		baseURL: base,
		apiKey:  key,
		appID:   strings.TrimSpace(cfg.AppID),
		http:    &http.Client{Timeout: timeout, Transport: cfg.Transport},
	}, nil
}

// envelope 统一响应信封，注意字段名是 msg
type envelope struct {
	Code      int             `json:"code"`
	Msg       string          `json:"msg"`
	ErrorCode string          `json:"error_code"`
	Data      json.RawMessage `json:"data"`
}

// result 一次调用的原始结果
type result struct {
	httpStatus int
	replayed   bool
	data       json.RawMessage
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, idempotencyKey string) (*result, error) {
	write := method != http.MethodGet
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("zovocard: encode request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, fmt.Errorf("zovocard: build request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if c.appID != "" {
		req.Header.Set("X-App-Id", c.appID)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		req.Header.Set("Idempotency-Key", idempotencyKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// 写请求发出后没拿到响应：上游可能已经执行，调用方必须按“结果不明”处理
		return nil, &TransportError{Op: method + " " + path, Write: write, Err: err}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes+1))
	if err != nil {
		return nil, &TransportError{Op: method + " " + path, Write: write, Err: err}
	}
	if len(raw) > maxResponseBodyBytes {
		return nil, &TransportError{Op: method + " " + path, Write: write, Err: errors.New("response body too large")}
	}

	var env envelope
	if jsonErr := json.Unmarshal(raw, &env); jsonErr != nil {
		// 非 JSON 响应（网关错误页等）：只保留状态码，不回显正文
		return nil, &APIError{HTTPStatus: resp.StatusCode, Write: write, Message: "non-json response"}
	}

	// 有接口会 HTTP 200 但 body.code 非 0，两者任一表示失败都按失败处理
	if resp.StatusCode >= 300 || env.Code != 0 {
		status := resp.StatusCode
		if status < 300 && env.Code >= 400 {
			status = env.Code
		}
		return nil, &APIError{
			HTTPStatus: status,
			Write:      write,
			ErrorCode:  strings.TrimSpace(env.ErrorCode),
			Message:    truncate(env.Msg, 200),
		}
	}
	return &result{
		httpStatus: resp.StatusCode,
		replayed:   strings.EqualFold(resp.Header.Get("Idempotent-Replayed"), "true"),
		data:       env.Data,
	}, nil
}

func decodeData(r *result, out any) error {
	if len(r.data) == 0 || string(r.data) == "null" {
		return errors.New("zovocard: empty data")
	}
	if err := json.Unmarshal(r.data, out); err != nil {
		return fmt.Errorf("zovocard: decode data: %w", err)
	}
	return nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
