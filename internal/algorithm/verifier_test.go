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
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

// x86Host is the platform the fixture manifests declare, pinned so the result
// does not depend on the machine running the tests.
func x86Host() algorithm.HostPlatform {
	return algorithm.HostPlatform{Arch: "x86_64", Target: "onnx"}
}

func TestVerifierMissingConventionFiles(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	ctx := context.Background()
	dir := t.TempDir()

	// 1. Missing manifest
	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrMissingManifestFile) {
		t.Errorf("expected ErrMissingManifestFile, got: %v", err)
	}

	// 2. 合法 manifest 但缺包根可执行文件（入口名 = manifest.id）
	manifestBytes, _ := json.Marshal(algorithm.Manifest{
		ID: "sample_pkg", Version: "1.0.0", Name: "sample_pkg",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	})
	mustWriteFile(t, filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)

	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrMissingSampleFile) {
		t.Errorf("expected ErrMissingSampleFile, got: %v", err)
	}

	// 3. 有样本但缺包根可执行文件（入口名 = manifest.id）
	mustWriteFile(t, filepath.Join(dir, "sample.jpg"), []byte(`fake`), 0o644)
	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorunner.ErrMissingEntry) {
		t.Errorf("expected algorunner.ErrMissingEntry, got: %v", err)
	}

	// 4. 旧形态 plugin.so 不得被当成入口
	mustWriteFile(t, filepath.Join(dir, "plugin.so"), []byte(`fake`), 0o755)
	_, _, _, err = verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorunner.ErrLegacyEntry) {
		t.Errorf("expected algorunner.ErrLegacyEntry, got: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "plugin.so")); err != nil {
		t.Fatalf("remove legacy entry: %v", err)
	}

	// 5. 补上入口即通过静态检查
	mustWriteFile(t, filepath.Join(dir, "sample_pkg"), []byte(verifyStubScript(1)), 0o755)
	if _, _, _, err := verifier.VerifyDirectory(ctx, dir); err != nil {
		t.Errorf("expected the package to verify, got: %v", err)
	}
}

func TestVerifierPlatformMismatch(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	ctx := context.Background()

	dir := t.TempDir()
	seedSampleAndJunk(t, dir)

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
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)

	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrPlatformMismatch) {
		t.Errorf("expected ErrPlatformMismatch, got: %v", err)
	}
}

// TestVerifierRejectsRetiredCPUTarget proves the "cpu" target is rejected with a
// dedicated sentinel instead of being silently mapped onto "onnx". A cpu-built
// package carries a different runtime and asset layout, so mapping it would only
// move the failure to an opaque loader/exec error much later.
func TestVerifierRejectsRetiredCPUTarget(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	ctx := context.Background()

	dir := t.TempDir()
	seedSampleAndJunk(t, dir)

	manifest := algorithm.Manifest{
		ID:        "test_pkg",
		Version:   "1.0.0",
		Name:      "test_package",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)

	_, _, _, err := verifier.VerifyDirectory(ctx, dir)
	if !errors.Is(err, algorithm.ErrPlatformTargetRetired) {
		t.Fatalf("expected ErrPlatformTargetRetired, got: %v", err)
	}
	if errors.Is(err, algorithm.ErrPlatformMismatch) {
		t.Errorf("retired target must not be reported as a plain platform mismatch")
	}
	if !strings.Contains(err.Error(), "onnx") {
		t.Errorf("error should point at the onnx target, got: %v", err)
	}
}

