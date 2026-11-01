package engine

import (
	"context"
	"sync"
	"time"
)

// streamPollInterval 是订阅控制循环轮询物理流状态的周期。
const streamPollInterval = 250 * time.Millisecond

// queueCore 是数据包订阅与 AI 结果订阅共享的「队列 + 唤醒 + 终态」内核。
//
// 内核只负责三件容易写错的事：加锁入队、非阻塞唤醒、幂等终态；
// 准入策略（超限失败 vs 丢最旧）由调用方在 enqueue 时选择，
// 见 pushBounded / pushDropOldest。
//
// ⚠️ Next 只支持单个消费者：notify 容量为 1，多余的并发读者会一直睡到订阅终止。
// 需要扇出请在业务层做（一个读者 → 多个下游）。
type queueCore[T any] struct {
	mu       sync.Mutex
	queue    []T
	buffered int
	cost     func(T) int // 单元素开销，用于字节预算统计；nil 视为 0
	notify   chan struct{}
	done     chan struct{}
	closed   bool
	err      error
}

func newQueueCore[T any](cost func(T) int) *queueCore[T] {
	if cost == nil {
		cost = zeroCost[T]
	}
	return &queueCore[T]{
		cost:   cost,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// zeroCost 是未指定 cost 时的默认算法：所有元素都视为零开销。
func zeroCost[T any](T) int {
	return 0
}

// queueBudget 是一次入队的双重上限：元素个数与字节总量。
//
// 字节记账只走 queueCore.cost 这一个来源 —— 入队时由内核按 cost(item) 累计，
// 出队时同样按 cost(item) 扣减，任何调用方自报的字节数都不会参与记账。
type queueBudget struct {
	maxItems int
	maxBytes int
}

// zeroOf 返回 T 的零值，用于错误返回路径。
func zeroOf[T any]() T {
	var zero T
	return zero
}

// pushBounded 在双预算内入队；元素数或字节数超限时以 overflow 终止队列。
//
// 返回 overflow 时表示「本次未入队且订阅已终止」，调用方无需再唤醒读者。
// 队列已终止时静默丢弃并返回 nil —— 晚到的回调不该再制造错误。
func (q *queueCore[T]) pushBounded(item T, budget queueBudget, overflow error) error {
	cost := q.cost(item)

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return nil
	}
	if len(q.queue) >= budget.maxItems || q.buffered+cost > budget.maxBytes {
		q.closeErrLocked(overflow)
		q.mu.Unlock()
		q.signal()
		return overflow
	}
	q.queue = append(q.queue, item)
	q.buffered += cost
	q.mu.Unlock()
	q.signal()
	return nil
}

// pushDropOldest 在达到容量上限时丢弃最旧元素（Drop Oldest），返回本次丢弃数。
func (q *queueCore[T]) pushDropOldest(item T, capacity int) int {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return 0
	}
	dropped := 0
	if len(q.queue) >= capacity {
		q.buffered -= q.cost(q.queue[0])
		q.queue[0] = zeroOf[T]()
		q.queue = q.queue[1:]
		dropped = 1
	}
	q.queue = append(q.queue, item)
	q.buffered += q.cost(item)
	q.mu.Unlock()
	q.signal()
	return dropped
}

// next 阻塞等待下一个元素。队列仍有积压时先交付积压，再返回终态错误。
func (q *queueCore[T]) next(ctx context.Context) (T, error) {
	for {
		q.mu.Lock()
		if len(q.queue) > 0 {
			item := q.queue[0]
			q.queue[0] = zeroOf[T]()
			q.queue = q.queue[1:]
			q.buffered -= q.cost(item)
			q.mu.Unlock()
			return item, nil
		}
		if q.closed {
			err := q.err
			q.mu.Unlock()
			if err == nil {
				err = ErrClosed
			}
			return zeroOf[T](), err
		}
		q.mu.Unlock()

		select {
		case <-q.notify:
		case <-ctx.Done():
			return zeroOf[T](), ctx.Err()
		case <-q.done:
		}
	}
}

// closeWithErr 以终态关闭队列，并唤醒所有等待方。重复调用只有第一次生效。
func (q *queueCore[T]) closeWithErr(err error) bool {
	q.mu.Lock()
	changed := q.closeErrLocked(err)
	q.mu.Unlock()
	if changed {
		q.signal()
	}
	return changed
}

// closeErrLocked 要求调用方已持有 mu。
//
// 终态错误一经写入就不再被 nil 覆盖：控制循环先记录 ctx 取消原因，
// 随后 onStop() 触发的 Close 不得把它擦成「正常关闭」。
func (q *queueCore[T]) closeErrLocked(err error) bool {
	if q.closed {
		return false
	}
	if err != nil || q.err == nil {
		q.err = err
	}
	q.closed = true
	close(q.done)
	return true
}

// setErrIfOpen 仅在队列仍开启且尚无终态错误时记录错误，不改变开启状态。
func (q *queueCore[T]) setErrIfOpen(err error) {
	q.mu.Lock()
	if !q.closed && q.err == nil {
		q.err = err
	}
	q.mu.Unlock()
}

// isClosed 报告队列是否已终止，用于入队前的快速丢弃（避免无谓的深拷贝）。
func (q *queueCore[T]) isClosed() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.closed
}

// dropQueued 清空积压：订阅关闭后不应再交付陈旧数据。
func (q *queueCore[T]) dropQueued() {
	q.mu.Lock()
	q.queue = nil
	q.buffered = 0
	q.mu.Unlock()
}

// signal 做非阻塞唤醒：重复信号被合并，因此读者醒来后必须重新检查队列。
func (q *queueCore[T]) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

// Done 返回队列终止的只读通道。
func (q *queueCore[T]) Done() <-chan struct{} {
	return q.done
}

// Err 返回队列的终态错误（尚未终止或正常关闭时为 nil）。
func (q *queueCore[T]) Err() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.err
}

// watch 轮询物理流状态，直到 ctx 结束、队列终止或流进入失败态。
// onStop 由订阅方提供 —— 两条订阅通道的注销路径不同。
func (q *queueCore[T]) watch(ctx context.Context, stream *Stream, onStop func()) {
	ticker := time.NewTicker(streamPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.setErrIfOpen(ctx.Err())
			onStop()
			return
		case <-q.done:
			onStop()
			return
		case <-ticker.C:
			if stream == nil {
				continue
			}
			status, err := stream.Status(ctx)
			if err != nil {
				q.closeWithErr(err)
				onStop()
				return
			}
			if status.State == StreamFailed {
				q.closeWithErr(streamFailure(status))
				onStop()
				return
			}
		}
	}
}

// streamFailure 把失败态的流状态收敛为一个错误值。
func streamFailure(status StreamStatus) error {
	if status.Error != nil {
		return status.Error
	}
	return ErrIO
}
