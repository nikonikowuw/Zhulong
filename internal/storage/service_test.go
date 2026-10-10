package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupTestService(t *testing.T) (*Service, string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	repo := NewRepository(dbStore)
	mediaDir := filepath.Join(tempDir, "media")
	_ = os.MkdirAll(mediaDir, 0o750)

	// Set initial media directory in config
	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	_, err := repo.UpdateConfig(context.Background(), initCfg)
	if err != nil {
		t.Fatalf("init config failed: %v", err)
	}

	inspector := NewDefaultPathInspector()
	gate := NewEmergencyGate(zap.NewNop())
	cleaner := NewCleanerEngine(inspector, zap.NewNop())

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop())

	svc := NewService(repo, inspector, gate, cleaner, zap.NewNop())
	svc.SetAuditor(auditSvc)

	cleanup := func() {
		_ = dbStore.Close()
	}

	return svc, mediaDir, cleanup
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

	// Put an expired export
	expDir := filepath.Join(mediaDir, "exports")
	_ = os.MkdirAll(expDir, 0o750)
	expFile := filepath.Join(expDir, "stale.mp4")
	_ = os.WriteFile(expFile, []byte("data"), 0o600)
	_ = os.Chtimes(expFile, time.Now().Add(-100*time.Hour), time.Now().Add(-100*time.Hour))

	summary, err := svc.TriggerManualCleanup(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("TriggerManualCleanup failed: %v", err)
	}
	if summary.DeletedFiles != 1 {
		t.Errorf("expected 1 deleted file, got %d", summary.DeletedFiles)
	}
}
