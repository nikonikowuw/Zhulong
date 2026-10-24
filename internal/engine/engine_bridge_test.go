package engine

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestEngineProbeContextCancelled(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	// 1. 发起前已经取消的 Context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.Probe(ctx, "rtsp://127.0.0.1:8554/nonexistent", StreamOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestEngineProbeConcurrencyLimit(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	// 占满 4 个探测令牌
	for i := 0; i < 4; i++ {
		e.probeTokens <- struct{}{}
	}

	// 第 5 个探测应当因为 Context 超时退出，且不影响引擎状态
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := e.Probe(ctx, "rtsp://127.0.0.1:8554/live", StreamOptions{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected DeadlineExceeded when tokens exhausted, got %v", err)
	}

	// 归还 4 个令牌
	for i := 0; i < 4; i++ {
		<-e.probeTokens
	}
}

func TestEngineStaleResource(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 构造一个模拟 Stream，记录当前 generation
	stream := &Stream{
		engine:     e,
		id:         1,
		consumerID: 100,
		kind:       ConsumerPreview,
		url:        "rtsp://127.0.0.1:8554/live",
		generation: generationForTest(e),
	}

	// 重启引擎，触发 generation 递增
	if err := e.Stop(); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if err := e.Start(); err != nil {
		t.Fatalf("Restart failed: %v", err)
	}
	defer e.Close()

	// 旧 generation 的 Stream 调用 Status 应当被识别为陈旧资源
	ctx := context.Background()
	_, err := stream.Status(ctx)
	if !errors.Is(err, ErrStaleResource) {
		t.Fatalf("expected ErrStaleResource, got %v", err)
	}

	// 旧 generation 的 Stream 调用 Subscribe 也应当被拒绝
	_, err = stream.Subscribe(ctx, SubscriptionOptions{})
	if !errors.Is(err, ErrNotRunning) && !errors.Is(err, ErrStaleResource) {
		t.Fatalf("expected ErrNotRunning or ErrStaleResource, got %v", err)
	}

	// 旧 generation 的 Stream 调用 Close 应当安全返回 ErrStaleResource 而不崩溃
	err = stream.Close()
	if !errors.Is(err, ErrStaleResource) {
		t.Fatalf("expected ErrStaleResource, got %v", err)
	}
}

func TestEngineAcquireWhenStopped(t *testing.T) {
	e := New()
	// 未 Start
	ctx := context.Background()
	_, err := e.Acquire(ctx, "rtsp://127.0.0.1:8554/live", 1, ConsumerPreview, StreamOptions{})
	if !errors.Is(err, ErrNotRunning) {
		t.Fatalf("expected ErrNotRunning on stopped engine, got %v", err)
	}
}

func TestEngineAcquireValidation(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx := context.Background()

	// 非法 URL
	_, err := e.Acquire(ctx, "http://invalid", 1, ConsumerPreview, StreamOptions{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for non-rtsp url, got %v", err)
	}

	// 零 consumerID
	_, err = e.Acquire(ctx, "rtsp://127.0.0.1:8554/live", 0, ConsumerPreview, StreamOptions{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for consumerID=0, got %v", err)
	}

	// 非法 kind
	_, err = e.Acquire(ctx, "rtsp://127.0.0.1:8554/live", 1, ConsumerKind(42), StreamOptions{})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid kind, got %v", err)
	}

	// 非法 Transport
	_, err = e.Acquire(ctx, "rtsp://127.0.0.1:8554/live", 1, ConsumerPreview, StreamOptions{Transport: Transport(5)})
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for invalid transport, got %v", err)
	}
}

func TestEngineConcurrentProbeCancellation(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
			defer cancel()
			// 探测不存在的端口，迅速触发 context deadline
			_, _ = e.Probe(ctx, "rtsp://127.0.0.1:18554/stream", StreamOptions{
				OpenTimeout: 10 * time.Second,
			})
		}()
	}
	wg.Wait()
}

