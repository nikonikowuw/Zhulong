package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"runtime/cgo"
	"sync"
	"sync/atomic"
)

// AIResultSubscription 封装对物理流 AI 推理结果事件流的订阅。
// 内部使用有界缓冲队列并采用丢最旧（Drop Oldest）背压策略，绝不阻塞 Native 推理线程。
type AIResultSubscription struct {
	stream     *Stream
	streamID   C.Zhulong_stream_id
	id         C.Zhulong_subscription_id
	token      uintptr
	generation uint64

	capacity int

	mu           sync.Mutex
	queue        [][]byte
	droppedCount atomic.Uint64
	closed       bool
	err          error

	notify        chan struct{}
	done          chan struct{}
	closeOnce     sync.Once
	tokenDeleted  atomic.Bool
	cancelControl context.CancelFunc
}

func newAIResultSubscription(s *Stream, streamID C.Zhulong_stream_id, gen uint64, capacity int) *AIResultSubscription {
	if capacity <= 0 {
		capacity = 32
	}
	return &AIResultSubscription{
		stream:     s,
		streamID:   streamID,
		generation: gen,
		capacity:   capacity,
		queue:      make([][]byte, 0, capacity),
		notify:     make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
}

func (s *AIResultSubscription) init(id C.Zhulong_subscription_id, token uintptr) {
	s.id = id
	s.token = token
}

func (s *AIResultSubscription) deleteToken() {
	if s.token == 0 {
		return
	}
	if s.tokenDeleted.CompareAndSwap(false, true) {
		h := cgo.Handle(s.token)
		h.Delete()
	}
}

// onResult 由 CGO 回调在 Native 工作线程同步调用。
func (s *AIResultSubscription) onResult(streamID uint64, data []byte) {
	if len(data) == 0 {
		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}

	// 丢最旧（Drop Oldest）背压策略
	if len(s.queue) >= s.capacity {
		s.queue[0] = nil // 协助 GC
		s.queue = s.queue[1:]
		s.droppedCount.Add(1)
	}

	s.queue = append(s.queue, data)
	s.mu.Unlock()

	select {
	case s.notify <- struct{}{}:
	default:
	}
}

// Next 阻塞等待获取下一个 AI 推理结果 JSON 原始载荷，支持 Context 取消。
func (s *AIResultSubscription) Next(ctx context.Context) ([]byte, error) {
	for {
		s.mu.Lock()
		if len(s.queue) > 0 {
			res := s.queue[0]
			s.queue[0] = nil
			s.queue = s.queue[1:]
			s.mu.Unlock()
			return res, nil
		}
		if s.closed {
			err := s.err
			s.mu.Unlock()
			if err == nil {
				return nil, ErrClosed
			}
			return nil, err
		}
		s.mu.Unlock()

		select {
		case <-s.notify:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.done:
		}
	}
}

// DroppedCount 返回因慢消费而被丢弃的历史结果数量。
func (s *AIResultSubscription) DroppedCount() uint64 {
	return s.droppedCount.Load()
}

// NewAIResultSubscriptionForTest 创建用于测试的隔离 AIResultSubscription 及模拟推流函数。
func NewAIResultSubscriptionForTest(capacity int) (*AIResultSubscription, func(data []byte)) {
	sub := newAIResultSubscription(nil, 1, 1, capacity)
	feed := func(data []byte) {
		sub.onResult(1, data)
	}
	return sub, feed
}

// Done 返回订阅关闭的只读通道。
func (s *AIResultSubscription) Done() <-chan struct{} {
	return s.done
}

// Err 返回订阅异常中止时的内部错误。
func (s *AIResultSubscription) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Close 幂等注销该 AI 结果订阅并排空回收 cgo.Handle。
func (s *AIResultSubscription) Close() error {
	var err error
	s.closeOnce.Do(func() {
		if s.cancelControl != nil {
			s.cancelControl()
		}

		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		close(s.done)

		if s.stream != nil && s.stream.engine != nil && s.id > 0 {
			err = s.stream.engine.unsubscribeAIResults(s.generation, s.streamID, s.id, s.deleteToken)
		} else {
			s.deleteToken()
		}

		if s.stream != nil {
			s.stream.onAISubClosed(s)
		}
	})
	return err
}
