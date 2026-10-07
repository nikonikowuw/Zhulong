package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestAuditHandler(t *testing.T) (*gin.Engine, *Service, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store, cleanupStore := setupTestStore(t)
	svc := NewService(store, zap.NewNop(), Config{
		BufferSize:    100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		MaxEntries:    1000,
	})
	_ = svc.Start(context.Background())

	handler := NewHandler(svc, zap.NewNop())
	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	cleanup := func() {
		_ = svc.Stop(context.Background())
		cleanupStore()
	}

	return router, svc, cleanup
}

func TestHandlerListLogsPagination(t *testing.T) {
	router, svc, cleanup := setupTestAuditHandler(t)
	defer cleanup()

	ctx := context.Background()

	// Seed 25 entries synchronously via RecordSync for deterministic testing
	for i := 1; i <= 25; i++ {
		_ = svc.RecordSync(ctx, Entry{
			Action: ActionCameraCreate,
			Target: "camera:test",
			Status: StatusSuccess,
		})
	}

	// Request page 1, pageSize 10
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/audit/logs?page=1&pageSize=10", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	dataBytes, _ := json.Marshal(resp.Data)
	var paginated httputil.PaginatedData[AuditLog]
	if err := json.Unmarshal(dataBytes, &paginated); err != nil {
		t.Fatalf("unmarshal paginated data: %v", err)
	}

	if paginated.Total != 25 {
		t.Fatalf("expected total 25, got %d", paginated.Total)
	}
	if len(paginated.Items) != 10 {
		t.Fatalf("expected 10 items, got %d", len(paginated.Items))
	}
	if paginated.Page != 1 || paginated.PageSize != 10 {
		t.Fatalf("unexpected pagination meta: page=%d pageSize=%d", paginated.Page, paginated.PageSize)
	}
}

func TestHandlerListLogsFilterAndValidationError(t *testing.T) {
	router, svc, cleanup := setupTestAuditHandler(t)
	defer cleanup()

	ctx := context.Background()
	_ = svc.RecordSync(ctx, Entry{Action: ActionAuthLogin, Status: StatusSuccess})
	_ = svc.RecordSync(ctx, Entry{Action: ActionAuthLogin, Status: StatusFailed, ErrorMsg: "bad password"})
	_ = svc.RecordSync(ctx, Entry{Action: ActionCameraDelete, Status: StatusSuccess})

	// Filter by action and status
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/audit/logs?action=auth.login&status=failed", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data httputil.PaginatedData[AuditLog] `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Total != 1 || len(resp.Data.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", resp.Data.Total)
	}
	if resp.Data.Items[0].ErrorMsg != "bad password" {
		t.Fatalf("unexpected item error msg: %s", resp.Data.Items[0].ErrorMsg)
	}

	// Bad RFC3339 time
	badReq, _ := http.NewRequest(http.MethodGet, "/api/v1/audit/logs?startTime=not-a-date", nil)
	badW := httptest.NewRecorder()
	router.ServeHTTP(badW, badReq)
	if badW.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid date, got %d", badW.Code)
	}
}

func TestHandlerClearLogs(t *testing.T) {
	router, svc, cleanup := setupTestAuditHandler(t)
	defer cleanup()

	ctx := context.Background()
	_ = svc.RecordSync(ctx, Entry{Action: ActionAuthLogout})
	_ = svc.RecordSync(ctx, Entry{Action: ActionAuthLogout})

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/audit/logs", nil)
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
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Data.Cleared != 2 {
		t.Fatalf("expected 2 cleared, got %d", resp.Data.Cleared)
	}

	// Verify count is 0
	total, _ := svc.Count(ctx)
	if total != 0 {
		t.Fatalf("expected 0 remaining, got %d", total)
	}
}
