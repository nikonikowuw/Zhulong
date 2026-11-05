package camera

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/region"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var (
	ErrStreamHubClosed      = errors.New("stream hub is closed")
	ErrCameraDisabled       = errors.New("camera is disabled")
	ErrCameraStreamNotFound = errors.New("camera stream not found")
	// ErrAIStreamInactive 表示该相机当前没有活跃的 AI 分析流，因而没有可用的
	// 解码会话可供抓拍。这是暂时状态（任务停用 / 算法未启动），不是配置错误。
	ErrAIStreamInactive = errors.New("camera has no active AI stream")

	// ErrStreamRecovering 表示引擎交回的是一路**已经失败**的物理流。
	//
	// Native 的 acquire 按 URL 复用：只要旧流还有消费者或仍处于 8 秒宽限期，
	// 它就把同一具失败流原样还回来。把这种引用当成新流交付，等于向调用方
	// 承诺一个永远不会出帧的 broker 路径，所以当场拒绝并交给重连协程接管。
	ErrStreamRecovering = errors.New("stream is recovering from a failure")
)

// defaultMaxWarmStreamDispatchers 是空闲热保留窗口内允许同时存活的分发器数量上限。
//
// 每条热流都还在拉 RTSP、跑包泵、占着 gopCache（单路上限 4 MiB）。若不设上限，
// 「把 16 个机位挨个看过一遍」就等于「16 条流常驻 10 分钟」—— 边缘设备的带宽与内存
// 里没有这笔预算。超上限时按最早进入空闲的顺序立即释放（走 Native 8s grace）。
const defaultMaxWarmStreamDispatchers = 4

// StreamHub 管理全系统的摄像机媒体分发器生命周期与按需订阅。
type StreamHub struct {
	aiConsumer       AIResultConsumer
	store            CameraStore
	flight           singleflight.Group
	engine           MediaEngine
	regionProvider   RegionConfigProvider
	taskCropProvider TaskCropProvider
	registry         *StateRegistry
	logger           *zap.Logger
	eventHub         *EventHub
	dispatchers      map[string]*StreamDispatcher
	aiStreams        map[string]*aiStreamRef
	aiSubscribers    map[string]map[string]struct{}
	// warmOrder 记录处于空闲热保留窗口的分发器 key，最早进入的在前。
	warmOrder     []string
	warmStreamCap int
	cipher        *LazyCipher
	// recoveries 记录每路相机当前在跑的重连协程（按 cameraID）。
	recoveries map[string]*aiRecovery
	// recoveryCtx 是所有重连协程的根上下文，Hub 关闭时统一取消。
	recoveryCtx    context.Context
	cancelRecovery context.CancelFunc
	// reconnectMinDelay / reconnectMaxDelay / recoveryGrace 是重连节奏；
	// 字段化以便测试缩短，生产默认值不变（见 reconnect.go 的常量说明）。
	reconnectMinDelay  time.Duration
	reconnectMaxDelay  time.Duration
	recoveryGrace      time.Duration
	streamReadyTimeout time.Duration
	recoverySeq        uint64
	nextConsumerID     atomic.Uint64
	mu                 sync.Mutex
	closed             bool
}

// aiStreamRef 表示一路相机的 AI 分析流（ConsumerAI 物理拉流 + FrameBroker）。
// 其生命周期由 aiSubscribers 的订阅者集合驱动：任一订阅者存在则保活，全部撤销后才释放。
type aiStreamRef struct {
	stream MediaStream
	aiSub  *engine.AIResultSubscription
	cancel context.CancelFunc

	// applySem（容量 1）串行化「读 provider + SetView」这一个复合动作。
	// 不加它，RefreshTaskCrop（读到 v2）与 existing-ref 快路径（读到 v1）可能倒序生效，
	// 让视图回退到旧配置。串行后「最后写入」必然对应「最后读取」。
	//
	// 刻意用 channel 而不是 sync.Mutex：等锁必须能被 ctx 取消。保存路径带着 200ms
	// 预算进来，Mutex 等锁那段时间不受预算约束——前一次 apply 卡在慢 SQLite 读上时，
	// 「保存不等媒体层」就成了一句空话。
	applySem chan struct{}

	// viewMu 保护视图快照。view 是引擎回报的生效 E；viewPrev 只保留上一代，
	// 用于把换窗前已发布的在途帧映射回正确的全幅坐标。
	viewMu      sync.Mutex
	view        region.ViewState
	viewPrev    region.Rect
	viewPending bool
}

// newAIStreamRef 构造一个视图为全幅的 ref。
func newAIStreamRef(stream MediaStream) *aiStreamRef {
	return &aiStreamRef{
		stream:   stream,
		applySem: make(chan struct{}, 1),
		view:     region.ViewState{Rect: region.Identity()},
		viewPrev: region.Identity(),
	}
}

// viewFor 选出生效窗口：早于切换点用上一代，否则用当前。
func (r *aiStreamRef) viewFor(timestamp time.Time) region.Rect {
	r.viewMu.Lock()
	defer r.viewMu.Unlock()
	if r.view.Since.IsZero() {
		return r.view.Rect
	}
	if timestamp.Before(r.view.Since) {
		return r.viewPrev
	}
	return r.view.Rect
}

