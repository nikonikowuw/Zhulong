package filestore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

type stubMountInspector struct {
	inspectCalls atomic.Int64
	telemetry    MountTelemetry
	inspectErr   error
	rootDev      uint64
	rootErr      error
}

func (s *stubMountInspector) InspectMount(string) (MountTelemetry, error) {
	s.inspectCalls.Add(1)
	if s.inspectErr != nil {
		return MountTelemetry{}, s.inspectErr
	}
	return s.telemetry, nil
}

func (s *stubMountInspector) RootDeviceID() (uint64, error) {
	if s.rootErr != nil {
		return 0, s.rootErr
	}
	return s.rootDev, nil
}

func symlinkOrSkip(t *testing.T, target, linkPath string) {
	t.Helper()
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}
}

func realDeviceID(t *testing.T, p string) uint64 {
	t.Helper()
	info, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	dev, ok := deviceIDFromFileInfo(info)
	if !ok {
		t.Skip("device identity unavailable on this platform")
	}
	return dev
}

func TestLocalDriver_PutOpenStatDeleteExists(t *testing.T) {
	root := t.TempDir()
	facesDir := filepath.Join(t.TempDir(), "faces")
	mapsDir := filepath.Join(t.TempDir(), "maps")

	driver := NewLocalDriver(LocalConfig{
		RootDir: root,
		NamespaceRoots: map[string]string{
			NamespaceFaces: facesDir,
			NamespaceMaps:  mapsDir,
		},
	})
	t.Cleanup(func() {
		if err := driver.Close(); err != nil {
			t.Errorf("close driver: %v", err)
		}
	})

	ctx := context.Background()
	snapKey := "snapshots/cam-01/2026-11-22/evt-1.jpg"
	payload := []byte("0123456789abcdef")

	putErr := driver.Put(
		ctx,
		snapKey,
		bytes.NewReader(payload),
		PutOptions{
			Size:      int64(len(payload)),
			Protected: true,
		},
	)
	if putErr != nil {
		t.Fatalf("Put snapshot: %v", putErr)
	}

	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(snapKey+".lock"))); err != nil {
		t.Fatalf("expected .lock marker next to protected snapshot: %v", err)
	}
	protected, err := driver.HasProtectionMarker(ctx, snapKey)
	if err != nil || !protected {
		t.Fatalf("HasProtectionMarker = (%v, %v), want (true, nil)", protected, err)
	}

	faceKey := "faces/p1_f1.jpg"
	faceErr := driver.Put(
		ctx,
		faceKey,
		strings.NewReader("face-bytes"),
		PutOptions{Size: 10},
	)
	if faceErr != nil {
		t.Fatalf("Put face: %v", faceErr)
	}
	if _, err := os.Stat(filepath.Join(facesDir, "p1_f1.jpg")); err != nil {
		t.Fatalf("expected face stored flat under facesDir override: %v", err)
	}

	rc, info, err := driver.Open(ctx, snapKey)
	if err != nil {
		t.Fatalf("Open snapshot: %v", err)
	}
	if info.SizeBytes != int64(len(payload)) || info.Name != "evt-1.jpg" {
		rc.Close()
		t.Fatalf("unexpected ObjectInfo: %+v", info)
	}
	if _, err := rc.Seek(4, io.SeekStart); err != nil {
		rc.Close()
		t.Fatalf("Seek: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll after Seek: %v", err)
	}
	if string(got) != "456789abcdef" {
		t.Fatalf("unexpected seeked read: %q", string(got))
	}

	exists, err := driver.Exists(ctx, faceKey)
	if err != nil || !exists {
		t.Fatalf(
			"Exists(%q) = (%v, %v)",
			faceKey,
			exists,
			err,
		)
	}
	if err := driver.Delete(ctx, faceKey); err != nil {
		t.Fatalf("Delete face: %v", err)
	}
	exists, err = driver.Exists(ctx, faceKey)
	if err != nil || exists {
		t.Fatalf("Exists after Delete = (%v, %v)", exists, err)
	}
	if err := driver.Delete(ctx, faceKey); err != nil {
		t.Fatalf("idempotent Delete failed: %v", err)
	}
}

func TestLocalDriver_PutFromLocalFile(t *testing.T) {
	root := t.TempDir()
	driver := NewLocalDriver(LocalConfig{RootDir: root})
	t.Cleanup(func() { _ = driver.Close() })

	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "spool.mp4")
	if err := os.WriteFile(srcFile, []byte("clip-bytes"), 0o640); err != nil {
		t.Fatalf("write spool: %v", err)
	}

	key := "recordings/cam-01/2026-11-22/evt-1.mp4"
	if err := driver.PutFromLocalFile(
		context.Background(),
		key,
		srcFile,
		PutOptions{Protected: true},
	); err != nil {
		t.Fatalf("PutFromLocalFile: %v", err)
	}

	if _, err := os.Stat(srcFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected source file to be moved/removed, got err=%v", err)
	}
	rc, _, err := driver.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open adopted clip: %v", err)
	}
	defer rc.Close()
	data, _ := io.ReadAll(rc)
	if string(data) != "clip-bytes" {
		t.Fatalf("unexpected adopted clip bytes: %q", string(data))
	}
}

