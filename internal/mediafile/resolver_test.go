package mediafile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// fixtureRoot builds a media root with the three real subdirectories plus the
// link shapes the defence layers must distinguish.
//
// Link targets are written as relative paths wherever a link is meant to be
// legal. That is not a stylistic choice: os.Root rejects an absolute target even
// when it resolves back inside the root, so a fixture built with
// filepath.Join(root, ...) targets would silently test the rejection path while
// claiming to test acceptance.
type fixtureRoot struct {
	path string
}

func newFixtureRoot(t *testing.T) fixtureRoot {
	t.Helper()

	root := t.TempDir()
	for _, dir := range []string{
		"recordings/cam-01/2026-01-01",
		"snapshots/cam-01/2026-01-01",
		"exports",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("create fixture dir %s: %v", dir, err)
		}
	}

	writeFixtureFile(t, root, "recordings/cam-01/2026-01-01/10-00-00.mp4", "recording-bytes")
	writeFixtureFile(t, root, "snapshots/cam-01/2026-01-01/evt-1.jpg", "snapshot-bytes")
	writeFixtureFile(t, root, "exports/task-1.mp4", "export-bytes")

	return fixtureRoot{path: root}
}

func writeFixtureFile(t *testing.T, root, relPath, content string) {
	t.Helper()
	target := filepath.Join(root, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("create parent of %s: %v", relPath, err)
	}
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture %s: %v", relPath, err)
	}
}

// newFixtureResolver creates a resolver without a device inspector, so the
// fallthrough layer stays out of path-shape tests.
func newFixtureResolver(t *testing.T, root string) *resolver {
	t.Helper()
	r, err := newResolver(root, nil)
	if err != nil {
		t.Fatalf("newResolver(%s): %v", root, err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Errorf("close resolver: %v", err)
		}
	})
	return r
}

// symlinkOrSkip creates a symlink, skipping the test on platforms or sandboxes
// where symlink creation is unavailable. The suite must still run for real on
// Linux CI.
func symlinkOrSkip(t *testing.T, target, linkPath string) {
	t.Helper()
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}
}

func TestResolver_RejectsTraversalAndMalformedPaths(t *testing.T) {
	fixture := newFixtureRoot(t)
	r := newFixtureResolver(t, fixture.path)

	cases := []struct {
		name string
		path string
		code string
	}{
		{name: "parent traversal", path: "../../etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "traversal after known kind", path: "recordings/../../etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "deep traversal", path: "recordings/cam-01/2026-01-01/../../../etc", code: "MEDIA_PATH_INVALID"},
		{name: "absolute path", path: "/etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "absolute path into media dir", path: "/data/media/recordings/a.mp4", code: "MEDIA_PATH_INVALID"},
		{name: "double leading slash", path: "//etc/passwd", code: "MEDIA_PATH_INVALID"},
		{name: "NUL byte", path: "recordings/a\x00.mp4", code: "MEDIA_PATH_INVALID"},
		{name: "empty path", path: "", code: "MEDIA_PATH_INVALID"},
		{name: "single dot segment", path: ".", code: "MEDIA_PATH_INVALID"},
		{name: "single dotdot segment", path: "..", code: "MEDIA_PATH_INVALID"},
		{name: "dot segment inside", path: "recordings/./a.mp4", code: "MEDIA_PATH_INVALID"},
		{name: "empty segment from double slash", path: "recordings//a.mp4", code: "MEDIA_PATH_INVALID"},
		{name: "trailing slash", path: "recordings/a.mp4/", code: "MEDIA_PATH_INVALID"},
		{name: "space in segment", path: "recordings/cam 01/a.mp4", code: "MEDIA_PATH_INVALID"},
		{name: "overlong path", path: "recordings/" + strings.Repeat("a", mediaPathMaxLength), code: "MEDIA_PATH_INVALID"},
		{name: "unknown top-level kind", path: "evil/a.mp4", code: "MEDIA_KIND_INVALID"},
		{name: "known kind not first", path: "cam-01/recordings/a.mp4", code: "MEDIA_KIND_INVALID"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := r.stat(tc.path)
			if err == nil {
				t.Fatalf("expected %s for %q, got success", tc.code, tc.path)
			}

			var appErr *apperr.Error
			if !errors.As(err, &appErr) {
				t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
			}
			if appErr.Code != tc.code {
				t.Fatalf("expected code %s for %q, got %s (%v)", tc.code, tc.path, appErr.Code, err)
			}
		})
	}
}

