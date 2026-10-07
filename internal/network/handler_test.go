package network

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	service := NewNetworkService(provider, watchdog, zap.NewNop())
	handler := NewHandler(service, zap.NewNop())

	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterProtectedRoutes(api)
	handler.RegisterPublicRoutes(api)

	return router, service, watchdog, provider
}

func TestHandlerListInterfaces(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/system/network/interfaces", nil)
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

	raw, _ := json.Marshal(resp.Data)
	var ifaces []InterfaceInfo
	if err := json.Unmarshal(raw, &ifaces); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	if len(ifaces) < 2 {
		t.Fatalf("expected at least 2 interfaces, got %d", len(ifaces))
	}

	hasCurrent := false
	for _, iface := range ifaces {
		if iface.IsCurrent {
			hasCurrent = true
		}
	}
	if !hasCurrent {
		t.Errorf("expected at least one interface to be marked current")
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
	bodyBytes, _ := json.Marshal(applyBody)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/interfaces/eth0/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	raw, _ := json.Marshal(resp.Data)
	var applyResp ApplyResponse
	_ = json.Unmarshal(raw, &applyResp)

	if applyResp.ConfirmToken == "" {
		t.Fatalf("expected confirm_token in response")
	}

	// Confirm via token endpoint
	confirmReq, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/confirm?token="+applyResp.ConfirmToken, nil)
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
	bodyBytes, _ := json.Marshal(badGateway)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/interfaces/eth0/apply", bytes.NewReader(bodyBytes))
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
	bodyBytes, _ = json.Marshal(conflict)
	req2, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/interfaces/eth1/apply", bytes.NewReader(bodyBytes))
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
	bodyBytes, _ := json.Marshal(applyBody)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/interfaces/eth0/apply", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("apply failed: %s", w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	raw, _ := json.Marshal(resp.Data)
	var applyResp ApplyResponse
	_ = json.Unmarshal(raw, &applyResp)

	// Rollback with token
	rollbackReq, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/rollback?token="+applyResp.ConfirmToken, nil)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, rollbackReq)

	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for rollback, got %d: %s", w2.Code, w2.Body.String())
	}
}

func TestHandlerPing(t *testing.T) {
	router, _, _, _ := setupTestNetworkHandler(t)

	pingBody := PingRequest{Target: "127.0.0.1"}
	bodyBytes, _ := json.Marshal(pingBody)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/system/network/ping", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for ping, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "OK" {
		t.Fatalf("expected code OK, got %s", resp.Code)
	}
}
