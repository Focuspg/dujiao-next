// Package directchargehttp 提供账号代充的前台接口。
package directchargehttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

// maxPreflightBody 预检请求体上限：凭据 64 KiB 加少量 JSON 字段
const maxPreflightBody = directchargeapp.DefaultMaxCredentialBytes + 4<<10

// preflightTimeout 单次上游预检的最长等待
const preflightTimeout = 45 * time.Second

// UserService 前台代充用例
type UserService interface {
	Preflight(ctx context.Context, input directchargeapp.PreflightInput) (*directchargeapp.PreflightView, error)
	CreateOrder(input directchargeapp.CreateOrderInput) (*directchargeapp.CreateOrderResult, error)
	GetOrder(userID uint, orderNo string) (*directchargeapp.TaskView, error)
	ResupplyCredential(ctx context.Context, input directchargeapp.ResupplyCredentialInput) (*directchargeapp.TaskView, error)
	RequestRefund(userID uint, orderNo string) (*directchargeapp.TaskView, error)
}

type UserHandler struct {
	service UserService
}

func NewUserHandler(service UserService) *UserHandler {
	if service == nil {
		panic("directcharge user handler: service is nil")
	}
	return &UserHandler{service: service}
}

type preflightRequest struct {
	SKUID      uint   `json:"sku_id"`
	Mode       string `json:"mode"`
	Credential string `json:"credential"`
}

type createOrderRequest struct {
	PreflightRef string `json:"preflight_ref"`
}

// Preflight 验证目标账号凭据并返回脱敏结果。请求体含敏感凭据：限制大小、禁止缓存，不记录请求体与绑定错误详情。
func (h *UserHandler) Preflight(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPreflightBody)
	var req preflightRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			ginutil.RespondError(c, response.CodeBadRequest, "error.direct_charge_credential_unsupported", nil)
			return
		}
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	if req.SKUID == 0 {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), preflightTimeout)
	defer cancel()
	view, err := h.service.Preflight(ctx, directchargeapp.PreflightInput{
		UserID:     uid,
		SKUID:      req.SKUID,
		Mode:       req.Mode,
		Credential: req.Credential,
	})
	req.Credential = ""
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, view)
}

// CreateOrder 用户确认目标账号后创建待支付订单；同一预检引用重复提交返回同一订单
func (h *UserHandler) CreateOrder(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	var req createOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.PreflightRef) == "" {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	result, err := h.service.CreateOrder(directchargeapp.CreateOrderInput{
		UserID:       uid,
		PreflightRef: strings.TrimSpace(req.PreflightRef),
		ClientIP:     c.ClientIP(),
		Tenant:       resellercontract.TenantOrMain(c.Request.Context()),
	})
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, result)
}

// GetOrder 查询自己的代充订单进度（只读本地状态，不触发上游调用）
func (h *UserHandler) GetOrder(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	view, err := h.service.GetOrder(uid, c.Param("order_no"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, view)
}

type credentialRequest struct {
	Mode       string `json:"mode"`
	Credential string `json:"credential"`
}

// ResupplyCredential 付款后凭据失效时补充同一账号的新凭据。请求体含敏感凭据：限制大小、禁止缓存，不记录请求体。
func (h *UserHandler) ResupplyCredential(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPreflightBody)
	var req credentialRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			ginutil.RespondError(c, response.CodeBadRequest, "error.direct_charge_credential_unsupported", nil)
			return
		}
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), preflightTimeout)
	defer cancel()
	view, err := h.service.ResupplyCredential(ctx, directchargeapp.ResupplyCredentialInput{
		UserID:     uid,
		OrderNo:    c.Param("order_no"),
		Mode:       req.Mode,
		Credential: req.Credential,
	})
	req.Credential = ""
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, view)
}

// RequestRefund 用户申请售后退款（只登记申请，由后台审批）
func (h *UserHandler) RequestRefund(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	uid, ok := ginutil.GetUserID(c)
	if !ok {
		return
	}
	view, err := h.service.RequestRefund(uid, c.Param("order_no"))
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, view)
}

var errorRules = []struct {
	target error
	code   int
	key    string
}{
	{directchargecontract.ErrDisabled, response.CodeBadRequest, "error.direct_charge_disabled"},
	{directchargecontract.ErrProductUnavailable, response.CodeBadRequest, "error.direct_charge_product_unavailable"},
	{directchargecontract.ErrCredentialUnsupported, response.CodeBadRequest, "error.direct_charge_credential_unsupported"},
	{directchargecontract.ErrCredentialInvalid, response.CodeBadRequest, "error.direct_charge_credential_invalid"},
	{directchargecontract.ErrNotEligible, response.CodeBadRequest, "error.direct_charge_not_eligible"},
	{directchargecontract.ErrUpstreamBusy, response.CodeTooManyRequests, "error.direct_charge_upstream_busy"},
	{directchargecontract.ErrQuoteExceeded, response.CodeBadRequest, "error.direct_charge_quote_exceeded"},
	{directchargecontract.ErrPreflightExpired, response.CodeBadRequest, "error.direct_charge_preflight_expired"},
	{directchargecontract.ErrAccountBusy, response.CodeBadRequest, "error.direct_charge_account_busy"},
	{directchargecontract.ErrNotFound, response.CodeNotFound, "error.direct_charge_not_found"},
	{directchargecontract.ErrCredentialNotRequired, response.CodeBadRequest, "error.direct_charge_credential_not_required"},
	{directchargecontract.ErrAccountMismatch, response.CodeBadRequest, "error.direct_charge_account_mismatch"},
	{directchargecontract.ErrRefundNotAllowed, response.CodeBadRequest, "error.direct_charge_refund_not_allowed"},
	{directchargecontract.ErrRefundFailed, response.CodeBadRequest, "error.direct_charge_refund_failed"},
	{directchargecontract.ErrActionNotAllowed, response.CodeBadRequest, "error.direct_charge_action_not_allowed"},
	{directchargecontract.ErrAttemptOpen, response.CodeBadRequest, "error.direct_charge_attempt_open"},
	{directchargecontract.ErrReasonRequired, response.CodeBadRequest, "error.direct_charge_reason_required"},
	{directchargecontract.ErrInvalidSpec, response.CodeBadRequest, "error.direct_charge_invalid_spec"},
	{directchargecontract.ErrSpecDuplicate, response.CodeBadRequest, "error.direct_charge_spec_duplicate"},
}

func respondError(c *gin.Context, err error) {
	var rejected *directchargecontract.OrderRejectedError
	if errors.As(err, &rejected) {
		code := response.CodeBadRequest
		if rejected.TooMany {
			code = response.CodeTooManyRequests
		}
		ginutil.RespondError(c, code, rejected.MessageKey, nil)
		return
	}
	for _, rule := range errorRules {
		if errors.Is(err, rule.target) {
			ginutil.RespondError(c, rule.code, rule.key, nil)
			return
		}
	}
	if errors.Is(err, directchargecontract.ErrOrderCreateFailed) {
		ginutil.RespondError(c, response.CodeInternal, "error.order_create_failed", nil)
		return
	}
	ginutil.RespondError(c, response.CodeInternal, "error.internal_error", err)
}
