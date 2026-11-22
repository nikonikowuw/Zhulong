package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"go.uber.org/zap"
)

type orderRecordingDBStub struct {
	order *[]string
}

func (s orderRecordingDBStub) OpenAndMigrate(context.Context) error { return nil }
func (s orderRecordingDBStub) Close() error {
	*s.order = append(*s.order, "database.close")
	return nil
}

type orderRecordingEngineStub struct {
	order *[]string
}

func (s orderRecordingEngineStub) Start() error { return nil }
func (s orderRecordingEngineStub) Close() error {
	*s.order = append(*s.order, "native.close")
	return nil
}

type orderRecordingStorageStub struct {
	order      *[]string
	synced     *bool
	syncCipher *bool
}

func (s orderRecordingStorageStub) Start(context.Context) error {
	*s.order = append(*s.order, "storage.start")
	return nil
}

func (s orderRecordingStorageStub) Stop(context.Context) error {
	*s.order = append(*s.order, "storage.stop")
	return nil
}

func (s orderRecordingStorageStub) SyncStore(context.Context) error {
	*s.order = append(*s.order, "storage.sync")
	if s.synced != nil {
		*s.synced = true
	}
	return nil
}

type orderRecordingFileStoreStub struct {
	order *[]string
}

func (s orderRecordingFileStoreStub) Close() error {
	*s.order = append(*s.order, "filestore.close")
	return nil
}

type orderRecordingCameraStub struct {
	order *[]string
}

func (s orderRecordingCameraStub) InitCipher(context.Context) error {
	*s.order = append(*s.order, "camera.init_cipher")
	return nil
}

func (s orderRecordingCameraStub) Start(context.Context) error {
	*s.order = append(*s.order, "camera.start")
	return nil
}

func (s orderRecordingCameraStub) Stop() {
	*s.order = append(*s.order, "camera.stop")
}

func TestLifecycleFileStoreStopsAfterStorageAndSyncsAfterInitCipher(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var events []string
	var synced bool
	rt := newLifecycleRuntime(runtimeParams{
		Database:  orderRecordingDBStub{order: &events},
		Native:    orderRecordingEngineStub{order: &events},
		Camera:    orderRecordingCameraStub{order: &events},
		Storage:   orderRecordingStorageStub{order: &events, synced: &synced},
		FileStore: orderRecordingFileStoreStub{order: &events},
		Config:    DefaultConfig(),
		Logger:    zap.NewNop(),
	})

	rt.storageReady = true
	rt.fileStoreReady = true
	rt.cameraReady = true
	rt.nativeReady = true
	rt.databaseReady = true

	if err := rt.Stop(ctx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	wantOrder := []string{"camera.stop", "storage.stop", "filestore.close", "native.close", "database.close"}
	if !slices.Equal(events, wantOrder) {
		t.Fatalf("expected stop sequence %v, got %v", wantOrder, events)
	}

	// Verify rollback includes filestore.close right after storage.stop as well.
	var rollbackEvents []string
	rtRollback := newLifecycleRuntime(runtimeParams{
		Database:  orderRecordingDBStub{order: &rollbackEvents},
		Native:    orderRecordingEngineStub{order: &rollbackEvents},
		Storage:   orderRecordingStorageStub{order: &rollbackEvents},
		FileStore: orderRecordingFileStoreStub{order: &rollbackEvents},
		Config:    DefaultConfig(),
		Logger:    zap.NewNop(),
	})
	rtRollback.storageReady = true
	rtRollback.fileStoreReady = true
	rtRollback.nativeReady = true
	rtRollback.databaseReady = true
	if err := errors.Join(rtRollback.rollback(ctx)...); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	wantRollback := []string{"storage.stop", "filestore.close", "native.close", "database.close"}
	if !slices.Equal(rollbackEvents, wantRollback) {
		t.Fatalf("expected rollback sequence %v, got %v", wantRollback, rollbackEvents)
	}
}

func TestStorageEncryptedDataProbeBlocksCameraKeyRegeneration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})

	camStore := camera.NewCameraStore(dbStore)
	keyMgr := camera.NewKeyManager(dataDir)
	lazyCipher := camera.NewLazyCipher()
	camMgr := camera.NewLifecycleManager(
		camStore,
		keyMgr,
		lazyCipher,
		nil,
		nil,
		nil,
	)

	storageComps := storage.NewStorageComponentsWithCipher(
		dbStore,
		lazyCipher,
		nil,
		zap.NewNop(),
	)
	camMgr.AddEncryptedDataProbe(storageComps.Service)

	if err := camMgr.InitCipher(ctx); err != nil {
		t.Fatalf("initial InitCipher failed: %v", err)
	}

	cfg := storage.DefaultStorageConfig()
	cfg.Backend = storage.BackendS3
	cfg.SpoolDirectory = filepath.Join(dataDir, "spool")
	cfg.S3 = &storage.S3Config{
		Endpoint:        "http://127.0.0.1:9000",
		Bucket:          "prod-media",
		AccessKeyID:     "AKIA",
		SecretAccessKey: "very-secret-key",
	}
	if _, err := storageComps.Repository.UpdateConfig(ctx, cfg); err != nil {
		t.Fatalf("persist encrypted s3 config: %v", err)
	}

	// Simulate operator/host reboot with camera.key removed while encrypted S3 secret exists.
	keyPath := filepath.Join(dataDir, "camera.key")
	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove camera.key: %v", err)
	}

	lazyCipher2 := camera.NewLazyCipher()
	camMgr2 := camera.NewLifecycleManager(
		camStore,
		camera.NewKeyManager(dataDir),
		lazyCipher2,
		nil,
		nil,
		nil,
	)
	storageComps2 := storage.NewStorageComponentsWithCipher(
		dbStore,
		lazyCipher2,
		nil,
		zap.NewNop(),
	)
	camMgr2.AddEncryptedDataProbe(storageComps2.Service)

	if err := camMgr2.InitCipher(ctx); err == nil {
		t.Fatalf("expected InitCipher to fail when camera.key is missing and encrypted S3 credentials exist")
	}
}

