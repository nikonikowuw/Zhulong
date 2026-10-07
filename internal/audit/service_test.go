package audit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestServiceAsyncBatchAndDrain(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := Config{
		BufferSize:    100,
		BatchSize:     5,
		FlushInterval: 50 * time.Millisecond,
		MaxEntries:    1000,
	}

	svc := NewService(store, zap.NewNop(), cfg)
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record 8 entries (5 should flush immediately by batch, 3 on ticker or drain)
	for i := 0; i < 8; i++ {
		svc.Record(Entry{
			IP:       "127.0.0.1",
			Action:   ActionCameraCreate,
			Target:   fmt.Sprintf("camera:%d", i),
			Status:   StatusSuccess,
			Username: "admin",
		})
	}

	// Stop service, which must drain all 8 entries
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	total, err := svc.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if total != 8 {
		t.Fatalf("expected 8 persisted entries, got %d", total)
	}
}

func TestServiceBufferFullNonBlocking(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	// Capacity 2, worker NOT started so nothing is drained
	cfg := Config{
		BufferSize:    2,
		BatchSize:     10,
		FlushInterval: 1 * time.Second,
		MaxEntries:    100,
	}

	svc := NewService(store, zap.NewNop(), cfg)

	// Enqueue 5 items - should not block or panic even though queue capacity is 2
	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			svc.Record(Entry{
				Action: ActionAuthLogin,
				Target: "admin",
			})
		}
		close(done)
	}()

	select {
	case <-done:
		// Succeeded without blocking
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Record blocked on full buffer")
	}
}

func TestServicePruneOnBatch(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	cfg := Config{
		BufferSize:    100,
		BatchSize:     5,
		FlushInterval: 20 * time.Millisecond,
		MaxEntries:    6,
	}

	svc := NewService(store, zap.NewNop(), cfg)
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Record 10 entries with small delays to ensure monotonic CreatedAt
	for i := 0; i < 10; i++ {
		svc.Record(Entry{
			Action:    ActionCameraUpdate,
			Target:    fmt.Sprintf("camera:%d", i),
			CreatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		})
	}

	// Wait and stop to drain
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	total, err := svc.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	// MaxEntries is 6, so prune should have trimmed it down to 6
	if total != 6 {
		t.Fatalf("expected 6 entries after prune, got %d", total)
	}
}
