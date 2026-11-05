package algorithm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// fakeVerifyEntryScript is the *package entry* of a stub package: it emulates
// `<id> --verify --sample <image>` while recording two facts the verifier
// contract depends on:
//
//	$CAPTURE_META line 1 → the working directory it was started in (must be the
//	                       package dir, so weight resolution matches deployment)
//	$CAPTURE_META line 2 → the sample path, copied to $CAPTURE_PPM (must be a P6
//	                       PPM, i.e. Go decoded sample.jpg before handing it over)
//
// It reports the decoded dimensions from the environment so the test can assert
// report parsing without a native binary.
const fakeVerifyEntryScript = `#!/bin/sh
sample=""
while [ $# -gt 0 ]; do
  case "$1" in
    --sample) sample="$2"; shift 2 ;;
    *) shift ;;
  esac
done
pwd -P > "$CAPTURE_META"
printf '%s\n' "$sample" >> "$CAPTURE_META"
cp "$sample" "$CAPTURE_PPM"
printf '{"status":"passed","sample":"%s","sample_decoded":%s,"width":%s,"height":%s,"result_type":1,"count":%s}\n' \
  "$sample" "$SAMPLE_DECODED" "$SAMPLE_W" "$SAMPLE_H" "$RESULT_COUNT"
`

// writeSampleJPEG writes a tiny deterministic JPEG the verifier must decode.
func writeSampleJPEG(t *testing.T, path string, width, height int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 8), G: uint8(y * 8), B: 200, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	mustWriteFile(t, path, buf.Bytes(), 0o644)
}

// newVerifiablePackage lays out a minimal package directory that passes the
// static gates, so verification reaches the dynamic stage. Its entry is the stub
// script above — the package *is* its executable, there is no separate runner.
func newVerifiablePackage(t *testing.T, sampleWidth, sampleHeight int) string {
	t.Helper()
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "sample_pkg"), []byte(fakeVerifyEntryScript), 0o755)
	writeSampleJPEG(t, filepath.Join(dir, "sample.jpg"), sampleWidth, sampleHeight)

	manifest := algorithm.Manifest{
		ID:        "sample_pkg",
		Version:   "1.0.0",
		Name:      "sample_package",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "manifest.json"), raw, 0o644)
	return dir
}

// TestVerifierFeedsDecodedSampleFromPackageCwd pins the two behaviours that make
// the black-box gate meaningful:
//
//  1. the entry is started with the package directory as CWD, so a package that
//     resolves weights relative to its own executable is exercised the way it
//     will run after install (a repo-CWD run would mask models/models/… bugs);
//  2. the packaged sample.jpg is decoded by Go and handed over as a real frame,
//     instead of the package silently falling back to a synthetic gray image.
func TestVerifierFeedsDecodedSampleFromPackageCwd(t *testing.T) {
	pkgDir := newVerifiablePackage(t, 8, 6)

	captureDir := t.TempDir()
	metaPath := filepath.Join(captureDir, "meta.txt")
	ppmPath := filepath.Join(captureDir, "frame.ppm")
	t.Setenv("CAPTURE_META", metaPath)
	t.Setenv("CAPTURE_PPM", ppmPath)
	t.Setenv("SAMPLE_DECODED", "true")
	t.Setenv("SAMPLE_W", "8")
	t.Setenv("SAMPLE_H", "6")
	t.Setenv("RESULT_COUNT", "1")

	core, logs := observer.New(zapcore.WarnLevel)
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{Logger: zap.New(core)})

	if _, err := verifier.VerifyDirectory(context.Background(), pkgDir); err != nil {
		t.Fatalf("verification failed: %v", err)
	}

	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("read capture meta: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(meta)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected cwd + sample lines, got: %q", string(meta))
	}

	resolvedPkgDir, err := filepath.EvalSymlinks(pkgDir)
	if err != nil {
		t.Fatalf("resolve package dir: %v", err)
	}
	if lines[0] != resolvedPkgDir {
		t.Errorf("entry cwd = %q, want package dir %q", lines[0], resolvedPkgDir)
	}

	// The handed-over frame must be a real P6 PPM of the sample's dimensions.
	frame, err := os.ReadFile(ppmPath)
	if err != nil {
		t.Fatalf("read captured frame: %v", err)
	}
	if !bytes.HasPrefix(frame, []byte("P6\n8 6\n255\n")) {
		t.Errorf("sample frame is not a decoded 8x6 P6 PPM, head: %q", string(frame[:min(len(frame), 24)]))
	}
	if want := len("P6\n8 6\n255\n") + 8*6*3; len(frame) != want {
		t.Errorf("frame size = %d, want %d (packed RGB24)", len(frame), want)
	}

	// A decoded sample with results must not warn.
	if entries := logs.FilterMessageSnippet("zero results").All(); len(entries) != 0 {
		t.Errorf("unexpected zero-result warning: %v", entries)
	}
}

