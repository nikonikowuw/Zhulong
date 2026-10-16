package algorithm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupServiceTest(t *testing.T) (*algorithm.AlgorithmComponents, *database.Store, string) {
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
	return algorithm.NewAlgorithmComponents(dbStore, tempDir, "cpu", zap.NewNop()), dbStore, tempDir
}

func TestServiceDeleteAlgorithmCleansUpTasks(t *testing.T) {
	comp, dbStore, _ := setupServiceTest(t)
	ctx := context.Background()

	// 1. Create an algorithm
	algo := &algorithm.Algorithm{
		ID:           "algo_fire_01",
		Name:         "fire_detect",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategoryFire,
		TargetArch:   "x86_64",
		TargetDevice: "cpu",
		ComputeCost:  20,
		MemoryMB:     128,
		Status:       algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm failed: %v", err)
	}

	// 2. Create analysis tasks referencing this algorithm directly in DB
	if err := dbStore.DB().Exec(`INSERT INTO analysis_tasks (id, camera_id, algorithm_id, enabled, sample_fps, config_json, created_at, updated_at) VALUES 
		('task_cam1_fire', 'cam_01', 'algo_fire_01', 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		('task_cam2_fire', 'cam_02', 'algo_fire_01', 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert test tasks failed: %v", err)
	}

	// 3. Delete Algorithm
	if err := comp.Service.DeleteAlgorithm(ctx, "algo_fire_01"); err != nil {
		t.Fatalf("delete algorithm failed: %v", err)
	}

	// 4. Verify algorithm is gone
	_, err := comp.Store.GetAlgorithm(ctx, "algo_fire_01")
	if err == nil {
		t.Fatal("expected algorithm to be deleted, but still found")
	}

	// 5. Verify analysis tasks for this algorithm are also gone
	var count int64
	dbStore.DB().Table("analysis_tasks").Where("algorithm_id = ?", "algo_fire_01").Count(&count)
	if count != 0 {
		t.Errorf("expected 0 tasks remaining, got: %d", count)
	}
}

func TestNewAlgorithmComponentsDoesNotCreateInstallDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "not-created-yet")
	if comp := algorithm.NewAlgorithmComponents(nil, dataDir, "rknn", zap.NewNop()); comp.Verifier.HostTarget != "rknn" {
		t.Fatalf("expected configured host target to reach verifier, got %q", comp.Verifier.HostTarget)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "algorithms")); !os.IsNotExist(err) {
		t.Fatalf("expected constructor to leave install directory absent, stat error = %v", err)
	}
}

func TestServiceErrorApperrClassifications(t *testing.T) {
	comp, _, _ := setupServiceTest(t)
	ctx := context.Background()

	// 1. GetAlgorithm not found -> apperr.KindNotFound
	_, err := comp.Service.GetAlgorithm(ctx, "non_existent_id")
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound || appErr.Code != "ALGORITHM_NOT_FOUND" {
		t.Fatalf("expected ALGORITHM_NOT_FOUND apperr on GetAlgorithm, got: %v", err)
	}

	// 2. DeleteAlgorithm not found -> apperr.KindNotFound
	err = comp.Service.DeleteAlgorithm(ctx, "non_existent_id")
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindNotFound || appErr.Code != "ALGORITHM_NOT_FOUND" {
		t.Fatalf("expected ALGORITHM_NOT_FOUND apperr on DeleteAlgorithm, got: %v", err)
	}

	// 3. ResetAlgorithm not in crash loop -> apperr.KindConflict
	algo := &algorithm.Algorithm{
		ID: "algo_reset", Name: "reset_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 10, MemoryMB: 64,
		Status: algorithm.StatusActive,
	}
	_ = comp.Store.CreateAlgorithm(ctx, algo)
	err = comp.Service.ResetAlgorithm(ctx, algo.ID)
	if !errors.As(err, &appErr) || appErr.Kind != apperr.KindConflict || appErr.Code != "ALGORITHM_NOT_IN_CRASH_LOOP" {
		t.Fatalf("expected ALGORITHM_NOT_IN_CRASH_LOOP apperr on ResetAlgorithm, got: %v", err)
	}
}
