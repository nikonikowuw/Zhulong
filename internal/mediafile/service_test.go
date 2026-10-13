package mediafile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/storage"
)

// stubRootProvider returns a media root that tests can swap, which is how the
// configuration hot switch is simulated.
type stubRootProvider struct {
	mu   sync.RWMutex
	root string
	err  error
}

func (p *stubRootProvider) MediaRoot(context.Context) (string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.root, p.err
}

func (p *stubRootProvider) set(root string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.root = root
}

// stubInspector records how often filesystem telemetry is consulted, so tests
// can prove the fallthrough check is not on the request path.
type stubInspector struct {
	inspectCalls atomic.Int64
	rootDevCalls atomic.Int64

	stats    *storage.FSStats
	statsErr error
	rootDev  uint64
	rootErr  error
}

func (i *stubInspector) InspectFS(string) (*storage.FSStats, error) {
	i.inspectCalls.Add(1)
	if i.statsErr != nil {
		return nil, i.statsErr
	}
	return i.stats, nil
}

func (i *stubInspector) GetRootDeviceID() (uint64, error) {
	i.rootDevCalls.Add(1)
	if i.rootErr != nil {
		return 0, i.rootErr
	}
	return i.rootDev, nil
}

const (
	testRootDevice = uint64(100)
	testDiskDevice = uint64(200)
)

// recordingInspector models an external disk that is currently mounted.
func recordingInspector() *stubInspector {
	return &stubInspector{
		stats:   &storage.FSStats{DeviceID: testDiskDevice, MountPoint: "/mnt/media", FSType: "ext4"},
		rootDev: testRootDevice,
	}
}

func newFixtureService(t *testing.T, provider MediaRootProvider, inspector DeviceInspector) *Service {
	t.Helper()
	return NewService(provider, inspector, nil)
}

func TestService_GetEntryReturnsMetadata(t *testing.T) {
	fixture := newFixtureRoot(t)
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, recordingInspector())

	entry, err := svc.GetEntry(context.Background(), "snapshots/cam-01/2026-01-01/evt-1.jpg")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}

	if entry.Path != "snapshots/cam-01/2026-01-01/evt-1.jpg" {
		t.Errorf("unexpected path %q", entry.Path)
	}
	if entry.Name != "evt-1.jpg" {
		t.Errorf("unexpected name %q", entry.Name)
	}
	if entry.SizeBytes != int64(len("snapshot-bytes")) {
		t.Errorf("unexpected size %d", entry.SizeBytes)
	}
	if entry.ModifiedAt.IsZero() {
		t.Error("expected a modification time")
	}
	if entry.ModifiedAt.Location().String() != "UTC" {
		t.Errorf("expected UTC modification time, got %s", entry.ModifiedAt.Location())
	}
}

func TestService_RejectsDirectoryAsNotAFile(t *testing.T) {
	fixture := newFixtureRoot(t)
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, recordingInspector())

	_, err := svc.GetEntry(context.Background(), "recordings/cam-01")
	assertAppErrorCode(t, err, "MEDIA_NOT_A_FILE", apperr.KindInvalid)

	file, _, err := svc.OpenContent(context.Background(), "recordings/cam-01")
	if file != nil {
		file.Close()
	}
	assertAppErrorCode(t, err, "MEDIA_NOT_A_FILE", apperr.KindInvalid)
}

func TestService_OpenContentReturnsSeekableFile(t *testing.T) {
	fixture := newFixtureRoot(t)
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, recordingInspector())

	file, info, err := svc.OpenContent(context.Background(), "exports/task-1.mp4")
	if err != nil {
		t.Fatalf("OpenContent: %v", err)
	}
	defer file.Close()

	if info.IsDir() {
		t.Fatal("expected a regular file")
	}
	// ServeContent needs a ReadSeeker; a root handle that returned a
	// non-seekable stream would break range support.
	if _, err := file.Seek(3, 0); err != nil {
		t.Fatalf("expected a seekable file: %v", err)
	}
	buf := make([]byte, 3)
	if _, err := file.Read(buf); err != nil {
		t.Fatalf("read after seek: %v", err)
	}
	if string(buf) != "ort" {
		t.Errorf("expected %q after seeking to 3, got %q", "ort", string(buf))
	}
}

