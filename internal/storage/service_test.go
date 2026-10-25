package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// newTestRepository opens a migrated throwaway SQLite database and returns both
// the store and a storage repository backed by it.
func newTestRepository(t *testing.T) (*database.Store, Repository, func()) {
	t.Helper()

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	return dbStore, NewRepository(dbStore), func() { _ = dbStore.Close() }
}

// errorInspector is a PathInspector whose every probe fails, standing in for a
// media root that became uninspectable at runtime.
type errorInspector struct {
	err error
}

func (e errorInspector) InspectFS(string) (*FSStats, error) { return nil, e.err }

func (e errorInspector) ProbeWritable(string) error { return e.err }

func (e errorInspector) GetRootDeviceID() (uint64, error) { return 0, e.err }

func (e errorInspector) ScanBreakdown(context.Context, string) (StorageUsageBreakdown, error) {
	return StorageUsageBreakdown{}, e.err
}

// mockRootDevErrorInspector reports healthy filesystem telemetry but cannot
// resolve the root device ID.
type mockRootDevErrorInspector struct {
	*mockInspector
}

func (mockRootDevErrorInspector) GetRootDeviceID() (uint64, error) {
	return 0, errors.New("root device unavailable")
}

func setupTestService(t *testing.T) (*Service, string, func()) {
	t.Helper()

	dbStore, repo, closeDB := newTestRepository(t)
	mediaDir := filepath.Join(t.TempDir(), "media")
	if err := os.MkdirAll(mediaDir, 0o750); err != nil {
		t.Fatalf("create media dir: %v", err)
	}

	// Set initial media directory in config
	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	if _, err := repo.UpdateConfig(context.Background(), initCfg); err != nil {
		t.Fatalf("init config failed: %v", err)
	}

	inspector := NewDefaultPathInspector()
	gate := NewEmergencyGate(zap.NewNop())
	cleaner := NewCleanerEngine(inspector, zap.NewNop())

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop())

	svc := NewService(repo, inspector, gate, cleaner, zap.NewNop())
	svc.SetAuditor(auditSvc)

	return svc, mediaDir, closeDB
}

func TestService_StartStopAndStatus(t *testing.T) {
	svc, mediaDir, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start service failed: %v", err)
	}
	defer func() {
		_ = svc.Stop(ctx)
	}()

	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.MediaDirectory != mediaDir {
		t.Errorf("expected media dir %q, got %q", mediaDir, status.MediaDirectory)
	}
	if !svc.CanWrite() {
		t.Errorf("expected CanWrite to be true initially")
	}

	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.MediaDirectory != mediaDir {
		t.Errorf("expected cfg media dir %q, got %q", mediaDir, cfg.MediaDirectory)
	}
}

func TestService_UpdateConfigAndPathSwitch(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = svc.Stop(ctx)
	}()

	tempDir := t.TempDir()
	newMediaDir := filepath.Join(tempDir, "new_media_mount")

	newCfg := StorageConfig{
		MediaDirectory:          newMediaDir,
		RecordingsRetentionDays: 20,
		SnapshotsRetentionDays:  60,
		ExportsRetentionHours:   24,
		HighWatermarkPercent:    85,
		LowWatermarkPercent:     70,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      1024,
	}

	status, err := svc.UpdateConfig(ctx, newCfg, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}
	if status.MediaDirectory != newMediaDir {
		t.Errorf("expected updated media dir %q, got %q", newMediaDir, status.MediaDirectory)
	}

	// Verify persistence
	cfg, err := svc.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.MediaDirectory != newMediaDir {
		t.Errorf("expected persisted media dir %q, got %q", newMediaDir, cfg.MediaDirectory)
	}
	if cfg.RecordingsRetentionDays != 20 {
		t.Errorf("expected 20 days, got %d", cfg.RecordingsRetentionDays)
	}
}

func TestService_TestPath(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	tempDir := t.TempDir()

	// Writable directory
	res, err := svc.TestPath(ctx, tempDir, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("TestPath failed: %v", err)
	}
	if !res.Writable {
		t.Errorf("expected tempDir to be writable")
	}
	if !res.Exists {
		t.Errorf("expected tempDir to exist")
	}

	// Subfolder that doesn't exist yet should be created and tested
	sub := filepath.Join(tempDir, "brand_new_subfolder")
	resSub, err := svc.TestPath(ctx, sub, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("TestPath sub failed: %v", err)
	}
	if !resSub.Writable {
		t.Errorf("expected subfolder to be writable")
	}
}

