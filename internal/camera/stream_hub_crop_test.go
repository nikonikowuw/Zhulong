package camera

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/region"
	"go.uber.org/zap"
)

// stubCropProvider 模拟 task.Store.CropFor：只读、可注入错误、记录调用次数。
type stubCropProvider struct {
	mu    sync.Mutex
	crops map[string]*region.Rect
	err   error
	calls int
}

func (p *stubCropProvider) CropFor(_ context.Context, cameraID string) (*region.Rect, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	rect, ok := p.crops[cameraID]
	if !ok {
		return nil, nil
	}
	copied := *rect
	return &copied, nil
}

func (p *stubCropProvider) set(cameraID string, rect *region.Rect) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if rect == nil {
		delete(p.crops, cameraID)
		return
	}
	p.crops[cameraID] = rect
}

func (p *stubCropProvider) setErr(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *stubCropProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func cropRect(x1, y1, x2, y2 float64) *region.Rect {
	return &region.Rect{x1, y1, x2, y2}
}

// newCropTestHub 构造一个只关心 AI 流视图的 Hub。
func newCropTestHub(t *testing.T, engine MediaEngine, provider TaskCropProvider) (*StreamHub, CameraStore, *LazyCipher, string, func()) {
	t.Helper()
	store, cipher, camID, cleanup := setupTestStoreAndCipher(t)
	hub := NewStreamHub(engine, store, cipher, nil, nil, zap.NewNop())
	if provider != nil {
		hub.SetTaskCropProvider(provider)
	}
	return hub, store, cipher, camID, cleanup
}

// TestAIStreamAppliesCropBeforePublish 是「首帧不能错映射」的硬约束：
// 视图必须在函数返回前生效，而不是等某个后台刷新。
func TestAIStreamAppliesCropBeforePublish(t *testing.T) {
	mockEngine := newMockMediaEngine()
	provider := &stubCropProvider{crops: map[string]*region.Rect{}}

	hub, _, _, camID, cleanup := newCropTestHub(t, mockEngine, provider)
	defer cleanup()
	defer hub.Close()
	provider.set(camID, cropRect(0.1, 0.2, 0.6, 0.8))

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	hub.mu.Lock()
	ref := hub.aiStreams[camID]
	hub.mu.Unlock()
	if ref == nil {
		t.Fatal("expected AI stream to be registered")
	}
	state, err := ref.stream.GetView(context.Background())
	if err != nil {
		t.Fatalf("get view: %v", err)
	}
	if state.Rect != (region.Rect{0.1, 0.2, 0.6, 0.8}) {
		t.Fatalf("expected crop to be applied, got %+v", state.Rect)
	}
	if state.Since.IsZero() {
		t.Fatal("expected a nonzero effective-since after applying crop")
	}
}

// TestRefreshTaskCropAppliesNewWindow 覆盖「保存即生效」：不重启流，只换窗口。
func TestRefreshTaskCropAppliesNewWindow(t *testing.T) {
	mockEngine := newMockMediaEngine()
	provider := &stubCropProvider{crops: map[string]*region.Rect{}}

	hub, _, _, camID, cleanup := newCropTestHub(t, mockEngine, provider)
	defer cleanup()
	defer hub.Close()
	provider.set(camID, cropRect(0.0, 0.0, 0.5, 0.5))

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	provider.set(camID, cropRect(0.25, 0.25, 0.75, 0.75))
	hub.RefreshTaskCrop(context.Background(), camID)

	hub.mu.Lock()
	ref := hub.aiStreams[camID]
	hub.mu.Unlock()
	state, err := ref.stream.GetView(context.Background())
	if err != nil {
		t.Fatalf("get view: %v", err)
	}
	if state.Rect != (region.Rect{0.25, 0.25, 0.75, 0.75}) {
		t.Fatalf("expected refreshed crop, got %+v", state.Rect)
	}

	// 清除：provider 返回 nil 也必须真的传 NULL，而不是保留旧窗口。
	provider.set(camID, nil)
	hub.RefreshTaskCrop(context.Background(), camID)
	state, err = ref.stream.GetView(context.Background())
	if err != nil {
		t.Fatalf("get view after clear: %v", err)
	}
	if state.Rect != region.Identity() {
		t.Fatalf("expected identity after clear, got %+v", state.Rect)
	}
}

// TestCropNotReadyKeepsStreamUsable 覆盖「配置已登记、尺寸还没有」：
// 不能因为 NOT_READY 让 Resolve 失败（那样算法包直接起不来）。
func TestCropNotReadyKeepsStreamUsable(t *testing.T) {
	mockEngine := newMockMediaEngine()
	provider := &stubCropProvider{crops: map[string]*region.Rect{}}

	hub, _, _, camID, cleanup := newCropTestHub(t, mockEngine, provider)
	defer cleanup()
	defer hub.Close()
	provider.set(camID, cropRect(0.2, 0.2, 0.8, 0.8))

	mockEngine.mu.Lock()
	mockEngine.nextStream = &mockMediaStream{
		source:       newMockPacketSource(4),
		viewNotReady: true,
	}
	mockEngine.mu.Unlock()

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve must succeed even when crop cannot be applied yet: %v", err)
	}

	hub.mu.Lock()
	ref := hub.aiStreams[camID]
	hub.mu.Unlock()
	ref.viewMu.Lock()
	pending := ref.viewPending
	ref.viewMu.Unlock()
	if !pending {
		t.Fatal("expected viewPending to be set on NOT_READY")
	}

	// 首个解码帧到达后由 RefreshTaskCrop 落地（真实路径由下一次保存/订阅触发）。
	mockEngine.mu.Lock()
	stream := mockEngine.streams[0]
	mockEngine.mu.Unlock()
	stream.mu.Lock()
	stream.viewNotReady = false
	stream.mu.Unlock()
	hub.RefreshTaskCrop(context.Background(), camID)

	state, err := ref.stream.GetView(context.Background())
	if err != nil {
		t.Fatalf("get view: %v", err)
	}
	if state.Rect != (region.Rect{0.2, 0.2, 0.8, 0.8}) {
		t.Fatalf("expected deferred crop to be applied, got %+v", state.Rect)
	}
}

