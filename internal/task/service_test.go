package task_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

func boolPtr(v bool) *bool {
	return &v
}

func stringPtr(v string) *string {
	return &v
}

type mockAlgoProvider struct {
	specs map[string]*task.AlgorithmSpec
}

func (m *mockAlgoProvider) GetAlgorithmSpec(
	_ context.Context,
	idOrName string,
) (*task.AlgorithmSpec, error) {
	if spec, ok := m.specs[idOrName]; ok {
		return spec, nil
	}
	return nil, errors.New("algorithm not found")
}

type mockTaskChangeListener struct {
	mu            sync.Mutex
	notifications []string
}

func (m *mockTaskChangeListener) OnTaskChanged(_ context.Context, algorithmID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.notifications = append(m.notifications, algorithmID)
}

func (m *mockTaskChangeListener) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notifications)
}

func setupTestService(t *testing.T) (*task.Service, *mockTaskChangeListener, func()) {
	t.Helper()
	store, db, cleanup := setupTestStore(t)

	insertMockAlgorithm(
		t,
		db,
		"algo_helmet",
		"helmet_detector",
		20,
	)
	insertMockAlgorithm(
		t,
		db,
		"algo_vest",
		"vest_detector",
		10,
	)

	provider := &mockAlgoProvider{
		specs: map[string]*task.AlgorithmSpec{
			"algo_helmet": {
				ID:               "algo_helmet",
				Name:             "helmet_detector",
				ComputeCost:      20,
				ConfigSchemaJSON: `{"type": "object", "properties": {"threshold": {"type": "number"}}}`,
			},
			"algo_vest": {
				ID:          "algo_vest",
				Name:        "vest_detector",
				ComputeCost: 10,
			},
		},
	}
	listener := &mockTaskChangeListener{}
	svc := task.NewService(
		store,
		provider,
		[]task.TaskChangeListener{listener},
		zap.NewNop(),
	)

	return svc, listener, cleanup
}

func TestServiceCreateTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Missing CameraID
	err := svc.CreateTask(ctx, &task.Task{
		Algorithms: []task.TaskAlgorithm{{AlgorithmID: "algo_helmet"}},
	})
	assertAppErrCode(t, err, "VALIDATION_FAILED")

	// 2. Normal creation
	tsk := &task.Task{
		CameraID: "cam_100",
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_helmet",
				SampleFPS:   5,
				ConfigJSON:  `{"threshold": 0.8}`,
			},
		},
	}
	if err := svc.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}
	if tsk.ID == "" {
		t.Fatal("expected generated task ID")
	}
	if listener.count() != 1 {
		t.Fatalf("expected 1 notification, got %d", listener.count())
	}

	// 3. Duplicate task for same camera
	dup := &task.Task{
		CameraID: "cam_100",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet"},
		},
	}
	assertAppErrCode(t, svc.CreateTask(ctx, dup), "TASK_ALREADY_EXISTS")

	// 4. Invalid config schema
	invalidCfg := &task.Task{
		CameraID: "cam_101",
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_helmet",
				ConfigJSON:  `{"threshold": "not_a_number"}`,
			},
		},
	}
	assertAppErrCode(t, svc.CreateTask(ctx, invalidCfg), "INVALID_TASK_CONFIG")

	// 5. Invalid region config
	invalidRegion := &task.Task{
		CameraID: "cam_102",
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID:      "algo_helmet",
				RegionConfigJSON: `{"roi": [[0.8, 0.2, 0.5, 0.9]]}`, // degenerate x1 >= x2
			},
		},
	}
	assertAppErrCode(t, svc.CreateTask(ctx, invalidRegion), "INVALID_REGION_CONFIG")
}

func TestServiceUpdateTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tsk := &task.Task{
		CameraID: "cam_200",
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_helmet",
				SampleFPS:   5,
				ConfigJSON:  `{"threshold": 0.5}`,
			},
		},
	}
	if err := svc.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	fps := 15
	cfg := `{"threshold": 0.9}`
	updated, err := svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{
		Algorithms: []task.TaskAlgorithmItem{
			{
				AlgorithmID: "algo_helmet",
				SampleFPS:   fps,
				ConfigJSON:  cfg,
			},
		},
	})
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	if len(updated.Algorithms) != 1 {
		t.Fatalf("expected 1 algorithm, got %d", len(updated.Algorithms))
	}
	gotAlgo := updated.Algorithms[0]
	if gotAlgo.SampleFPS != 15 || gotAlgo.ConfigJSON != cfg {
		t.Fatalf("unexpected updated fields: %+v", gotAlgo)
	}
	if listener.count() != 2 {
		t.Fatalf("expected 2 notifications, got %d", listener.count())
	}

	badRegion := `{"mask": [[-0.1, 0.1, 0.5, 0.5]]}`
	_, err = svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{
		Algorithms: []task.TaskAlgorithmItem{
			{
				AlgorithmID:      "algo_helmet",
				RegionConfigJSON: badRegion,
			},
		},
	})
	assertAppErrCode(t, err, "INVALID_REGION_CONFIG")
}

func TestServiceDeleteTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tsk := &task.Task{
		CameraID: "cam_300",
		Algorithms: []task.TaskAlgorithm{
			{
				AlgorithmID: "algo_helmet",
				SampleFPS:   5,
			},
		},
	}
	if err := svc.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	if err := svc.DeleteTask(ctx, tsk.ID); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	if _, err := svc.GetTask(ctx, tsk.ID); err == nil {
		t.Fatal("expected task to be deleted")
	}
	if listener.count() != 2 {
		t.Fatalf("expected 2 notifications (create + delete), got %d", listener.count())
	}
}

func TestServiceSaveTaskUpsert(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	first := &task.Task{
		CameraID: "cam_save",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 5, ConfigJSON: "{}"},
		},
	}
	if err := svc.SaveTask(ctx, first); err != nil {
		t.Fatalf("SaveTask (create) failed: %v", err)
	}
	if first.ID == "" {
		t.Fatal("expected store to assign a task ID")
	}

	// 同一摄像头再次保存：必须复用既有任务行，而不是撞 camera_id 唯一约束。
	second := &task.Task{
		CameraID: "cam_save",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 10, ConfigJSON: "{}"},
		},
	}
	if err := svc.SaveTask(ctx, second); err != nil {
		t.Fatalf("SaveTask (update) failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected upsert to reuse task ID %q, got %q", first.ID, second.ID)
	}

	stored, err := svc.GetTaskByCamera(ctx, "cam_save")
	if err != nil {
		t.Fatalf("GetTaskByCamera failed: %v", err)
	}
	if len(stored.Algorithms) != 1 || stored.Algorithms[0].SampleFPS != 10 {
		t.Fatalf("expected sampleFps 10, got %+v", stored.Algorithms)
	}
	// 两次保存各自广播一次（同一算法 ID 在一次变更内去重）。
	if listener.count() != 2 {
		t.Fatalf("expected 2 notifications, got %d", listener.count())
	}
}

func TestServiceRejectsDuplicateAlgorithm(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_dup_algo",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 5},
			{AlgorithmID: "algo_helmet", SampleFPS: 10},
		},
	})
	assertAppErrCode(t, err, "DUPLICATE_ALGORITHM")
}

func TestServiceRejectsNegativeSampleFPS(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_negative_fps",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: -3},
		},
	})
	assertAppErrCode(t, err, "INVALID_TASK_CONFIG")
}

func TestServiceComputeBudgetExceeded(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// algo_helmet 单点算力 20，100 fps 即 2,000 点，超过 1,000 点预算池。
	err := svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_budget",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", Enabled: true, SampleFPS: 100},
		},
	})
	assertAppErrCode(t, err, "COMPUTE_BUDGET_EXCEEDED")
}

func TestServiceDeleteTaskRejectsEmptyID(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	assertAppErrCode(t, svc.DeleteTask(context.Background(), ""), "VALIDATION_FAILED")
}

// assertAppErrCode 断言错误是带指定机器码的 apperr.Error。
func assertAppErrCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %q, got nil", code)
	}
	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected apperr.Error, got %T", err)
	}
	if appErr.Code != code {
		t.Fatalf("expected code %q, got %q", code, appErr.Code)
	}
}

func TestServiceUpdateTaskPartialFields(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// 直接走 Service 时 Enabled 必须显式给出：默认值收敛只发生在 Handler 的请求类型层。
	tsk := &task.Task{
		CameraID: "cam_partial",
		Name:     "original",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", Enabled: true, SampleFPS: 5, ConfigJSON: `{"threshold": 0.5}`},
		},
	}
	if err := svc.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	// 只改名字：算法配置必须原样保留。
	renamed, err := svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{Name: stringPtr("renamed")})
	if err != nil {
		t.Fatalf("rename failed: %v", err)
	}
	if renamed.Name != "renamed" || !renamed.Enabled {
		t.Fatalf("unexpected task after rename: %+v", renamed)
	}
	if len(renamed.Algorithms) != 1 || renamed.Algorithms[0].SampleFPS != 5 {
		t.Fatalf("algorithms must survive a rename, got %+v", renamed.Algorithms)
	}

	// 只改开关：显式 false 必须真正落库。
	disabled, err := svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{Enabled: boolPtr(false)})
	if err != nil {
		t.Fatalf("disable failed: %v", err)
	}
	if disabled.Enabled {
		t.Fatal("expected Enabled=false to persist")
	}
	reread, err := svc.GetTask(ctx, tsk.ID)
	if err != nil {
		t.Fatalf("GetTask failed: %v", err)
	}
	if reread.Enabled {
		t.Fatal("expected Enabled=false to be stored, got true")
	}

	// 旧的扁平字段：只给区域策略，其余字段不动。
	patched, err := svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{
		RegionConfigJSON: `{"roi": [[0.1, 0.1, 0.4, 0.4]]}`,
	})
	if err != nil {
		t.Fatalf("legacy region patch failed: %v", err)
	}
	sub := patched.Algorithms[0]
	if sub.RegionConfigJSON != `{"roi": [[0.1, 0.1, 0.4, 0.4]]}` {
		t.Fatalf("unexpected region config: %s", sub.RegionConfigJSON)
	}
	if sub.ConfigJSON != `{"threshold": 0.5}` || sub.SampleFPS != 5 {
		t.Fatalf("legacy patch must not touch other fields, got %+v", sub)
	}
}

