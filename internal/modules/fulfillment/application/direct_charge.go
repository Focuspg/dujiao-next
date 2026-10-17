package application

import (
	"errors"
	"strings"
	"time"

	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	ordercontract "github.com/dujiao-next/internal/modules/order/contract"

	"github.com/dujiao-next/internal/constants"
)

// CreateDirectCharge 账号代充确认成功后的系统交付（没有操作管理员）。
// 订单已有交付记录时视为成功，供后台任务重复调用。
func (s *Service) CreateDirectCharge(orderID uint, payload string) (*fulfillmentdomain.Fulfillment, error) {
	payload = strings.TrimSpace(payload)
	if orderID == 0 || payload == "" {
		return nil, ErrFulfillmentInvalid
	}
	order, err := s.orderStore.GetByID(orderID)
	if err != nil {
		return nil, ErrOrderFetchFailed
	}
	if order == nil {
		return nil, ErrOrderNotFound
	}
	if order.ParentID == nil && len(order.Children) > 0 {
		return nil, ErrFulfillmentInvalid
	}
	for _, item := range order.Items {
		if strings.TrimSpace(item.FulfillmentType) != constants.FulfillmentTypeDirectCharge {
			return nil, ErrFulfillmentInvalid
		}
	}
	if order.Status == constants.OrderStatusDelivered || order.Status == constants.OrderStatusCompleted {
		return nil, nil
	}
	if order.Status != constants.OrderStatusPaid && order.Status != constants.OrderStatusFulfilling {
		return nil, ErrOrderStatusInvalid
	}

	now := time.Now()
	var created *fulfillmentdomain.Fulfillment
	err = s.orderStore.WithinTransaction(func(tx ordercontract.Transaction) error {
		if _, found, err := tx.Fulfillments().FindByOrderIDForUpdate(orderID); err != nil {
			return err
		} else if found {
			return ErrFulfillmentExists
		}
		fulfillment := &fulfillmentdomain.Fulfillment{
			OrderID:     orderID,
			Type:        constants.FulfillmentTypeDirectCharge,
			Status:      constants.FulfillmentStatusDelivered,
			Payload:     payload,
			DeliveredAt: &now,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := tx.Fulfillments().Create(fulfillment); err != nil {
			return ErrFulfillmentCreateFailed
		}
		if err := tx.Orders().UpdateFields(order.ID, map[string]interface{}{
			"status":     constants.OrderStatusDelivered,
			"updated_at": now,
		}); err != nil {
			return ErrOrderUpdateFailed
		}
		created = fulfillment
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrFulfillmentExists) {
			return nil, nil
		}
		if errors.Is(err, ErrOrderUpdateFailed) {
			return nil, ErrOrderUpdateFailed
		}
		return nil, ErrFulfillmentCreateFailed
	}
	s.afterDelivered(order, orderID, now)
	return created, nil
}