func TestAppRemotePruneProviderAndReconciler(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close test db: %v", err)
		}
	})

	db := dbStore.DB()
	now := time.Now().UTC()
	oldTime := now.Add(-20 * 24 * time.Hour)
	freshTime := now.Add(-2 * time.Hour)

	exec := func(sql string, args ...any) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(sql, args...).Error; err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	exec(`INSERT INTO cameras (id, name, enabled) VALUES ('cam-1', 'Cam1', 1)`)
	exec(
		`INSERT INTO recognitions (id, camera_id, pts, image_path, face_image_path, gallery_image_path, captured_at, storage_backend)
		 VALUES (?, 'cam-1', 1, 'snapshots/old/r1.jpg', 'snapshots/old/r1_face.jpg', 'faces/p1.jpg', ?, 's3')`,
		"rec-old", oldTime,
	)
	exec(
		`INSERT INTO captures (id, camera_id, target_type, image_path, crop_path, pts, captured_at, storage_backend)
		 VALUES (?, 'cam-1', 'person', 'snapshots/old/c1.jpg', '', 2, ?, 's3')`,
		"cap-old", oldTime,
	)
	exec(
		`INSERT INTO event_clips (id, camera_id, path, status, started_at, size_bytes, storage_backend, upload_status)
		 VALUES (?, 'cam-1', 'recordings/old/clip1.mp4', 'completed', ?, 2048, 's3', 'uploaded')`,
		"clip-old", oldTime,
	)
	exec(
		`INSERT INTO event_clips (id, camera_id, path, status, started_at, size_bytes, storage_backend, upload_status)
		 VALUES (?, 'cam-1', 'recordings/old/clip-alarm.mp4', 'completed', ?, 2048, 's3', 'uploaded')`,
		"clip-alarm", oldTime,
	)
	exec(
		`INSERT INTO event_clips (id, camera_id, path, status, started_at, size_bytes, storage_backend, upload_status)
		 VALUES (?, 'cam-1', 'recordings/fresh/clip2.mp4', 'completed', ?, 4096, 's3', 'uploaded')`,
		"clip-fresh", freshTime,
	)
	exec(
		`INSERT INTO alarms (id, camera_id, alarm_type, pts, image_path, captured_at, clip_id, storage_backend)
		 VALUES ('alm-1', 'cam-1', 'intrusion', 10, 'snapshots/old/a1.jpg', ?, 'clip-alarm', 's3')`,
		oldTime,
	)

	cfg := storage.DefaultStorageConfig()
	cfg.Backend = storage.BackendS3
	cfg.RecordingsRetentionDays = 7
	cfg.SnapshotsRetentionDays = 7

	provider := &appRemotePruneProvider{dbProvider: dbStore}
	plan, err := provider.BuildRemotePrunePlan(ctx, &cfg, now)
	if err != nil {
		t.Fatalf("BuildRemotePrunePlan failed: %v", err)
	}
	if len(plan.Expired) != 3 {
		t.Fatalf("expected 3 expired units (rec-old, cap-old, clip-old; clip-alarm & clip-fresh excluded), got %+v",
			plan.Expired)
	}

	// Ensure gallery_image_path (faces/p1.jpg) was NOT included in rec-old keys.
	for _, k := range plan.Expired[0].Keys {
		if k == "faces/p1.jpg" {
			t.Fatalf("gallery_image_path must never be pruned with snapshots")
		}
	}

	reconcile := reconcileRemoteDeletedClips(dbStore, zap.NewNop())
	if err := reconcile(ctx, []string{"clip-old"}); err != nil {
		t.Fatalf("reconcileRemoteDeletedClips failed: %v", err)
	}
	var count int64
	if err := db.WithContext(ctx).Table("event_clips").Where("id = 'clip-old'").Count(&count).Error; err != nil {
		t.Fatalf("count clip-old: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected clip-old row removed, count=%d", count)
	}
}

