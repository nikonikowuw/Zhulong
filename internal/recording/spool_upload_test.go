package recording_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/recording"
	"go.uber.org/zap"
)

type spoolTargetProvider struct {
	target recording.StorageTarget
}

func (p spoolTargetProvider) RecordingTarget(context.Context) (recording.StorageTarget, error) {
	return p.target, nil
}

type failingRemoteStore struct {
	filestore.Store
	mu     sync.RWMutex
	putErr error
}

func (s *failingRemoteStore) Backend() string { return filestore.BackendS3 }

func (s *failingRemoteStore) Put(
	ctx context.Context,
	key string,
	r io.Reader,
	opts filestore.PutOptions,
) error {
	s.mu.RLock()
	err := s.putErr
	s.mu.RUnlock()
	if err != nil {
		return err
	}
	return s.Store.Put(
		ctx,
		key,
		r,
		opts,
	)
}

func openSpoolTestStore(t *testing.T) *recording.Store {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})
	if err := dbStore.DB().Exec(insertCameraSQL, "cam-spool", "SpoolCam").Error; err != nil {
		t.Fatalf("seed camera: %v", err)
	}
	if err := dbStore.DB().Exec(`
		INSERT INTO alarms (id, camera_id, alarm_type, score, pts, captured_at, created_at)
		VALUES ('alm-spool', 'cam-spool', 'intrusion', 0.9, 1000, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("seed alarm: %v", err)
	}
	return recording.NewStore(dbStore)
}

func TestRecording_SpoolTriggerAsyncUploadAndDrainOnStop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openSpoolTestStore(t)

	mediaDir := t.TempDir()
	spoolDir := t.TempDir()
	hub := newFakeHub()

	memPrimary := filestore.NewMemoryDriver()
	mgr := filestore.NewManager(filestore.ManagerConfig{
		Local:  filestore.LocalConfig{RootDir: mediaDir},
		Logger: zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close filestore manager: %v", err)
		}
	}()

	remoteWrapper := &failingRemoteStore{Store: memPrimary}
	if err := mgr.Swap(remoteWrapper, nil); err != nil {
		t.Fatalf("swap remote driver: %v", err)
	}

	svc := recording.NewService(
		store,
		hub,
		nil,
		nil,
		zap.NewNop(),
		recording.Options{
			PollInterval:        10 * time.Millisecond,
			CleanupInterval:     time.Hour,
			CleanupInitialDelay: time.Hour,
			UploadWorkers:       1,
			UploadQueueSize:     8,
		},
	)
	svc.SetFileStore(mgr)
	svc.SetTargetProvider(spoolTargetProvider{
		target: recording.StorageTarget{
			Backend:  filestore.BackendS3,
			MediaDir: mediaDir,
			SpoolDir: spoolDir,
		},
	})

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	clipID, _, err := svc.Trigger(ctx, recording.TriggerRequest{
		CameraID:  "cam-spool",
		EventKind: recording.EventKindAlarm,
		EventID:   "alm-spool",
		CapturedAt: time.Date(
			2026,
			11,
			22,
			8,
			0,
			0,
			0,
			time.UTC,
		),
		Policy: recording.ClipPolicy{PostSeconds: 5},
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}

	// Verify C++ hub received a path inside SpoolDir and NO ".lock" was created there.
	if len(hub.requests) != 1 || !strings.HasPrefix(hub.requests[0].Path, spoolDir) {
		t.Fatalf("expected trigger path under SpoolDir %q, got %+v", spoolDir, hub.requests)
	}
	absPath := hub.requests[0].Path
	if _, statErr := os.Stat(absPath + ".lock"); !os.IsNotExist(statErr) {
		t.Fatalf("expected no .lock sidecar in spool dir, got %v", statErr)
	}

	// Simulate C++ Fmp4Writer finishing clip file into SpoolDir.
	clipBytes := []byte("ftypisom-mock-fmp4-payload")
	if err := os.WriteFile(absPath, clipBytes, 0o640); err != nil {
		t.Fatalf("write spool clip: %v", err)
	}

	rowBefore, err := store.GetClip(ctx, clipID)
	if err != nil {
		t.Fatalf("GetClip before upload: %v", err)
	}
	if rowBefore.StorageBackend != filestore.BackendS3 || rowBefore.UploadStatus != recording.UploadStatusPending {
		t.Fatalf("expected pending s3 row before upload, got %+v", rowBefore)
	}

	// Before upload completes, SpoolDir copy allows reading via filestore.LocalDriver directly.
	spoolReader := filestore.NewLocalDriver(filestore.LocalConfig{RootDir: spoolDir})
	if ok, exErr := spoolReader.Exists(ctx, filepath.ToSlash(rowBefore.Path)); exErr != nil || !ok {
		t.Fatalf("expected clip readable in spool before upload, ok=%v err=%v", ok, exErr)
	}
	if err := spoolReader.Close(); err != nil {
		t.Fatalf("close spool reader: %v", err)
	}

	// Emit completion event, then Stop to drain upload queue deterministically.
	hub.mu.Lock()
	hub.events = append(hub.events, recording.ClipEvent{
		Seq:        1,
		Status:     recording.ClipStatusCompleted,
		Codec:      "h264",
		DurationMs: 4500,
		SizeBytes:  int64(len(clipBytes)),
	})
	hub.mu.Unlock()

	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop drain failed: %v", err)
	}

	rowAfter, err := store.GetClip(ctx, clipID)
	if err != nil {
		t.Fatalf("GetClip after Stop: %v", err)
	}
	if rowAfter.Status != recording.ClipStatusCompleted || rowAfter.UploadStatus != recording.UploadStatusUploaded {
		t.Fatalf("expected completed+uploaded row after drain, got %+v", rowAfter)
	}
	if _, statErr := os.Stat(absPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected spool file deleted after upload, statErr=%v", statErr)
	}

	rc, _, err := mgr.Open(ctx, filepath.ToSlash(rowAfter.Path))
	if err != nil {
		t.Fatalf("Open uploaded clip from filestore failed: %v", err)
	}
	gotBytes, readErr := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close reader: %v", closeErr)
	}
	if readErr != nil || !bytes.Equal(gotBytes, clipBytes) {
		t.Fatalf("uploaded bytes mismatch: got=%q err=%v", gotBytes, readErr)
	}
}

func TestRecording_RestartRecoveryAndFailedRetry(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := openSpoolTestStore(t)

	mediaDir := t.TempDir()
	spoolDir := t.TempDir()

	// 1. Interrupted "recording" clip with a ".part" file left in SpoolDir.
	partRel := "recordings/cam-spool/2026-11-22/evt-orphan.mp4"
	partAbs := filepath.Join(spoolDir, filepath.FromSlash(partRel)+".part")
	if err := os.MkdirAll(filepath.Dir(partAbs), 0o750); err != nil {
		t.Fatalf("mkdir spool: %v", err)
	}
	partBytes := []byte("partial-fmp4-recovered")
	if err := os.WriteFile(partAbs, partBytes, 0o640); err != nil {
		t.Fatalf("write part file: %v", err)
	}
	if err := store.CreateClip(ctx, &recording.EventClip{
		ID:             "orphan-1",
		CameraID:       "cam-spool",
		Path:           partRel,
		Codec:          "h264",
		Status:         recording.ClipStatusRecording,
		StorageBackend: filestore.BackendS3,
		UploadStatus:   recording.UploadStatusPending,
		StartedAt:      time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("seed orphan clip: %v", err)
	}

	// 2. Previously failed upload still residing in SpoolDir.
	failedRel := "recordings/cam-spool/2026-11-22/evt-failed.mp4"
	failedAbs := filepath.Join(spoolDir, filepath.FromSlash(failedRel))
	failedBytes := []byte("retry-fmp4-bytes")
	if err := os.WriteFile(failedAbs, failedBytes, 0o640); err != nil {
		t.Fatalf("write failed spool file: %v", err)
	}
	if err := store.CreateClip(ctx, &recording.EventClip{
		ID:             "failed-1",
		CameraID:       "cam-spool",
		Path:           failedRel,
		Codec:          "h264",
		Status:         recording.ClipStatusCompleted,
		StorageBackend: filestore.BackendS3,
		UploadStatus:   recording.UploadStatusFailed,
		SizeBytes:      int64(len(failedBytes)),
		StartedAt:      time.Now().UTC().Add(-2 * time.Minute),
	}); err != nil {
		t.Fatalf("seed failed clip: %v", err)
	}

	remoteStore := &failingRemoteStore{Store: filestore.NewMemoryDriver()}

	// First fail uploads cleanly and confirm CleanupOnce does NOT delete row while in Spool.
	remoteStore.mu.Lock()
	remoteStore.putErr = errors.New("s3 temporarily unreachable")
	remoteStore.mu.Unlock()

	svc := recording.NewService(
		store,
		nil,
		nil,
		nil,
		zap.NewNop(),
		recording.Options{
			CleanupInterval:     time.Hour,
			CleanupInitialDelay: time.Hour,
			UploadWorkers:       1,
			UploadQueueSize:     8,
		},
	)
	svc.SetFileStore(remoteStore)
	svc.SetTargetProvider(spoolTargetProvider{
		target: recording.StorageTarget{
			Backend:  filestore.BackendS3,
			MediaDir: mediaDir,
			SpoolDir: spoolDir,
		},
	})

	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Wait briefly for initial failed upload attempt, then run CleanupOnce while S3 is down.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r1, err := store.GetClip(ctx, "orphan-1")
		if err == nil && r1.Status == recording.ClipStatusCompleted && r1.UploadStatus == recording.UploadStatusFailed {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if err := svc.CleanupOnce(ctx); err != nil {
		t.Fatalf("CleanupOnce during outage: %v", err)
	}
	if row, err := store.GetClip(ctx, "orphan-1"); err != nil || row == nil {
		t.Fatalf("expected orphan-1 preserved while spool copy exists, err=%v", err)
	}

	// Restore S3 and trigger retry via CleanupOnce, then Stop to drain queue.
	remoteStore.mu.Lock()
	remoteStore.putErr = nil
	remoteStore.mu.Unlock()

	if err := svc.CleanupOnce(ctx); err != nil {
		t.Fatalf("CleanupOnce retry: %v", err)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	for _, id := range []string{"orphan-1", "failed-1"} {
		row, err := store.GetClip(ctx, id)
		if err != nil {
			t.Fatalf("GetClip %s: %v", id, err)
		}
		if row.UploadStatus != recording.UploadStatusUploaded {
			t.Fatalf("expected %s uploaded after recovery, got %+v", id, row)
		}
		if exists, exErr := remoteStore.Exists(ctx, filepath.ToSlash(row.Path)); exErr != nil || !exists {
			t.Fatalf(
				"expected %s in remote store, exists=%v err=%v",
				id,
				exists,
				exErr,
			)
		}
	}
}