// TestCropProviderFailureKeepsCurrentView 守住「读不到 ≠ 清空」。
func TestCropProviderFailureKeepsCurrentView(t *testing.T) {
	mockEngine := newMockMediaEngine()
	provider := &stubCropProvider{crops: map[string]*region.Rect{}}

	hub, _, _, camID, cleanup := newCropTestHub(t, mockEngine, provider)
	defer cleanup()
	defer hub.Close()
	provider.set(camID, cropRect(0.1, 0.1, 0.9, 0.9))

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}

	provider.setErr(errors.New("db unavailable"))
	hub.RefreshTaskCrop(context.Background(), camID)

	hub.mu.Lock()
	ref := hub.aiStreams[camID]
	hub.mu.Unlock()
	state, err := ref.stream.GetView(context.Background())
	if err != nil {
		t.Fatalf("get view: %v", err)
	}
	if state.Rect != (region.Rect{0.1, 0.1, 0.9, 0.9}) {
		t.Fatalf("provider failure must not clear the view, got %+v", state.Rect)
	}
}

// TestReleaseClearsView 覆盖「流交回引擎池前必须清视图」：
// 否则下一台无 crop 的相机会继承上一任的窗口，且完全静默。
func TestReleaseClearsView(t *testing.T) {
	mockEngine := newMockMediaEngine()
	provider := &stubCropProvider{crops: map[string]*region.Rect{}}

	hub, _, _, camID, cleanup := newCropTestHub(t, mockEngine, provider)
	defer cleanup()
	defer hub.Close()
	provider.set(camID, cropRect(0.3, 0.3, 0.7, 0.7))

	if _, err := hub.ResolveAIStreamBrokerPath(context.Background(), camID); err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	hub.AttachAIStreamSubscriber(camID, "algo")
	hub.mu.Lock()
	ref := hub.aiStreams[camID]
	hub.mu.Unlock()

	hub.ReleaseAIStreamSubscriber(camID, "algo")

	// Release 之后流已经 Close，GetView 会返回 NOT_FOUND；直接看 mock 的内部状态：
	// 关键是清除发生在 Close 之前（否则流被复用时窗口会泄漏给下一台相机）。
	stream, ok := ref.stream.(*mockMediaStream)
	if !ok {
		t.Fatalf("unexpected stream type %T", ref.stream)
	}
	stream.mu.Lock()
	view := stream.view
	closed := stream.closed
	stream.mu.Unlock()
	if !closed {
		t.Fatal("expected stream to be closed after release")
	}
	if view.Rect != region.Identity() {
		t.Fatalf("expected identity after release, got %+v", view.Rect)
	}
}

