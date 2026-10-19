// Package storefrontmeta 为 user SPA 入口页提供服务端写入的标题、描述与分享卡片信息。
//
// internal/web 不依赖业务模块，只定义 web.PageMetaProvider；这里组合设置、分销站点、
// 商品三方数据实现它，并在 app/httpserver 中装配。
package storefrontmeta

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	resellerdomain "github.com/dujiao-next/internal/modules/reseller/domain"

	"github.com/dujiao-next/internal/config"
	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/web"
)

const (
	defaultCacheTTL = 30 * time.Second
	// maxCacheEntries 限制缓存条目数：Host 与商品 slug 都可由请求方任意构造，
	// 超过上限时整体清空，避免被刷出无界内存。
	maxCacheEntries = 1024
	// productDescriptionMaxRunes 商品分享描述的截断长度。
	productDescriptionMaxRunes = 120
	defaultLang                = constants.LocaleZhCN
)

// errSiteUnavailable 表示当前 Host 不是可用站点（未知域名、分销站已停用）。
// 此时入口页原样返回，不写入主站品牌信息，避免任意域名指向本机时冒用主站品牌。
var errSiteUnavailable = errors.New("storefront site unavailable")

// SettingsReader 读取主站 site_config（已合并默认值）。
type SettingsReader interface {
	GetConfig(defaults map[string]interface{}) (map[string]interface{}, error)
}

// TenantResolver 把请求 Host 解析为主站或分销站上下文。
type TenantResolver interface {
	ResolveHost(ctx context.Context, rawHost string) (resellercontract.TenantContext, error)
}

// ResellerSiteReader 读取分销站点资料与站点配置。
type ResellerSiteReader interface {
	GetProfileByID(id uint) (*resellerdomain.Profile, error)
	GetSiteConfigByResellerID(resellerID uint) (*resellerdomain.SiteConfig, error)
}

// ProductReader 按租户读取公开商品（已应用分销站隐藏商品列表）。
type ProductReader interface {
	GetPublicBySlugForTenant(tenant resellercontract.TenantContext, slug string) (*productdomain.Product, error)
}

// ResellerProductPricer 判断商品在分销站是否可售（与商品详情接口同一套规则）。
type ResellerProductPricer interface {
	LoadDisplayPricingBatch(tenant resellercontract.TenantContext, products []productdomain.Product) (*resellercontract.DisplayPricingBatch, error)
	ResolveDisplayPrices(tenant resellercontract.TenantContext, product *productdomain.Product, batch *resellercontract.DisplayPricingBatch) (*resellercontract.DisplayPriceResult, error)
}

// Dependencies 是 Provider 的装配依赖；Products 为空时商品页使用站点级信息。
type Dependencies struct {
	Settings  SettingsReader
	Tenants   TenantResolver
	Resellers ResellerSiteReader
	Products  ProductReader
	Pricer    ResellerProductPricer
	Reseller  config.ResellerConfig
	CacheTTL  time.Duration
	Now       func() time.Time
}

type cacheEntry struct {
	meta    web.PageMeta
	expires time.Time
}

// Provider 计算入口页元信息，并按 Host（商品页再加 slug）短期缓存。
type Provider struct {
	deps  Dependencies
	mu    sync.Mutex
	cache map[string]cacheEntry
}

