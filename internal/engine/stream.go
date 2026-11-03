package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// Stream 封装单个消费者对物理 RTSP 流的持有引用。
type Stream struct {
	engine     *Engine
	id         C.Zhulong_stream_id
	consumerID uint64
	kind       ConsumerKind
	url        string
	generation uint64

	mu          sync.Mutex
	closed      bool
	activeSub   *Subscription
	activeAISub *AIResultSubscription
	closeOnce   sync.Once
}

// streamLease 是一次跨 CGO 调用前的流标识快照。
type streamLease struct {
	generation uint64
	id         C.Zhulong_stream_id
}

// lease 校验 ctx 与关闭态，并在同一把锁内取出不可变的流标识。
func (s *Stream) lease(ctx context.Context) (streamLease, error) {
	if err := ctx.Err(); err != nil {
		return streamLease{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return streamLease{}, ErrClosed
	}
	return streamLease{generation: s.generation, id: s.id}, nil
}

// ID 返回引擎为该物理流分配的全局唯一编号。
//
// 宿主下发给算法包的控制报文按流寻址（如 MSG_TASK_CONFIG 指定某一路流的任务配置），
// 因此这里必须是引擎的权威编号，而不是从 broker socket 名反解的字符串——
// 后者会让「socket 命名规则」变成第二个事实来源。
func (s *Stream) ID(ctx context.Context) (uint64, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return 0, err
	}
	return uint64(ref.id), nil
}

// BrokerPath 查询当前物理流关联的 FrameBroker Unix Domain Socket 路径。
// 必须在 ConsumerAI 类型流上调用（且当前流仍有活跃 AI 消费者）。
func (s *Stream) BrokerPath(ctx context.Context) (string, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return "", err
	}
	return s.engine.getBrokerPath(ref.generation, ref.id)
}

// BrokerClients 查询该流 FrameBroker 上各算法 Runner 客户端的帧投递统计。
//
// 返回的 total 是当前客户端**总数**；当 total > len(clients) 时结果已被截断
// （每个物理流同时订阅的算法包量以个位数计，截断意味着出现了异常配置）。
func (s *Stream) BrokerClients(ctx context.Context) (clients []BrokerClientStats, total uint32, err error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return nil, 0, err
	}
	return s.engine.getBrokerClients(ref.generation, ref.id)
}

// SetPublishRate 动态设置帧发布最大采样帧率（由关联任务的 max(sample_fps) 驱动，0 表示不节流）。
func (s *Stream) SetPublishRate(ctx context.Context, fps uint32) error {
	ref, err := s.lease(ctx)
	if err != nil {
		return err
	}
	return s.engine.setPublishRate(ref.generation, ref.id, fps)
}

// Status 查询物理流当前运行状态与底层错误码。
func (s *Stream) Status(ctx context.Context) (StreamStatus, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return StreamStatus{}, err
	}
	return s.engine.getStreamStatus(ref.generation, ref.id)
}