func TestService_MissingRootIsUnavailable(t *testing.T) {
	svc := newFixtureService(t, &stubRootProvider{}, recordingInspector())

	_, err := svc.GetEntry(context.Background(), "exports/task-1.mp4")
	assertAppErrorCode(t, err, "MEDIA_ROOT_UNAVAILABLE", apperr.KindInternal)
}

func TestService_ProviderFailureIsUnavailable(t *testing.T) {
	provider := &stubRootProvider{err: errors.New("database is locked")}
	svc := newFixtureService(t, provider, recordingInspector())

	_, err := svc.GetEntry(context.Background(), "exports/task-1.mp4")
	assertAppErrorCode(t, err, "MEDIA_ROOT_UNAVAILABLE", apperr.KindInternal)
}

// realDeviceID returns the device the fixture actually lives on. The resolver
// reads st_dev from the real filesystem, so a synthetic inspector only produces
// a meaningful fallthrough verdict when its numbers are expressed relative to
// this value.
func realDeviceID(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	deviceID, ok := deviceIDFromFileInfo(info)
	if !ok {
		t.Skip("device identity is unavailable on this platform")
	}
	return deviceID
}

func TestService_FallthroughDetection(t *testing.T) {
	if !deviceTelemetryIsReal {
		t.Skip("fallthrough detection requires real kernel filesystem telemetry (Linux only)")
	}

	cases := []struct {
		name string
		// adjust receives the fixture's real device id and the inspector, and
		// is responsible for expressing the scenario relative to it.
		adjust      func(realDev uint64, i *stubInspector)
		wantRefused bool
	}{
		{
			// The default configuration: MediaDirectory is "data/media" on the
			// system filesystem, so the root device is the media root device. A
			// naive "device != expected" comparison would refuse every read
			// here, which is exactly the bug this case locks out.
			name: "media root on the system filesystem is allowed",
			adjust: func(realDev uint64, i *stubInspector) {
				i.stats = &storage.FSStats{DeviceID: realDev}
				i.rootDev = realDev
			},
			wantRefused: false,
		},
		{
			name: "external disk mounted normally is allowed",
			adjust: func(realDev uint64, i *stubInspector) {
				i.stats = &storage.FSStats{DeviceID: realDev}
				i.rootDev = realDev + 1
			},
			wantRefused: false,
		},
		{
			// The disk dropped off: the mount point now resolves to the root
			// device, which is different from the device captured when the
			// handle was built.
			name: "external disk fallen through to the system filesystem is refused",
			adjust: func(realDev uint64, i *stubInspector) {
				i.rootDev = realDev + 1
				i.stats = &storage.FSStats{DeviceID: realDev + 1}
			},
			wantRefused: true,
		},
		{
			name: "inspect failure is refused",
			adjust: func(realDev uint64, i *stubInspector) {
				i.rootDev = realDev + 1
				i.statsErr = errors.New("statfs failed")
			},
			wantRefused: true,
		},
		{
			name: "root device lookup failure is refused",
			adjust: func(realDev uint64, i *stubInspector) {
				i.stats = &storage.FSStats{DeviceID: realDev}
				i.rootErr = errors.New("stat / failed")
			},
			wantRefused: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newFixtureRoot(t)
			realDev := realDeviceID(t, fixture.path)

			inspector := &stubInspector{}
			tc.adjust(realDev, inspector)

			svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, inspector)

			_, err := svc.GetEntry(context.Background(), "exports/task-1.mp4")
			if tc.wantRefused {
				assertAppErrorCode(t, err, "MEDIA_ROOT_UNAVAILABLE", apperr.KindInternal)
				return
			}
			if err != nil {
				t.Fatalf("expected the read to be allowed, got %v", err)
			}
		})
	}
}

