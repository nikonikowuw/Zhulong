package algorithm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

func findFaceRecognitionArchive(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/packages/face_recognition-x86_64-onnx.tar.gz",
		"build/packages/face_recognition-x86_64-onnx.tar.gz",
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
				cmd := exec.Command("python3", absScript, "--target", "onnx")
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
	archivePath := findFaceRecognitionArchive(t)

	// The probe loads the ONNX face model, so its runtime is dominated by model init
	// and by how many sibling packages are competing for the CPU. A 10s budget made
	// this test a coin flip under `go test -race ./...` (it measures ~10s even alone),
	// which is a property of the test harness, not of the package being verified.
	verifier := algorithm.NewVerifier(x86Host(), algorithm.VerifyOptions{Timeout: 60 * time.Second})

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
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
	if installed.ComputeCost != 35 {
		t.Errorf("expected ComputeCost 35, got: %d", installed.ComputeCost)
	}
	if installed.MemoryMB != 160 {
		t.Errorf("expected MemoryMB 160, got: %d", installed.MemoryMB)
	}
	if installed.TargetArch != "x86_64" {
		t.Errorf("expected TargetArch 'x86_64', got: %s", installed.TargetArch)
	}
	if installed.TargetDevice != "onnx" {
		t.Errorf("expected TargetDevice 'onnx', got: %s", installed.TargetDevice)
	}

	// 验证解压产物规范与可执行权限
	rt := algorunner.Runtime{AlgorithmsDir: installDir}
	pkgDir := rt.PackageDir(installed.ID, installed.Version)
	entryPath := rt.EntryPath(installed.ID, installed.Version)
	fi, err := os.Stat(entryPath)
	if err != nil {
		t.Fatalf("stat package entry: %v", err)
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("expected the package entry to be executable, got: %o", fi.Mode().Perm())
	}
	// 旧形态不得随包安装：两种入口并存意味着有代码没人跑。
	if _, statErr := os.Stat(filepath.Join(pkgDir, algorunner.LegacyEntryFileName)); statErr == nil {
		t.Errorf("installed package still ships %s", algorunner.LegacyEntryFileName)
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
