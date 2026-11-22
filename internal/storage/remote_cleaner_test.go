package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

type stubRemotePruneProvider struct {
	plan RemotePrunePlan
	err  error
}

func (s *stubRemotePruneProvider) BuildRemotePrunePlan(
	_ context.Context,
	_ *StorageConfig,
	_ time.Time,
) (RemotePrunePlan, error) {
	return s.plan, s.err
}

func TestCleanerEngine_RunRemotePrune_ExpiredQuotaAndEvidenceGuard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := filestore.NewMemoryDriver()
	seed := func(key string) {
		t.Helper()
		payload := []byte("payload-" + key)
		putErr := store.Put(
			ctx,
			key,
			bytes.NewReader(payload),
			filestore.PutOptions{Size: int64(len(payload))},
		)
		if putErr != nil {
			t.Fatalf("seed %s: %v", key, putErr)
		}
	}

	keys := []string{
		"snapshots/2026/s-exp-free.jpg",
		"snapshots/2026/s-exp-free_face.jpg",
		"snapshots/2026/s-exp-alarm.jpg",
		"snapshots/2026/s-quota-1.jpg",
		"snapshots/2026/s-quota-keep.jpg",
		"recordings/2026/clip-1.mp4",
	}
	for _, k := range keys {
		seed(k)
	}

	cleaner := NewCleanerEngine(nil, zap.NewNop(), CleanerOptions{BatchSize: 2})
	cleaner.SetEvidenceGuard(func(_ context.Context, ids []string) (map[string]bool, error) {
		protected := make(map[string]bool, len(ids))
		for _, id := range ids {
			if id == "snap-alarm" {
				protected[id] = true
			}
		}
		return protected, nil
	})

	provider := &stubRemotePruneProvider{
		plan: RemotePrunePlan{
			Expired: []RemotePruneUnit{
				{
					ID:         "snap-free",
					SnapshotID: "snap-free",
					Keys:       []string{"snapshots/2026/s-exp-free.jpg", "snapshots/2026/s-exp-free_face.jpg"},
					SizeBytes:  200,
				},
				{
					ID:         "snap-alarm",
					SnapshotID: "snap-alarm",
					Keys:       []string{"snapshots/2026/s-exp-alarm.jpg"},
					SizeBytes:  150,
				},
			},
			QuotaExcess: []RemotePruneUnit{
				{
					ID:        "clip-1",
					ClipID:    "clip-1",
					Keys:      []string{"recordings/2026/clip-1.mp4"},
					SizeBytes: 300,
				},
				{
					ID:         "snap-quota-1",
					SnapshotID: "snap-quota-1",
					Keys:       []string{"snapshots/2026/s-quota-1.jpg"},
					SizeBytes:  150,
				},
				{
					ID:         "snap-quota-keep",
					SnapshotID: "snap-quota-keep",
					Keys:       []string{"snapshots/2026/s-quota-keep.jpg"},
					SizeBytes:  500,
				},
			},
			// ExcessBytes=600 -> 200 freed by snap-free, then 300 (clip-1) + 150 (snap-quota-1) reaches 0!
			ExcessBytes: 600,
		},
	}

	cfg := baseValidS3StorageConfig(t.TempDir())
	summary, clipIDs, err := cleaner.RunRemotePrune(
		ctx,
		RemotePruneRequest{
			Config:   &cfg,
			Trigger:  PruneTriggerManual,
			Store:    store,
			Provider: provider,
		},
	)
	if err != nil {
		t.Fatalf("RunRemotePrune failed: %v", err)
	}
	if summary.StopReason != PruneStopTargetReached || summary.DeletedFiles != 4 || summary.ProtectedFiles != 1 {
		t.Fatalf("unexpected remote prune summary: %+v", summary)
	}
	if len(summary.DeletedSnapshotIDs) != 2 || len(clipIDs) != 1 || clipIDs[0] != "clip-1" {
		t.Fatalf("unexpected reconciled IDs: snapshots=%v clips=%v", summary.DeletedSnapshotIDs, clipIDs)
	}

	assertExists := func(key string, want bool) {
		t.Helper()
		ok, existsErr := store.Exists(ctx, key)
		if existsErr != nil || ok != want {
			t.Fatalf(
				"Exists(%s) = (%v, %v), want %v",
				key,
				ok,
				existsErr,
				want,
			)
		}
	}
	assertExists("snapshots/2026/s-exp-free.jpg", false)
	assertExists("snapshots/2026/s-exp-free_face.jpg", false)
	assertExists("recordings/2026/clip-1.mp4", false)
	assertExists("snapshots/2026/s-quota-1.jpg", false)
	assertExists("snapshots/2026/s-exp-alarm.jpg", true)
	assertExists("snapshots/2026/s-quota-keep.jpg", true)
}

