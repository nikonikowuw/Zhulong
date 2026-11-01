package systemtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// localizedHandler wires a handler around a caller-supplied driver so error
// paths that depend on clock permissions stay reachable.
func localizedHandler(t *testing.T, driver *StubClockDriver, sntpClient SNTPClient) (*gin.Engine, *TimeService) {
	t.Helper()

	repo, cleanup := setupTestRepository(t)
	t.Cleanup(cleanup)

	svc := NewTimeService(repo, driver, sntpClient, zap.NewNop())
	handler := NewHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	return router, svc
}

// failingRepository forces the read and write paths to fail, standing in for a
// locked or half-open database.
type failingRepository struct {
	err error
}

func (f failingRepository) GetConfig(context.Context) (*SystemTimeConfig, error) {
	return nil, f.err
}

func (f failingRepository) UpdateConfig(context.Context, TimeConfigUpdate) (*SystemTimeConfig, error) {
	return nil, f.err
}

// TestHandlerErrorsAreLocalizedAndSanitized pins the error contract for every
// code this package can emit: the message comes from the catalog registered in
// locale.go, and the internal cause (syscall text, SQLite text, rejected input)
// never reaches the response body.
func TestHandlerErrorsAreLocalizedAndSanitized(t *testing.T) {
	healthySNTP := &mockSNTPClient{
		result: &SNTPResult{Server: "ntp.aliyun.com", Offset: 50 * time.Millisecond, Stratum: 2},
	}
	tests := []struct {
		name        string
		mutate      func(*StubClockDriver)
		prepare     func(*testing.T, *TimeService)
		requests    []httpCall // applied in order, only the last is asserted
		wantStatus  int
		wantCode    string
		wantMessage string
		forbidden   []string
	}{
		{
			name: "invalid timezone",
			requests: []httpCall{{
				method: http.MethodPut,
				path:   "/api/v1/system/time/config",
				body:   testConfigBody(ModeNTP, "Mars/Olympus"),
			}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    "INVALID_TIME_CONFIG",
			wantMessage: "时间配置无效",
			forbidden:   []string{"Mars/Olympus", "unknown time zone"},
		},
		{
			name: "invalid timestamp",
			requests: []httpCall{{
				method: http.MethodPost,
				path:   "/api/v1/system/time/manual",
				body:   `{"targetTime":"yesterday"}`,
			}},
			wantStatus:  http.StatusUnprocessableEntity,
			wantCode:    "INVALID_TIMESTAMP",
			wantMessage: "目标时间必须是合法的 ISO-8601 或 RFC3339 时间格式",
			forbidden:   []string{"yesterday", "unrecognized timestamp"},
		},
		{
			name: "manual mode active",
			requests: []httpCall{
				{
					method: http.MethodPut,
					path:   "/api/v1/system/time/config",
					body:   testConfigBody(ModeManual, "Asia/Shanghai"),
				},
				{method: http.MethodPost, path: "/api/v1/system/time/sync"},
			},
			wantStatus:  http.StatusPreconditionFailed,
			wantCode:    "MANUAL_MODE_ACTIVE",
			wantMessage: "手动模式下无法执行 NTP 对时",
		},
		{
			name: "sync already in progress",
			prepare: func(t *testing.T, svc *TimeService) {
				svc.syncMu.Lock()
				t.Cleanup(svc.syncMu.Unlock)
			},
			requests:    []httpCall{{method: http.MethodPost, path: "/api/v1/system/time/sync"}},
			wantStatus:  http.StatusConflict,
			wantCode:    "SYNC_IN_PROGRESS",
			wantMessage: "已有一次 NTP 对时正在进行",
		},
		{
			name: "database failure during sync",
			prepare: func(_ *testing.T, svc *TimeService) {
				svc.repo = failingRepository{err: errors.New("database is locked")}
			},
			requests:    []httpCall{{method: http.MethodPost, path: "/api/v1/system/time/sync"}},
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "DATABASE_ERROR",
			wantMessage: "数据存储操作失败",
			forbidden:   []string{"database is locked", "system_time_configs"},
		},
		{
			name:   "clock permission missing",
			mutate: func(d *StubClockDriver) { d.SetPermission(false) },
			requests: []httpCall{{
				method: http.MethodPost,
				path:   "/api/v1/system/time/manual",
				body:   `{"targetTime":"2026-10-07T16:45:00Z"}`,
			}},
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "SET_TIME_FAILED",
			wantMessage: "设置系统时间失败",
			forbidden:   []string{"operation not permitted"},
		},
		{
			name: "valid config keeps working",
			requests: []httpCall{{
				method: http.MethodPut,
				path:   "/api/v1/system/time/config",
				body:   testConfigBody(ModeNTP, "Asia/Shanghai"),
			}},
			wantStatus:  http.StatusOK,
			wantCode:    "OK",
			wantMessage: "成功",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			driver := NewStubClockDriver()
			if tc.mutate != nil {
				tc.mutate(driver)
			}
			router, svc := localizedHandler(t, driver, healthySNTP)
			if tc.prepare != nil {
				tc.prepare(t, svc)
			}

			var w *httptest.ResponseRecorder
			for _, call := range tc.requests {
				call.acceptLanguage = "zh-CN"
				w = call.send(router)
			}

			if w.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.wantStatus, w.Code, w.Body.String())
			}

			var resp httputil.Response
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if resp.Code != tc.wantCode {
				t.Errorf("expected code %q, got %q", tc.wantCode, resp.Code)
			}
			if resp.Message != tc.wantMessage {
				t.Errorf("expected localized message %q, got %q", tc.wantMessage, resp.Message)
			}
			for _, forbidden := range tc.forbidden {
				if strings.Contains(w.Body.String(), forbidden) {
					t.Errorf("response leaks internal detail %q: %s", forbidden, w.Body.String())
				}
			}
		})
	}
}
