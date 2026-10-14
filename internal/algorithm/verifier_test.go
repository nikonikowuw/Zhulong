package algorithm_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

func findRunnerBinary(t *testing.T) string {
	t.Helper()
	// Check standard build locations
	candidates := []string{
		"../../build/native/host/zhulong_algo_runner",
		"build/native/host/zhulong_algo_runner",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	t.Skip("zhulong_algo_runner not found, skipping dynamic runner test")
	return ""
}

func findMockDetectorPlugin(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/native/host/libMockDetector.so",
		"build/native/host/libMockDetector.so",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	t.Skip("libMockDetector.so not found, skipping dynamic runner test")
	return ""
}

func findMockPersonStructurePlugin(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/native/host/libMockPersonStructure.so",
		"build/native/host/libMockPersonStructure.so",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	t.Skip("libMockPersonStructure.so not found, skipping dynamic runner test")
	return ""
}

func TestVerifierMissingConventionFiles(t *testing.T) {
	verifier := algorithm.NewVerifier("", "cpu")
	ctx := context.Background()
	dir := t.TempDir()

	// 1. Missing manifest
	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrMissingManifestFile) {
		t.Errorf("expected ErrMissingManifestFile, got: %v", err)
	}

	// Add manifest
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(`{}`), 0644)

	// 2. Missing plugin.so
	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrMissingPluginFile) {
		t.Errorf("expected ErrMissingPluginFile, got: %v", err)
	}

	// Add plugin.so
	_ = os.WriteFile(filepath.Join(dir, "plugin.so"), []byte(`fake`), 0755)

	// 3. Missing sample.jpg
	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrMissingSampleFile) {
		t.Errorf("expected ErrMissingSampleFile, got: %v", err)
	}
}

func TestVerifierPlatformMismatch(t *testing.T) {
	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"
	ctx := context.Background()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "plugin.so"), []byte(`fake`), 0755)
	_ = os.WriteFile(filepath.Join(dir, "sample.jpg"), []byte(`fake`), 0644)

	manifest := algorithm.Manifest{
		ID:        "test_pkg",
		Version:   "1.0.0",
		Name:      "test_package",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "aarch64", Target: "rk3568"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0644)

	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrPlatformMismatch) {
		t.Errorf("expected ErrPlatformMismatch, got: %v", err)
	}
}

func TestVerifierInvalidComputeBudget(t *testing.T) {
	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"
	ctx := context.Background()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "plugin.so"), []byte(`fake`), 0755)
	_ = os.WriteFile(filepath.Join(dir, "sample.jpg"), []byte(`fake`), 0644)

	// compute_cost > 1000
	manifest := algorithm.Manifest{
		ID:        "test_pkg",
		Version:   "1.0.0",
		Name:      "test_package",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 1500, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0644)

	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrInvalidComputeBudget) {
		t.Errorf("expected ErrInvalidComputeBudget, got: %v", err)
	}
}

