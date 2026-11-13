package task_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestStore(t *testing.T) (*task.Store, *gorm.DB, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := task.NewStore(dbStore)
	cleanup := func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	}
	return store, dbStore.DB(), cleanup
}

func insertMockAlgorithm(
	t *testing.T,
	db *gorm.DB,
	id string,
	name string,
	computeCost int,
) {
	t.Helper()
	err := db.Exec(
		`INSERT INTO algorithms (
			id, name, version, task_type, category, target_arch, target_device,
			compute_cost, memory_mb, description, manifest_json, config_schema_json,
			status, previous_version, created_at, updated_at
		) VALUES (
			?, ?, '1.0.0', 'detection', 'safety', 'x86_64', 'cpu', ?, 64,
			'', '{}', '{}', 'ACTIVE', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		)`,
		id,
		name,
		computeCost,
	).Error
	if err != nil {
		t.Fatalf("failed to insert mock algorithm: %v", err)
	}
}

func TestStoreTaskCRUD(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_01",
		"helmet_detector",
		10,
	)

	tsk := &task.Task{
		ID:       "task_01",
		CameraID: "cam_01",
		Enabled:  true,
		Name:     "大门监控",
		Algorithms: []task.TaskAlgorithm{
			{
				ID:          "sub_01",
				AlgorithmID: "algo_01",
				Enabled:     true,
				SampleFPS:   5,
				ConfigJSON:  `{"threshold": 0.5}`,
			},
		},
	}

	if err := store.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	found, err := store.GetTask(ctx, "task_01")
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if found.CameraID != "cam_01" {
		t.Fatalf("expected camera cam_01, got %s", found.CameraID)
	}
	if len(found.Algorithms) != 1 || found.Algorithms[0].SampleFPS != 5 {
		t.Fatalf("unexpected found algorithms: %+v", found.Algorithms)
	}

	camList, err := store.ListTasksByCamera(ctx, "cam_01")
	if err != nil || len(camList) != 1 {
		t.Fatalf("ListTasksByCamera failed: %v, count=%d", err, len(camList))
	}

	algoList, err := store.ListTasksByAlgorithm(ctx, "algo_01")
	if err != nil || len(algoList) != 1 {
		t.Fatalf("ListTasksByAlgorithm failed: %v, count=%d", err, len(algoList))
	}

	tsk.Algorithms[0].SampleFPS = 10
	if err := store.SaveTaskWithinBudget(ctx, tsk, task.MaxComputePoints); err != nil {
		t.Fatalf("SaveTaskWithinBudget failed: %v", err)
	}
	updated, err := store.GetTask(ctx, "task_01")
	if err != nil {
		t.Fatalf("GetTask after update failed: %v", err)
	}
	if len(updated.Algorithms) != 1 || updated.Algorithms[0].SampleFPS != 10 {
		t.Fatalf("expected updated SampleFPS 10, got %+v", updated.Algorithms)
	}

	if err := store.DeleteTask(ctx, "task_01"); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	if _, err := store.GetTask(ctx, "task_01"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreTaskBudget(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"heavy_algo",
		"heavy_detector",
		100,
	)

	// 100 * 5 = 500 点，未超 1000
	t1 := &task.Task{
		ID:       "task_01",
		CameraID: "cam_01",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:          "sub_01",
				AlgorithmID: "heavy_algo",
				Enabled:     true,
				SampleFPS:   5,
				ConfigJSON:  "{}",
			},
		},
	}
	if err := store.CreateTaskWithinBudget(ctx, t1, 1000); err != nil {
		t.Fatalf("CreateTaskWithinBudget t1 failed: %v", err)
	}

	// 再次加入 100 * 6 = 600 点，500 + 600 = 1100 > 1000，应拒绝
	t2 := &task.Task{
		ID:       "task_02",
		CameraID: "cam_02",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:          "sub_02",
				AlgorithmID: "heavy_algo",
				Enabled:     true,
				SampleFPS:   6,
				ConfigJSON:  "{}",
			},
		},
	}
	err := store.CreateTaskWithinBudget(ctx, t2, 1000)
	if !errors.Is(err, task.ErrComputeBudgetExceeded) {
		t.Fatalf("expected ErrComputeBudgetExceeded, got %v", err)
	}
}

