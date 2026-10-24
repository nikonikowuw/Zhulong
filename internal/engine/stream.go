package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
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

// BrokerPath 查询当前物理流关联的 FrameBroker Unix Domain Socket 路径。
// 必须在 ConsumerAI 类型流上调用（且当前流仍有活跃 AI 消费者）。
func (s *Stream) BrokerPath(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return "", ErrClosed
	}
	s.mu.Unlock()

	return s.engine.getBrokerPath(s.generation, s.id)
}

// SetPublishRate 动态设置帧发布最大采样帧率（由关联任务的 max(sample_fps) 驱动，0 表示不节流）。
func (s *Stream) SetPublishRate(ctx context.Context, fps uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrClosed
	}
	s.mu.Unlock()

	return s.engine.setPublishRate(s.generation, s.id, fps)
}

// Status 查询物理流当前运行状态与底层错误码。
func (s *Stream) Status(ctx context.Context) (StreamStatus, error) {
	if err := ctx.Err(); err != nil {
		return StreamStatus{}, err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return StreamStatus{}, ErrClosed
	}
	s.mu.Unlock()

	return s.engine.getStreamStatus(s.generation, s.id)
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
		stream:        s,
		generation:    s.generation,
		limits:        limits,
		core:          newQueueCore[Packet](func(pkt Packet) int { return len(pkt.Data) }),
		cancelControl: cancelCtrl,
	}

	subID, token, err := s.engine.subscribe(s.generation, s.id, s.consumerID, sub)
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

// Close 释放消费者对底层物理流的引用。若存在活跃订阅，会先自动安全退订。
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

		if sub != nil {
			_ = sub.Close()
		}
		if aiSub != nil {
			_ = aiSub.Close()
		}

		closeErr = s.engine.releaseStream(s.generation, s.id, s.consumerID)
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