// mustWriteFile writes data to path, failing the test on error. Used by tests
// added in this task to avoid introducing new `_ = fn()` violations.
func mustWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestVerifierManifestTokenCharset(t *testing.T) {
	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"
	ctx := context.Background()

	// Each case supplies the id/version/name triple. Malicious or malformed
	// tokens must be rejected with ErrInvalidManifestToken; safe tokens pass.
	cases := []struct {
		name      string
		id        string
		version   string
		pkgName   string
		expectErr bool
	}{
		// --- rejected ---
		{"dot-dot-slash escape", "../../../tmp/pwned", "1.0.0", "pkg", true},
		{"bare dot dot", "..", "1.0.0", "pkg", true},
		{"single dot", ".", "1.0.0", "pkg", true},
		{"absolute path", "/abs/path", "1.0.0", "pkg", true},
		{"forward slash", "a/b", "1.0.0", "pkg", true},
		{"backslash", `a\b`, "1.0.0", "pkg", true},
		{"leading dash", "-leading-dash", "1.0.0", "pkg", true},
		{"overlong 65 chars", strings.Repeat("a", 65), "1.0.0", "pkg", true},
		{"space in id", "a b", "1.0.0", "pkg", true},
		{"unicode in id", "检测", "1.0.0", "pkg", true},
		{"bad version", "pkg", "../../etc", "pkg", true},
		// Name is NOT charset-restricted (display field) but IS byte-bounded
		// because it reaches HandshakeReq.client_name[32].
		{"name over 32 bytes", "pkg", "1.0.0", strings.Repeat("n", 33), true},
		{"name multi-byte over 32 bytes", "pkg", "1.0.0", strings.Repeat("检", 11), true},
		// --- accepted ---
		{"valid id", "yolov8-person-v2", "1.0.0", "yolov8_person", false},
		{"semver with prerelease", "pkg", "v1.2.3-rc.1", "pkg", false},
		{"single char id", "a", "1.0.0", "a", false},
		// A human-readable display name must pass: no charset restriction.
		{"human readable name with space", "pkg", "1.0.0", "Helmet Detector", false},
		{"unicode display name", "pkg", "1.0.0", "安全帽佩戴检测算法", false},
		{"name exactly 32 bytes", "pkg", "1.0.0", strings.Repeat("n", 32), false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			mustWriteFile(t, filepath.Join(dir, "plugin.so"), []byte("fake"), 0755)
			mustWriteFile(t, filepath.Join(dir, "sample.jpg"), []byte("fake"), 0644)

			manifest := algorithm.Manifest{
				ID:        tc.id,
				Version:   tc.version,
				Name:      tc.pkgName,
				TaskType:  algorithm.TaskTypeDetection,
				Category:  algorithm.CategorySafety,
				Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
				Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
			}
			manifestBytes, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			mustWriteFile(t, filepath.Join(dir, "manifest.json"), manifestBytes, 0644)

			_, _, _, err = verifier.VerifyDirectory(ctx, dir)
			if tc.expectErr {
				if !errors.Is(err, algorithm.ErrInvalidManifestToken) {
					t.Errorf("expected ErrInvalidManifestToken for %q, got: %v", tc.id, err)
				}
				return
			}
			if err != nil {
				t.Errorf("expected valid manifest %q to pass, got: %v", tc.id, err)
			}
		})
	}

	// An empty id is rejected earlier by the required-fields check, not by the
	// charset whitelist. Assert that it is still rejected (defense in depth).
	t.Run("empty id rejected", func(t *testing.T) {
		dir := t.TempDir()
		mustWriteFile(t, filepath.Join(dir, "plugin.so"), []byte("fake"), 0755)
		mustWriteFile(t, filepath.Join(dir, "sample.jpg"), []byte("fake"), 0644)
		manifestBytes, err := json.Marshal(algorithm.Manifest{
			Version:   "1.0.0",
			Name:      "pkg",
			TaskType:  algorithm.TaskTypeDetection,
			Category:  algorithm.CategorySafety,
			Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
			Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
		})
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		mustWriteFile(t, filepath.Join(dir, "manifest.json"), manifestBytes, 0644)
		if _, _, _, err := verifier.VerifyDirectory(ctx, dir); err == nil {
			t.Errorf("expected empty id to be rejected")
		}
	})
}

// TestStageAndVerifyRejectsPathEscape proves that a malicious manifest id cannot
// create any directory outside finalInstallDir. filepath.Join escape is real:
// filepath.Join("/data/algorithms", "../../../tmp/pwned", "1.0.0") -> /tmp/pwned/1.0.0
func TestStageAndVerifyRejectsPathEscape(t *testing.T) {
	manifest := algorithm.Manifest{
		ID:        "../../../tmp/pwned",
		Version:   "1.0.0",
		Name:      "pwned",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json": manifestBytes,
		"plugin.so":     []byte("fake"),
		"sample.jpg":    []byte("fake"),
	})
	defer os.Remove(archivePath)

	// installRoot/../sibling is the escape target we must never touch.
	parent := t.TempDir()
	installRoot := filepath.Join(parent, "algorithms")
	if err := os.MkdirAll(installRoot, 0755); err != nil {
		t.Fatalf("mkdir install root: %v", err)
	}
	sibling := filepath.Join(parent, "tmp")

	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"

	_, err = verifier.StageAndVerify(context.Background(), archivePath, installRoot)
	if !errors.Is(err, algorithm.ErrInvalidManifestToken) {
		t.Fatalf("expected ErrInvalidManifestToken, got: %v", err)
	}

	if _, err := os.Stat(sibling); !os.IsNotExist(err) {
		t.Errorf("escape target %q must not exist, stat err: %v", sibling, err)
	}

	// The install root must still exist and remain empty.
	entries, err := os.ReadDir(installRoot)
	if err != nil {
		t.Fatalf("read install root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("install root must be empty after rejected escape, found: %v", entries)
	}
}

