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

func setupTestStore(t *testing.T) *Store {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close test database: %v", err)
		}
	})
	return NewStore(dbStore)
}

// countRows returns how many rows are persisted. The store does not expose a
// Count query: every caller can read the total from List, so this helper keeps
// that surface closed and still lets table-size assertions stay direct.
func countRows(t *testing.T, store *Store) int64 {
	t.Helper()
	_, total, err := store.List(context.Background(), Filter{Limit: 1})
	if err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return total
}

func TestStoreCreateAndList(t *testing.T) {
	store := setupTestStore(t)

	ctx := context.Background()

	// Initial count should be 0
	if count := countRows(t, store); count != 0 {
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
	store := setupTestStore(t)

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
	store := setupTestStore(t)

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
	store := setupTestStore(t)

	ctx := context.Background()
	baseTime := time.Date(2025, 1, 1, 10, 0, 0, 0, time.UTC)

	// Create 10 entries with distinct timestamps
	logs := []*AuditLog{}
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

	if count := countRows(t, store); count != 6 {
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

// TestPruneBoundsPrunableRowsWhenClearRecordsDominate pins the counting fix:
// audit.clear rows are exempt from deletion, so they must not be counted as
// excess. Otherwise the delete statement runs out of eligible rows and the
// table stays above maxEntries for good.
func TestPruneBoundsPrunableRowsWhenClearRecordsDominate(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	base := time.Now().UTC()

	for i := 0; i < 20; i++ {
		if err := store.Create(ctx, &AuditLog{
			CreatedAt: base.Add(-time.Hour + time.Duration(i)*time.Millisecond),
			Action:    ActionAuditClear,
			Status:    StatusSuccess,
		}); err != nil {
			t.Fatalf("seed audit.clear %d failed: %v", i, err)
		}
	}
	for i := 0; i < 12; i++ {
		if err := store.Create(ctx, &AuditLog{
			CreatedAt: base.Add(time.Duration(i) * time.Millisecond),
			Action:    ActionCameraUpdate,
			Status:    StatusSuccess,
		}); err != nil {
			t.Fatalf("seed ordinary record %d failed: %v", i, err)
		}
	}

	const maxEntries = 5
	deleted, err := store.Prune(ctx, maxEntries)
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if deleted != 7 {
		t.Fatalf("expected 7 prunable records deleted, got %d", deleted)
	}

	// Exempt rows survive, and pruning is idempotent once the limit is met.
	deleted, err = store.Prune(ctx, maxEntries)
	if err != nil {
		t.Fatalf("second Prune failed: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected no further deletions, got %d", deleted)
	}

	items, total, err := store.List(ctx, Filter{Limit: 100})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != int64(20+maxEntries) || len(items) != int(total) {
		t.Fatalf("expected 20 exempt plus %d prunable rows, got total=%d items=%d",
			maxEntries, total, len(items))
	}

	clears := 0
	for _, item := range items {
		if item.Action == ActionAuditClear {
			clears++
		}
	}
	if clears != 20 {
		t.Fatalf("expected all 20 audit.clear records to survive, got %d", clears)
	}
}

func TestStoreClearAllPreservesSelfAudit(t *testing.T) {
	store := setupTestStore(t)

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

	auditEntry := func() *AuditLog {
		return &AuditLog{Action: ActionAuditClear, Target: "audit_logs", Status: StatusSuccess}
	}

	// Clearing before baseTime + 5 minutes deletes 1 ordinary record and adds
	// the self-audit record in the same transaction.
	before := baseTime.Add(5 * time.Minute)
	deleted, err := store.ClearWithAudit(ctx, auditEntry(), &before)
	if err != nil {
		t.Fatalf("ClearWithAudit with before failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	// Clearing everything removes the remaining ordinary record; only the two
	// self-audit records survive.
	deleted, err = store.ClearWithAudit(ctx, auditEntry(), nil)
	if err != nil {
		t.Fatalf("ClearWithAudit all failed: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 deleted, got %d", deleted)
	}

	if count := countRows(t, store); count != 2 {
		t.Fatalf("expected only the 2 audit.clear records to remain, got %d", count)
	}
}

type mockNilDBProvider struct{}

func (mockNilDBProvider) DB() *gorm.DB { return nil }

// unconfiguredStore names one way a store can have no usable database.
type unconfiguredStore struct {
	name  string
	store *Store
}

// unconfiguredStores lists the two ways a store has no usable database: a
// provider that yields nil and a nil provider.
func unconfiguredStores() []unconfiguredStore {
	return []unconfiguredStore{
		{name: "unready", store: NewStore(mockNilDBProvider{})},
		{name: "nil", store: NewStore(nil)},
	}
}

func TestStoreUnreadyDatabase(t *testing.T) {
	ctx := context.Background()

	for _, tc := range unconfiguredStores() {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.store.Create(ctx, &AuditLog{Action: "test"}); err == nil {
				t.Errorf("%s: expected error on Create, got nil", tc.name)
			}
			if err := tc.store.CreateBatch(ctx, []*AuditLog{{Action: "test"}}); err == nil {
				t.Errorf("%s: expected error on CreateBatch, got nil", tc.name)
			}
			if _, _, err := tc.store.List(ctx, Filter{}); err == nil {
				t.Errorf("%s: expected error on List, got nil", tc.name)
			}
			if _, err := tc.store.Prune(ctx, 10); err == nil {
				t.Errorf("%s: expected error on Prune, got nil", tc.name)
			}
			if _, err := tc.store.ClearWithAudit(ctx, &AuditLog{Action: ActionAuditClear}, nil); err == nil {
				t.Errorf("%s: expected error on ClearWithAudit, got nil", tc.name)
			}
		})
	}
}

func TestPruneExemptsAuditClear(t *testing.T) {
	store := setupTestStore(t)
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
	store := setupTestStore(t)
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
	cleared, err := store.ClearWithAudit(ctx, entry, nil)
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
	store := setupTestStore(t)
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
	}, nil)
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

	if remaining := countRows(t, store); remaining != 2 {
		t.Fatalf("expected only clear records to remain, got %d", remaining)
	}
}

func TestClearWithAuditRespectsBeforeFilter(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	old := &AuditLog{CreatedAt: base.Add(-2 * time.Hour), Action: ActionCameraDelete, Status: StatusSuccess}
	recent := &AuditLog{CreatedAt: base, Action: ActionCameraDelete, Status: StatusSuccess}
	if err := store.CreateBatch(ctx, []*AuditLog{old, recent}); err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	before := base.Add(-time.Hour)
	cleared, err := store.ClearWithAudit(ctx, &AuditLog{
		Action: ActionAuditClear,
		Target: "audit_logs",
		Status: StatusSuccess,
	}, &before)
	if err != nil {
		t.Fatalf("ClearWithAudit failed: %v", err)
	}
	if cleared != 1 {
		t.Fatalf("expected only the old record to be cleared, got %d", cleared)
	}

	// The recent ordinary record plus the new self-audit record remain.
	if total := countRows(t, store); total != 2 {
		t.Fatalf("expected 2 remaining records, got %d", total)
	}
}

func TestClearWithAuditFailsWhenDatabaseUnavailable(t *testing.T) {
	ctx := context.Background()

	for _, tc := range unconfiguredStores() {
		t.Run(tc.name, func(t *testing.T) {
			cleared, err := tc.store.ClearWithAudit(ctx, &AuditLog{Action: ActionAuditClear}, nil)
			if err == nil {
				t.Fatalf("%s: expected an error so the caller cannot report success", tc.name)
			}
			if cleared != 0 {
				t.Fatalf("%s: expected zero cleared on failure, got %d", tc.name, cleared)
			}
		})
	}
}

func TestStoreCreateNilAndCreateBatchSkipsNilAndNormalizesDefaults(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()

	if err := store.Create(ctx, nil); err != nil {
		t.Fatalf("Create(nil) should be a noop, got %v", err)
	}

	if err := store.CreateBatch(ctx, []*AuditLog{nil}); err != nil {
		t.Fatalf("CreateBatch all-nil should be a noop, got %v", err)
	}

	err := store.CreateBatch(ctx, []*AuditLog{
		nil,
		{Action: ActionCameraCreate, Target: "camera:1"},
		nil,
	})
	if err != nil {
		t.Fatalf("CreateBatch with mixed nil failed: %v", err)
	}

	items, total, err := store.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("expected 1 persisted item, got total=%d len=%d", total, len(items))
	}
	if items[0].Username != UsernameUnknown {
		t.Fatalf("expected direct Store write to normalize empty username to %q, got %q", UsernameUnknown, items[0].Username)
	}
	if items[0].Status != StatusSuccess {
		t.Fatalf("expected default status %q, got %q", StatusSuccess, items[0].Status)
	}
	if items[0].CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be populated")
	}
}

func TestStoreListWithZeroLimitAndNonZeroOffset(t *testing.T) {
	store := setupTestStore(t)
	ctx := context.Background()
	base := time.Date(2025, 2, 1, 10, 0, 0, 0, time.UTC)

	for i := 0; i < 4; i++ {
		if err := store.Create(ctx, &AuditLog{
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
			Action:    ActionCameraUpdate,
			Target:    fmt.Sprintf("camera:%d", i),
		}); err != nil {
			t.Fatalf("seed %d failed: %v", i, err)
		}
	}

	items, total, err := store.List(ctx, Filter{Offset: 2})
	if err != nil {
		t.Fatalf("List with Limit=0 and Offset=2 must not fail SQLite syntax: %v", err)
	}
	if total != 4 || len(items) != 2 {
		t.Fatalf("expected total=4 len=2, got total=%d len=%d", total, len(items))
	}
	if items[0].Target != "camera:1" || items[1].Target != "camera:0" {
		t.Fatalf("unexpected offset items: %+v", items)
	}
}
