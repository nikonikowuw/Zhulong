package algorithm_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func createTestPNGBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 120, G: 180, B: 240, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func setupTrialTestEnvironment(t *testing.T) (*algorithm.Service, *algorithm.Store, string) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	algoDir := filepath.Join(tempDir, "algorithms")
	_ = os.MkdirAll(algoDir, 0o755)

	rt := algorunner.Runtime{AlgorithmsDir: algoDir}
	host := algorithm.HostPlatform{Arch: "x86_64", Target: "onnx"}
	comp := algorithm.NewAlgorithmComponents(dbStore, rt, host, zap.NewNop())

	return comp.Service, comp.Store, algoDir
}

func TestTrialInference_StateValidation(t *testing.T) {
	svc, store, _ := setupTrialTestEnvironment(t)
	ctx := context.Background()

	// 1. Not found
	pngBytes := createTestPNGBytes(t, 100, 100)
	_, err := svc.TrialInference(ctx, "non_existent", bytes.NewReader(pngBytes))
	if err == nil {
		t.Fatal("expected error for non-existent algorithm, got nil")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "ALGORITHM_NOT_FOUND" {
		t.Fatalf("expected ALGORITHM_NOT_FOUND, got: %v", err)
	}

	// 2. Active algorithm rejected
	algoActive := &algorithm.Algorithm{
		ID:           "active_algo",
		Name:         "Active Algo",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusActive,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algoActive); err != nil {
		t.Fatalf("save active algo: %v", err)
	}

	_, err = svc.TrialInference(ctx, "active_algo", bytes.NewReader(pngBytes))
	if err == nil {
		t.Fatal("expected error for active algorithm, got nil")
	}
	if !errors.As(err, &appErr) || appErr.Code != "ALGORITHM_IS_ACTIVE" {
		t.Fatalf("expected ALGORITHM_IS_ACTIVE, got: %v", err)
	}

	// 3. Crash loop algorithm rejected
	algoCrash := &algorithm.Algorithm{
		ID:           "crash_algo",
		Name:         "Crash Algo",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusCrashLoop,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algoCrash); err != nil {
		t.Fatalf("save crash algo: %v", err)
	}

	_, err = svc.TrialInference(ctx, "crash_algo", bytes.NewReader(pngBytes))
	if err == nil {
		t.Fatal("expected error for crash-loop algorithm, got nil")
	}
	if !errors.As(err, &appErr) || appErr.Code != "ALGORITHM_IN_CRASH_LOOP" {
		t.Fatalf("expected ALGORITHM_IN_CRASH_LOOP, got: %v", err)
	}
}

func TestTrialInference_InvalidImage(t *testing.T) {
	svc, store, _ := setupTrialTestEnvironment(t)
	ctx := context.Background()

	algo := &algorithm.Algorithm{
		ID:           "test_algo",
		Name:         "Test Algo",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("save disabled algo: %v", err)
	}

	// Bad bytes
	_, err := svc.TrialInference(ctx, "test_algo", bytes.NewReader([]byte("not an image")))
	if err == nil {
		t.Fatal("expected error for invalid image bytes, got nil")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "INVALID_IMAGE_FILE" {
		t.Fatalf("expected INVALID_IMAGE_FILE, got: %v", err)
	}
}

func TestTrialInference_SuccessWithDetections(t *testing.T) {
	svc, store, algoDir := setupTrialTestEnvironment(t)
	ctx := context.Background()

	algoID := "mock_detector"
	version := "1.0.0"
	pkgDir := filepath.Join(algoDir, algoID, version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkgDir: %v", err)
	}

	// Create executable script that prints a verify output with structured results
	scriptPath := filepath.Join(pkgDir, algoID)
	scriptContent := `#!/bin/sh
printf '%s\n' '{"status":"passed","sample":"mock.ppm","sample_decoded":true,"width":640,"height":480,"result_type":1,"count":1,"cost_ms":28,"result":{"stream_id":0,"pts":1000,"capture_time_ms":0,"cost_ms":28,"algorithm_id":"mock_detector","task_type":"detection","detections":[{"box":[0.1,0.2,0.3,0.4],"label":"helmet","score":0.95}],"classifications":[],"persons":[],"vehicles":[]}}'
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write mock entry script: %v", err)
	}

	algo := &algorithm.Algorithm{
		ID:           algoID,
		Name:         "Mock Detector",
		Version:      version,
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("save algo: %v", err)
	}

	pngBytes := createTestPNGBytes(t, 200, 150)
	resp, err := svc.TrialInference(ctx, algoID, bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("TrialInference failed: %v", err)
	}

	if resp.Width != 200 || resp.Height != 150 {
		t.Errorf("expected 200x150, got %dx%d", resp.Width, resp.Height)
	}
	if resp.CostMs != 28 {
		t.Errorf("expected costMs 28, got %d", resp.CostMs)
	}
	if resp.Count != 1 {
		t.Errorf("expected count 1, got %d", resp.Count)
	}
	if len(resp.Detections) != 1 {
		t.Fatalf("expected 1 detection, got %d", len(resp.Detections))
	}
	det := resp.Detections[0]
	if det.Label != "helmet" || det.Score != 0.95 {
		t.Errorf("unexpected detection: %+v", det)
	}
	if det.Box[0] != 0.1 || det.Box[1] != 0.2 || det.Box[2] != 0.3 || det.Box[3] != 0.4 {
		t.Errorf("unexpected box: %+v", det.Box)
	}
}

func TestTrialInference_SampleDecodedFalseRejected(t *testing.T) {
	svc, store, algoDir := setupTrialTestEnvironment(t)
	ctx := context.Background()

	algoID := "undecodable_algo"
	version := "1.0.0"
	pkgDir := filepath.Join(algoDir, algoID, version)
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkgDir: %v", err)
	}

	// Script returns passed status but sample_decoded: false (fallback to synthetic frame)
	scriptPath := filepath.Join(pkgDir, algoID)
	scriptContent := `#!/bin/sh
printf '%s\n' '{"status":"passed","sample":"mock.ppm","sample_decoded":false,"width":640,"height":480,"result_type":1,"count":0,"cost_ms":10,"result":{"task_type":"detection","detections":[],"classifications":[],"persons":[],"vehicles":[]}}'
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write mock entry script: %v", err)
	}

	algo := &algorithm.Algorithm{
		ID:           algoID,
		Name:         "Undecodable Algo",
		Version:      version,
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("save algo: %v", err)
	}

	pngBytes := createTestPNGBytes(t, 200, 150)
	_, err := svc.TrialInference(ctx, algoID, bytes.NewReader(pngBytes))
	if err == nil {
		t.Fatal("expected error when sample_decoded is false, got nil")
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "TRIAL_EXECUTION_FAILED" {
		t.Fatalf("expected TRIAL_EXECUTION_FAILED, got: %v", err)
	}
}