// updateView 吸收引擎回报的生效窗口。
//
// 单调性防护：引擎的切换点只会前进（`advance_since_locked` 取 max(now, last+1)）。
// 零切换点 = 「从未切换过」，更旧的切换点 = 并发读到的旧值；两者都不得覆盖已知状态，
// 否则视图会静默退回旧窗口，整批结果按错的映射落库。
//
// ⚠️ 被忽略的快照**不清 viewPending**：NOT_READY 之后的首帧还没到，清了就再也没人去捞它。
func (r *aiStreamRef) updateView(state region.ViewState) {
	r.viewMu.Lock()
	defer r.viewMu.Unlock()
	stale := state.Since.IsZero() || state.Since.Before(r.view.Since)
	if stale && (r.viewPending || !r.view.Since.IsZero()) {
		return
	}
	r.viewPending = false
	if state.Rect != r.view.Rect || !state.Since.Equal(r.view.Since) {
		r.viewPrev = r.view.Rect
		r.view = state
	}
}

// needsViewPoll 报告结果路径是否需要轮询引擎的生效窗口。
//
// 引擎只会在两种情况下自己改窗口：延迟生效（配了 crop 但首帧尺寸还没到）与分辨率热切换。
// 两者都以「本机配过窗口」为前提，所以未裁剪的相机可以完全省掉每条结果一次
// cgo + handle lease；裁剪过的相机必须继续轮询（热切换只有引擎知道）。
func (r *aiStreamRef) needsViewPoll() bool {
	r.viewMu.Lock()
	defer r.viewMu.Unlock()
	return r.viewPending || !r.view.Rect.IsIdentity()
}

func (r *aiStreamRef) setViewPending(pending bool) {
	r.viewMu.Lock()
	r.viewPending = pending
	r.viewMu.Unlock()
}

// AIResultConsumer consumes parsed AI inference results for persistence or business rules.
type AIResultConsumer interface {
	ProcessInferenceResult(ctx context.Context, res aitypes.InferenceResult) error
}

// RegionConfigProvider provides read access to the compiled region configuration for a given camera and algorithm.
type RegionConfigProvider interface {
	ConfigFor(cameraID, algorithmID string) (region.Config, bool)
}

// TaskCropProvider 提供任务级裁剪窗口（按相机）。
//
// 返回 (nil, nil) 表示「当前无 crop / 任务不存在 / 任务停用」；error 表示读库或解析失败，
// 调用方必须保留当前视图而不是误清。
type TaskCropProvider interface {
	CropFor(ctx context.Context, cameraID string) (*region.Rect, error)
}

// SetAIConsumer registers an optional consumer for AI inference results.
func (h *StreamHub) SetAIConsumer(consumer AIResultConsumer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.aiConsumer = consumer
}

// SetRegionConfigProvider registers an optional provider for region configurations.
func (h *StreamHub) SetRegionConfigProvider(provider RegionConfigProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.regionProvider = provider
}

// SetTaskCropProvider registers an optional provider for task-level crop windows.
func (h *StreamHub) SetTaskCropProvider(provider TaskCropProvider) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.taskCropProvider = provider
}

// NewStreamHub 构造全局流分发中心。
func NewStreamHub(
	engine MediaEngine,
	store CameraStore,
	cipher *LazyCipher,
	registry *StateRegistry,
	eventHub *EventHub,
	logger *zap.Logger,
) *StreamHub {
	if logger == nil {
		logger = zap.NewNop()
	}
	hub := &StreamHub{
		engine:             engine,
		store:              store,
		cipher:             cipher,
		registry:           registry,
		eventHub:           eventHub,
		logger:             logger.Named("camera.stream_hub"),
		dispatchers:        make(map[string]*StreamDispatcher),
		aiStreams:          make(map[string]*aiStreamRef),
		aiSubscribers:      make(map[string]map[string]struct{}),
		recoveries:         make(map[string]*aiRecovery),
		warmStreamCap:      defaultMaxWarmStreamDispatchers,
		reconnectMinDelay:  defaultReconnectMinDelay,
		reconnectMaxDelay:  defaultReconnectMaxDelay,
		recoveryGrace:      defaultRecoveryGrace,
		streamReadyTimeout: defaultStreamReadyTimeout,
	}
	hub.recoveryCtx, hub.cancelRecovery = context.WithCancel(context.Background())
	hub.nextConsumerID.Store(1000)
	return hub
}

