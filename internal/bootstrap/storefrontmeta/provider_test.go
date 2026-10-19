package storefrontmeta

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	resellerdomain "github.com/dujiao-next/internal/modules/reseller/domain"
	"github.com/dujiao-next/internal/shared/jsonmap"
)

type stubSettings struct {
	cfg   map[string]interface{}
	err   error
	calls int
}

func (s *stubSettings) GetConfig(map[string]interface{}) (map[string]interface{}, error) {
	s.calls++
	return s.cfg, s.err
}

type stubTenants map[string]resellercontract.TenantContext

func (s stubTenants) ResolveHost(_ context.Context, host string) (resellercontract.TenantContext, error) {
	if tenant, ok := s[host]; ok {
		return tenant, nil
	}
	return resellercontract.MainTenantContext(host), nil
}

type stubResellers struct {
	profile *resellerdomain.Profile
	cfg     *resellerdomain.SiteConfig
}

func (s stubResellers) GetProfileByID(uint) (*resellerdomain.Profile, error) { return s.profile, nil }
func (s stubResellers) GetSiteConfigByResellerID(uint) (*resellerdomain.SiteConfig, error) {
	return s.cfg, nil
}

type stubProducts map[string]*productdomain.Product

func (s stubProducts) GetPublicBySlugForTenant(_ resellercontract.TenantContext, slug string) (*productdomain.Product, error) {
	if p, ok := s[slug]; ok {
		return p, nil
	}
	return nil, errors.New("not found")
}

type stubPricer struct{ visible bool }

func (s stubPricer) LoadDisplayPricingBatch(resellercontract.TenantContext, []productdomain.Product) (*resellercontract.DisplayPricingBatch, error) {
	return &resellercontract.DisplayPricingBatch{}, nil
}

func (s stubPricer) ResolveDisplayPrices(resellercontract.TenantContext, *productdomain.Product, *resellercontract.DisplayPricingBatch) (*resellercontract.DisplayPriceResult, error) {
	return &resellercontract.DisplayPriceResult{Visible: s.visible}, nil
}

func mainConfig() map[string]interface{} {
	return map[string]interface{}{
		"languages": []interface{}{"zh-CN", "zh-TW", "en-US"},
		"brand": map[string]interface{}{
			"site_name":        "主站",
			"site_url":         "https://main.example.com/",
			"site_logo":        "/uploads/main-logo.png",
			"site_icon":        "/uploads/main-icon.png",
			"site_share_image": "",
		},
		"seo": map[string]interface{}{
			"title":       map[string]interface{}{"zh-CN": "", "en-US": "Main SEO"},
			"description": map[string]interface{}{"zh-CN": "主站描述"},
			"keywords":    map[string]interface{}{"zh-CN": "主站,关键词"},
		},
	}
}

func resellerTenant(host string) resellercontract.TenantContext {
	return resellercontract.ResellerTenantContext(host, 7, 70, host)
}

func TestProviderMainSite(t *testing.T) {
	p := New(Dependencies{Settings: &stubSettings{cfg: mainConfig()}})
	meta, err := p.PageMeta(httptest.NewRequest("GET", "http://main.example.com/", nil))
	if err != nil {
		t.Fatal(err)
	}
	// zh-CN 标题为空时按 zh-CN → zh-TW → en-US 顺序回落到任意非空语言。
	if meta.Title != "Main SEO" || meta.SiteName != "主站" || meta.Lang != "zh-CN" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if meta.Description != "主站描述" || meta.Keywords != "主站,关键词" {
		t.Fatalf("unexpected seo: %+v", meta)
	}
	if meta.Image != "/uploads/main-logo.png" || meta.BaseURL != "https://main.example.com" {
		t.Fatalf("unexpected image/base: %+v", meta)
	}
}

