package notificationadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
	"github.com/dujiao-next/internal/modules/directcharge/infrastructure/zovocard"
	notificationcontract "github.com/dujiao-next/internal/modules/notification/contract"
	"github.com/shopspring/decimal"
)

// BalanceClient 是 ZovoCard 客户端中余额巡检用到的部分
type BalanceClient interface {
	GetBalance(ctx context.Context) (*zovocard.Balance, error)
	GetCard(ctx context.Context, id int64) (*zovocard.Card, error)
}

// SpecLister 列出代充规格，用于找出需要巡检的付款卡
type SpecLister interface {
	ListSpecs() ([]directchargedomain.Spec, error)
}

// BalanceReader 把 ZovoCard 余额接口适配为通知中心的上游余额端口
type BalanceReader struct {
	client BalanceClient
	specs  SpecLister
}

var _ notificationcontract.UpstreamBalanceReader = (*BalanceReader)(nil)

// NewBalanceReader ZovoCard 未配置（client 为 nil）时返回 nil，通知中心跳过余额巡检
func NewBalanceReader(client *zovocard.Client, specs SpecLister) notificationcontract.UpstreamBalanceReader {
	if client == nil {
		return nil
	}
	return newBalanceReader(client, specs)
}

func newBalanceReader(client BalanceClient, specs SpecLister) *BalanceReader {
	return &BalanceReader{client: client, specs: specs}
}

// AccountBalance 账户余额；ZovoCard 余额以美元计，币种不是 USD 时按查询失败处理，避免与美元阈值错误比较
func (r *BalanceReader) AccountBalance(ctx context.Context) (notificationcontract.UpstreamAccountBalance, error) {
	balance, err := r.client.GetBalance(ctx)
	if err != nil {
		return notificationcontract.UpstreamAccountBalance{}, err
	}
	if balance == nil {
		return notificationcontract.UpstreamAccountBalance{}, fmt.Errorf("zovocard: empty balance")
	}
	currency := strings.ToUpper(strings.TrimSpace(balance.Currency))
	if currency != "" && currency != "USD" {
		return notificationcontract.UpstreamAccountBalance{}, fmt.Errorf("zovocard: unexpected balance currency %q", balance.Currency)
	}
	spendable, err := parseAmount("spendable_balance", balance.SpendableBalance)
	if err != nil {
		return notificationcontract.UpstreamAccountBalance{}, err
	}
	total, err := parseAmount("balance", balance.Balance)
	if err != nil {
		return notificationcontract.UpstreamAccountBalance{}, err
	}
	if currency == "" {
		currency = "USD"
	}
	return notificationcontract.UpstreamAccountBalance{Spendable: spendable, Balance: total, Currency: currency}, nil
}

// MonitoredCardIDs 已启用的 ZovoCard 代充规格引用的付款卡，去重升序
func (r *BalanceReader) MonitoredCardIDs(_ context.Context) ([]int64, error) {
	if r.specs == nil {
		return nil, nil
	}
	specs, err := r.specs.ListSpecs()
	if err != nil {
		return nil, err
	}
	seen := make(map[int64]struct{}, len(specs))
	ids := make([]int64, 0, len(specs))
	for _, spec := range specs {
		if !spec.Enabled || spec.CardID <= 0 {
			continue
		}
		if supplier := strings.TrimSpace(spec.Supplier); supplier != "" && supplier != directchargedomain.SupplierZovoCard {
			continue
		}
		if _, ok := seen[spec.CardID]; ok {
			continue
		}
		seen[spec.CardID] = struct{}{}
		ids = append(ids, spec.CardID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

// CardBalance 付款卡可用额度（美元）
func (r *BalanceReader) CardBalance(ctx context.Context, cardID int64) (notificationcontract.UpstreamCardBalance, error) {
	card, err := r.client.GetCard(ctx, cardID)
	if err != nil {
		return notificationcontract.UpstreamCardBalance{CardID: cardID}, err
	}
	if card == nil {
		return notificationcontract.UpstreamCardBalance{CardID: cardID}, fmt.Errorf("zovocard: empty card")
	}
	available, err := parseAmount("available_amount", card.AvailableAmount)
	if err != nil {
		return notificationcontract.UpstreamCardBalance{CardID: cardID}, err
	}
	return notificationcontract.UpstreamCardBalance{
		CardID:    cardID,
		Available: available,
		Stale:     card.BalanceStale,
		AsOf:      card.BalanceAsOfText(),
	}, nil
}

func parseAmount(field string, value json.Number) (decimal.Decimal, error) {
	amount, err := decimal.NewFromString(strings.TrimSpace(value.String()))
	if err != nil {
		return decimal.Zero, fmt.Errorf("zovocard: invalid %s", field)
	}
	return amount, nil
}