// New 创建 Provider。
func New(deps Dependencies) *Provider {
	if deps.CacheTTL <= 0 {
		deps.CacheTTL = defaultCacheTTL
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Provider{deps: deps, cache: make(map[string]cacheEntry)}
}

// PageMeta 满足 web.PageMetaProvider。
func (p *Provider) PageMeta(req *http.Request) (web.PageMeta, error) {
	if p == nil || p.deps.Settings == nil || req == nil {
		return web.PageMeta{}, errors.New("storefront meta provider not configured")
	}
	host := resellercontract.ResolveRequestHost(req, p.deps.Reseller)
	slug := productSlugFromPath(req.URL.Path)
	key := host + "\x00" + slug

	now := p.deps.Now()
	p.mu.Lock()
	if entry, ok := p.cache[key]; ok && now.Before(entry.expires) {
		p.mu.Unlock()
		return entry.meta, nil
	}
	p.mu.Unlock()

	meta, err := p.build(req.Context(), host, slug)
	if err != nil {
		return web.PageMeta{}, err
	}

	p.mu.Lock()
	if len(p.cache) >= maxCacheEntries {
		p.cache = make(map[string]cacheEntry)
	}
	p.cache[key] = cacheEntry{meta: meta, expires: now.Add(p.deps.CacheTTL)}
	p.mu.Unlock()
	return meta, nil
}

func (p *Provider) build(ctx context.Context, host, slug string) (web.PageMeta, error) {
	tenant := resellercontract.MainTenantContext(host)
	if p.deps.Tenants != nil {
		resolved, err := p.deps.Tenants.ResolveHost(ctx, host)
		if err != nil {
			return web.PageMeta{}, err
		}
		tenant = resolved
	}
	if tenant.Unavailable {
		return web.PageMeta{}, errSiteUnavailable
	}

	mainCfg, err := p.deps.Settings.GetConfig(map[string]interface{}{})
	if err != nil {
		return web.PageMeta{}, err
	}
	var resellerCfg *resellerdomain.SiteConfig
	if tenant.IsReseller() && p.deps.Resellers != nil {
		resellerCfg, err = p.loadResellerSiteConfig(*tenant.ResellerID)
		if err != nil {
			return web.PageMeta{}, err
		}
	}
	site := resolveSite(mainCfg, tenant, resellerCfg)

	if slug != "" {
		if product := p.loadVisibleProduct(tenant, slug); product != nil {
			return productMeta(site, product), nil
		}
	}
	return site.meta(), nil
}

func (p *Provider) loadResellerSiteConfig(resellerID uint) (*resellerdomain.SiteConfig, error) {
	profile, err := p.deps.Resellers.GetProfileByID(resellerID)
	if err != nil {
		return nil, err
	}
	// 与 public config 的分销站覆盖规则一致：停用的分销站不使用其站点配置。
	if profile == nil || profile.Status != resellerdomain.ProfileStatusActive {
		return nil, nil
	}
	return p.deps.Resellers.GetSiteConfigByResellerID(resellerID)
}

// loadVisibleProduct 返回当前站点可公开展示的商品；不存在、已下架、分销站不展示或读取失败时返回 nil，
// 由调用方回落到站点级信息。
func (p *Provider) loadVisibleProduct(tenant resellercontract.TenantContext, slug string) *productdomain.Product {
	if p.deps.Products == nil {
		return nil
	}
	product, err := p.deps.Products.GetPublicBySlugForTenant(tenant, slug)
	if err != nil || product == nil || !product.IsActive {
		return nil
	}
	if !tenant.IsReseller() {
		return product
	}
	// 以下与商品详情接口 decoratePublicProductForTenant 的分销站可见性规则保持一致。
	if strings.TrimSpace(product.FulfillmentType) == constants.FulfillmentTypeResellerActivation || p.deps.Pricer == nil {
		return nil
	}
	batch, err := p.deps.Pricer.LoadDisplayPricingBatch(tenant, []productdomain.Product{*product})
	if err != nil {
		return nil
	}
	display, err := p.deps.Pricer.ResolveDisplayPrices(tenant, product, batch)
	if err != nil || display == nil || !display.Visible {
		return nil
	}
	return product
}

// siteInfo 是解析完主站 / 分销站优先级后的站点级信息。
type siteInfo struct {
	lang        string
	siteName    string
	title       string
	description string
	keywords    string
	image       string
	baseURL     string
}

func (s siteInfo) meta() web.PageMeta {
	return web.PageMeta{
		Lang:        s.lang,
		Title:       s.title,
		SiteName:    s.siteName,
		Description: s.description,
		Keywords:    s.keywords,
		Image:       s.image,
		BaseURL:     s.baseURL,
	}
}

// resolveSite 按前台 public config 的同一套优先级合成站点信息：
//   - 站点名、Logo、图标：分销站已填写时使用分销站的，否则回落到主站。
//   - SEO：分销站保存过 SEO 配置时整体使用分销站的（与 ApplyPublicConfigOverlay 一致，
//     避免主站 SEO 文案出现在白标分销站上）；标题为空时回落到站点名。
//   - 分享图：分销站 SEO 默认分享图 → 分销站 Logo → 分销站图标 → 主站分享图 → 主站 Logo → 主站图标。
func resolveSite(mainCfg map[string]interface{}, tenant resellercontract.TenantContext, resellerCfg *resellerdomain.SiteConfig) siteInfo {
	brand := asMap(mainCfg["brand"])
	seo := asMap(mainCfg["seo"])
	lang := resolveLang(mainCfg["languages"])

	siteName := asString(brand["site_name"])
	logo := asString(brand["site_logo"])
	icon := asString(brand["site_icon"])
	shareImage := asString(brand["site_share_image"])
	baseURL := strings.TrimRight(asString(brand["site_url"]), "/")
	images := []string{shareImage, logo, icon}

	if tenant.IsReseller() {
		// 与 ApplyPublicConfigOverlay 一致：分销站的站点地址总是取自其自身域名，绝不使用主站 site_url。
		baseURL = ""
		if h := strings.TrimSpace(tenant.Host); h != "" {
			baseURL = "https://" + h
		} else if h := strings.TrimSpace(tenant.PrimaryDomain); h != "" {
			baseURL = "https://" + h
		}
		if resellerCfg != nil {
			if v := strings.TrimSpace(resellerCfg.SiteName); v != "" {
				siteName = v
			}
			resellerImages := []string{}
			if len(resellerCfg.SEOJSON) > 0 {
				seo = map[string]interface{}(resellerCfg.SEOJSON)
				resellerImages = append(resellerImages, asString(seo["default_og_image"]))
			}
			resellerImages = append(resellerImages, strings.TrimSpace(resellerCfg.Logo), strings.TrimSpace(resellerCfg.Favicon))
			images = append(resellerImages, images...)
		}
	}

	title := localizedText(seo["title"], lang)
	if title == "" {
		title = siteName
	}
	return siteInfo{
		lang:        lang,
		siteName:    siteName,
		title:       title,
		description: localizedText(seo["description"], lang),
		keywords:    localizedText(seo["keywords"], lang),
		image:       firstNonEmpty(images...),
		baseURL:     baseURL,
	}
}

// productMeta 商品详情页：标题为「商品名 - 站点名」，描述优先取商品 SEO 描述，再取商品简介纯文本。
func productMeta(site siteInfo, product *productdomain.Product) web.PageMeta {
	meta := site.meta()
	meta.OGType = "product"

	siteName := firstNonEmpty(site.siteName, site.title)
	if name := localizedText(map[string]interface{}(product.TitleJSON), site.lang); name != "" {
		if siteName != "" && siteName != name {
			meta.Title = name + " - " + siteName
		} else {
			meta.Title = name
		}
	}

	seoMeta := map[string]interface{}(product.SeoMetaJSON)
	description := localizedText(seoMeta["description"], site.lang)
	if description == "" {
		description = localizedText(map[string]interface{}(product.DescriptionJSON), site.lang)
	}
	if description = truncateRunes(plainText(description), productDescriptionMaxRunes); description != "" {
		meta.Description = description
	}
	if keywords := localizedText(seoMeta["keywords"], site.lang); keywords != "" {
		meta.Keywords = keywords
	}
	for _, image := range product.Images {
		if image = strings.TrimSpace(image); image != "" {
			meta.Image = image
			break
		}
	}
	return meta
}

// productSlugFromPath 识别前台商品详情路由 /products/:slug。
func productSlugFromPath(p string) string {
	rest, ok := strings.CutPrefix(p, "/products/")
	if !ok {
		return ""
	}
	rest = strings.TrimSuffix(rest, "/")
	if rest == "" || strings.Contains(rest, "/") {
		return ""
	}
	if unescaped, err := url.PathUnescape(rest); err == nil {
		rest = unescaped
	}
	return strings.TrimSpace(rest)
}

// resolveLang 前台默认语言为 zh-CN；站点关闭了 zh-CN 时取启用语言列表中的第一个受支持语言。
func resolveLang(raw interface{}) string {
	var langs []string
	switch v := raw.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				langs = append(langs, strings.TrimSpace(s))
			}
		}
	case []string:
		langs = v
	}
	if len(langs) == 0 {
		return defaultLang
	}
	first := ""
	for _, lang := range langs {
		if lang == defaultLang {
			return defaultLang
		}
		if first == "" && isSupportedLocale(lang) {
			first = lang
		}
	}
	if first == "" {
		return defaultLang
	}
	return first
}

