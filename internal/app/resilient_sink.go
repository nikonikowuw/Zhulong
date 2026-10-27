package app

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap/zapcore"
)

// A file sink is the one logging destination that can stall the whole host.
// zap writes synchronously from whichever goroutine logged, and boundedFileSink
// holds its mutex across write(2). On an edge device with a worn SD card or a
// wedged USB bridge that turns one blocked syscall into a host that stops
// serving HTTP while still looking alive — the failure mode this file exists to
// remove.
//
// resilientSink puts a bounded, non-blocking queue in front of the file and owns
// every failure mode:
//
//   - Queue full, or admission contended: drop and count, never wait. Logging is
//     best-effort, and a host that records video must not stop to write a line.
//   - Prepare failed (read-only rootfs, unmounted volume, no space): the host
//     keeps serving on stderr and reports a degraded logger instead of refusing
//     to boot. The device's job is recording and inference, not logging.
//   - Sticky write failure: circuit break to stderr, keep accepting records so
//     error paths still produce evidence, and report the cause through Health.
//
// Transient refusals (budget reservations for compression) are counted and
// dropped without breaking the circuit: boundedFileSink reports those through
// Write but not through Health, which is exactly the distinction used here.
const (
	// logQueueDepth bounds queued records, not bytes. One encoded record is
	// already bounded by the sanitizing core's per-field 2048-byte cap, so depth
	// alone gives a worst case in the low megabytes.
	logQueueDepth = 1024
	// logDrainTimeout bounds shutdown and sync. A dead disk must not stop the
	// host from exiting; unflushed records are reported instead of retried.
	logDrainTimeout = 5 * time.Second
)

var errLogSinkNotPrepared = errors.New("file log sink has not been prepared by the application lifecycle")

var errLogSinkClosed = errors.New("file log sink is closed")

type logQueueEntry struct {
	data []byte
	// ack is non-nil only for sync barriers. Barriers travel through the same
	// queue as records so ordering is enforced by the single worker rather than
	// by a second coordination mechanism.
	ack chan struct{}
}

type resilientSink struct {
	primary  loggerOutput
	fallback loggerOutput
	queue    chan logQueueEntry
	pool     sync.Pool

	stop chan struct{}
	done chan struct{}

	// mu guards degraded, which is a sticky explanation rather than a counter.
	mu       sync.Mutex
	degraded error

	prepared atomic.Bool
	started  atomic.Bool
	closed   atomic.Bool
	// fallbackMode routes every write to stderr: set when Prepare failed or the
	// file sink hit a persistent failure.
	fallbackMode atomic.Bool
	dropped      atomic.Uint64
}