func TestEngineUnsubscribeNotFoundDeletesToken(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	tokenDeleted := false
	deleteToken := func() {
		tokenDeleted = true
	}

	// 针对一个不存在的 stream ID 或 subscription ID 执行退订，C++ 会返回 ERR_NOT_FOUND
	err := e.unsubscribe(generationForTest(e), 99999, 88888, deleteToken)
	if err == nil {
		t.Fatal("expected error for non-existent stream/subscription")
	}

	if !tokenDeleted {
		t.Fatal("expected deleteToken to be called even when unsubscribe returns NOT_FOUND")
	}
}

func TestEngineBrokerPathAndPublishRateValidation(t *testing.T) {
	e := New()
	if err := e.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer e.Close()

	ctx := context.Background()

	// 1. 已关闭的 Stream 调用返回 ErrClosed
	sClosed := &Stream{
		engine:     e,
		id:         1,
		consumerID: 101,
		kind:       ConsumerAI,
		closed:     true,
		generation: generationForTest(e),
	}
	if _, err := sClosed.BrokerPath(ctx); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed on closed stream BrokerPath, got %v", err)
	}
	if err := sClosed.SetPublishRate(ctx, 10); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected ErrClosed on closed stream SetPublishRate, got %v", err)
	}

	// 2. 针对不存在的 stream ID 调用返回 ErrNotFound
	sNonExistent := &Stream{
		engine:     e,
		id:         99999,
		consumerID: 102,
		kind:       ConsumerAI,
		closed:     false,
		generation: generationForTest(e),
	}
	if _, err := sNonExistent.BrokerPath(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent stream, got %v", err)
	}
	if err := sNonExistent.SetPublishRate(ctx, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non-existent stream, got %v", err)
	}
}

// TestEngineStopRacesInFlightProbe 覆盖「停机快照 vs 探测自行销毁」的竞态窗口。
//
// 历史缺陷：stopInternal 把 activeProbes 快照出锁后裸调 Zhulong_engine_stop，
// 而探测 goroutine 同时会 stop 并在返回时 destroy 同一个私有引擎 ——
// 快照晚用一步就是对已释放句柄调 stop（Use-After-Free）。
// 窗口靠调度，因此这里用「不可路由地址 + 立即 Stop/Close」反复冲击它。
func TestEngineStopRacesInFlightProbe(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		eng := New()
		if err := eng.Start(); err != nil {
			t.Fatalf("iteration %d: Start: %v", iteration, err)
		}

		var probes sync.WaitGroup
		for range 2 {
			probes.Add(1)
			go func() {
				defer probes.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
				defer cancel()
				// TEST-NET-1（RFC 5737）：连接会被黑洞，探测停留在原生 open 里。
				_, _ = eng.Probe(ctx, "rtsp://192.0.2.1:554/race", StreamOptions{OpenTimeout: 500 * time.Millisecond})
			}()
		}

		time.Sleep(2 * time.Millisecond)
		if err := eng.Stop(); err != nil {
			t.Fatalf("iteration %d: Stop: %v", iteration, err)
		}
		if err := eng.Close(); err != nil {
			t.Fatalf("iteration %d: Close: %v", iteration, err)
		}
		probes.Wait()
	}
}

// TestProbeContextStopDestroyAreSerialized 验证私有探测上下文的 stop/destroy 互斥。
//
// 这是 UAF 防护的最小可测单元：stopInternal 拿着 activeProbes 的快照调 stop，
// 而探测 goroutine 自己会 destroy 同一个上下文。两者若不在同一把锁下串行，
// 快照晚用的那一步就会对已释放句柄调用 Zhulong_engine_stop。
// 用空句柄（不碰 C 内存）只验证 destroyed 标志的读写被串行化 —— 裸读写会被 -race 抓到。
func TestProbeContextStopDestroyAreSerialized(t *testing.T) {
	for iteration := 0; iteration < 200; iteration++ {
		pCtx := &probeContext{}

		var both sync.WaitGroup
		both.Add(2)
		go func() {
			defer both.Done()
			pCtx.stop()
		}()
		go func() {
			defer both.Done()
			pCtx.destroy()
		}()
		both.Wait()

		if !pCtx.destroyed {
			t.Fatalf("iteration %d: destroy must mark the context as destroyed", iteration)
		}
	}
}