// HasPacketActivitySource 报告该相机这一路流是否有 Go 侧可见的收包通道。
//
// 预览分发器在 Go 侧逐包可观测；AI 分析流只有推理结果，而结果是运动门控之后的产物，
// 不能当作「视频包还在到达」的证据（见 PacketActivitySource）。
func (h *StreamHub) HasPacketActivitySource(cameraID, role string) bool {
	h.mu.Lock()
	d, ok := h.dispatchers[streamKey(cameraID, role)]
	h.mu.Unlock()
	if !ok {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	return !d.closed
}

func (h *StreamHub) broadcastState(state *CameraStateInfo) {
	if h.eventHub != nil && state != nil {
		h.eventHub.BroadcastChange(state)
	}
}

func (h *StreamHub) recordPacketActivity(cameraID, role string) {
	if h.registry == nil {
		return
	}
	var wasAbnormal bool
	if st, ok := h.registry.GetState(cameraID); ok && st != nil {
		streamAbnormal := st.Streams != nil && st.Streams[role] != nil &&
			(st.Streams[role].Health != HealthStateOnline || st.Streams[role].Session == SessionStateReconnecting || st.Streams[role].Session == SessionStateError)
		if st.Health != HealthStateOnline || st.Session == SessionStateReconnecting || st.Session == SessionStateError || streamAbnormal {
			wasAbnormal = true
		}
	}

	state := h.registry.RecordStreamSuccess(cameraID, 0, role, EvidencePacketActivity)
	if wasAbnormal && state != nil {
		h.broadcastState(state)
	}
}

// streamKey 是分发器在 Hub 内的唯一标识：同一相机的主/子流各占一条。
func streamKey(cameraID, role string) string {
	return cameraID + ":" + role
}

// GetOrCreateDispatcher 获取或按需拉起指定摄像机流的分发器。
func (h *StreamHub) GetOrCreateDispatcher(ctx context.Context, cameraID, role string) (*StreamDispatcher, error) {
	key := streamKey(cameraID, role)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrStreamHubClosed
	}
	if d, ok := h.dispatchers[key]; ok {
		d.mu.Lock()
		isClosed := d.closed
		d.mu.Unlock()
		if !isClosed {
			h.mu.Unlock()
			return d, nil
		}
	}
	h.mu.Unlock()

	// 慢速路径：使用 singleflight 防并发击穿
	val, err, _ := h.flight.Do(key, func() (any, error) {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return nil, ErrStreamHubClosed
		}
		if d, ok := h.dispatchers[key]; ok {
			d.mu.Lock()
			isClosed := d.closed
			d.mu.Unlock()
			if !isClosed {
				h.mu.Unlock()
				return d, nil
			}
		}
		h.mu.Unlock()

		return h.createDispatcher(ctx, cameraID, role, key)
	})

	if err != nil {
		return nil, err
	}
	return val.(*StreamDispatcher), nil
}

// streamTarget 是一路相机流解析后的拉流目标。
//
// 抽出来是因为视频分发器与音频通道都要走「查相机 → 选 role → 解密 URI → 定传输协议」
// 这同一段；复制两份的代价不是多几十行，而是某天只改了其中一份。
type streamTarget struct {
	RawURI    string
	Transport engine.Transport
}

func (h *StreamHub) resolveStreamTarget(ctx context.Context, cameraID, role string) (streamTarget, error) {
	cam, err := h.store.GetByID(ctx, cameraID)
	if err != nil {
		return streamTarget{}, err
	}
	if cam == nil {
		return streamTarget{}, ErrCameraNotFound
	}
	if !cam.Enabled {
		return streamTarget{}, ErrCameraDisabled
	}

	var targetStream *CameraStream
	for i := range cam.Streams {
		if cam.Streams[i].Role == role {
			targetStream = &cam.Streams[i]
			break
		}
	}
	if targetStream == nil {
		return streamTarget{}, ErrCameraStreamNotFound
	}

	rawBytes, err := h.cipher.Decrypt(targetStream.EncryptedURI, MakeAAD(cameraID, role))
	if err != nil {
		return streamTarget{}, fmt.Errorf("decrypt stream uri: %w", err)
	}

	transport := engine.TransportTCP
	if targetStream.Transport == "udp" {
		transport = engine.TransportUDP
	}
	return streamTarget{RawURI: string(rawBytes), Transport: transport}, nil
}

func (h *StreamHub) createDispatcher(ctx context.Context, cameraID, role, key string) (*StreamDispatcher, error) {
	target, err := h.resolveStreamTarget(ctx, cameraID, role)
	if err != nil {
		return nil, err
	}

	consumerID := h.nextConsumerID.Add(1)
	mediaStream, err := h.engine.AcquireStream(ctx, target.RawURI, consumerID, engine.ConsumerPreview, engine.StreamOptions{
		Transport: target.Transport,
	})
	if err != nil {
		h.logger.Warn("acquire media stream failed",
			zap.String("camera_id", cameraID),
			zap.String("role", role),
			zap.Error(err),
		)
		return nil, err
	}

	sub, err := mediaStream.Subscribe(ctx, engine.SubscriptionOptions{})
	if err != nil {
		_ = mediaStream.Close()
		h.logger.Warn("subscribe media stream failed",
			zap.String("camera_id", cameraID),
			zap.String("role", role),
			zap.Error(err),
		)
		return nil, err
	}

	pumpCtx, cancelPump := context.WithCancel(context.Background())
	dispatcher := NewStreamDispatcher(h, cameraID, role, mediaStream, sub, cancelPump, h.logger)

	// 4. 并发二次检查并登记
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		dispatcher.Close()
		return nil, ErrStreamHubClosed
	}

	if existing, ok := h.dispatchers[key]; ok {
		existing.mu.Lock()
		isClosed := existing.closed
		existing.mu.Unlock()
		if !isClosed {
			h.mu.Unlock()
			dispatcher.Close() // 丢弃多余创建的分发器
			return existing, nil
		}
	}

	h.dispatchers[key] = dispatcher
	var newState *CameraStateInfo
	if h.registry != nil {
		newState = h.registry.UpdateSessionState(cameraID, role, SessionStateRunning)
	}
	h.mu.Unlock()

	if newState != nil {
		h.broadcastState(newState)
	}

	go dispatcher.RunPumpLoop(pumpCtx)

	h.logger.Info("media stream dispatcher started",
		zap.String("camera_id", cameraID),
		zap.String("role", role),
	)

	return dispatcher, nil
}

