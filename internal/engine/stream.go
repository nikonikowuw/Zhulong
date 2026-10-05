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

	mu        sync.Mutex
	closed    bool
	activeSub *Subscription
	closeOnce sync.Once
}

// Status 查询物理流当前运行状态与底层错误码。
func (s *Stream) Status(ctx context.Context) (StreamStatus, error) {
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

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if s.activeSub != nil {
		s.mu.Unlock()
		return nil, ErrDuplicate
	}

	// 校验 SubscriptionOptions
	maxPackets := options.MaxPackets
	if maxPackets <= 0 {
		maxPackets = DefaultMaxPackets
	} else if maxPackets > HardLimitMaxPackets {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: MaxPackets exceeds %d", ErrInvalidArgument, HardLimitMaxPackets)
	}

	maxPacketBytes := options.MaxPacketBytes
	if maxPacketBytes <= 0 {
		maxPacketBytes = DefaultMaxPacketBytes
	} else if maxPacketBytes > HardLimitMaxPacketBytes {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: MaxPacketBytes exceeds %d", ErrInvalidArgument, HardLimitMaxPacketBytes)
	}

	maxBufferedBytes := options.MaxBufferedBytes
	if maxBufferedBytes <= 0 {
		maxBufferedBytes = DefaultMaxBufferedBytes
	} else if maxBufferedBytes > HardLimitMaxBufferedBytes {
		s.mu.Unlock()
		return nil, fmt.Errorf("%w: MaxBufferedBytes exceeds %d", ErrInvalidArgument, HardLimitMaxBufferedBytes)
	}

	ctrlCtx, cancelCtrl := context.WithCancel(ctx)

	sub := &Subscription{
		stream:           s,
		generation:       s.generation,
		maxPackets:       maxPackets,
		maxPacketBytes:   maxPacketBytes,
		maxBufferedBytes: maxBufferedBytes,
		notify:           make(chan struct{}, 1),
		done:             make(chan struct{}),
		cancelControl:    cancelCtrl,
	}

	subID, token, err := s.engine.subscribe(s.generation, s.id, s.consumerID, sub)
	if err != nil {
		cancelCtrl()
		s.mu.Unlock()
		return nil, err
	}

	sub.id = subID
	sub.token = token
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
		s.mu.Unlock()

		if sub != nil {
			_ = sub.Close()
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