// newResilientSink wraps a file destination. Prepare, not construction, starts
// the worker: Fx providers must stay free of lifecycle side effects.
func newResilientSink(primary loggerOutput, fallback loggerOutput) *resilientSink {
	if fallback == nil {
		fallback = &streamLogSink{writer: zapcore.Lock(os.Stderr)}
	}
	return &resilientSink{
		primary:  primary,
		fallback: fallback,
		queue:    make(chan logQueueEntry, logQueueDepth),
		pool:     sync.Pool{New: func() any { return make([]byte, 0, 256) }},
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Prepare both opens the file and starts the single writer goroutine, so a
// prepared sink is ready to accept records immediately.
//
// A destination failure is deliberately not returned: it demotes the logger to
// stderr instead of aborting the boot. Configuration and policy errors still fail
// earlier, in buildLogger or openLogSink, and those do abort.
func (s *resilientSink) Prepare() error {
	if s.prepared.Swap(true) {
		return nil
	}
	if err := s.primary.Prepare(); err != nil {
		s.enterFallback(err)
		return nil
	}
	s.started.Store(true)
	go s.run()
	return nil
}

func (s *resilientSink) Write(data []byte) (int, error) {
	switch {
	case s.closed.Load():
		return 0, errLogSinkClosed
	case !s.prepared.Load():
		return 0, errLogSinkNotPrepared
	case s.fallbackMode.Load():
		return s.fallback.Write(data)
	}

	entry := logQueueEntry{data: s.buffer(len(data))}
	copy(entry.data, data)
	select {
	case s.queue <- entry:
		return len(data), nil
	default:
		// Report success to zap on purpose: a dropped record must not turn into a
		// second synchronous write through ErrorOutput on the same hot path.
		s.dropped.Add(1)
		s.putBuffer(entry.data)
		return len(data), nil
	}
}

// Sync flushes everything queued before touching the file. Without the barrier
// a shutdown would fsync an empty buffer and silently lose the tail of the log.
func (s *resilientSink) Sync() error {
	if !s.started.Load() {
		if s.fallbackMode.Load() {
			return s.fallback.Sync()
		}
		return s.primary.Sync()
	}
	if s.fallbackMode.Load() {
		return s.fallback.Sync()
	}

	ack := make(chan struct{})
	select {
	case s.queue <- logQueueEntry{ack: ack}:
	case <-time.After(logDrainTimeout):
		return fmt.Errorf("log sink sync timed out after %s: queue is full", logDrainTimeout)
	}
	select {
	case <-ack:
		return s.primary.Sync()
	case <-time.After(logDrainTimeout):
		return fmt.Errorf("log sink sync timed out after %s: writer is blocked", logDrainTimeout)
	}
}

// Health reports the sticky degradation cause and the number of dropped records.
//
// It never consults the primary sink: that would take a mutex the writer may be
// holding across a blocked write(2), which would hang the /health handler — the
// exact failure this wrapper exists to prevent. The primary's own counters are
// joined in by Close, after the worker has stopped.
func (s *resilientSink) Health() (error, uint64) {
	s.mu.Lock()
	degraded := s.degraded
	s.mu.Unlock()
	return degraded, s.dropped.Load()
}

// Close drains and closes the file. If the writer cannot finish within
// logDrainTimeout it is left alone on purpose: the stuck goroutine still holds
// the file's mutex, so calling Close would block shutdown on the same syscall.
func (s *resilientSink) Close() error {
	if s.closed.Swap(true) {
		return nil
	}
	if !s.started.Load() {
		return s.primary.Close()
	}
	close(s.stop)
	select {
	case <-s.done:
		return s.primary.Close()
	case <-time.After(logDrainTimeout):
		pending := len(s.queue)
		s.dropped.Add(uint64(pending))
		return fmt.Errorf("log sink did not drain within %s; %d records still queued", logDrainTimeout, pending)
	}
}

func (s *resilientSink) run() {
	defer close(s.done)
	for {
		select {
		case entry := <-s.queue:
			s.deliver(entry)
		case <-s.stop:
			// Drain what is already queued, then stop. New admissions after Close
			// are refused by closed, so this loop terminates.
			for {
				select {
				case entry := <-s.queue:
					s.deliver(entry)
				default:
					return
				}
			}
		}
	}
}

func (s *resilientSink) deliver(entry logQueueEntry) {
	if len(entry.data) > 0 {
		if s.fallbackMode.Load() {
			_, _ = s.fallback.Write(entry.data)
		} else if _, err := s.primary.Write(entry.data); err != nil {
			s.dropped.Add(1)
			// Only a sticky failure belongs in Health; a transient budget refusal
			// (compression reservation) must not demote the file destination.
			if healthErr, _ := s.primary.Health(); healthErr != nil {
				s.enterFallback(healthErr)
				_, _ = s.fallback.Write(entry.data)
			}
		}
		s.putBuffer(entry.data)
	}
	if entry.ack != nil {
		close(entry.ack)
	}
}

func (s *resilientSink) enterFallback(cause error) {
	if s.fallbackMode.Swap(true) {
		return
	}
	s.mu.Lock()
	s.degraded = errors.Join(s.degraded, cause)
	s.mu.Unlock()
	// Independent diagnostic channel: the encoded sink may be the thing that is
	// broken, so this cannot go back through the logger core.
	_, _ = fmt.Fprintf(os.Stderr, "zhulong: file log sink unavailable; continuing on stderr: %v\n", cause)
}

func (s *resilientSink) buffer(size int) []byte {
	if pooled, ok := s.pool.Get().([]byte); ok && cap(pooled) >= size {
		return pooled[:size]
	}
	return make([]byte, size)
}

func (s *resilientSink) putBuffer(buffer []byte) {
	// Records are re-encoded from scratch by zap, so retaining the bytes is only
	// ever a capacity donation. Cap the pool entry to keep one oversized record
	// from pinning megabytes for the process lifetime.
	if cap(buffer) > 0 && cap(buffer) <= logQueueDepth*64 {
		s.pool.Put(buffer[:0])
	}
}
