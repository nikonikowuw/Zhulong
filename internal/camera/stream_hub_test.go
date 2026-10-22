package camera

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/region"
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
	aiSub     *engine.AIResultSubscription
	closeOnce sync.Once
	closed    bool
	mu        sync.Mutex
}

func (s *mockMediaStream) Subscribe(ctx context.Context, options engine.SubscriptionOptions) (PacketSource, error) {
	return s.source, nil
}

func (s *mockMediaStream) SubscribeAIResults(ctx context.Context) (*engine.AIResultSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.aiSub, nil
}

func (s *mockMediaStream) BrokerPath(ctx context.Context) (string, error) {
	return "/tmp/zhulong_broker_test.sock", nil
}

func (s *mockMediaStream) SetPublishRate(ctx context.Context, fps uint32) error {
	return nil
}

func (s *mockMediaStream) Snapshot(ctx context.Context, options engine.SnapshotOptions) ([]engine.SnapshotImage, error) {
	return nil, engine.ErrSnapshotUnavailable
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
	nextStream   *mockMediaStream
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

	if e.nextStream != nil {
		s := e.nextStream
		e.streams = append(e.streams, s)
		e.nextStream = nil
		return s, nil
	}

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

	store := NewCameraStore(dbStore)
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
		sendChan:   make(chan outFrame, 16),
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
		sendChan:   make(chan outFrame, 16),
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

	// 验证状态机切为 idle。
	// SessionStateIdle 由 StreamDispatcher 的收流协程异步写入（stream_dispatcher.go 中的
	// UpdateSessionState 调用），可能晚于 UnregisterClient 返回，因此必须轮询等待而不能立即断言。
	deadline := time.After(time.Second)
	for {
		info, ok = registry.GetState(camID)
		if ok && info != nil && info.Streams["main"] != nil && info.Streams["main"].Session == SessionStateIdle {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for SessionStateIdle, got %v", info)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestStreamDispatcherGOPCacheInstantPlayback(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	// 1. 发送一帧关键帧
	keyPkt := engine.Packet{
		Codec:      engine.CodecH264,
		PTS:        1000,
		DTS:        1000,
		HasPTS:     true,
		HasDTS:     true,
		KeyFrame:   true,
		PtsUS:      10000,
		DtsUS:      10000,
		FrameClass: engine.FrameIDR,
		GOPID:      1,
		Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x67, 0x42},
	}
	disp.Broadcast(keyPkt)

	// 2. 发送一帧非关键帧
	pPkt := engine.Packet{
		Codec:      engine.CodecH264,
		PTS:        2000,
		DTS:        2000,
		HasPTS:     true,
		HasDTS:     true,
		KeyFrame:   false,
		PtsUS:      20000,
		DtsUS:      20000,
		FrameClass: engine.FramePredictive,
		GOPID:      1,
		Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x41},
	}
	disp.Broadcast(pPkt)

	// 3. 新客户端连入
	client := &StreamClient{
		sendChan:   make(chan outFrame, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	// 4. 新客户端必须收到「完整 GOP」：IDR + 其后的每一个 P 帧。
	//    只补一个 IDR、然后把中间的 P 帧全跳过，会让参考链断裂，
	//    外部表现正是「刚开始播放时卡几帧」。
	wantPts := []int64{10000, 20000}
	for i, expected := range wantPts {
		select {
		case frame := <-client.sendChan:
			header, payload, err := UnpackPacket(frame.data)
			if err != nil {
				t.Fatalf("unpack primed frame %d failed: %v", i, err)
			}
			if header.PTSUS != expected {
				t.Fatalf("primed frame %d: expected pts_us %d, got %d", i, expected, header.PTSUS)
			}
			if i == 0 && !header.IsKeyFrame() {
				t.Fatalf("expected first primed frame to be keyframe")
			}
			if i == 0 && len(payload) != len(keyPkt.Data) {
				t.Fatalf("expected payload len %d, got %d", len(keyPkt.Data), len(payload))
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for primed frame %d", i)
		}
	}

	// 5. 补齐后时间预算基准应落在 GOP 最后一帧，后续实时帧不得再触发重同步。
	//    这是「秒开即被自己丢掉」那个 bug 的回归保护。
	disp.Broadcast(engine.Packet{
		Codec:      engine.CodecH264,
		PtsUS:      60000,
		DtsUS:      60000,
		FrameClass: engine.FramePredictive,
		GOPID:      1,
		Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x41},
	})
	if got := disp.Counters().LagResyncs; got != 0 {
		t.Fatalf("expected no lag resync right after priming, got %d", got)
	}
	select {
	case frame := <-client.sendChan:
		if frame.ptsUS != 60000 {
			t.Fatalf("expected realtime frame pts_us 60000, got %d", frame.ptsUS)
		}
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for realtime frame after priming")
	}
}

// TestStreamDispatcherGOPCacheResetsOnNewIDR 验证新 GOP 到来时缓存被替换，
// 而不是把两个 GOP 拼在一起（那会让新客户端收到跨 GOP 的断链帧）。
func TestStreamDispatcherGOPCacheResetsOnNewIDR(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	broadcast := func(ptsUS int64, key bool) {
		pkt := engine.Packet{
			Codec:      engine.CodecH264,
			KeyFrame:   key,
			PtsUS:      ptsUS,
			DtsUS:      ptsUS,
			FrameClass: engine.FramePredictive,
			GOPID:      1,
			Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x41},
		}
		if key {
			pkt.FrameClass = engine.FrameIDR
		}
		disp.Broadcast(pkt)
	}

	broadcast(10000, true)
	broadcast(20000, false)
	broadcast(30000, false)
	broadcast(40000, true) // 新 GOP
	broadcast(50000, false)

	client := &StreamClient{
		sendChan:   make(chan outFrame, 16),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	got := []int64{}
	for {
		select {
		case frame := <-client.sendChan:
			got = append(got, frame.ptsUS)
			continue
		default:
		}
		break
	}

	want := []int64{40000, 50000}
	if len(got) != len(want) {
		t.Fatalf("expected primed frames %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected primed frames %v, got %v", want, got)
		}
	}
}

// TestStreamDispatcherBackpressureResync 验证慢客户端的处置语义：
//
//   - 绝不逐帧挑着丢非关键帧（那会破坏参考链，画面花到下一个 IDR）；
//   - 队列溢出时整段丢弃并降级为「只等关键帧」，关键帧到来后自动接上；
//   - 绝不因慢而踢掉客户端 —— 踢掉只会在弱网上制造重连风暴。
func TestStreamDispatcherBackpressureResync(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	mark := func(ptsUS int64, key bool) engine.Packet {
		pkt := engine.Packet{
			Codec:      engine.CodecH264,
			KeyFrame:   key,
			PtsUS:      ptsUS,
			DtsUS:      ptsUS,
			FrameClass: engine.FramePredictive,
			GOPID:      1,
			Data:       []byte{0x00, 0x00, 0x00, 0x01, 0x41},
		}
		if key {
			pkt.FrameClass = engine.FrameIDR
		}
		return pkt
	}

	// 建立一个完整 GOP：IDR + 一个参考帧
	disp.Broadcast(mark(10_000, true))
	disp.Broadcast(mark(20_000, false))

	// 容量 2 的客户端：补发这两帧恰好填满队列，模拟消费端卡住
	client := &StreamClient{
		sendChan:   make(chan outFrame, 2),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	// 新帧到达但队列已满 → 整段重同步
	disp.Broadcast(mark(30_000, false))
	if got := disp.Counters().QueueResyncs; got == 0 {
		t.Fatalf("expected at least one queue resync, got %d", got)
	}

	// 重同步期间参考帧被跳过，而不是被塞进队列
	disp.Broadcast(mark(40_000, false))
	if got := disp.Counters().ResyncSkips; got == 0 {
		t.Fatalf("expected predictive frames to be skipped during resync, got %d", got)
	}

	// 关键帧到来：重同步结束，客户端恢复正常投递
	disp.Broadcast(mark(100_000, true))

	select {
	case frame := <-client.sendChan:
		header, _, err := UnpackPacket(frame.data)
		if err != nil {
			t.Fatalf("unpack frame failed: %v", err)
		}
		if !header.IsKeyFrame() {
			t.Fatalf("expected recovery frame to be keyframe, got class %d", header.FrameClass)
		}
		if header.PTSUS != 100_000 {
			t.Fatalf("expected recovery pts_us 100000, got %d", header.PTSUS)
		}
	case <-time.After(time.Second):
		t.Fatalf("client did not recover after keyframe")
	}
}

func TestStreamDispatcherLagBudgetTracksWriteProgress(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	client := &StreamClient{
		sendChan:   make(chan outFrame, 64),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	for i := int64(0); i <= 20; i++ {
		key := i == 0
		frameClass := engine.FramePredictive
		if key {
			frameClass = engine.FrameIDR
		}
		ptsUs := i * 40_000
		disp.Broadcast(engine.Packet{
			Codec:      engine.CodecH264,
			KeyFrame:   key,
			PtsUS:      ptsUs,
			DtsUS:      ptsUs,
			FrameClass: frameClass,
			GOPID:      1,
			Data:       []byte{0, 0, 0, 1, 0x41},
		})
	}

	if got := disp.Counters().LagResyncs; got == 0 {
		t.Fatal("expected stalled writer to trigger DTS progress resynchronization")
	}
	if got := disp.Counters().QueueResyncs; got != 0 {
		t.Fatalf("lag resync should trigger before queue fills, got queue resyncs %d", got)
	}
	if len(client.sendChan) != 0 {
		t.Fatalf("expected stalled client's queued frames to be drained, got %d", len(client.sendChan))
	}
}

func TestStreamDispatcherLagBudgetAdvancesOnSuccessfulWrites(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	client := &StreamClient{
		sendChan:   make(chan outFrame, 8),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)

	for i := int64(0); i <= 20; i++ {
		key := i == 0
		frameClass := engine.FramePredictive
		if key {
			frameClass = engine.FrameIDR
		}
		ptsUs := i * 40_000
		disp.Broadcast(engine.Packet{
			Codec:      engine.CodecH264,
			KeyFrame:   key,
			PtsUS:      ptsUs,
			DtsUS:      ptsUs,
			FrameClass: frameClass,
			GOPID:      1,
			Data:       []byte{0, 0, 0, 1, 0x41},
		})

		select {
		case frame := <-client.sendChan:
			disp.markFrameWritten(client, frame)
		default:
			t.Fatalf("frame %d was not queued for a progressing writer", i)
		}
	}

	if got := disp.Counters().LagResyncs; got != 0 {
		t.Fatalf("successful writes should advance the DTS budget, got %d lag resyncs", got)
	}
	if got := disp.Counters().QueueResyncs; got != 0 {
		t.Fatalf("progressing writer should not hit queue resync, got %d", got)
	}
}

func TestStreamDispatcherGOPCacheFrameLimitMatchesClientQueue(t *testing.T) {
	disp := NewStreamDispatcher(nil, "cam_test", "main", nil, nil, nil, zap.NewNop())
	defer disp.Close()

	for i := 0; i <= gopCacheMaxFrames; i++ {
		key := i == 0
		frameClass := engine.FramePredictive
		if key {
			frameClass = engine.FrameIDR
		}
		ptsUs := int64(i) * 40_000
		disp.Broadcast(engine.Packet{
			Codec:      engine.CodecH264,
			KeyFrame:   key,
			PtsUS:      ptsUs,
			DtsUS:      ptsUs,
			FrameClass: frameClass,
			GOPID:      1,
			Data:       []byte{0, 0, 0, 1, 0x41},
		})
	}

	if disp.gop.complete {
		t.Fatal("GOP cache must not mark a GOP complete when it cannot fit the client queue")
	}
	if got := disp.Counters().GOPTruncations; got != 1 {
		t.Fatalf("expected one frame-limit truncation, got %d", got)
	}

	client := &StreamClient{
		sendChan:   make(chan outFrame, defaultClientBufferSize),
		dispatcher: disp,
		done:       make(chan struct{}),
		logger:     zap.NewNop(),
	}
	disp.RegisterClient(client)
	if len(client.sendChan) != 0 || !client.needsKey {
		t.Fatal("incomplete GOP must not be primed; client must wait for the next IDR")
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
		sendChan:   make(chan outFrame, 16),
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
		sendChan:   make(chan outFrame, 16),
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

func TestStreamHubAIStreamSubscribeAndRelease(t *testing.T) {
	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, nil, zap.NewNop())
	defer hub.Close()

	ctx := context.Background()

	// 1. 解析 AI 流路径（此时仅拉起物理拉流，尚未建立订阅关系）
	path1, err := hub.ResolveAIStreamBrokerPath(ctx, camID)
	if err != nil {
		t.Fatalf("first ResolveAIStreamBrokerPath failed: %v", err)
	}
	if path1 != "/tmp/zhulong_broker_test.sock" {
		t.Fatalf("unexpected broker path: %s", path1)
	}
	if mockEngine.getAcquireCount() != 1 {
		t.Fatalf("expected 1 acquire on engine, got %d", mockEngine.getAcquireCount())
	}

	// 2. 重复解析复用同一物理拉流
	path2, err := hub.ResolveAIStreamBrokerPath(ctx, camID)
	if err != nil {
		t.Fatalf("second ResolveAIStreamBrokerPath failed: %v", err)
	}
	if path2 != path1 {
		t.Fatalf("paths do not match: %s vs %s", path1, path2)
	}
	if mockEngine.getAcquireCount() != 1 {
		t.Fatalf("expected still 1 acquire on engine, got %d", mockEngine.getAcquireCount())
	}

	// 3. 两个订阅者共同保活
	hub.AttachAIStreamSubscriber(camID, "algo_a")
	hub.AttachAIStreamSubscriber(camID, "algo_b")

	// 4. 注销第一个订阅者：流仍然存活
	hub.ReleaseAIStreamSubscriber(camID, "algo_a")
	if mockEngine.streams[0].closed {
		t.Fatal("stream closed prematurely while another subscriber remains")
	}

	// 5. 设置发布速率
	if err := hub.SetAIPublishRate(ctx, camID, 15); err != nil {
		t.Fatalf("SetAIPublishRate failed: %v", err)
	}

	// 6. 注销最后一个订阅者：流被释放
	hub.ReleaseAIStreamSubscriber(camID, "algo_b")
	if !mockEngine.streams[0].closed {
		t.Fatal("expected stream to be closed after final subscriber left")
	}
}

func TestStreamHubReleaseReleasesStreamWhenLastSubscriberLeaves(t *testing.T) {
	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, nil, zap.NewNop())
	defer hub.Close()

	ctx := context.Background()
	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("resolve AI stream failed: %v", err)
	}

	// Two packages subscribe to the same camera.
	hub.AttachAIStreamSubscriber(camID, "algo_a")
	hub.AttachAIStreamSubscriber(camID, "algo_b")

	// algo_b is removed (its task was deleted): the stream must survive for algo_a.
	hub.ReleaseAIStreamSubscriber(camID, "algo_b")
	if mockEngine.streams[0].closed {
		t.Fatal("stream closed while algo_a still subscribed")
	}
	if ids := hub.AICameraIDs(); len(ids) != 1 {
		t.Fatalf("expected the camera to remain tracked, got %v", ids)
	}

	// algo_a leaves too: the stream and its broker are released.
	hub.ReleaseAIStreamSubscriber(camID, "algo_a")
	if !mockEngine.streams[0].closed {
		t.Fatal("expected stream to be closed after the last subscriber left")
	}
	if ids := hub.AICameraIDs(); len(ids) != 0 {
		t.Fatalf("expected no tracked AI cameras, got %v", ids)
	}
}

func TestStreamHubAICameraIDs(t *testing.T) {
	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, nil, zap.NewNop())
	defer hub.Close()

	ctx := context.Background()
	if ids := hub.AICameraIDs(); len(ids) != 0 {
		t.Fatalf("expected no AI cameras initially, got %v", ids)
	}
	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("resolve AI stream failed: %v", err)
	}
	ids := hub.AICameraIDs()
	if len(ids) != 1 || ids[0] != camID {
		t.Fatalf("expected [%s], got %v", camID, ids)
	}
}