func TestVerifierInvalidComputeBudget(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	ctx := context.Background()

	dir := t.TempDir()
	seedSampleAndJunk(t, dir)

	// compute_cost > 1000
	manifest := algorithm.Manifest{
		ID:        "test_pkg",
		Version:   "1.0.0",
		Name:      "test_package",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 1500, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)
	_ = os.WriteFile(filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)

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
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
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
			seedSampleAndJunk(t, dir)

			manifest := algorithm.Manifest{
				ID:        tc.id,
				Version:   tc.version,
				Name:      tc.pkgName,
				TaskType:  algorithm.TaskTypeDetection,
				Category:  algorithm.CategorySafety,
				Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
				Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
			}
			manifestBytes, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			mustWriteFile(t, filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)
			if !tc.expectErr {
				seedEntry(t, dir, tc.id)
			}

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
		seedSampleAndJunk(t, dir)
		manifestBytes, err := json.Marshal(algorithm.Manifest{
			Version:   "1.0.0",
			Name:      "pkg",
			TaskType:  algorithm.TaskTypeDetection,
			Category:  algorithm.CategorySafety,
			Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
			Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
		})
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		mustWriteFile(t, filepath.Join(dir, "manifest.json"), manifestBytes, 0o644)
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
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json": manifestBytes,
		"escaped_pkg":   []byte(verifyStubScript(1)),
		"sample.jpg":    []byte("fake"),
	})
	defer os.Remove(archivePath)

	// installRoot/../sibling is the escape target we must never touch.
	parent := t.TempDir()
	installRoot := filepath.Join(parent, "algorithms")
	if err := os.MkdirAll(installRoot, 0o755); err != nil {
		t.Fatalf("mkdir install root: %v", err)
	}
	sibling := filepath.Join(parent, "tmp")

	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})

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

// TestVerifierDynamicVerificationAndResultTypeMismatch pins the one cross-check
// the dynamic stage exists for: a package whose verdict contradicts its manifest
// must be rejected, no matter how healthy its process is.
//
// The packages here are stub entries rather than real models — that is the point
// of the black-box stage, it only reads the report the entry prints.
func TestVerifierDynamicVerificationAndResultTypeMismatch(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host())
	ctx := context.Background()

	writePackage := func(t *testing.T, manifest algorithm.Manifest, entryScript string) string {
		t.Helper()
		dir := t.TempDir()
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatalf("marshal manifest: %v", err)
		}
		mustWriteFile(t, filepath.Join(dir, "manifest.json"), raw, 0o644)
		mustWriteFile(t, filepath.Join(dir, "sample.jpg"), []byte("sample"), 0o644)
		// 入口名必须等于 manifest.id
		mustWriteFile(t, filepath.Join(dir, manifest.ID), []byte(entryScript), 0o755)
		return dir
	}

	detectionManifest := algorithm.Manifest{
		ID: "mismatch_pkg", Version: "1.0.0", Name: "mismatch_package",
		TaskType:  algorithm.TaskTypeClassification, // 与包内报告的 result_type 1 冲突
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	dir := writePackage(t, detectionManifest, verifyStubScript(1))
	if _, _, _, err := verifier.VerifyDirectory(ctx, dir); !errors.Is(err, algorithm.ErrResultTypeMismatch) {
		t.Errorf("expected ErrResultTypeMismatch, got: %v", err)
	}

	personManifest := detectionManifest
	personManifest.ID = "mismatch_person_pkg"
	personManifest.Name = "mismatch_person_package"
	personManifest.TaskType = algorithm.TaskTypePersonStructure // 期望 3，包报告 1
	personDir := writePackage(t, personManifest, verifyStubScript(1))
	if _, _, _, err := verifier.VerifyDirectory(ctx, personDir); !errors.Is(err, algorithm.ErrResultTypeMismatch) {
		t.Errorf("expected ErrResultTypeMismatch for person_structure vs detector, got: %v", err)
	}

	validManifest := detectionManifest
	validManifest.ID = "valid_detector"
	validManifest.Name = "valid_detector"
	validManifest.TaskType = algorithm.TaskTypeDetection
	validDir := writePackage(t, validManifest, verifyStubScript(1))
	parsedManifest, _, _, err := verifier.VerifyDirectory(ctx, validDir)
	if err != nil {
		t.Fatalf("VerifyDirectory for a matching detector failed: %v", err)
	}
	if parsedManifest.ID != "valid_detector" {
		t.Errorf("expected manifest ID valid_detector, got: %s", parsedManifest.ID)
	}

	validPersonManifest := detectionManifest
	validPersonManifest.ID = "valid_person_plugin"
	validPersonManifest.Name = "valid_person_plugin"
	validPersonManifest.TaskType = algorithm.TaskTypePersonStructure
	validPersonManifest.Resources = algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 128}
	validPersonDir := writePackage(t, validPersonManifest, verifyStubScript(3))
	parsedPersonManifest, _, _, err := verifier.VerifyDirectory(ctx, validPersonDir)
	if err != nil {
		t.Fatalf("VerifyDirectory for a matching person_structure package failed: %v", err)
	}
	if parsedPersonManifest.ID != "valid_person_plugin" {
		t.Errorf("expected manifest ID valid_person_plugin, got: %s", parsedPersonManifest.ID)
	}
}