func TestProviderShareImagePrecedence(t *testing.T) {
	cfg := mainConfig()
	cfg["brand"].(map[string]interface{})["site_share_image"] = "/uploads/share.png"
	p := New(Dependencies{Settings: &stubSettings{cfg: cfg}})
	meta, _ := p.PageMeta(httptest.NewRequest("GET", "http://main.example.com/", nil))
	if meta.Image != "/uploads/share.png" {
		t.Fatalf("share image should win: %+v", meta)
	}

	cfg = mainConfig()
	cfg["brand"].(map[string]interface{})["site_logo"] = ""
	p = New(Dependencies{Settings: &stubSettings{cfg: cfg}})
	meta, _ = p.PageMeta(httptest.NewRequest("GET", "http://main.example.com/", nil))
	if meta.Image != "/uploads/main-icon.png" {
		t.Fatalf("favicon should be last fallback: %+v", meta)
	}

	cfg = mainConfig()
	cfg["seo"] = map[string]interface{}{}
	p = New(Dependencies{Settings: &stubSettings{cfg: cfg}})
	meta, _ = p.PageMeta(httptest.NewRequest("GET", "http://main.example.com/", nil))
	if meta.Title != "主站" {
		t.Fatalf("title should fall back to site name: %+v", meta)
	}
}

func TestProviderResellerOverridesMain(t *testing.T) {
	host := "shop.reseller.test"
	deps := Dependencies{
		Settings: &stubSettings{cfg: mainConfig()},
		Tenants:  stubTenants{host: resellerTenant(host)},
		Resellers: stubResellers{
			profile: &resellerdomain.Profile{Status: resellerdomain.ProfileStatusActive},
			cfg: &resellerdomain.SiteConfig{
				SiteName: "分销小店",
				Logo:     "/uploads/r-logo.png",
				SEOJSON: jsonmap.JSON{
					"title":       map[string]interface{}{"zh-CN": "分销 SEO 标题"},
					"description": map[string]interface{}{"zh-CN": ""},
				},
			},
		},
	}
	meta, err := New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/", nil))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "分销 SEO 标题" || meta.SiteName != "分销小店" || meta.Image != "/uploads/r-logo.png" {
		t.Fatalf("reseller values should win: %+v", meta)
	}
	// 分销站保存了 SEO 时整体接管，主站描述 / 关键词不得泄漏到白标站。
	if meta.Description != "" || meta.Keywords != "" {
		t.Fatalf("main seo leaked to reseller: %+v", meta)
	}
	if meta.BaseURL != "https://"+host {
		t.Fatalf("reseller base url must be its own host: %+v", meta)
	}
}

func TestProviderResellerFallsBackToMainPerField(t *testing.T) {
	host := "empty.reseller.test"
	deps := Dependencies{
		Settings: &stubSettings{cfg: mainConfig()},
		Tenants:  stubTenants{host: resellerTenant(host)},
		Resellers: stubResellers{
			profile: &resellerdomain.Profile{Status: resellerdomain.ProfileStatusActive},
			cfg:     &resellerdomain.SiteConfig{Favicon: "/uploads/r-icon.png"},
		},
	}
	meta, _ := New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/", nil))
	if meta.SiteName != "主站" || meta.Title != "Main SEO" || meta.Description != "主站描述" {
		t.Fatalf("empty reseller fields should fall back to main: %+v", meta)
	}
	if meta.Image != "/uploads/r-icon.png" {
		t.Fatalf("reseller favicon should beat main images: %+v", meta)
	}

	// 停用的分销站：不使用其配置。
	deps.Resellers = stubResellers{profile: &resellerdomain.Profile{Status: "disabled"}, cfg: &resellerdomain.SiteConfig{SiteName: "x"}}
	meta, _ = New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/", nil))
	if meta.SiteName != "主站" {
		t.Fatalf("inactive reseller config must be ignored: %+v", meta)
	}
}

func TestProviderUnavailableHostAndErrors(t *testing.T) {
	host := "unknown.test"
	deps := Dependencies{
		Settings: &stubSettings{cfg: mainConfig()},
		Tenants:  stubTenants{host: resellercontract.UnavailableTenantContext(host, "not_found")},
	}
	if _, err := New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/", nil)); err == nil {
		t.Fatal("unavailable host must return error so index.html is served untouched")
	}
	deps = Dependencies{Settings: &stubSettings{err: errors.New("db down")}}
	if _, err := New(deps).PageMeta(httptest.NewRequest("GET", "http://main.example.com/", nil)); err == nil {
		t.Fatal("settings error must propagate")
	}
}

