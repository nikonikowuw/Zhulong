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
		BatchSleep: 0,
	})

	cfg := &StorageConfig{
		HighWatermarkPercent: 90,
		LowWatermarkPercent:  80,
	}

	// Automatic trigger should skip because usage (50%) < high watermark (90%)
	summary, err := cleaner.RunPrune(context.Background(), tempDir, cfg, "watermark")
	if err != nil {
		t.Fatalf("unexpected prune error: %v", err)
	}
	if summary.TargetStatus != "skipped_below_high_watermark" {
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
	_ = os.MkdirAll(exportsDir, 0o750)
	_ = os.MkdirAll(recordingsDir, 0o750)

	now := time.Now()

	// 1. Expired export file (3 days old)
	expOld := filepath.Join(exportsDir, "export_old.mp4")
	_ = os.WriteFile(expOld, []byte("old_export"), 0o600)
	_ = os.Chtimes(expOld, now.Add(-72*time.Hour), now.Add(-72*time.Hour))

	// 2. Fresh export file (1 hour old)
	expFresh := filepath.Join(exportsDir, "export_fresh.mp4")
	_ = os.WriteFile(expFresh, []byte("fresh_export"), 0o600)
	_ = os.Chtimes(expFresh, now.Add(-1*time.Hour), now.Add(-1*time.Hour))

	// 3. Expired recording (20 days old)
	recOld := filepath.Join(recordingsDir, "rec_old.mp4")
	_ = os.WriteFile(recOld, []byte("old_recording"), 0o600)
	_ = os.Chtimes(recOld, now.Add(-20*24*time.Hour), now.Add(-20*24*time.Hour))

	// 4. Locked recording (25 days old, but marked with .lock)
	recLocked := filepath.Join(recordingsDir, "evidence.lock")
	_ = os.WriteFile(recLocked, []byte("vital_evidence"), 0o600)
	_ = os.Chtimes(recLocked, now.Add(-25*24*time.Hour), now.Add(-25*24*time.Hour))

	// 5. Locked recording with _locked in name
	recLocked2 := filepath.Join(recordingsDir, "alarm_event_locked.mp4")
	_ = os.WriteFile(recLocked2, []byte("important_alarm"), 0o600)
	_ = os.Chtimes(recLocked2, now.Add(-25*24*time.Hour), now.Add(-25*24*time.Hour))

	// 6. Normal recent recording (2 days old)
	recNormal := filepath.Join(recordingsDir, "rec_normal.mp4")
	_ = os.WriteFile(recNormal, []byte("normal_recording"), 0o600)
	_ = os.Chtimes(recNormal, now.Add(-48*time.Hour), now.Add(-48*time.Hour))

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
		BatchSleep: 0,
	})

	// Run manual prune
	summary, err := cleaner.RunPrune(context.Background(), tempDir, cfg, "manual")
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
	_ = os.MkdirAll(expDir, 0o750)
	fPath := filepath.Join(expDir, "f.mp4")
	_ = os.WriteFile(fPath, []byte("data"), 0o600)
	_ = os.Chtimes(fPath, time.Now().Add(-100*time.Hour), time.Now().Add(-100*time.Hour))

	cfg := &StorageConfig{
		HighWatermarkPercent:  90,
		LowWatermarkPercent:   80,
		ExportsRetentionHours: 24,
	}

	go func() {
		_, _ = cleaner.RunPrune(context.Background(), tempDir, cfg, "manual")
	}()

	time.Sleep(20 * time.Millisecond)

	if !cleaner.IsCleaning() {
		t.Errorf("expected cleaner to be in running state")
	}

	// Second concurrent call should fail with ErrCleanupInProgress
	_, err := cleaner.RunPrune(context.Background(), tempDir, cfg, "manual")
	if err != ErrCleanupInProgress {
		t.Errorf("expected ErrCleanupInProgress, got %v", err)
	}

	close(blockCh)
}