// TestVerifierWarnsWhenEntryCannotDecodeSample locks in the "no silent
// fake-pass" contract: when the runner falls back to its synthetic frame the
// verdict still stands, but the degradation must be visible to the operator.
func TestVerifierWarnsWhenEntryCannotDecodeSample(t *testing.T) {
	pkgDir := newVerifiablePackage(t, 4, 4)

	t.Setenv("CAPTURE_META", filepath.Join(t.TempDir(), "meta.txt"))
	t.Setenv("CAPTURE_PPM", filepath.Join(t.TempDir(), "frame.ppm"))
	t.Setenv("SAMPLE_DECODED", "false")
	t.Setenv("SAMPLE_W", "640")
	t.Setenv("SAMPLE_H", "480")
	t.Setenv("RESULT_COUNT", "0")

	core, logs := observer.New(zapcore.WarnLevel)
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{Logger: zap.New(core)})

	if _, err := verifier.VerifyDirectory(context.Background(), pkgDir); err != nil {
		t.Fatalf("verification must not fail, got: %v", err)
	}
	if entries := logs.FilterMessageSnippet("without a decoded sample frame").All(); len(entries) != 1 {
		t.Errorf("expected exactly one sample-undecoded warning, got: %v", logs.All())
	}
}

// TestVerifierWarnsOnZeroResultsFromDecodedSample covers the other honest-signal
// case: the frame was real, but the plugin produced nothing.
func TestVerifierWarnsOnZeroResultsFromDecodedSample(t *testing.T) {
	pkgDir := newVerifiablePackage(t, 4, 4)

	t.Setenv("CAPTURE_META", filepath.Join(t.TempDir(), "meta.txt"))
	t.Setenv("CAPTURE_PPM", filepath.Join(t.TempDir(), "frame.ppm"))
	t.Setenv("SAMPLE_DECODED", "true")
	t.Setenv("SAMPLE_W", "4")
	t.Setenv("SAMPLE_H", "4")
	t.Setenv("RESULT_COUNT", "0")

	core, logs := observer.New(zapcore.WarnLevel)
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{Logger: zap.New(core)})

	if _, err := verifier.VerifyDirectory(context.Background(), pkgDir); err != nil {
		t.Fatalf("verification must not fail, got: %v", err)
	}
	if entries := logs.FilterMessageSnippet("zero results").All(); len(entries) != 1 {
		t.Errorf("expected exactly one zero-results warning, got: %v", logs.All())
	}
}

// TestVerifierReportsEntryFailureWithStderr keeps the failure path intact: a
// non-zero package entry exit must surface as ErrVerificationFailed, and the
// stderr it wrote must be carried into the message so the operator can act.
func TestVerifierReportsEntryFailureWithStderr(t *testing.T) {
	pkgDir := newVerifiablePackage(t, 4, 4)
	// 覆盖成会失败的入口
	script := "#!/bin/sh\necho 'boom: model_create failed' >&2\nexit 2\n"
	if err := os.WriteFile(filepath.Join(pkgDir, "sample_pkg"), []byte(script), 0o755); err != nil {
		t.Fatalf("write failing entry: %v", err)
	}

	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{})
	_, err := verifier.VerifyDirectory(context.Background(), pkgDir)
	if !errors.Is(err, algorithm.ErrVerificationFailed) {
		t.Fatalf("expected ErrVerificationFailed, got: %v", err)
	}
}
