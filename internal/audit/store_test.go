package audit

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := NewStore(dbStore.DB)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return store, cleanup
}

func TestStoreCreateAndList(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()

	// Initial count should be 0
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 count, got %d", count)
	}

	// Create single log
	now := time.Now().UTC()
	log := &AuditLog{
		CreatedAt: now,
		IP:        "192.168.1.100",
		Username:  "admin",
		Action:    ActionAuthLogin,
		Target:    "user:admin",
		Detail:    `{"method":"password"}`,
		Status:    StatusSuccess,
	}

	if err := store.Create(ctx, log); err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if log.ID == 0 {
		t.Fatal("expected non-zero ID after create")
	}

	// List
	items, total, err := store.List(ctx, Filter{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 item, got total=%d len=%d", total, len(items))
	}
	if items[0].Action != ActionAuthLogin || items[0].IP != "192.168.1.100" {
		t.Fatalf("item mismatch: %+v", items[0])
	}
}

func TestStoreFilterByActionStatusAndDate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	logs := []*AuditLog{
		{
			CreatedAt: baseTime.Add(1 * time.Minute),
			IP:        "10.0.0.1",
			Username:  "admin",
			Action:    ActionAuthLogin,
			Target:    "user:admin",
			Status:    StatusSuccess,
		},
		{
			CreatedAt: baseTime.Add(2 * time.Minute),
			IP:        "10.0.0.2",
			Username:  "admin",
			Action:    ActionAuthLogin,
			Target:    "user:admin",
			Status:    StatusFailed,
			ErrorMsg:  "invalid password",
		},
		{
			CreatedAt: baseTime.Add(3 * time.Minute),
			IP:        "10.0.0.3",
			Username:  "admin",
			Action:    ActionCameraCreate,
			Target:    "camera:cam_1",
			Status:    StatusSuccess,
		},
	}

	if err := store.CreateBatch(ctx, logs); err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	// Filter by action
	items, total, err := store.List(ctx, Filter{Action: ActionAuthLogin})
	if err != nil {
		t.Fatalf("List by action failed: %v", err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("expected 2 auth.login items, got %d", total)
	}

	// Filter by status
	items, total, err = store.List(ctx, Filter{Status: StatusFailed})
	if err != nil {
		t.Fatalf("List by status failed: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].ErrorMsg != "invalid password" {
		t.Fatalf("expected 1 failed item, got %d", total)
	}

	// Filter by time range
	t1 := baseTime.Add(90 * time.Second)
	t2 := baseTime.Add(150 * time.Second)
	items, total, err = store.List(ctx, Filter{StartTime: &t1, EndTime: &t2})
	if err != nil {
		t.Fatalf("List by time range failed: %v", err)
	}
	if total != 1 || len(items) != 1 || items[0].IP != "10.0.0.2" {
		t.Fatalf("expected 1 item within time range, got %d", total)
	}
}

func TestStorePruneFIFO(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	// Create 10 entries with distinct timestamps
	var logs []*AuditLog
	for i := 0; i < 10; i++ {
		logs = append(logs, &AuditLog{
			CreatedAt: baseTime.Add(time.Duration(i) * time.Minute),
			IP:        "127.0.0.1",
			Username:  "admin",
			Action:    ActionCameraUpdate,
			Target:    fmt.Sprintf("camera:cam_%d", i),
			Status:    StatusSuccess,
		})
	}
	if err := store.CreateBatch(ctx, logs); err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	// Prune with maxEntries = 6, expecting 4 oldest pruned
	deleted, err := store.Prune(ctx, 6)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if deleted != 4 {
		t.Fatalf("expected 4 deleted, got %d", deleted)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if count != 6 {
		t.Fatalf("expected 6 remaining, got %d", count)
	}

	// The remaining items should be cam_4 through cam_9 (newest first in list)
	remaining, _, err := store.List(ctx, Filter{Limit: 10})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(remaining) != 6 {
		t.Fatalf("expected 6 items, got %d", len(remaining))
	}
	// Oldest remaining should be cam_4
	if remaining[5].Target != "camera:cam_4" {
		t.Fatalf("expected oldest remaining to be cam_4, got %s", remaining[5].Target)
	}
	// Newest should be cam_9
	if remaining[0].Target != "camera:cam_9" {
		t.Fatalf("expected newest to be cam_9, got %s", remaining[0].Target)
	}

	// Prune again when count is within maxEntries -> should do nothing
	deleted, err = store.Prune(ctx, 6)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted, got %d", deleted)
	}
}

func TestStoreClear(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	logs := []*AuditLog{
		{
			CreatedAt: baseTime.Add(1 * time.Minute),
			Action:    ActionAuthLogout,
			Status:    StatusSuccess,
		},
		{
			CreatedAt: baseTime.Add(10 * time.Minute),
			Action:    ActionAuthLogout,
			Status:    StatusSuccess,
		},
	}
	if err := store.CreateBatch(ctx, logs); err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	// Clear before baseTime + 5 minutes -> deletes 1
	deleted, err := store.Clear(ctx, baseTime.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Clear with time failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// Clear all
	deleted, err = store.Clear(ctx)
	if err != nil {
		t.Fatalf("Clear all failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	count, _ := store.Count(ctx)
	if count != 0 {
		t.Fatalf("expected 0 count, got %d", count)
	}
}

func TestStoreUnreadyDatabase(t *testing.T) {
	// Provider returns nil, simulating constructor phase before OpenAndMigrate
	store := NewStore(func() *gorm.DB { return nil })
	ctx := context.Background()

	if err := store.Create(ctx, &AuditLog{Action: "test"}); err == nil {
		t.Fatalf("expected error when DB is unready, got nil")
	}
	if err := store.CreateBatch(ctx, []*AuditLog{{Action: "test"}}); err == nil {
		t.Fatalf("expected error on CreateBatch when DB is unready, got nil")
	}
	if _, _, err := store.List(ctx, Filter{}); err == nil {
		t.Fatalf("expected error on List when DB is unready, got nil")
	}
	if _, err := store.Count(ctx); err == nil {
		t.Fatalf("expected error on Count when DB is unready, got nil")
	}
	if _, err := store.Prune(ctx, 10); err == nil {
		t.Fatalf("expected error on Prune when DB is unready, got nil")
	}
	if _, err := store.Clear(ctx); err == nil {
		t.Fatalf("expected error on Clear when DB is unready, got nil")
	}
}

