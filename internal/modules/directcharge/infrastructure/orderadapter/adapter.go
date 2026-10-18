// Package orderadapter 通过订单模块创建、取消和读取代充订单。
package orderadapter

import (
	"errors"
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	fulfillmentdomain "github.com/dujiao-next/internal/modules/fulfillment/domain"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	orderriskcontract "github.com/dujiao-next/internal/modules/orderrisk/contract"
)

// OrderService 订单模块中代充需要的用例
type OrderService interface {
	CreateOrder(input orderapp.CreateOrderInput) (*orderdomain.Order, error)
	CancelOrder(orderID uint, userID uint) (*orderdomain.Order, error)
}

// OrderReader 按 ID 读取订单
type OrderReader interface {
	GetByID(id uint) (*orderdomain.Order, error)
}

// Deliverer 交付模块中代充成功后的系统交付
type Deliverer interface {
	CreateDirectCharge(orderID uint, payload string) (*fulfillmentdomain.Fulfillment, error)
}

type Adapter struct {
	orders    OrderService
	reader    OrderReader
	deliverer Deliverer
	refunder  Refunder
	emails    StatusEmailer
	now       func() time.Time
}

var _ directchargecontract.Orders = (*Adapter)(nil)

func New(orders OrderService, reader OrderReader, deliverer Deliverer) *Adapter {
	return &Adapter{orders: orders, reader: reader, deliverer: deliverer, now: time.Now}
}

// Deliver 代充成功后交付子订单；已交付时交付模块返回成功
func (a *Adapter) Deliver(orderID uint, payload string) error {
	if a.deliverer == nil {
		return errors.New("orderadapter: deliverer is not configured")
	}
	_, err := a.deliverer.CreateDirectCharge(orderID, payload)
	return err
}

func (a *Adapter) CreateDirectChargeOrder(request directchargecontract.OrderRequest) (*directchargecontract.OrderRef, error) {
	order, err := a.orders.CreateOrder(orderapp.CreateOrderInput{
		UserID: request.UserID,
		Items: []orderapp.CreateOrderItem{{
			ProductID:       request.ProductID,
			SKUID:           request.SKUID,
			Quantity:        1,
			FulfillmentType: constants.FulfillmentTypeDirectCharge,
		}},
		ClientIP:     request.ClientIP,
		Tenant:       request.Tenant,
		DirectCharge: true,
	})
	if err != nil {
		return nil, mapCreateError(err)
	}
	if order == nil || len(order.Children) != 1 {
		return nil, directchargecontract.ErrOrderCreateFailed
	}
	child := order.Children[0]
	return &directchargecontract.OrderRef{
		ParentID:  order.ID,
		ParentNo:  order.OrderNo,
		ChildID:   child.ID,
		ChildNo:   child.OrderNo,
		ExpiresAt: order.ExpiresAt,
	}, nil
}

func (a *Adapter) CancelUnpaid(parentOrderID, userID uint) error {
	_, err := a.orders.CancelOrder(parentOrderID, userID)
	return err
}

// Status 返回订单状态；待支付但已过付款期限的订单视为已取消
func (a *Adapter) Status(orderID uint) (string, error) {
	order, err := a.reader.GetByID(orderID)
	if err != nil {
		return "", err
	}
	if order == nil {
		return "", nil
	}
	if order.Status == constants.OrderStatusPendingPayment && order.ExpiresAt != nil && !a.now().Before(*order.ExpiresAt) {
		return constants.OrderStatusCanceled, nil
	}
	return order.Status, nil
}

var rejectedRules = []struct {
	target  error
	key     string
	tooMany bool
}{
	{orderriskcontract.ErrIPBlacklisted, "error.risk_ip_blacklisted", false},
	{orderriskcontract.ErrClientIPUnavailable, "error.risk_client_ip_unavailable", false},
	{orderriskcontract.ErrTooManyPendingOrders, "error.risk_too_many_pending_orders", true},
	{orderriskcontract.ErrProductQuantityLimit, "error.risk_product_quantity_limit", false},
	{orderriskcontract.ErrPendingProductQuantityLimit, "error.risk_pending_product_quantity_limit", true},
	{orderriskcontract.ErrOrderRateLimited, "error.risk_order_rate_limited", true},
	{orderapp.ErrProductNotAvailable, "error.product_not_available", false},
	{orderapp.ErrProductSKUInvalid, "error.product_not_available", false},
	{orderapp.ErrProductPurchaseNotAllowed, "error.product_purchase_not_allowed", false},
	{orderapp.ErrDirectChargeOrderInvalid, "error.direct_charge_order_invalid", false},
	{orderapp.ErrQueueUnavailable, "error.queue_unavailable", false},
}

func mapCreateError(err error) error {
	for _, rule := range rejectedRules {
		if errors.Is(err, rule.target) {
			return &directchargecontract.OrderRejectedError{MessageKey: rule.key, TooMany: rule.tooMany, Err: err}
		}
	}
	return directchargecontract.ErrOrderCreateFailed
}