func TestLocalDriver_SymlinkAndEscapeClassification(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(
		root,
		"recordings",
		"cam-01",
		"a.mp4",
	)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("ok"), 0o644); err != nil {
		t.Fatalf("write target: %v", err)
	}

	symlinkOrSkip(t, "cam-01/a.mp4", filepath.Join(root, "recordings", "rel.mp4"))
	symlinkOrSkip(t, target, filepath.Join(root, "recordings", "abs-inside.mp4"))
	symlinkOrSkip(t, "/etc/passwd", filepath.Join(root, "recordings", "esc-abs"))
	symlinkOrSkip(t, "../../etc/passwd", filepath.Join(root, "recordings", "esc-rel"))

	driver := NewLocalDriver(LocalConfig{RootDir: root})
	t.Cleanup(func() { _ = driver.Close() })

	ctx := context.Background()
	if _, err := driver.Stat(ctx, "recordings/rel.mp4"); err != nil {
		t.Fatalf("expected relative in-root symlink to succeed, got %v", err)
	}

	for _, bad := range []string{
		"recordings/abs-inside.mp4",
		"recordings/esc-abs",
		"recordings/esc-rel",
	} {
		_, err := driver.Stat(ctx, bad)
		if err == nil {
			t.Fatalf("expected %q to fail", bad)
		}
	}

	_, err := driver.Stat(ctx, "recordings/esc-abs")
	if got := apperr.CodeOf(err); got != "MEDIA_PATH_ESCAPE" {
		t.Fatalf("expected MEDIA_PATH_ESCAPE, got %q (%v)", got, err)
	}
}

func TestLocalDriver_FallthroughCheckedOncePerHandle(t *testing.T) {
	if !deviceTelemetryIsReal {
		t.Skip("fallthrough detection requires Linux kernel telemetry")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "exports"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "exports", "a.mp4"), []byte("data"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	realDev := realDeviceID(t, root)

	inspector := &stubMountInspector{
		telemetry: MountTelemetry{DeviceID: realDev, MountPoint: "/mnt/media", FSType: "ext4"},
		rootDev:   realDev + 1,
	}
	driver := NewLocalDriver(LocalConfig{
		RootDir:   root,
		Inspector: inspector,
	})
	t.Cleanup(func() { _ = driver.Close() })

	for range 10 {
		if _, err := driver.Stat(context.Background(), "exports/a.mp4"); err != nil {
			t.Fatalf("Stat failed: %v", err)
		}
	}
	if got := inspector.inspectCalls.Load(); got != 1 {
		t.Fatalf("expected 1 mount inspection across 10 Stats, got %d", got)
	}

	fallenDriver := NewLocalDriver(LocalConfig{
		RootDir: root,
		Inspector: &stubMountInspector{
			telemetry: MountTelemetry{DeviceID: realDev + 1},
			rootDev:   realDev + 1,
		},
	})
	t.Cleanup(func() { _ = fallenDriver.Close() })

	_, err := fallenDriver.Stat(context.Background(), "exports/a.mp4")
	if got := apperr.CodeOf(err); got != "MEDIA_ROOT_UNAVAILABLE" {
		t.Fatalf("expected MEDIA_ROOT_UNAVAILABLE on fallthrough, got %q (%v)", got, err)
	}
}

func TestLocalDriver_ConcurrentOpenAndCloseIsRaceFree(t *testing.T) {
	root := t.TempDir()
	driver := NewLocalDriver(LocalConfig{RootDir: root})
	key := "exports/concurrent.mp4"
	putErr := driver.Put(
		context.Background(),
		key,
		strings.NewReader("payload-content"),
		PutOptions{Size: 15},
	)
	if putErr != nil {
		t.Fatalf("Put: %v", putErr)
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rc, _, err := driver.Open(context.Background(), key)
			if err != nil {
				return
			}
			_, _ = io.ReadAll(rc)
			_ = rc.Close()
		}()
	}
	wg.Wait()
	if err := driver.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestLocalDriver_NoDirectoryScanningInProductionFiles(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("not meaningful on js")
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, forbidden := range []string{"ReadDir", "Readdirnames"} {
			if strings.Contains(string(src), forbidden) {
				t.Errorf("%s contains forbidden directory scan %q", name, forbidden)
			}
		}
	}
}
