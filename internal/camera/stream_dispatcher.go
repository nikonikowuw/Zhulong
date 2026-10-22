package camera

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// PTS 适合交给解码器呈现，但带 B 帧时会按解码包序回退。背压必须比较单调 DTS 的
// 最新输入进度与最近一次成功写入的进度，不能用相邻包 PTS 或入队时间冒充发送进度。
const (
	maxClientLagUS int64 = 500_000

	// A complete GOP must fit both the byte and frame bounds so a newly registered
	// client can always receive the same GOP retained by the cache.
	gopCacheMaxFrames = defaultClientBufferSize

	// 4 MiB 单路 GOP 缓存字节上限；帧数同时受客户端队列容量约束。
	gopCacheMaxBytes = 4 << 20
)

// gopCache 是从最近一个关键帧到当前时刻的**完整**帧窗口。
//
// 为什么必须是完整 GOP 而不是「最新关键帧单帧」：
// 新客户端只拿到一个 IDR 就直接接实时帧，中间缺掉的 P 帧会让参考链断裂，
// 外部表现就是「刚开始播放时卡几帧、花一下，然后才正常」，直到下一个 IDR 才自愈。
// 补齐全 GOP 多传几百 KB，换来的是确定性首帧。
type gopCache struct {
	frames   []outFrame
	bytes    int
	complete bool // 从 IDR 起无缺口；false 表示已因超限退化，不得用于补齐
}

// StreamDispatcher 负责单路摄像机流的拉流消费、GOP 缓存与 Web 客户端扇出。
type StreamDispatcher struct {
	hub      *StreamHub
	cameraID string
	role     string
	logger   *zap.Logger

	mu          sync.Mutex
	subscribers map[*StreamClient]struct{}
	gop         gopCache

	stream     MediaStream
	sub        PacketSource
	cancelPump context.CancelFunc
	closed     bool
	closeOnce  sync.Once

	// seq 是流级帧序号：即使某个客户端自己跳了帧，也能从 seq 空洞看出
	// 「是流本身丢了包」还是「是我这边主动丢的」，这是定位问题的关键分界。
	seq atomic.Uint32

	keyframeInjected atomic.Uint64
	lagResyncs       atomic.Uint64
	queueResyncs     atomic.Uint64
	resyncSkips      atomic.Uint64
	gopTruncations   atomic.Uint64
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

// RegisterClient 注册新的 WebSocket 订阅者，并在持有锁的情况下补齐整个 GOP。
//
// 补发必须在锁内完成，原因有二：
//  1. 与 Broadcast 互斥，保证「历史帧」与「实时帧」的先后顺序不错乱
//     —— 两个写者交错会破坏历史帧与实时帧的解码顺序和参考链；
//  2. 此时客户端尚未进入 subscribers，不会与 Broadcast 重复投递同一个 GOP 起点帧。
//
// 若队列装不下整个 GOP，宁可退化为「等下一个 IDR」也不发半截 GOP：
// 半截 GOP 等于断了参考链，正是「开始播放卡几帧」的病根。
func (d *StreamDispatcher) RegisterClient(client *StreamClient) {
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		client.Close()
		return
	}

	primed := 0
	if d.gop.complete && len(d.gop.frames) > 0 {
	prime:
		for _, frame := range d.gop.frames {
			select {
			case client.sendChan <- frame:
				if !client.hasProgressDTS {
					client.lastProgressDTSUS = frame.dtsUS
					client.hasProgressDTS = true
				}
				primed++
			default:
				// 队列装不下完整 GOP：整段放弃，从下一个 IDR 干净接入
				d.drainLocked(client)
				client.lastProgressDTSUS = 0
				client.hasProgressDTS = false
				client.needsKey = true
				primed = 0
				break prime
			}
		}
	} else {
		// 没有可用 GOP（刚建流或已因超限退化）
		client.needsKey = true
	}

	d.subscribers[client] = struct{}{}
	count := len(d.subscribers)
	d.mu.Unlock()

	d.logger.Debug("client subscribed to stream",
		zap.String("camera_id", d.cameraID),
		zap.String("role", d.role),
		zap.Int("subscriber_count", count),
		zap.Int("primed_frames", primed),
	)
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
//
// 丢帧策略（这是预览链路稳定性的核心）：
//   - 绝不用「丢掉中间某个 P 帧」的方式降延迟 —— H.264 的 P 帧绝大多数是参考帧，
//     丢一帧后面全花，直到下一个 IDR 才能恢复；
//   - 一旦某个客户端落后超过 maxClientLagUS，直接判定它需要重同步：
//     清空它的待发队列并只接收下一个 IDR，让它一次性追上而不是永远落后；
//   - 慢客户端不再被踢掉，而是自己降级为「只跟关键帧」。
//     断掉它反而制造重连风暴，边缘现场承受不起。
func (d *StreamDispatcher) Broadcast(pkt engine.Packet) {
	seq := d.seq.Add(1) - 1
	packed, header := packFrame(pkt, seq)

	if pkt.Flags.Has(engine.PacketFlagParameterSetsInjected) {
		d.keyframeInjected.Add(1)
	}

	frame := outFrame{
		data:  packed,
		ptsUS: header.PTSUS,
		dtsUS: header.DTSUS,
		frame: header.FrameClass,
		gopID: header.GOPID,
	}

	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return
	}

	d.appendGopLocked(frame, header)

	for client := range d.subscribers {
		d.deliverLocked(client, frame)
	}
	d.mu.Unlock()

	// 慢客户端不再淘汰：踢掉它等于制造一次重连风暴。改为降级为关键帧对齐。
}

