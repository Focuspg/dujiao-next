package directchargehttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	directchargeapp "github.com/dujiao-next/internal/modules/directcharge/application"
	directchargecontract "github.com/dujiao-next/internal/modules/directcharge/contract"
	directchargedomain "github.com/dujiao-next/internal/modules/directcharge/domain"

	"github.com/gin-gonic/gin"
)

type fakeAdminService struct {
	gotID, gotAdmin uint
	gotText         string
	gotOutcome      string
	err             error
}

func (f *fakeAdminService) ListSpecs() ([]directchargedomain.Spec, error) { return nil, f.err }
func (f *fakeAdminService) SaveSpec(id uint, _ directchargeapp.SpecInput) (*directchargedomain.Spec, error) {
	f.gotID = id
	return &directchargedomain.Spec{ID: id}, f.err
}
func (f *fakeAdminService) ListTasks(directchargecontract.TaskFilter) ([]directchargedomain.Task, int64, error) {
	return nil, 0, f.err
}
func (f *fakeAdminService) TaskDetail(id uint) (*directchargeapp.TaskDetail, error) {
	f.gotID = id
	return &directchargeapp.TaskDetail{}, f.err
}
func (f *fakeAdminService) Reconcile(_ context.Context, id, adminID uint) (*directchargeapp.ReconcileResult, error) {
	f.gotID, f.gotAdmin = id, adminID
	return &directchargeapp.ReconcileResult{}, f.err
}
func (f *fakeAdminService) Retry(id uint, reason string, adminID uint) (*directchargeapp.TaskDetail, error) {
	f.gotID, f.gotText, f.gotAdmin = id, reason, adminID
	return &directchargeapp.TaskDetail{}, f.err
}
func (f *fakeAdminService) Resolve(id uint, outcome, reason string, adminID uint) (*directchargeapp.TaskDetail, error) {
	f.gotID, f.gotOutcome, f.gotText, f.gotAdmin = id, outcome, reason, adminID
	return &directchargeapp.TaskDetail{}, f.err
}
func (f *fakeAdminService) Refund(id uint, remark string, adminID uint) (*directchargeapp.TaskDetail, error) {
	f.gotID, f.gotText, f.gotAdmin = id, remark, adminID
	return &directchargeapp.TaskDetail{}, f.err
}

func adminRouter(service AdminService, adminID uint) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if adminID > 0 {
			c.Set("admin_id", adminID)
		}
	})
	RegisterAdminRoutes(router, NewAdminHandler(service))
	return router
}

func TestAdminActionsPassAdminAndBody(t *testing.T) {
	service := &fakeAdminService{}
	router := adminRouter(service, 7)
	cases := []struct {
		path, body string
		check      func() bool
	}{
		{"/direct-charge/tasks/3/retry", `{"reason":"checked"}`, func() bool { return service.gotText == "checked" }},
		{"/direct-charge/tasks/3/resolve", `{"outcome":"failed","reason":"r"}`, func() bool { return service.gotOutcome == "failed" }},
		{"/direct-charge/tasks/3/refund", `{"remark":"voucher"}`, func() bool { return service.gotText == "voucher" }},
		{"/direct-charge/tasks/3/reconcile", ``, func() bool { return true }},
	}
	for _, tc := range cases {
		*service = fakeAdminService{}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body)))
		if !strings.Contains(rec.Body.String(), `"status_code":0`) || service.gotID != 3 || service.gotAdmin != 7 || !tc.check() {
			t.Fatalf("%s: code=%d service=%+v", tc.path, rec.Code, service)
		}
	}
}

func TestAdminActionsRequireAdminAndMapErrors(t *testing.T) {
	service := &fakeAdminService{}
	rec := httptest.NewRecorder()
	adminRouter(service, 0).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/direct-charge/tasks/3/refund", strings.NewReader(`{"remark":"x"}`)))
	if !strings.Contains(rec.Body.String(), `"status_code":401`) || service.gotID != 0 {
		t.Fatalf("missing admin id must be rejected before calling the service")
	}

	service.err = directchargecontract.ErrAttemptOpen
	rec = httptest.NewRecorder()
	adminRouter(service, 7).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/direct-charge/tasks/3/refund", strings.NewReader(`{"remark":"x"}`)))
	if !strings.Contains(rec.Body.String(), `"status_code":400`) {
		t.Fatalf("attempt open should map to a 400 with its message, got %d %s", rec.Code, rec.Body.String())
	}
}