// AcquiredAudioStream 是音频通道专用的流引用与元数据快照。
type AcquiredAudioStream struct {
	Stream MediaStream
	Info   engine.AudioInfo
}

// AcquireAudioStream 为一条按需音频通道获取独立流引用。
//
// ⚠️ 刻意不复用视频分发器的 MediaStream：那个句柄上已经挂着视频订阅，而单句柄单订阅
// 是既有不变量。音频用**自己的消费者 ID + 自己的 Stream 句柄**，原生侧按 URL 去重，
// 因此不会多开一条 RTSP 连接，但音频订阅的任何故障（失败、背压、关流）都不会碰到
// 视频订阅与健康态 —— 这正是本任务最重要的一条隔离。
func (h *StreamHub) AcquireAudioStream(ctx context.Context, cameraID, role string) (*AcquiredAudioStream, error) {
	target, err := h.resolveStreamTarget(ctx, cameraID, role)
	if err != nil {
		return nil, err
	}

	consumerID := h.nextConsumerID.Add(1)
	mediaStream, err := h.engine.AcquireStream(ctx, target.RawURI, consumerID, engine.ConsumerPreview, engine.StreamOptions{
		Transport: target.Transport,
	})
	if err != nil {
		h.logger.Warn("acquire audio media stream failed",
			zap.String("camera_id", cameraID),
			zap.String("role", role),
			zap.Error(err),
		)
		return nil, err
	}

	info, err := waitAudioInfo(ctx, mediaStream)
	if err != nil {
		_ = mediaStream.Close()
		h.logger.Warn("query audio info failed",
			zap.String("camera_id", cameraID),
			zap.String("role", role),
			zap.Error(err),
		)
		return nil, err
	}
	return &AcquiredAudioStream{Stream: mediaStream, Info: info}, nil
}

// audioInfoReadyTimeout 是等流进入 running 后查询音频元数据的预算。
//
// acquire 是异步的：on_ready 之前音频元数据还是空的。这里必须等，否则「刚点开喇叭
// 就被告知无音频」这种误判会变成偶发现象，比彻底不支持更难排查。
const audioInfoReadyTimeout = 5 * time.Second

func waitAudioInfo(ctx context.Context, stream MediaStream) (engine.AudioInfo, error) {
	return waitAudioInfoWithin(ctx, stream, audioInfoReadyTimeout)
}

// waitAudioInfoWithin 带预算的版本：预览能力帧只需要一个很短的上限。
func waitAudioInfoWithin(ctx context.Context, stream MediaStream, timeout time.Duration) (engine.AudioInfo, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		info, err := stream.AudioInfo(ctx)
		if err == nil {
			return info, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return engine.AudioInfo{}, lastErr
		}
		select {
		case <-ctx.Done():
			return engine.AudioInfo{}, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func (h *StreamHub) removeDispatcher(cameraID, role string) {
	key := streamKey(cameraID, role)
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.dispatchers, key)
	h.dropWarmLocked(key)
}

// markStreamWarm 记录某路流进入空闲热保留窗口，并按上限回收最早进入空闲的那条。
//
// 必须在调用方释放分发器锁之后调用：回收会把受害者 Close 掉，而 Close 会回头拿 Hub 锁。
func (h *StreamHub) markStreamWarm(cameraID, role string) {
	key := streamKey(cameraID, role)

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.dropWarmLocked(key)
	h.warmOrder = append(h.warmOrder, key)
	var victim *StreamDispatcher
	if h.warmStreamCap > 0 && len(h.warmOrder) > h.warmStreamCap {
		victim = h.dispatchers[h.warmOrder[0]]
		h.warmOrder = h.warmOrder[1:]
	}
	h.mu.Unlock()

	if victim != nil {
		h.logger.Debug("warm stream cap exceeded, releasing idle dispatcher")
		victim.Close()
	}
}

// markStreamActive 把某路流移出空闲热保留集合（有客户端回来了）。
func (h *StreamHub) markStreamActive(cameraID, role string) {
	key := streamKey(cameraID, role)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropWarmLocked(key)
}

// dropWarmLocked 从空闲集合中移除指定 key，要求持有 h.mu。
func (h *StreamHub) dropWarmLocked(key string) {
	for index, existing := range h.warmOrder {
		if existing == key {
			h.warmOrder = append(h.warmOrder[:index], h.warmOrder[index+1:]...)
			return
		}
	}
}

// WarmStreams 返回当前处于空闲热保留窗口的流数量。
func (h *StreamHub) WarmStreams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.warmOrder)
}

// ActiveStreams 返回当前活跃流分发器数量。
func (h *StreamHub) ActiveStreams() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.dispatchers)
}

// AICameraIDs 返回当前存在 AI 订阅者或活跃 AI 流的全部相机 ID。
// 供上层解析流程收敛订阅关系使用（它需要看到自己已不再订阅的相机）。
func (h *StreamHub) AICameraIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	ids := make([]string, 0, len(h.aiSubscribers)+len(h.aiStreams))
	seen := make(map[string]struct{}, len(h.aiSubscribers)+len(h.aiStreams))
	for cameraID := range h.aiSubscribers {
		seen[cameraID] = struct{}{}
		ids = append(ids, cameraID)
	}
	for cameraID := range h.aiStreams {
		if _, ok := seen[cameraID]; ok {
			continue
		}
		ids = append(ids, cameraID)
	}
	sort.Strings(ids)
	return ids
}

