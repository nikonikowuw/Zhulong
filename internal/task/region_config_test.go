package task_test

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/region"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

func TestRegionConfigCache(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_1", "det_1", 10)
	insertMockAlgorithm(t, db, "algo_2", "det_2", 10)

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	// Initially empty
	cfg, ok := cache.ConfigFor("cam_1", "algo_1")
	if ok || !cfg.IsEmpty() {
		t.Fatalf("expected empty cache initially, got %+v, ok=%v", cfg, ok)
	}

	// Create enabled task with valid region
	tsk1 := &task.Task{
		ID:               "task_1",
		CameraID:         "cam_1",
		AlgorithmID:      "algo_1",
		Enabled:          true,
		RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
	}
	if err := store.CreateTask(ctx, tsk1); err != nil {
		t.Fatalf("create tsk1: %v", err)
	}

	// Create disabled task with valid region (should NOT be cached)
	tsk2 := &task.Task{
		ID:               "task_2",
		CameraID:         "cam_1",
		AlgorithmID:      "algo_2",
		Enabled:          false,
		RegionConfigJSON: `{"mask": [[0.2, 0.2, 0.6, 0.6]]}`,
	}
	if err := store.CreateTask(ctx, tsk2); err != nil {
		t.Fatalf("create tsk2: %v", err)
	}

	// Reload
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("cache reload: %v", err)
	}

	// Check tsk1
	cfg1, ok1 := cache.ConfigFor("cam_1", "algo_1")
	if !ok1 {
		t.Fatalf("expected cam_1, algo_1 in cache")
	}
	if len(cfg1.ROI) != 1 || cfg1.ROI[0] != (region.Rect{0.1, 0.1, 0.5, 0.5}) {
		t.Fatalf("unexpected cached ROI: %+v", cfg1)
	}

	// Check tsk2 (disabled)
	_, ok2 := cache.ConfigFor("cam_1", "algo_2")
	if ok2 {
		t.Fatalf("disabled task should not be in cache")
	}

	// Check nonexistent
	_, ok3 := cache.ConfigFor("cam_99", "algo_1")
	if ok3 {
		t.Fatalf("nonexistent task should not be in cache")
	}
}

// TestRegionConfigCacheOnTaskChanged verifies the cache refreshes itself when it is
// registered as a TaskChangeListener, without any external orchestration.
func TestRegionConfigCacheOnTaskChanged(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_1", "det_1", 10)

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	tsk := &task.Task{
		ID:               "task_1",
		CameraID:         "cam_1",
		AlgorithmID:      "algo_1",
		Enabled:          true,
		RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
	}
	if err := store.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	// The snapshot is untouched until the listener fires.
	if _, ok := cache.ConfigFor("cam_1", "algo_1"); ok {
		t.Fatalf("cache should still be empty before the change notification")
	}

	cache.OnTaskChanged(ctx, "algo_1")

	if _, ok := cache.ConfigFor("cam_1", "algo_1"); !ok {
		t.Fatalf("expected cam_1/algo_1 in cache after OnTaskChanged")
	}
}
