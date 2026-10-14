package algorithm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupTestStore(t *testing.T) (*algorithm.Store, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := algorithm.NewStore(dbStore)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return store, cleanup
}

func TestStoreAlgorithmCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	algo := &algorithm.Algorithm{
		ID:           "algo_test_01",
		Name:         "test_detector",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "cpu",
		ComputeCost:  20,
		MemoryMB:     128,
		Description:  "Safety helmet detector",
		Status:       algorithm.StatusDisabled,
	}

	// 1. Create
	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("CreateAlgorithm failed: %v", err)
	}

	// 2. Get by ID
	found, err := store.GetAlgorithm(ctx, "algo_test_01")
	if err != nil {
		t.Fatalf("GetAlgorithm failed: %v", err)
	}
	if found.Name != "test_detector" {
		t.Errorf("expected name 'test_detector', got '%s'", found.Name)
	}
	if found.Status != algorithm.StatusDisabled {
		t.Errorf("expected status 'DISABLED', got '%s'", found.Status)
	}
	if found.ComputeCost != 20 {
		t.Errorf("expected compute cost 20, got %d", found.ComputeCost)
	}

	// 3. Get by Name
	foundByName, err := store.GetAlgorithmByName(ctx, "test_detector")
	if err != nil {
		t.Fatalf("GetAlgorithmByName failed: %v", err)
	}
	if foundByName.ID != "algo_test_01" {
		t.Errorf("expected ID 'algo_test_01', got '%s'", foundByName.ID)
	}

	// 4. Update status
	if err := store.UpdateAlgorithmStatus(ctx, "algo_test_01", algorithm.StatusActive); err != nil {
		t.Fatalf("UpdateAlgorithmStatus failed: %v", err)
	}
	found, err = store.GetAlgorithm(ctx, "algo_test_01")
	if err != nil {
		t.Fatalf("GetAlgorithm failed: %v", err)
	}
	if found.Status != algorithm.StatusActive {
		t.Errorf("expected status 'ACTIVE', got '%s'", found.Status)
	}

	// 5. List
	list, err := store.ListAlgorithms(ctx)
	if err != nil {
		t.Fatalf("ListAlgorithms failed: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("expected 1 algorithm, got %d", len(list))
	}

	// 6. Delete
	if err := store.DeleteAlgorithm(ctx, "algo_test_01"); err != nil {
		t.Fatalf("DeleteAlgorithm failed: %v", err)
	}
	_, err = store.GetAlgorithm(ctx, "algo_test_01")
	if !errors.Is(err, algorithm.ErrNotFound) {
		t.Errorf("expected ErrNotFound after deletion, got %v", err)
	}
}

func TestStoreAnalysisTaskCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	task := &algorithm.AnalysisTask{
		ID:          "task_01",
		CameraID:    "cam_01",
		AlgorithmID: "algo_test_detector",
		Enabled:     true,
		SampleFPS:   5,
		ConfigJSON:  `{"threshold": 0.5}`,
	}

	// 1. Create
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 2. Get
	found, err := store.GetTask(ctx, "task_01")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if found.CameraID != "cam_01" {
		t.Errorf("expected camera ID 'cam_01', got '%s'", found.CameraID)
	}
	if found.AlgorithmID != "algo_test_detector" {
		t.Errorf("expected algorithm ID 'algo_test_detector', got '%s'", found.AlgorithmID)
	}
	if found.SampleFPS != 5 {
		t.Errorf("expected sample FPS 5, got %d", found.SampleFPS)
	}

	// 3. Get by camera and algorithm
	foundByCam, err := store.GetTaskByCameraAndAlgorithm(ctx, "cam_01", "algo_test_detector")
	if err != nil {
		t.Fatalf("GetTaskByCameraAndAlgorithm failed: %v", err)
	}
	if foundByCam.ID != "task_01" {
		t.Errorf("expected task ID 'task_01', got '%s'", foundByCam.ID)
	}

	// 4. List by camera
	camList, err := store.ListTasksByCamera(ctx, "cam_01")
	if err != nil {
		t.Fatalf("ListTasksByCamera failed: %v", err)
	}
	if len(camList) != 1 {
		t.Errorf("expected 1 task, got %d", len(camList))
	}

	// 5. Delete by camera
	if err := store.DeleteTasksByCamera(ctx, "cam_01"); err != nil {
		t.Fatalf("DeleteTasksByCamera failed: %v", err)
	}
	camList, err = store.ListTasksByCamera(ctx, "cam_01")
	if err != nil {
		t.Fatalf("ListTasksByCamera after delete failed: %v", err)
	}
	if len(camList) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(camList))
	}
}

func TestStoreRollbackAlgorithmVersion(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	algo := &algorithm.Algorithm{
		ID:              "algo_rollback_test",
		Name:            "test_rollback_detector",
		Version:         "2.0.0",
		TaskType:        algorithm.TaskTypeDetection,
		Category:        algorithm.CategorySafety,
		TargetArch:      "x86_64",
		TargetDevice:    "cpu",
		ComputeCost:     20,
		MemoryMB:        128,
		Status:          algorithm.StatusActive,
		PreviousVersion: "1.0.0",
	}

	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("CreateAlgorithm failed: %v", err)
	}

	// Roll back version from 2.0.0 to 1.0.0
	if err := store.RollbackAlgorithmVersion(ctx, "algo_rollback_test", "1.0.0", "2.0.0"); err != nil {
		t.Fatalf("RollbackAlgorithmVersion failed: %v", err)
	}

	updated, err := store.GetAlgorithm(ctx, "algo_rollback_test")
	if err != nil {
		t.Fatalf("GetAlgorithm failed: %v", err)
	}
	if updated.Version != "1.0.0" {
		t.Errorf("expected rolled back version '1.0.0', got '%s'", updated.Version)
	}
	if updated.PreviousVersion != "2.0.0" {
		t.Errorf("expected previous version '2.0.0', got '%s'", updated.PreviousVersion)
	}
	if updated.Status != algorithm.StatusActive {
		t.Errorf("expected status 'ACTIVE', got '%s'", updated.Status)
	}
}

// TestStoreCreateTaskPersistsDisabledEnabled guards the GORM default-tag trap:
// a `default:true` tag makes GORM omit a zero-valued Enabled from the INSERT, so
// an explicitly disabled task silently came back enabled. D5 makes enabled a
// task-change trigger, so this must round-trip faithfully.
func TestStoreCreateTaskPersistsDisabledEnabled(t *testing.T) {
	ctx := context.Background()
	store, cleanup := setupTestStore(t)
	defer cleanup()

	task := &algorithm.AnalysisTask{
		ID: "task_disabled", CameraID: "cam_disabled", AlgorithmID: "algo_disabled",
		Enabled: false, SampleFPS: 5, ConfigJSON: "{}",
	}
	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	stored, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if stored.Enabled {
		t.Fatal("Enabled=false was coerced back to true on insert (GORM default tag trap)")
	}
}
