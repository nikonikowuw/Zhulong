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