func TestServiceUnknownAlgorithm(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()

	err := svc.CreateTask(context.Background(), &task.Task{
		CameraID: "cam_unknown_algo",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_missing", SampleFPS: 5},
		},
	})
	assertAppErrCode(t, err, "ALGORITHM_NOT_FOUND")
}

func TestServiceWithoutAlgorithmProvider(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// provider 为 nil 时不做规格校验，由 Store 的预算查询兜底认出"算法不存在"。
	svc := task.NewService(
		store,
		nil,
		nil,
		nil,
	)

	err := svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_no_provider",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_not_in_db", Enabled: true, SampleFPS: 5},
		},
	})
	assertAppErrCode(t, err, "ALGORITHM_NOT_FOUND")
}

func TestServiceMapsClosedStoreToInternalError(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := task.NewService(
		store,
		nil,
		nil,
		zap.NewNop(),
	)

	closeUnderlyingDB(t, db)

	// 驱动错误（database is closed）不得裸抛给调用方。
	assertAppErrCode(t, svc.DeleteTask(ctx, "task_gone"), "INTERNAL_ERROR")
	assertAppErrCode(t, svc.CreateTask(ctx, &task.Task{
		CameraID: "cam_closed",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 5},
		},
	}), "INTERNAL_ERROR")
	// SaveTask 不预检摄像头占用，直接撞在写入失败上，走 mapWriteError 的兜底分支。
	assertAppErrCode(t, svc.SaveTask(ctx, &task.Task{
		CameraID: "cam_closed",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 5},
		},
	}), "TASK_SAVE_FAILED")
}

func TestServiceListTasksFilters(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	for _, tsk := range []*task.Task{
		{
			CameraID: "cam_list_a",
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: "algo_helmet", SampleFPS: 5},
			},
		},
		{
			CameraID: "cam_list_b",
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: "algo_vest", SampleFPS: 5},
			},
		},
	} {
		if err := svc.CreateTask(ctx, tsk); err != nil {
			t.Fatalf("CreateTask %s: %v", tsk.CameraID, err)
		}
	}

	list, err := svc.ListTasks(ctx, "", "")
	if err != nil {
		t.Fatalf("unfiltered list failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(list))
	}

	byCamera, err := svc.ListTasks(ctx, "cam_list_a", "")
	if err != nil {
		t.Fatalf("camera filter failed: %v", err)
	}
	if len(byCamera) != 1 || byCamera[0].CameraID != "cam_list_a" {
		t.Fatalf("unexpected camera filter result: %+v", byCamera)
	}

	byAlgorithm, err := svc.ListTasks(ctx, "", "algo_vest")
	if err != nil {
		t.Fatalf("algorithm filter failed: %v", err)
	}
	if len(byAlgorithm) != 1 || byAlgorithm[0].CameraID != "cam_list_b" {
		t.Fatalf("unexpected algorithm filter result: %+v", byAlgorithm)
	}
}

func TestServiceRejectsNilPayload(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	assertAppErrCode(t, svc.CreateTask(ctx, nil), "INVALID_TASK_CONFIG")
	assertAppErrCode(t, svc.SaveTask(ctx, nil), "INVALID_TASK_CONFIG")
}

