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

	// Create channel task with two algorithms:
	// algo_1 enabled with ROI, algo_2 disabled with MASK
	tsk1 := &task.Task{
		ID:       "task_1",
		CameraID: "cam_1",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:               "sub_1",
				AlgorithmID:      "algo_1",
				Enabled:          true,
				SampleFPS:        5,
				RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
			},
			{
				ID:               "sub_2",
				AlgorithmID:      "algo_2",
				Enabled:          false,
				SampleFPS:        5,
				RegionConfigJSON: `{"mask": [[0.2, 0.2, 0.6, 0.6]]}`,
			},
		},
	}
	if err := store.CreateTask(ctx, tsk1); err != nil {
		t.Fatalf("create tsk1: %v", err)
	}

	// Reload
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("cache reload: %v", err)
	}

	// Check tsk1 algo_1
	cfg1, ok1 := cache.ConfigFor("cam_1", "algo_1")
	if !ok1 {
		t.Fatalf("expected cam_1, algo_1 in cache")
	}
	if len(cfg1.ROI) != 1 || cfg1.ROI[0] != (region.Rect{0.1, 0.1, 0.5, 0.5}) {
		t.Fatalf("unexpected cached ROI: %+v", cfg1)
	}

	// Check tsk1 algo_2 (disabled)
	_, ok2 := cache.ConfigFor("cam_1", "algo_2")
	if ok2 {
		t.Fatalf("disabled algorithm should not be in cache")
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
		ID:       "task_1",
		CameraID: "cam_1",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:               "sub_1",
				AlgorithmID:      "algo_1",
				Enabled:          true,
				SampleFPS:        5,
				RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
			},
		},
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

// TestRegionConfigCacheMultiAlgoIndependent verifies that under a single channel task with
// multiple enabled algorithms, each algorithm correctly resolves its own independent region config.
func TestRegionConfigCacheMultiAlgoIndependent(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_det", "det_model", 10)
	insertMockAlgorithm(t, db, "algo_face", "face_model", 20)

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	tsk := &task.Task{
		ID:       "task_channel_1",
		CameraID: "cam_multi",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:               "sub_det",
				AlgorithmID:      "algo_det",
				Enabled:          true,
				SampleFPS:        10,
				RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.9, 0.9]]}`,
			},
			{
				ID:               "sub_face",
				AlgorithmID:      "algo_face",
				Enabled:          true,
				SampleFPS:        5,
				RegionConfigJSON: `{"mask": [[0.3, 0.3, 0.7, 0.7]]}`,
			},
		},
	}
	if err := store.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("cache reload: %v", err)
	}

	// algo_det has ROI
	cfgDet, okDet := cache.ConfigFor("cam_multi", "algo_det")
	if !okDet {
		t.Fatalf("expected algo_det in cache")
	}
	if len(cfgDet.ROI) != 1 || cfgDet.ROI[0] != (region.Rect{0.1, 0.1, 0.9, 0.9}) {
		t.Fatalf("unexpected algo_det ROI: %+v", cfgDet)
	}
	if len(cfgDet.MASK) != 0 {
		t.Fatalf("expected no MASK for algo_det, got %+v", cfgDet.MASK)
	}

	// algo_face has MASK
	cfgFace, okFace := cache.ConfigFor("cam_multi", "algo_face")
	if !okFace {
		t.Fatalf("expected algo_face in cache")
	}
	if len(cfgFace.MASK) != 1 || cfgFace.MASK[0] != (region.Rect{0.3, 0.3, 0.7, 0.7}) {
		t.Fatalf("unexpected algo_face MASK: %+v", cfgFace)
	}
	if len(cfgFace.ROI) != 0 {
		t.Fatalf("expected no ROI for algo_face, got %+v", cfgFace.ROI)
	}
}
