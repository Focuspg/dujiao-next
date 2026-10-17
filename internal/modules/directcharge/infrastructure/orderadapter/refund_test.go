package orderadapter

import (
	"testing"

	orderrefund "github.com/dujiao-next/internal/modules/order/application/refund"
	orderdomain "github.com/dujiao-next/internal/modules/order/domain"
	"github.com/dujiao-next/internal/queue"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/shopspring/decimal"
)

type fakeRefunder struct {
	inputs []orderrefund.AdminManualRefundInput
	reader fakeReader
}

func (f *fakeRefunder) AdminManualRefund(input orderrefund.AdminManualRefundInput) (*orderdomain.Order, *orderdomain.OrderRefundRecord, error) {
	f.inputs = append(f.inputs, input)
	order := f.reader[input.OrderID]
	order.RefundedAmount = order.TotalAmount
	order.Status = "refunded"
	f.reader[*order.ParentID].Status = "refunded"
	return order, &orderdomain.OrderRefundRecord{ID: 55}, nil
}

func TestRefundRecordsRemainingAmountOnceAndEmailsParent(t *testing.T) {
	parentID := uint(1)
	reader := fakeReader{
		1: {ID: 1, OrderNo: "DJ1", Status: "paid"},
		2: {ID: 2, OrderNo: "DJ1-01", ParentID: &parentID, TotalAmount: money.FromDecimal(decimal.RequireFromString("29.90")), RefundedAmount: money.FromDecimal(decimal.RequireFromString("0.90"))},
	}
	refunder := &fakeRefunder{reader: reader}
	var emails []queue.OrderStatusEmailPayload
	adapter := New(nil, reader, nil).WithRefunds(refunder, func(payload queue.OrderStatusEmailPayload) error {
		emails = append(emails, payload)
		return nil
	})
	if err := adapter.Refund(2, "voucher 1"); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if len(refunder.inputs) != 1 || refunder.inputs[0].Amount.Decimal.String() != "29" || refunder.inputs[0].OrderID != 2 {
		t.Fatalf("expected remaining amount 29 on the child order, got %+v", refunder.inputs)
	}
	if len(emails) != 1 || emails[0].OrderID != 1 || emails[0].Status != "refunded" || emails[0].RefundRecordID != 55 {
		t.Fatalf("refund email should target the parent order: %+v", emails)
	}
	if err := adapter.Refund(2, "voucher 1"); err != nil || len(refunder.inputs) != 1 {
		t.Fatalf("fully refunded order must not be refunded twice")
	}
}
