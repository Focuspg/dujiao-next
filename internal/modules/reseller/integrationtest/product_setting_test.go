package integrationtest

import (
	"errors"
	"fmt"
	"testing"
	"time"

	resellergormstore "github.com/dujiao-next/internal/modules/reseller/infrastructure/gormstore"

	resellermodule "github.com/dujiao-next/internal/modules/reseller/application"
	resellercontract "github.com/dujiao-next/internal/modules/reseller/contract"
	resellerdomain "github.com/dujiao-next/internal/modules/reseller/domain"

	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"

	categorydomain "github.com/dujiao-next/internal/modules/catalog/category/domain"

	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"

	userdomain "github.com/dujiao-next/internal/modules/identity/user/domain"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func openResellerProductSettingServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:reseller_product_setting_service_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(
		&userdomain.User{},
		&categorydomain.Category{},
		&productdomain.Product{},
		&productdomain.ProductSKU{},
		&resellerdomain.Profile{},
		&resellerdomain.ProductSetting{},
		&resellerdomain.AgentPrice{},
	); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	return db
}

func seedResellerProductSettingServiceData(t *testing.T, db *gorm.DB) (userdomain.User, resellerdomain.Profile, productdomain.Product, []productdomain.ProductSKU) {
	t.Helper()
	user := userdomain.User{Email: fmt.Sprintf("setting-service-%d@example.test", time.Now().UnixNano()), PasswordHash: "hash", Status: constants.UserStatusActive}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user failed: %v", err)
	}
	profile := resellerdomain.Profile{
		UserID:               user.ID,
		Status:               resellerdomain.ProfileStatusActive,
		DefaultMarkupPercent: money.FromDecimal(decimal.RequireFromString("10.00")),
		MaxMarkupPercent:     money.FromDecimal(decimal.RequireFromString("40.00")),
		SettlementStatus:     resellerdomain.SettlementStatusNormal,
	}
	if err := db.Create(&profile).Error; err != nil {
		t.Fatalf("create profile failed: %v", err)
	}
	category := categorydomain.Category{Slug: "service-category", NameJSON: jsonmap.JSON{"zh-CN": "分类"}, IsActive: true}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("create category failed: %v", err)
	}
	product := productdomain.Product{
		CategoryID:      category.ID,
		Slug:            "service-product",
		TitleJSON:       jsonmap.JSON{"zh-CN": "服务商品", "zh-TW": "服務商品", "en-US": "Service Product"},
		PriceAmount:     money.FromDecimal(decimal.RequireFromString("100.00")),
		CostPriceAmount: money.FromDecimal(decimal.RequireFromString("80.00")),
		IsActive:        true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatalf("create product failed: %v", err)
	}
	skus := []productdomain.ProductSKU{
		{ProductID: product.ID, SKUCode: "A", PriceAmount: money.FromDecimal(decimal.RequireFromString("100.00")), CostPriceAmount: money.FromDecimal(decimal.RequireFromString("80.00")), IsActive: true},
		{ProductID: product.ID, SKUCode: "B", PriceAmount: money.FromDecimal(decimal.RequireFromString("200.00")), CostPriceAmount: money.FromDecimal(decimal.RequireFromString("120.00")), IsActive: true},
	}
	if err := db.Create(&skus).Error; err != nil {
		t.Fatalf("create skus failed: %v", err)
	}
	return user, profile, product, skus
}

func newResellerProductSettingServiceForTest(db *gorm.DB) *ResellerProductSettingService {
	return NewResellerProductSettingService(
		resellergormstore.New(db),
		resellergormstore.New(db),
		productgormstore.NewProductStore(db),
	)
}