func TestStoreCreateTaskPersistsDisabledEnabled(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_02",
		"face_detector",
		10,
	)

	tsk := &task.Task{
		ID:       "task_disabled",
		CameraID: "cam_02",
		Enabled:  false,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:          "sub_disabled",
				AlgorithmID: "algo_02",
				Enabled:     false,
				SampleFPS:   5,
				ConfigJSON:  "{}",
			},
		},
	}

	if err := store.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	stored, err := store.GetTask(ctx, tsk.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if stored.Enabled {
		t.Fatalf("expected parent Enabled=false to persist, got Enabled=%v", stored.Enabled)
	}
	if len(stored.Algorithms) != 1 || stored.Algorithms[0].Enabled {
		t.Fatalf("expected child Enabled=false to persist, got %+v", stored.Algorithms)
	}
}

func TestStoreSaveTaskSyncAlgorithms(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, algo := range []struct {
		id   string
		cost int
	}{
		{id: "algo_a", cost: 10},
		{id: "algo_b", cost: 20},
		{id: "algo_c", cost: 30},
	} {
		insertMockAlgorithm(
			t,
			db,
			algo.id,
			algo.id,
			algo.cost,
		)
	}

	tsk := &task.Task{
		ID:       "task_sync",
		CameraID: "cam_sync",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_a",
				Enabled:     true,
				SampleFPS:   5,
			},
			{
				AlgorithmID: "algo_b",
				Enabled:     true,
				SampleFPS:   5,
			},
		},
	}

	if err := store.SaveTaskWithinBudget(ctx, tsk, 1000); err != nil {
		t.Fatalf("initial save: %v", err)
	}

	stored, err := store.GetTask(ctx, "task_sync")
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if len(stored.Algorithms) != 2 {
		t.Fatalf("expected 2 algorithms, got %d", len(stored.Algorithms))
	}

	// Update: remove algo_a, update algo_b sampleFPS to 10, add algo_c
	tskUpdate := &task.Task{
		ID:       "task_sync",
		CameraID: "cam_sync",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_b",
				Enabled:     true,
				SampleFPS:   10,
			},
			{
				AlgorithmID: "algo_c",
				Enabled:     true,
				SampleFPS:   4,
			},
		},
	}

	if err := store.SaveTaskWithinBudget(ctx, tskUpdate, 1000); err != nil {
		t.Fatalf("update save: %v", err)
	}

	updated, err := store.GetTask(ctx, "task_sync")
	if err != nil {
		t.Fatalf("get updated task: %v", err)
	}
	if len(updated.Algorithms) != 2 {
		t.Fatalf("expected 2 algorithms after sync, got %d", len(updated.Algorithms))
	}

	algoMap := make(map[string]task.TaskAlgorithm, len(updated.Algorithms))
	for _, a := range updated.Algorithms {
		algoMap[a.AlgorithmID] = a
	}
	if _, ok := algoMap["algo_a"]; ok {
		t.Errorf("algo_a should have been removed")
	}
	if b, ok := algoMap["algo_b"]; !ok || b.SampleFPS != 10 {
		t.Errorf("algo_b should be updated to sampleFPS 10, got %+v", b)
	}
	if c, ok := algoMap["algo_c"]; !ok || c.SampleFPS != 4 {
		t.Errorf("algo_c should be added with sampleFPS 4, got %+v", c)
	}
}

func TestStoreCreateTaskWithinBudgetRejectsDuplicateCamera(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_dup",
		"dup_detector",
		10,
	)

	first := &task.Task{
		CameraID: "cam_dup",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_dup", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	if err := store.CreateTaskWithinBudget(ctx, first, 1000); err != nil {
		t.Fatalf("first create failed: %v", err)
	}

	// 同一摄像头再来一次：必须被 camera_id 唯一约束拦下，并归因为 ErrTaskAlreadyExists，
	// 而不是把原始 SQLite 错误交给上层变成 500。
	second := &task.Task{
		CameraID: "cam_dup",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_dup", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	err := store.CreateTaskWithinBudget(ctx, second, 1000)
	if !errors.Is(err, task.ErrTaskAlreadyExists) {
		t.Fatalf("expected ErrTaskAlreadyExists, got %v", err)
	}
}

func TestStoreSaveTaskWithinBudgetRejectsCameraMove(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_move",
		"move_detector",
		10,
	)

	taskA := &task.Task{
		CameraID: "cam_move_a",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_move", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	taskB := &task.Task{
		CameraID: "cam_move_b",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_move", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	for _, tsk := range []*task.Task{taskA, taskB} {
		if err := store.CreateTaskWithinBudget(ctx, tsk, 1000); err != nil {
			t.Fatalf("create task for %s: %v", tsk.CameraID, err)
		}
	}

	// 用 A 的 ID 把任务挪到已被 B 占用的摄像头：必须拒绝，不能把 B 覆盖掉。
	moved := &task.Task{
		ID:       taskA.ID,
		CameraID: "cam_move_b",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_move", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	err := store.SaveTaskWithinBudget(ctx, moved, 1000)
	if !errors.Is(err, task.ErrTaskAlreadyExists) {
		t.Fatalf("expected ErrTaskAlreadyExists, got %v", err)
	}

	stored, err := store.GetTask(ctx, taskB.ID)
	if err != nil {
		t.Fatalf("get task B: %v", err)
	}
	if stored.CameraID != "cam_move_b" {
		t.Fatalf("task B must stay on cam_move_b, got %s", stored.CameraID)
	}
}

