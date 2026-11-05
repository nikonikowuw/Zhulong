package systeminfo

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap/zaptest"
)

func TestHandler_GetHardware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	col := &mockCollector{}
	svc := NewService(col, logger)
	identity := NewIdentityService(nil, logger)
	handler := NewHandler(svc, identity, logger)

	router := gin.New()
	apiGroup := router.Group("/api/v1")
	handler.RegisterRoutes(apiGroup)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/hardware", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response envelope: %v", err)
	}

	if resp.Code != "OK" {
		t.Fatalf("expected code 'OK', got %q", resp.Code)
	}
}

func TestHandler_IdentityGetAndUpdate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger := zaptest.NewLogger(t)
	identity := NewIdentityService(newTestIdentityDB(t), logger)
	if err := identity.Start(context.Background()); err != nil {
		t.Fatalf("identity Start: %v", err)
	}
	handler := NewHandler(NewService(&mockCollector{}, logger), identity, logger)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	// GET returns the generated fallback.
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/identity", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET identity status %d, body: %s", rec.Code, rec.Body.String())
	}
	var getResp struct {
		Code string                 `json:"code"`
		Data SystemIdentityResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode GET: %v", err)
	}
	if getResp.Code != "OK" || !IsValidDeviceID(getResp.Data.DeviceID) {
		t.Fatalf("unexpected GET payload: %+v", getResp)
	}

	// PUT persists an operator-supplied id.
	body, _ := json.Marshal(UpdateSystemIdentityRequest{DeviceID: "site-b-02"})
	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT identity status %d, body: %s", rec.Code, rec.Body.String())
	}
	if got := identity.DeviceID(); got != "site-b-02" {
		t.Fatalf("device id not updated, got %q", got)
	}

	// PUT with an illegal value is rejected as a validation error.
	body, _ = json.Marshal(UpdateSystemIdentityRequest{DeviceID: "bad value"})
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/system/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid id, got %d, body: %s", rec.Code, rec.Body.String())
	}
	if got := identity.DeviceID(); got != "site-b-02" {
		t.Fatalf("rejected update must not change the identity, got %q", got)
	}
}
