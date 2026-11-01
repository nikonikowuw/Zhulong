package storage

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// batchSleep builds the pointer CleanerOptions uses to tell "not set" from an
// explicit zero.
func batchSleep(d time.Duration) *time.Duration {
	return &d
}

type mockInspector struct {
	mu           sync.Mutex
	stats        FSStats
	rootDev      uint64
	inspectCalls int
}

func (m *mockInspector) InspectFS(path string) (*FSStats, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inspectCalls++
	cpy := m.stats
	return &cpy, nil
}

func (m *mockInspector) ProbeWritable(path string) error {
	return nil
}

func (m *mockInspector) GetRootDeviceID() (uint64, error) {
	return m.rootDev, nil
}

func (m *mockInspector) ScanBreakdown(ctx context.Context, mediaDir string) (StorageUsageBreakdown, error) {
	return StorageUsageBreakdown{}, nil
}

func (m *mockInspector) setUsage(used, total uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stats.TotalBytes = total
	m.stats.UsedBytes = used
	if total >= used {
		m.stats.FreeBytes = total - used
	}
}

func TestCleaner_WatermarkHysteresisSkippedWhenLow(t *testing.T) {
	tempDir := t.TempDir()
	mock := &mockInspector{
		stats: FSStats{
			TotalBytes: 100 * 1024 * 1024 * 1024,
			UsedBytes:  50 * 1024 * 1024 * 1024, // 50%
			FreeBytes:  50 * 1024 * 1024 * 1024,
		},
	}

	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  5,
		BatchSleep: batchSleep(0),
	})

	cfg := &StorageConfig{
		HighWatermarkPercent: 90,
		LowWatermarkPercent:  80,
	}

	// Automatic trigger should skip because usage (50%) < high watermark (90%)
	summary, err := cleaner.RunPrune(context.Background(), tempDir, cfg, PruneTriggerWatermark)
	if err != nil {
		t.Fatalf("unexpected prune error: %v", err)
	}
	if summary.TargetStatus != PruneStatusSkippedLow {
		t.Errorf("expected skipped_below_high_watermark, got %q", summary.TargetStatus)
	}
	if summary.DeletedFiles != 0 {
		t.Errorf("expected 0 deleted files, got %d", summary.DeletedFiles)
	}
}

func TestCleaner_CascadeOrderAndLockedFileProtection(t *testing.T) {
	tempDir := t.TempDir()

	// Create directories
	exportsDir := filepath.Join(tempDir, "exports")
	recordingsDir := filepath.Join(tempDir, "recordings", "cam_01")
	mustMkdirAll(t, exportsDir)
	mustMkdirAll(t, recordingsDir)

	now := time.Now()

	// 1. Expired export file (3 days old)
	expOld := filepath.Join(exportsDir, "export_old.mp4")
	mustWriteFile(t, expOld, []byte("old_export"))
	mustSetModTime(t, expOld, now.Add(-72*time.Hour))

	// 2. Fresh export file (1 hour old)
	expFresh := filepath.Join(exportsDir, "export_fresh.mp4")
	mustWriteFile(t, expFresh, []byte("fresh_export"))
	mustSetModTime(t, expFresh, now.Add(-1*time.Hour))

	// 3. Expired recording (20 days old)
	recOld := filepath.Join(recordingsDir, "rec_old.mp4")
	mustWriteFile(t, recOld, []byte("old_recording"))
	mustSetModTime(t, recOld, now.Add(-20*24*time.Hour))

	// 4. Locked recording (25 days old, but marked with .lock)
	recLocked := filepath.Join(recordingsDir, "evidence.lock")
	mustWriteFile(t, recLocked, []byte("vital_evidence"))
	mustSetModTime(t, recLocked, now.Add(-25*24*time.Hour))

	// 5. Locked recording with _locked in name
	recLocked2 := filepath.Join(recordingsDir, "alarm_event_locked.mp4")
	mustWriteFile(t, recLocked2, []byte("important_alarm"))
	mustSetModTime(t, recLocked2, now.Add(-25*24*time.Hour))

	// 6. Normal recent recording (2 days old)
	recNormal := filepath.Join(recordingsDir, "rec_normal.mp4")
	mustWriteFile(t, recNormal, []byte("normal_recording"))
	mustSetModTime(t, recNormal, now.Add(-48*time.Hour))

	mock := &mockInspector{
		stats: FSStats{
			TotalBytes: 100 * 1024 * 1024 * 1024,
			UsedBytes:  95 * 1024 * 1024 * 1024, // 95% triggers cleaning
			FreeBytes:  5 * 1024 * 1024 * 1024,
		},
	}

	cfg := &StorageConfig{
		HighWatermarkPercent:    90,
		LowWatermarkPercent:     80,
		RecordingsRetentionDays: 15,
		ExportsRetentionHours:   48,
	}

	cleaner := NewCleanerEngine(mock, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})

	// Run manual prune
	summary, err := cleaner.RunPrune(context.Background(), tempDir, cfg, PruneTriggerManual)
	if err != nil {
		t.Fatalf("prune failed: %v", err)
	}

	// Expired export should be deleted
	if _, err := os.Stat(expOld); !os.IsNotExist(err) {
		t.Errorf("expected expOld to be deleted")
	}
	// Fresh export should still exist
	if _, err := os.Stat(expFresh); os.IsNotExist(err) {
		t.Errorf("expected expFresh to be preserved")
	}

	// Old recording should be deleted
	if _, err := os.Stat(recOld); !os.IsNotExist(err) {
		t.Errorf("expected recOld to be deleted")
	}

	// Locked recordings must NEVER be deleted
	if _, err := os.Stat(recLocked); os.IsNotExist(err) {
		t.Errorf("expected recLocked to be preserved from deletion!")
	}
	if _, err := os.Stat(recLocked2); os.IsNotExist(err) {
		t.Errorf("expected recLocked2 to be preserved from deletion!")
	}

	if summary.DeletedFiles < 2 {
		t.Errorf("expected at least 2 deleted files, got %d", summary.DeletedFiles)
	}
}