func TestService_TriggerManualCleanup(t *testing.T) {
	svc, mediaDir, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		_ = svc.Stop(ctx)
	}()
	pruned := make(chan struct{}, 1)
	svc.SetAfterPrune(func(context.Context) { pruned <- struct{}{} })

	// Put an expired export
	expDir := filepath.Join(mediaDir, "exports")
	if err := os.MkdirAll(expDir, 0o750); err != nil {
		t.Fatalf("create exports dir: %v", err)
	}
	expFile := filepath.Join(expDir, "stale.mp4")
	if err := os.WriteFile(expFile, []byte("data"), 0o600); err != nil {
		t.Fatalf("write expired export: %v", err)
	}
	stale := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(expFile, stale, stale); err != nil {
		t.Fatalf("age expired export: %v", err)
	}

	summary, err := svc.TriggerManualCleanup(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("TriggerManualCleanup failed: %v", err)
	}
	if summary.DeletedFiles != 1 {
		t.Errorf("expected 1 deleted file, got %d", summary.DeletedFiles)
	}
	select {
	case <-pruned:
	case <-time.After(time.Second):
		t.Fatal("after-prune callback was not called")
	}
}

// TestService_RefreshStatusBlocksWritesWhenTelemetryFails pins the contract that
// an uninspectable media root closes the write gate, not just the reported
// status: writers read the gate, not the payload.
func TestService_RefreshStatusBlocksWritesWhenTelemetryFails(t *testing.T) {
	_, repo, closeDB := newTestRepository(t)
	defer closeDB()

	cfg := DefaultStorageConfig()
	cfg.MediaDirectory = t.TempDir()
	if _, err := repo.UpdateConfig(context.Background(), cfg); err != nil {
		t.Fatalf("init config failed: %v", err)
	}

	probeErr := errors.New("mount point vanished")
	gate := NewEmergencyGate(zap.NewNop())
	svc := NewService(repo, errorInspector{err: probeErr}, gate, nil, zap.NewNop())

	status, err := svc.RefreshStatus(context.Background())
	if err == nil {
		t.Fatal("expected RefreshStatus to fail when telemetry is unavailable")
	}
	if !errors.Is(err, probeErr) {
		t.Errorf("expected the probe error to be wrapped, got %v", err)
	}
	if status.Status != StatusError {
		t.Errorf("expected %q, got %q", StatusError, status.Status)
	}
	if status.CanWrite {
		t.Error("expected the reported canWrite to be false")
	}
	if gate.CanWrite() {
		t.Error("expected the write gate to be closed when telemetry is unavailable")
	}
	if !gate.IsEmergency() {
		t.Error("expected the gate to enter the emergency state")
	}
}

// TestService_TestPathFailsWhenRootDeviceUnavailable guards the regression where
// a swallowed GetRootDeviceID error left rootDev at 0 and made every candidate
// path report isExternal=true.
func TestService_TestPathFailsWhenRootDeviceUnavailable(t *testing.T) {
	inspector := mockRootDevErrorInspector{mockInspector: &mockInspector{}}
	svc := NewService(nil, inspector, nil, nil, zap.NewNop())

	res, err := svc.TestPath(context.Background(), t.TempDir(), "admin", "127.0.0.1")
	if err == nil {
		t.Fatalf("expected TestPath to surface the root device error, got %+v", res)
	}
	if res != nil {
		t.Errorf("expected no partial response alongside the error, got %+v", res)
	}
}

func TestService_StopIsIdempotent(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	const callers = 8
	errs := make(chan error, callers)
	for range callers {
		go func() { errs <- svc.Stop(ctx) }()
	}
	for range callers {
		if err := <-errs; err != nil {
			t.Errorf("concurrent Stop failed: %v", err)
		}
	}

	if err := svc.Stop(ctx); err != nil {
		t.Errorf("repeated Stop failed: %v", err)
	}
}

func TestService_StopWithoutSuccessfulStart(t *testing.T) {
	svc := NewService(NewRepository(nil), nil, nil, nil, zap.NewNop())
	if err := svc.Start(context.Background()); err == nil {
		t.Fatal("expected Start to fail without a database provider")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	started := time.Now()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop after a failed Start should return immediately, got %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("Stop blocked for %v after a failed Start", elapsed)
	}
}