func TestSQLiteStorageMigrationSource(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close db: %v", err)
		}
	}()

	db := dbStore.DB()
	now := time.Now().UTC()
	if err := db.Exec(`INSERT INTO cameras (id, name, enabled) VALUES ('cam-mig', 'Cam Mig', 1)`).Error; err != nil {
		t.Fatalf("insert camera: %v", err)
	}
	if err := db.Exec(`INSERT INTO recognitions (id, camera_id, pts, image_path, face_image_path, captured_at, created_at, storage_backend)
		VALUES ('rec-1', 'cam-mig', 1, 'snapshots/cam-mig/2026-11-22/rec.jpg', 'snapshots/cam-mig/2026-11-22/face.jpg', ?, ?, 'local')`,
		now, now).Error; err != nil {
		t.Fatalf("insert recognition: %v", err)
	}
	if err := db.Exec(`INSERT INTO captures (id, camera_id, target_type, pts, image_path, crop_path, captured_at, created_at, storage_backend)
		VALUES ('cap-1', 'cam-mig', 'person', 1, 'snapshots/cam-mig/2026-11-22/cap.jpg', '', ?, ?, 'local')`,
		now, now).Error; err != nil {
		t.Fatalf("insert capture: %v", err)
	}
	if err := db.Exec(`INSERT INTO alarms (id, camera_id, alarm_type, pts, image_path, crop_path, captured_at, created_at, storage_backend)
		VALUES ('alm-1', 'cam-mig', 'intrusion', 1, 'snapshots/cam-mig/2026-11-22/alm.jpg', '', ?, ?, 'local')`,
		now, now).Error; err != nil {
		t.Fatalf("insert alarm: %v", err)
	}
	if err := db.Exec(`INSERT INTO event_clips (id, camera_id, path, codec, status, partial, started_at, duration_ms, size_bytes, created_at, storage_backend, upload_status)
		VALUES ('clip-1', 'cam-mig', 'recordings/cam-mig/2026-11-22/evt-1.mp4', 'h264', 'completed', 0, ?, 1000, 100, ?, 'local', 'local')`,
		now, now).Error; err != nil {
		t.Fatalf("insert event_clip: %v", err)
	}
	if err := db.Exec(`INSERT INTO persons (id, name, source, created_at, updated_at)
		VALUES ('p-1', 'Alice', 'manual', ?, ?)`, now, now).Error; err != nil {
		t.Fatalf("insert person: %v", err)
	}
	if err := db.Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at, storage_backend)
		VALUES ('pf-1', 'p-1', 'alice.jpg', X'0102', ?, 'local')`, now).Error; err != nil {
		t.Fatalf("insert person_face: %v", err)
	}
	if err := db.Exec(`INSERT INTO canvas_maps (id, name, image_path, width, height, created_at, updated_at, storage_backend)
		VALUES ('map-1', 'Floor 1', 'map-1.svg', 100, 100, ?, ?, 'local')`, now, now).Error; err != nil {
		t.Fatalf("insert canvas_map: %v", err)
	}

	src := newSQLiteStorageMigrationSource(dbStore, map[string]string{
		filestore.NamespaceFaces: filepath.Join(tempDir, "faces"),
		filestore.NamespaceMaps:  filepath.Join(tempDir, "maps"),
	})
	assets, err := src.ListLocalAssets(ctx)
	if err != nil {
		t.Fatalf("ListLocalAssets failed: %v", err)
	}
	if len(assets) != 6 {
		t.Fatalf("expected 6 local assets across all tables, got %d: %+v", len(assets), assets)
	}
	for _, asset := range assets {
		if err := src.MarkAssetMigrated(ctx, asset); err != nil {
			t.Fatalf(
				"MarkAssetMigrated(%s/%s): %v",
				asset.Table,
				asset.ID,
				err,
			)
		}
	}

	remaining, err := src.ListLocalAssets(ctx)
	if err != nil {
		t.Fatalf("ListLocalAssets after mark failed: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("expected 0 remaining local assets, got %d", len(remaining))
	}

	var uploadStatus string
	if err := db.Table("event_clips").Select("upload_status").Where("id = 'clip-1'").Scan(&uploadStatus).Error; err != nil {
		t.Fatalf("query event_clips upload_status: %v", err)
	}
	if uploadStatus != "uploaded" {
		t.Fatalf("expected clip-1 upload_status='uploaded', got %q", uploadStatus)
	}
}
