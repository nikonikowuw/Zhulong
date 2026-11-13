package task_test

import (
	"context"
	"sync"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/region"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

func TestRegionConfigCache(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_1",
		"det_1",
		10,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_2",
		"det_2",
		10,
	)

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	cfg, ok := cache.ConfigFor("cam_1", "algo_1")
	if ok || !cfg.IsEmpty() {
		t.Fatalf("expected empty cache initially, got %+v, ok=%v", cfg, ok)
	}

	// algo_1 enabled with ROI, algo_2 disabled with MASK.
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

	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("cache reload: %v", err)
	}

	cfg1, ok1 := cache.ConfigFor("cam_1", "algo_1")
	if !ok1 {
		t.Fatalf("expected cam_1, algo_1 in cache")
	}
	if len(cfg1.ROI) != 1 || cfg1.ROI[0] != (region.Rect{0.1, 0.1, 0.5, 0.5}) {
		t.Fatalf("unexpected cached ROI: %+v", cfg1)
	}

	if _, ok := cache.ConfigFor("cam_1", "algo_2"); ok {
		t.Fatalf("disabled algorithm should not be in cache")
	}
	if _, ok := cache.ConfigFor("cam_99", "algo_1"); ok {
		t.Fatalf("nonexistent task should not be in cache")
	}
}

// TestRegionConfigCacheOnTaskChanged verifies the cache refreshes itself when it is
// registered as a TaskChangeListener, without any external orchestration.
func TestRegionConfigCacheOnTaskChanged(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_1",
		"det_1",
		10,
	)

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

	insertMockAlgorithm(
		t,
		db,
		"algo_det",
		"det_model",
		10,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_face",
		"face_model",
		20,
	)

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

func TestRegionConfigCacheSkipsInactiveAndInvalidEntries(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_roi",
		"roi_detector",
		10,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_bad",
		"bad_detector",
		10,
	)

	validROI := `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`

	// 1) 停用的通道：整条不入缓存。
	disabledTask := &task.Task{
		CameraID: "cam_off",
		Enabled:  false,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_roi", Enabled: true, SampleFPS: 5, RegionConfigJSON: validROI},
		},
	}
	// 2) 通道启用但算法子项停用：不入缓存。
	disabledAlgo := &task.Task{
		CameraID: "cam_algo_off",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_roi", Enabled: false, SampleFPS: 5, RegionConfigJSON: validROI},
		},
	}
	// 3) 区域配置非法：跳过并告警，不能拖垮整张缓存。
	invalidConfig := &task.Task{
		CameraID: "cam_bad",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID:      "algo_bad",
				Enabled:          true,
				SampleFPS:        5,
				RegionConfigJSON: `{"mask": [[-0.1, 0.1, 0.5, 0.5]]}`,
			},
		},
	}
	// 4) 正常项：必须进缓存。
	active := &task.Task{
		CameraID: "cam_on",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_roi", Enabled: true, SampleFPS: 5, RegionConfigJSON: validROI},
		},
	}
	for _, tsk := range []*task.Task{disabledTask, disabledAlgo, invalidConfig, active} {
		if err := store.CreateTask(ctx, tsk); err != nil {
			t.Fatalf("seed task %s: %v", tsk.CameraID, err)
		}
	}

	cache := task.NewRegionConfigCache(store, zap.NewNop())
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	absentKeys := []struct {
		cameraID    string
		algorithmID string
	}{
		{cameraID: "cam_off", algorithmID: "algo_roi"},
		{cameraID: "cam_algo_off", algorithmID: "algo_roi"},
		{cameraID: "cam_bad", algorithmID: "algo_bad"},
	}
	for _, key := range absentKeys {
		if _, ok := cache.ConfigFor(key.cameraID, key.algorithmID); ok {
			t.Errorf("expected %s/%s to be absent from the cache", key.cameraID, key.algorithmID)
		}
	}
	cfg, ok := cache.ConfigFor("cam_on", "algo_roi")
	if !ok || len(cfg.ROI) != 1 {
		t.Fatalf("expected cam_on/algo_roi to be cached, got %+v ok=%v", cfg, ok)
	}
}

func TestRegionConfigCacheNilStore(t *testing.T) {
	// store 为 nil 时缓存保持空快照，供构造顺序受限的装配路径使用。
	cache := task.NewRegionConfigCache(nil, nil)
	if err := cache.Reload(context.Background()); err != nil {
		t.Fatalf("expected nil error with nil store, got %v", err)
	}
	if _, ok := cache.ConfigFor("cam", "algo"); ok {
		t.Fatal("expected empty cache")
	}
}

func TestRegionConfigCacheOnTaskChangedLogsReloadFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	// 任务变更已经提交，重载失败只能降级为告警，不能 panic 或回抛。
	cache.OnTaskChanged(ctx, "algo_roi")
}

func TestRegionConfigCacheSkipsEmptyRegionConfig(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_plain",
		"plain_detector",
		10,
	)

	// 启用中的任务但没有任何自定义区域策略：不进缓存（回落到全帧）。
	// 同时覆盖 "" 与 "{}" 两种"空配置"写法。
	tasks := []*task.Task{
		{
			CameraID: "cam_no_region",
			Enabled:  true,
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: "algo_plain", Enabled: true, SampleFPS: 5, RegionConfigJSON: "{}"},
			},
		},
		{
			CameraID: "cam_empty_region",
			Enabled:  true,
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: "algo_plain", Enabled: true, SampleFPS: 5, RegionConfigJSON: ""},
			},
		},
	}
	for _, tsk := range tasks {
		if err := store.CreateTask(ctx, tsk); err != nil {
			t.Fatalf("seed task %s: %v", tsk.CameraID, err)
		}
	}

	cache := task.NewRegionConfigCache(store, zap.NewNop())
	if err := cache.Reload(ctx); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}
	for _, cameraID := range []string{"cam_no_region", "cam_empty_region"} {
		if _, ok := cache.ConfigFor(cameraID, "algo_plain"); ok {
			t.Errorf("an empty region config (%s) must not be cached", cameraID)
		}
	}
}

func TestRegionConfigCacheConcurrentReload(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_conc",
		"concurrent_detector",
		10,
	)

	if err := store.CreateTask(ctx, &task.Task{
		CameraID: "cam_conc",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID:      "algo_conc",
				Enabled:          true,
				SampleFPS:        5,
				RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.5, 0.5]]}`,
			},
		},
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}

	cache := task.NewRegionConfigCache(store, zap.NewNop())

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				if err := cache.Reload(ctx); err != nil {
					t.Errorf("concurrent Reload failed: %v", err)
				}
				cache.ConfigFor("cam_conc", "algo_conc")
			}
		}()
	}
	wg.Wait()

	if _, ok := cache.ConfigFor("cam_conc", "algo_conc"); !ok {
		t.Fatal("expected cam_conc/algo_conc to be cached after concurrent reloads")
	}
}
