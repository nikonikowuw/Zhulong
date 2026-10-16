package audithttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// newHandlerHarness wires the audit HTTP Handler against a live service. The
// audit middleware is deliberately NOT mounted: these tests cover the Handler's
// own query and clear contract, and ClearLogs must not depend on the
// asynchronous middleware path.
func newHandlerHarness(t *testing.T) (*gin.Engine, *audit.Service, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	service := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	handler := NewHandler(service, zap.NewNop())
	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	cleanup := func() {
		_ = service.Stop(context.Background())
		_ = dbStore.Close()
	}
	return router, service, cleanup
}

func TestHandlerListLogsPagination(t *testing.T) {
	router, service, cleanup := newHandlerHarness(t)
	defer cleanup()

	ctx := context.Background()
	for i := 1; i <= 25; i++ {
		if err := service.RecordSync(ctx, audit.Entry{Action: audit.ActionCameraCreate, Target: "camera:test"}); err != nil {
			t.Fatalf("seed audit entry: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs?page=1&pageSize=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data httputil.PaginatedData[AuditLogResponse] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Total != 25 {
		t.Fatalf("expected total 25, got %d", resp.Data.Total)
	}
	if len(resp.Data.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(resp.Data.Items))
	}
	if resp.Data.Page != 1 || resp.Data.PageSize != 10 {
		t.Fatalf("unexpected pagination meta: page=%d pageSize=%d", resp.Data.Page, resp.Data.PageSize)
	}
}

func TestHandlerListLogsFilterAndValidationError(t *testing.T) {
	router, service, cleanup := newHandlerHarness(t)
	defer cleanup()

	ctx := context.Background()
	seed := []audit.Entry{
		{Action: audit.ActionAuthLogin, Status: audit.StatusSuccess},
		{Action: audit.ActionAuthLogin, Status: audit.StatusFailed, ErrorMsg: "bad password"},
		{Action: audit.ActionCameraDelete, Status: audit.StatusSuccess},
	}
	for _, e := range seed {
		if err := service.RecordSync(ctx, e); err != nil {
			t.Fatalf("seed audit entry: %v", err)
		}
	}

	// Single action plus status filter.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs?action=auth.login&status=failed", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data httputil.PaginatedData[AuditLogResponse] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("expected 1 matching item, got total=%d items=%d", resp.Data.Total, len(resp.Data.Items))
	}
	if resp.Data.Items[0].ErrorMsg != "bad password" {
		t.Fatalf("unexpected error msg: %q", resp.Data.Items[0].ErrorMsg)
	}

	// Repeated actions take precedence over the legacy single action field.
	precedenceReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/logs?action=auth.login&actions=camera.delete&status=success", nil)
	precedenceW := httptest.NewRecorder()
	router.ServeHTTP(precedenceW, precedenceReq)
	var precedenceResp struct {
		Data httputil.PaginatedData[AuditLogResponse] `json:"data"`
	}
	if err := json.Unmarshal(precedenceW.Body.Bytes(), &precedenceResp); err != nil {
		t.Fatalf("unmarshal precedence response: %v", err)
	}
	if precedenceResp.Data.Total != 1 || len(precedenceResp.Data.Items) != 1 {
		t.Fatalf("expected actions to override action, got total=%d items=%d",
			precedenceResp.Data.Total, len(precedenceResp.Data.Items))
	}
	if precedenceResp.Data.Items[0].Action != audit.ActionCameraDelete {
		t.Fatalf("expected the camera.delete entry, got %q", precedenceResp.Data.Items[0].Action)
	}

	// Multiple actions use OR semantics before pagination; status stays ANDed.
	multiReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/audit/logs?actions=auth.login&actions=camera.delete&status=success&page=1&pageSize=1", nil)
	multiW := httptest.NewRecorder()
	router.ServeHTTP(multiW, multiReq)
	if multiW.Code != http.StatusOK {
		t.Fatalf("expected 200 for multiple actions, got %d: %s", multiW.Code, multiW.Body.String())
	}
	var multiResp struct {
		Data httputil.PaginatedData[AuditLogResponse] `json:"data"`
	}
	if err := json.Unmarshal(multiW.Body.Bytes(), &multiResp); err != nil {
		t.Fatalf("unmarshal multi-action response: %v", err)
	}
	if multiResp.Data.Total != 2 || len(multiResp.Data.Items) != 1 || multiResp.Data.PageSize != 1 {
		t.Fatalf("expected 2 matching rows and one paginated result, got total=%d items=%d pageSize=%d",
			multiResp.Data.Total, len(multiResp.Data.Items), multiResp.Data.PageSize)
	}
	if multiResp.Data.Items[0].Status != audit.StatusSuccess {
		t.Fatalf("expected the status filter to apply, got %q", multiResp.Data.Items[0].Status)
	}

	// A malformed timestamp is a validation error, not a silent full scan.
	badReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs?startTime=not-a-date", nil)
	badW := httptest.NewRecorder()
	router.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an invalid date, got %d", badW.Code)
	}
}

// TestHandlerClearLogsPersistsSelfAudit pins the strong guarantee: a successful
// clear must leave its own audit.clear record behind, in the same transaction
// that performed the delete.
func TestHandlerClearLogsPersistsSelfAudit(t *testing.T) {
	router, service, cleanup := newHandlerHarness(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := service.RecordSync(ctx, audit.Entry{Action: audit.ActionAuthLogout}); err != nil {
			t.Fatalf("seed audit entry: %v", err)
		}
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/audit/logs", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			Cleared int64 `json:"cleared"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal clear response: %v", err)
	}
	if resp.Data.Cleared != 2 {
		t.Fatalf("expected 2 cleared records, got %d", resp.Data.Cleared)
	}

	// The self-audit record is the only survivor, and it records the count.
	items, total, err := service.List(ctx, audit.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list after clear: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected exactly the audit.clear record to survive, got total=%d items=%d", total, len(items))
	}
	if items[0].Action != audit.ActionAuditClear {
		t.Fatalf("expected an audit.clear record, got %q", items[0].Action)
	}
	if items[0].Detail != "deleted=2" {
		t.Fatalf("expected the deleted count in the detail, got %q", items[0].Detail)
	}
}

// TestHandlerClearLogsKeepsPreviousClearRecords pins the repeated-clear
// guarantee: clearing twice must retain the earlier audit.clear evidence.
func TestHandlerClearLogsKeepsPreviousClearRecords(t *testing.T) {
	router, service, cleanup := newHandlerHarness(t)
	defer cleanup()

	ctx := context.Background()
	if err := service.RecordSync(ctx, audit.Entry{Action: audit.ActionAuthLogout}); err != nil {
		t.Fatalf("seed audit entry: %v", err)
	}

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/audit/logs", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("clear #%d failed: %d %s", i+1, w.Code, w.Body.String())
		}
	}

	items, total, err := service.List(ctx, audit.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list after clears: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected both audit.clear records to survive, got total=%d items=%d", total, len(items))
	}
	for _, item := range items {
		if item.Action != audit.ActionAuditClear {
			t.Errorf("expected only audit.clear records, got %q", item.Action)
		}
	}
}