// ResolveAIStreamID 解析指定相机 AI 分析流的引擎流编号。
//
// 先走 ResolveAIStreamBrokerPath 确保 AI 流已激活（它同时负责拉起拉流与 flight 去重），
// 再从已建立的引用上取编号：调用方拿到的编号与 broker socket 属于同一路流。
func (h *StreamHub) ResolveAIStreamID(ctx context.Context, cameraID string) (uint64, error) {
	if _, err := h.ResolveAIStreamBrokerPath(ctx, cameraID); err != nil {
		return 0, err
	}
	h.mu.Lock()
	ref, ok := h.aiStreams[cameraID]
	h.mu.Unlock()
	if !ok || ref.stream == nil {
		return 0, ErrStreamHubClosed
	}
	return ref.stream.StreamID(ctx)
}

// ResolveAIStreamBrokerPath 解析指定相机 AI 分析流的 FrameBroker socket 路径，
// 并在该流尚未激活时以 ConsumerAI 模式拉起物理拉流（固定 main 角色）。
//
// ⚠️ 本方法只负责「解析」，不建立订阅关系。调用方拿到路径后必须在同一次解析流程内
// 调用 AttachAIStreamSubscriber，否则该流会在下一次 ResolveAIStream 时被回收。
func (h *StreamHub) ResolveAIStreamBrokerPath(ctx context.Context, cameraID string) (string, error) {
	if err := h.waitForRecoveryGate(ctx, cameraID); err != nil {
		return "", err
	}
	return h.acquireAIStreamBrokerPath(ctx, cameraID)
}

// acquireAIStreamBrokerPath 是 ResolveAIStreamBrokerPath 的无门禁版本：
// 调用方（重连协程）自己已经等过宽限期，再走一遍门禁只会重复等待。
func (h *StreamHub) acquireAIStreamBrokerPath(ctx context.Context, cameraID string) (string, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", ErrStreamHubClosed
	}
	if ref, ok := h.aiStreams[cameraID]; ok {
		h.mu.Unlock()
		// 已存在的流也要跟一次配置：订阅者可能在任务保存后重连，
		// 而 RefreshTaskCrop 恰好落在释放/重建的空档里。
		// 代价是订阅路径上一次 task 读库（每台相机每次解析一次，频率跟着算法包重启走）；
		// 换成「只在 dirty 时读」需要额外维护配置版本号，当前规模不值得。
		h.applyTaskCrop(ctx, cameraID, ref)
		return ref.stream.BrokerPath(ctx)
	}
	h.mu.Unlock()

	flightKey := "ai:" + cameraID
	val, err, _ := h.flight.Do(flightKey, func() (any, error) {
		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			return "", ErrStreamHubClosed
		}
		if ref, ok := h.aiStreams[cameraID]; ok {
			h.mu.Unlock()
			h.applyTaskCrop(ctx, cameraID, ref)
			path, err := ref.stream.BrokerPath(ctx)
			return path, err
		}
		h.mu.Unlock()

		cam, err := h.store.GetByID(ctx, cameraID)
		if err != nil {
			return "", err
		}
		if cam == nil {
			return "", ErrCameraNotFound
		}
		if !cam.Enabled {
			return "", ErrCameraDisabled
		}

		role := StreamRoleMain
		var targetStream *CameraStream
		for _, s := range cam.Streams {
			if s.Role == role {
				targetStream = &s
				break
			}
		}
		if targetStream == nil {
			return "", ErrCameraStreamNotFound
		}

		rawBytes, err := h.cipher.Decrypt(targetStream.EncryptedURI, MakeAAD(cameraID, role))
		if err != nil {
			return "", fmt.Errorf("decrypt stream uri: %w", err)
		}
		rawURI := string(rawBytes)

		transport := engine.TransportTCP
		if targetStream.Transport == "udp" {
			transport = engine.TransportUDP
		}

		consumerID := h.nextConsumerID.Add(1)
		mediaStream, err := h.engine.AcquireStream(ctx, rawURI, consumerID, engine.ConsumerAI, engine.StreamOptions{
			Transport: transport,
		})
		if err != nil {
			return "", err
		}

		// 引擎按 URL 复用物理流：旧流仍在宽限期或还有其它消费者时，交回的可能是
		// 同一路已经失败的流。交给调用方前必须认出来，否则它会拿到一个永远不出帧的 broker。
		if status, failed := streamIsFailed(ctx, mediaStream); failed {
			if closeErr := mediaStream.Close(); closeErr != nil {
				h.logger.Warn("close failed AI stream after detection",
					zap.String("camera_id", cameraID), zap.Error(closeErr))
			}
			if status.Error != nil {
				return "", fmt.Errorf("%w: %v", ErrStreamRecovering, status.Error)
			}
			return "", ErrStreamRecovering
		}

		// 受理 ≠ 可用：等它真的跑起来再交付 broker 路径。
		if err := h.awaitStreamReady(ctx, mediaStream); err != nil {
			if closeErr := mediaStream.Close(); closeErr != nil {
				h.logger.Warn("close AI stream after it never became ready",
					zap.String("camera_id", cameraID), zap.Error(closeErr))
			}
			return "", err
		}

		// 视图必须在首个订阅建立前生效：算法包一订阅就可能立刻收到帧，
		// 迟一步应用等于允许若干帧按全幅坐标解码，后续逆映射会静默错位。
		ref := newAIStreamRef(mediaStream)
		h.applyTaskCrop(ctx, cameraID, ref)

		path, err := mediaStream.BrokerPath(ctx)
		if err != nil {
			if closeErr := mediaStream.Close(); closeErr != nil {
				h.logger.Warn("close AI stream after broker path failure",
					zap.String("camera_id", cameraID), zap.Error(closeErr))
			}
			return "", err
		}

		aiSub, err := mediaStream.SubscribeAIResults(context.Background())
		var cancel context.CancelFunc
		if err != nil {
			h.logger.Warn("subscribe AI results for camera failed",
				zap.String("camera_id", cameraID), zap.Error(err))
		}
		var consumeCtx context.Context
		if err == nil && aiSub != nil {
			consumeCtx, cancel = context.WithCancel(context.Background())
			ref.aiSub = aiSub
			ref.cancel = cancel
		}

		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if aiSub != nil {
				if closeSubErr := aiSub.Close(); closeSubErr != nil {
					h.logger.Warn("close AI subscription failed", zap.Error(closeSubErr))
				}
			}
			if closeErr := mediaStream.Close(); closeErr != nil {
				h.logger.Warn("close AI stream after hub shutdown",
					zap.String("camera_id", cameraID), zap.Error(closeErr))
			}
			return "", ErrStreamHubClosed
		}
		h.aiStreams[cameraID] = ref
		var newState *CameraStateInfo
		if h.registry != nil {
			newState = h.registry.UpdateSessionState(cameraID, role, SessionStateRunning)
		}
		h.mu.Unlock()

		if newState != nil {
			h.broadcastState(newState)
		}

		// 先注册再启动消费者：注册之前没有任何路径能拿到 ref，
		// 消费者一跑起来就必须能读到正确的窗口快照。
		if consumeCtx != nil {
			go h.consumeAIResults(consumeCtx, cameraID, ref, aiSub)
		}

		return path, nil
	})

	if err != nil {
		return "", err
	}
	return val.(string), nil
}