func TestCleaner_PreservesSnapshotsWithLockSidecars(t *testing.T) {
	mediaDir := t.TempDir()
	snapshotsDir := filepath.Join(mediaDir, "snapshots", "cam_01")
	if err := os.MkdirAll(snapshotsDir, 0o750); err != nil {
		t.Fatal(err)
	}

	locked := filepath.Join(snapshotsDir, "alarm.jpg")
	marker := locked + ".lock"
	expired := filepath.Join(snapshotsDir, "ordinary.jpg")
	for _, path := range []string{locked, marker, expired} {
		if err := os.WriteFile(path, []byte("evidence"), 0o600); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}

	inspector := &mockInspector{stats: FSStats{
		TotalBytes: 100 * 1024 * 1024,
		UsedBytes:  95 * 1024 * 1024,
	}}
	cleaner := NewCleanerEngine(inspector, zap.NewNop(), CleanerOptions{BatchSize: 1, BatchSleep: batchSleep(0)})
	summary, err := cleaner.RunPrune(context.Background(), mediaDir, &StorageConfig{
		LowWatermarkPercent:    80,
		SnapshotsRetentionDays: 1,
	}, PruneTriggerManual)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}

	for _, path := range []string{locked, marker} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("protected evidence %s was removed: %v", path, err)
		}
	}
	if _, err := os.Stat(expired); !os.IsNotExist(err) {
		t.Errorf("expired ordinary snapshot should be removed, err=%v", err)
	}
	if summary.DeletedFiles != 1 {
		t.Errorf("deleted files = %d, want 1", summary.DeletedFiles)
	}
}

func TestCleaner_ConcurrentPrevention(t *testing.T) {
	tempDir := t.TempDir()
	mock := &mockInspector{
		stats: FSStats{
			TotalBytes: 100 * 1024 * 1024,
			UsedBytes:  95 * 1024 * 1024,
		},
	}

	blockCh := make(chan struct{})
	cleaner := NewCleanerEngine(mock, zap.NewNop())
	cleaner.fileRemover = func(path string) error {
		<-blockCh
		return nil
	}

	// Put a file in exports to trigger remover
	expDir := filepath.Join(tempDir, "exports")
	mustMkdirAll(t, expDir)
	fPath := filepath.Join(expDir, "f.mp4")
	mustWriteFile(t, fPath, []byte("data"))
	mustSetModTime(t, fPath, time.Now().Add(-100*time.Hour))

	cfg := &StorageConfig{
		HighWatermarkPercent:  90,
		LowWatermarkPercent:   80,
		ExportsRetentionHours: 24,
	}

	go func() {
		_, _ = cleaner.RunPrune(context.Background(), tempDir, cfg, PruneTriggerManual)
	}()

	time.Sleep(20 * time.Millisecond)

	if !cleaner.IsCleaning() {
		t.Errorf("expected cleaner to be in running state")
	}

	// Second concurrent call should fail with ErrCleanupInProgress
	_, err := cleaner.RunPrune(context.Background(), tempDir, cfg, PruneTriggerManual)
	if err != ErrCleanupInProgress {
		t.Errorf("expected ErrCleanupInProgress, got %v", err)
	}

	close(blockCh)
}

// TestCleanerOptions_BatchSizeOnlyKeepsDefaultThrottle is the regression guard
// for the option trap: tuning BatchSize alone used to reset BatchSleep to zero,
// silently removing the ext4 journal-friendly yield between batches.
func TestCleanerOptions_BatchSizeOnlyKeepsDefaultThrottle(t *testing.T) {
	engine := NewCleanerEngine(&mockInspector{}, zap.NewNop(), CleanerOptions{BatchSize: 7})

	if engine.batchSize != 7 {
		t.Errorf("expected batch size 7, got %d", engine.batchSize)
	}
	if engine.batchSleep != defaultPruneBatchSleep {
		t.Errorf("expected the default throttle %v, got %v", defaultPruneBatchSleep, engine.batchSleep)
	}
}

// TestCleanerOptions_ExplicitZeroDisablesThrottle pins the other half of the
// contract: an explicit zero is still how a caller turns the throttle off.
func TestCleanerOptions_ExplicitZeroDisablesThrottle(t *testing.T) {
	engine := NewCleanerEngine(&mockInspector{}, zap.NewNop(), CleanerOptions{BatchSleep: batchSleep(0)})

	if engine.batchSleep != 0 {
		t.Errorf("expected the throttle to be disabled, got %v", engine.batchSleep)
	}
	if engine.batchSize != defaultPruneBatchSize {
		t.Errorf("expected the default batch size %d, got %d", defaultPruneBatchSize, engine.batchSize)
	}
}
