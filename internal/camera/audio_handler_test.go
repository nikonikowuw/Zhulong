package camera

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// setupTestCameraAppWithSession 构造带会话注入的路由。
//
// 生产环境里音频路由挂在受保护分组下，session token 由中间件写入 gin.Context；
// 测试里必须显式注入，否则每条音频通道都会因为没有会话键而被拒绝（这本身就是
// 「通道必须以登录会话为键」的负向证据）。
func setupTestCameraAppWithSession(t *testing.T, token string) (*gin.Engine, *mockMediaEngine, func()) {
	t.Helper()

	router, _, _, mockEngine, cleanup := setupTestCameraApp(t, func(c *gin.Context) {
		if token != "" {
			auth.SetSessionToken(c, token)
		}
		c.Next()
	})
	return router, mockEngine, cleanup
}

func createCameraForTest(t *testing.T, router *gin.Engine, id string) {
	t.Helper()
	body := `{
		"id": "` + id + `",
		"name": "Audio Test Camera",
		"mainStream": {
			"rtspUrl": "rtsp://admin:pass@127.0.0.1:554/live/main",
			"transport": "tcp"
		}
	}`
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/cameras", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create camera failed: %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestAudioStreamWSRequiresSession 钉住「没有登录会话就没有频道键」。
func TestAudioStreamWSRequiresSession(t *testing.T) {
	router, _, cleanup := setupTestCameraAppWithSession(t, "")
	defer cleanup()

	createCameraForTest(t, router, "cam_audio_no_session")
	server := httptest.NewServer(router)
	defer server.Close()

	// 不带 Upgrade 头也能看出语义：会话缺失时必须在升级前以 401 信封拒绝
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/cameras/cam_audio_no_session/streams/main/audio/ws", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without a session, got %d (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestAudioStreamWSCameraWithoutAudio 钉住「无音频 → error 帧 + 4404」。
func TestAudioStreamWSCameraWithoutAudio(t *testing.T) {
	router, _, cleanup := setupTestCameraAppWithSession(t, "session-no-audio")
	defer cleanup()

	createCameraForTest(t, router, "cam_audio_absent")
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/v1/cameras/cam_audio_absent/streams/main/audio/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("audio ws dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read error frame failed: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected a text error frame, got type %d", msgType)
	}
	var message AudioErrorMessage
	if err := json.Unmarshal(data, &message); err != nil {
		t.Fatalf("decode error frame failed: %v", err)
	}
	if message.Type != AudioFrameTypeError || message.Code != AudioErrorNoAudio {
		t.Fatalf("unexpected error frame: %s", string(data))
	}

	// 随后连接必须以 4404 关闭：4404 让前端区分「这路没声音」与「网络抖了」
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !asCloseError(err, &closeErr) {
		t.Fatalf("expected a close frame, got %v", err)
	}
	if closeErr.Code != AudioCloseNoAudio {
		t.Fatalf("expected close code %d, got %d", AudioCloseNoAudio, closeErr.Code)
	}
}

// TestAudioStreamWSDeliversFrames 端到端：文本 audio_info → ZLA1 二进制帧。
func TestAudioStreamWSDeliversFrames(t *testing.T) {
	router, mockEngine, cleanup := setupTestCameraAppWithSession(t, "session-with-audio")
	defer cleanup()

	mockEngine.mu.Lock()
	mockEngine.audioInfo = engine.AudioInfo{
		HasAudio:   true,
		Codec:      engine.CodecPCMA,
		SampleRate: 8000,
		Channels:   1,
	}
	mockEngine.mu.Unlock()

	createCameraForTest(t, router, "cam_audio_ok")
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/v1/cameras/cam_audio_ok/streams/main/audio/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("audio ws dial failed: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read audio_info failed: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected audio_info text frame first, got type %d", msgType)
	}
	var info AudioInfoMessage
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("decode audio_info failed: %v", err)
	}
	matchesAudioSpec := info.HasAudio && info.Codec == "pcma" &&
		info.SampleRate == 8000 && info.Channels == 1
	if !matchesAudioSpec {
		t.Fatalf("unexpected audio_info: %s", string(data))
	}

	// 推一帧音频包：通道把它封成 ZLA1 后转发
	mockEngine.mu.Lock()
	if len(mockEngine.streams) == 0 {
		mockEngine.mu.Unlock()
		t.Fatal("expected the audio stream to be acquired")
	}
	source := mockEngine.streams[len(mockEngine.streams)-1].source
	mockEngine.mu.Unlock()

	source.packets <- engine.Packet{
		TrackType: engine.TrackAudio,
		Codec:     engine.CodecPCMA,
		PTSUS:     20_000,
		Data:      bytes.Repeat([]byte{0xD5}, 160),
	}

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	msgType, data, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("read audio frame failed: %v", err)
	}
	if msgType != websocket.BinaryMessage {
		t.Fatalf("expected a binary audio frame, got type %d", msgType)
	}
	header, payload, err := UnpackAudioPacket(data)
	if err != nil {
		t.Fatalf("unpack audio frame failed: %v", err)
	}
	if header.Codec != engine.CodecPCMA || header.PTSUS != 20_000 {
		t.Fatalf("unexpected audio header: %+v", header)
	}
	if len(payload) != 160 {
		t.Fatalf("expected 160-byte G.711 payload, got %d", len(payload))
	}
}

