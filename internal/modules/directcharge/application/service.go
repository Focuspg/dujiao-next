// Package application 实现账号代充的前台用例：账号预检、创建订单与订单进度查询。
package application

import (
	"strings"
	"time"

	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"
)

// DefaultMaxCredentialBytes 凭据内容上限（需求 REQ-UI-02 默认 64 KiB）
const DefaultMaxCredentialBytes = 64 << 10

type Options struct {
	Enabled            bool
	Store              directchargecontract.Store
	Upstream           directchargecontract.Upstream
	Vault              directchargecontract.Vault
	Orders             directchargecontract.Orders
	PreflightTTL       time.Duration
	CredentialTTL      time.Duration
	MaxCredentialBytes int
	Now                func() time.Time
}

type Service struct {
	enabled            bool
	store              directchargecontract.Store
	upstream           directchargecontract.Upstream
	vault              directchargecontract.Vault
	orders             directchargecontract.Orders
	preflightTTL       time.Duration
	credentialTTL      time.Duration
	maxCredentialBytes int
	now                func() time.Time
}

func NewService(options Options) *Service {
	s := &Service{
		enabled: options.Enabled, store: options.Store, upstream: options.Upstream,
		vault: options.Vault, orders: options.Orders,
		preflightTTL: options.PreflightTTL, credentialTTL: options.CredentialTTL,
		maxCredentialBytes: options.MaxCredentialBytes, now: options.Now,
	}
	if s.preflightTTL <= 0 {
		s.preflightTTL = 10 * time.Minute
	}
	if s.credentialTTL <= 0 {
		s.credentialTTL = 30 * time.Minute
	}
	if s.maxCredentialBytes <= 0 {
		s.maxCredentialBytes = DefaultMaxCredentialBytes
	}
	if s.now == nil {
		s.now = time.Now
	}
	return s
}

// Available 代充是否已启用且依赖齐全
func (s *Service) Available() bool {
	return s != nil && s.enabled && s.store != nil && s.upstream != nil && s.vault != nil && s.orders != nil
}

func (s *Service) loadSellableSpec(skuID uint) (*directchargedomain.Spec, error) {
	spec, err := s.store.SpecBySKU(skuID)
	if err != nil {
		return nil, err
	}
	if spec == nil || !spec.Enabled || spec.Supplier != directchargedomain.SupplierZovoCard {
		return nil, directchargecontract.ErrProductUnavailable
	}
	return spec, nil
}

func normalizeMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return directchargedomain.CredentialModeSession
	}
	return mode
}
