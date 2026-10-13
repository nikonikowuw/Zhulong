package algorithm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func TestServiceDeleteAlgorithmCleansUpTasks(t *testing.T) {
	comp, _ := setupServiceTest(t)
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

	// 2. Create analysis tasks referencing this algorithm by AlgorithmID
	task1 := &algorithm.AnalysisTask{
		ID:          "task_cam1_fire",
		CameraID:    "cam_01",
		AlgorithmID: "algo_fire_01",
		Enabled:     true,
		SampleFPS:   5,
	}
	task2 := &algorithm.AnalysisTask{
		ID:          "task_cam2_fire",
		CameraID:    "cam_02",
		AlgorithmID: "algo_fire_01",
		Enabled:     true,
		SampleFPS:   5,
	}
	if err := comp.Store.CreateTask(ctx, task1); err != nil {
		t.Fatalf("create task 1 failed: %v", err)
	}
	if err := comp.Store.CreateTask(ctx, task2); err != nil {
		t.Fatalf("create task 2 failed: %v", err)
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
	tasks, err := comp.Store.ListTasksByAlgorithm(ctx, "algo_fire_01")
	if err != nil {
		t.Fatalf("list tasks failed: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got: %d", len(tasks))
	}
}

func setupServiceTest(t *testing.T) (*algorithm.AlgorithmComponents, string) {
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
	return algorithm.NewAlgorithmComponents(dbStore, nil, tempDir, "cpu", zap.NewNop()), tempDir
}

func TestServiceCreateTaskValidatesConfigSchema(t *testing.T) {
	comp, _ := setupServiceTest(t)
	ctx := context.Background()
	algo := &algorithm.Algorithm{
		ID: "algo_schema", Name: "schema_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 10, MemoryMB: 64,
		ConfigSchemaJSON: `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object","properties":{"threshold":{"type":"number","minimum":0,"maximum":1}},"required":["threshold"],"additionalProperties":false}`,
		Status:           algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	invalid := &algorithm.AnalysisTask{
		ID: "task_invalid_config", CameraID: "cam_01", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 1, ConfigJSON: `{"threshold":"high"}`,
	}
	if err := comp.Service.CreateTask(ctx, invalid); !errors.Is(err, algorithm.ErrInvalidTaskConfig) {
		t.Fatalf("expected invalid task config error, got %v", err)
	}

	valid := &algorithm.AnalysisTask{
		ID: "task_valid_config", CameraID: "cam_02", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 1, ConfigJSON: `{"threshold":0.5}`,
	}
	if err := comp.Service.CreateTask(ctx, valid); err != nil {
		t.Fatalf("create task with schema-valid config: %v", err)
	}
}

func TestServiceCreateTaskEnforcesComputeBudgetAtomically(t *testing.T) {
	comp, _ := setupServiceTest(t)
	ctx := context.Background()
	algo := &algorithm.Algorithm{
		ID: "algo_budget", Name: "budget_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 1000,
		MemoryMB: 64, Status: algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	overflow := &algorithm.AnalysisTask{
		ID: "task_overflow", CameraID: "cam_overflow", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: int(^uint(0) >> 1), ConfigJSON: "{}",
	}
	if err := comp.Service.CreateTask(ctx, overflow); !errors.Is(err, algorithm.ErrComputeBudgetExceeded) {
		t.Fatalf("expected overflow to exceed compute budget, got %v", err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"task_1", "task_2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			errs <- comp.Service.CreateTask(ctx, &algorithm.AnalysisTask{
				ID: id, CameraID: id, AlgorithmID: algo.ID,
				Enabled: true, SampleFPS: 1, ConfigJSON: "{}",
			})
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		if !errors.Is(err, algorithm.ErrComputeBudgetExceeded) {
			t.Fatalf("unexpected task creation error: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("expected exactly one concurrent task to fit the budget, got %d", succeeded)
	}
	tasks, err := comp.Store.ListTasks(ctx)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected one persisted task, got %d", len(tasks))
	}
}

func TestNewAlgorithmComponentsDoesNotCreateInstallDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "not-created-yet")
	if comp := algorithm.NewAlgorithmComponents(nil, nil, dataDir, "rknn", zap.NewNop()); comp.Verifier.HostTarget != "rknn" {
		t.Fatalf("expected configured host target to reach verifier, got %q", comp.Verifier.HostTarget)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "algorithms")); !os.IsNotExist(err) {
		t.Fatalf("expected constructor to leave install directory absent, stat error = %v", err)
	}
}
