package audit

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

// errSentinel stands in for any operation failure in identity tests.
var errSentinel = errors.New("operation failed")

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

func TestServiceRecordEventResolvesActorFromContext(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	svc := NewService(store, zap.NewNop(), Config{
		BufferSize:    100,
		BatchSize:     10,
		FlushInterval: 10 * time.Millisecond,
		MaxEntries:    1000,
	})
	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	authed := WithActor(ctx, Actor{IP: "10.1.2.3", Username: "operator", Source: SourceWeb})
	svc.RecordEvent(authed, ActionCameraCreate, "camera:1", "created", nil)
	svc.RecordEvent(authed, ActionCameraDelete, "camera:2", "", errSentinel)
	svc.RecordEvent(ctx, ActionAuthLogout, "user:someone", "", nil)
	svc.RecordSystem("time.rtc_heal", "system_time", "restored", nil)

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	items, total, err := svc.List(ctx, Filter{Limit: 50})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected 4 persisted entries, got %d", total)
	}

	byAction := make(map[string]AuditLog, len(items))
	for _, item := range items {
		byAction[item.Action] = item
	}

	created := byAction[ActionCameraCreate]
	if created.IP != "10.1.2.3" || created.Username != "operator" {
		t.Fatalf("expected actor IP and username, got ip=%q user=%q", created.IP, created.Username)
	}
	if created.Status != StatusSuccess {
		t.Fatalf("expected success status, got %q", created.Status)
	}

	failed := byAction[ActionCameraDelete]
	if failed.Status != StatusFailed || failed.ErrorMsg == "" {
		t.Fatalf("expected failed status with message, got status=%q msg=%q", failed.Status, failed.ErrorMsg)
	}
	if failed.IP != "10.1.2.3" {
		t.Fatalf("expected IP to be kept on failure, got %q", failed.IP)
	}

	// No actor in context must not be promoted to a system event.
	unattributed := byAction[ActionAuthLogout]
	if unattributed.Username != UsernameUnknown {
		t.Fatalf("expected %q, got %q", UsernameUnknown, unattributed.Username)
	}
	if unattributed.IP != "" {
		t.Fatalf("expected empty IP, got %q", unattributed.IP)
	}

	system := byAction["time.rtc_heal"]
	if system.Username != UsernameSystem {
		t.Fatalf("expected system username, got %q", system.Username)
	}
	if system.IP != "" {
		t.Fatalf("expected empty IP for system event, got %q", system.IP)
	}
}

func TestServiceClearWithAuditUsesContextActor(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	svc := NewService(store, zap.NewNop())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := svc.RecordSync(ctx, Entry{Action: ActionCameraCreate, Target: "camera:x"}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	cleared, err := svc.ClearWithAudit(WithActor(ctx, Actor{IP: "172.16.0.8", Username: "operator"}), "audit_logs")
	if err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}
	if cleared != 3 {
		t.Fatalf("expected 3 cleared, got %d", cleared)
	}

	items, total, err := svc.List(ctx, Filter{Action: ActionAuditClear})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected one clear record, got total=%d len=%d", total, len(items))
	}
	if items[0].IP != "172.16.0.8" || items[0].Username != "operator" {
		t.Fatalf("expected actor to be recorded, got ip=%q user=%q", items[0].IP, items[0].Username)
	}
}

func TestServiceClearWithAuditWithoutActorIsUnknown(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	svc := NewService(store, zap.NewNop())
	ctx := context.Background()

	if _, err := svc.ClearWithAudit(ctx, "audit_logs"); err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}

	items, _, err := svc.List(ctx, Filter{Action: ActionAuditClear})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one clear record, got %d", len(items))
	}
	if items[0].Username != UsernameUnknown {
		t.Fatalf("expected %q instead of a hardcoded admin, got %q", UsernameUnknown, items[0].Username)
	}
	if items[0].IP != "" {
		t.Fatalf("expected empty IP, got %q", items[0].IP)
	}
}