// AttachAIStreamSubscriber 登记一个 AI 流订阅者，使该相机的 AI 流在本次解析后保持存活。
// 同一 subscriberID 重复登记是幂等的。
func (h *StreamHub) AttachAIStreamSubscriber(cameraID, subscriberID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	subs := h.aiSubscribers[cameraID]
	if subs == nil {
		subs = make(map[string]struct{})
		h.aiSubscribers[cameraID] = subs
	}
	subs[subscriberID] = struct{}{}
}

// ReleaseAIStreamSubscriber 注销一个 AI 流订阅者；当该相机的订阅者集合清空时，
// 释放底层 ConsumerAI 拉流与 FrameBroker。
func (h *StreamHub) ReleaseAIStreamSubscriber(cameraID, subscriberID string) {
	var toClose MediaStream

	h.mu.Lock()
	if subs, ok := h.aiSubscribers[cameraID]; ok {
		delete(subs, subscriberID)
		if len(subs) == 0 {
			delete(h.aiSubscribers, cameraID)
			// 没人再要这一路流：立刻停掉在跑的重连，别为已停用的任务持续拉流。
			if rec, ok := h.recoveries[cameraID]; ok {
				rec.cancel()
				delete(h.recoveries, cameraID)
			}
			if ref, ok := h.aiStreams[cameraID]; ok {
				delete(h.aiStreams, cameraID)
				toClose = ref.stream
				if ref.cancel != nil {
					ref.cancel()
				}
				if ref.aiSub != nil {
					_ = ref.aiSub.Close()
				}
			}
		}
	}
	var newState *CameraStateInfo
	if toClose != nil && h.registry != nil {
		hasDispatcher := false
		for _, role := range []string{StreamRoleMain, StreamRoleSub} {
			if _, ok := h.dispatchers[cameraID+":"+role]; ok {
				hasDispatcher = true
				break
			}
		}
		if !hasDispatcher {
			newState = h.registry.UpdateSessionState(cameraID, StreamRoleMain, SessionStateIdle)
		}
	}
	h.mu.Unlock()

	if toClose != nil {
		// 清除视图必须发生在关闭之前：流交回引擎池后可能被无 crop 的相机复用，
		// 上一任的窗口会变成别人的默认值（而且是静默的）。
		clearCtx, clearCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer clearCancel()
		if _, err := toClose.SetView(clearCtx, nil); err != nil &&
			!errors.Is(err, engine.ErrNotFound) && !errors.Is(err, engine.ErrStreamNotReady) {
			h.logger.Warn("clear AI view before release failed",
				zap.String("camera_id", cameraID), zap.Error(err))
		}

		if err := toClose.Close(); err != nil {
			h.logger.Warn("release AI stream after last subscriber left",
				zap.String("camera_id", cameraID), zap.Error(err))
		}
	}

	if newState != nil {
		h.broadcastState(newState)
	}
}

// SetAIPublishRate 动态配置指定相机的 AI 帧发布速率上限。
// 相机当前没有活跃 AI 流时返回 nil（无发布通道可配置）。
func (h *StreamHub) SetAIPublishRate(ctx context.Context, cameraID string, fps uint32) error {
	h.mu.Lock()
	ref := h.aiStreams[cameraID]
	h.mu.Unlock()

	if ref == nil {
		return nil
	}
	return ref.stream.SetPublishRate(ctx, fps)
}

