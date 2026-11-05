package record_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func pendingCount(t *testing.T, dbStore interface{ DB() *gorm.DB }) int64 {
	t.Helper()
	var count int64
	if err := dbStore.DB().Table("pending_snapshot_deletes").Count(&count).Error; err != nil {
		t.Fatalf("count pending: %v", err)
	}
	return count
}

func TestRetentionPrunerPendingOutboxRetriesAndClears(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Fresh rows: the TTL pruner would never touch them. The outbox must.
	ids := seedRecordRows(t, store, "cam_01", time.Now().UTC().Add(-time.Hour))

	pruner := record.NewRetentionPruner(store, zap.NewNop(), record.RetentionOptions{})
	if _, err := pruner.EnqueueSnapshotDeletes(ctx, []string{ids.recID, ids.capID, ids.recID}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := pendingCount(t, dbStore); got != 2 {
		t.Fatalf("pending rows = %d, want 2 (deduplicated)", got)
	}

	// Canceled context must leave both the rows and the pending IDs intact.
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := pruner.ProcessPendingSnapshotDeletes(canceled, 0); err == nil {
		t.Fatal("expected canceled context to fail pending processing")
	}
	if got := pendingCount(t, dbStore); got != 2 {
		t.Fatalf("pending rows after canceled pass = %d, want 2", got)
	}
	assertRecordRows(t, store, ids, true, true, true)

	if _, err := pruner.ProcessPendingSnapshotDeletes(ctx, 0); err != nil {
		t.Fatalf("process pending: %v", err)
	}
	if got := pendingCount(t, dbStore); got != 0 {
		t.Fatalf("pending rows after success = %d, want 0", got)
	}
	assertRecordRows(t, store, ids, false, true, false)

	// Replaying the same IDs is idempotent and must not touch the alarm row.
	if _, err := pruner.EnqueueSnapshotDeletes(ctx, []string{ids.recID, ids.capID}); err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if _, err := pruner.ProcessPendingSnapshotDeletes(ctx, 0); err != nil {
		t.Fatalf("replay pending: %v", err)
	}
	if got := pendingCount(t, dbStore); got != 0 {
		t.Fatalf("pending rows after replay = %d, want 0", got)
	}
	assertRecordRows(t, store, ids, false, true, false)
}

func TestStoreExistingAlarmIDs(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ids := seedRecordRows(t, store, "cam_01", time.Now().UTC())

	found, err := store.ExistingAlarmIDs(ctx, []string{ids.alarmID, ids.capID, ids.alarmID})
	if err != nil {
		t.Fatalf("ExistingAlarmIDs: %v", err)
	}
	if !found[ids.alarmID] {
		t.Error("alarm ID must be reported as protected")
	}
	if found[ids.capID] {
		t.Error("capture ID must not be reported as an alarm")
	}
}

func TestStoreAlarmEvidencePaths(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	ids := seedRecordRows(t, store, "cam_01", time.Now().UTC())
	paths, err := store.AlarmEvidencePaths(ctx)
	if err != nil {
		t.Fatalf("AlarmEvidencePaths: %v", err)
	}

	want := "snapshots/cam_01/alarm-" + ids.alarmID + ".jpg"
	found := false
	for _, path := range paths {
		if path == want {
			found = true
		}
	}
	if !found {
		t.Fatalf("alarm evidence paths = %v, want %q", paths, want)
	}

	// Insert additional alarm with duplicate paths and verify deduplication
	extraAlarm := &record.Alarm{
		ID:        "extra_alarm_1",
		CameraID:  "cam_01",
		ImagePath: want, // duplicate path
		CropPath:  "snapshots/cam_01/crop-extra.jpg",
	}
	if err := store.CreateAlarm(ctx, extraAlarm); err != nil {
		t.Fatalf("CreateAlarm: %v", err)
	}

	paths2, err := store.AlarmEvidencePaths(ctx)
	if err != nil {
		t.Fatalf("AlarmEvidencePaths2: %v", err)
	}
	seenCount := 0
	hasCrop := false
	for _, p := range paths2 {
		if p == want {
			seenCount++
		}
		if p == "snapshots/cam_01/crop-extra.jpg" {
			hasCrop = true
		}
	}
	if seenCount != 1 {
		t.Fatalf("expected duplicate path %q to appear exactly once, got %d", want, seenCount)
	}
	if !hasCrop {
		t.Fatalf("expected crop path to be included")
	}
}

type staticMediaRoot string

func (s staticMediaRoot) MediaRoot(context.Context) (string, error) { return string(s), nil }

func TestSnapshotStoreEnsureEvidenceLocks(t *testing.T) {
	root := t.TempDir()
	rel := "snapshots/cam-01/2026-11-04/01ABC.jpg"
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("jpeg"), 0o640); err != nil {
		t.Fatal(err)
	}

	store := record.NewSnapshotStore(nil, staticMediaRoot(root), nil, 0, zap.NewNop())
	ctx := context.Background()

	written, err := store.EnsureEvidenceLocks(ctx, []string{rel, "snapshots/missing.jpg"})
	if err != nil {
		t.Fatalf("EnsureEvidenceLocks: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	if _, err := os.Stat(abs + ".lock"); err != nil {
		t.Fatalf("lock sidecar missing: %v", err)
	}

	written, err = store.EnsureEvidenceLocks(ctx, []string{rel})
	if err != nil {
		t.Fatalf("second EnsureEvidenceLocks: %v", err)
	}
	if written != 0 {
		t.Fatalf("second backfill wrote %d locks, want 0", written)
	}

	if _, err := store.EnsureEvidenceLocks(ctx, []string{"../escape.jpg"}); err == nil {
		t.Fatal("path escaping the media root must be rejected")
	}
}
