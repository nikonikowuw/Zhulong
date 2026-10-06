package camera

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
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
	logger   *zap.Logger

	mu             sync.Mutex
	dispatchers    map[string]*StreamDispatcher
	closed         bool
	nextConsumerID atomic.Uint64
}

// NewStreamHub 构造全局流分发中心。
func NewStreamHub(
	engine MediaEngine,
	store CameraStore,
	cipher *LazyCipher,
	registry *StateRegistry,
	logger *zap.Logger,
) *StreamHub {
	if logger == nil {
		logger = zap.NewNop()
	}
	hub := &StreamHub{
		engine:      engine,
		store:       store,
		cipher:      cipher,
		registry:    registry,
		logger:      logger.Named("stream_hub"),
		dispatchers: make(map[string]*StreamDispatcher),
	}
	hub.nextConsumerID.Store(1000)
	return hub
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
	if h.registry != nil {
		h.registry.UpdateSessionState(cameraID, role, SessionStateRunning)
	}
	h.mu.Unlock()

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
	h.mu.Unlock()

	for _, d := range list {
		d.Close()
	}

	h.logger.Info("stream hub stopped, all dispatchers closed")
	return nil
}
