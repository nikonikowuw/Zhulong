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

	store := NewStore(dbStore)
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

func TestStoreFilterByMultipleActionsStatusAndDate(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)
	logs := []*AuditLog{
		{CreatedAt: baseTime.Add(time.Minute), Action: ActionAuthLogin, Status: StatusSuccess},
		{CreatedAt: baseTime.Add(2 * time.Minute), Action: ActionCameraDelete, Status: StatusSuccess},
		{CreatedAt: baseTime.Add(3 * time.Minute), Action: ActionAuthLogin, Status: StatusFailed},
		{CreatedAt: baseTime.Add(2 * time.Minute), Action: ActionCameraCreate, Status: StatusSuccess},
		{CreatedAt: baseTime.Add(4 * time.Minute), Action: ActionCameraDelete, Status: StatusSuccess},
	}
	if err := store.CreateBatch(ctx, logs); err != nil {
		t.Fatalf("CreateBatch failed: %v", err)
	}

	startTime := baseTime.Add(time.Minute)
	endTime := baseTime.Add(3 * time.Minute)
	items, total, err := store.List(ctx, Filter{
		Actions:   []string{ActionAuthLogin, ActionCameraDelete},
		Status:    StatusSuccess,
		StartTime: &startTime,
		EndTime:   &endTime,
		Limit:     1,
		Offset:    1,
	})
	if err != nil {
		t.Fatalf("List with multiple action filters failed: %v", err)
	}
	if total != 2 || len(items) != 1 {
		t.Fatalf("expected 2 filtered rows and 1 paginated item, got total=%d items=%d", total, len(items))
	}
	if items[0].Action != ActionAuthLogin {
		t.Fatalf("expected second matching row after pagination to be auth.login, got %s", items[0].Action)
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

type mockNilDBProvider struct{}

func (mockNilDBProvider) DB() *gorm.DB { return nil }

func TestStoreUnreadyDatabase(t *testing.T) {
	ctx := context.Background()

	for name, store := range map[string]*Store{
		"unready": NewStore(mockNilDBProvider{}),
		"nil":     NewStore(nil),
	} {
		t.Run(name, func(t *testing.T) {
			if err := store.Create(ctx, &AuditLog{Action: "test"}); err == nil {
				t.Errorf("%s: expected error on Create, got nil", name)
			}
			if err := store.CreateBatch(ctx, []*AuditLog{{Action: "test"}}); err == nil {
				t.Errorf("%s: expected error on CreateBatch, got nil", name)
			}
			if _, _, err := store.List(ctx, Filter{}); err == nil {
				t.Errorf("%s: expected error on List, got nil", name)
			}
			if _, err := store.Count(ctx); err == nil {
				t.Errorf("%s: expected error on Count, got nil", name)
			}
			if _, err := store.Prune(ctx, 10); err == nil {
				t.Errorf("%s: expected error on Prune, got nil", name)
			}
			if _, err := store.Clear(ctx); err == nil {
				t.Errorf("%s: expected error on Clear, got nil", name)
			}
		})
	}
}

func TestPruneExemptsAuditClear(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Now().UTC()
	// One older-than-everything clear record that must survive pruning.
	if err := store.Create(ctx, &AuditLog{
		CreatedAt: base.Add(-time.Hour),
		Action:    ActionAuditClear,
		Username:  UsernameUnknown,
		Target:    "audit_logs",
		Status:    StatusSuccess,
	}); err != nil {
		t.Fatalf("seed audit.clear failed: %v", err)
	}

	// More ordinary records than the retention limit allows.
	const maxEntries = 5
	for i := 0; i < 12; i++ {
		if err := store.Create(ctx, &AuditLog{
			CreatedAt: base.Add(time.Duration(i) * time.Millisecond),
			Action:    ActionCameraUpdate,
			Target:    fmt.Sprintf("camera:%d", i),
			Username:  UsernameUnknown,
			Status:    StatusSuccess,
		}); err != nil {
			t.Fatalf("seed ordinary record %d failed: %v", i, err)
		}
	}

	if _, err := store.Prune(ctx, maxEntries); err != nil {
		t.Fatalf("Prune failed: %v", err)
	}

	clears, total, err := store.List(ctx, Filter{Action: ActionAuditClear})
	if err != nil {
		t.Fatalf("List audit.clear failed: %v", err)
	}
	if len(clears) != 1 {
		t.Fatalf("expected audit.clear to survive pruning, got %d of %d total", len(clears), total)
	}
	if clears[0].Target != "audit_logs" {
		t.Fatalf("unexpected surviving audit.clear target: %q", clears[0].Target)
	}
}

func TestClearWithAuditPersistsSelfAudit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := store.Create(ctx, &AuditLog{
			Action:   ActionCameraDelete,
			Target:   fmt.Sprintf("camera:%d", i),
			Username: UsernameUnknown,
			Status:   StatusSuccess,
		}); err != nil {
			t.Fatalf("seed failed: %v", err)
		}
	}

	entry := &AuditLog{
		Action:   ActionAuditClear,
		Target:   "audit_logs",
		IP:       "10.0.0.4",
		Username: "operator",
		Status:   StatusSuccess,
	}
	cleared, err := store.ClearWithAudit(ctx, entry)
	if err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}
	if cleared != 3 {
		t.Fatalf("expected 3 cleared, got %d", cleared)
	}

	clears, total, err := store.List(ctx, Filter{Action: ActionAuditClear})
	if err != nil {
		t.Fatalf("List audit.clear failed: %v", err)
	}
	if total != 1 || len(clears) != 1 {
		t.Fatalf("expected exactly one audit.clear record, got total=%d len=%d", total, len(clears))
	}
	if clears[0].IP != "10.0.0.4" {
		t.Fatalf("expected request IP to be persisted, got %q", clears[0].IP)
	}
	if clears[0].Detail != "deleted=3" {
		t.Fatalf("expected deleted count in detail, got %q", clears[0].Detail)
	}
}

