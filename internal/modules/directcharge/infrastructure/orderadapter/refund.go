package orderadapter

import (
	"errors"
	"strings"

	"github.com/dujiao-next/internal/logger"
	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/queue"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

// Refunder 订单模块的线下退款登记
type Refunder interface {
	AdminManualRefund(input orderrefund.AdminManualRefundInput) (*orderdomain.Order, *orderdomain.OrderRefundRecord, error)
}

// StatusEmailer 投递订单状态邮件
type StatusEmailer func(payload queue.OrderStatusEmailPayload) error

// WithRefunds 配置退款登记与退款通知邮件
func (a *Adapter) WithRefunds(refunder Refunder, emails StatusEmailer) *Adapter {
	a.refunder = refunder
	a.emails = emails
	return a
}

// Refund 登记子订单剩余可退金额的线下退款，并按父订单发送退款邮件。已退完时直接返回。
func (a *Adapter) Refund(orderID uint, remark string) error {
	if a.refunder == nil {
		return errors.New("orderadapter: refunder is not configured")
	}
	order, err := a.reader.GetByID(orderID)
	if err != nil {
		return err
	}
	if order == nil {
		return errors.New("orderadapter: order not found")
	}
	refundable := order.TotalAmount.Decimal.Sub(order.RefundedAmount.Decimal).Round(2)
	if refundable.LessThanOrEqual(decimal.Zero) {
		return nil
	}
	refunded, record, err := a.refunder.AdminManualRefund(orderrefund.AdminManualRefundInput{
		OrderID: orderID,
		Amount:  money.FromDecimal(refundable),
		Remark:  strings.TrimSpace(remark),
	})
	if err != nil {
		return err
	}
	a.enqueueRefundEmail(refunded, record)
	return nil
}

func (a *Adapter) enqueueRefundEmail(order *orderdomain.Order, record *orderdomain.OrderRefundRecord) {
	if a.emails == nil || order == nil {
		return
	}
	target := order
	if order.ParentID != nil && *order.ParentID > 0 {
		if parent, err := a.reader.GetByID(*order.ParentID); err == nil && parent != nil {
			target = parent
		}
	}
	payload := queue.OrderStatusEmailPayload{OrderID: target.ID, Status: target.Status}
	if record != nil {
		payload.RefundRecordID = record.ID
	}
	if err := a.emails(payload); err != nil {
		logger.Warnw("directcharge_refund_email_enqueue_failed", "order_id", target.ID, "error", err)
	}
}
