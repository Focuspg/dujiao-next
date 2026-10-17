package directchargehttp

import (
	"context"
	"strings"
	"time"

	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
	"github.com/dujiao-next/internal/platform/http/ginutil"
	"github.com/dujiao-next/internal/platform/http/response"

	"github.com/gin-gonic/gin"
)

// reconcileTimeout 后台查原单的最长等待（可能翻多页订单列表）
const reconcileTimeout = 90 * time.Second

// AdminService 后台代充管理用例
type AdminService interface {
	ListSpecs() ([]directchargedomain.Spec, error)
	SaveSpec(id uint, input directchargeapp.SpecInput) (*directchargedomain.Spec, error)
	ListTasks(filter directchargecontract.TaskFilter) ([]directchargedomain.Task, int64, error)
	TaskDetail(id uint) (*directchargeapp.TaskDetail, error)
	Reconcile(ctx context.Context, id, adminID uint) (*directchargeapp.ReconcileResult, error)
	Retry(id uint, reason string, adminID uint) (*directchargeapp.TaskDetail, error)
	Resolve(id uint, outcome, reason string, adminID uint) (*directchargeapp.TaskDetail, error)
	Refund(id uint, remark string, adminID uint) (*directchargeapp.TaskDetail, error)
}

type AdminHandler struct {
	service AdminService
}

func NewAdminHandler(service AdminService) *AdminHandler {
	if service == nil {
		panic("directcharge admin handler: service is nil")
	}
	return &AdminHandler{service: service}
}

type reasonRequest struct {
	Reason string `json:"reason"`
}

type resolveRequest struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
}

type refundRequest struct {
	Remark string `json:"remark"`
}

func (h *AdminHandler) ListSpecs(c *gin.Context) {
	specs, err := h.service.ListSpecs()
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, specs)
}

func (h *AdminHandler) CreateSpec(c *gin.Context) {
	h.saveSpec(c, 0)
}

func (h *AdminHandler) UpdateSpec(c *gin.Context) {
	id, err := ginutil.ParseParamUint(c, "id")
	if err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	h.saveSpec(c, id)
}

func (h *AdminHandler) saveSpec(c *gin.Context, id uint) {
	var input directchargeapp.SpecInput
	if err := c.ShouldBindJSON(&input); err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	spec, err := h.service.SaveSpec(id, input)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, spec)
}

func (h *AdminHandler) ListTasks(c *gin.Context) {
	page, pageSize := ginutil.ParsePagination(c)
	tasks, total, err := h.service.ListTasks(directchargecontract.TaskFilter{
		Status:       strings.TrimSpace(c.Query("status")),
		OrderNo:      strings.TrimSpace(c.Query("order_no")),
		RefundStatus: strings.TrimSpace(c.Query("refund_status")),
		Page:         page,
		PageSize:     pageSize,
	})
	if err != nil {
		respondError(c, err)
		return
	}
	response.SuccessWithPage(c, tasks, response.BuildPagination(page, pageSize, total))
}

func (h *AdminHandler) GetTask(c *gin.Context) {
	id, ok := parseTaskID(c)
	if !ok {
		return
	}
	detail, err := h.service.TaskDetail(id)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, detail)
}

func (h *AdminHandler) ReconcileTask(c *gin.Context) {
	id, adminID, ok := parseTaskAction(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), reconcileTimeout)
	defer cancel()
	result, err := h.service.Reconcile(ctx, id, adminID)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, result)
}

func (h *AdminHandler) RetryTask(c *gin.Context) {
	id, adminID, ok := parseTaskAction(c)
	if !ok {
		return
	}
	var req reasonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	detail, err := h.service.Retry(id, req.Reason, adminID)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, detail)
}

func (h *AdminHandler) ResolveTask(c *gin.Context) {
	id, adminID, ok := parseTaskAction(c)
	if !ok {
		return
	}
	var req resolveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	detail, err := h.service.Resolve(id, strings.TrimSpace(req.Outcome), req.Reason, adminID)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, detail)
}

func (h *AdminHandler) RefundTask(c *gin.Context) {
	id, adminID, ok := parseTaskAction(c)
	if !ok {
		return
	}
	var req refundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return
	}
	detail, err := h.service.Refund(id, req.Remark, adminID)
	if err != nil {
		respondError(c, err)
		return
	}
	response.Success(c, detail)
}

func parseTaskID(c *gin.Context) (uint, bool) {
	id, err := ginutil.ParseParamUint(c, "id")
	if err != nil {
		ginutil.RespondError(c, response.CodeBadRequest, "error.bad_request", nil)
		return 0, false
	}
	return id, true
}

func parseTaskAction(c *gin.Context) (uint, uint, bool) {
	id, ok := parseTaskID(c)
	if !ok {
		return 0, 0, false
	}
	adminID, ok := ginutil.GetAdminID(c)
	if !ok {
		return 0, 0, false
	}
	return id, adminID, true
}