// TestCropViewForSelectsGeneration 覆盖逆映射的窗口选择：
// 早于切换点的结果用上一代，晚于的用当前，零切换点视为全幅。
func TestCropViewForSelectsGeneration(t *testing.T) {
	ref := newAIStreamRef(&mockMediaStream{})
	if got := ref.viewFor(time.UnixMilli(1000)); got != region.Identity() {
		t.Fatalf("expected identity before any change, got %+v", got)
	}

	first := region.ViewState{Rect: region.Rect{0.1, 0.1, 0.5, 0.5}, Since: time.UnixMilli(2000)}
	ref.updateView(first)
	second := region.ViewState{Rect: region.Rect{0.2, 0.2, 0.8, 0.8}, Since: time.UnixMilli(3000)}
	ref.updateView(second)

	if got := ref.viewFor(time.UnixMilli(2500)); got != first.Rect {
		t.Fatalf("old frame must use previous view, got %+v", got)
	}
	if got := ref.viewFor(time.UnixMilli(3000)); got != second.Rect {
		t.Fatalf("frame at since must use current view, got %+v", got)
	}
	if got := ref.viewFor(time.UnixMilli(4000)); got != second.Rect {
		t.Fatalf("new frame must use current view, got %+v", got)
	}

	// 倒退保护：since=0 的旧快照不得把当前窗口清回全幅。
	ref.updateView(region.ViewState{Rect: region.Identity()})
	if got := ref.viewFor(time.UnixMilli(4000)); got != second.Rect {
		t.Fatalf("stale zero-since update must be ignored, got %+v", got)
	}
	// 同理：并发读到的更旧切换点也不得让窗口退代。
	ref.updateView(region.ViewState{Rect: region.Rect{0.9, 0.9, 1, 1}, Since: time.UnixMilli(2500)})
	if got := ref.viewFor(time.UnixMilli(4000)); got != second.Rect {
		t.Fatalf("older switch point must not roll the view back, got %+v", got)
	}
}

// TestNeedsViewPollTracksPendingAndWindow 覆盖「未裁剪的相机不付结果路径的 cgo」。
//
// 轮询的价值在两种引擎侧变化：延迟生效（NOT_READY 后首帧到）与分辨率热切换。
// 两者都以「配过窗口」为前提，所以全幅且无 pending 的路由必须完全退出轮询。
func TestNeedsViewPollTracksPendingAndWindow(t *testing.T) {
	ref := newAIStreamRef(&mockMediaStream{})
	if ref.needsViewPoll() {
		t.Fatal("a stream with no crop must not poll the engine per result")
	}

	// NOT_READY：配置已登记、引擎还没认账，只能靠结果路径的轮询把它捞回来。
	ref.setViewPending(true)
	if !ref.needsViewPoll() {
		t.Fatal("a pending view must be polled until the engine reports it")
	}
	// 零切换点的旧快照不得清 pending，否则轮询会提前停掉，延迟生效的窗口永远不落地。
	ref.updateView(region.ViewState{Rect: region.Identity()})
	if !ref.needsViewPoll() {
		t.Fatal("a stale zero-since snapshot must not clear the pending flag")
	}

	// 引擎认账后：窗口非全幅，继续轮询（热切换只有引擎知道）。
	ref.updateView(region.ViewState{Rect: region.Rect{0.1, 0.1, 0.5, 0.5}, Since: time.UnixMilli(2000)})
	if !ref.needsViewPoll() {
		t.Fatal("a cropped stream must keep polling for engine-side window changes")
	}

	// 清空裁剪后回到零开销路径。
	ref.updateView(region.ViewState{Rect: region.Identity(), Since: time.UnixMilli(3000)})
	if ref.needsViewPoll() {
		t.Fatal("clearing the crop must stop the per-result poll")
	}
}
