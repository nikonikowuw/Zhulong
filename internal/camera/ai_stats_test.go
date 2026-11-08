package camera

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// TestAIStreamStatsHandler pins the two behaviours an operator actually depends on:
// a camera without an active AI stream must say so explicitly (not report "0 clients",
// which is indistinguishable from "nobody is subscribed"), and the counters must be
// mapped field-for-field into the response envelope.
func TestAIStreamStatsHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)

	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, nil, zap.NewNop())
	defer func() { _ = hub.Close() }()

	registry := NewStateRegistry()
	eventHub := NewEventHub(registry)
	defer eventHub.Close()
	service := NewCameraService(store, lazyCipher, NewProbeService(&mockProber{}), registry, eventHub, nil, zap.NewNop())
	handler := NewHandler(service, eventHub, hub, zap.NewNop())

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	path := "/api/v1/cameras/" + camID + "/ai-stats"

	// 1. 没有活跃 AI 流：如实 404 + 稳定机器码，不静默返回空列表。
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("inactive AI stream: want 404, got %d (%s)", w.Code, w.Body.String())
	}
	var inactive struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &inactive); err != nil {
		t.Fatalf("decode inactive response: %v", err)
	}
	if inactive.Code != "AI_STREAM_INACTIVE" {
		t.Fatalf("want code AI_STREAM_INACTIVE, got %q", inactive.Code)
	}

	// 2. 拉起 AI 流并注入两个算法包的统计。
	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve AI stream: %v", err)
	}
	if len(mockEngine.streams) != 1 {
		t.Fatalf("want 1 acquired stream, got %d", len(mockEngine.streams))
	}
	stream := mockEngine.streams[0]
	stream.mu.Lock()
	stream.brokerClients = []engine.BrokerClientStats{
		{ClientID: 1, Name: "face_recognition", InFlight: 2, MaxInFlight: 2, DeliveredFrames: 1500, DroppedFrames: 37},
		{ClientID: 2, Name: "plate_recognition", InFlight: 0, MaxInFlight: 2, DeliveredFrames: 1500, DroppedFrames: 0},
	}
	stream.brokerTotal = 17 // 大于返回条数：证明截断信息被透传
	stream.mu.Unlock()

	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("active AI stream: want 200, got %d (%s)", w.Code, w.Body.String())
	}

	var envelope struct {
		Code string                `json:"code"`
		Data AIStreamStatsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode stats response: %v", err)
	}
	if envelope.Code != "OK" {
		t.Fatalf("want envelope code OK, got %q", envelope.Code)
	}
	if envelope.Data.CameraID != camID {
		t.Fatalf("cameraId mismatch: %q vs %q", envelope.Data.CameraID, camID)
	}
	if envelope.Data.ClientCount != 17 {
		t.Fatalf("clientCount must carry the true total (truncation signal), got %d", envelope.Data.ClientCount)
	}
	if len(envelope.Data.Clients) != 2 {
		t.Fatalf("want 2 clients, got %d", len(envelope.Data.Clients))
	}
	first := envelope.Data.Clients[0]
	hasExpectedMeta := first.ClientID == 1 && first.Name == "face_recognition"
	hasExpectedInFlight := first.InFlight == 2 && first.MaxInFlight == 2
	hasExpectedCounters := first.DeliveredFrames == 1500 && first.DroppedFrames == 37
	if !hasExpectedMeta || !hasExpectedInFlight || !hasExpectedCounters {
		t.Fatalf("first client counters not mapped 1:1: %+v", first)
	}
}
