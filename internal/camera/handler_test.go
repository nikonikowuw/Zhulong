package camera

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

func setupTestCameraApp(t *testing.T) (*gin.Engine, *CameraService, *EventHub, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	_ = dbStore.OpenAndMigrate(context.Background())

	km := NewKeyManager(tempDir)
	cipher, _ := km.InitOrLoadKey(false)
	store := NewCameraStore(dbStore.DB)
	registry := NewStateRegistry()
	hub := NewEventHub(registry)

	prober := &mockProber{
		probeFunc: func(ctx context.Context, uri string, options engine.StreamOptions) (engine.VideoInfo, error) {
			return engine.VideoInfo{
				Codec:  engine.CodecH264,
				Width:  1920,
				Height: 1080,
				FPS:    engine.Rational{Num: 25, Den: 1},
			}, nil
		},
	}
	probeService := NewProbeService(prober)
	scheduler := NewHealthScheduler(store, cipher, registry, hub, nil, zap.NewNop())
	service := NewCameraService(store, cipher, probeService, registry, hub, scheduler, zap.NewNop())
	handler := NewHandler(service, hub, zap.NewNop())

	router := gin.New()
	apiGroup := router.Group("/api/v1")
	handler.RegisterRoutes(apiGroup)

	cleanup := func() {
		hub.Close()
		_ = dbStore.Close()
	}
	return router, service, hub, cleanup
}

func TestCameraHandlerCRUD(t *testing.T) {
	router, _, _, cleanup := setupTestCameraApp(t)
	defer cleanup()

	// 1. Create Camera
	createBody := `{
		"id": "cam_gate_test",
		"name": "Gate Camera",
		"mainStream": {
			"rtspUrl": "rtsp://admin:pass123@192.168.1.10:554/live/main",
			"transport": "tcp"
		}
	}`

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewBufferString(createBody))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("Create camera failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Code string          `json:"code"`
		Data *CameraResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}
	if resp.Code != "OK" || resp.Data == nil || resp.Data.ID != "cam_gate_test" {
		t.Fatalf("unexpected create response: %+v", resp)
	}

	// 2. Get Camera
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/cameras/cam_gate_test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Get camera failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store on camera detail, got: %q", w.Header().Get("Cache-Control"))
	}
	var getResp struct {
		Code string          `json:"code"`
		Data *CameraResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("Unmarshal get response failed: %v", err)
	}
	if getResp.Data == nil || len(getResp.Data.Streams) == 0 {
		t.Fatalf("expected streams in get response: %+v", getResp)
	}
	if !strings.Contains(getResp.Data.Streams[0].RTSPURL, "admin:pass123") {
		t.Fatalf("expected complete plaintext RTSP URL with credentials in API response, got: %s", getResp.Data.Streams[0].RTSPURL)
	}

	// 2b. List Cameras (must provide complete RTSP URL for frontend display)
	listReq, _ := http.NewRequest(http.MethodGet, "/api/v1/cameras", nil)
	listW := httptest.NewRecorder()
	router.ServeHTTP(listW, listReq)
	if listW.Code != http.StatusOK {
		t.Fatalf("List cameras failed: %d", listW.Code)
	}
	var listResp struct {
		Data struct {
			Items []CameraResponse `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listW.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("Unmarshal list response failed: %v", err)
	}
	if len(listResp.Data.Items) == 0 || len(listResp.Data.Items[0].Streams) == 0 {
		t.Fatal("expected items in list response")
	}
	if !strings.Contains(listResp.Data.Items[0].Streams[0].RTSPURL, "admin:pass123") {
		t.Fatalf("expected complete plaintext RTSP URL in list view, got: %s", listResp.Data.Items[0].Streams[0].RTSPURL)
	}

	// 3. Get Credentials (admin view)
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/cameras/cam_gate_test/credentials", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Get credentials failed: status=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store on credentials endpoint, got: %q", w.Header().Get("Cache-Control"))
	}
	var credResp struct {
		Data *CameraCredentialsResponse `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &credResp)
	if credResp.Data == nil || credResp.Data.Credentials["main"] == "" {
		t.Fatalf("expected plaintext credentials, got: %+v", credResp)
	}
	if !strings.Contains(credResp.Data.Credentials["main"], "admin:pass123") {
		t.Fatalf("expected plaintext password, got: %s", credResp.Data.Credentials["main"])
	}

	// 4. Update Camera with Revision CAS
	updateBody := `{
		"revision": 1,
		"name": "Gate Camera Renamed"
	}`
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/cameras/cam_gate_test", bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Update camera failed: status=%d body=%s", w.Code, w.Body.String())
	}

	// 5. Update with wrong revision -> 409 Conflict
	badUpdateBody := `{
		"revision": 1,
		"name": "Stale Update"
	}`
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/cameras/cam_gate_test", bytes.NewBufferString(badUpdateBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got: %d (%s)", w.Code, w.Body.String())
	}

	// 6. Delete Camera
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/cameras/cam_gate_test", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Delete camera failed: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCameraSSEEventsRouteNoCollision(t *testing.T) {
	router, _, _, cleanup := setupTestCameraApp(t)
	defer cleanup()

	// Ensure GET /api/v1/cameras/events is hit and initiates SSE without colliding with /:id
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/cameras/events", nil)
	w := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	go func() {
		// Cancel after short moment
		cancel()
	}()

	router.ServeHTTP(w, req)

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Fatalf("expected text/event-stream, got %q (code=%d, body=%s)", contentType, w.Code, w.Body.String())
	}

	bodyStr := w.Body.String()
	if !strings.Contains(bodyStr, "event: snapshot") {
		t.Fatalf("expected snapshot event in SSE body, got: %s", bodyStr)
	}
}