// TestServiceRejectsEmptyAlgorithmList 守住「空聚合根」的回流口。
//
// 回归：曾经向 SaveTask/UpdateTask 提交 algorithms: [] 是合法的，Store 会剪掉全部子项、
// 主表行原样保留，于是库里多出一条 enabled=true、algorithms=[] 的“看不见的任务”。
// 它不占算力，却会让前端按“任务行存在”渲染出算力看板与空通道卡片。
func TestServiceRejectsEmptyAlgorithmList(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("create", func(t *testing.T) {
		err := svc.CreateTask(ctx, &task.Task{CameraID: "cam_empty_create", Enabled: true})
		assertAppErrCode(t, err, "TASK_ALGORITHM_REQUIRED")
		if _, err := svc.GetTaskByCamera(ctx, "cam_empty_create"); err == nil {
			t.Fatal("expected no task row to be created for an empty algorithm list")
		}
	})

	t.Run("save", func(t *testing.T) {
		err := svc.SaveTask(ctx, &task.Task{CameraID: "cam_empty_save", Enabled: true})
		assertAppErrCode(t, err, "TASK_ALGORITHM_REQUIRED")
		if _, err := svc.GetTaskByCamera(ctx, "cam_empty_save"); err == nil {
			t.Fatal("expected no task row to be created for an empty algorithm list")
		}
	})

	t.Run("update keeps existing algorithms", func(t *testing.T) {
		created := &task.Task{
			CameraID:   "cam_empty_update",
			Enabled:    true,
			Algorithms: []task.TaskAlgorithm{{AlgorithmID: "algo_helmet", SampleFPS: 5}},
		}
		if err := svc.SaveTask(ctx, created); err != nil {
			t.Fatalf("SaveTask (seed) failed: %v", err)
		}

		stored, err := svc.GetTaskByCamera(ctx, "cam_empty_update")
		if err != nil {
			t.Fatalf("GetTaskByCamera failed: %v", err)
		}

		_, err = svc.UpdateTask(ctx, stored.ID, &task.UpdateTaskRequest{
			Algorithms: []task.TaskAlgorithmItem{},
		})
		assertAppErrCode(t, err, "TASK_ALGORITHM_REQUIRED")

		// 拒绝必须发生在写库之前：原有订阅一字不动。
		after, err := svc.GetTask(ctx, stored.ID)
		if err != nil {
			t.Fatalf("GetTask failed: %v", err)
		}
		if len(after.Algorithms) != 1 || after.Algorithms[0].AlgorithmID != "algo_helmet" {
			t.Fatalf("expected existing algorithm to survive the rejected update, got %+v", after.Algorithms)
		}
	})
}

func TestServiceSaveTaskRejectsCameraConflict(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	for _, cameraID := range []string{"cam_conflict_a", "cam_conflict_b"} {
		if err := svc.SaveTask(ctx, &task.Task{
			CameraID: cameraID,
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: "algo_helmet", SampleFPS: 5},
			},
		}); err != nil {
			t.Fatalf("seed %s: %v", cameraID, err)
		}
	}

	// 用 A 的 ID 把任务挪到 B 的摄像头：Service 层必须映射成 409，而不是 500。
	existing, err := svc.GetTaskByCamera(ctx, "cam_conflict_a")
	if err != nil {
		t.Fatalf("GetTaskByCamera failed: %v", err)
	}

	err = svc.SaveTask(ctx, &task.Task{
		ID:       existing.ID,
		CameraID: "cam_conflict_b",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_helmet", SampleFPS: 5},
		},
	})
	assertAppErrCode(t, err, "TASK_ALREADY_EXISTS")
}

func TestServiceRejectsIncompletePayloads(t *testing.T) {
	svc, _, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("missing algorithm id", func(t *testing.T) {
		err := svc.CreateTask(ctx, &task.Task{
			CameraID:   "cam_no_algo_id",
			Algorithms: []task.TaskAlgorithm{{AlgorithmID: "", SampleFPS: 5}},
		})
		assertAppErrCode(t, err, "VALIDATION_FAILED")
	})

	t.Run("update without task id", func(t *testing.T) {
		_, err := svc.UpdateTask(ctx, "", &task.UpdateTaskRequest{Name: stringPtr("x")})
		assertAppErrCode(t, err, "VALIDATION_FAILED")
	})

	t.Run("update with nil payload", func(t *testing.T) {
		_, err := svc.UpdateTask(ctx, "task_x", nil)
		assertAppErrCode(t, err, "INVALID_REQUEST")
	})

	t.Run("get without task id", func(t *testing.T) {
		_, err := svc.GetTask(ctx, "")
		assertAppErrCode(t, err, "VALIDATION_FAILED")
	})

	t.Run("get by camera without camera id", func(t *testing.T) {
		_, err := svc.GetTaskByCamera(ctx, "")
		assertAppErrCode(t, err, "VALIDATION_FAILED")
	})
}

func TestServiceListTasksWrapsStoreFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()

	svc := task.NewService(store, nil, nil, zap.NewNop())
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("unwrap sql.DB: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql.DB: %v", err)
	}

	_, err = svc.ListTasks(context.Background(), "", "")
	assertAppErrCode(t, err, "INTERNAL_ERROR")
}
