package algorithm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

func findFaceRecognitionArchive(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/packages/face_recognition-x86_64-cpu.tar.gz",
		"build/packages/face_recognition-x86_64-cpu.tar.gz",
		"../../build/packages/face_recognition.tar.gz",
		"build/packages/face_recognition.tar.gz",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}

	// 若未找到，尝试调用 build_package.py 自动组装
	scriptCandidates := []string{
		"../../packages/face_recognition/scripts/build_package.py",
		"packages/face_recognition/scripts/build_package.py",
	}
	for _, s := range scriptCandidates {
		absScript, err := filepath.Abs(s)
		if err == nil {
			if fi, err := os.Stat(absScript); err == nil && !fi.IsDir() {
				cmd := exec.Command("python3", absScript, "--target", "cpu")
				cmd.Dir = filepath.Dir(filepath.Dir(filepath.Dir(absScript)))
				if out, err := cmd.CombinedOutput(); err == nil {
					for _, c := range candidates {
						if abs, err := filepath.Abs(c); err == nil {
							if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
								return abs
							}
						}
					}
				} else {
					t.Logf("build_package.py failed: %s", string(out))
				}
			}
		}
	}

	t.Skip("face_recognition.tar.gz not found and could not be built, skipping package integration test")
	return ""
}

func TestFaceRecognitionPackageVerification(t *testing.T) {
	runnerPath := findRunnerBinary(t)
	archivePath := findFaceRecognitionArchive(t)

	verifier := algorithm.NewVerifier(runnerPath, "cpu")
	verifier.HostArch = "x86_64"
	verifier.HostTarget = "cpu"
	verifier.Timeout = 10 * time.Second

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	installDir := t.TempDir()
	installed, err := verifier.StageAndVerify(ctx, archivePath, installDir)
	if err != nil {
		t.Fatalf("StageAndVerify face_recognition package failed: %v", err)
	}

	if installed.ID != "face_recognition" {
		t.Errorf("expected ID 'face_recognition', got: %s", installed.ID)
	}
	if installed.Name != "人脸识别算法" {
		t.Errorf("expected Name '人脸识别算法', got: %s", installed.Name)
	}
	if installed.TaskType != algorithm.TaskTypePersonStructure {
		t.Errorf("expected TaskType %s, got: %s", algorithm.TaskTypePersonStructure, installed.TaskType)
	}
	if installed.Category != algorithm.CategorySafety {
		t.Errorf("expected Category %s, got: %s", algorithm.CategorySafety, installed.Category)
	}
	if installed.ComputeCost != 25 {
		t.Errorf("expected ComputeCost 25, got: %d", installed.ComputeCost)
	}
	if installed.MemoryMB != 128 {
		t.Errorf("expected MemoryMB 128, got: %d", installed.MemoryMB)
	}
	if installed.TargetArch != "x86_64" {
		t.Errorf("expected TargetArch 'x86_64', got: %s", installed.TargetArch)
	}
	if installed.TargetDevice != "cpu" {
		t.Errorf("expected TargetDevice 'cpu', got: %s", installed.TargetDevice)
	}

	// 验证解压产物规范与可执行权限
	pkgDir := installed.InstallDir(installDir)
	pluginPath := filepath.Join(pkgDir, "plugin.so")
	fi, err := os.Stat(pluginPath)
	if err != nil {
		t.Fatalf("stat plugin.so: %v", err)
	}
	if fi.Mode().Perm()&0111 == 0 {
		t.Errorf("expected plugin.so to have executable permissions, got: %o", fi.Mode().Perm())
	}

	samplePath := filepath.Join(pkgDir, "sample.jpg")
	if _, err := os.Stat(samplePath); err != nil {
		t.Errorf("sample.jpg not found in installed package: %v", err)
	}

	schemaPath := filepath.Join(pkgDir, "config.schema.json")
	if _, err := os.Stat(schemaPath); err != nil {
		t.Errorf("config.schema.json not found in installed package: %v", err)
	}
}
