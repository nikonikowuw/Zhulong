package app

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// stubLogSink records what reached a destination and can stall, refuse or fail on
// demand so the wrapper's failure modes are exercised rather than simulated.
type stubLogSink struct {
	mu sync.Mutex

	written    [][]byte
	prepareErr error
	writeErr   error
	healthErr  error
	lost       uint64
	// block, when non-nil, holds every write until it is closed. It stands in for
	// a wedged flash controller.
	block chan struct{}
	syncs int
	close int
}

func (s *stubLogSink) Prepare() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.prepareErr
}

func (s *stubLogSink) Write(data []byte) (int, error) {
	s.mu.Lock()
	block := s.block
	err := s.writeErr
	s.mu.Unlock()

	if block != nil {
		<-block
	}
	if err != nil {
		s.mu.Lock()
		s.lost++
		s.mu.Unlock()
		return 0, err
	}

	copied := append([]byte(nil), data...)
	s.mu.Lock()
	s.written = append(s.written, copied)
	s.mu.Unlock()
	return len(data), nil
}

func (s *stubLogSink) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncs++
	return nil
}

func (s *stubLogSink) Health() (error, uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healthErr, s.lost
}

func (s *stubLogSink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.close++
	return nil
}

func (s *stubLogSink) payloads() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.written))
	for _, entry := range s.written {
		out = append(out, string(entry))
	}
	return out
}

func (s *stubLogSink) closeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.close
}

