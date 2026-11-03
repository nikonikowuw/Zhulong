package camera

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// fakeAudioSource 是只产出音频包的可控订阅源。
type fakeAudioSource struct {
	packets chan engine.Packet
	closed  chan struct{}
	once    sync.Once
}

func newFakeAudioSource() *fakeAudioSource {
	return &fakeAudioSource{
		packets: make(chan engine.Packet, 64),
		closed:  make(chan struct{}),
	}
}

func (s *fakeAudioSource) Next(ctx context.Context) (engine.Packet, error) {
	select {
	case pkt := <-s.packets:
		return pkt, nil
	case <-s.closed:
		return engine.Packet{}, engine.ErrClosed
	case <-ctx.Done():
		return engine.Packet{}, ctx.Err()
	}
}

func (s *fakeAudioSource) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func (s *fakeAudioSource) isClosed() bool {
	select {
	case <-s.closed:
		return true
	default:
		return false
	}
}

func newTestAudioChannel(registry *AudioChannelRegistry, key string) *AudioChannel {
	return &AudioChannel{
		registry: registry,
		logger:   zap.NewNop(),
		key:      key,
		cameraID: "cam-" + key,
		role:     "main",
		send:     make(chan []byte, audioChannelQueueFrames),
		done:     make(chan struct{}),
	}
}

// TestAudioRegistrySupersedesSameSession 钉住「同一登录会话只留一条」。
func TestAudioRegistrySupersedesSameSession(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())

	first := newTestAudioChannel(registry, "session-a")
	previous, err := registry.Open("session-a", first)
	if err != nil || previous != nil {
		t.Fatalf("first open: previous=%v err=%v", previous, err)
	}

	second := newTestAudioChannel(registry, "session-a")
	previous, err = registry.Open("session-a", second)
	if err != nil {
		t.Fatalf("second open failed: %v", err)
	}
	if previous != first {
		t.Fatal("expected the first channel to be handed back for supersede")
	}
	if registry.Len() != 1 {
		t.Fatalf("expected 1 channel after supersede, got %d", registry.Len())
	}

	// 旧通道的延迟关闭不能把新表项删掉（顶替时序的真实风险）
	registry.Remove("session-a", first)
	if registry.Len() != 1 {
		t.Fatalf("stale Remove must not evict the live channel, got %d", registry.Len())
	}
	registry.Remove("session-a", second)
	if registry.Len() != 0 {
		t.Fatalf("expected empty registry, got %d", registry.Len())
	}

	opened, superseded, rejected := registry.Stats()
	if opened != 2 || superseded != 1 || rejected != 0 {
		t.Fatalf("unexpected stats: opened=%d superseded=%d rejected=%d", opened, superseded, rejected)
	}
}

// TestAudioRegistryIsolatesSessions 钉住「不同会话互不影响」。
func TestAudioRegistryIsolatesSessions(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())

	alice := newTestAudioChannel(registry, "alice")
	bob := newTestAudioChannel(registry, "bob")

	if _, err := registry.Open("alice", alice); err != nil {
		t.Fatalf("alice open failed: %v", err)
	}
	previous, err := registry.Open("bob", bob)
	if err != nil {
		t.Fatalf("bob open failed: %v", err)
	}
	if previous != nil {
		t.Fatal("different sessions must not supersede each other")
	}
	if registry.Len() != 2 {
		t.Fatalf("expected 2 channels, got %d", registry.Len())
	}
}

// TestAudioRegistryRejectsBeyondLimit 钉住全局硬上限：超限明确拒绝，且不驱逐已有通道。
func TestAudioRegistryRejectsBeyondLimit(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())
	registry.max = 2

	for i := 0; i < 2; i++ {
		if _, err := registry.Open(fmt.Sprintf("session-%d", i), newTestAudioChannel(registry, fmt.Sprintf("s%d", i))); err != nil {
			t.Fatalf("open %d failed: %v", i, err)
		}
	}

	overflow := newTestAudioChannel(registry, "overflow")
	if _, err := registry.Open("overflow", overflow); !errors.Is(err, ErrAudioChannelLimit) {
		t.Fatalf("expected ErrAudioChannelLimit, got %v", err)
	}
	if registry.Len() != 2 {
		t.Fatalf("rejection must not evict a live channel, got %d", registry.Len())
	}

	// 上限只限制「新增会话」：已有会话的接管不受影响，否则刷新页永远接不回来
	replacement := newTestAudioChannel(registry, "s0")
	if _, err := registry.Open("session-0", replacement); err != nil {
		t.Fatalf("supersede at limit must be allowed, got %v", err)
	}
}

// TestAudioRegistryConcurrentAccess 给 -race 抓并发：注册/移除/查询同时发生。
func TestAudioRegistryConcurrentAccess(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("session-%d", i%4)
			channel := newTestAudioChannel(registry, key)
			if _, err := registry.Open(key, channel); err != nil && !errors.Is(err, ErrAudioChannelLimit) {
				t.Errorf("open failed: %v", err)
			}
			_ = registry.Len()
			registry.Remove(key, channel)
			_, _, _ = registry.Stats()
		}(i)
	}
	wg.Wait()
}

// TestAudioChannelPumpPacksFrames 钉住泵出的帧确实是 ZLA1 且时间戳不再重算。
func TestAudioChannelPumpPacksFrames(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())
	source := newFakeAudioSource()
	channel := newTestAudioChannel(registry, "pump")
	channel.sub = source

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go channel.Pump(ctx)

	source.packets <- engine.Packet{
		TrackType: engine.TrackAudio,
		Codec:     engine.CodecPCMU,
		PtsUS:     8000,
		Data:      []byte{0xFF, 0xFF},
	}

	select {
	case frame := <-channel.send:
		header, payload, err := UnpackAudioPacket(frame)
		if err != nil {
			t.Fatalf("unpack failed: %v", err)
		}
		if header.Codec != engine.CodecPCMU || header.PTSUS != 8000 {
			t.Fatalf("unexpected header: %+v", header)
		}
		if len(payload) != 2 {
			t.Fatalf("expected 2-byte payload, got %d", len(payload))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a packed audio frame")
	}
}

// TestAudioChannelBackpressureClosesOnlyThisChannel 钉住「队列溢出只关这条通道」。
func TestAudioChannelBackpressureClosesOnlyThisChannel(t *testing.T) {
	registry := NewAudioChannelRegistry(zap.NewNop())
	source := newFakeAudioSource()
	channel := newTestAudioChannel(registry, "slow")
	channel.sub = source
	// 队列容量 1，且没人消费：第二帧必然溢出
	channel.send = make(chan []byte, 1)

	if _, err := registry.Open("slow", channel); err != nil {
		t.Fatalf("open failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go channel.Pump(ctx)

	for i := 0; i < 3; i++ {
		source.packets <- engine.Packet{
			TrackType: engine.TrackAudio,
			Codec:     engine.CodecPCMA,
			PtsUS:     int64(i),
			Data:      []byte{0xD5},
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if channel.closed.Load() {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !channel.closed.Load() {
		t.Fatal("expected the audio channel to close on backpressure")
	}
	if registry.Len() != 0 {
		t.Fatalf("closed channel must leave the registry, got %d", registry.Len())
	}
	if channel.dropped.Load() == 0 {
		t.Fatal("expected a dropped-frame counter to record the overflow")
	}
	// 关闭必须同步排空订阅：Close 返回后源已经关闭，不会再有回调在跑。
	if !source.isClosed() {
		t.Fatal("channel close must drain (close) its subscription")
	}
}