func TestResellerProductSettingServiceUserSaveValidatesAndReturnsEffectivePrices(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, _, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	detail, err := svc.SaveUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: 0, IsListed: true, PricingMode: resellerdomain.PricingModeMarkupPercent, MarkupPercent: decimal.RequireFromString("20.00")},
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("130.00")},
		},
	})
	if err != nil {
		t.Fatalf("save user settings failed: %v", err)
	}
	if detail == nil || detail.Product.ID != product.ID || len(detail.Settings) != 2 {
		t.Fatalf("unexpected detail: %+v", detail)
	}
	effective := detail.EffectiveBySKUID[skus[0].ID]
	if effective.StringFixed(2) != "130.00" {
		t.Fatalf("sku override effective price want 130.00 got %s", effective.StringFixed(2))
	}
	fallback := detail.EffectiveBySKUID[skus[1].ID]
	if fallback.StringFixed(2) != "240.00" {
		t.Fatalf("product markup effective price want 240.00 got %s", fallback.StringFixed(2))
	}
}

func TestResellerProductSettingServicePersistsFirstHiddenSKUSetting(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	detail, err := svc.SaveUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: skus[0].ID, IsListed: false, PricingMode: resellerdomain.PricingModeInherit},
		},
	})
	if err != nil {
		t.Fatalf("save hidden sku setting failed: %v", err)
	}
	if detail == nil {
		t.Fatal("expected detail after hidden save")
	}

	var row resellerdomain.ProductSetting
	if err := db.Where("reseller_id = ? AND product_id = ? AND sku_id = ?", profile.ID, product.ID, skus[0].ID).
		First(&row).Error; err != nil {
		t.Fatalf("fetch hidden sku setting failed: %v", err)
	}
	if row.IsListed {
		t.Fatalf("expected first hidden sku save to persist is_listed=false, got true: %+v", row)
	}
}

func TestResellerProductSettingServiceRejectsPriceBelowBase(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, _, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	_, err := svc.SaveUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("99.99")},
		},
	})
	if err != ErrResellerPriceBelowBase {
		t.Fatalf("expected ErrResellerPriceBelowBase, got %v", err)
	}
}

func TestResellerProductSettingServiceRejectsMarkupExceeded(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, _, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	_, err := svc.SaveUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("150.00")},
		},
	})
	if err != ErrResellerMarkupExceeded {
		t.Fatalf("expected ErrResellerMarkupExceeded, got %v", err)
	}
}

func TestResellerProductSettingServiceRejectsBatchWithoutPartialWrites(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	_, err := svc.SaveUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("130.00")},
			{SKUID: skus[1].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("99.99")},
		},
	})
	if err != ErrResellerPriceBelowBase {
		t.Fatalf("expected ErrResellerPriceBelowBase, got %v", err)
	}

	var count int64
	if err := db.Model(&resellerdomain.ProductSetting{}).
		Where("reseller_id = ? AND product_id = ?", profile.ID, product.ID).
		Count(&count).Error; err != nil {
		t.Fatalf("count settings failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no partial writes after rejected batch, got %d settings", count)
	}
}

func findPreviewItem(items []ResellerProductSettingPreviewItem, skuID uint) *ResellerProductSettingPreviewItem {
	for i := range items {
		if items[i].SKUID == skuID {
			return &items[i]
		}
	}
	return nil
}

func TestResellerProductSettingServicePreviewMatchesSaveSemantics(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	items, err := svc.PreviewUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: 0, IsListed: true, PricingMode: resellerdomain.PricingModeMarkupPercent, MarkupPercent: decimal.RequireFromString("20.00")},
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("130.00")},
			{SKUID: skus[1].ID, IsListed: true, PricingMode: resellerdomain.PricingModeInherit},
		},
	})
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}

	skuA := findPreviewItem(items, skus[0].ID)
	if skuA == nil || !skuA.Valid || skuA.EffectivePrice.StringFixed(2) != "130.00" {
		t.Fatalf("sku A preview want valid 130.00, got %+v", skuA)
	}
	skuB := findPreviewItem(items, skus[1].ID)
	if skuB == nil || !skuB.Valid || skuB.EffectivePrice.StringFixed(2) != "240.00" {
		t.Fatalf("sku B inherit preview want valid 240.00, got %+v", skuB)
	}

	// 预览不得落库。
	var count int64
	if err := db.Model(&resellerdomain.ProductSetting{}).Where("reseller_id = ?", profile.ID).Count(&count).Error; err != nil {
		t.Fatalf("count settings failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("preview must not persist settings, found %d rows", count)
	}
}

