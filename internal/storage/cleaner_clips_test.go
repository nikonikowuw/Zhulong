package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// 事件录像片段回归测试（AC7 / design §3.8）：
//   - recordings/<cam>/<date>/evt-<id>.mp4 + evt-<id>.mp4.lock 在 TTL 与水位两个阶段都跳过；
//   - 同目录的两个片段是两个独立清理单元（文件名不含 '_'，mediaPrefix 不会把它们并组）；
//   - 无锁片段按 TTL / 水位正常删除。

func clipFile(t *testing.T, mediaDir, id string, age time.Duration, locked bool) (clip, lock string) {
	t.Helper()
	dir := filepath.Join(mediaDir, "recordings", "cam-01", "2026-11-04")
	mustMkdirAll(t, dir)
	clip = filepath.Join(dir, "evt-"+id+".mp4")
	mustWriteFile(t, clip, []byte("fmp4"))
	mustSetModTime(t, clip, time.Now().Add(-age))
	if locked {
		lock = clip + lockMarkerSuffix
		mustWriteFile(t, lock, nil)
		mustSetModTime(t, lock, time.Now().Add(-age))
	}
	return clip, lock
}

func TestMediaPrefixSeparatesEventClips(t *testing.T) {
	a := mediaPrefix("evt-01AAA.mp4")
	b := mediaPrefix("evt-01BBB.mp4")
	if a == b {
		t.Fatalf("two clips must not share a cleanup unit: %q", a)
	}
	if got := mediaPrefix("evt-01AAA.mp4.lock"); got != a {
		t.Fatalf("lock sidecar must group with its clip: %q vs %q", got, a)
	}
	if got := mediaPrefix("evt-01AAA.mp4.part"); got != a {
		t.Fatalf(".part file must group with its clip: %q vs %q", got, a)
	}
	// 反例：带下划线的命名会把整天的片段并成一组 —— 这正是禁止 '_' 的原因。
	if mediaPrefix("evt_01AAA.mp4") != mediaPrefix("evt_01BBB.mp4") {
		t.Fatalf("sanity: underscore names are expected to collide")
	}
}

func TestCleaner_EventClipLockSurvivesBothPhases(t *testing.T) {
	tests := []struct {
		name  string
		age   time.Duration
		stats FSStats
	}{
		{
			// Phase 1：片段已超过 RecordingsRetentionDays，磁盘健康。
			name:  "ttl phase",
			age:   20 * 24 * time.Hour,
			stats: FSStats{TotalBytes: 100 * testGB, UsedBytes: 50 * testGB, FreeBytes: 50 * testGB},
		},
		{
			// Phase 2：片段未过期，但水位超限触发容量淘汰。
			name:  "capacity phase",
			age:   24 * time.Hour,
			stats: FSStats{TotalBytes: 100 * testGB, UsedBytes: 95 * testGB, FreeBytes: 5 * testGB},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mediaDir := t.TempDir()
			lockedClip, lock := clipFile(t, mediaDir, "01LOCKED", tt.age, true)
			freeClip, _ := clipFile(t, mediaDir, "01FREE", tt.age, false)

			cleaner := NewCleanerEngine(&mockInspector{stats: tt.stats}, zap.NewNop(), CleanerOptions{
				BatchSize:  1,
				BatchSleep: batchSleep(0),
			})
			summary, err := cleaner.RunPrune(
				context.Background(),
				mediaDir,
				&StorageConfig{
					HighWatermarkPercent:    90,
					LowWatermarkPercent:     80,
					SnapshotsRetentionDays:  90,
					RecordingsRetentionDays: 15,
				},
				PruneTriggerManual,
			)
			if err != nil {
				t.Fatalf("RunPrune: %v", err)
			}
			for _, path := range []string{lockedClip, lock} {
				if _, err := os.Stat(path); err != nil {
					t.Errorf("locked clip %s must survive: %v", path, err)
				}
			}
			if _, err := os.Stat(freeClip); !os.IsNotExist(err) {
				t.Errorf("unlocked clip in the same directory must be deleted, err=%v", err)
			}
			if summary.DeletedFiles != 1 {
				t.Errorf("deleted files = %d, want 1", summary.DeletedFiles)
			}
			if len(summary.DeletedSnapshotIDs) != 0 {
				t.Errorf("recording deletions must not report snapshot IDs: %v", summary.DeletedSnapshotIDs)
			}
		})
	}
}

func TestCleaner_LockedPartFileSurvivesCleanup(t *testing.T) {
	mediaDir := t.TempDir()
	dir := filepath.Join(mediaDir, "recordings", "cam-01", "2026-11-04")
	mustMkdirAll(t, dir)
	age := 20 * 24 * time.Hour

	partPath := filepath.Join(dir, "evt-01PART.mp4.part")
	lockPath := filepath.Join(dir, "evt-01PART.mp4.lock")
	mustWriteFile(t, partPath, []byte("partial-fmp4"))
	mustWriteFile(t, lockPath, nil)
	mustSetModTime(t, partPath, time.Now().Add(-age))
	mustSetModTime(t, lockPath, time.Now().Add(-age))

	cleaner := NewCleanerEngine(&mockInspector{
		stats: FSStats{TotalBytes: 100 * testGB, UsedBytes: 95 * testGB, FreeBytes: 5 * testGB},
	}, zap.NewNop(), CleanerOptions{
		BatchSize:  1,
		BatchSleep: batchSleep(0),
	})
	_, err := cleaner.RunPrune(
		context.Background(),
		mediaDir,
		&StorageConfig{
			HighWatermarkPercent:    90,
			LowWatermarkPercent:     80,
			SnapshotsRetentionDays:  90,
			RecordingsRetentionDays: 15,
		},
		PruneTriggerManual,
	)
	if err != nil {
		t.Fatalf("RunPrune: %v", err)
	}
	if _, err := os.Stat(partPath); err != nil {
		t.Errorf("locked .part file must survive cleanup: %v", err)
	}
}
