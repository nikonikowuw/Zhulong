package alertpush

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

type mockSender struct {
	mu       sync.Mutex
	received []record.AlarmEvent
	delay    time.Duration
	err      error
	closed   atomic.Bool
}

func (m *mockSender) Send(ctx context.Context, ev record.AlarmEvent) error {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.delay):
		}
	}
	if m.err != nil {
		return m.err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.received = append(m.received, ev)
	return nil
}

func (m *mockSender) Close() error {
	m.closed.Store(true)
	return nil
}

func (m *mockSender) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.received)
}

func TestDispatcherFaultIsolation(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	fastSender := &mockSender{}
	slowSender := &mockSender{delay: 2 * time.Second}

	fastWorker := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "fast", ChannelType: ChannelTypeWebhook},
		sender:      fastSender,
		filter:      destinationFilter{},
		fingerprint: "fp1",
		logger:      zap.NewNop(),
	})
	slowWorker := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "slow", ChannelType: ChannelTypeWebhook},
		sender:      slowSender,
		filter:      destinationFilter{},
		fingerprint: "fp2",
		logger:      zap.NewNop(),
	})

	d.mu.Lock()
	d.workers["fast"] = fastWorker
	d.workers["slow"] = slowWorker
	d.mu.Unlock()

	fastWorker.start()
	slowWorker.start()

	ev := record.AlarmEvent{
		ID:         "01J8ZTEST01",
		CameraID:   "cam_01",
		CapturedAt: time.Now().UTC(),
	}

	start := time.Now()
	d.Dispatch(ev)

	// fast sender 应该在 100ms 内收到，不受 slow sender 2s 延迟影响
	deadline := time.Now().Add(500 * time.Millisecond)
	for fastSender.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if fastSender.count() != 1 {
		t.Fatalf("expected fast sender to receive event quickly, elapsed=%v", time.Since(start))
	}
}

func TestDispatcherQueueDropOldest(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	// 不启动 worker 消费，纯测试队列填满丢最旧
	sender := &mockSender{}
	w := newDestinationWorker(destinationWorkerOptions{
		dest:        &AlertDestination{ID: "drop_test", ChannelType: ChannelTypeWebhook},
		sender:      sender,
		filter:      destinationFilter{},
		fingerprint: "fp",
		logger:      zap.NewNop(),
	})

	d.mu.Lock()
	d.workers["drop_test"] = w
	d.mu.Unlock()

	// 队列容量 16，连续投递 20 条
	for range 20 {
		d.Dispatch(record.AlarmEvent{ID: "ev"})
	}

	if len(w.queue) != 16 {
		t.Fatalf("expected queue length 16, got %d", len(w.queue))
	}
	if w.dropped.Load() != 4 {
		t.Fatalf("expected 4 dropped events, got %d", w.dropped.Load())
	}
}

func TestDispatcherStopSafe(t *testing.T) {
	d := NewDispatcher(DispatcherOptions{Logger: zap.NewNop()})
	ctx := context.Background()

	// 未 Start 调用 Dispatch 不 panic
	d.Dispatch(record.AlarmEvent{ID: "safe"})

	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if err := d.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// 停止后调用 Dispatch 不 panic
	d.Dispatch(record.AlarmEvent{ID: "safe_after_stop"})
}

func TestDispatcherReconcilePreservesUnchangedWorker(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	dest := &AlertDestination{
		ID:          "dest_reconcile_test",
		Name:        "Webhook 1",
		ChannelType: ChannelTypeWebhook,
		Enabled:     true,
		ImageMode:   ImageModeURL,
		ConfigJSON:  `{"auth_type":"none","timeout_seconds":5}`,
		FilterJSON:  `{}`,
	}
	secBytes, err := store.EncryptSecret(dest.ID, &webhookSecret{URL: "http://127.0.0.1:9999/hook"})
	if err != nil {
		t.Fatalf("EncryptSecret failed: %v", err)
	}
	dest.SecretCiphertext = secBytes
	if err := store.Create(ctx, dest); err != nil {
		t.Fatalf("Create destination failed: %v", err)
	}

	d := NewDispatcher(DispatcherOptions{
		Store:  store,
		Signer: NewEmptyURLSigner(),
		Config: Config{PublicBaseURL: "http://127.0.0.1:8000"},
		Logger: zap.NewNop(),
	})
	if err := d.Start(ctx); err != nil {
		t.Fatalf("Start dispatcher failed: %v", err)
	}
	defer func() {
		if err := d.Stop(ctx); err != nil {
			t.Errorf("Stop failed: %v", err)
		}
	}()

	d.mu.RLock()
	initialWorker, ok := d.workers[dest.ID]
	d.mu.RUnlock()
	if !ok || initialWorker == nil {
		t.Fatalf("expected initial worker to be created on start")
	}

	// 再次触发 reconcile，目标配置未改变
	d.reconcile()

	d.mu.RLock()
	currentWorker := d.workers[dest.ID]
	d.mu.RUnlock()

	// 核心断言：worker 指针恒定，没有被粗暴销毁替换
	if currentWorker != initialWorker {
		t.Fatalf("expected worker instance to be preserved on unchanged reconcile")
	}
}
