package application

import (
	"strings"
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// 对客展示状态（需求 5.4）
const (
	DisplayPendingPayment     = "pending_payment"
	DisplayQueued             = "queued"
	DisplayProcessing         = "processing"
	DisplayCredentialRequired = "credential_required"
	DisplayActionRequired     = "action_required"
	DisplayVerifying          = "verifying"
	DisplaySucceeded          = "succeeded"
	DisplayFailed             = "failed"
	DisplayClosed             = "closed"
)

// TaskView 用户可见的代充进度；不含凭据、指纹和上游内部信息
type TaskView struct {
	OrderNo          string         `json:"order_no"`
	Status           string         `json:"status"`
	Product          string         `json:"product"`
	Plan             string         `json:"plan"`
	AccountMasked    string         `json:"account_masked"`
	QuoteCurrency    string         `json:"quote_currency"`
	QuoteAmountMinor int64          `json:"quote_amount_minor"`
	QuoteExponent    int            `json:"quote_exponent"`
	ActionMessage    string         `json:"action_message,omitempty"`
	CreatedAt        time.Time      `json:"created_at"`
	PaidAt           *time.Time     `json:"paid_at,omitempty"`
	CompletedAt      *time.Time     `json:"completed_at,omitempty"`
	Timeline         []TimelineItem `json:"timeline"`
}

// TimelineItem 进度时间线
type TimelineItem struct {
	Status string    `json:"status"`
	At     time.Time `json:"at"`
}

// GetOrder 查询用户自己的代充订单进度（按用户支付的父订单号）
func (s *Service) GetOrder(userID uint, orderNo string) (*TaskView, error) {
	if s == nil || s.store == nil {
		return nil, directchargecontract.ErrNotFound
	}
	orderNo = strings.TrimSpace(orderNo)
	if userID == 0 || orderNo == "" {
		return nil, directchargecontract.ErrNotFound
	}
	task, err := s.store.TaskByParentOrderNo(userID, orderNo)
	if err != nil {
		return nil, err
	}
	if task == nil {
		return nil, directchargecontract.ErrNotFound
	}
	orderStatus := ""
	if task.Status == directchargedomain.TaskAwaitingPayment && s.orders != nil {
		if orderStatus, err = s.orders.Status(task.OrderID); err != nil {
			return nil, err
		}
		if orderStatus == "" || orderStatus == constants.OrderStatusCanceled {
			if _, err := s.closeIfOrderCanceled(task); err != nil {
				return nil, err
			}
			if task, err = s.store.TaskByParentOrderNo(userID, orderNo); err != nil || task == nil {
				return nil, directchargecontract.ErrNotFound
			}
		}
	}
	logs, err := s.store.TaskLogs(task.ID)
	if err != nil {
		return nil, err
	}
	view := &TaskView{
		OrderNo:          task.ParentOrderNo,
		Status:           DisplayStatus(task.Status, orderStatus),
		Product:          task.Product,
		Plan:             task.Plan,
		AccountMasked:    task.AccountMasked,
		QuoteCurrency:    task.QuoteCurrency,
		QuoteAmountMinor: task.QuoteAmountMinor,
		QuoteExponent:    task.QuoteExponent,
		ActionMessage:    task.ActionMessage,
		CreatedAt:        task.CreatedAt,
		PaidAt:           task.PaidAt,
		CompletedAt:      task.CompletedAt,
		Timeline:         buildTimeline(logs),
	}
	return view, nil
}

// DisplayStatus 把内部任务状态映射为对客状态。orderStatus 仅在任务尚未感知付款时用于显示“排队充值”。
func DisplayStatus(taskStatus, orderStatus string) string {
	switch taskStatus {
	case directchargedomain.TaskAwaitingPayment:
		switch orderStatus {
		case constants.OrderStatusPaid, constants.OrderStatusFulfilling:
			return DisplayQueued
		}
		return DisplayPendingPayment
	case directchargedomain.TaskReady, directchargedomain.TaskPreflighting:
		return DisplayQueued
	case directchargedomain.TaskSubmitting, directchargedomain.TaskSubmitUnknown, directchargedomain.TaskProcessing:
		return DisplayProcessing
	case directchargedomain.TaskWaitingCredential:
		return DisplayCredentialRequired
	case directchargedomain.TaskRequiresAction:
		return DisplayActionRequired
	case directchargedomain.TaskReview:
		return DisplayVerifying
	case directchargedomain.TaskSucceeded:
		return DisplaySucceeded
	case directchargedomain.TaskFailedConfirmed, directchargedomain.TaskCancelledConfirmed:
		return DisplayFailed
	case directchargedomain.TaskClosed:
		return DisplayClosed
	}
	return DisplayVerifying
}

// buildTimeline 合并相邻的相同对客状态，只暴露状态与时间
func buildTimeline(logs []directchargedomain.TaskLog) []TimelineItem {
	items := make([]TimelineItem, 0, len(logs))
	for _, entry := range logs {
		status := DisplayStatus(entry.ToStatus, "")
		if n := len(items); n > 0 && items[n-1].Status == status {
			continue
		}
		items = append(items, TimelineItem{Status: status, At: entry.CreatedAt})
	}
	return items
}