func TestResolver_AcceptsValidPaths(t *testing.T) {
	fixture := newFixtureRoot(t)
	r := newFixtureResolver(t, fixture.path)

	cases := []struct {
		name string
		path string
	}{
		{name: "recording", path: "recordings/cam-01/2026-01-01/10-00-00.mp4"},
		{name: "snapshot", path: "snapshots/cam-01/2026-01-01/evt-1.jpg"},
		{name: "flat export", path: "exports/task-1.mp4"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clean, info, err := r.stat(tc.path)
			if err != nil {
				t.Fatalf("expected %q to be accepted, got %v", tc.path, err)
			}
			if clean != tc.path {
				t.Errorf("expected validated path %q, got %q", tc.path, clean)
			}
			if info.IsDir() {
				t.Errorf("expected a file for %q", tc.path)
			}
		})
	}
}

// TestResolver_MissingAndMalformedAreNotFound covers the two rejection shapes
// that must not be reported as an internal error.
//
// A file removed by the retention cleaner is the normal case this endpoint must
// report as 404. Traversing through a regular file is a malformed request, and
// reporting it as a 500 would make a client error look like a server fault and
// pollute the 5xx signal.
func TestResolver_MissingAndMalformedAreNotFound(t *testing.T) {
	fixture := newFixtureRoot(t)
	symlinkOrSkip(t, "..", filepath.Join(fixture.path, "snapshots", "up"))

	r := newFixtureResolver(t, fixture.path)

	cases := []struct {
		name string
		path string
	}{
		{name: "file pruned by the retention cleaner", path: "exports/gone.mp4"},
		{name: "missing nested path", path: "recordings/cam-01/2026-01-01/gone.mp4"},
		{name: "segment traverses through a regular file", path: "recordings/cam-01/2026-01-01/10-00-00.mp4/nested.mp4"},
		// A traversal sequence inside the *request path* is caught by the
		// first defence layer as MEDIA_PATH_INVALID. The ENOENT-shaped
		// rejection only arises when the escape comes from a symlink target,
		// which is the "up" link created below.
		{name: "symlink target escaping via parent segments", path: "snapshots/up/etc/passwd"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := r.stat(tc.path)
			if err == nil {
				t.Fatalf("expected %q to be refused", tc.path)
			}

			var appErr *apperr.Error
			if !errors.As(err, &appErr) {
				t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
			}
			if appErr.Code != "MEDIA_ENTRY_NOT_FOUND" {
				t.Errorf("expected MEDIA_ENTRY_NOT_FOUND, got %s (%v)", appErr.Code, err)
			}
			if appErr.Kind != apperr.KindNotFound {
				t.Errorf("expected KindNotFound, got %v", appErr.Kind)
			}
		})
	}
}

func TestResolver_SymlinkPolicy(t *testing.T) {
	fixture := newFixtureRoot(t)
	root := fixture.path

	// Relative target, resolves inside the root: legal deployment shape.
	symlinkOrSkip(t, "cam-01/2026-01-01/10-00-00.mp4", filepath.Join(root, "recordings", "rel-file.mp4"))
	symlinkOrSkip(t, "cam-01", filepath.Join(root, "recordings", "dirlink"))
	symlinkOrSkip(t, "rel-file.mp4", filepath.Join(root, "recordings", "chain.mp4"))

	// Absolute target even though it lands inside the root: rejected.
	symlinkOrSkip(t, filepath.Join(root, "recordings", "cam-01", "2026-01-01", "10-00-00.mp4"), filepath.Join(root, "recordings", "abs-inside.mp4"))

	// Escaping links. These sit under a whitelisted top-level directory on
	// purpose: an unknown top-level name is refused by the kind whitelist
	// before os.Root is consulted, so a link at the root would never exercise
	// the kernel-level escape rejection.
	symlinkOrSkip(t, "/etc/passwd", filepath.Join(root, "recordings", "esc-abs"))
	symlinkOrSkip(t, "../../etc/passwd", filepath.Join(root, "recordings", "esc-rel"))
	symlinkOrSkip(t, "..", filepath.Join(root, "snapshots", "up"))

	r := newFixtureResolver(t, root)

	accepted := []string{
		"recordings/rel-file.mp4",
		"recordings/chain.mp4",
		"recordings/dirlink/2026-01-01/10-00-00.mp4",
	}
	for _, path := range accepted {
		t.Run("allow/"+path, func(t *testing.T) {
			if _, _, err := r.stat(path); err != nil {
				t.Fatalf("expected %q to be allowed, got %v", path, err)
			}
		})
	}

	// Both escape shapes must be refused, and neither may ever be a success.
	rejected := []string{
		"recordings/esc-abs",
		"recordings/esc-rel",
		"recordings/abs-inside.mp4",
		"snapshots/up/etc/passwd",
		// A link at the root is refused by the kind whitelist instead, which is
		// a separate (and equally closed) rejection path.
		"esc-root",
	}
	for _, path := range rejected {
		t.Run("deny/"+path, func(t *testing.T) {
			if _, _, err := r.stat(path); err == nil {
				t.Fatalf("expected %q to be refused, got success", path)
			}
		})
	}

	// The escape classification must be specific for the EXDEV-shaped
	// rejections, so callers can tell a traversal attempt from a missing file.
	t.Run("escape is classified", func(t *testing.T) {
		_, _, err := r.stat("recordings/esc-abs")
		var appErr *apperr.Error
		if !errors.As(err, &appErr) {
			t.Fatalf("expected *apperr.Error, got %T: %v", err, err)
		}
		if appErr.Code != "MEDIA_PATH_ESCAPE" {
			t.Fatalf("expected MEDIA_PATH_ESCAPE for an absolute symlink, got %s (%v)", appErr.Code, err)
		}
	})
}