func TestCleanerEngine_RunRemotePrune_FailClosedEvidenceGuard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := filestore.NewMemoryDriver()
	seedErr := store.Put(
		ctx,
		"snapshots/2026/u1.jpg",
		bytes.NewReader([]byte("x")),
		filestore.PutOptions{Size: 1},
	)
	if seedErr != nil {
		t.Fatalf("seed object: %v", seedErr)
	}

	cleaner := NewCleanerEngine(nil, zap.NewNop(), CleanerOptions{BatchSize: 10})
	cleaner.SetEvidenceGuard(func(_ context.Context, _ []string) (map[string]bool, error) {
		return nil, errors.New("sqlite locked")
	})

	provider := &stubRemotePruneProvider{
		plan: RemotePrunePlan{
			Expired: []RemotePruneUnit{
				{ID: "u1", SnapshotID: "u1", Keys: []string{"snapshots/2026/u1.jpg"}, SizeBytes: 1},
			},
		},
	}

	cfg := baseValidS3StorageConfig(t.TempDir())
	summary, _, err := cleaner.RunRemotePrune(
		ctx,
		RemotePruneRequest{
			Config:   &cfg,
			Trigger:  PruneTriggerWatermark,
			Store:    store,
			Provider: provider,
		},
	)
	if err != nil {
		t.Fatalf("RunRemotePrune failed: %v", err)
	}
	if summary.DeletedFiles != 0 || summary.ProtectedFiles != 1 {
		t.Fatalf("expected fail-closed guard to protect all files, got %+v", summary)
	}
	if exists, existsErr := store.Exists(ctx, "snapshots/2026/u1.jpg"); existsErr != nil || !exists {
		t.Fatalf("object must not be deleted when EvidenceGuard errors, exists=%v err=%v", exists, existsErr)
	}
}

func TestService_TriggerManualCleanup_S3TriggersReconcilersWithoutLocalWalk(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dbStore := openStorageTestDB(t)
	cipherImpl := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipherImpl, zap.NewNop())

	mediaDir := t.TempDir()
	spoolDir := t.TempDir()
	cfg := baseValidS3StorageConfig(spoolDir)
	cfg.MediaDirectory = mediaDir
	cfg.S3.SecretAccessKey = "s3-secret"
	if _, err := repo.UpdateConfig(ctx, cfg); err != nil {
		t.Fatalf("seed s3 config: %v", err)
	}

	memStore := filestore.NewMemoryDriver()
	putSnapErr := memStore.Put(
		ctx,
		"snapshots/2026/exp.jpg",
		bytes.NewReader([]byte("img")),
		filestore.PutOptions{Size: 3},
	)
	if putSnapErr != nil {
		t.Fatalf("seed snapshot: %v", putSnapErr)
	}
	putClipErr := memStore.Put(
		ctx,
		"recordings/2026/exp.mp4",
		bytes.NewReader([]byte("mp4")),
		filestore.PutOptions{Size: 3},
	)
	if putClipErr != nil {
		t.Fatalf("seed clip: %v", putClipErr)
	}

	svc := NewService(ServiceDeps{
		Repo:   repo,
		Cipher: cipherImpl,
		S3Health: func(_ context.Context, _ filestore.S3Config) (filestore.HealthStatus, error) {
			return filestore.HealthStatus{Backend: filestore.BackendS3, Available: true}, nil
		},
		Logger: zap.NewNop(),
	})

	var reconciledSnapshots, reconciledClips []string
	svc.SetRemoteStore(memStore)
	svc.SetRemotePruneProvider(&stubRemotePruneProvider{
		plan: RemotePrunePlan{
			Expired: []RemotePruneUnit{
				{ID: "snap-1", SnapshotID: "snap-1", Keys: []string{"snapshots/2026/exp.jpg"}, SizeBytes: 3},
				{ID: "clip-1", ClipID: "clip-1", Keys: []string{"recordings/2026/exp.mp4"}, SizeBytes: 3},
			},
		},
	})
	svc.SetAfterPrune(func(_ context.Context, ids []string) {
		reconciledSnapshots = append(reconciledSnapshots, ids...)
	})
	svc.SetRemoteClipReconciler(func(_ context.Context, ids []string) error {
		reconciledClips = append(reconciledClips, ids...)
		return nil
	})

	summary, err := svc.TriggerManualCleanup(ctx, "admin", "127.0.0.1")
	if err != nil {
		t.Fatalf("TriggerManualCleanup in S3 mode failed: %v", err)
	}
	if summary.DeletedFiles != 2 {
		t.Fatalf("expected 2 deleted files, got %+v", summary)
	}
	if len(reconciledSnapshots) != 1 || reconciledSnapshots[0] != "snap-1" {
		t.Fatalf("expected pending_snapshot_deletes callback for snap-1, got %v", reconciledSnapshots)
	}
	if len(reconciledClips) != 1 || reconciledClips[0] != "clip-1" {
		t.Fatalf("expected clip reconciler callback for clip-1, got %v", reconciledClips)
	}
}
