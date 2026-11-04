package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap"
)

// emergencyThenHealthyInspector reports a nearly full volume while target
// exists, and a healthy volume once the cleanup removed it. This models the
// real gate/cleaner feedback loop without sleeping for a ticker.
type emergencyThenHealthyInspector struct {
	target string
}

func (e emergencyThenHealthyInspector) InspectFS(string) (*FSStats, error) {
	if _, err := os.Stat(e.target); os.IsNotExist(err) {
		return &FSStats{
			TotalBytes: 100 * testGB,
			UsedBytes:  50 * testGB,
			FreeBytes:  50 * testGB,
			DeviceID:   1,
			MountPoint: "/",
		}, nil
	}
	return &FSStats{
		TotalBytes: 100 * testGB,
		UsedBytes:  99 * testGB,
		FreeBytes:  1 * testGB,
		DeviceID:   1,
		MountPoint: "/",
	}, nil
}

func (e emergencyThenHealthyInspector) ProbeWritable(string) error { return nil }

func (e emergencyThenHealthyInspector) GetRootDeviceID() (uint64, error) { return 1, nil }

func (e emergencyThenHealthyInspector) ScanBreakdown(context.Context, string) (StorageUsageBreakdown, error) {
	return StorageUsageBreakdown{}, nil
}

func TestService_TickEmergencyCleanupAndResume(t *testing.T) {
	dbStore, repo, closeDB := newTestRepository(t)
	defer closeDB()

	mediaDir := t.TempDir()
	target := filepath.Join(mediaDir, "snapshots", "cam-01", "2026-11-04", "01ABC.jpg")
	mustMkdirAll(t, filepath.Dir(target))
	mustWriteFile(t, target, []byte("evidence"))
	mustSetModTime(t, target, time.Now().Add(-48*time.Hour))

	cfg := DefaultStorageConfig()
	cfg.MediaDirectory = mediaDir
	cfg.SnapshotsRetentionDays = 1
	if _, err := repo.UpdateConfig(context.Background(), cfg); err != nil {
		t.Fatalf("init config: %v", err)
	}

	inspector := emergencyThenHealthyInspector{target: target}
	gate := NewEmergencyGate(zap.NewNop())
	cleaner := NewCleanerEngine(inspector, zap.NewNop(), CleanerOptions{
		BatchSize:       1,
		BatchSleep:      batchSleep(0),
		ActiveFileGrace: batchSleep(0),
		CapacityProbe:   inspector.InspectFS,
	})
	svc := NewService(ServiceDeps{
		Repo:      repo,
		Inspector: inspector,
		Gate:      gate,
		Cleaner:   cleaner,
		Logger:    zap.NewNop(),
	})
	_ = dbStore
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = svc.Stop(ctx)
	}()

	pruned := make(chan []string, 1)
	svc.SetAfterPrune(func(_ context.Context, ids []string) {
		pruned <- ids
	})

	svc.tick()

	select {
	case ids := <-pruned:
		if !containsString(ids, "01ABC") {
			t.Fatalf("after-prune IDs = %v, want 01ABC", ids)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("emergency cleanup did not invoke the after-prune callback")
	}

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expired snapshot should be deleted, err=%v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for gate.IsEmergency() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if gate.IsEmergency() {
		t.Fatal("gate did not resume after emergency cleanup reached the safe target")
	}
	if !gate.CanWrite() {
		t.Fatal("gate should allow writes after resume")
	}
}