// RefreshTaskCrop 让指定相机的 AI 流重新读取并应用任务级 crop。
//
// 非阻塞语义：没有活跃 AI 流时直接返回（下一次 acquire 会带上最新配置）；
// 读库/应用失败只记日志，绝不让「保存任务」因为媒体层问题失败。
func (h *StreamHub) RefreshTaskCrop(ctx context.Context, cameraID string) {
	h.mu.Lock()
	ref := h.aiStreams[cameraID]
	h.mu.Unlock()
	if ref == nil {
		return
	}
	h.applyTaskCrop(ctx, cameraID, ref)
}

// applyTaskCrop 读取 provider 并把视图应用到流上。
//
// 并发口径分两层：applySem 串行化「读 + 写」保证「最后写入 = 最后读取」；等锁本身
// 吃调用方的 ctx——保存路径带着 200ms 预算进来，别人正在跑就直接放弃（Debug 留痕）。
// 放弃不会丢配置：每次 apply 都重读 provider，下一次 acquire / 保存会带上最新值。
func (h *StreamHub) applyTaskCrop(ctx context.Context, cameraID string, ref *aiStreamRef) {
	if ref == nil || ref.stream == nil {
		return
	}

	h.mu.Lock()
	provider := h.taskCropProvider
	logger := h.logger
	h.mu.Unlock()
	if provider == nil {
		return
	}
	if logger == nil {
		logger = zap.NewNop()
	}

	// ctx 没有 Done 通道时（Background/WithoutCancel）这一支永远不会就绪，等价于纯等锁。
	select {
	case ref.applySem <- struct{}{}:
		defer func() { <-ref.applySem }()
	case <-ctx.Done():
		logger.Debug("skip AI view refresh: another apply holds the slot",
			zap.String("camera_id", cameraID), zap.Error(ctx.Err()))
		return
	}

	crop, err := provider.CropFor(ctx, cameraID)
	if err != nil {
		logger.Warn("read task crop failed; keeping current AI view",
			zap.String("camera_id", cameraID), zap.Error(err))
		return
	}

	state, err := ref.stream.SetView(ctx, crop)
	if err != nil {
		switch {
		case errors.Is(err, engine.ErrStreamNotReady):
			// 配置已登记在引擎里，首个解码帧到达后自动生效。
			ref.setViewPending(true)
		case errors.Is(err, engine.ErrNotFound):
			// 流已失效：不冒错给调用方，后续 acquire 会重建并重新应用。
		default:
			logger.Warn("apply task crop failed; keeping current AI view",
				zap.String("camera_id", cameraID), zap.Error(err))
		}
		return
	}
	ref.updateView(state)
}

// AIStreamBrokerStats 返回该相机 AI 分析流上各算法包的帧投递统计快照。
//
// 用途：回答「这一路到底投给了谁、丢了多少」。FrameBroker 内部一直有 per-client 的
// in_flight / delivered / dropped 计数，但此前没有任何出口，宿主只能靠 CPU 占用猜。
//
// 语义（⚠️ 与 ResolveAIStreamBrokerPath 的区别）：
//   - 只读观测，**不会**为了查询而拉起拉流：没有活跃 AI 流时如实返回
//     ErrAIStreamInactive。
//   - 先在锁内取 ref 再在锁外调用原生接口 —— 与 Snapshot 一致；把原生调用留在锁内
//     会让一次统计查询阻塞所有相机的流操作。
//   - 第二个返回值是客户端**总数**，可能大于 len(clients)（截断）。
func (h *StreamHub) AIStreamBrokerStats(ctx context.Context, cameraID string) ([]engine.BrokerClientStats, uint32, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, 0, ErrStreamHubClosed
	}
	ref := h.aiStreams[cameraID]
	h.mu.Unlock()

	if ref == nil {
		return nil, 0, ErrAIStreamInactive
	}
	return ref.stream.BrokerClients(ctx)
}

// Snapshot 对指定相机的 AI 分析流执行抓拍（全幅 + 可选裁剪），返回 JPEG 字节流。
//
// 语义（⚠️ 调用方必须了解）：
//   - 若 options.PTS > 0，优先在 Native 历史环形缓存中精确匹配对应帧；未指定（PTS <= 0）
//     或历史已滑出时退回抓取当前最新的一帧。历史未命中时与结果之间存在固有延迟（实测约 1~4 帧）。
//   - 相机当前没有活跃 AI 流时返回 ErrAIStreamInactive，不自行拉起拉流：
//     默默拉流会引来无法解释的带宽消耗与运维困惑。
//   - 本调用同步执行 JPEG 编码（RK3568 1080p 约 35~60ms），必须在独立 goroutine
//     中调用，绝不能阻塞 AI 结果消费 goroutine。
func (h *StreamHub) Snapshot(ctx context.Context, cameraID string, options engine.SnapshotOptions) ([]engine.SnapshotImage, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil, ErrStreamHubClosed
	}
	ref := h.aiStreams[cameraID]
	h.mu.Unlock()

	if ref == nil {
		h.logger.Warn("StreamHub.Snapshot: no active AI stream for camera",
			zap.String("camera_id", cameraID))
		return nil, ErrAIStreamInactive
	}
	imgs, err := ref.stream.Snapshot(ctx, options)
	if err != nil {
		h.logger.Warn("StreamHub.Snapshot: stream.Snapshot failed",
			zap.String("camera_id", cameraID), zap.Error(err))
	}
	return imgs, err
}

