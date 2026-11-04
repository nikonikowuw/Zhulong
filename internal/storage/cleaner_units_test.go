package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

const testGB = uint64(1024 * 1024 * 1024)

func snapshotFile(t *testing.T, mediaDir, name string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(mediaDir, "snapshots", "cam-01", "2026-11-04")
	mustMkdirAll(t, dir)
	path := filepath.Join(dir, name)
	mustWriteFile(t, path, []byte("evidence"))
	mustSetModTime(t, path, time.Now().Add(-age))

	return path
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCleaner_EmergencyStopCoversPercentTrigger(t *testing.T) {
	mediaDir := t.TempDir()
	first := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)
	second := snapshotFile(t, mediaDir, "01DEF.jpg", 23*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  96 * testGB,
		FreeBytes:  4 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	probeCalls := 0
	cleaner.capacityProbe = func(string) (*FSStats, error) {
		probeCalls++
		if probeCalls == 1 {
			// free target is already satisfied, but usage is still above high.
			return &FSStats{TotalBytes: 100 * testGB, UsedBytes: 96 * testGB, FreeBytes: 4 * testGB}, nil
		}
		return &FSStats{TotalBytes: 100 * testGB, UsedBytes: 89 * testGB, FreeBytes: 11 * testGB}, nil
	}

	cfg := &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      2048,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, cfg, PruneTriggerEmergency)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.StopReason != PruneStopTargetReached {
		t.Fatalf("stop reason = %q, want %q", summary.StopReason, PruneStopTargetReached)
	}
	if summary.DeletedFiles != 1 {
		t.Fatalf("deleted files = %d, want 1", summary.DeletedFiles)
	}
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Errorf("oldest snapshot should be deleted, err=%v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Errorf("second snapshot should remain until the usage target is reached: %v", err)
	}
	if !containsString(summary.DeletedSnapshotIDs, "01ABC") {
		t.Errorf("deleted IDs = %v, want 01ABC", summary.DeletedSnapshotIDs)
	}
}

func TestCleaner_ManualHealthyDoesNotEvictUnexpired(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  50 * testGB,
		FreeBytes:  50 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 0 {
		t.Fatalf("deleted files = %d, want 0", summary.DeletedFiles)
	}
	if summary.StopReason != PruneStopTargetReached {
		t.Fatalf("stop reason = %q, want target_reached", summary.StopReason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("unexpired snapshot on a healthy volume must survive manual cleanup: %v", err)
	}
}

func TestCleaner_Phase1RunsBelowLowWatermark(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 100*24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  50 * testGB,
		FreeBytes:  50 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 1 {
		t.Fatalf("deleted files = %d, want 1", summary.DeletedFiles)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expired snapshot must be removed even below low watermark, err=%v", err)
	}
}

func TestCleaner_GroupedSnapshotDeletionAndIDs(t *testing.T) {
	mediaDir := t.TempDir()
	main := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)
	crop := snapshotFile(t, mediaDir, "01ABC_crop.jpg", 24*time.Hour)
	face := snapshotFile(t, mediaDir, "01ABC_face.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 3 {
		t.Fatalf("deleted files = %d, want 3", summary.DeletedFiles)
	}
	for _, path := range []string{main, crop, face} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("group member %s should be deleted, err=%v", path, err)
		}
	}
	if !containsString(summary.DeletedSnapshotIDs, "01ABC") {
		t.Errorf("deleted IDs = %v, want 01ABC", summary.DeletedSnapshotIDs)
	}
}

func TestCleaner_LockedGroupIsProtected(t *testing.T) {
	mediaDir := t.TempDir()
	image := snapshotFile(t, mediaDir, "alarm.jpg", 100*24*time.Hour)
	lock := image + ".lock"
	mustWriteFile(t, lock, nil)
	mustSetModTime(t, lock, time.Now().Add(-100*24*time.Hour))

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	for _, path := range []string{image, lock} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("protected evidence %s must survive: %v", path, err)
		}
	}
	if summary.ProtectedFiles != 1 {
		t.Errorf("protected files = %d, want 1", summary.ProtectedFiles)
	}
	if summary.DeletedFiles != 0 {
		t.Errorf("deleted files = %d, want 0", summary.DeletedFiles)
	}
}

func TestCleaner_ActiveGroupIsSkipped(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 0)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	grace := time.Hour
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:       1,
		BatchSleep:      batchSleep(0),
		ActiveFileGrace: &grace,
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 0 {
		t.Fatalf("deleted files = %d, want 0", summary.DeletedFiles)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("active snapshot must survive: %v", err)
	}
}

func TestCleaner_EvidenceGuardFailsClosed(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})
	cleaner.SetEvidenceGuard(func(_ context.Context, ids []string) (map[string]bool, error) {
		protected := make(map[string]bool, len(ids))
		for _, id := range ids {
			protected[id] = true
		}
		return protected, nil
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 0 || summary.ProtectedFiles != 1 {
		t.Fatalf("deleted=%d protected=%d, want 0/1", summary.DeletedFiles, summary.ProtectedFiles)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("guarded snapshot must survive: %v", err)
	}
}

func TestCleaner_PartialGroupFailureKeepsDBID(t *testing.T) {
	mediaDir := t.TempDir()
	main := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)
	crop := snapshotFile(t, mediaDir, "01ABC_crop.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})
	cleaner.fileRemover = func(path string) error {
		if filepath.Base(path) == "01ABC_crop.jpg" {
			return os.ErrPermission
		}
		return os.Remove(path)
	}

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 1 {
		t.Fatalf("deleted files = %d, want 1", summary.DeletedFiles)
	}
	if len(summary.DeletedSnapshotIDs) != 0 {
		t.Fatalf("partial group must not be sent to the DB cascade, got %v", summary.DeletedSnapshotIDs)
	}
	if _, err := os.Stat(main); !os.IsNotExist(err) {
		t.Errorf("main image should be deleted, err=%v", err)
	}
	if _, err := os.Stat(crop); err != nil {
		t.Errorf("crop image should remain, err=%v", err)
	}
}

func TestCleaner_InfeasibleEmergencyReserveDoesNotEvict(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  99 * testGB,
		FreeBytes:  1 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      102400, // 100 GiB reserve on a 100 GiB volume
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerEmergency)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 0 {
		t.Fatalf("deleted files = %d, want 0", summary.DeletedFiles)
	}
	if summary.StopReason != PruneStopCandidatesExhausted {
		t.Fatalf("stop reason = %q, want candidates_exhausted", summary.StopReason)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("unexpired snapshot must survive an infeasible reserve: %v", err)
	}
}

func TestCleaner_EvidenceGuardErrorProtectsBatch(t *testing.T) {
	mediaDir := t.TempDir()
	path := snapshotFile(t, mediaDir, "01ABC.jpg", 24*time.Hour)

	mock := &mockInspector{stats: FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  95 * testGB,
		FreeBytes:  5 * testGB,
	}}
	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})
	cleaner.SetEvidenceGuard(func(context.Context, []string) (map[string]bool, error) {
		return nil, errors.New("alarm database unavailable")
	})

	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		SnapshotsRetentionDays:  90,
		RecordingsRetentionDays: 15,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if summary.DeletedFiles != 0 || summary.ProtectedFiles != 1 {
		t.Fatalf("deleted=%d protected=%d, want 0/1", summary.DeletedFiles, summary.ProtectedFiles)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("guard failure must protect the snapshot: %v", err)
	}
}
