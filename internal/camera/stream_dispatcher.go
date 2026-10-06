package camera

import (
	"context"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// StreamDispatcher 负责单路摄像机流的拉流消费、GOP 缓存与 Web 客户端扇出。
type StreamDispatcher struct {
	hub      *StreamHub
	cameraID string
	role     string
	logger   *zap.Logger

	mu             sync.Mutex
	subscribers    map[*StreamClient]struct{}
	cachedKeyFrame []byte

	stream     MediaStream
	sub        PacketSource
	cancelPump context.CancelFunc
	closed     bool
	closeOnce  sync.Once
}

// NewStreamDispatcher 构造单流分发器。
func NewStreamDispatcher(
	hub *StreamHub,
	cameraID string,
	role string,
	stream MediaStream,
	sub PacketSource,
	cancelPump context.CancelFunc,
	logger *zap.Logger,
) *StreamDispatcher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &StreamDispatcher{
		hub:         hub,
		cameraID:    cameraID,
		role:        role,
		logger:      logger,
		subscribers: make(map[*StreamClient]struct{}),
		stream:      stream,
		sub:         sub,
		cancelPump:  cancelPump,
	}
}

// RegisterClient 注册新的 WebSocket 订阅者；若存在最新关键帧缓存，立即下发首帧。
func (d *StreamDispatcher) RegisterClient(client *StreamClient) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		client.Close()
		return
	}

	d.subscribers[client] = struct{}{}
	cached := d.cachedKeyFrame
	count := len(d.subscribers)
	d.mu.Unlock()

	d.logger.Debug("client subscribed to stream",
		zap.String("camera_id", d.cameraID),
		zap.String("role", d.role),
		zap.Int("subscriber_count", count),
	)

	// 若存在关键帧缓存，立即投递第一帧实现秒开
	if len(cached) > 0 {
		select {
		case client.sendChan <- cached:
		default:
		}
	}
}

// UnregisterClient 注销 WebSocket 订阅者；若订阅者清零，自动触发延迟休眠与资源释放。
func (d *StreamDispatcher) UnregisterClient(client *StreamClient) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}

	delete(d.subscribers, client)
	remaining := len(d.subscribers)
	d.mu.Unlock()

	d.logger.Debug("client unsubscribed from stream",
		zap.String("camera_id", d.cameraID),
		zap.String("role", d.role),
		zap.Int("remaining_subscribers", remaining),
	)

	if remaining == 0 {
		d.Close()
	}
}

// Broadcast 封包底层视频帧并扇出给所有已连接客户端。
func (d *StreamDispatcher) Broadcast(pkt engine.Packet) {
	packed := PackPacket(pkt)

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}

	// 持续维护最新关键帧（含 SPS/PPS）供新进客户端首帧秒开
	if pkt.KeyFrame {
		d.cachedKeyFrame = packed
	}

	clients := make([]*StreamClient, 0, len(d.subscribers))
	for c := range d.subscribers {
		clients = append(clients, c)
	}
	d.mu.Unlock()

	for _, client := range clients {
		select {
		case client.sendChan <- packed:
		default:
			// 慢客户端背压流控：非关键帧优先丢弃以保低延迟
			if !pkt.KeyFrame {
				continue
			}
			// 关键帧若依然无法入队，判定对端为僵死/极慢连接，执行主动淘汰
			d.logger.Warn("slow consumer evicted due to buffer overflow",
				zap.String("camera_id", d.cameraID),
				zap.String("role", d.role),
			)
			go client.CloseWithReason(websocket.ClosePolicyViolation, "slow consumer evicted")
		}
	}
}

// RunPumpLoop 持续从底层包源读取数据并广播，直到上下文取消或流出错。
func (d *StreamDispatcher) RunPumpLoop(ctx context.Context) {
	if d.hub != nil && d.hub.registry != nil {
		newState := d.hub.registry.UpdateSessionState(d.cameraID, d.role, SessionStateRunning)
		if newState != nil {
			d.hub.broadcastState(newState)
		}
	}

	defer func() {
		d.Close()
	}()

	var lastReport time.Time
	for {
		pkt, err := d.sub.Next(ctx)
		if err != nil {
			d.logger.Debug("stream pump ended",
				zap.String("camera_id", d.cameraID),
				zap.String("role", d.role),
				zap.Error(err),
			)
			break
		}

		now := time.Now()
		if pkt.KeyFrame || now.Sub(lastReport) >= time.Second {
			lastReport = now
			if d.hub != nil {
				d.hub.recordPacketActivity(d.cameraID, d.role)
			}
		}

		d.Broadcast(pkt)
	}
}

// SubscriberCount 返回当前订阅客户端数量。
func (d *StreamDispatcher) SubscriberCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.subscribers)
}

// Close 关闭分发器并释放底层 Native 流。
func (d *StreamDispatcher) Close() {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		if d.cancelPump != nil {
			d.cancelPump()
		}

		clients := make([]*StreamClient, 0, len(d.subscribers))
		for c := range d.subscribers {
			clients = append(clients, c)
		}
		d.subscribers = nil
		d.cachedKeyFrame = nil
		d.mu.Unlock()

		// 断开所有挂载的客户端
		for _, c := range clients {
			c.Close()
		}

		// 释放底层订阅与流引用
		if d.sub != nil {
			_ = d.sub.Close()
		}
		if d.stream != nil {
			_ = d.stream.Close()
		}

		// 从全局 Hub 移除自身
		if d.hub != nil {
			d.hub.removeDispatcher(d.cameraID, d.role)
			if d.hub.registry != nil {
				newState := d.hub.registry.UpdateSessionState(d.cameraID, d.role, SessionStateIdle)
				if newState != nil {
					d.hub.broadcastState(newState)
				}
			}
		}

		d.logger.Info("stream dispatcher closed",
			zap.String("camera_id", d.cameraID),
			zap.String("role", d.role),
		)
	})
}