func TestStreamHub_ConsumeAIResultsAndBroadcast(t *testing.T) {
	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	eventHub := NewEventHub(NewStateRegistry())
	defer eventHub.Close()

	eventSub, _, err := eventHub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe to event hub: %v", err)
	}
	defer eventHub.Unsubscribe(eventSub)

	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, eventHub, zap.NewNop())
	defer hub.Close()

	aiSub, feed := engine.NewAIResultSubscriptionForTest(16)
	ctx := context.Background()

	// 提前塞入 mock stream 供 Resolve 获取
	mockStream := &mockMediaStream{source: newMockPacketSource(16), aiSub: aiSub}
	mockEngine.nextStream = mockStream

	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("resolve AI stream failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "test_algo")

	// 模拟 Runner 上报一条检测结果
	feed([]byte(`{"stream_id":1,"pts":12345,"cost_ms":10,"task_type":"detection","detections":[{"box":[0.1,0.2,0.3,0.4],"label":"helmet","score":0.95}],"classifications":[],"persons":[]}`))

	// SSE 客户端应当收到广播事件
	select {
	case msg := <-eventSub.Channel():
		if msg.Event != EventTypeAIResult {
			t.Fatalf("expected EventTypeAIResult, got: %s", msg.Event)
		}
		res, ok := msg.Data.(aitypes.InferenceResult)
		if !ok {
			t.Fatalf("expected aitypes.InferenceResult, got: %T", msg.Data)
		}
		if res.CameraID != camID || res.TaskType != aitypes.TaskTypeDetection || len(res.Detections) != 1 {
			t.Fatalf("unexpected result payload: %+v", res)
		}
		if res.Detections[0].Label != "helmet" || res.Detections[0].Score != 0.95 {
			t.Errorf("unexpected detection: %+v", res.Detections[0])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for AI result SSE event")
	}

	hub.ReleaseAIStreamSubscriber(camID, "test_algo")
}

type mockRegionConfigProvider struct {
	configs map[string]map[string]region.Config
}

func (m *mockRegionConfigProvider) ConfigFor(cameraID, algorithmID string) (region.Config, bool) {
	if algos, ok := m.configs[cameraID]; ok {
		if cfg, ok := algos[algorithmID]; ok {
			return cfg, true
		}
	}
	return region.Config{}, false
}

// recordingAIResultConsumer captures every inference result handed to it, so a
// test can assert that the persistence path and the frontend broadcast path
// observe the same (post-filter) payload.
type recordingAIResultConsumer struct {
	mu      sync.Mutex
	results []aitypes.InferenceResult
}

func (c *recordingAIResultConsumer) ProcessInferenceResult(_ context.Context, res aitypes.InferenceResult) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.results = append(c.results, res)
	return nil
}