// TestResolver_EscapeErrorTextIsStable guards the one fragile assumption in the
// escape classification: the standard library exposes no sentinel error for
// "path escapes from parent", so the text is the only signal available. If a Go
// upgrade rewords it, classification silently degrades to not-found for every
// traversal attempt, which is a fail-closed outcome but a real behaviour change.
// This test fails loudly at that moment instead.
func TestResolver_EscapeErrorTextIsStable(t *testing.T) {
	fixture := newFixtureRoot(t)
	root := fixture.path
	symlinkOrSkip(t, "/etc/passwd", filepath.Join(root, "recordings", "esc-abs"))

	r := newFixtureResolver(t, root)

	_, err := r.root.Open("recordings/esc-abs")
	if err == nil {
		t.Fatal("expected the escaping symlink to be refused by os.Root")
	}

	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("expected *os.PathError from os.Root, got %T: %v", err, err)
	}
	if !strings.Contains(pathErr.Err.Error(), escapeErrText) {
		t.Fatalf("os.Root escape error text changed: %q no longer contains %q; "+
			"update escapeErrText and re-verify the escape classification",
			pathErr.Err.Error(), escapeErrText)
	}
}

func TestResolver_RootItselfMayBeASymlink(t *testing.T) {
	fixture := newFixtureRoot(t)
	linkRoot := filepath.Join(t.TempDir(), "media-link")
	symlinkOrSkip(t, fixture.path, linkRoot)

	r := newFixtureResolver(t, linkRoot)
	if _, _, err := r.stat("recordings/cam-01/2026-01-01/10-00-00.mp4"); err != nil {
		t.Fatalf("expected a symlinked media root to work, got %v", err)
	}
}

func TestResolver_DirectoryIsNotAFile(t *testing.T) {
	fixture := newFixtureRoot(t)
	r := newFixtureResolver(t, fixture.path)

	// os.Root.Open succeeds on a directory, so the type check must be explicit
	// in the service rather than inferred from a failed open.
	file, err := r.open("recordings/cam-01")
	if err != nil {
		t.Fatalf("expected os.Root.Open to succeed on a directory, got %v", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		t.Fatalf("stat opened directory: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected the opened handle to be reported as a directory")
	}
}

func TestResolver_OpenFileIsIndependentOfRootHandle(t *testing.T) {
	fixture := newFixtureRoot(t)
	root, err := os.OpenRoot(fixture.path)
	if err != nil {
		t.Fatalf("open root: %v", err)
	}

	file, err := root.Open("exports/task-1.mp4")
	if err != nil {
		root.Close()
		t.Fatalf("open file: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatalf("close root: %v", err)
	}

	// The hot-switch design closes the previous root handle while in-flight
	// requests are still streaming. That is only safe because an opened file
	// keeps its own descriptor.
	buf := make([]byte, 6)
	n, err := file.ReadAt(buf, 0)
	if err != nil {
		file.Close()
		t.Fatalf("read after closing the root handle: %v", err)
	}
	file.Close()

	if got := string(buf[:n]); got != "export" {
		t.Fatalf("expected to read %q, got %q", "export", got)
	}
}

func TestResolver_NoDirectoryScanning(t *testing.T) {
	if runtime.GOOS == "js" {
		t.Skip("source scan not meaningful on this platform")
	}

	// The service deliberately provides no listing. Directory enumeration must
	// not exist anywhere in the package, not merely be absent from the route
	// table: a helper that enumerates would be the seed of a scan endpoint.
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		source, err := os.ReadFile(entry.Name())
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		for _, forbidden := range []string{"ReadDir", "Readdirnames"} {
			if strings.Contains(string(source), forbidden) {
				t.Errorf("%s contains %q; media listing belongs to the event domain, not this service",
					entry.Name(), forbidden)
			}
		}
	}
}
