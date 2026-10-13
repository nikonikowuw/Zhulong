package algorithm

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

func TestInstallDirectoryAtomicallyDoesNotReplaceConcurrentTarget(t *testing.T) {
	root := t.TempDir()
	dst := filepath.Join(root, "installed")
	for _, payload := range []string{"first", "second"} {
		src := filepath.Join(root, payload)
		if err := os.Mkdir(src, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, "payload"), []byte(payload), 0644); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			<-start
			results <- installDirectoryAtomically(filepath.Join(root, name), dst)
		}(name)
	}
	close(start)
	wg.Wait()
	close(results)

	succeeded, rejected := 0, 0
	for err := range results {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrVersionAlreadyInstalled):
			rejected++
		default:
			t.Fatalf("unexpected install error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("expected one install and one rejection, got success=%d rejected=%d", succeeded, rejected)
	}

	installed, err := os.ReadFile(filepath.Join(dst, "payload"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(installed); got != "first" && got != "second" {
		t.Fatalf("unexpected installed payload: %q", got)
	}
}

func TestInstallDirectoryAtomicallyCopiesAcrossFilesystems(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "payload"), []byte("complete"), 0644); err != nil {
		t.Fatal(err)
	}

	parent := makeCrossDeviceTempDir(t, src)
	dst := filepath.Join(parent, "installed")
	if err := installDirectoryAtomically(src, dst); err != nil {
		t.Fatalf("install across filesystems: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dst, "payload")); err != nil || string(got) != "complete" {
		t.Fatalf("installed payload = %q, error = %v", got, err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("cross-filesystem copy should preserve source until caller cleanup: %v", err)
	}
}

func TestInstallDirectoryAtomicallyCleansFailedCrossFilesystemCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "source")
	if err := os.Mkdir(src, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a-payload"), []byte("partial"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing-target", filepath.Join(src, "z-link")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	parent := makeCrossDeviceTempDir(t, src)
	dst := filepath.Join(parent, "installed")
	if err := installDirectoryAtomically(src, dst); err == nil {
		t.Fatal("expected copy to reject a symlink")
	}
	if _, err := os.Lstat(dst); !os.IsNotExist(err) {
		t.Fatalf("failed copy published destination: %v", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed copy left staging entries: %v", entries)
	}
}

func makeCrossDeviceTempDir(t *testing.T, src string) string {
	t.Helper()
	parent, err := os.MkdirTemp("/dev/shm", "zhulong-install-test-")
	if err != nil {
		t.Skipf("cross-filesystem temp directory unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })

	sourceInfo, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	targetInfo, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}
	sourceStat, sourceOK := sourceInfo.Sys().(*syscall.Stat_t)
	targetStat, targetOK := targetInfo.Sys().(*syscall.Stat_t)
	if !sourceOK || !targetOK || sourceStat.Dev == targetStat.Dev {
		t.Skip("/dev/shm is not on a different filesystem")
	}
	return parent
}