// appendGopLocked 维护「从最近 IDR 到当前」的完整窗口供新客户端补齐。
func (d *StreamDispatcher) appendGopLocked(frame outFrame, header WireHeader) {
	if header.IsKeyFrame() {
		// 新 GOP：清空引用后复用底层数组容量，避免每次 GOP 重新分配
		clear(d.gop.frames)
		d.gop.frames = d.gop.frames[:0]
		d.gop.bytes = 0
		d.gop.complete = true
	}

	if !d.gop.complete {
		return
	}

	if d.gop.bytes+len(frame.data) > gopCacheMaxBytes ||
		len(d.gop.frames) >= gopCacheMaxFrames {
		// 缓存限制与客户端队列一致；任何标记为 complete 的 GOP 都能完整补发。
		// 这里不能只保留后半段 —— 没有 IDR 的片段对新客户端毫无价值。
		clear(d.gop.frames)
		d.gop.frames = d.gop.frames[:0]
		d.gop.bytes = 0
		d.gop.complete = false
		d.gopTruncations.Add(1)
		d.logger.Warn("gop cache truncated, preview cold start degrades to next IDR",
			zap.String("camera_id", d.cameraID),
			zap.String("role", d.role),
			zap.Int("limit_bytes", gopCacheMaxBytes),
			zap.Int("limit_frames", gopCacheMaxFrames),
		)
		return
	}

	d.gop.frames = append(d.gop.frames, frame)
	d.gop.bytes += len(frame.data)
}

// deliverLocked 在持有 d.mu 时向单个客户端投递一帧，必要时先执行重同步。
func (d *StreamDispatcher) deliverLocked(client *StreamClient, frame outFrame) {
	if client.needsKey {
		if frame.frame != WireFrameIDR {
			d.resyncSkips.Add(1)
			return
		}
		client.needsKey = false
	}

	// 判据是「最新输入帧 DTS - 最近一次成功 WebSocket 写入帧 DTS」而不是队列长度：队列长度只说明包数，
	// DTS 是解码包序的单调时间轴，可直接测量客户端实际发送进度；PTS 会因 B 帧重排而回退。
	if client.hasProgressDTS && frame.dtsUS > client.lastProgressDTSUS &&
		frame.dtsUS-client.lastProgressDTSUS > maxClientLagUS {
		d.drainLocked(client)
		client.hasProgressDTS = false
		client.needsKey = frame.frame != WireFrameIDR
		d.lagResyncs.Add(1)
		if client.needsKey {
			return
		}
	}

	select {
	case client.sendChan <- frame:
		if !client.hasProgressDTS {
			client.lastProgressDTSUS = frame.dtsUS
			client.hasProgressDTS = true
		}
	default:
		// 队列满：必然是消费端卡住（网络或浏览器解码），同样整段追赶
		d.drainLocked(client)
		client.hasProgressDTS = false
		client.needsKey = frame.frame != WireFrameIDR
		d.queueResyncs.Add(1)
		if client.needsKey {
			return
		}
		select {
		case client.sendChan <- frame:
			if !client.hasProgressDTS {
				client.lastProgressDTSUS = frame.dtsUS
				client.hasProgressDTS = true
			}
		default:
		}
	}
}

func (d *StreamDispatcher) markFrameWritten(client *StreamClient, frame outFrame) {
	d.mu.Lock()
	client.lastProgressDTSUS = frame.dtsUS
	client.hasProgressDTS = true
	d.mu.Unlock()
}

// drainLocked 丢弃客户端全部待发帧（非阻塞），为新一帧腾出空间。
func (d *StreamDispatcher) drainLocked(client *StreamClient) {
	for {
		select {
		case <-client.sendChan:
		default:
			return
		}
	}
}

// DispatchCounters 是单路预览分发器的累计观测计数快照。
type DispatchCounters struct {
	KeyframeInjected uint64 // 参数集注入次数
	LagResyncs       uint64 // 因超出时间预算触发的重同步
	QueueResyncs     uint64 // 因发送队列溢出触发的重同步
	ResyncSkips      uint64 // 重同步期间被跳过的非关键帧
	GOPTruncations   uint64 // GOP 缓存超限退化次数（相机 GOP 过长的信号）
	Subscribers      int
}

// Counters 返回当前计数快照（无锁，relaxed 读取）。
func (d *StreamDispatcher) Counters() DispatchCounters {
	d.mu.Lock()
	subscribers := len(d.subscribers)
	d.mu.Unlock()
	return DispatchCounters{
		KeyframeInjected: d.keyframeInjected.Load(),
		LagResyncs:       d.lagResyncs.Load(),
		QueueResyncs:     d.queueResyncs.Load(),
		ResyncSkips:      d.resyncSkips.Load(),
		GOPTruncations:   d.gopTruncations.Load(),
		Subscribers:      subscribers,
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
		clear(d.gop.frames)
		d.gop.frames = nil
		d.gop.bytes = 0
		d.gop.complete = false
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