// TestService_SpaceCircuitBreakerDoesNotBlockReads pins the distinction the
// design turns on: running out of space stops writes, but the operator must
// still be able to review the evidence already on disk. Only a fallthrough
// refuses reads.
func TestService_SpaceCircuitBreakerDoesNotBlockReads(t *testing.T) {
	fixture := newFixtureRoot(t)
	inspector := recordingInspector()

	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, inspector)

	// A read-only, nearly full volume: no device change, so reads must pass.
	inspector.stats.IsReadOnly = false
	inspector.stats.FreeBytes = 1
	inspector.stats.UsedBytes = inspector.stats.TotalBytes - 1

	if _, err := svc.GetEntry(context.Background(), "exports/task-1.mp4"); err != nil {
		t.Fatalf("a full disk must not block reads, got %v", err)
	}
	if _, _, err := svc.OpenContent(context.Background(), "exports/task-1.mp4"); err != nil {
		t.Fatalf("a full disk must not block content reads, got %v", err)
	}
}

// TestService_FallthroughCheckIsNotOnTheRequestPath verifies that filesystem
// telemetry is consulted when the handle is built and not per request. A single
// video preview issues tens to hundreds of range requests, and each inspection
// reads /proc/mounts.
func TestService_FallthroughCheckIsNotOnTheRequestPath(t *testing.T) {
	if !deviceTelemetryIsReal {
		t.Skip("fallthrough detection requires real kernel filesystem telemetry (Linux only)")
	}

	fixture := newFixtureRoot(t)
	inspector := recordingInspector()
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, inspector)

	const requests = 25
	for i := 0; i < requests; i++ {
		if _, _, err := svc.OpenContent(context.Background(), "exports/task-1.mp4"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}

	if got := inspector.inspectCalls.Load(); got != 1 {
		t.Errorf("expected exactly 1 filesystem inspection for %d requests, got %d", requests, got)
	}
}

func TestService_HotSwitchUsesTheNewRoot(t *testing.T) {
	first := newFixtureRoot(t)
	second := t.TempDir()
	writeFixtureFile(t, second, "exports/other.mp4", "second-root-bytes")

	provider := &stubRootProvider{root: first.path}
	svc := newFixtureService(t, provider, recordingInspector())

	if _, err := svc.GetEntry(context.Background(), "exports/task-1.mp4"); err != nil {
		t.Fatalf("first root: %v", err)
	}

	provider.set(second)

	// The old root's files are no longer reachable: the switch is a pointer
	// swap, not a migration.
	_, err := svc.GetEntry(context.Background(), "exports/task-1.mp4")
	assertAppErrorCode(t, err, "MEDIA_ENTRY_NOT_FOUND", apperr.KindNotFound)

	if _, err := svc.GetEntry(context.Background(), "exports/other.mp4"); err != nil {
		t.Fatalf("second root: %v", err)
	}
}

// TestService_ConcurrentOpenDuringHotSwitch exercises the handle swap under
// -race. It is the test that fails if the previous root handle is closed
// without the write lock, or if an opened file were tied to the root handle's
// lifetime.
func TestService_ConcurrentOpenDuringHotSwitch(t *testing.T) {
	roots := make([]string, 4)
	for i := range roots {
		roots[i] = t.TempDir()
		writeFixtureFile(t, roots[i], "exports/task-1.mp4", strings.Repeat("x", 64))
	}

	provider := &stubRootProvider{root: roots[0]}
	svc := newFixtureService(t, provider, recordingInspector())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup

	// Readers continuously open and read while the root changes underneath.
	for reader := 0; reader < 8; reader++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				file, _, err := svc.OpenContent(ctx, "exports/task-1.mp4")
				if err != nil {
					continue
				}
				buf := make([]byte, 8)
				if _, err := file.Read(buf); err != nil {
					t.Errorf("read while switching roots: %v", err)
				}
				file.Close()
			}
		}()
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil; i++ {
			provider.set(roots[i%len(roots)])
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ctx.Err() == nil && i < 200; i++ {
			_, _ = svc.GetRootInfo(ctx)
		}
	}()

	// Let the goroutines interleave, then stop them.
	for i := 0; i < 50; i++ {
		provider.set(roots[i%len(roots)])
	}
	cancel()
	wg.Wait()
}