// Subscribe 开启视频压缩包的派发订阅（仅 Preview 类型的消费者允许调用）。
func (s *Stream) Subscribe(ctx context.Context, options SubscriptionOptions) (*Subscription, error) {
	if s.kind != ConsumerPreview {
		return nil, fmt.Errorf("%w: only preview consumers can subscribe to packets", ErrInvalidArgument)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limits, err := subscriptionLimits{
		maxPackets:       options.MaxPackets,
		maxPacketBytes:   options.MaxPacketBytes,
		maxBufferedBytes: options.MaxBufferedBytes,
	}.validate()
	if err != nil {
		return nil, err
	}

	// 零值保持 ABI v4 语义（仅视频）；音频订阅必须自己带上 TrackMaskAudio。
	mask := options.TrackMask
	if mask == 0 {
		mask = TrackMaskVideo
	}
	if mask.Has(TrackMaskAudio) && mask.Has(TrackMaskVideo) {
		// 两条轨的生命周期、队列预算与消费节奏都不同，混订等于把「音频挤掉视频」
		// 这个必须排除的故障模式重新引回来。需要两条就开两条订阅。
		return nil, fmt.Errorf("%w: subscribe one track per subscription", ErrInvalidArgument)
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.activeSub != nil {
		s.mu.Unlock()
		return nil, ErrDuplicate
	}

	ctrlCtx, cancelCtrl := context.WithCancel(ctx)
	sub := &Subscription{
		stream:     s,
		generation: s.generation,
		limits:     limits,
		// mask 必须在注册前就位：原生订阅一旦生效，回调可能在任何时刻开始读它。
		mask:          mask,
		core:          newQueueCore[Packet](func(pkt Packet) int { return len(pkt.Data) }),
		cancelControl: cancelCtrl,
	}

	// 此处持有 s.mu 做一次 CGO 调用，与 Engine 的「mu 下不做 CGO」不矛盾：
	// 原生 subscribe 只是注册回调表项，不阻塞拉流；而「closed 判定 + 单订阅不变量 +
	// 句柄发布」必须原子，拆锁反而要引入回滚分支。
	subID, token, err := s.engine.subscribe(s.generation, s.id, s.consumerID, mask, sub)
	if err != nil {
		cancelCtrl()
		s.mu.Unlock()
		return nil, err
	}

	sub.id = subID
	sub.token.value = token
	s.activeSub = sub
	s.mu.Unlock()

	go sub.runControlLoop(ctrlCtx)

	return sub, nil
}

// AudioInfo 查询该路物理流的音频元数据。
//
// 无音频的相机同样返回成功（HasAudio=false），只有流本身不存在/未就绪才是错误：
// 「这路没有声音」是正常状态，不该让调用方去区分「查询失败」和「确实没有」。
func (s *Stream) AudioInfo(ctx context.Context) (AudioInfo, error) {
	ref, err := s.lease(ctx)
	if err != nil {
		return AudioInfo{}, err
	}
	return s.engine.getAudioInfo(ref.generation, uint64(ref.id))
}

// Close 释放消费者对底层物理流的引用。若存在活跃订阅，会先自动安全退订。
//
// 退订错误不会被吞掉：它们与 release 错误一起用 errors.Join 返回，由调用方决定是否上报。
// 停机排空路径（Engine.stopLocked）显式忽略返回值 —— 此时流的终态已由各自的订阅通道保证可见。
func (s *Stream) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		sub := s.activeSub
		aiSub := s.activeAISub
		s.activeSub = nil
		s.activeAISub = nil
		s.mu.Unlock()

		var unsubscribeErrs []error
		if sub != nil {
			if err := sub.Close(); err != nil {
				unsubscribeErrs = append(unsubscribeErrs, err)
			}
		}
		if aiSub != nil {
			if err := aiSub.Close(); err != nil {
				unsubscribeErrs = append(unsubscribeErrs, err)
			}
		}

		releaseErr := s.engine.releaseStream(s.generation, s.id, s.consumerID)
		closeErr = errors.Join(append(unsubscribeErrs, releaseErr)...)
		s.engine.onStreamClosed(s)
	})
	return closeErr
}

func (s *Stream) onSubClosed(sub *Subscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeSub == sub {
		s.activeSub = nil
	}
}

func (s *Stream) onAISubClosed(sub *AIResultSubscription) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.activeAISub == sub {
		s.activeAISub = nil
	}
}

// SubscribeAIResults 订阅当前物理流关联的 AI 推理结果事件流。
func (s *Stream) SubscribeAIResults(ctx context.Context) (*AIResultSubscription, error) {
	// 与 Subscribe 对齐：先判 ctx 再上锁，避免已取消的 ctx 仍然走完注册流程。
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.activeAISub != nil {
		s.mu.Unlock()
		return nil, ErrDuplicate
	}

	streamID := s.id
	gen := s.generation
	eng := s.engine

	ctrlCtx, cancelCtrl := context.WithCancel(ctx)
	sub := newAIResultSubscription(s, streamID, gen, defaultAIResultCapacity)
	sub.cancelControl = cancelCtrl

	subID, token, err := eng.subscribeAIResults(gen, streamID, sub)
	if err != nil {
		cancelCtrl()
		s.mu.Unlock()
		return nil, err
	}

	sub.init(subID, token)
	s.activeAISub = sub
	s.mu.Unlock()

	go sub.runControlLoop(ctrlCtx)

	return sub, nil
}