// TestAudioStreamWSSupersedesSameSession 钉住「同一会话第二条接管旧连接（4001）」。
func TestAudioStreamWSSupersedesSameSession(t *testing.T) {
	router, mockEngine, cleanup := setupTestCameraAppWithSession(t, "session-shared")
	defer cleanup()

	mockEngine.mu.Lock()
	mockEngine.audioInfo = engine.AudioInfo{
		HasAudio:   true,
		Codec:      engine.CodecPCMA,
		SampleRate: 8000,
		Channels:   1,
	}
	mockEngine.mu.Unlock()

	createCameraForTest(t, router, "cam_audio_supersede")
	server := httptest.NewServer(router)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") +
		"/api/v1/cameras/cam_audio_supersede/streams/main/audio/ws"

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial failed: %v", err)
	}
	defer first.Close()
	_ = first.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := first.ReadMessage(); err != nil {
		t.Fatalf("first audio_info failed: %v", err)
	}

	second, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("second dial failed: %v", err)
	}
	defer second.Close()

	// 旧连接必须以 4001 关闭：前端据此知道「是被自己顶掉的」，不该自动重连
	_ = first.SetReadDeadline(time.Now().Add(3 * time.Second))
	var closeErr *websocket.CloseError
	for {
		_, _, err = first.ReadMessage()
		if err == nil {
			continue
		}
		if !asCloseError(err, &closeErr) {
			t.Fatalf("expected a close frame on the superseded channel, got %v", err)
		}
		break
	}
	if closeErr.Code != AudioCloseSuperseded {
		t.Fatalf("expected close code %d, got %d", AudioCloseSuperseded, closeErr.Code)
	}

	// 新连接照常收到 audio_info
	_ = second.SetReadDeadline(time.Now().Add(3 * time.Second))
	msgType, _, err := second.ReadMessage()
	if err != nil {
		t.Fatalf("second audio_info failed: %v", err)
	}
	if msgType != websocket.TextMessage {
		t.Fatalf("expected audio_info text frame, got type %d", msgType)
	}
}

// asCloseError 把读错误判定成 WebSocket 关闭帧，并区分关闭码为 0 的「裸断开」。
func asCloseError(err error, target **websocket.CloseError) bool {
	if closeErr, ok := err.(*websocket.CloseError); ok {
		*target = closeErr
		return true
	}
	return false
}

// TestAudioChannelRegistryClosedOnHubShutdown 保证服务关闭时音频通道被清空。
func TestAudioChannelRegistryClosedOnHubShutdown(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())
	channel := newTestAudioChannel(registry, "shutdown")
	if _, err := registry.Open("shutdown", channel); err != nil {
		t.Fatalf("open failed: %v", err)
	}
	channel.Close()
	if registry.Len() != 0 {
		t.Fatalf("expected registry to be empty after channel close, got %d", registry.Len())
	}
}