func TestVerifierDynamicVerificationAndResultTypeMismatch(t *testing.T) {
	runnerPath := findRunnerBinary(t)
	mockPluginPath := findMockDetectorPlugin(t)

	mockPluginBytes, err := os.ReadFile(mockPluginPath)
	if err != nil {
		t.Fatalf("read mock plugin: %v", err)
	}

	verifier := algorithm.NewVerifier(runnerPath, "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"
	ctx := context.Background()

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "plugin.so"), mockPluginBytes, 0755)
	_ = os.WriteFile(filepath.Join(dir, "sample.jpg"), []byte("sample"), 0644)

	// 1. Result type mismatch: detector plugin, but manifest says classification!
	mismatchManifest := algorithm.Manifest{
		ID:        "mismatch_pkg",
		Version:   "1.0.0",
		Name:      "mismatch_package",
		TaskType:  algorithm.TaskTypeClassification, // Mismatch!
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	mismatchBytes, _ := json.Marshal(mismatchManifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), mismatchBytes, 0644)

	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrResultTypeMismatch) {
		t.Errorf("expected ErrResultTypeMismatch, got: %v", err)
	}

	// 2. Result type mismatch: detector plugin (result_type 1), but manifest says person_structure (expects 3)!
	mismatchPersonManifest := algorithm.Manifest{
		ID:        "mismatch_person_pkg",
		Version:   "1.0.0",
		Name:      "mismatch_person_package",
		TaskType:  algorithm.TaskTypePersonStructure, // Mismatch with detector!
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	mismatchPersonBytes, _ := json.Marshal(mismatchPersonManifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), mismatchPersonBytes, 0644)

	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrResultTypeMismatch) {
		t.Errorf("expected ErrResultTypeMismatch for person_structure vs detector, got: %v", err)
	}

	// 3. Matching manifest: detection!
	validManifest := algorithm.Manifest{
		ID:        "valid_detector",
		Version:   "1.0.0",
		Name:      "valid_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	validBytes, _ := json.Marshal(validManifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), validBytes, 0644)

	parsedManifest, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if err != nil {
		t.Fatalf("VerifyDirectory with real runner failed: %v", err)
	}
	if parsedManifest.ID != "valid_detector" {
		t.Errorf("expected manifest ID valid_detector, got: %s", parsedManifest.ID)
	}

	// 4. Matching manifest: person_structure!
	mockPersonPluginPath := findMockPersonStructurePlugin(t)
	mockPersonPluginBytes, err := os.ReadFile(mockPersonPluginPath)
	if err != nil {
		t.Fatalf("read mock person plugin: %v", err)
	}

	personDir := t.TempDir()
	_ = os.WriteFile(filepath.Join(personDir, "plugin.so"), mockPersonPluginBytes, 0755)
	_ = os.WriteFile(filepath.Join(personDir, "sample.jpg"), []byte("sample"), 0644)

	validPersonManifest := algorithm.Manifest{
		ID:        "valid_person_plugin",
		Version:   "1.0.0",
		Name:      "valid_person_plugin",
		TaskType:  algorithm.TaskTypePersonStructure,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 128},
	}
	validPersonBytes, _ := json.Marshal(validPersonManifest)
	_ = os.WriteFile(filepath.Join(personDir, "manifest.json"), validPersonBytes, 0644)

	parsedPersonManifest, _, _, err := verifier.VerifyDirectory(ctx, personDir)
	if err != nil {
		t.Fatalf("VerifyDirectory with real runner for person_structure failed: %v", err)
	}
	if parsedPersonManifest.ID != "valid_person_plugin" {
		t.Errorf("expected manifest ID valid_person_plugin, got: %s", parsedPersonManifest.ID)
	}
}

