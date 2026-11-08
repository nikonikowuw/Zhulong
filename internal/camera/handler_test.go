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
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

func setupTestCameraApp(
	t *testing.T,
	middlewares ...gin.HandlerFunc,
) (*gin.Engine, *CameraService, *EventHub, *mockMediaEngine, func()) {
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
	// 中间件必须在注册路由**之前**挂上：gin 只把 Use 之后的中间件编进路由链，
	// 顺序反了会得到一个「测试里认证永远缺失」的假象。
	router.Use(middlewares...)
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
		Data *CameraResponse `json:"data"`
		Code string          `json:"code"`
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
		Data *CameraResponse `json:"data"`
		Code string          `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("Unmarshal get response failed: %v", err)
	}
	if getResp.Data == nil || len(getResp.Data.Streams) == 0 {
		t.Fatalf("expected streams in get response: %+v", getResp)
	}
	if !strings.Contains(getResp.Data.Streams[0].RTSPURL, "admin:pass123") {
		t.Fatalf(
			"expected complete plaintext RTSP URL with credentials in API response, got: %s",
			getResp.Data.Streams[0].RTSPURL,
		)
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
		Codec:      engine.CodecH264,
		PTS:        90000,
		DTS:        90000,
		HasPTS:     true,
		HasDTS:     true,
		KeyFrame:   true,
		PtsUS:      1000000,
		DtsUS:      1000000,
		FrameClass: engine.FrameIDR,
		GOPID:      1,
		Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42, 0x00, 0x1f},
	}
	activeStream.source.packets <- testPkt

	// 3.1 建连后第一条必须是文本能力帧，且只发一次。
	//     老前端按 `instanceof ArrayBuffer` 判据忽略它，所以它绝不能出现在二进制帧之后
	//     ——那会让旧 bundle 把文本当帧解析，直接黑屏。
	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, msgData, err := wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read capability frame failed: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected the capability text frame first, got message type %d", msgType)
	}
	var capability StreamInfoMessage
	if err := json.Unmarshal(msgData, &capability); err != nil {
		t.Fatalf("decode capability frame failed: %v", err)
	}
	if capability.Type != StreamFrameTypeInfo || capability.Version != int(WireProtocolVersion) {
		t.Fatalf("unexpected capability frame: %s", string(msgData))
	}
	if capability.CameraID != "cam_ws_play" || capability.Role != "main" {
		t.Fatalf("capability frame must address the same stream, got %+v", capability)
	}
	// 这个 mock 相机没有音频，能力帧必须如实说"没有"，而不是让前端去猜
	if capability.Audio.Present {
		t.Fatalf("expected Present=false for a video-only stream, got %+v", capability.Audio)
	}

	_ = wsConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, msgData, err = wsConn.ReadMessage()
	if err != nil {
		t.Fatalf("read ws message failed: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected binary message, got %d", msgType)
	}

	// 3.2 二进制帧逐字节黄金对比：头 32 字节是写死的期望值，不调用 PackPacket 反推自己。
	expectedHeader := []byte{
		0x5A, 0x4C, 0x4D, 0x32, // "ZLM2"
		0x02,                            // version 2
		WireCodecH264,                   // codec
		WireFrameIDR,                    // frame_class
		WireFlagKeyFrame,                // flags
		0, 0, 0, 0, 0, 0x0F, 0x42, 0x40, // pts_us = 1_000_000
		0, 0, 0, 0, 0, 0x0F, 0x42, 0x40, // dts_us = 1_000_000
		0, 0, 0, 0, // seq（流级帧序号，首帧为 0）
		0, 0, 0, 1, // gop_id = 1
	}
	expected := append(expectedHeader, testPkt.Data...)
	if !bytes.Equal(msgData, expected) {
		t.Fatalf("binary frame changed byte-for-byte:\n got %x\nwant %x", msgData, expected)
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
	if header.PTSUS != 1000000 {
		t.Fatalf("expected pts_us 1000000, got %d", header.PTSUS)
	}
	if header.FrameClass != WireFrameIDR {
		t.Fatalf("expected IDR frame class, got %d", header.FrameClass)
	}
	if header.GOPID != 1 {
		t.Fatalf("expected gop id 1, got %d", header.GOPID)
	}
	if !bytes.Equal(payload, testPkt.Data) {
		t.Fatalf("payload mismatch")
	}
}

func TestCameraHandler_AuditLogging(t *testing.T) {
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

	// Wire the real audit middleware: the handler no longer records directly.
	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}

	r := gin.New()
	api := r.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
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

	// Drain the asynchronous queue, then assert on the persisted trail.
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	entries, total, err := auditSvc.List(context.Background(), audit.Filter{Limit: 20})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected 4 entries, got %d", total)
	}

	// Entries come back newest first; index by action so the assertions do not
	// depend on ordering. Toggle and update share one route, so a missing
	// TagAction override would collapse them into a single action.
	byAction := make(map[string]audit.AuditLog, len(entries))
	for _, e := range entries {
		byAction[e.Action] = e
	}

	createEntry, ok := byAction[audit.ActionCameraCreate]
	if !ok || createEntry.Status != audit.StatusSuccess {
		t.Errorf("camera.create mismatch: %+v", createEntry)
	}
	// The RTSP credential must never appear in the audit detail.
	if strings.Contains(createEntry.Detail, "secret123") {
		t.Fatalf("plain password found in audit detail: %s", createEntry.Detail)
	}
	if _, ok := byAction[audit.ActionCameraToggle]; !ok {
		t.Errorf("expected a distinct camera.toggle entry; got %+v", entries)
	}
	if _, ok := byAction[audit.ActionCameraUpdate]; !ok {
		t.Errorf("expected a distinct camera.update entry; got %+v", entries)
	}
	if deleteEntry, ok := byAction[audit.ActionCameraDelete]; !ok || deleteEntry.Status != audit.StatusSuccess {
		t.Errorf("camera.delete mismatch: %+v", deleteEntry)
	}
}

func TestCameraHandlerCreate_GeneratedULID(t *testing.T) {
	router, _, _, _, cleanup := setupTestCameraApp(t)
	defer cleanup()

	// 1. Create first camera without ID field
	createBody1 := `{
		"name": "Auto Camera 1",
		"mainStream": {
			"rtspUrl": "rtsp://admin:pass123@192.168.1.11:554/live/main",
			"transport": "tcp"
		}
	}`

	req1, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewBufferString(createBody1))
	req1.Header.Set("Content-Type", "application/json")
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)

	if w1.Code != http.StatusCreated {
		t.Fatalf("Create camera 1 failed: status=%d body=%s", w1.Code, w1.Body.String())
	}

	var resp1 struct {
		Data *CameraResponse `json:"data"`
		Code string          `json:"code"`
	}
	if err := json.Unmarshal(w1.Body.Bytes(), &resp1); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}
	if resp1.Data == nil || !idutil.IsValid(resp1.Data.ID) {
		t.Fatalf("expected camera 1 to have valid ULID, got %+v", resp1.Data)
	}

	// 2. Create second camera with empty string ID
	createBody2 := `{
		"id": "",
		"name": "Auto Camera 2",
		"mainStream": {
			"rtspUrl": "rtsp://admin:pass123@192.168.1.12:554/live/main",
			"transport": "tcp"
		}
	}`

	req2, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewBufferString(createBody2))
	req2.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)

	if w2.Code != http.StatusCreated {
		t.Fatalf("Create camera 2 failed: status=%d body=%s", w2.Code, w2.Body.String())
	}

	var resp2 struct {
		Data *CameraResponse `json:"data"`
		Code string          `json:"code"`
	}
	if err := json.Unmarshal(w2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("Unmarshal response failed: %v", err)
	}
	if resp2.Data == nil || !idutil.IsValid(resp2.Data.ID) {
		t.Fatalf("expected camera 2 to have valid ULID, got %+v", resp2.Data)
	}

	if resp1.Data.ID == resp2.Data.ID {
		t.Fatalf("expected unique camera IDs, but got duplicates: %s", resp1.Data.ID)
	}
	if resp1.Data.ID >= resp2.Data.ID {
		t.Fatalf("expected camera IDs to be monotonic (%s < %s)", resp1.Data.ID, resp2.Data.ID)
	}
}

func TestCreateGB28181Camera(t *testing.T) {
	_, service, _, _, cleanup := setupTestCameraApp(t)
	defer cleanup()

	ctx := context.Background()

	// 1. Invalid params (non-20-digit)
	_, err := service.CreateGB28181Camera(ctx, CreateGB28181CameraParams{
		DeviceID:  "123",
		ChannelID: "34020000001310000001",
	})
	if err == nil {
		t.Fatalf("expected error for invalid device ID")
	}

	// 2. Valid GB28181 camera creation
	cam, err := service.CreateGB28181Camera(ctx, CreateGB28181CameraParams{
		DeviceID:         "34020000001180000001",
		ChannelID:        "34020000001310000001",
		Name:             "GB Channel 1",
		ResolutionWidth:  1920,
		ResolutionHeight: 1080,
	})
	if err != nil {
		t.Fatalf("failed to create gb28181 camera: %v", err)
	}

	if cam.Name != "GB Channel 1" {
		t.Errorf("expected name 'GB Channel 1', got %q", cam.Name)
	}
	if len(cam.Streams) != 1 {
		t.Fatalf("expected 1 stream, got %d", len(cam.Streams))
	}
	mainStream := cam.Streams[0]
	if mainStream.Protocol != ProtocolGB28181 {
		t.Errorf("expected protocol gb28181, got %q", mainStream.Protocol)
	}
	expectedURI := "gb28181://34020000001180000001/34020000001310000001"
	if mainStream.RTSPURL != expectedURI {
		t.Errorf("expected URI %q, got %q", expectedURI, mainStream.RTSPURL)
	}
	if mainStream.Width != 1920 || mainStream.Height != 1080 {
		t.Errorf("expected 1920x1080, got %dx%d", mainStream.Width, mainStream.Height)
	}

	// No RTSP probe ran for this protocol, so the state must stay "unknown": claiming
	// online evidence here would be a lie the GB input (S4) cannot back yet.
	state, ok := service.registry.GetState(cam.ID)
	if !ok {
		t.Fatalf("expected the created camera to be registered")
	}
	if state.Streams[StreamRoleMain].EvidenceType != EvidenceNone {
		t.Errorf("expected no probe evidence, got %q", state.Streams[StreamRoleMain].EvidenceType)
	}

	// An unnamed channel falls back to its channel ID instead of an empty camera name.
	unnamed, err := service.CreateGB28181Camera(ctx, CreateGB28181CameraParams{
		DeviceID:  "34020000001180000001",
		ChannelID: "34020000001310000009",
	})
	if err != nil {
		t.Fatalf("create unnamed gb28181 camera: %v", err)
	}
	if unnamed.Name != "34020000001310000009" {
		t.Errorf("expected the channel ID as the fallback name, got %q", unnamed.Name)
	}

	// 3. Verify camera is retrievable
	fetched, err := service.Get(ctx, cam.ID)
	if err != nil {
		t.Fatalf("failed to get camera: %v", err)
	}
	if fetched.Streams[0].Protocol != ProtocolGB28181 {
		t.Errorf("expected fetched protocol gb28181, got %q", fetched.Streams[0].Protocol)
	}
}
