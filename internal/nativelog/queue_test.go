package nativelog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestNewQueueLogsStructuredNativeRecords(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	queue, err := NewQueue(4, zap.New(core))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	seenAt := time.Date(2026, 10, 20, 12, 30, 0, 0, time.UTC)
	if !queue.TryEnqueue(Record{Level: Info, StreamID: 17, Module: "engine.capture", Message: "frame acquired", SeenAt: seenAt}) {
		t.Fatal("TryEnqueue rejected a valid record")
	}
	pending, err := queue.Stop(context.Background())
	if err != nil || pending != 0 {
		t.Fatalf("Stop = (%d, %v), want (0, nil)", pending, err)
	}
	entries := observed.All()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Level != zap.InfoLevel || entry.LoggerName != "native.log" || entry.Message != "native event" {
		t.Fatalf("unexpected native log entry: %+v", entry)
	}
	fields := entry.ContextMap()
	if fields["module"] != "engine.capture" || fields["streamId"] != uint64(17) || fields["message"] != "frame acquired" || !fields["seenAt"].(time.Time).Equal(seenAt) {
		t.Fatalf("unexpected structured fields: %#v", fields)
	}
}

func TestNewQueueAcceptsEmptyNativeMessage(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	queue, err := NewQueue(1, zap.New(core))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !queue.TryEnqueue(Record{Level: Info, Module: "engine", SeenAt: time.Now()}) {
		t.Fatal("TryEnqueue rejected a valid empty native message")
	}
	pending, err := queue.Stop(context.Background())
	if err != nil || pending != 0 {
		t.Fatalf("Stop = (%d, %v), want a fully drained queue", pending, err)
	}
	entries := observed.All()
	if len(entries) != 1 || entries[0].ContextMap()["message"] != "" {
		t.Fatalf("empty native message was not preserved: %+v", entries)
	}
}

func TestQueueNonblockingAdmissionAndDropAccounting(t *testing.T) {
	queue := newQueue(1, func(Record) error { return nil })
	queue.admission.Lock()
	done := make(chan bool, 1)
	go func() { done <- queue.TryEnqueue(validRecord()) }()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("record unexpectedly passed admission while the lock was held")
		}
	case <-time.After(time.Second):
		queue.admission.Unlock()
		t.Fatal("TryEnqueue waited for admission synchronization")
	}
	queue.admission.Unlock()
	if got := queue.Dropped(); got != 1 {
		t.Fatalf("dropped = %d, want 1 for lock contention", got)
	}
	if !queue.TryEnqueue(validRecord()) {
		t.Fatal("first buffered record was rejected")
	}
	if queue.TryEnqueue(validRecord()) {
		t.Fatal("queue accepted a record while full")
	}
	queue.RecordDrop()
	if got := queue.Dropped(); got != 3 {
		t.Fatalf("dropped = %d, want 3 for contention, full queue, and RecordDrop", got)
	}
	pending, err := queue.Stop(context.Background())
	if !errors.Is(err, ErrNotStarted) || pending != 1 {
		t.Fatalf("Stop before Start = (%d, %v), want (1, ErrNotStarted)", pending, err)
	}
	if queue.TryEnqueue(validRecord()) || queue.Dropped() != 4 {
		t.Fatal("post-stop enqueue was not rejected and counted")
	}
}

func TestQueueConcurrentProducersAndSuccessfulDrain(t *testing.T) {
	var consumed atomic.Int64
	queue := newQueue(32, func(Record) error {
		consumed.Add(1)
		return nil
	})
	if err := queue.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	const producers = 8
	const recordsPerProducer = 500
	var accepted atomic.Int64
	var producersDone sync.WaitGroup
	producersDone.Add(producers)
	for i := 0; i < producers; i++ {
		go func() {
			defer producersDone.Done()
			for j := 0; j < recordsPerProducer; j++ {
				if queue.TryEnqueue(validRecord()) {
					accepted.Add(1)
				}
			}
		}()
	}
	producersDone.Wait()
	attempted := int64(producers * recordsPerProducer)
	if accepted.Load()+int64(queue.Dropped()) != attempted {
		t.Fatalf("accepted %d + dropped %d != attempted %d", accepted.Load(), queue.Dropped(), attempted)
	}
	pending, err := queue.Stop(context.Background())
	if err != nil || pending != 0 {
		t.Fatalf("Stop = (%d, %v), want a fully drained queue", pending, err)
	}
	if got := consumed.Load(); got != accepted.Load() {
		t.Fatalf("consumed %d records, accepted %d", got, accepted.Load())
	}
	if queue.TryEnqueue(validRecord()) {
		t.Fatal("post-stop enqueue was accepted")
	}
	if got := accepted.Load() + int64(queue.Dropped()); got != attempted+1 {
		t.Fatalf("post-stop attempt accounting = %d, want %d", got, attempted+1)
	}
}

func TestQueueStopTimeoutReportsPendingAndSinkErrors(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	sinkErr := errors.New("sink unavailable")
	queue := newQueue(1, func(Record) error {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		return sinkErr
	})
	if err := queue.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !queue.TryEnqueue(validRecord()) {
		t.Fatal("first record rejected")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("consumer did not begin sink I/O")
	}
	if !queue.TryEnqueue(validRecord()) {
		t.Fatal("buffered record rejected")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	pending, err := queue.Stop(ctx)
	if !errors.Is(err, context.DeadlineExceeded) || pending != 2 {
		t.Fatalf("timed Stop = (%d, %v), want two pending records and deadline error", pending, err)
	}
	releaseOnce.Do(func() { close(release) })
	ctx2, cancel2 := context.WithTimeout(context.Background(), time.Second)
	defer cancel2()
	pending, err = queue.Stop(ctx2)
	if err != nil || pending != 1 {
		t.Fatalf("Stop after unblocking sink = (%d, %v), want one queued record after timeout abort", pending, err)
	}
	if got := queue.SinkFailures(); got != 1 || !errors.Is(queue.LastSinkError(), sinkErr) {
		t.Fatalf("sink health = (%d, %v), want one reported sink error", got, queue.LastSinkError())
	}
}

func TestQueueDropSummaryIsEmittedOnStop(t *testing.T) {
	core, observed := observer.New(zap.WarnLevel)
	queue, err := NewQueue(1, zap.New(core))
	if err != nil {
		t.Fatalf("NewQueue: %v", err)
	}
	if err := queue.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	queue.RecordDrop()
	pending, err := queue.Stop(context.Background())
	if err != nil || pending != 0 {
		t.Fatalf("Stop = (%d, %v)", pending, err)
	}
	entries := observed.All()
	if len(entries) != 1 || entries[0].Message != "native log records dropped" || entries[0].ContextMap()["dropped_total"] != uint64(1) {
		t.Fatalf("unexpected drop summary: %+v", entries)
	}
}

func validRecord() Record {
	return Record{Level: Info, Module: "test", Message: "record", SeenAt: time.Now().UTC()}
}
