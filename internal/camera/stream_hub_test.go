package camera

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

type mockPacketSource struct {
	packets chan engine.Packet
	closed  atomic.Bool
}

func newMockPacketSource(buffer int) *mockPacketSource {
	return &mockPacketSource{
		packets: make(chan engine.Packet, buffer),
	}
}

func (s *mockPacketSource) Next(ctx context.Context) (engine.Packet, error) {
	select {
	case pkt, ok := <-s.packets:
		if !ok {
			return engine.Packet{}, errors.New("eof")
		}
		return pkt, nil
	case <-ctx.Done():
		return engine.Packet{}, ctx.Err()
	}
}

func (s *mockPacketSource) Close() error {
	if s.closed.CompareAndSwap(false, true) {
		close(s.packets)
	}
	return nil
}

type mockMediaStream struct {
	source    *mockPacketSource
	closeOnce sync.Once
	closed    bool
	mu        sync.Mutex
}

func (s *mockMediaStream) Subscribe(ctx context.Context, options engine.SubscriptionOptions) (PacketSource, error) {
	return s.source, nil
}

func (s *mockMediaStream) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		_ = s.source.Close()
	})
	return nil
}

type mockMediaEngine struct {
	mu           sync.Mutex
	acquireCount int
	streams      []*mockMediaStream
}

func newMockMediaEngine() *mockMediaEngine {
	return &mockMediaEngine{}
}

func (e *mockMediaEngine) AcquireStream(
	ctx context.Context,
	uri string,
	consumerID uint64,
	kind engine.ConsumerKind,
	options engine.StreamOptions,
) (MediaStream, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acquireCount++

	stream := &mockMediaStream{source: newMockPacketSource(16)}
	e.streams = append(e.streams, stream)
	return stream, nil
}

func (e *mockMediaEngine) getAcquireCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.acquireCount
}

func setupTestStoreAndCipher(t *testing.T) (CameraStore, *LazyCipher, string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := NewCameraStore(dbStore.DB)
	cleanup := func() {
		_ = dbStore.Close()
	}

	rawCipher, err := NewCipher(make([]byte, 32))
	if err != nil {
		t.Fatalf("new cipher: %v", err)
	}
	lazy := NewLazyCipher()
	lazy.Set(rawCipher)

	camID := "cam_hub_test"
	uri := "rtsp://admin:pass@127.0.0.1:554/live/main"
	encURI, err := lazy.Encrypt([]byte(uri), MakeAAD(camID, "main"))
	if err != nil {
		t.Fatalf("encrypt uri: %v", err)
	}

	cam := &Camera{
		ID:       camID,
		Name:     "Hub Test Camera",
		Enabled:  true,
		Revision: 1,
	}
	streams := []CameraStream{
		{
			Role:         "main",
			Protocol:     "rtsp",
			EncryptedURI: encURI,
			Transport:    "tcp",
			Codec:        "h264",
			Width:        1920,
			Height:       1080,
		},
	}
	if err := store.Create(context.Background(), cam, streams); err != nil {
		t.Fatalf("create camera: %v", err)
	}

	return store, lazy, camID, cleanup
}

