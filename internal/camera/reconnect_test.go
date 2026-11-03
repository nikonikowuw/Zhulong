package camera

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// awaitCondition 轮询等待条件成立；超时直接失败。
//
// 重连是后台协程驱动的，测试必须等待而不是立即断言（与 hub 其它用例一致）。
func awaitCondition(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for !cond() {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for %s", what)
		case <-time.After(time.Millisecond):
		}
	}
}

// newRecoveryTestHub 构造一个把退避阶梯压到毫秒级的 Hub，便于测试真实的重连循环。
func newRecoveryTestHub(
	t *testing.T,
	engine MediaEngine,
	store CameraStore,
	cipher *LazyCipher,
	registry *StateRegistry,
) *StreamHub {
	t.Helper()
	hub := NewStreamHub(engine, store, cipher, registry, nil, zap.NewNop())
	hub.reconnectMinDelay = 20 * time.Millisecond
	hub.reconnectMaxDelay = 40 * time.Millisecond
	hub.recoveryGrace = 5 * time.Millisecond
	return hub
}

// TestStreamHubAIRecoveryReacquiresAfterFailure 覆盖现场故障的核心回归：
// 物理流失败后，只要还有算法包订阅，Hub 必须把 AI 流重新拉起来，会话回到 running。
func TestStreamHubAIRecoveryReacquiresAfterFailure(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	registry := NewStateRegistry()
	cam, err := store.GetByID(context.Background(), camID)
	if err != nil || cam == nil {
		t.Fatalf("failed to get camera: %v", err)
	}
	registry.InitCameraState(cam)

	mockEngine := newMockMediaEngine()
	hub := newRecoveryTestHub(t, mockEngine, store, cipher, registry)
	defer hub.Close()

	ctx := context.Background()
	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("initial resolve failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "face_recognition")

	// 模拟底层流失败：引擎把首条流标记为 FAILED 后，Hub 收到订阅错误。
	mockEngine.mu.Lock()
	first := mockEngine.streams[0]
	mockEngine.mu.Unlock()
	first.failed.Store(true)
	hub.handleAIStreamFailure(camID, errors.New("rtsp connection reset by peer"))

	awaitCondition(t, "AI stream to be re-acquired", 2*time.Second, func() bool {
		return mockEngine.getAcquireCount() == 2
	})

	// 新流必须真的挂在 aiStreams 上，而不是「acquire 完就丢」。
	hub.mu.Lock()
	ref, live := hub.aiStreams[camID]
	hub.mu.Unlock()
	if !live || ref == nil {
		t.Fatal("expected a live AI stream after recovery")
	}

	// 会话状态必须离开 reconnecting：否则前端会永久显示「视频流重连中」。
	awaitCondition(t, "session back to running", 2*time.Second, func() bool {
		st, ok := registry.GetState(camID)
		return ok && st != nil && st.Streams[StreamRoleMain] != nil &&
			st.Streams[StreamRoleMain].Session == SessionStateRunning
	})

	// 重连协程成功后退场，不能留下常驻循环。
	awaitCondition(t, "recovery worker to exit", 2*time.Second, func() bool {
		hub.mu.Lock()
		defer hub.mu.Unlock()
		return len(hub.recoveries) == 0
	})
}

// TestStreamHubResolveRejectsFailedStream 守住「不得把一具尸体当新流交付」：
// Native 在 8 秒宽限期内按 URL 复用失败流，Go 侧必须当场识别并退回。
func TestStreamHubResolveRejectsFailedStream(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	failed := &mockMediaStream{source: newMockPacketSource(4)}
	failed.failed.Store(true)

	mockEngine := newMockMediaEngine()
	mockEngine.nextStream = failed

	hub := newRecoveryTestHub(t, mockEngine, store, cipher, nil)
	defer hub.Close()

	_, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID)
	if !errors.Is(err, ErrStreamRecovering) {
		t.Fatalf("expected ErrStreamRecovering, got %v", err)
	}
	if !failed.closed {
		t.Fatal("expected the failed stream reference to be released immediately")
	}
	hub.mu.Lock()
	_, registered := hub.aiStreams[camID]
	hub.mu.Unlock()
	if registered {
		t.Fatal("a failed stream must never be registered as the active AI stream")
	}
}