func TestStageAndVerifyEndToEnd(t *testing.T) {
	manifest := algorithm.Manifest{
		ID:        "installed_detector",
		Version:   "1.0.0",
		Name:      "installed_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 25, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)

	// Create archive
	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json":      manifestBytes,
		"installed_detector": []byte(verifyStubScript(1)),
		"sample.jpg":         []byte("sample image bytes"),
		"config.schema.json": []byte(`{"type":"object"}`),
		".env":               []byte("DEBUG=1"),
	})
	defer os.Remove(archivePath)

	installBaseDir := t.TempDir()
	verifier := algorithm.NewVerifier(x86Host())

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
	if _, err := os.Stat(filepath.Join(expectedDir, "installed_detector")); err != nil {
		t.Errorf("installed package entry not found: %v", err)
	}
	if _, err := os.Stat(filepath.Join(expectedDir, "sample.jpg")); err != nil {
		t.Errorf("installed sample.jpg not found: %v", err)
	}
}

func TestVerifierRejectsInvalidConfigSchema(t *testing.T) {
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	dir := t.TempDir()
	seedSampleAndJunk(t, dir)
	mustWriteFile(t, filepath.Join(dir, "manifest.json"), []byte(`{"id":"schema_pkg","version":"1.0.0","name":"schema_pkg","task_type":"detection","category":"safety","platform":{"arch":"x86_64","target":"onnx"},"resources":{"compute_cost":20,"memory_mb":64}}`), 0o644)
	seedEntry(t, dir, "schema_pkg")
	mustWriteFile(t, filepath.Join(dir, "config.schema.json"), []byte(`{"type":17}`), 0o644)

	_, _, _, err := verifier.VerifyDirectory(context.Background(), dir)
	if !errors.Is(err, algorithm.ErrInvalidConfigSchema) {
		t.Fatalf("expected invalid config schema error, got %v", err)
	}
}

func TestStageAndVerifyDoesNotReplaceExistingVersion(t *testing.T) {
	manifest := algorithm.Manifest{
		ID: "same_version_pkg", Version: "1.0.0", Name: "same_version_pkg",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 20, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	archive := func(plugin []byte) string {
		t.Helper()
		path := createTestTarGz(t, map[string][]byte{
			"manifest.json":    manifestBytes,
			"same_version_pkg": plugin,
			"sample.jpg":       []byte("sample"),
		})
		t.Cleanup(func() {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				t.Errorf("remove test archive: %v", err)
			}
		})
		return path
	}

	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{SkipDynamic: true})
	installDir := t.TempDir()
	installed, err := verifier.StageAndVerify(context.Background(), archive([]byte(verifyStubScript(1))), installDir)
	if err != nil {
		t.Fatalf("install initial version: %v", err)
	}
	entryPath := algorunner.Runtime{AlgorithmsDir: installDir}.EntryPath(installed.ID, installed.Version)

	_, err = verifier.StageAndVerify(context.Background(), archive([]byte("#!/bin/sh\nexit 0\n")), installDir)
	if !errors.Is(err, algorithm.ErrVersionAlreadyInstalled) {
		t.Fatalf("expected existing-version conflict, got %v", err)
	}
	got, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("read installed entry: %v", err)
	}
	if string(got) != verifyStubScript(1) {
		t.Fatalf("existing version was overwritten: got %q", got)
	}
}
