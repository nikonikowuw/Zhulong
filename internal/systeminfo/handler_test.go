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

func newTestRouter(t *testing.T, identity *IdentityService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	handler := NewHandler(NewService(&mockCollector{}, zaptest.NewLogger(t)), identity, zaptest.NewLogger(t))
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	return router
}

func TestHandler_GetHardware(t *testing.T) {
	router := newTestRouter(t, NewIdentityService(nil, zaptest.NewLogger(t)))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/system/hardware", nil))

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
	logger := zaptest.NewLogger(t)
	identity := NewIdentityService(newTestIdentityDB(t), logger)
	if err := identity.Start(context.Background()); err != nil {
		t.Fatalf("identity Start: %v", err)
	}
	router := newTestRouter(t, identity)

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
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, identityPutRequest(t, "site-b-02"))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT identity status %d, body: %s", rec.Code, rec.Body.String())
	}
	if got := identity.DeviceID(); got != "site-b-02" {
		t.Fatalf("device id not updated, got %q", got)
	}

	// PUT with an illegal value is rejected as a validation error.
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, identityPutRequest(t, "bad value"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid id, got %d, body: %s", rec.Code, rec.Body.String())
	}
	if got := identity.DeviceID(); got != "site-b-02" {
		t.Fatalf("rejected update must not change the identity, got %q", got)
	}
}

func TestHandler_UpdateIdentityWithoutDatabase(t *testing.T) {
	// A handler wired with an identity service that has no store must answer 503 and,
	// critically, must not reuse the "malformed input" machine code for it.
	router := newTestRouter(t, NewIdentityService(nil, zaptest.NewLogger(t)))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, identityPutRequest(t, "site-b-02"))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d, body: %s", rec.Code, rec.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Code != CodeIdentityUnavailable {
		t.Fatalf("expected code %q, got %q", CodeIdentityUnavailable, resp.Code)
	}
}

func identityPutRequest(t *testing.T, deviceID string) *http.Request {
	t.Helper()

	body, err := json.Marshal(UpdateSystemIdentityRequest{DeviceID: deviceID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/identity", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}
