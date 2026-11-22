package filestore

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestManager_PureConstructorDoesNotCreateRootDir(t *testing.T) {
	t.Parallel()
	missingRoot := filepath.Join(t.TempDir(), "not-created-yet")

	mgr := NewManager(ManagerConfig{
		Local:  LocalConfig{RootDir: missingRoot},
		Logger: zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	}()

	if mgr.Backend() != BackendLocal {
		t.Fatalf("expected initial backend local, got %q", mgr.Backend())
	}
	if mgr.LocalRoot() != missingRoot {
		t.Fatalf("expected LocalRoot %q, got %q", missingRoot, mgr.LocalRoot())
	}
}

func TestManager_HotSwitchAndLocalFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	localRoot := t.TempDir()

	mgr := NewManager(ManagerConfig{
		Local:  LocalConfig{RootDir: localRoot},
		Logger: zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	}()

	const legacyKey = "snapshots/2026/legacy.jpg"
	legacyData := []byte("legacy-local-jpeg")
	putLegacyErr := mgr.Put(
		ctx,
		legacyKey,
		bytes.NewReader(legacyData),
		PutOptions{
			Size:      int64(len(legacyData)),
			Protected: true,
		},
	)
	if putLegacyErr != nil {
		t.Fatalf("seed legacy local object: %v", putLegacyErr)
	}
	if hasLock, err := mgr.HasProtectionMarker(ctx, legacyKey); err != nil || !hasLock {
		t.Fatalf("expected local protection marker, got hasLock=%v err=%v", hasLock, err)
	}

	// Switch to mock S3 with localFallback=true using mock S3 server.
	mockSrv := newMockS3Server(t, "zhulong-test")

	s3Cfg := S3Config{
		Endpoint:        mockSrv.server.URL,
		Region:          "us-east-1",
		Bucket:          "zhulong-test",
		Prefix:          "edge",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "minioadmin",
		UsePathStyle:    true,
	}
	if err := mgr.ConfigureS3(s3Cfg, LocalConfig{RootDir: localRoot}, true); err != nil {
		t.Fatalf("ConfigureS3 failed: %v", err)
	}
	if mgr.Backend() != BackendS3 || !mgr.LocalFallbackEnabled() {
		t.Fatalf("expected backend s3 with fallback enabled, got %q fallback=%v",
			mgr.Backend(), mgr.LocalFallbackEnabled())
	}

	// Legacy object is absent from primary S3, yet readable via fallback.
	inPrimary, err := mgr.ExistsInPrimary(ctx, legacyKey)
	if err != nil || inPrimary {
		t.Fatalf("expected legacy key absent from S3 primary, got inPrimary=%v err=%v", inPrimary, err)
	}
	if exists, err := mgr.Exists(ctx, legacyKey); err != nil || !exists {
		t.Fatalf("expected legacy key visible via fallback, got exists=%v err=%v", exists, err)
	}
	rc, info, err := mgr.Open(ctx, legacyKey)
	if err != nil {
		t.Fatalf("Open fallback failed: %v", err)
	}
	got, err := io.ReadAll(rc)
	if closeErr := rc.Close(); closeErr != nil {
		t.Fatalf("close reader: %v", closeErr)
	}
	if err != nil || !bytes.Equal(got, legacyData) || info.SizeBytes != int64(len(legacyData)) {
		t.Fatalf(
			"unexpected fallback read: data=%q info=%+v err=%v",
			got,
			info,
			err,
		)
	}

	// New writes go strictly to S3 primary and support presigning.
	const remoteKey = "snapshots/2026/new.jpg"
	remoteData := []byte("remote-s3-jpeg")
	putRemoteErr := mgr.Put(
		ctx,
		remoteKey,
		bytes.NewReader(remoteData),
		PutOptions{
			Size:      int64(len(remoteData)),
			Protected: true,
		},
	)
	if putRemoteErr != nil {
		t.Fatalf("Put remote failed: %v", putRemoteErr)
	}
	if err := mgr.EnsureProtectionMarker(ctx, remoteKey); err != nil {
		t.Fatalf("remote protection marker should be no-op, got %v", err)
	}
	if inPrimary, err := mgr.ExistsInPrimary(ctx, remoteKey); err != nil || !inPrimary {
		t.Fatalf("expected remoteKey in S3 primary, got inPrimary=%v err=%v", inPrimary, err)
	}
	urlStr, err := mgr.PresignGet(ctx, remoteKey, time.Minute)
	if err != nil || urlStr == "" {
		t.Fatalf("expected presigned URL in S3 mode, got url=%q err=%v", urlStr, err)
	}
	if !mgr.SupportsPresign() {
		t.Fatalf("expected SupportsPresign=true in S3 mode")
	}

	// Deleting legacyKey in fallback mode also removes stale local copy.
	if err := mgr.Delete(ctx, legacyKey); err != nil {
		t.Fatalf("Delete legacy key failed: %v", err)
	}
	if exists, err := mgr.Exists(ctx, legacyKey); err != nil || exists {
		t.Fatalf("expected legacyKey deleted everywhere, got exists=%v err=%v", exists, err)
	}

	// Disabling fallback stops local reads from serving unmigrated files.
	const localOnlyKey = "recordings/2026/only-local.mp4"
	localClip := []byte("local-mp4-bytes")
	localOnly := NewLocalDriver(LocalConfig{RootDir: localRoot})
	putLocalErr := localOnly.Put(
		ctx,
		localOnlyKey,
		bytes.NewReader(localClip),
		PutOptions{Size: int64(len(localClip))},
	)
	if putLocalErr != nil {
		t.Fatalf("seed local clip: %v", putLocalErr)
	}
	if err := localOnly.Close(); err != nil {
		t.Fatalf("close helper driver: %v", err)
	}
	if err := mgr.ConfigureS3(s3Cfg, LocalConfig{RootDir: localRoot}, false); err != nil {
		t.Fatalf("ConfigureS3 fallback=false failed: %v", err)
	}
	if _, _, err := mgr.Open(ctx, localOnlyKey); !IsNotFound(err) {
		t.Fatalf("expected NotFound when fallback=false, got %v", err)
	}

	// Switching back to local restores local reads and rejects presign.
	if err := mgr.ConfigureLocal(LocalConfig{RootDir: localRoot}); err != nil {
		t.Fatalf("ConfigureLocal failed: %v", err)
	}
	if mgr.Backend() != BackendLocal {
		t.Fatalf("expected backend local after switch, got %q", mgr.Backend())
	}
	if mgr.SupportsPresign() {
		t.Fatalf("expected SupportsPresign=false on local backend")
	}
	if _, err := mgr.PresignGet(ctx, localOnlyKey, time.Minute); !IsPresignUnsupported(err) {
		t.Fatalf("expected IsPresignUnsupported error on local backend, got %v", err)
	}
	if exists, err := mgr.Exists(ctx, localOnlyKey); err != nil || !exists {
		t.Fatalf("expected local clip readable after ConfigureLocal, got exists=%v err=%v", exists, err)
	}
}

func TestManager_ConcurrentSwapsAndReads(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	localRoot := t.TempDir()

	mgr := NewManager(ManagerConfig{
		Local:  LocalConfig{RootDir: localRoot},
		Logger: zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close manager: %v", err)
		}
	}()

	const key = "snapshots/race/frame.jpg"
	payload := []byte("race-safe-bytes")
	putRaceErr := mgr.Put(
		ctx,
		key,
		bytes.NewReader(payload),
		PutOptions{Size: int64(len(payload))},
	)
	if putRaceErr != nil {
		t.Fatalf("seed key: %v", putRaceErr)
	}

	mem := NewMemoryDriver()
	putMemErr := mem.Put(
		ctx,
		key,
		bytes.NewReader(payload),
		PutOptions{Size: int64(len(payload))},
	)
	if putMemErr != nil {
		t.Fatalf("seed memory driver: %v", putMemErr)
	}

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range 20 {
				if id%2 == 0 {
					rc, _, err := mgr.Open(ctx, key)
					if err != nil {
						t.Errorf("concurrent Open failed: %v", err)
						return
					}
					if _, err := io.ReadAll(rc); err != nil {
						t.Errorf("read failed: %v", err)
					}
					if err := rc.Close(); err != nil {
						t.Errorf("close failed: %v", err)
					}
					continue
				}
				if j%2 == 0 {
					if err := mgr.Swap(mem, nil); err != nil {
						t.Errorf("swap to memory failed: %v", err)
					}
				} else {
					if err := mgr.ConfigureLocal(LocalConfig{RootDir: localRoot}); err != nil {
						t.Errorf("swap to local failed: %v", err)
					}
				}
			}
		}(i)
	}
	wg.Wait()
}