func TestStoreListTasksReturnsEmptySlice(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	list, err := store.ListTasks(ctx)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	if list == nil {
		t.Fatal("expected non-nil empty slice so JSON encodes [] instead of null")
	}
	if len(list) != 0 {
		t.Fatalf("expected empty list, got %d", len(list))
	}
}

// nilDBProvider 模拟"数据库还没初始化"的装配顺序，用于验证 Store 的前置守卫。
type nilDBProvider struct{}

func (nilDBProvider) DB() *gorm.DB { return nil }

func TestStoreRequiresInitializedDatabase(t *testing.T) {
	ctx := context.Background()

	for name, provider := range map[string]database.DBProvider{
		"nil provider": nil,
		"nil database": nilDBProvider{},
	} {
		t.Run(name, func(t *testing.T) {
			store := task.NewStore(provider)

			// 每个入口都必须在触库前给出错误，而不是 panic 或静默返回空结果。
			if _, err := store.GetTask(ctx, "task_x"); err == nil {
				t.Error("GetTask must fail")
			}
			if _, err := store.GetTaskByCamera(ctx, "cam_x"); err == nil {
				t.Error("GetTaskByCamera must fail")
			}
			if _, err := store.ListTasks(ctx); err == nil {
				t.Error("ListTasks must fail")
			}
			if _, err := store.ListTasksByCamera(ctx, "cam_x"); err == nil {
				t.Error("ListTasksByCamera must fail")
			}
			if _, err := store.ListTasksByAlgorithm(ctx, "algo_x"); err == nil {
				t.Error("ListTasksByAlgorithm must fail")
			}
			if err := store.CreateTask(ctx, &task.Task{CameraID: "cam_x"}); err == nil {
				t.Error("CreateTask must fail")
			}
			if err := store.CreateTaskWithinBudget(ctx, &task.Task{CameraID: "cam_x"}, 1000); err == nil {
				t.Error("CreateTaskWithinBudget must fail")
			}
			if err := store.SaveTaskWithinBudget(ctx, &task.Task{CameraID: "cam_x"}, 1000); err == nil {
				t.Error("SaveTaskWithinBudget must fail")
			}
			if err := store.DeleteTask(ctx, "task_x"); err == nil {
				t.Error("DeleteTask must fail")
			}
		})
	}
}

func TestStoreRejectsNilTask(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreateTask(ctx, nil); err == nil {
		t.Error("CreateTask(nil) must fail")
	}
	if err := store.CreateTaskWithinBudget(ctx, nil, 1000); err == nil {
		t.Error("CreateTaskWithinBudget(nil) must fail")
	}
	if err := store.SaveTaskWithinBudget(ctx, nil, 1000); err == nil {
		t.Error("SaveTaskWithinBudget(nil) must fail")
	}
}

