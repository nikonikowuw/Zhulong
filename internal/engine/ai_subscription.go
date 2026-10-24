package engine

/*
#cgo CFLAGS: -I${SRCDIR}/../../native/include
#include <stdint.h>
#include <Zhulong/engine.h>
*/
import "C"

import (
	"context"
	"sync"
	"sync/atomic"
)

// AIResultSubscription 封装对物理流 AI 推理结果事件流的订阅。
// 内部使用有界缓冲队列并采用丢最旧（Drop Oldest）背压策略，绝不阻塞 Native 推理线程。
type AIResultSubscription struct {
	stream     *Stream
	streamID   C.Zhulong_stream_id
	id         C.Zhulong_subscription_id
	token      cgoToken
	generation uint64

	capacity int

	core          *queueCore[[]byte]
	droppedCount  atomic.Uint64
	closeOnce     sync.Once
	cancelControl context.CancelFunc
}

// defaultAIResultCapacity 是 AI 结果队列的默认深度（丢最旧策略，不宜过深）。
const defaultAIResultCapacity = 32

func newAIResultSubscription(s *Stream, streamID C.Zhulong_stream_id, gen uint64, capacity int) *AIResultSubscription {
	if capacity <= 0 {
		capacity = defaultAIResultCapacity
	}
	return &AIResultSubscription{
		stream:     s,
		streamID:   streamID,
		generation: gen,
		capacity:   capacity,
		core:       newQueueCore[[]byte](func(payload []byte) int { return len(payload) }),
	}
}

// NewAIResultSubscriptionForTest 创建用于测试的隔离 AIResultSubscription 及模拟推流函数。
//
// ⚠️ 仅供测试：生产路径必须通过 Stream.SubscribeAIResults 取得实例，
// 否则拿不到 generation / streamID，也就无法安全注销。
func NewAIResultSubscriptionForTest(capacity int) (*AIResultSubscription, func(data []byte)) {
	sub := newAIResultSubscription(nil, 1, 1, capacity)
	return sub, func(data []byte) { sub.onResult(1, data) }
}

func (s *AIResultSubscription) init(id C.Zhulong_subscription_id, token uintptr) {
	s.id = id
	s.token.value = token
}

// onResult 由 CGO 回调在 Native 工作线程同步调用。
func (s *AIResultSubscription) onResult(streamID uint64, data []byte) {
	if len(data) == 0 {
		return
	}
	if dropped := s.core.pushDropOldest(data, s.capacity); dropped > 0 {
		s.droppedCount.Add(uint64(dropped))
	}
}

// Next 阻塞等待获取下一个 AI 推理结果 JSON 原始载荷，支持 Context 取消。
func (s *AIResultSubscription) Next(ctx context.Context) ([]byte, error) {
	return s.core.next(ctx)
}

// DroppedCount 返回因慢消费而被丢弃的历史结果数量。
func (s *AIResultSubscription) DroppedCount() uint64 {
	return s.droppedCount.Load()
}

// Done 返回订阅关闭的只读通道。
func (s *AIResultSubscription) Done() <-chan struct{} {
	return s.core.Done()
}

// Err 返回订阅异常中止时的内部错误。
func (s *AIResultSubscription) Err() error {
	return s.core.Err()
}

// reportCallbackPanic 把回调内的 panic 转成订阅终态，避免它变成「静默丢结果」。
func (s *AIResultSubscription) reportCallbackPanic(recovered any) {
	if recovered == nil {
		return
	}
	s.core.closeWithErr(nativeCallbackPanic("ai_result_subscription", recovered))
}

// Close 幂等注销该 AI 结果订阅并排空回收 cgo.Handle。
func (s *AIResultSubscription) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		if s.cancelControl != nil {
			s.cancelControl()
		}

		s.core.closeWithErr(nil)
		s.core.dropQueued()

		if s.stream != nil && s.stream.engine != nil && s.id > 0 {
			closeErr = s.stream.engine.unsubscribeWith(
				s.generation,
				"unsubscribe_ai_results",
				func(handle C.Zhulong_engine_h) C.Zhulong_status_t {
					return C.Zhulong_stream_unsubscribe_ai_results(handle, s.streamID, s.id)
				},
				s.token.release,
			)
		} else {
			s.token.release()
		}

		if s.stream != nil {
			s.stream.onAISubClosed(s)
		}
	})
	return closeErr
}

// runControlLoop 监控订阅生命周期与底层物理流状态。
func (s *AIResultSubscription) runControlLoop(ctx context.Context) {
	s.core.watch(ctx, s.stream, func() {
		// 同 Subscription：退出路径无处上报注销错误，终态由 core 保证可见。
		_ = s.Close()
	})
}
