package orderadapter

import (
	"errors"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	orderapp "github.com/dujiao-next/internal/modules/order/application"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	orderriskcontract "github.com/dujiao-next/internal/modules/orderrisk/contract"
)

type fakeOrderService struct {
	input orderapp.CreateOrderInput
	order *orderdomain.Order
	err   error
}

func (f *fakeOrderService) CreateOrder(input orderapp.CreateOrderInput) (*orderdomain.Order, error) {
	f.input = input
	return f.order, f.err
}

func (f *fakeOrderService) CancelOrder(uint, uint) (*orderdomain.Order, error) { return nil, nil }

type fakeReader map[uint]*orderdomain.Order

func (f fakeReader) GetByID(id uint) (*orderdomain.Order, error) { return f[id], nil }

func TestCreateDirectChargeOrderBuildsSingleItem(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	service := &fakeOrderService{order: &orderdomain.Order{
		ID: 1, OrderNo: "DJ1", ExpiresAt: &expires,
		Children: []orderdomain.Order{{ID: 2, OrderNo: "DJ1-01"}},
	}}
	ref, err := New(service, fakeReader{}).CreateDirectChargeOrder(directchargecontract.OrderRequest{UserID: 3, ProductID: 4, SKUID: 5, ClientIP: "1.2.3.4"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if ref.ParentNo != "DJ1" || ref.ChildID != 2 || ref.ChildNo != "DJ1-01" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
	in := service.input
	if !in.DirectCharge || len(in.Items) != 1 || in.Items[0].Quantity != 1 || in.Items[0].FulfillmentType != constants.FulfillmentTypeDirectCharge || in.CouponCode != "" {
		t.Fatalf("unexpected order input: %+v", in)
	}
}

func TestCreateErrorsAreMapped(t *testing.T) {
	service := &fakeOrderService{err: orderriskcontract.ErrTooManyPendingOrders}
	_, err := New(service, fakeReader{}).CreateDirectChargeOrder(directchargecontract.OrderRequest{UserID: 1})
	var rejected *directchargecontract.OrderRejectedError
	if !errors.As(err, &rejected) || !rejected.TooMany || rejected.MessageKey != "error.risk_too_many_pending_orders" {
		t.Fatalf("unexpected error: %v", err)
	}
	service.err = errors.New("db down")
	if _, err := New(service, fakeReader{}).CreateDirectChargeOrder(directchargecontract.OrderRequest{UserID: 1}); !errors.Is(err, directchargecontract.ErrOrderCreateFailed) {
		t.Fatalf("unknown error should map to create failed: %v", err)
	}
}

func TestStatusTreatsExpiredUnpaidAsCanceled(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	reader := fakeReader{
		1: {Status: constants.OrderStatusPendingPayment, ExpiresAt: &past},
		2: {Status: constants.OrderStatusPaid, ExpiresAt: &past},
	}
	adapter := New(&fakeOrderService{}, reader)
	for id, want := range map[uint]string{1: constants.OrderStatusCanceled, 2: constants.OrderStatusPaid, 3: ""} {
		if got, _ := adapter.Status(id); got != want {
			t.Errorf("order %d: want %q got %q", id, want, got)
		}
	}
}