func isSupportedLocale(lang string) bool {
	for _, supported := range constants.SupportedLocales {
		if lang == supported {
			return true
		}
	}
	return false
}

// localizedText 取多语言文本：优先 lang，再 zh-CN、zh-TW、en-US，最后任意非空值；
// 兼容直接是字符串的旧数据。
func localizedText(raw interface{}, lang string) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case nil:
		return ""
	}
	m := asMap(raw)
	if len(m) == 0 {
		return ""
	}
	order := append([]string{lang}, constants.SupportedLocales...)
	for _, key := range order {
		if s := asString(m[key]); s != "" {
			return s
		}
	}
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if s := asString(m[key]); s != "" {
			return s
		}
	}
	return ""
}

var (
	htmlTagPattern    = regexp.MustCompile(`(?s)<[^>]*>`)
	blockScriptsStyle = regexp.MustCompile(`(?is)<(script|style)\b[^>]*>.*?</(script|style)>`)
)

// plainText 去掉 HTML 标签、解码实体并折叠空白。
func plainText(s string) string {
	s = blockScriptsStyle.ReplaceAllString(s, " ")
	s = htmlTagPattern.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func truncateRunes(s string, limit int) string {
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func asMap(raw interface{}) map[string]interface{} {
	switch v := raw.(type) {
	case map[string]interface{}:
		return v
	case jsonmap.JSON:
		return map[string]interface{}(v)
	case map[string]string:
		out := make(map[string]interface{}, len(v))
		for key, value := range v {
			out[key] = value
		}
		return out
	}
	return nil
}

func asString(raw interface{}) string {
	s, _ := raw.(string)
	return strings.TrimSpace(s)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
