package notificationadapter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/zovocard"
)

type balanceClientStub struct {
	balance *zovocard.Balance
	cards   map[int64]*zovocard.Card
	err     error
}

func (s balanceClientStub) GetBalance(context.Context) (*zovocard.Balance, error) {
	return s.balance, s.err
}

func (s balanceClientStub) GetCard(_ context.Context, id int64) (*zovocard.Card, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.cards[id], nil
}

type specListerStub []directchargedomain.Spec

func (s specListerStub) ListSpecs() ([]directchargedomain.Spec, error) { return s, nil }

func TestNewBalanceReaderReturnsNilWithoutClient(t *testing.T) {
	if reader := NewBalanceReader(nil, specListerStub{}); reader != nil {
		t.Fatalf("unconfigured ZovoCard must not wire a balance reader, got %#v", reader)
	}
}

func TestBalanceReaderAccountBalance(t *testing.T) {
	reader := newBalanceReader(balanceClientStub{balance: &zovocard.Balance{
		Balance: json.Number("52.30"), SpendableBalance: json.Number("50.15"), Currency: "usd",
	}}, nil)
	balance, err := reader.AccountBalance(context.Background())
	if err != nil {
		t.Fatalf("account balance: %v", err)
	}
	if balance.Spendable.String() != "50.15" || balance.Balance.String() != "52.3" || balance.Currency != "USD" {
		t.Fatalf("unexpected balance %+v", balance)
	}

	reader = newBalanceReader(balanceClientStub{balance: &zovocard.Balance{SpendableBalance: json.Number("1"), Balance: json.Number("1"), Currency: "EUR"}}, nil)
	if _, err := reader.AccountBalance(context.Background()); err == nil {
		t.Fatal("non-USD balance must not be compared with a USD threshold")
	}
	reader = newBalanceReader(balanceClientStub{balance: &zovocard.Balance{Currency: "USD"}}, nil)
	if _, err := reader.AccountBalance(context.Background()); err == nil {
		t.Fatal("missing spendable balance must be a query failure, not zero")
	}
	upstreamErr := errors.New("zovocard: http 503")
	reader = newBalanceReader(balanceClientStub{err: upstreamErr}, nil)
	if _, err := reader.AccountBalance(context.Background()); !errors.Is(err, upstreamErr) {
		t.Fatalf("upstream error must propagate, got %v", err)
	}
}

func TestBalanceReaderMonitoredCardsAndCardBalance(t *testing.T) {
	specs := specListerStub{
		{ID: 1, CardID: 525944, Enabled: true, Supplier: directchargedomain.SupplierZovoCard},
		{ID: 2, CardID: 525944, Enabled: true, Supplier: directchargedomain.SupplierZovoCard},
		{ID: 3, CardID: 100, Enabled: true},
		{ID: 4, CardID: 200, Enabled: false, Supplier: directchargedomain.SupplierZovoCard},
		{ID: 5, CardID: 0, Enabled: true, Supplier: directchargedomain.SupplierZovoCard},
	}
	reader := newBalanceReader(balanceClientStub{cards: map[int64]*zovocard.Card{
		525944: {ID: 525944, AvailableAmount: json.Number("12.34"), BalanceStale: true, BalanceAsOf: json.RawMessage(`"2026-10-21T08:00:00Z"`)},
	}}, specs)

	ids, err := reader.MonitoredCardIDs(context.Background())
	if err != nil {
		t.Fatalf("monitored cards: %v", err)
	}
	if !reflect.DeepEqual(ids, []int64{100, 525944}) {
		t.Fatalf("monitored cards = %v, want enabled specs' cards deduplicated", ids)
	}

	card, err := reader.CardBalance(context.Background(), 525944)
	if err != nil {
		t.Fatalf("card balance: %v", err)
	}
	if card.CardID != 525944 || card.Available.String() != "12.34" || !card.Stale || card.AsOf != "2026-10-21T08:00:00Z" {
		t.Fatalf("unexpected card balance %+v", card)
	}
	if _, err := reader.CardBalance(context.Background(), 404); err == nil {
		t.Fatal("missing card must be a query failure")
	}
}