func TestProviderProductPage(t *testing.T) {
	product := &productdomain.Product{
		Slug:            "netflix",
		IsActive:        true,
		TitleJSON:       jsonmap.JSON{"zh-CN": "奈飞会员"},
		DescriptionJSON: jsonmap.JSON{"zh-CN": "<p>官方&amp;正版</p>\n\n<script>x()</script>  <b>自动发货</b>"},
		Images:          []string{"", "/uploads/p1.png"},
	}
	inactive := &productdomain.Product{Slug: "off", IsActive: false, TitleJSON: jsonmap.JSON{"zh-CN": "下架"}}
	deps := Dependencies{
		Settings: &stubSettings{cfg: mainConfig()},
		Products: stubProducts{"netflix": product, "off": inactive},
	}
	p := New(deps)
	meta, err := p.PageMeta(httptest.NewRequest("GET", "http://main.example.com/products/netflix", nil))
	if err != nil {
		t.Fatal(err)
	}
	if meta.Title != "奈飞会员 - 主站" || meta.OGType != "product" {
		t.Fatalf("unexpected product title: %+v", meta)
	}
	if meta.Description != "官方&正版 自动发货" {
		t.Fatalf("unexpected product description: %q", meta.Description)
	}
	if meta.Image != "/uploads/p1.png" || meta.Keywords != "主站,关键词" {
		t.Fatalf("unexpected product image/keywords: %+v", meta)
	}

	for _, path := range []string{"/products/off", "/products/missing", "/products/netflix/extra"} {
		meta, _ = p.PageMeta(httptest.NewRequest("GET", "http://main.example.com"+path, nil))
		if meta.Title != "Main SEO" || meta.OGType != "" {
			t.Fatalf("%s should fall back to site meta: %+v", path, meta)
		}
	}

	// 商品 SEO 描述优先，超长截断。
	long := ""
	for i := 0; i < 200; i++ {
		long += "长"
	}
	product.SeoMetaJSON = jsonmap.JSON{"description": map[string]interface{}{"zh-CN": long}, "keywords": "a,b"}
	meta, _ = New(deps).PageMeta(httptest.NewRequest("GET", "http://main.example.com/products/netflix", nil))
	if got := []rune(meta.Description); len(got) != productDescriptionMaxRunes+1 || got[len(got)-1] != '…' {
		t.Fatalf("description not truncated: %q", meta.Description)
	}
	if meta.Keywords != "a,b" {
		t.Fatalf("product keywords: %+v", meta)
	}
}

func TestProviderResellerProductVisibility(t *testing.T) {
	host := "shop.reseller.test"
	product := &productdomain.Product{Slug: "p", IsActive: true, TitleJSON: jsonmap.JSON{"zh-CN": "商品"}}
	deps := Dependencies{
		Settings:  &stubSettings{cfg: mainConfig()},
		Tenants:   stubTenants{host: resellerTenant(host)},
		Resellers: stubResellers{profile: &resellerdomain.Profile{Status: resellerdomain.ProfileStatusActive}, cfg: &resellerdomain.SiteConfig{SiteName: "分销小店"}},
		Products:  stubProducts{"p": product},
		Pricer:    stubPricer{visible: true},
	}
	meta, _ := New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/products/p", nil))
	if meta.Title != "商品 - 分销小店" {
		t.Fatalf("visible reseller product: %+v", meta)
	}
	deps.Pricer = stubPricer{visible: false}
	meta, _ = New(deps).PageMeta(httptest.NewRequest("GET", "http://"+host+"/products/p", nil))
	if meta.OGType == "product" {
		t.Fatalf("product hidden on reseller must fall back to site meta: %+v", meta)
	}
}

func TestProviderCachesPerHost(t *testing.T) {
	settings := &stubSettings{cfg: mainConfig()}
	now := time.Unix(1000, 0)
	p := New(Dependencies{Settings: settings, CacheTTL: 30 * time.Second, Now: func() time.Time { return now }})
	req := func(host string) {
		if _, err := p.PageMeta(httptest.NewRequest("GET", "http://"+host+"/x", nil)); err != nil {
			t.Fatal(err)
		}
	}
	req("a.test")
	req("a.test")
	if settings.calls != 1 {
		t.Fatalf("second request should hit cache, calls=%d", settings.calls)
	}
	req("b.test")
	if settings.calls != 2 {
		t.Fatalf("different host must miss cache, calls=%d", settings.calls)
	}
	now = now.Add(31 * time.Second)
	req("a.test")
	if settings.calls != 3 {
		t.Fatalf("expired entry must reload, calls=%d", settings.calls)
	}
}
