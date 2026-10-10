package systemtime

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestHandler(t *testing.T) (*gin.Engine, *TimeService, func()) {
	t.Helper()
	repo, cleanup := setupTestRepository(t)
	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{
		result: &SNTPResult{Server: "ntp.aliyun.com", Offset: 50 * time.Millisecond, Stratum: 2},
	}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())
	handler := NewHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	return router, svc, cleanup
}

func TestHandlerGetTime(t *testing.T) {
	router, _, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/time", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code OK, got %s", resp.Code)
	}
}

func TestHandlerUpdateConfigValidationAndSuccess(t *testing.T) {
	router, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// 1. Validation error: missing mode and servers
	badBody := []byte(`{}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/time/config", bytes.NewReader(badBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422 for bad body, got %d (body: %s)", w.Code, w.Body.String())
	}

	// 2. Success update
	validReq := UpdateConfigRequest{
		Mode:                ModeNTP,
		NTPServers:          []string{"ntp.aliyun.com", "cn.pool.ntp.org"},
		SyncIntervalSeconds: 600,
		Timezone:            "Asia/Shanghai",
	}
	validBody, _ := json.Marshal(validReq)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/system/time/config", bytes.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 for valid update, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandlerSyncNow(t *testing.T) {
	router, _, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/time/sync", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}

func TestHandlerManualSet(t *testing.T) {
	router, _, cleanup := setupTestHandler(t)
	defer cleanup()

	// Browser ISO string
	body := []byte(`{"targetTime": "2026-10-07T16:45:00.000Z"}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/time/manual", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d (body: %s)", w.Code, w.Body.String())
	}
}