func (c *recordingAIResultConsumer) snapshot() []aitypes.InferenceResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]aitypes.InferenceResult, len(c.results))
	copy(out, c.results)
	return out
}

func TestStreamHub_ConsumeAIResults_RegionFiltering(t *testing.T) {
	store, lazyCipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	eventHub := NewEventHub(NewStateRegistry())
	defer eventHub.Close()

	eventSub, _, err := eventHub.Subscribe()
	if err != nil {
		t.Fatalf("subscribe to event hub: %v", err)
	}
	defer eventHub.Unsubscribe(eventSub)

	hub := NewStreamHub(mockEngine, store, lazyCipher, nil, eventHub, zap.NewNop())
	defer hub.Close()

	// Configure the region config provider:
	// ROI: [0.0, 0.0, 0.5, 0.5], MASK: [0.1, 0.1, 0.3, 0.3]
	provider := &mockRegionConfigProvider{
		configs: map[string]map[string]region.Config{
			camID: {
				"test_algo": {
					ROI:  []region.Rect{{0.0, 0.0, 0.5, 0.5}},
					MASK: []region.Rect{{0.1, 0.1, 0.3, 0.3}},
				},
			},
		},
	}
	hub.SetRegionConfigProvider(provider)

	// Register a recording AI consumer (the persistence path). Filtering must
	// happen before both fan-out targets, so this consumer and the eventHub must
	// observe byte-for-byte identical payloads.
	consumer := &recordingAIResultConsumer{}
	hub.SetAIConsumer(consumer)

	aiSub, feed := engine.NewAIResultSubscriptionForTest(16)
	ctx := context.Background()

	mockStream := &mockMediaStream{source: newMockPacketSource(16), aiSub: aiSub}
	mockEngine.nextStream = mockStream

	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("resolve AI stream failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "test_algo")

	// Target 1: inside ROI and outside MASK -> bottom center (0.05, 0.2) -> KEEP
	// Target 2: inside ROI but inside MASK -> bottom center (0.2, 0.25) -> FILTERED
	// Target 3: outside ROI -> bottom center (0.75, 0.8) -> FILTERED
	feed([]byte(`{
		"stream_id":1,
		"pts":12345,
		"cost_ms":10,
		"task_type":"detection",
		"algorithm_id":"test_algo",
		"detections":[
			{"box":[0.0, 0.1, 0.1, 0.2],"label":"keep_me","score":0.95},
			{"box":[0.15, 0.15, 0.25, 0.25],"label":"masked_out","score":0.90},
			{"box":[0.7, 0.7, 0.8, 0.8],"label":"outside_roi","score":0.85}
		],
		"classifications":[],
		"persons":[]
	}`))

	select {
	case msg := <-eventSub.Channel():
		if msg.Event != EventTypeAIResult {
			t.Fatalf("expected EventTypeAIResult, got: %s", msg.Event)
		}
		res, ok := msg.Data.(aitypes.InferenceResult)
		if !ok {
			t.Fatalf("expected aitypes.InferenceResult, got: %T", msg.Data)
		}
		if len(res.Detections) != 1 {
			t.Fatalf("expected exactly 1 detection after filtering, got %d", len(res.Detections))
		}
		if res.Detections[0].Label != "keep_me" {
			t.Errorf("expected keep_me, got %s", res.Detections[0].Label)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for AI result SSE event")
	}

	// The consumer call precedes the eventHub broadcast in consumeAIResults, but
	// poll with a bound rather than relying on that ordering assumption.
	var consumerResults []aitypes.InferenceResult
	for i := 0; i < 100; i++ {
		consumerResults = consumer.snapshot()
		if len(consumerResults) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(consumerResults) != 1 {
		t.Fatalf("expected exactly 1 result on the persistence path, got %d", len(consumerResults))
	}
	if len(consumerResults[0].Detections) != 1 {
		t.Fatalf("expected exactly 1 detection on the persistence path, got %d", len(consumerResults[0].Detections))
	}
	if consumerResults[0].Detections[0].Label != "keep_me" {
		t.Errorf("expected keep_me on the persistence path, got %s", consumerResults[0].Detections[0].Label)
	}

	hub.ReleaseAIStreamSubscriber(camID, "test_algo")
}