// TestStreamHubAIRecoveryStopsWhenSubscribersLeave 确认停机/停用任务后不再拉流：
// 否则一台没人看的相机会被无限重连，持续烧带宽与解码会话。
func TestStreamHubAIRecoveryStopsWhenSubscribersLeave(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := newRecoveryTestHub(t, mockEngine, store, cipher, nil)
	defer hub.Close()

	ctx := context.Background()
	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("initial resolve failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "face_recognition")
	hub.handleAIStreamFailure(camID, errors.New("rtsp timeout"))

	// 任务停用 / 算法包被摘掉：订阅者清零必须立刻掐掉重连。
	hub.ReleaseAIStreamSubscriber(camID, "face_recognition")

	time.Sleep(200 * time.Millisecond)

	if got := mockEngine.getAcquireCount(); got != 1 {
		t.Fatalf("expected no re-acquire after the last subscriber left, got %d acquires", got)
	}
	hub.mu.Lock()
	pending := len(hub.recoveries)
	hub.mu.Unlock()
	if pending != 0 {
		t.Fatalf("expected no pending recovery, got %d", pending)
	}
}

// TestStreamHubReconnectDelayRespectsNativeGrace 守住退避阶梯的硬约束：
// 任何一轮都不得短于 Native 的 8 秒宽限期，且必须封顶。
func TestStreamHubReconnectDelayRespectsNativeGrace(t *testing.T) {
	hub := NewStreamHub(nil, nil, nil, nil, nil, zap.NewNop())
	hub.reconnectMinDelay = defaultReconnectMinDelay
	hub.reconnectMaxDelay = defaultReconnectMaxDelay

	previous := time.Duration(0)
	for attempt := 0; attempt < 8; attempt++ {
		delay := hub.reconnectDelay(attempt)
		if delay < nativeStreamGrace {
			t.Fatalf("attempt %d delay %v is inside the native grace period %v", attempt, delay, nativeStreamGrace)
		}
		if delay > time.Duration(float64(defaultReconnectMaxDelay)*(1+reconnectJitterFrac))+time.Millisecond {
			t.Fatalf("attempt %d delay %v exceeds the capped ceiling", attempt, delay)
		}
		// 抖动只在上限封顶前保留单调性；到达上限后每轮在 [max, max*(1+frac)] 内浮动。
		if attempt <= 2 && delay < previous {
			t.Fatalf("attempt %d delay %v is shorter than the previous %v", attempt, delay, previous)
		}
		previous = delay
	}

	// 首轮必须晚于宽限期，否则拿回的一定是同一具失败流。
	if hub.reconnectDelay(0) <= nativeStreamGrace {
		t.Fatal("first reconnect attempt must start after the native grace period")
	}
	// 阶梯必须真的爬升（首轮与上限之间要有中间档）。
	if hub.reconnectDelay(0) >= defaultReconnectMaxDelay {
		t.Fatal("first reconnect attempt must not already be at the ceiling")
	}
}

// TestStreamHubAISubscriptionGenerationGuard 守住「旧错误不得拆新流」：
// 只有当前挂在该相机上的那条订阅才有资格触发失败处理。
func TestStreamHubAISubscriptionGenerationGuard(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	current, _ := engine.NewAIResultSubscriptionForTest(4)
	stale, _ := engine.NewAIResultSubscriptionForTest(4)

	mockEngine := newMockMediaEngine()
	mockEngine.nextStream = &mockMediaStream{source: newMockPacketSource(4), aiSub: current}

	hub := newRecoveryTestHub(t, mockEngine, store, cipher, nil)
	defer hub.Close()

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	if !hub.aiSubscriptionIsCurrent(camID, current) {
		t.Fatal("the subscription installed on the live stream must be recognised as current")
	}
	if hub.aiSubscriptionIsCurrent(camID, stale) {
		t.Fatal("a subscription from a previous stream generation must be recognised as stale")
	}
}

// TestStreamHubAIRecoveryWaitsOutTheGracePeriod 覆盖「另一个消费者还握着死流」的场景：
// 引擎把同一具失败流交回来时必须当场退回，短等后重试，而不是把退避阶梯一次烧穿。
func TestStreamHubAIRecoveryWaitsOutTheGracePeriod(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	registry := NewStateRegistry()
	cam, err := store.GetByID(context.Background(), camID)
	if err != nil || cam == nil {
		t.Fatalf("failed to get camera: %v", err)
	}
	registry.InitCameraState(cam)

	mockEngine := newMockMediaEngine()
	hub := newRecoveryTestHub(t, mockEngine, store, cipher, registry)
	defer hub.Close()

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("initial resolve failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "face_recognition")

	// 宽限期内的下一具流：引擎会把同 URL 的失败流原样交回。
	corpse := &mockMediaStream{source: newMockPacketSource(4)}
	corpse.failed.Store(true)
	mockEngine.mu.Lock()
	mockEngine.nextStream = corpse
	mockEngine.mu.Unlock()

	hub.handleAIStreamFailure(camID, errors.New("rtsp connection reset by peer"))

	// 第三次 acquire（初次 + 尸体 + 真正的新流）才应该成功。
	awaitCondition(t, "a healthy stream to be acquired after the grace wait", 2*time.Second, func() bool {
		return mockEngine.getAcquireCount() == 3
	})
	if !corpse.closed {
		t.Fatal("the corpse handed back by the engine must be released immediately")
	}
	awaitCondition(t, "session back to running", 2*time.Second, func() bool {
		st, ok := registry.GetState(camID)
		return ok && st != nil && st.Streams[StreamRoleMain] != nil &&
			st.Streams[StreamRoleMain].Session == SessionStateRunning
	})
}

// TestStreamHubResolveWaitsForRecoveryGate 守住「算法包重启不得拿死 socket」：
// 重连窗口内的解析必须等宽限期结束再 acquire，而不是立刻失败。
//
// 现场对照：解析直接报错时，runner 拿不到新 broker 路径 → 退出 5 → 重启 →
// 再退出，三次之后进 CRASH_LOOP，视频流恢复了算法却停了。
func TestStreamHubResolveWaitsForRecoveryGate(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	mockEngine := newMockMediaEngine()
	hub := newRecoveryTestHub(t, mockEngine, store, cipher, nil)
	hub.recoveryGrace = 80 * time.Millisecond
	defer hub.Close()

	ctx := context.Background()
	if _, err := hub.ResolveAIStreamBrokerPath(ctx, camID); err != nil {
		t.Fatalf("initial resolve failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "face_recognition")
	hub.handleAIStreamFailure(camID, errors.New("rtsp connection reset by peer"))

	// 角色互换：此刻算法包在重启，直接来要路径。
	started := time.Now()
	path, err := hub.ResolveAIStreamBrokerPath(ctx, camID)
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("resolve during recovery must wait for the gate instead of failing: %v", err)
	}
	if path == "" {
		t.Fatal("expected a broker path")
	}
	if elapsed < 60*time.Millisecond {
		t.Fatalf("resolve returned after %v; it must wait out the recovery grace window", elapsed)
	}
	if got := mockEngine.getAcquireCount(); got < 2 {
		t.Fatalf("expected a fresh acquire, got %d", got)
	}
}

// TestStreamHubResolveRejectsStreamThatNeverBecomesReady 守住「受理 ≠ 可用」：
// acquire 只保证登记消费者，RTSP 可能还没连上。此时交付 broker 路径会让算法包
// 拿到一条几秒后才断的流，状态在 running/reconnecting 之间反复横跳。
func TestStreamHubResolveRejectsStreamThatNeverBecomesReady(t *testing.T) {
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	defer cleanup()

	pending := &mockMediaStream{
		source: newMockPacketSource(4),
		// 先 STARTING（受理），随后 FAILED（RTSP 连不上）。
		stateSeq: []engine.StreamState{engine.StreamStarting, engine.StreamStarting, engine.StreamFailed},
	}

	mockEngine := newMockMediaEngine()
	mockEngine.nextStream = pending

	hub := newRecoveryTestHub(t, mockEngine, store, cipher, nil)
	hub.streamReadyTimeout = 2 * time.Second
	defer hub.Close()

	_, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID)
	if !errors.Is(err, ErrStreamRecovering) {
		t.Fatalf("expected ErrStreamRecovering for a stream that never became ready, got %v", err)
	}
	if !pending.closed {
		t.Fatal("a stream that failed to become ready must be released")
	}
}