func TestStreamHubOnDemandAcquireAndRelease(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	registry := NewStateRegistry()
	registry.InitCameraState(&Camera{
		ID:       camID,
		Name:     "Hub Test Camera",
		Enabled:  true,
		Revision: 1,
		Streams:  []CameraStream{{Role: "main"}},
	})
	logger := zap.NewNop()

	hub := NewStreamHub(mockEngine, store, cipher, registry, nil, logger)
	defer hub.Close()

	if hub.ActiveStreams() != 0 {
		t.Fatalf("expected 0 active streams, got %d", hub.ActiveStreams())
	}

	// 1. Client A 加入 -> 触发拉流
	disp, err := hub.GetOrCreateDispatcher(context.Background(), camID, "main")
	if err != nil {
		t.Fatalf("get or create dispatcher failed: %v", err)
	}
	if mockEngine.getAcquireCount() != 1 {
		t.Fatalf("expected 1 acquire call, got %d", mockEngine.getAcquireCount())
	}
	if hub.ActiveStreams() != 1 {
		t.Fatalf("expected 1 active stream, got %d", hub.ActiveStreams())
	}

	clientA := &StreamClient{
		sendChan:   make(chan []byte, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     logger,
	}
	disp.RegisterClient(clientA)

	// 验证状态机切换为 running
	info, ok := registry.GetState(camID)
	if !ok || info == nil || info.Streams["main"] == nil || info.Streams["main"].Session != SessionStateRunning {
		t.Fatalf("expected SessionStateRunning, got %v", info)
	}

	// 2. Client B 加入 -> 复用相同分发器，不额外调用 AcquireStream
	disp2, err := hub.GetOrCreateDispatcher(context.Background(), camID, "main")
	if err != nil {
		t.Fatalf("second get dispatcher failed: %v", err)
	}
	if disp2 != disp {
		t.Fatalf("expected same dispatcher instance")
	}
	if mockEngine.getAcquireCount() != 1 {
		t.Fatalf("expected still 1 acquire call, got %d", mockEngine.getAcquireCount())
	}

	clientB := &StreamClient{
		sendChan:   make(chan []byte, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     logger,
	}
	disp.RegisterClient(clientB)

	if disp.SubscriberCount() != 2 {
		t.Fatalf("expected 2 subscribers, got %d", disp.SubscriberCount())
	}

	// 3. Client A 断开 -> 仍剩 Client B，不释放底层流
	disp.UnregisterClient(clientA)
	if disp.SubscriberCount() != 1 {
		t.Fatalf("expected 1 subscriber, got %d", disp.SubscriberCount())
	}
	if hub.ActiveStreams() != 1 {
		t.Fatalf("expected 1 active stream, got %d", hub.ActiveStreams())
	}

	// 4. Client B 断开 -> 订阅者清零，自动关闭并释放底层流
	disp.UnregisterClient(clientB)
	if hub.ActiveStreams() != 0 {
		t.Fatalf("expected 0 active streams after all unsubscribed, got %d", hub.ActiveStreams())
	}

	// 验证状态机切为 idle
	info, ok = registry.GetState(camID)
	if !ok || info == nil || info.Streams["main"] == nil || info.Streams["main"].Session != SessionStateIdle {
		t.Fatalf("expected SessionStateIdle, got %v", info)
	}
}

func TestStreamDispatcherGOPCacheInstantPlayback(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	// 1. 发送一帧关键帧
	keyPkt := engine.Packet{
		Codec:    engine.CodecH264,
		PTS:      1000,
		DTS:      1000,
		HasPTS:   true,
		HasDTS:   true,
		KeyFrame: true,
		Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42},
	}
	disp.Broadcast(keyPkt)

	// 2. 发送一帧非关键帧
	pPkt := engine.Packet{
		Codec:    engine.CodecH264,
		PTS:      2000,
		DTS:      2000,
		HasPTS:   true,
		HasDTS:   true,
		KeyFrame: false,
		Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x41},
	}
	disp.Broadcast(pPkt)

	// 3. 新客户端连入
	client := &StreamClient{
		sendChan:   make(chan []byte, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	// 4. 验证新客户端立即收到缓存的关键帧
	select {
	case frame := <-client.sendChan:
		header, payload, err := UnpackPacket(frame)
		if err != nil {
			t.Fatalf("unpack cached frame failed: %v", err)
		}
		if !header.IsKeyFrame() {
			t.Fatalf("expected first frame to be keyframe, got non-key")
		}
		if header.PTS != 1000 {
			t.Fatalf("expected PTS 1000, got %d", header.PTS)
		}
		if len(payload) != len(keyPkt.Data) {
			t.Fatalf("expected payload len %d, got %d", len(keyPkt.Data), len(payload))
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for cached keyframe")
	}
}

func TestStreamDispatcherBackpressureSlowConsumer(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	// 创建一个发送缓冲极小（容量 1）的客户端
	client := &StreamClient{
		sendChan:   make(chan []byte, 1),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	// 填满客户端缓冲
	client.sendChan <- []byte("stalled")

	// 1. 广播非关键帧 -> 不阻塞，自动丢弃
	nonKeyPkt := engine.Packet{
		Codec:    engine.CodecH264,
		KeyFrame: false,
		Data:     []byte{0x01},
	}
	doneCh := make(chan struct{})
	go func() {
		disp.Broadcast(nonKeyPkt)
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// Broadcast 未被慢客户端阻塞
	case <-time.After(time.Second):
		t.Fatalf("broadcast blocked by slow consumer on non-keyframe")
	}
}

func TestStreamHubShutdown(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	registry := NewStateRegistry()
	registry.InitCameraState(&Camera{
		ID:       camID,
		Name:     "Hub Test Camera",
		Enabled:  true,
		Revision: 1,
		Streams:  []CameraStream{{Role: "main"}},
	})
	hub := NewStreamHub(mockEngine, store, cipher, registry, nil, zap.NewNop())

	disp, err := hub.GetOrCreateDispatcher(context.Background(), camID, "main")
	if err != nil {
		t.Fatalf("create dispatcher failed: %v", err)
	}

	client := &StreamClient{
		sendChan:   make(chan []byte, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	if hub.ActiveStreams() != 1 {
		t.Fatalf("expected 1 active stream, got %d", hub.ActiveStreams())
	}

	// 停止 Hub
	if err := hub.Close(); err != nil {
		t.Fatalf("hub close failed: %v", err)
	}

	if hub.ActiveStreams() != 0 {
		t.Fatalf("expected 0 active streams after hub close, got %d", hub.ActiveStreams())
	}

	// 验证无法再获取或创建
	_, err = hub.GetOrCreateDispatcher(context.Background(), camID, "main")
	if !errors.Is(err, ErrStreamHubClosed) {
		t.Fatalf("expected ErrStreamHubClosed, got %v", err)
	}
}

func TestStreamHubSingleflightConcurrentRequests(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	registry := NewStateRegistry()
	registry.InitCameraState(&Camera{
		ID:       camID,
		Name:     "Hub Test Camera",
		Enabled:  true,
		Revision: 1,
		Streams:  []CameraStream{{Role: "main"}},
	})

	hub := NewStreamHub(mockEngine, store, cipher, registry, nil, zap.NewNop())
	defer hub.Close()

	const concurrentCount = 10
	var wg sync.WaitGroup
	dispatchers := make([]*StreamDispatcher, concurrentCount)
	errs := make([]error, concurrentCount)

	wg.Add(concurrentCount)
	for i := 0; i < concurrentCount; i++ {
		idx := i
		go func() {
			defer wg.Done()
			disp, err := hub.GetOrCreateDispatcher(context.Background(), camID, "main")
			dispatchers[idx] = disp
			errs[idx] = err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d failed: %v", i, err)
		}
	}

	first := dispatchers[0]
	if first == nil {
		t.Fatal("first dispatcher is nil")
	}
	for i := 1; i < concurrentCount; i++ {
		if dispatchers[i] != first {
			t.Fatalf("goroutine %d got different dispatcher %p != %p", i, dispatchers[i], first)
		}
	}

	if mockEngine.getAcquireCount() != 1 {
		t.Fatalf("expected exactly 1 acquire call due to singleflight, got %d", mockEngine.getAcquireCount())
	}
}

func TestStreamHubSessionStateBroadcast(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	registry := NewStateRegistry()
	registry.InitCameraState(&Camera{
		ID:       camID,
		Name:     "Hub Test Camera",
		Enabled:  true,
		Revision: 1,
		Streams:  []CameraStream{{Role: "main"}},
	})
	eventHub := NewEventHub(registry)
	defer eventHub.Close()

	sub, _, err := eventHub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe eventHub: %v", err)
	}
	defer eventHub.Unsubscribe(sub)

	hub := NewStreamHub(mockEngine, store, cipher, registry, eventHub, zap.NewNop())
	defer hub.Close()

	// 1. 获取 Dispatcher -> 触发 SessionStateRunning 广播
	disp, err := hub.GetOrCreateDispatcher(context.Background(), camID, "main")
	if err != nil {
		t.Fatalf("get or create dispatcher: %v", err)
	}

	// 监听 SessionStateRunning 事件
	var gotRunning bool
	timeout := time.After(time.Second)
	for !gotRunning {
		select {
		case msg := <-sub.Channel():
			if msg.Event == EventTypeChange {
				if info, ok := msg.Data.(*CameraStateInfo); ok && info.CameraID == camID {
					if streamInfo, ok := info.Streams["main"]; ok && streamInfo.Session == SessionStateRunning {
						gotRunning = true
					}
				}
			}
		case <-timeout:
			t.Fatal("timeout waiting for SessionStateRunning event")
		}
	}

	// 2. 注册并注销客户端 -> 触发 StreamDispatcher.Close 与 SessionStateIdle 广播
	client := &StreamClient{
		sendChan:   make(chan []byte, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)
	disp.UnregisterClient(client)

	var gotIdle bool
	timeout = time.After(time.Second)
	for !gotIdle {
		select {
		case msg := <-sub.Channel():
			if msg.Event == EventTypeChange {
				if info, ok := msg.Data.(*CameraStateInfo); ok && info.CameraID == camID {
					if streamInfo, ok := info.Streams["main"]; ok && streamInfo.Session == SessionStateIdle {
						gotIdle = true
					}
				}
			}
		case <-timeout:
			t.Fatal("timeout waiting for SessionStateIdle event")
		}
	}
}

func TestStreamDispatcherPacketActivityThrottledReporting(t *testing.T) {
	mockStream := &mockMediaStream{source: newMockPacketSource(16)}
	registry := NewStateRegistry()
	camID := "cam_packet_test"
	registry.InitCameraState(&Camera{
		ID:       camID,
		Name:     "Packet Test",
		Enabled:  true,
		Revision: 1,
		Streams:  []CameraStream{{Role: "main"}},
	})
	registry.UpdateSessionState(camID, "main", SessionStateRunning)

	hub := NewStreamHub(nil, nil, nil, registry, nil, zap.NewNop())
	pumpCtx, cancelPump := context.WithCancel(context.Background())
	defer cancelPump()

	disp := NewStreamDispatcher(hub, camID, "main", mockStream, mockStream.source, cancelPump, zap.NewNop())
	defer disp.Close()

	go disp.RunPumpLoop(pumpCtx)

	// 发送一帧关键帧
	mockStream.source.packets <- engine.Packet{
		Codec:    engine.CodecH264,
		KeyFrame: true,
		Data:     []byte{0x00, 0x00, 0x00, 0x01, 0x67},
	}

	// 等待 Pump 处理
	time.Sleep(50 * time.Millisecond)

	state, ok := registry.GetState(camID)
	if !ok || state == nil {
		t.Fatal("expected state in registry")
	}
	mainStream := state.Streams["main"]
	if mainStream == nil {
		t.Fatal("expected main stream in registry")
	}

	if mainStream.EvidenceType != EvidencePacketActivity {
		t.Fatalf("expected evidence %s, got %s", EvidencePacketActivity, mainStream.EvidenceType)
	}
	if mainStream.Health != HealthStateOnline {
		t.Fatalf("expected health %s, got %s", HealthStateOnline, mainStream.Health)
	}
	if mainStream.LastCheckedAt == nil {
		t.Fatal("expected non-nil LastCheckedAt")
	}
}
