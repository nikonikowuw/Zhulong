package camera

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

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
)

// defaultMaxWarmStreamDispatchers 是空闲热保留窗口内允许同时存活的分发器数量上限。
//
// 每条热流都还在拉 RTSP、跑包泵、占着 gopCache（单路上限 4 MiB）。若不设上限，
// 「把 16 个机位挨个看过一遍」就等于「16 条流常驻 10 分钟」—— 边缘设备的带宽与内存
// 里没有这笔预算。超上限时按最早进入空闲的顺序立即释放（走 Native 8s grace）。
const defaultMaxWarmStreamDispatchers = 4

// StreamHub 管理全系统的摄像机媒体分发器生命周期与按需订阅。
type StreamHub struct {
	aiConsumer     AIResultConsumer
	store          CameraStore
	flight         singleflight.Group
	engine         MediaEngine
	regionProvider RegionConfigProvider
	registry       *StateRegistry
	logger         *zap.Logger
	eventHub       *EventHub
	dispatchers    map[string]*StreamDispatcher
	aiStreams      map[string]*aiStreamRef
	aiSubscribers  map[string]map[string]struct{}
	// warmOrder 记录处于空闲热保留窗口的分发器 key，最早进入的在前。
	warmOrder      []string
	warmStreamCap  int
	cipher         *LazyCipher
	nextConsumerID atomic.Uint64
	mu             sync.Mutex
	closed         bool
}

// aiStreamRef 表示一路相机的 AI 分析流（ConsumerAI 物理拉流 + FrameBroker）。
// 其生命周期由 aiSubscribers 的订阅者集合驱动：任一订阅者存在则保活，全部撤销后才释放。
type aiStreamRef struct {
	stream MediaStream
	aiSub  *engine.AIResultSubscription
	cancel context.CancelFunc
}

// AIResultConsumer consumes parsed AI inference results for persistence or business rules.
type AIResultConsumer interface {
	ProcessInferenceResult(ctx context.Context, res aitypes.InferenceResult) error
}

// RegionConfigProvider provides read access to the compiled region configuration for a given camera and algorithm.
type RegionConfigProvider interface {
	ConfigFor(cameraID, algorithmID string) (region.Config, bool)
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
		engine:        engine,
		store:         store,
		cipher:        cipher,
		registry:      registry,
		eventHub:      eventHub,
		logger:        logger.Named("camera.stream_hub"),
		dispatchers:   make(map[string]*StreamDispatcher),
		aiStreams:     make(map[string]*aiStreamRef),
		aiSubscribers: make(map[string]map[string]struct{}),
		warmStreamCap: defaultMaxWarmStreamDispatchers,
	}
	hub.nextConsumerID.Store(1000)
	return hub
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
	var wasNonOnline bool
	if st, ok := h.registry.GetState(cameraID); ok && st != nil {
		if st.Health != HealthStateOnline || (st.Streams != nil && st.Streams[role] != nil && st.Streams[role].Health != HealthStateOnline) {
			wasNonOnline = true
		}
	}

	state := h.registry.RecordStreamSuccess(cameraID, 0, role, EvidencePacketActivity)
	if wasNonOnline && state != nil {
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

func (h *StreamHub) createDispatcher(ctx context.Context, cameraID, role, key string) (*StreamDispatcher, error) {
	// 1. 查询摄像机与流信息
	cam, err := h.store.GetByID(ctx, cameraID)
	if err != nil {
		return nil, err
	}
	if cam == nil {
		return nil, ErrCameraNotFound
	}
	if !cam.Enabled {
		return nil, ErrCameraDisabled
	}

	var targetStream *CameraStream
	for _, s := range cam.Streams {
		if s.Role == role {
			targetStream = &s
			break
		}
	}
	if targetStream == nil {
		return nil, ErrCameraStreamNotFound
	}

	// 2. 解密明文 RTSP 地址
	rawBytes, err := h.cipher.Decrypt(targetStream.EncryptedURI, MakeAAD(cameraID, role))
	if err != nil {
		return nil, fmt.Errorf("decrypt stream uri: %w", err)
	}
	rawURI := string(rawBytes)

	// 3. 准备底层拉流选项
	transport := engine.TransportTCP
	if targetStream.Transport == "udp" {
		transport = engine.TransportUDP
	}

	consumerID := h.nextConsumerID.Add(1)
	mediaStream, err := h.engine.AcquireStream(ctx, rawURI, consumerID, engine.ConsumerPreview, engine.StreamOptions{
		Transport: transport,
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

// ResolveAIStreamBrokerPath 解析指定相机 AI 分析流的 FrameBroker socket 路径，
// 并在该流尚未激活时以 ConsumerAI 模式拉起物理拉流（固定 main 角色）。
//
// ⚠️ 本方法只负责「解析」，不建立订阅关系。调用方拿到路径后必须在同一次解析流程内
// 调用 AttachAIStreamSubscriber，否则该流会在下一次 ResolveAIStream 时被回收。
func (h *StreamHub) ResolveAIStreamBrokerPath(ctx context.Context, cameraID string) (string, error) {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return "", ErrStreamHubClosed
	}
	if ref, ok := h.aiStreams[cameraID]; ok {
		path, err := ref.stream.BrokerPath(ctx)
		h.mu.Unlock()
		return path, err
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
			path, err := ref.stream.BrokerPath(ctx)
			h.mu.Unlock()
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
		if err == nil && aiSub != nil {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			go h.consumeAIResults(ctx, cameraID, aiSub)
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
		h.aiStreams[cameraID] = &aiStreamRef{stream: mediaStream, aiSub: aiSub, cancel: cancel}
		var newState *CameraStateInfo
		if h.registry != nil {
			newState = h.registry.UpdateSessionState(cameraID, role, SessionStateRunning)
		}
		h.mu.Unlock()

		if newState != nil {
			h.broadcastState(newState)
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
}

func (h *StreamHub) consumeAIResults(ctx context.Context, cameraID string, sub *engine.AIResultSubscription) {
	defer sub.Close()
	for {
		data, err := sub.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, engine.ErrClosed) {
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
