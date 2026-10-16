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
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

var (
	ErrStreamHubClosed      = errors.New("stream hub is closed")
	ErrCameraDisabled       = errors.New("camera is disabled")
	ErrCameraStreamNotFound = errors.New("camera stream not found")
)

// StreamHub 管理全系统的摄像机媒体分发器生命周期与按需订阅。
type StreamHub struct {
	engine   MediaEngine
	store    CameraStore
	cipher   *LazyCipher
	registry *StateRegistry
	eventHub *EventHub
	logger   *zap.Logger

	aiConsumer     AIResultConsumer
	mu             sync.Mutex
	dispatchers    map[string]*StreamDispatcher
	aiStreams      map[string]*aiStreamRef
	aiSubscribers  map[string]map[string]struct{}
	closed         bool
	nextConsumerID atomic.Uint64
	flight         singleflight.Group
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

// SetAIConsumer registers an optional consumer for AI inference results.
func (h *StreamHub) SetAIConsumer(consumer AIResultConsumer) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.aiConsumer = consumer
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
		logger:        logger.Named("stream_hub"),
		dispatchers:   make(map[string]*StreamDispatcher),
		aiStreams:     make(map[string]*aiStreamRef),
		aiSubscribers: make(map[string]map[string]struct{}),
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

// GetOrCreateDispatcher 获取或按需拉起指定摄像机流的分发器。
func (h *StreamHub) GetOrCreateDispatcher(ctx context.Context, cameraID, role string) (*StreamDispatcher, error) {
	key := fmt.Sprintf("%s:%s", cameraID, role)

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
		h.logger.Error("decrypt stream uri failed",
			zap.String("camera_id", cameraID),
			zap.String("role", role),
			zap.Error(err),
		)
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
	key := fmt.Sprintf("%s:%s", cameraID, role)
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.dispatchers, key)
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
		if err == nil && aiSub != nil {
			var ctx context.Context
			ctx, cancel = context.WithCancel(context.Background())
			go h.consumeAIResults(ctx, cameraID, aiSub)
		} else if err != nil {
			h.logger.Warn("subscribe AI results for camera failed",
				zap.String("camera_id", cameraID), zap.Error(err))
		}

		h.mu.Lock()
		if h.closed {
			h.mu.Unlock()
			if cancel != nil {
				cancel()
			}
			if aiSub != nil {
				_ = aiSub.Close()
			}
			if closeErr := mediaStream.Close(); closeErr != nil {
				h.logger.Warn("close AI stream after hub shutdown",
					zap.String("camera_id", cameraID), zap.Error(closeErr))
			}
			return "", ErrStreamHubClosed
		}
		h.aiStreams[cameraID] = &aiStreamRef{stream: mediaStream, aiSub: aiSub, cancel: cancel}
		h.mu.Unlock()

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
	h.mu.Unlock()

	if toClose != nil {
		if err := toClose.Close(); err != nil {
			h.logger.Warn("release AI stream after last subscriber left",
				zap.String("camera_id", cameraID), zap.Error(err))
		}
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

func (h *StreamHub) consumeAIResults(ctx context.Context, cameraID string, sub *engine.AIResultSubscription) {
	defer sub.Close()
	for {
		data, err := sub.Next(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) && !errors.Is(err, engine.ErrClosed) {
				h.logger.Debug("ai results consumer exiting",
					zap.String("camera_id", cameraID), zap.Error(err))
			}
			return
		}

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
		h.mu.Unlock()

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