// Close 停机时安全关闭所有分发器。
func (h *StreamHub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true

	list := make([]*StreamDispatcher, 0, len(h.dispatchers))
	for _, d := range h.dispatchers {
		list = append(list, d)
	}
	h.dispatchers = make(map[string]*StreamDispatcher)

	// 先停重连：否则停机过程中仍会有协程去 acquire 新流。
	if h.cancelRecovery != nil {
		h.cancelRecovery()
	}
	h.recoveries = make(map[string]*aiRecovery)

	aiList := make([]*aiStreamRef, 0, len(h.aiStreams))
	for _, ref := range h.aiStreams {
		aiList = append(aiList, ref)
	}
	h.aiStreams = make(map[string]*aiStreamRef)
	h.aiSubscribers = make(map[string]map[string]struct{})
	h.warmOrder = nil
	h.mu.Unlock()

	for _, d := range list {
		d.Close()
	}
	for _, ref := range aiList {
		if ref.cancel != nil {
			ref.cancel()
		}
		if ref.aiSub != nil {
			_ = ref.aiSub.Close()
		}
		if err := ref.stream.Close(); err != nil {
			h.logger.Warn("close AI stream on hub shutdown", zap.Error(err))
		}
	}

	h.logger.Info("stream hub stopped, all dispatchers closed")
	return nil
}

func (h *StreamHub) handleAIStreamFailure(cameraID string, err error) {
	h.mu.Lock()
	var toClose MediaStream
	if ref, ok := h.aiStreams[cameraID]; ok {
		delete(h.aiStreams, cameraID)
		toClose = ref.stream
		if ref.cancel != nil {
			ref.cancel()
		}
	}
	h.mu.Unlock()

	if toClose != nil {
		if closeErr := toClose.Close(); closeErr != nil {
			h.logger.Warn("close failed AI stream",
				zap.String("camera_id", cameraID), zap.Error(closeErr))
		}
	}

	if h.registry != nil {
		newState := h.registry.RecordStreamFailure(StreamFailureParams{
			CameraID:       cameraID,
			Role:           StreamRoleMain,
			Reason:         "ai_stream_failed",
			ErrorMessage:   err.Error(),
			IsMediaSession: true,
		})
		if newState != nil {
			h.broadcastState(newState)
		}
	}

	// 失败不会自愈是 Native 层的契约（无隐式重连），所以业务层必须接住：
	// 只要还有算法包想要这一路流，就按指数退避重建。
	h.scheduleAIRecovery(cameraID)
}

func (h *StreamHub) consumeAIResults(ctx context.Context, cameraID string, ref *aiStreamRef, sub *engine.AIResultSubscription) {
	defer sub.Close()
	for {
		data, err := sub.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, engine.ErrClosed) {
				// 错误可能来自上一代流：重连或任务同步已经重建了新流时，
				// 一条迟到的旧错误不得把刚建好的新流拆掉。
				if !h.aiSubscriptionIsCurrent(cameraID, sub) {
					h.logger.Debug("ignoring failure from a superseded AI stream subscription",
						zap.String("camera_id", cameraID), zap.Error(err))
					return
				}
				h.logger.Warn("ai stream failed",
					zap.String("camera_id", cameraID), zap.Error(err))
				h.handleAIStreamFailure(cameraID, err)
			}
			return
		}

		h.recordPacketActivity(cameraID, StreamRoleMain)

		// The AI stream is per-camera and shared by every algorithm runner attached to that
		// camera, so this hub cannot attribute a result to one algorithm. The runner is the
		// only party that knows, and it stamps algorithm_id into the payload (passed to it via
		// --algorithm-id); ParseInferenceResult prefers that payload value. The argument stays
		// empty on purpose: a guessed non-empty value would silently mis-attribute results
		// from other algorithms whenever the payload omits the field.
		res, err := aitypes.ParseInferenceResult(cameraID, "", data)
		if err != nil {
			h.logger.Warn("parse ai result failed",
				zap.String("camera_id", cameraID),
				zap.Error(err),
				zap.Int("payload_len", len(data)))
			continue
		}

		// 引擎是生效窗口的唯一权威：只有配置过窗口（或待生效）的路由才轮询快照，
		// 未裁剪的相机因此零 cgo 开销。轮询本身不能省：切换点之后仍可能有旧窗口帧到达
		// （发布线程与解码线程不是同一个），延迟生效与分辨率热切换也只有引擎知道。
		if ref.needsViewPoll() {
			if state, stateErr := ref.stream.GetView(ctx); stateErr == nil {
				ref.updateView(state)
			}
		}
		res = region.Reproject(res, ref.viewFor(res.Timestamp))

		h.mu.Lock()
		consumer := h.aiConsumer
		regionProv := h.regionProvider
		h.mu.Unlock()

		if regionProv != nil && res.AlgorithmID != "" {
			if cfg, ok := regionProv.ConfigFor(res.CameraID, res.AlgorithmID); ok && !cfg.IsEmpty() {
				res = cfg.Filter(res)
			}
		}

		if consumer != nil {
			if err := consumer.ProcessInferenceResult(ctx, res); err != nil {
				h.logger.Warn("process ai result by consumer failed",
					zap.String("camera_id", cameraID),
					zap.Error(err))
			}
		}

		if h.eventHub != nil {
			h.eventHub.BroadcastAIResult(res)
		}
	}
}