func TestClearWithAuditKeepsPreviousClearRecords(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.Create(ctx, &AuditLog{
		Action:   ActionAuditClear,
		Target:   "audit_logs",
		Username: "first-operator",
		Status:   StatusSuccess,
	}); err != nil {
		t.Fatalf("seed first clear failed: %v", err)
	}
	if err := store.Create(ctx, &AuditLog{
		Action:   ActionCameraCreate,
		Target:   "camera:1",
		Username: UsernameUnknown,
		Status:   StatusSuccess,
	}); err != nil {
		t.Fatalf("seed ordinary record failed: %v", err)
	}

	// Second clear: only the ordinary record may be deleted.
	cleared, err := store.ClearWithAudit(ctx, &AuditLog{
		Action:   ActionAuditClear,
		Target:   "audit_logs",
		Username: "second-operator",
		Status:   StatusSuccess,
	})
	if err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("expected only the ordinary record to be cleared, got %d", cleared)
	}

	clears, total, err := store.List(ctx, Filter{Action: ActionAuditClear})
	if err != nil {
		t.Fatalf("List audit.clear failed: %v", err)
	}
	if total != 2 || len(clears) != 2 {
		t.Fatalf("expected both clear records to survive, got total=%d len=%d", total, len(clears))
	}

	remaining, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("expected only clear records to remain, got %d", remaining)
	}
}

func TestClearWithAuditRespectsBeforeFilter(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Now().UTC()
	old := &AuditLog{CreatedAt: base.Add(-2 * time.Hour), Action: ActionCameraDelete, Status: StatusSuccess}
	recent := &AuditLog{CreatedAt: base, Action: ActionCameraDelete, Status: StatusSuccess}
	if err := store.CreateBatch(ctx, []*AuditLog{old, recent}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	cleared, err := store.ClearWithAudit(ctx, &AuditLog{
		Action: ActionAuditClear,
		Target: "audit_logs",
		Status: StatusSuccess,
	}, base.Add(-time.Hour))
	if err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("expected only the old record to be cleared, got %d", cleared)
	}

	// The recent ordinary record plus the new self-audit record remain.
	total, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 remaining records, got %d", total)
	}
}

func TestClearWithAuditFailsWhenDatabaseUnavailable(t *testing.T) {
	ctx := context.Background()

	for name, store := range map[string]*Store{
		"unready": NewStore(mockNilDBProvider{}),
		"nil":     NewStore(nil),
	} {
		t.Run(name, func(t *testing.T) {
			cleared, err := store.ClearWithAudit(ctx, &AuditLog{Action: ActionAuditClear})
			if err == nil {
				t.Fatalf("%s: expected an error so the caller cannot report success", name)
			}
			if cleared != 0 {
				t.Fatalf("%s: expected zero cleared on failure, got %d", name, cleared)
			}
		})
	}
}