func TestResellerProductSettingServicePreviewFlagsInvalidRules(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, _, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	// 低于基准价 → price_invalid；超出封顶加价 → markup_exceeded。
	items, err := svc.PreviewUserProductSettings(user.ID, product.ID, ResellerProductSettingSaveInput{
		Settings: []ResellerProductSettingInput{
			{SKUID: skus[0].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("99.99")},
			{SKUID: skus[1].ID, IsListed: true, PricingMode: resellerdomain.PricingModeFixedPrice, FixedPriceAmount: decimal.RequireFromString("300.00")},
		},
	})
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	skuA := findPreviewItem(items, skus[0].ID)
	if skuA == nil || skuA.Valid || skuA.ErrorCode != "price_invalid" {
		t.Fatalf("sku A want invalid price_invalid, got %+v", skuA)
	}
	skuB := findPreviewItem(items, skus[1].ID)
	if skuB == nil || skuB.Valid || skuB.ErrorCode != "markup_exceeded" {
		t.Fatalf("sku B want invalid markup_exceeded, got %+v", skuB)
	}
}

func TestResellerProductSettingServiceRequiresActiveProfile(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, _ := seedResellerProductSettingServiceData(t, db)
	if err := db.Model(&resellerdomain.Profile{}).Where("id = ?", profile.ID).Update("status", resellerdomain.ProfileStatusPendingReview).Error; err != nil {
		t.Fatalf("update profile failed: %v", err)
	}
	svc := newResellerProductSettingServiceForTest(db)
	_, _, err := svc.ListUserProductSettings(user.ID, ResellerProductSettingUserListInput{Page: 1, PageSize: 20})
	if err != ErrResellerProfileInactive {
		t.Fatalf("expected inactive profile, got %v product=%d", err, product.ID)
	}
}

func TestResellerProductSettingServiceAdminAgentPrices(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	price := decimal.RequireFromString("85.50")
	if err := svc.SaveAdminAgentPrices(profile.ID, []resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[0].ID, PriceAmount: &price},
	}); err != nil {
		t.Fatalf("save agent price failed: %v", err)
	}
	tooHigh := decimal.RequireFromString("200.01")
	if err := svc.SaveAdminAgentPrices(profile.ID, []resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[1].ID, PriceAmount: &tooHigh},
	}); !errors.Is(err, resellercontract.ErrAgentPriceInvalid) {
		t.Fatalf("agent price above retail want ErrAgentPriceInvalid got %v", err)
	}

	list, err := svc.ListAdminAgentPrices(profile.ID, resellermodule.AgentPriceListInput{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list agent prices failed: %v", err)
	}
	if list.Total != 1 || list.AgentPrices[skus[0].ID].StringFixed(2) != "85.50" {
		t.Fatalf("unexpected agent price list: total=%d prices=%v", list.Total, list.AgentPrices)
	}
	if _, ok := list.AgentPrices[skus[1].ID]; ok {
		t.Fatalf("rejected agent price must not be saved")
	}

	// 分销商在控制台看到自己的拿货价：设置了代理价的 SKU 显示代理价，其余按零售价
	detail, err := svc.GetUserProductSetting(user.ID, product.ID)
	if err != nil {
		t.Fatalf("get user product setting failed: %v", err)
	}
	if got := detail.AgentPriceBySKUID[skus[0].ID].StringFixed(2); got != "85.50" {
		t.Fatalf("sku A agent price want 85.50 got %s", got)
	}
	if got := detail.AgentPriceBySKUID[skus[1].ID].StringFixed(2); got != "200.00" {
		t.Fatalf("sku B agent price want retail 200.00 got %s", got)
	}

	if err := svc.SaveAdminAgentPrices(profile.ID, []resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[0].ID},
	}); err != nil {
		t.Fatalf("clear agent price failed: %v", err)
	}
	var count int64
	if err := db.Model(&resellerdomain.AgentPrice{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("agent price should be cleared, count=%d err=%v", count, err)
	}
}