func TestStageAndVerifyEndToEnd(t *testing.T) {
	runnerPath := findRunnerBinary(t)
	mockPluginPath := findMockDetectorPlugin(t)

	mockPluginBytes, err := os.ReadFile(mockPluginPath)
	if err != nil {
		t.Fatalf("read mock plugin: %v", err)
	}

	manifest := algorithm.Manifest{
		ID:        "installed_detector",
		Version:   "1.0.0",
		Name:      "installed_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 25, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)

	// Create archive
	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json":      manifestBytes,
		"plugin.so":          mockPluginBytes,
		"sample.jpg":         []byte("sample image bytes"),
		"config.schema.json": []byte(`{"type":"object"}`),
		".env":               []byte("DEBUG=1"),
	})
	defer os.Remove(archivePath)

	installBaseDir := t.TempDir()
	verifier := algorithm.NewVerifier(runnerPath, "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"

	pkg, err := verifier.StageAndVerify(context.Background(), archivePath, installBaseDir)
	if err != nil {
		t.Fatalf("StageAndVerify failed: %v", err)
	}

	if pkg.ID != "installed_detector" {
		t.Errorf("expected package ID 'installed_detector', got: %s", pkg.ID)
	}
	if pkg.ComputeCost != 25 {
		t.Errorf("expected compute cost 25, got: %d", pkg.ComputeCost)
	}

	// Verify installed files exist on disk
	expectedDir := filepath.Join(installBaseDir, "installed_detector", "1.0.0")
	if _, err := os.Stat(filepath.Join(expectedDir, "plugin.so")); err != nil {
		t.Errorf("installed plugin.so not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "sample.jpg")); err != nil {
		t.Errorf("installed sample.jpg not found: %v", err)
	}
}

func TestVerifierRejectsInvalidConfigSchema(t *testing.T) {
	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	dir := t.TempDir()
	mustWriteFile(t, filepath.Join(dir, "plugin.so"), []byte("fake"), 0755)
	mustWriteFile(t, filepath.Join(dir, "sample.jpg"), []byte("sample"), 0644)
	mustWriteFile(t, filepath.Join(dir, "manifest.json"), []byte(`{"id":"schema_pkg","version":"1.0.0","name":"schema_pkg","task_type":"detection","category":"safety","platform":{"arch":"x86_64","target":"cpu"},"resources":{"compute_cost":20,"memory_mb":64}}`), 0644)
	mustWriteFile(t, filepath.Join(dir, "config.schema.json"), []byte(`{"type":17}`), 0644)

	_, _, _, err := verifier.VerifyDirectory(context.Background(), dir)
	if !errors.Is(err, algorithm.ErrInvalidConfigSchema) {
		t.Fatalf("expected invalid config schema error, got %v", err)
	}
}

func TestStageAndVerifyDoesNotReplaceExistingVersion(t *testing.T) {
	manifest := algorithm.Manifest{
		ID: "same_version_pkg", Version: "1.0.0", Name: "same_version_pkg",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	archive := func(plugin []byte) string {
		t.Helper()
		path := createTestTarGz(t, map[string][]byte{
			"manifest.json": manifestBytes,
			"plugin.so":     plugin,
			"sample.jpg":    []byte("sample"),
		})
		t.Cleanup(func() {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove test archive: %v", err)
			}
		})
		return path
	}

	verifier := algorithm.NewVerifier("", "cpu")
	verifier.HostArch = "x86_64"
	installDir := t.TempDir()
	installed, err := verifier.StageAndVerify(context.Background(), archive([]byte("original")), installDir)
	if err != nil {
		t.Fatalf("install initial version: %v", err)
	}
	pluginPath := filepath.Join(installed.InstallDir(installDir), "plugin.so")

	_, err = verifier.StageAndVerify(context.Background(), archive([]byte("replacement")), installDir)
	if !errors.Is(err, algorithm.ErrVersionAlreadyInstalled) {
		t.Fatalf("expected existing-version conflict, got %v", err)
	}
	got, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read installed plugin: %v", err)
	}
	if string(got) != "original" {
		t.Fatalf("existing version was overwritten: got %q", got)
	}
}
