// Package nativelog provides a bounded admission queue for preformatted native logs.
package nativelog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

var (
	ErrInvalidCapacity = errors.New("native log queue capacity must be positive")
	ErrAlreadyStarted  = errors.New("native log queue already started")
	ErrStopped         = errors.New("native log queue is stopped")
	ErrNotStarted      = errors.New("native log queue has not been started")
)

// Level is the native log severity. Its values are independent from zap's enums.
type Level uint8

const (
	Debug Level = iota
	Info
	Warn
	Error
)

// Record contains Go-owned strings copied from native buffers before admission.
type Record struct {
	Level    Level
	StreamID uint64
	Module   string
	Message  string
	SeenAt   time.Time
}

type Consumer func(Record) error

// Queue admits records without waiting on capacity or admission-lock contention.
type Queue struct {
	records chan Record
	consume Consumer
	logger  *zap.Logger

	admission sync.Mutex
	started   bool
	stopped   bool
	stopCh    chan struct{}
	abortCh   chan struct{}
	doneCh    chan struct{}
	abortOnce sync.Once

	pending      atomic.Int64
	dropped      atomic.Uint64
	sinkFailures atomic.Uint64
	lastErrMu    sync.Mutex
	lastErr      error
	reportedDrop uint64
}

// NewQueue constructs a queue whose consumer emits structured records through logger.
func NewQueue(capacity int, logger *zap.Logger) (*Queue, error) {
	if capacity <= 0 {
		return nil, ErrInvalidCapacity
	}
	if logger == nil {
		return nil, errors.New("native log queue requires an explicit logger")
	}
	nativeLogger := logger.Named("native.log")
	q := newQueue(capacity, func(record Record) error {
		fields := []zap.Field{
			zap.String("module", record.Module),
			zap.Time("seenAt", record.SeenAt),
			zap.String("message", record.Message),
		}
		if record.StreamID != 0 {
			fields = append(fields, zap.Uint64("streamId", record.StreamID))
		}
		switch record.Level {
		case Debug:
			nativeLogger.Debug("native event", fields...)
		case Info:
			nativeLogger.Info("native event", fields...)
		case Warn:
			nativeLogger.Warn("native event", fields...)
		case Error:
			nativeLogger.Error("native event", fields...)
		default:
			return fmt.Errorf("invalid native log level %d", record.Level)
		}
		return nil
	})
	q.logger = nativeLogger
	return q, nil
}

func newQueue(capacity int, consume Consumer) *Queue {
	return &Queue{
		records: make(chan Record, capacity),
		consume: consume,
		stopCh:  make(chan struct{}),
		abortCh: make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start starts the single consumer goroutine.
func (q *Queue) Start() error {
	q.admission.Lock()
	if q.stopped {
		q.admission.Unlock()
		return ErrStopped
	}
	if q.started {
		q.admission.Unlock()
		return ErrAlreadyStarted
	}
	q.started = true
	q.admission.Unlock()
	go q.run()
	return nil
}

// TryEnqueue makes one bounded, nonblocking admission attempt.
func (q *Queue) TryEnqueue(record Record) bool {
	if !record.valid() {
		q.RecordDrop()
		return false
	}
	if !q.admission.TryLock() {
		q.RecordDrop()
		return false
	}
	defer q.admission.Unlock()
	if q.stopped {
		q.RecordDrop()
		return false
	}
	q.pending.Add(1)
	select {
	case q.records <- record:
		return true
	default:
		q.pending.Add(-1)
		q.RecordDrop()
		return false
	}
}

// RecordDrop accounts for native-side invalid input rejected before queue admission.
func (q *Queue) RecordDrop() { q.dropped.Add(1) }

// Dropped returns the total number of invalid, contended, full, or post-stop drops.
func (q *Queue) Dropped() uint64 { return q.dropped.Load() }

// Pending returns queued and currently-consumed records not yet drained.
func (q *Queue) Pending() int { return int(q.pending.Load()) }

// SinkFailures reports consumer errors since construction.
func (q *Queue) SinkFailures() uint64 { return q.sinkFailures.Load() }

// LastSinkError returns the latest consumer error, if any.
func (q *Queue) LastSinkError() error {
	q.lastErrMu.Lock()
	defer q.lastErrMu.Unlock()
	return q.lastErr
}

// Stop prevents new admissions, drains within ctx, and returns the number of
// records still queued or in-flight when the context expires. It never closes
// the producer channel, so concurrent TryEnqueue calls cannot panic.
func (q *Queue) Stop(ctx context.Context) (int, error) {
	if ctx == nil {
		return q.Pending(), errors.New("native log queue Stop requires a context")
	}
	q.admission.Lock()
	started := q.started
	if !q.stopped {
		q.stopped = true
		close(q.stopCh)
	}
	q.admission.Unlock()
	if !started {
		return q.Pending(), ErrNotStarted
	}
	select {
	case <-q.doneCh:
		return q.Pending(), nil
	case <-ctx.Done():
		select {
		case <-q.doneCh:
			return q.Pending(), nil
		default:
		}
		q.abortOnce.Do(func() { close(q.abortCh) })
		pending := q.Pending()
		return pending, fmt.Errorf("stop native log queue with %d pending records: %w", pending, ctx.Err())
	}
}

func (q *Queue) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer close(q.doneCh)
	for {
		select {
		case <-q.abortCh:
			q.reportDrops()
			return
		default:
		}
		select {
		case <-q.abortCh:
			q.reportDrops()
			return
		case <-q.stopCh:
			q.drain()
			q.reportDrops()
			return
		case record := <-q.records:
			q.deliver(record)
		case <-ticker.C:
			q.reportDrops()
		}
	}
}

func (q *Queue) drain() {
	for {
		select {
		case <-q.abortCh:
			return
		default:
		}
		select {
		case <-q.abortCh:
			return
		case record := <-q.records:
			q.deliver(record)
		default:
			return
		}
	}
}

func (q *Queue) deliver(record Record) {
	if err := q.consume(record); err != nil {
		q.sinkFailures.Add(1)
		q.lastErrMu.Lock()
		q.lastErr = err
		q.lastErrMu.Unlock()
		if q.logger != nil {
			q.logger.Error("native log consumer failed", zap.Error(err))
		}
	}
	q.pending.Add(-1)
}

func (q *Queue) reportDrops() {
	if q.logger == nil {
		return
	}
	if dropped := q.Dropped(); dropped > q.reportedDrop {
		q.logger.Warn("native log records dropped", zap.Uint64("dropped_total", dropped))
		q.reportedDrop = dropped
	}
}

func (r Record) valid() bool {
	return r.Level <= Error && r.Module != "" && !r.SeenAt.IsZero()
}
