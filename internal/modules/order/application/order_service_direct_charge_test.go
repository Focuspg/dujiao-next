package application

import (
	"errors"
	"fmt"
	"testing"
	"time"

	categorydomain "github.com/dujiao-next/internal/modules/catalog/category/domain"
	productdomain "github.com/dujiao-next/internal/modules/catalog/product/domain"
	productgormstore "github.com/dujiao-next/internal/modules/catalog/product/store/gormstore"
	promotiondomain "github.com/dujiao-next/internal/modules/promotion/domain"
	promotiongormstore "github.com/dujiao-next/internal/modules/promotion/infrastructure/gormstore"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/shared/jsonmap"
	"github.com/dujiao-next/internal/shared/money"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func TestBuildOrderResultDirectChargeRules(t *testing.T) {
	dsn := fmt.Sprintf("file:order_service_direct_charge_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&categorydomain.Category{}, &productdomain.Product{}, &productdomain.ProductSKU{}, &promotiondomain.Promotion{}); err != nil {
		t.Fatalf("migrate failed: %v", err)
	}
	now := time.Now()
	category := categorydomain.Category{Slug: "dc", NameJSON: jsonmap.JSON{"zh-CN": "代充"}, CreatedAt: now}
	if err := db.Create(&category).Error; err != nil {
		t.Fatalf("create category failed: %v", err)
	}
	newProduct := func(slug, fulfillment string) (uint, uint) {
		product := productdomain.Product{
			CategoryID: category.ID, Slug: slug, TitleJSON: jsonmap.JSON{"zh-CN": slug},
			PriceAmount: money.FromDecimal(decimal.NewFromInt(100)), PurchaseType: constants.ProductPurchaseMember,
			FulfillmentType: fulfillment, IsActive: true, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&product).Error; err != nil {
			t.Fatalf("create product failed: %v", err)
		}
		sku := productdomain.ProductSKU{
			ProductID: product.ID, SKUCode: productdomain.DefaultSKUCode, PriceAmount: money.FromDecimal(decimal.NewFromInt(100)),
			IsActive: true, ManualStockTotal: constants.ManualStockUnlimited, CreatedAt: now, UpdatedAt: now,
		}
		if err := db.Create(&sku).Error; err != nil {
			t.Fatalf("create sku failed: %v", err)
		}
		return product.ID, sku.ID
	}
	dcProduct, dcSKU := newProduct("gpt-plus", constants.FulfillmentTypeDirectCharge)
	manualProduct, manualSKU := newProduct("manual", constants.FulfillmentTypeManual)

	svc := NewOrderService(OrderServiceOptions{
		ProductStore:    productgormstore.NewProductStore(db),
		ProductSKUStore: productgormstore.NewSKUStore(db),
		PromotionRepo:   promotiongormstore.New(db),
		ExpireMinutes:   15,
	})
	item := func(productID, skuID uint, quantity int) CreateOrderItem {
		return CreateOrderItem{ProductID: productID, SKUID: skuID, Quantity: quantity}
	}
	cases := []struct {
		name   string
		params orderCreateParams
		want   error
	}{
		{"direct charge via flow", orderCreateParams{UserID: 1, DirectCharge: true, Items: []CreateOrderItem{item(dcProduct, dcSKU, 1)}}, nil},
		{"direct charge via normal checkout", orderCreateParams{UserID: 1, Items: []CreateOrderItem{item(dcProduct, dcSKU, 1)}}, ErrDirectChargeOrderInvalid},
		{"normal product via flow", orderCreateParams{UserID: 1, DirectCharge: true, Items: []CreateOrderItem{item(manualProduct, manualSKU, 1)}}, ErrDirectChargeOrderInvalid},
		{"quantity above one", orderCreateParams{UserID: 1, DirectCharge: true, Items: []CreateOrderItem{item(dcProduct, dcSKU, 2)}}, ErrDirectChargeOrderInvalid},
		{"with coupon", orderCreateParams{UserID: 1, DirectCharge: true, CouponCode: "X", Items: []CreateOrderItem{item(dcProduct, dcSKU, 1)}}, ErrDirectChargeOrderInvalid},
		{"mixed cart", orderCreateParams{UserID: 1, DirectCharge: true, Items: []CreateOrderItem{item(dcProduct, dcSKU, 1), item(manualProduct, manualSKU, 1)}}, ErrDirectChargeOrderInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := svc.buildOrderResult(tc.params)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
			if tc.want == nil && (len(result.Plans) != 1 || result.Plans[0].Item.FulfillmentType != constants.FulfillmentTypeDirectCharge) {
				t.Fatalf("unexpected plans: %+v", result.Plans)
			}
		})
	}
}