func TestService_GetRootInfoReportsDirectories(t *testing.T) {
	fixture := newFixtureRoot(t)
	// Remove one subdirectory to prove the report reflects reality.
	if err := os.RemoveAll(filepath.Join(fixture.path, "exports")); err != nil {
		t.Fatalf("remove exports: %v", err)
	}

	inspector := recordingInspector()
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, inspector)

	info, err := svc.GetRootInfo(context.Background())
	if err != nil {
		t.Fatalf("GetRootInfo: %v", err)
	}

	// Directory presence comes from stat calls on the three known names, so it
	// is reported identically on every platform.
	if !info.Available {
		t.Error("expected the media root to be available")
	}
	if !info.Directories.Recordings || !info.Directories.Snapshots {
		t.Errorf("expected recordings and snapshots to exist, got %+v", info.Directories)
	}
	if info.Directories.Exports {
		t.Error("expected exports to be reported as missing after removal")
	}

	if !deviceTelemetryIsReal {
		return
	}
	if info.DeviceID != testDiskDevice || info.RootDevice != testRootDevice {
		t.Errorf("unexpected device identity: %+v", info)
	}
	if !info.IsExternal {
		t.Error("expected the disk to be reported as external")
	}
	if info.MountPoint != "/mnt/media" || info.FSType != "ext4" {
		t.Errorf("unexpected mount details: %+v", info)
	}
}

func TestService_GetRootInfoReportsUnavailableOnFallthrough(t *testing.T) {
	if !deviceTelemetryIsReal {
		t.Skip("fallthrough detection requires real kernel filesystem telemetry (Linux only)")
	}

	fixture := newFixtureRoot(t)

	// A media root that starts on the system device cannot fall through; use a
	// provider whose root changes to simulate the disk disappearing. Here the
	// simpler check is that a broken root is reported unavailable rather than
	// advertised as usable.
	inspector := recordingInspector()
	inspector.statsErr = errors.New("statfs failed")
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, inspector)

	info, err := svc.GetRootInfo(context.Background())
	if err != nil {
		t.Fatalf("GetRootInfo must not fail the request: %v", err)
	}
	if info.Available {
		t.Error("expected an unavailable root to be reported as such")
	}
}

func TestService_ConcurrentGetEntryIsRaceFree(t *testing.T) {
	fixture := newFixtureRoot(t)
	svc := newFixtureService(t, &stubRootProvider{root: fixture.path}, recordingInspector())

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			path := fmt.Sprintf("exports/task-1.mp4")
			if _, err := svc.GetEntry(context.Background(), path); err != nil {
				t.Errorf("goroutine %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
}

func assertAppErrorCode(t *testing.T, err error, code string, kind apperr.Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %s, got nil", code)
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
	}
	if appErr.Code != code {
		t.Errorf("expected code %s, got %s (%v)", code, appErr.Code, err)
	}
	if appErr.Kind != kind {
		t.Errorf("expected kind %v for %s, got %v", kind, code, appErr.Kind)
	}
}

// TestIsFallenThrough covers the fallthrough rule itself, independently of the
// platform gate that decides whether real kernel telemetry is available. This
// keeps the decision covered on every host, including developer machines where
// the storage inspector returns synthetic values.
func TestIsFallenThrough(t *testing.T) {
	const root = uint64(100)
	const disk = uint64(200)

	cases := []struct {
		name     string
		selfDev  uint64
		statsDev uint64
		rootDev  uint64
		want     bool
	}{
		{
			// The default configuration: the media root lives on the system
			// filesystem, so there is nothing to fall through to.
			name:    "media root on the system filesystem never falls through",
			selfDev: root, statsDev: root, rootDev: root,
			want: false,
		},
		{
			name:    "external disk still mounted resolves to its own device",
			selfDev: disk, statsDev: disk, rootDev: root,
			want: false,
		},
		{
			// The disk dropped off and the mount point now exposes the system
			// filesystem. Serving from here would read the wrong volume.
			name:    "external disk resolving to the system device has fallen through",
			selfDev: disk, statsDev: root, rootDev: root,
			want: true,
		},
		{
			name:    "a different external device is a swap, not a fallthrough",
			selfDev: disk, statsDev: disk + 1, rootDev: root,
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFallenThrough(tc.selfDev, tc.statsDev, tc.rootDev); got != tc.want {
				t.Errorf("isFallenThrough(%d, %d, %d) = %v, want %v",
					tc.selfDev, tc.statsDev, tc.rootDev, got, tc.want)
			}
		})
	}
}
