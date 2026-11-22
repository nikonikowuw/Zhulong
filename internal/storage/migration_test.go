package storage

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

type stubMigrationSource struct {
	mu       sync.Mutex
	nsRoots  map[string]string
	assets   []MigrationAsset
	migrated []string
}

func (s *stubMigrationSource) NamespaceRoots() map[string]string {
	out := make(map[string]string, len(s.nsRoots))
	for k, v := range s.nsRoots {
		out[k] = v
	}
	return out
}

func (s *stubMigrationSource) ListLocalAssets(_ context.Context) ([]MigrationAsset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]MigrationAsset, len(s.assets))
	copy(out, s.assets)
	return out, nil
}

func (s *stubMigrationSource) MarkAssetMigrated(_ context.Context, asset MigrationAsset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.migrated = append(s.migrated, asset.Table+":"+asset.ID)
	return nil
}

func TestMigrationJobExecution_CopiesIdempotentlyAndDeletesOptional(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	mediaDir := filepath.Join(tempDir, "media")
	spoolDir := filepath.Join(tempDir, "spool")
	facesDir := filepath.Join(tempDir, "faces")
	mapsDir := filepath.Join(tempDir, "maps")
	for _, dir := range []string{mediaDir, spoolDir, facesDir, mapsDir} {
		mustMkdirAll(t, dir)
	}

	writeTestFile := func(path string, content []byte) {
		t.Helper()
		mustMkdirAll(t, filepath.Dir(path))
		if err := os.WriteFile(path, content, 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	writeTestFile(filepath.Join(mediaDir, "snapshots/cam-1/2026-11-22/snap-1.jpg"), []byte("snap-img"))
	writeTestFile(filepath.Join(mediaDir, "snapshots/cam-1/2026-11-22/face-1.jpg"), []byte("face-crop"))
	writeTestFile(filepath.Join(spoolDir, "recordings/cam-1/2026-11-22/evt-1.mp4"), []byte("mp4-data"))
	writeTestFile(filepath.Join(facesDir, "alice.jpg"), []byte("alice-face"))
	writeTestFile(filepath.Join(mapsDir, "floor1.svg"), []byte("<svg/>"))

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close db: %v", err)
		}
	}()

	cipher := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipher, zap.NewNop())
	cfg := DefaultStorageConfig()
	cfg.Backend = BackendS3
	cfg.MediaDirectory = mediaDir
	cfg.SpoolDirectory = spoolDir
	cfg.EmergencyStopMinMB = 100
	cfg.S3 = &S3Config{
		Endpoint:        "http://minio.local:9000",
		Bucket:          "zhulong",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "miniosecret",
	}
	if _, err := repo.UpdateConfig(ctx, cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	remote := filestore.NewMemoryDriver()
	// Pre-populate alice.jpg in remote primary so idempotent skip leaves it intact even without re-reading.
	seedRemoteErr := remote.Put(
		ctx,
		"faces/alice.jpg",
		bytes.NewReader([]byte("alice-face")),
		filestore.PutOptions{Size: 10},
	)
	if seedRemoteErr != nil {
		t.Fatalf("seed remote: %v", seedRemoteErr)
	}
	mgr := filestore.NewManager(filestore.ManagerConfig{
		Local:         filestore.LocalConfig{RootDir: mediaDir},
		LocalFallback: true,
	})
	mgr.Swap(remote, nil)

	source := &stubMigrationSource{
		nsRoots: map[string]string{
			filestore.NamespaceFaces: facesDir,
			filestore.NamespaceMaps:  mapsDir,
		},
		assets: []MigrationAsset{
			{
				Table: "recognitions",
				ID:    "rec-1",
				Keys: []string{
					"snapshots/cam-1/2026-11-22/snap-1.jpg",
					"snapshots/cam-1/2026-11-22/face-1.jpg",
				},
			},
			{
				Table:  "event_clips",
				ID:     "clip-1",
				Keys:   []string{"recordings/cam-1/2026-11-22/evt-1.mp4"},
				IsClip: true,
			},
			{
				Table: "person_faces",
				ID:    "face-alice",
				Keys:  []string{"faces/alice.jpg"},
			},
			{
				Table: "canvas_maps",
				ID:    "map-1",
				Keys:  []string{"maps/floor1.svg"},
			},
			{
				Table: "captures",
				ID:    "cap-missing",
				Keys:  []string{"snapshots/cam-1/2026-11-22/missing.jpg"},
			},
		},
	}

	svc := NewService(ServiceDeps{
		Repo:      repo,
		Inspector: NewDefaultPathInspector(),
		Gate:      NewEmergencyGate(zap.NewNop()),
		Cleaner:   NewCleanerEngine(NewDefaultPathInspector(), zap.NewNop()),
		Cipher:    cipher,
		Logger:    zap.NewNop(),
	})
	asyncComp := asyncjob.NewAsyncJobComponents(dbStore, zap.NewNop())
	svc.SetAsyncJobs(asyncComp.Service)
	svc.SetMigrationSource(source)
	svc.SetRemoteStore(mgr)
	asyncComp.Service.RegisterHandler(NewMigrationJobHandler(svc))

	if err := asyncComp.Service.Start(ctx); err != nil {
		t.Fatalf("start asyncjob: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := asyncComp.Service.Stop(stopCtx); err != nil {
			t.Logf("stop asyncjob: %v", err)
		}
	}()

	job, err := svc.StartMigration(
		ctx,
		StartMigrationRequest{DeleteLocal: false},
		"admin",
		"127.0.0.1",
	)
	if err != nil {
		t.Fatalf("StartMigration: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var done *asyncjob.Job
	for time.Now().Before(deadline) {
		cur, err := asyncComp.Service.GetJob(ctx, job.ID)
		if err == nil && cur.Status.IsTerminal() {
			done = cur
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if done == nil {
		t.Fatalf("timed out waiting for migration job %s", job.ID)
	}
	if done.Status != asyncjob.StatusCompleted || done.Succeeded != 4 || done.Failed != 1 {
		t.Fatalf(
			"unexpected migration result: status=%s total=%d succeeded=%d failed=%d err=%s",
			done.Status,
			done.Total,
			done.Succeeded,
			done.Failed,
			done.ErrorMessage,
		)
	}

	// Verify objects copied to remote store.
	for _, key := range []string{
		"snapshots/cam-1/2026-11-22/snap-1.jpg",
		"snapshots/cam-1/2026-11-22/face-1.jpg",
		"recordings/cam-1/2026-11-22/evt-1.mp4",
		"faces/alice.jpg",
		"maps/floor1.svg",
	} {
		ok, err := remote.Exists(ctx, key)
		if err != nil || !ok {
			t.Fatalf(
				"expected remote object %q to exist (ok=%v err=%v)",
				key,
				ok,
				err,
			)
		}
	}

	// Non-destructive run must keep original local files.
	if _, err := os.Stat(filepath.Join(mediaDir, "snapshots/cam-1/2026-11-22/snap-1.jpg")); err != nil {
		t.Fatalf("expected non-destructive local snapshot preserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mapsDir, "floor1.svg")); err != nil {
		t.Fatalf("expected non-destructive map preserved: %v", err)
	}

	// Missing asset must not be marked migrated.
	if len(source.migrated) != 4 {
		t.Fatalf("expected 4 migrated assets, got %v", source.migrated)
	}

	// Second run with DeleteLocal=true cleans local source copies once migrated.
	source.mu.Lock()
	source.assets = source.assets[:4]
	source.migrated = nil
	source.mu.Unlock()

	job2, err := svc.StartMigration(
		ctx,
		StartMigrationRequest{DeleteLocal: true},
		"admin",
		"127.0.0.1",
	)
	if err != nil {
		t.Fatalf("StartMigration #2: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cur, err := asyncComp.Service.GetJob(ctx, job2.ID)
		if err == nil && cur.Status.IsTerminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, "snapshots/cam-1/2026-11-22/snap-1.jpg")); !os.IsNotExist(err) {
		t.Fatalf("expected local snapshot removed when deleteLocal=true, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(spoolDir, "recordings/cam-1/2026-11-22/evt-1.mp4")); !os.IsNotExist(err) {
		t.Fatalf("expected spool clip removed when deleteLocal=true, stat err=%v", err)
	}
}

func TestCloseMigrationStores_ClosesLocalAndSpoolOnly(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	mediaDir := filepath.Join(tempDir, "media")
	spoolDir := filepath.Join(tempDir, "spool")
	mustMkdirAll(t, filepath.Join(mediaDir, "snapshots"))
	mustMkdirAll(t, filepath.Join(spoolDir, "recordings"))
	if err := os.WriteFile(filepath.Join(mediaDir, "snapshots/a.jpg"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write media file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(spoolDir, "recordings/b.mp4"), []byte("b"), 0o600); err != nil {
		t.Fatalf("write spool file: %v", err)
	}

	remote := filestore.NewMemoryDriver()
	seedRemoteErr := remote.Put(
		ctx,
		"snapshots/r.jpg",
		bytes.NewReader([]byte("r")),
		filestore.PutOptions{Size: 1},
	)
	if seedRemoteErr != nil {
		t.Fatalf("seed remote: %v", seedRemoteErr)
	}

	cfg := DefaultStorageConfig()
	cfg.MediaDirectory = mediaDir
	cfg.SpoolDirectory = spoolDir
	stores, err := buildMigrationStores(&cfg, remote, nil)
	if err != nil {
		t.Fatalf("buildMigrationStores: %v", err)
	}

	// Open an object through each local driver so os.Root handles are cached before closing.
	for _, tc := range []struct {
		store filestore.Store
		key   string
	}{
		{store: stores.local, key: "snapshots/a.jpg"},
		{store: stores.spool, key: "recordings/b.mp4"},
	} {
		rc, _, openErr := tc.store.Open(ctx, tc.key)
		if openErr != nil {
			t.Fatalf("open %s before close: %v", tc.key, openErr)
		}
		if closeErr := rc.Close(); closeErr != nil {
			t.Fatalf("close reader %s: %v", tc.key, closeErr)
		}
	}

	if err := closeMigrationStores(stores); err != nil {
		t.Fatalf("closeMigrationStores: %v", err)
	}

	for _, tc := range []struct {
		name  string
		store filestore.Store
		key   string
	}{
		{name: "local", store: stores.local, key: "snapshots/a.jpg"},
		{name: "spool", store: stores.spool, key: "recordings/b.mp4"},
	} {
		if _, _, err := tc.store.Open(ctx, tc.key); err == nil {
			t.Fatalf("expected closed %s driver to reject Open after closeMigrationStores", tc.name)
		}
	}

	exists, err := remote.Exists(ctx, "snapshots/r.jpg")
	if err != nil || !exists {
		t.Fatalf("expected shared remote store to stay usable, exists=%v err=%v", exists, err)
	}
}
