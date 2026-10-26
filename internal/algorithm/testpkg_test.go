package algorithm_test

import (
	"os"
	"path/filepath"
	"testing"
)

// Package stubs for tests that need a *runnable* package.
//
// Since packages became self-contained executables, a test package cannot get
// away with a dummy `plugin.so` blob: the installer, the verifier and the
// supervisor all resolve `<manifest.id>` and execute it. These helpers produce
// the two shapes the host actually meets — a stub that answers `--verify` during
// installation, and a stub that simply stays alive once supervised.

// verifyStubReport is what a well-behaved package entry prints when invoked as
// `--verify --sample <image>`. result_type is the only field the host
// cross-checks against the manifest.
func verifyStubScript(resultType int) string {
	return "#!/bin/sh\n" +
		"printf '%s\\n' '{\"step\":\"verify\",\"status\":\"passed\",\"sample_decoded\":false,\"width\":0,\"height\":0,\"result_type\":" + itoa(resultType) + ",\"count\":0}'\n" +
		"exit 0\n"
}

// idleStubScript stays alive until SIGTERM, so a supervised status is
// deterministic. `while true; do sleep 1; done` keeps sh as the direct child so
// the trap fires promptly.
func idleStubScript() string {
	return "#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
}

// installableEntry returns an executable stub suitable for putting *inside an
// archive*: it answers the --verify probe and otherwise idles.
func installableEntry(resultType int) []byte {
	return []byte("#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  --verify)\n" +
		"    printf '%s\\n' '{\"step\":\"verify\",\"status\":\"passed\",\"sample_decoded\":false,\"width\":0,\"height\":0,\"result_type\":" + itoa(resultType) + ",\"count\":0}'\n" +
		"    exit 0\n" +
		"    ;;\n" +
		"esac\n" +
		"trap 'exit 0' TERM INT\n" +
		"while true; do sleep 1; done\n")
}

// installStubPackage materialises an installed package (no archive, no
// verifier) at <root>/<id>/<version>/<id>, which is exactly the layout the
// supervisor resolves.
func installStubPackage(t *testing.T, root, id, version, script string) {
	t.Helper()
	dir := filepath.Join(root, id, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, id), []byte(script), 0o755); err != nil {
		t.Fatalf("write package entry: %v", err)
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	digits := ""
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		digits = string(rune('0'+v%10)) + digits
		v /= 10
	}
	if neg {
		return "-" + digits
	}
	return digits
}

// seedSampleAndJunk writes the two files that every static-gate test needs and
// that are independent of the manifest: a sample.jpg placeholder and a plainly
// invalid executable-named file. Tests that care about the entry name seed it
// themselves once the manifest ID is known.
func seedSampleAndJunk(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "sample.jpg"), []byte("sample"), 0o644); err != nil {
		t.Fatalf("write sample.jpg: %v", err)
	}
}

// seedEntry writes the package entry named after id, so a test that only wrote
// a manifest can reach the later stages.
func seedEntry(t *testing.T, dir, id string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, id), []byte(verifyStubScript(1)), 0o755); err != nil {
		t.Fatalf("write package entry: %v", err)
	}
}