func TestStoreBudgetSkipsDisabledAlgorithms(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_heavy",
		"heavy_detector",
		100,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_light",
		"light_detector",
		10,
	)

	// 通道启用，但其中一条算法子项停用：只有启用项计入预算。
	tsk := &task.Task{
		CameraID: "cam_mixed",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_heavy", Enabled: false, SampleFPS: 1000, ConfigJSON: "{}"},
			{AlgorithmID: "algo_light", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	if err := store.CreateTaskWithinBudget(ctx, tsk, 1000); err != nil {
		t.Fatalf("disabled algorithm must not consume budget: %v", err)
	}
}

func TestStoreDeleteMissingTask(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	if err := store.DeleteTask(context.Background(), "task_absent"); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreDynamicPeakBudgetWithSchedule(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_day",
		"day_detector",
		100,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_night",
		"night_detector",
		100,
	)

	// 任务 1：白天运行 08:00 - 18:00，点数 100 * 6 = 600 点
	t1 := &task.Task{
		ID:       "task_day",
		CameraID: "cam_day",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:           "sub_day",
				AlgorithmID:  "algo_day",
				Enabled:      true,
				SampleFPS:    6,
				ConfigJSON:   "{}",
				ScheduleJSON: `{"enabled": true, "days": {"monday": [{"start": "08:00", "end": "18:00"}]}}`,
			},
		},
	}
	if err := store.CreateTaskWithinBudget(ctx, t1, 1000); err != nil {
		t.Fatalf("CreateTaskWithinBudget t1 failed: %v", err)
	}

	// 任务 2（不重叠）：夜间运行 18:00 - 24:00，点数 100 * 6 = 600 点。
	// 静态相加为 1200 > 1000，但分时峰值为 600 <= 1000，应允许成功。
	t2NonOverlap := &task.Task{
		ID:       "task_night",
		CameraID: "cam_night",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:           "sub_night",
				AlgorithmID:  "algo_night",
				Enabled:      true,
				SampleFPS:    6,
				ConfigJSON:   "{}",
				ScheduleJSON: `{"enabled": true, "days": {"monday": [{"start": "18:00", "end": "24:00"}]}}`,
			},
		},
	}
	if err := store.CreateTaskWithinBudget(ctx, t2NonOverlap, 1000); err != nil {
		t.Fatalf("CreateTaskWithinBudget t2NonOverlap should succeed with peak 600, got: %v", err)
	}

	// 任务 3（重叠）：时段为 12:00 - 20:00，点数 100 * 5 = 500 点。
	// 在 12:00 - 18:00 与任务 1 重叠，瞬间峰值为 600 + 500 = 1100 > 1000，应拒绝。
	t3Overlap := &task.Task{
		ID:       "task_overlap",
		CameraID: "cam_overlap",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{
				ID:           "sub_overlap",
				AlgorithmID:  "algo_night",
				Enabled:      true,
				SampleFPS:    5,
				ConfigJSON:   "{}",
				ScheduleJSON: `{"enabled": true, "days": {"monday": [{"start": "12:00", "end": "20:00"}]}}`,
			},
		},
	}
	err := store.CreateTaskWithinBudget(ctx, t3Overlap, 1000)
	if !errors.Is(err, task.ErrComputeBudgetExceeded) {
		t.Fatalf("expected ErrComputeBudgetExceeded for overlapping schedule, got %v", err)
	}
}

func TestStoreCountEnabledTasks(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_cnt",
		"helmet_detector",
		10,
	)

	// 1. Enabled task with enabled algorithm -> counted
	if err := store.CreateTask(ctx, &task.Task{
		ID:       "t_enabled_with_algo",
		CameraID: "cam_1",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{ID: "sub_1", AlgorithmID: "algo_cnt", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}); err != nil {
		t.Fatalf("CreateTask 1 failed: %v", err)
	}

	// 2. Disabled task with enabled algorithm -> not counted
	if err := store.CreateTask(ctx, &task.Task{
		ID:       "t_disabled",
		CameraID: "cam_2",
		Enabled:  false,
		Algorithms: []task.TaskAlgorithm{
			{ID: "sub_2", AlgorithmID: "algo_cnt", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}); err != nil {
		t.Fatalf("CreateTask 2 failed: %v", err)
	}

	// 3. Enabled orphan task with zero algorithms -> not counted
	if err := store.CreateTask(ctx, &task.Task{
		ID:       "t_enabled_empty",
		CameraID: "cam_3",
		Enabled:  true,
	}); err != nil {
		t.Fatalf("CreateTask 3 failed: %v", err)
	}

	// 4. Enabled task whose bound algorithm is disabled -> not counted
	if err := store.CreateTask(ctx, &task.Task{
		ID:       "t_enabled_algo_off",
		CameraID: "cam_4",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{ID: "sub_4", AlgorithmID: "algo_cnt", Enabled: false, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}); err != nil {
		t.Fatalf("CreateTask 4 failed: %v", err)
	}

	cnt, err := store.CountEnabledTasks(ctx)
	if err != nil {
		t.Fatalf("CountEnabledTasks failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("CountEnabledTasks = %d, want 1", cnt)
	}
}

func TestStoreCreateTaskWithinBudgetRejectsNegativeFPSAsInvalidConfig(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_neg_fps",
		"neg_fps_detector",
		10,
	)

	err := store.CreateTaskWithinBudget(ctx, &task.Task{
		CameraID: "cam_neg_fps",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_neg_fps", Enabled: true, SampleFPS: -2, ConfigJSON: "{}"},
		},
	}, 1000)
	if err == nil {
		t.Fatal("expected error for negative sample_fps")
	}
	if errors.Is(err, task.ErrComputeBudgetExceeded) {
		t.Fatalf("negative sample_fps must not be reported as ErrComputeBudgetExceeded, got %v", err)
	}
}
