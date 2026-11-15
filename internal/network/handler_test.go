package network

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestNetworkHandler(t *testing.T) (*gin.Engine, *NetworkService, *WatchdogManager, *MockProvider) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpDir := t.TempDir()
	provider := NewMockProvider(zap.NewNop())
	watchdog := NewWatchdogManager(tmpDir, provider, zap.NewNop())
	service := NewNetworkService(
		provider,
		watchdog,
		"8000",
		zap.NewNop(),
	)
	handler := NewHandler(service)

	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterProtectedRoutes(api)
	handler.RegisterPublicRoutes(api)

	return router, service, watchdog, provider
}

func TestHandlerListInterfaces(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	req, err := http.NewRequest(http.MethodGet, "/api/v1/system/network/interfaces", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Code != "OK" {
		t.Fatalf("expected code OK, got %s", resp.Code)
	}

	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var ifaces []InterfaceInfo
	if err := json.Unmarshal(raw, &ifaces); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	if len(ifaces) < 2 {
		t.Fatalf("expected at least 2 interfaces, got %d", len(ifaces))
	}

	for _, iface := range ifaces {
		if iface.IsCurrent {
			t.Errorf("expected no current interface when the request address cannot be identified, got %s", iface.Name)
		}
		if iface.DNS == nil {
			t.Errorf("expected DNS to be non-nil empty array, got nil for %s", iface.Name)
		}
		if iface.IPAddresses == nil {
			t.Errorf("expected IPAddresses to be non-nil empty array, got nil for %s", iface.Name)
		}
	}
	bodyStr := w.Body.String()
	if strings.Contains(bodyStr, `"dns":null`) {
		t.Errorf("response body should never contain '\"dns\":null'")
	}
	if strings.Contains(bodyStr, `"ipAddresses":null`) {
		t.Errorf("response body should never contain '\"ipAddresses\":null'")
	}
}

func TestHandlerApplyAndConfirm(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	// Apply valid static config
	applyBody := InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.188",
		SubnetMask: "255.255.255.0",
		Gateway:    "192.168.1.1",
		DNS:        []string{"8.8.8.8"},
		SetDefault: true,
	}
	bodyBytes, err := json.Marshal(applyBody)
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/interfaces/eth0/apply",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var applyResp ApplyResponse
	if err := json.Unmarshal(raw, &applyResp); err != nil {
		t.Fatalf("unmarshal apply response: %v", err)
	}

	if applyResp.ConfirmToken == "" {
		t.Fatalf("expected confirm_token in response")
	}

	// Confirm via token endpoint
	confirmReq, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/confirm?token="+applyResp.ConfirmToken,
		nil,
	)
	if err != nil {
		t.Fatalf("create confirm request: %v", err)
	}
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, confirmReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for confirm, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandlerApplyConflictAndValidation(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	// Gateway out of subnet
	badGateway := InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.50",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1",
	}
	bodyBytes, err := json.Marshal(badGateway)
	if err != nil {
		t.Fatalf("marshal bad gateway body: %v", err)
	}
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/interfaces/eth0/apply",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for gateway out of subnet, got %d: %s", w.Code, w.Body.String())
	}

	// Gateway conflict
	conflict := InterfaceConfig{
		Mode:       "static",
		IPAddress:  "10.0.0.50",
		SubnetMask: "255.255.255.0",
		Gateway:    "10.0.0.1",
		SetDefault: true,
	}
	bodyBytes, err = json.Marshal(conflict)
	if err != nil {
		t.Fatalf("marshal conflict body: %v", err)
	}
	req2, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/interfaces/eth1/apply",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		t.Fatalf("create conflict request: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict for second default gateway, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandlerRollback(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	// Apply config first
	applyBody := InterfaceConfig{
		Mode:       "static",
		IPAddress:  "192.168.1.188",
		SubnetMask: "255.255.255.0",
		Gateway:    "192.168.1.1",
		SetDefault: true,
	}
	bodyBytes, err := json.Marshal(applyBody)
	if err != nil {
		t.Fatalf("marshal apply body: %v", err)
	}
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/interfaces/eth0/apply",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		t.Fatalf("create apply request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("apply failed: %s", w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	raw, err := json.Marshal(resp.Data)
	if err != nil {
		t.Fatalf("marshal response data: %v", err)
	}
	var applyResp ApplyResponse
	if err := json.Unmarshal(raw, &applyResp); err != nil {
		t.Fatalf("unmarshal apply response: %v", err)
	}

	// Rollback with token
	rollbackReq, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/rollback?token="+applyResp.ConfirmToken,
		nil,
	)
	if err != nil {
		t.Fatalf("create rollback request: %v", err)
	}
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, rollbackReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for rollback, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandlerPing(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	pingBody := PingRequest{Target: "127.0.0.1"}
	bodyBytes, err := json.Marshal(pingBody)
	if err != nil {
		t.Fatalf("marshal ping body: %v", err)
	}
	req, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/ping",
		bytes.NewReader(bodyBytes),
	)
	if err != nil {
		t.Fatalf("create ping request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for ping, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Fatalf("expected code OK, got %s", resp.Code)
	}

	// Invalid ping target should return 422
	badPing := PingRequest{Target: "-c 2 127.0.0.1"}
	badBytes, err := json.Marshal(badPing)
	if err != nil {
		t.Fatalf("marshal bad ping body: %v", err)
	}
	req2, err := http.NewRequest(
		http.MethodPost,
		"/api/v1/system/network/ping",
		bytes.NewReader(badBytes),
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for invalid ping target, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandlerConfirmRollbackTokenRequired(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	// Confirm without token or session
	req, err := http.NewRequest(http.MethodPost, "/api/v1/system/network/confirm", nil)
	if err != nil {
		t.Fatalf("create confirm request: %v", err)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for confirm without token, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "TOKEN_REQUIRED" {
		t.Fatalf("expected code TOKEN_REQUIRED, got %s", resp.Code)
	}

	// Rollback without token or session
	req2, err := http.NewRequest(http.MethodPost, "/api/v1/system/network/rollback", nil)
	if err != nil {
		t.Fatalf("create rollback request: %v", err)
	}
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden for rollback without token, got %d: %s", w2.Code, w2.Body.String())
	}
}
