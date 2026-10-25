package algorithm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

// findPlateRecognitionArchive locates (or builds) the packaged plate algorithm,
// mirroring findFaceRecognitionArchive. Building requires a host with real
// inference (onnx/rknn): the test_stub fixture backend rejects real models by design,
// so the packaging gate fails there and this test skips.
func findPlateRecognitionArchive(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/packages/plate_recognition-x86_64-cpu.tar.gz",
		"build/packages/plate_recognition-x86_64-cpu.tar.gz",
		"../../build/packages/plate_recognition.tar.gz",
		"build/packages/plate_recognition.tar.gz",
	}
	find := func() string {
		for _, candidate := range candidates {
			abs, err := filepath.Abs(candidate)
			if err != nil {
				continue
			}
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				return abs
			}
		}
		return ""
	}
	if archive := find(); archive != "" {
		return archive
	}

	for _, script := range []string{
		"../../packages/plate_recognition/scripts/build_package.py",
		"packages/plate_recognition/scripts/build_package.py",
	} {
		absScript, err := filepath.Abs(script)
		if err != nil {
			continue
		}
		if _, err := os.Stat(absScript); err != nil {
			continue
		}
		cmd := exec.Command("python3", absScript, "--target", "cpu")
		cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(absScript)))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("build_package.py failed (expected on a test_stub host build): %s", string(out))
			continue
		}
		if archive := find(); archive != "" {
			return archive
		}
	}

	t.Skip("plate_recognition package not found and could not be built, skipping package integration test")
	return ""
}

// TestPlateRecognitionPackageInstall is the end-to-end admission gate for the
// plate package: build → extract → static contract → dynamic black-box
// verification (real sample frame, package directory as CWD) → atomic install.
//
// It is the regression that would have caught the historical `models/models/...`
// path bug, which passed whenever the verifier happened to run from the repo root.
func TestPlateRecognitionPackageInstall(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping package build in short mode")
	}
	runnerPath := findRunnerBinary(t)
	archivePath := findPlateRecognitionArchive(t)

	installDir := t.TempDir()
	verifier := algorithm.NewVerifier(runnerPath, x86Host())

	ctx, cancel := context.WithTimeout(context.Background(), 60*1e9)
	defer cancel()

	installed, err := verifier.StageAndVerify(ctx, archivePath, installDir)
	if err != nil {
		t.Fatalf("StageAndVerify plate_recognition package failed: %v", err)
	}

	if installed.ID != "plate_recognition" {
		t.Errorf("expected ID plate_recognition, got: %s", installed.ID)
	}
	if installed.TaskType != algorithm.TaskTypeVehicleStructure {
		t.Errorf("expected task_type %s, got: %s", algorithm.TaskTypeVehicleStructure, installed.TaskType)
	}
	if installed.Category != algorithm.CategoryTraffic {
		t.Errorf("expected Category %s, got: %s", algorithm.CategoryTraffic, installed.Category)
	}

	// 安装产物：plugin.so 可执行、模型与样本随包落地
	rt := algorunner.Runtime{AlgorithmsDir: installDir}
	pkgDir := rt.PackageDir(installed.ID, installed.Version)
	pluginPath := rt.PluginPath(installed.ID, installed.Version)
	info, err := os.Stat(pluginPath)
	if err != nil {
		t.Fatalf("stat plugin.so: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("expected plugin.so to have executable permissions, got: %o", info.Mode().Perm())
	}
	for _, name := range []string{
		"sample.jpg",
		"config.schema.json",
		"models/plate_detect.onnx",
		"models/plate_rec_color.onnx",
	} {
		if _, err := os.Stat(filepath.Join(pkgDir, name)); err != nil {
			t.Errorf("installed package missing %s: %v", name, err)
		}
	}
}
