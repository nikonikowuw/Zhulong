package camera

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

func setupTestCameraApp(t *testing.T) (*gin.Engine, *CameraService, *EventHub, *mockMediaEngine, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	_ = dbStore.OpenAndMigrate(context.Background())

	km := NewKeyManager(tempDir)
	rawCipher, _ := km.InitOrLoadKey(false)
	lazyCipher := NewLazyCipher()
	lazyCipher.Set(rawCipher)
	store := NewCameraStore(dbStore)
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
	scheduler := NewHealthScheduler(store, lazyCipher, registry, hub, nil, zap.NewNop())
	service := NewCameraService(store, lazyCipher, probeService, registry, hub, scheduler, zap.NewNop())
	mockEngine := newMockMediaEngine()
	streamHub := NewStreamHub(mockEngine, store, lazyCipher, registry, hub, zap.NewNop())
	handler := NewHandler(service, hub, streamHub, zap.NewNop())

	router := gin.New()
	apiGroup := router.Group("/api/v1")
	handler.RegisterRoutes(apiGroup)

	cleanup := func() {
		_ = streamHub.Close()
		hub.Close()
		_ = dbStore.Close()
	}
	return router, service, hub, mockEngine, cleanup
}

func TestCameraHandlerCRUD(t *testing.T) {
	router, _, _, _, cleanup := setupTestCameraApp(t)
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
	router, _, _, _, cleanup := setupTestCameraApp(t)
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

func TestCameraHandlerStreamWS(t *testing.T) {
	router, _, _, mockEngine, cleanup := setupTestCameraApp(t)
	defer cleanup()

	// 1. 测试不存在的摄像机 -> HTTP 404
	req404, _ := http.NewRequest(http.MethodGet, "/api/v1/cameras/cam_not_found/ws", nil)
	w404 := httptest.NewRecorder()
	router.ServeHTTP(w404, req404)
	if w404.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent camera, got %d (body: %s)", w404.Code, w404.Body.String())
	}
	if !strings.Contains(w404.Body.String(), "CAMERA_NOT_FOUND") {
		t.Fatalf("expected CAMERA_NOT_FOUND, got: %s", w404.Body.String())
	}

	// 2. 创建有效摄像机
	createBody := `{
		"id": "cam_ws_play",
		"name": "WS Test Camera",
		"mainStream": {
			"rtspUrl": "rtsp://admin:pass@127.0.0.1:554/live/main",
			"transport": "tcp"
		}
	}`
	createReq, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewBufferString(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	wCreate := httptest.NewRecorder()
	router.ServeHTTP(wCreate, createReq)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create camera failed: %d (body: %s)", wCreate.Code, wCreate.Body.String())
	}

	// 3. 启动 HTTP Server 并测试 WebSocket 实时推流
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/api/v1/cameras/cam_ws_play/ws"
	wsConn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial failed: %v (resp status: %v)", err, resp)
	}
	defer wsConn.Close()

	// 模拟 Native 产生一帧关键帧包
	mockEngine.mu.Lock()
	if len(mockEngine.streams) == 0 {
		t.Fatalf("expected mock media stream created")
	}
	activeStream := mockEngine.streams[0]
	mockEngine.mu.Unlock()

	testPkt := engine.Packet{
		Codec:    engine.CodecH264,
		PTS:      90000,
		DTS:      90000,
		HasPTS:   true,
		HasDTS:   true,
		KeyFrame: true,
		Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1f},
	}
	activeStream.source.packets <- testPkt

	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, msgData, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected binary message, got %d", msgType)
	}

	header, payload, err := UnpackPacket(msgData)
	if err != nil {
		t.Fatalf("unpack packet failed: %v", err)
	}
	if header.Codec != WireCodecH264 {
		t.Fatalf("expected H264 codec, got %d", header.Codec)
	}
	if !header.IsKeyFrame() {
		t.Fatalf("expected key frame flag")
	}
	if header.PTS != 90000 {
		t.Fatalf("expected PTS 90000, got %d", header.PTS)
	}
	if !bytes.Equal(payload, testPkt.Data) {
		t.Fatalf("payload mismatch")
	}
}

type testCameraAuditor struct {
	entries []audit.Entry
}

func (m *testCameraAuditor) Record(entry audit.Entry) {
	m.entries = append(m.entries, entry)
}

func TestCameraHandler_AuditLogging(t *testing.T) {
	auditor := &testCameraAuditor{}
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	_ = dbStore.OpenAndMigrate(context.Background())
	defer dbStore.Close()

	km := NewKeyManager(tempDir)
	rawCipher, _ := km.InitOrLoadKey(false)
	lazyCipher := NewLazyCipher()
	lazyCipher.Set(rawCipher)
	store := NewCameraStore(dbStore)
	registry := NewStateRegistry()
	hub := NewEventHub(registry)
	defer hub.Close()

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
	scheduler := NewHealthScheduler(store, lazyCipher, registry, hub, nil, zap.NewNop())
	service := NewCameraService(store, lazyCipher, probeService, registry, hub, scheduler, zap.NewNop())
	mockEngine := newMockMediaEngine()
	streamHub := NewStreamHub(mockEngine, store, lazyCipher, registry, hub, zap.NewNop())
	defer streamHub.Close()

	handler := NewHandler(service, hub, streamHub, zap.NewNop())
	handler.SetAuditor(auditor)

	r := gin.New()
	api := r.Group("/api/v1")
	handler.RegisterRoutes(api)

	// 1. Create camera with password in RTSP
	createReq := CreateCameraRequest{
		ID:   "cam_audit_1",
		Name: "Front Gate",
		MainStream: CreateStreamRequest{
			Role:      "main",
			Protocol:  "rtsp",
			RTSPURL:   "rtsp://admin:secret123@192.168.1.100:554/h264",
			Transport: "tcp",
		},
	}
	body, _ := json.Marshal(createReq)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// 2. Toggle camera enabled
	disabled := false
	toggleReq := UpdateCameraRequest{
		Revision: 1,
		Enabled:  &disabled,
	}
	body, _ = json.Marshal(toggleReq)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/cameras/cam_audit_1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 3. Update camera name
	newName := "Front Gate Updated"
	updateReq := UpdateCameraRequest{
		Revision: 2,
		Name:     &newName,
	}
	body, _ = json.Marshal(updateReq)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/cameras/cam_audit_1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// 4. Delete camera
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/cameras/cam_audit_1", nil)
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Assertions on recorded audits
	if len(auditor.entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(auditor.entries))
	}
	if auditor.entries[0].Action != audit.ActionCameraCreate || auditor.entries[0].Status != audit.StatusSuccess {
		t.Errorf("entry 0 mismatch: %+v", auditor.entries[0])
	}
	// Password must NOT be present in detail
	if strings.Contains(auditor.entries[0].Detail, "secret123") {
		t.Fatalf("plain password found in audit detail: %s", auditor.entries[0].Detail)
	}
	if auditor.entries[1].Action != audit.ActionCameraToggle || auditor.entries[1].Status != audit.StatusSuccess {
		t.Errorf("entry 1 mismatch: %+v", auditor.entries[1])
	}
	if auditor.entries[2].Action != audit.ActionCameraUpdate || auditor.entries[2].Status != audit.StatusSuccess {
		t.Errorf("entry 2 mismatch: %+v", auditor.entries[2])
	}
	if auditor.entries[3].Action != audit.ActionCameraDelete || auditor.entries[3].Status != audit.StatusSuccess {
		t.Errorf("entry 3 mismatch: %+v", auditor.entries[3])
	}
}