func TestResellerProductSettingServiceDefaultAgentPrices(t *testing.T) {
	db := openResellerProductSettingServiceTestDB(t)
	user, profile, product, skus := seedResellerProductSettingServiceData(t, db)
	svc := newResellerProductSettingServiceForTest(db)

	defaultA := decimal.RequireFromString("90")
	defaultB := decimal.RequireFromString("150")
	if err := svc.SaveAdminDefaultAgentPrices([]resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[0].ID, PriceAmount: &defaultA},
		{ProductID: product.ID, SKUID: skus[1].ID, PriceAmount: &defaultB},
	}); err != nil {
		t.Fatalf("save default agent prices failed: %v", err)
	}
	tooHigh := decimal.RequireFromString("200.01")
	if err := svc.SaveAdminDefaultAgentPrices([]resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[1].ID, PriceAmount: &tooHigh},
	}); !errors.Is(err, resellercontract.ErrAgentPriceInvalid) {
		t.Fatalf("default agent price above retail want ErrAgentPriceInvalid got %v", err)
	}
	zero := decimal.Zero
	if err := svc.SaveAdminDefaultAgentPrices([]resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[1].ID, PriceAmount: &zero},
	}); !errors.Is(err, resellercontract.ErrAgentPriceInvalid) {
		t.Fatalf("zero default agent price want ErrAgentPriceInvalid got %v", err)
	}

	defaults, err := svc.ListAdminDefaultAgentPrices(resellermodule.AgentPriceListInput{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list default agent prices failed: %v", err)
	}
	if defaults.Total != 1 || defaults.AgentPrices[skus[0].ID].StringFixed(2) != "90.00" || defaults.AgentPrices[skus[1].ID].StringFixed(2) != "150.00" {
		t.Fatalf("unexpected default agent price list: total=%d prices=%v", defaults.Total, defaults.AgentPrices)
	}

	// 分销商单独设置的代理价优先于统一代理价
	own := decimal.RequireFromString("85.50")
	if err := svc.SaveAdminAgentPrices(profile.ID, []resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[0].ID, PriceAmount: &own},
	}); err != nil {
		t.Fatalf("save reseller agent price failed: %v", err)
	}
	list, err := svc.ListAdminAgentPrices(profile.ID, resellermodule.AgentPriceListInput{Page: 1, PageSize: 20})
	if err != nil {
		t.Fatalf("list reseller agent prices failed: %v", err)
	}
	if list.AgentPrices[skus[0].ID].StringFixed(2) != "85.50" || list.DefaultAgentPrices[skus[0].ID].StringFixed(2) != "90.00" {
		t.Fatalf("reseller list must keep own and default prices apart: own=%v default=%v", list.AgentPrices, list.DefaultAgentPrices)
	}
	if _, ok := list.AgentPrices[skus[1].ID]; ok {
		t.Fatalf("default agent price must not appear as the reseller's own price")
	}

	detail, err := svc.GetUserProductSetting(user.ID, product.ID)
	if err != nil {
		t.Fatalf("get user product setting failed: %v", err)
	}
	if got := detail.AgentPriceBySKUID[skus[0].ID].StringFixed(2); got != "85.50" {
		t.Fatalf("sku A effective agent price want own 85.50 got %s", got)
	}
	if got := detail.AgentPriceBySKUID[skus[1].ID].StringFixed(2); got != "150.00" {
		t.Fatalf("sku B effective agent price want default 150.00 got %s", got)
	}

	// 清除统一代理价后回落到零售价，不影响分销商单独设置的价格
	if err := svc.SaveAdminDefaultAgentPrices([]resellermodule.AgentPriceInput{
		{ProductID: product.ID, SKUID: skus[1].ID},
	}); err != nil {
		t.Fatalf("clear default agent price failed: %v", err)
	}
	detail, err = svc.GetUserProductSetting(user.ID, product.ID)
	if err != nil {
		t.Fatalf("get user product setting failed: %v", err)
	}
	if got := detail.AgentPriceBySKUID[skus[1].ID].StringFixed(2); got != "200.00" {
		t.Fatalf("sku B agent price want retail 200.00 after clearing default, got %s", got)
	}
	if got := detail.AgentPriceBySKUID[skus[0].ID].StringFixed(2); got != "85.50" {
		t.Fatalf("sku A own agent price must survive clearing defaults, got %s", got)
	}
}
