package audithttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
func newHandlerHarness(t *testing.T) (*gin.Engine, *audit.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	service := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize:    100,
		BatchSize:     10,
		FlushInterval: 5 * time.Millisecond,
		MaxEntries:    1000,
	})
	if err := service.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		if err := service.Stop(context.Background()); err != nil {
			t.Logf("stop audit service: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Logf("close test database: %v", err)
		}
	})

	router := gin.New()
	NewHandler(service, zap.NewNop()).RegisterRoutes(router.Group("/api/v1"))
	return router, service
}

// getLogs issues a GET against the audit list endpoint and requires a 200.
func getLogs(t *testing.T, router *gin.Engine, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs"+query, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for %q, got %d: %s", query, w.Code, w.Body.String())
	}
	return w
}

// decodeLogsPage decodes the paginated audit log envelope.
func decodeLogsPage(t *testing.T, w *httptest.ResponseRecorder) httputil.PaginatedData[AuditLogResponse] {
	t.Helper()
	var resp struct {
		Data httputil.PaginatedData[AuditLogResponse] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal audit logs response: %v", err)
	}
	return resp.Data
}

func TestHandlerListLogsPagination(t *testing.T) {
	router, service := newHandlerHarness(t)

	ctx := context.Background()
	for i := 1; i <= 25; i++ {
		if err := service.RecordSync(ctx, audit.Entry{Action: audit.ActionCameraCreate, Target: "camera:test"}); err != nil {
			t.Fatalf("seed audit entry: %v", err)
		}
	}

	page := decodeLogsPage(t, getLogs(t, router, "?page=1&pageSize=10"))
	if page.Total != 25 {
		t.Fatalf("expected total 25, got %d", page.Total)
	}
	if len(page.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(page.Items))
	}
	if page.Page != 1 || page.PageSize != 10 {
		t.Fatalf("unexpected pagination meta: page=%d pageSize=%d", page.Page, page.PageSize)
	}
}

func TestHandlerListLogsFilterAndValidationError(t *testing.T) {
	router, service := newHandlerHarness(t)

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
	filtered := decodeLogsPage(t, getLogs(t, router, "?action=auth.login&status=failed"))
	if filtered.Total != 1 || len(filtered.Items) != 1 {
		t.Fatalf("expected 1 matching item, got total=%d items=%d", filtered.Total, len(filtered.Items))
	}
	if filtered.Items[0].ErrorMsg != "bad password" {
		t.Fatalf("unexpected error msg: %q", filtered.Items[0].ErrorMsg)
	}

	// Repeated actions take precedence over the legacy single action field.
	precedence := decodeLogsPage(t, getLogs(t, router, "?action=auth.login&actions=camera.delete&status=success"))
	if precedence.Total != 1 || len(precedence.Items) != 1 {
		t.Fatalf("expected actions to override action, got total=%d items=%d",
			precedence.Total, len(precedence.Items))
	}
	if precedence.Items[0].Action != audit.ActionCameraDelete {
		t.Fatalf("expected the camera.delete entry, got %q", precedence.Items[0].Action)
	}

	// Empty actions values are ignored so legacy single action still applies.
	emptyActions := decodeLogsPage(t, getLogs(t, router, "?action=auth.login&actions=&status=failed"))
	if emptyActions.Total != 1 || len(emptyActions.Items) != 1 {
		t.Fatalf("expected empty actions param to fall back to action, got total=%d items=%d",
			emptyActions.Total, len(emptyActions.Items))
	}
	if emptyActions.Items[0].Action != audit.ActionAuthLogin {
		t.Fatalf("expected auth.login entry, got %q", emptyActions.Items[0].Action)
	}

	// Multiple actions use OR semantics before pagination; status stays ANDed.
	multi := decodeLogsPage(t, getLogs(t, router,
		"?actions=auth.login&actions=camera.delete&status=success&page=1&pageSize=1"))
	if multi.Total != 2 || len(multi.Items) != 1 || multi.PageSize != 1 {
		t.Fatalf("expected 2 matching rows and one paginated result, got total=%d items=%d pageSize=%d",
			multi.Total, len(multi.Items), multi.PageSize)
	}
	if multi.Items[0].Status != audit.StatusSuccess {
		t.Fatalf("expected the status filter to apply, got %q", multi.Items[0].Status)
	}

	// A malformed timestamp is a validation error, not a silent full scan.
	badReq := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs?startTime=not-a-date", nil)
	badW := httptest.NewRecorder()
	router.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an invalid date, got %d", badW.Code)
	}
}

// TestHandlerListLogsRejectsMalformedPagination pins the fix for a silent
// filter drop: Gin stops binding at the first malformed field, so ignoring the
// binding error would discard the action filter and return every record.
func TestHandlerListLogsRejectsMalformedPagination(t *testing.T) {
	router, service := newHandlerHarness(t)

	if err := service.RecordSync(context.Background(), audit.Entry{
		Action: audit.ActionCameraDelete,
		Target: "camera:1",
	}); err != nil {
		t.Fatalf("seed audit entry: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/audit/logs?page=abc&actions=auth.login", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for malformed pagination, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "camera:1") {
		t.Fatalf("malformed query must not return unfiltered records: %s", w.Body.String())
	}
}

// TestHandlerClearLogsPersistsSelfAudit pins the strong guarantee: a successful
// clear must leave its own audit.clear record behind, in the same transaction
// that performed the delete.
func TestHandlerClearLogsPersistsSelfAudit(t *testing.T) {
	router, service := newHandlerHarness(t)

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
	router, service := newHandlerHarness(t)

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