// TestResilientSinkNeverBlocksOnAStalledDestination is the regression test for the
// failure this wrapper exists for: a sync write to a dead disk used to freeze every
// goroutine that logged, HTTP handlers included.
func TestResilientSinkNeverBlocksOnAStalledDestination(t *testing.T) {
	primary := &stubLogSink{block: make(chan struct{})}
	fallback := &stubLogSink{}
	sink := newResilientSink(primary, fallback)
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		for i := 0; i < logQueueDepth+128; i++ {
			if _, err := sink.Write([]byte(fmt.Sprintf("record-%d", i))); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writes blocked behind a stalled destination")
	}

	if healthErr, dropped := sink.Health(); healthErr != nil || dropped == 0 {
		t.Fatalf("health = (%v, %d), want a nil cause and dropped records", healthErr, dropped)
	}

	close(primary.block)
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestResilientSinkDegradesWhenTheDestinationCannotBePrepared pins the policy
// split: an unusable log destination costs the host its file log, not its boot.
func TestResilientSinkDegradesWhenTheDestinationCannotBePrepared(t *testing.T) {
	prepareErr := errors.New("read-only file system")
	primary := &stubLogSink{prepareErr: prepareErr}
	fallback := &stubLogSink{}
	sink := newResilientSink(primary, fallback)

	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare must degrade rather than fail: %v", err)
	}
	healthErr, _ := sink.Health()
	if !errors.Is(healthErr, prepareErr) {
		t.Fatalf("health = %v, want the prepare cause", healthErr)
	}
	if _, err := sink.Write([]byte("degraded-record")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got, want := fallback.payloads(), []string{"degraded-record"}; !equalStrings(got, want) {
		t.Fatalf("fallback payloads = %v, want %v", got, want)
	}
	if got := primary.payloads(); len(got) != 0 {
		t.Fatalf("unusable primary received %v", got)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestResilientSinkBreaksCircuitOnStickyFailure distinguishes a persistent sink
// failure (which must demote to stderr) from a transient refusal (which must not).
func TestResilientSinkBreaksCircuitOnStickyFailure(t *testing.T) {
	writeErr := errors.New("input/output error")
	primary := &stubLogSink{writeErr: writeErr, healthErr: writeErr}
	fallback := &stubLogSink{}
	sink := newResilientSink(primary, fallback)
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	if _, err := sink.Write([]byte("lost-record")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The first record is retried onto stderr by the worker, so the evidence of the
	// failure survives the failure itself.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if healthErr, _ := sink.Health(); errors.Is(healthErr, writeErr) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("circuit did not break: health = %v", healthErrOrNil(sink))
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := sink.Write([]byte("later-record")); err != nil {
		t.Fatalf("Write after break: %v", err)
	}
	got := fallback.payloads()
	if !containsString(got, "lost-record") || !containsString(got, "later-record") {
		t.Fatalf("fallback payloads = %v, want both records", got)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestResilientSinkKeepsWritingAfterTransientRefusal(t *testing.T) {
	primary := &stubLogSink{writeErr: errors.New("compression reservation does not fit the budget")}
	fallback := &stubLogSink{}
	sink := newResilientSink(primary, fallback)
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := sink.Write([]byte("refused")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, dropped := sink.Health(); dropped > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("refused write was not counted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if healthErr, _ := sink.Health(); healthErr != nil {
		t.Fatalf("transient refusal demoted the sink: %v", healthErr)
	}
	if got := fallback.payloads(); len(got) != 0 {
		t.Fatalf("transient refusal fell back to stderr: %v", got)
	}

	primary.mu.Lock()
	primary.writeErr = nil
	primary.mu.Unlock()
	if _, err := sink.Write([]byte("recovered")); err != nil {
		t.Fatalf("Write after recovery: %v", err)
	}
	deadline = time.Now().Add(2 * time.Second)
	for !containsString(primary.payloads(), "recovered") {
		if time.Now().After(deadline) {
			t.Fatalf("primary payloads = %v, want the recovered record", primary.payloads())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestResilientSinkSyncFlushesQueuedRecords guards the shutdown contract: Sync must
// act as a barrier, otherwise a graceful stop fsyncs an empty file and drops the
// tail of the log.
func TestResilientSinkSyncFlushesQueuedRecords(t *testing.T) {
	primary := &stubLogSink{block: make(chan struct{})}
	sink := newResilientSink(primary, &stubLogSink{})
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := sink.Write([]byte(fmt.Sprintf("record-%d", i))); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	close(primary.block)
	if err := sink.Sync(); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	primary.mu.Lock()
	defer primary.mu.Unlock()
	if primary.syncs == 0 {
		t.Fatal("Sync did not reach the primary sink")
	}
	if got := len(primary.written); got != 8 {
		t.Fatalf("Sync flushed %d of 8 queued records", got)
	}
	t.Cleanup(func() { _ = sink.Close() })
}

func TestResilientSinkCloseDrainsQueuedRecords(t *testing.T) {
	primary := &stubLogSink{}
	sink := newResilientSink(primary, &stubLogSink{})
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	var want []string
	for i := 0; i < 64; i++ {
		record := fmt.Sprintf("record-%d", i)
		want = append(want, record)
		if _, err := sink.Write([]byte(record)); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := primary.payloads(); !equalStrings(got, want) {
		t.Fatalf("drained payloads = %d entries, want %d in order", len(got), len(want))
	}
	if primary.closeCount() != 1 {
		t.Fatalf("primary closed %d times, want 1", primary.closeCount())
	}
}

// TestResilientSinkRejectsWritesBeforePrepare keeps the lifecycle contract: a log
// sink used before its Prepare is a programming error, not a silent no-op.
func TestResilientSinkRejectsWritesBeforePrepare(t *testing.T) {
	sink := newResilientSink(&stubLogSink{}, &stubLogSink{})
	if _, err := sink.Write([]byte("too-early")); !errors.Is(err, errLogSinkNotPrepared) {
		t.Fatalf("Write before Prepare = %v, want %v", err, errLogSinkNotPrepared)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close before Prepare: %v", err)
	}
	if _, err := sink.Write([]byte("after-close")); !errors.Is(err, errLogSinkClosed) {
		t.Fatalf("Write after Close = %v, want %v", err, errLogSinkClosed)
	}
}

func TestResilientSinkHealthNeverTouchesAStalledDestination(t *testing.T) {
	primary := &stubLogSink{block: make(chan struct{})}
	sink := newResilientSink(primary, &stubLogSink{})
	if err := sink.Prepare(); err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if _, err := sink.Write([]byte("queued")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The worker now holds the primary's lock inside a blocked write. Health is
	// polled on every /health request, so it must not queue behind it.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = sink.Health()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Health blocked behind a stalled destination")
	}
	close(primary.block)
	t.Cleanup(func() { _ = sink.Close() })
}

func healthErrOrNil(sink *resilientSink) error {
	err, _ := sink.Health()
	return err
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
