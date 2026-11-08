package task_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/task"
	"gorm.io/gorm"
)

// 本文件用**真实 SQLite + GORM 回调**注入故障来覆盖写路径的错误传播分支：
// 不引入 mock ORM，也不改生产代码，断言的是"错误被包装且 cause 留在链上"。

const (
	faultCreate = "create"
	faultUpdate = "update"
	faultDelete = "delete"
	faultQuery  = "query"
	faultRow    = "row"
)

type faultSpec struct {
	processor string
	nth       int
	err       error
}

// injectFault 让指定 GORM 处理器的第 nth 次调用（1 起）返回 spec.err。
func injectFault(t *testing.T, db *gorm.DB, spec faultSpec) {
	t.Helper()

	var calls int
	hook := func(tx *gorm.DB) {
		calls++
		if calls == spec.nth {
			tx.AddError(spec.err)
		}
	}
	name := fmt.Sprintf("zhulong:test_fault:%s:%d", spec.processor, spec.nth)

	var err error
	switch spec.processor {
	case faultCreate:
		err = db.Callback().Create().Before("gorm:create").Register(name, hook)
	case faultUpdate:
		err = db.Callback().Update().Before("gorm:update").Register(name, hook)
	case faultDelete:
		err = db.Callback().Delete().Before("gorm:delete").Register(name, hook)
	case faultQuery:
		err = db.Callback().Query().Before("gorm:query").Register(name, hook)
	case faultRow:
		// Scan 走的是 Row 处理器而不是 Query。
		err = db.Callback().Row().Before("gorm:row").Register(name, hook)
	default:
		t.Fatalf("unknown gorm processor %q", spec.processor)
	}
	if err != nil {
		t.Fatalf("register %s fault hook: %v", spec.processor, err)
	}
}

// neutralizeDeletes 把 DELETE 换成空操作，模拟"行在读写之间被别的请求删掉"的竞态。
func neutralizeDeletes(t *testing.T, db *gorm.DB) {
	t.Helper()
	err := db.Callback().Delete().Replace("gorm:delete", func(tx *gorm.DB) {
		tx.RowsAffected = 0
	})
	if err != nil {
		t.Fatalf("replace delete callback: %v", err)
	}
}

// seedTask 直接写一条任务（用 CreateTask，避免预算语义干扰）。
func seedTask(t *testing.T, store *task.Store, cameraID string, algorithmIDs ...string) *task.Task {
	t.Helper()
	tsk := &task.Task{
		CameraID:   cameraID,
		Enabled:    false,
		Name:       cameraID,
		Algorithms: make([]task.TaskAlgorithm, 0, len(algorithmIDs)),
	}
	for i, id := range algorithmIDs {
		tsk.Algorithms = append(tsk.Algorithms, task.TaskAlgorithm{
			AlgorithmID: id,
			Enabled:     true,
			SampleFPS:   5 + i,
			ConfigJSON:  "{}",
		})
	}
	if err := store.CreateTask(context.Background(), tsk); err != nil {
		t.Fatalf("seed task %s: %v", cameraID, err)
	}
	return tsk
}

func TestStoreCreateTaskPropagatesDriverFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	fault := errors.New("injected: disk I/O error")
	injectFault(t, db, faultSpec{processor: faultCreate, nth: 1, err: fault})

	err := store.CreateTask(ctx, &task.Task{CameraID: "cam_fault_insert"})
	if !errors.Is(err, fault) {
		t.Fatalf("expected the driver error to be wrapped, got %v", err)
	}
}

func TestStoreCreateTaskWithinBudgetPropagatesDriverFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	fault := errors.New("injected: database or disk is full")
	injectFault(t, db, faultSpec{processor: faultCreate, nth: 1, err: fault})

	// Enabled=false 让预算校验直接放行，把断言点收敛到 INSERT 失败。
	err := store.CreateTaskWithinBudget(ctx, &task.Task{CameraID: "cam_fault_budget"}, 1000)
	if !errors.Is(err, fault) {
		t.Fatalf("expected the driver error to be wrapped, got %v", err)
	}
}

func TestStoreSaveTaskPropagatesDriverFailures(t *testing.T) {
	roiAlgorithm := "algo_fault"

	t.Run("existing task lookup", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		insertMockAlgorithm(
			t,
			db,
			roiAlgorithm,
			"fault_detector",
			10,
		)
		seedTask(t, store, "cam_lookup", roiAlgorithm)

		fault := errors.New("injected: query failure")
		injectFault(t, db, faultSpec{processor: faultQuery, nth: 1, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{CameraID: "cam_lookup"}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected lookup failure to be wrapped, got %v", err)
		}
	})

	t.Run("camera owner lookup", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		fault := errors.New("injected: owner lookup failure")
		// 第 1 次查询是按 ID 定位（未命中），第 2 次是摄像头占用检查。
		injectFault(t, db, faultSpec{processor: faultQuery, nth: 2, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       "task_fault_owner",
			CameraID: "cam_fault_owner",
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected owner lookup failure to be wrapped, got %v", err)
		}
	})

	t.Run("insert new task row", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		fault := errors.New("injected: insert failure")
		injectFault(t, db, faultSpec{processor: faultCreate, nth: 1, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       "task_fault_new",
			CameraID: "cam_fault_new",
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected insert failure to be wrapped, got %v", err)
		}
	})

	t.Run("update task row", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		insertMockAlgorithm(
			t,
			db,
			roiAlgorithm,
			"fault_detector",
			10,
		)
		seeded := seedTask(t, store, "cam_update", roiAlgorithm)

		fault := errors.New("injected: update failure")
		injectFault(t, db, faultSpec{processor: faultUpdate, nth: 1, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       seeded.ID,
			CameraID: "cam_update",
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: roiAlgorithm, Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
			},
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected update failure to be wrapped, got %v", err)
		}
	})

	t.Run("prune removed algorithms", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		insertMockAlgorithm(
			t,
			db,
			roiAlgorithm,
			"fault_detector",
			10,
		)
		seeded := seedTask(t, store, "cam_prune", roiAlgorithm)

		fault := errors.New("injected: delete failure")
		injectFault(t, db, faultSpec{processor: faultDelete, nth: 1, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       seeded.ID,
			CameraID: "cam_prune",
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected prune failure to be wrapped, got %v", err)
		}
	})

	t.Run("update existing algorithm", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		insertMockAlgorithm(
			t,
			db,
			roiAlgorithm,
			"fault_detector",
			10,
		)
		seeded := seedTask(t, store, "cam_algo_update", roiAlgorithm)

		fault := errors.New("injected: algorithm update failure")
		// 第 1 次 UPDATE 写主表，第 2 次写算法子项。
		injectFault(t, db, faultSpec{processor: faultUpdate, nth: 2, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       seeded.ID,
			CameraID: "cam_algo_update",
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: roiAlgorithm, Enabled: true, SampleFPS: 9, ConfigJSON: "{}"},
			},
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected algorithm update failure to be wrapped, got %v", err)
		}
	})

	t.Run("insert new algorithm", func(t *testing.T) {
		store, db, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		insertMockAlgorithm(
			t,
			db,
			roiAlgorithm,
			"fault_detector",
			10,
		)
		insertMockAlgorithm(
			t,
			db,
			"algo_fault_extra",
			"fault_extra",
			10,
		)
		seeded := seedTask(t, store, "cam_algo_insert", roiAlgorithm)

		fault := errors.New("injected: algorithm insert failure")
		injectFault(t, db, faultSpec{processor: faultCreate, nth: 1, err: fault})

		err := store.SaveTaskWithinBudget(ctx, &task.Task{
			ID:       seeded.ID,
			CameraID: "cam_algo_insert",
			Algorithms: []task.TaskAlgorithm{
				{AlgorithmID: roiAlgorithm, Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
				{AlgorithmID: "algo_fault_extra", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
			},
		}, 1000)
		if !errors.Is(err, fault) {
			t.Fatalf("expected algorithm insert failure to be wrapped, got %v", err)
		}
	})
}

func TestStoreDeleteTaskPropagatesDriverFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seeded := seedTask(t, store, "cam_delete_fault")

	fault := errors.New("injected: delete failure")
	injectFault(t, db, faultSpec{processor: faultDelete, nth: 1, err: fault})

	if err := store.DeleteTask(ctx, seeded.ID); !errors.Is(err, fault) {
		t.Fatalf("expected delete failure to be wrapped, got %v", err)
	}
}

// injectFaultOnQueryDest 只在查询的扫描目标匹配时才注入故障，用来精确打中某一条 SELECT。
func injectFaultOnQueryDest(
	t *testing.T,
	db *gorm.DB,
	match func(dest any) bool,
	fault error,
) {
	t.Helper()

	hook := func(tx *gorm.DB) {
		if tx.Statement != nil && match(tx.Statement.Dest) {
			tx.AddError(fault)
		}
	}
	err := db.Callback().Query().Before("gorm:query").Register("zhulong:test_fault:by_dest", hook)
	if err != nil {
		t.Fatalf("register query fault hook: %v", err)
	}
}

func TestStoreSaveTaskFailsOnAlgorithmLookup(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_fault_lookup",
		"fault_detector",
		10,
	)
	seeded := seedTask(t, store, "cam_algo_lookup", "algo_fault_lookup")

	fault := errors.New("injected: algorithm lookup failure")
	// 只打中"按 (task_id, algorithm_id) 定位子项"的查询，主表查询照常成功。
	injectFaultOnQueryDest(
		t,
		db,
		func(dest any) bool {
			_, ok := dest.(*task.TaskAlgorithm)
			return ok
		},
		fault,
	)

	err := store.SaveTaskWithinBudget(ctx, &task.Task{
		ID:       seeded.ID,
		CameraID: "cam_algo_lookup",
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_fault_lookup", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}, 1000)
	if !errors.Is(err, fault) {
		t.Fatalf("expected algorithm lookup failure to be wrapped, got %v", err)
	}
}

func TestStoreListTasksReportsQueryFailure(t *testing.T) {
	fault := errors.New("injected: query failure")

	tests := []struct {
		name string
		call func(store *task.Store) error
	}{
		{
			name: "ListTasks",
			call: func(store *task.Store) error {
				_, err := store.ListTasks(context.Background())
				return err
			},
		},
		{
			name: "ListTasksByCamera",
			call: func(store *task.Store) error {
				_, err := store.ListTasksByCamera(context.Background(), "cam_x")
				return err
			},
		},
		{
			name: "ListTasksByAlgorithm",
			call: func(store *task.Store) error {
				_, err := store.ListTasksByAlgorithm(context.Background(), "algo_x")
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, db, cleanup := setupTestStore(t)
			defer cleanup()

			injectFault(t, db, faultSpec{processor: faultQuery, nth: 1, err: fault})
			if err := test.call(store); !errors.Is(err, fault) {
				t.Fatalf("expected the query failure to surface, got %v", err)
			}
		})
	}
}

func TestStoreBudgetScanFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	fault := errors.New("injected: allocation scan failure")
	// 预算汇总的第一次查询就是扫描其它任务的分配表（Scan 走 Row 处理器）。
	injectFault(t, db, faultSpec{processor: faultRow, nth: 1, err: fault})

	err := store.CreateTaskWithinBudget(ctx, &task.Task{CameraID: "cam_scan_fault"}, 1000)
	if !errors.Is(err, fault) {
		t.Fatalf("expected the scan failure to surface, got %v", err)
	}
}

func TestStoreCreateTaskProbeFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_probe",
		"probe_detector",
		10,
	)
	// 先占位，制造出 camera_id 冲突。
	seedTask(t, store, "cam_probe", "algo_probe")

	insertFault := errors.New("injected: insert failure")
	injectFault(t, db, faultSpec{processor: faultCreate, nth: 1, err: insertFault})
	// 归因探针（COUNT）也失败：错误不能被误判成"摄像头已被占用"。
	injectFaultOnQueryDest(
		t,
		db,
		func(dest any) bool {
			_, ok := dest.(*int64)
			return ok
		},
		errors.New("injected: count failure"),
	)

	err := store.CreateTaskWithinBudget(ctx, &task.Task{
		CameraID: "cam_probe",
		Enabled:  false,
	}, 1000)
	if errors.Is(err, task.ErrTaskAlreadyExists) {
		t.Fatal("a failed probe must not be reported as a camera conflict")
	}
	if !errors.Is(err, insertFault) {
		t.Fatalf("expected the insert failure to be wrapped, got %v", err)
	}
}

func TestStoreBudgetDetectsExhaustedState(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_exhaust",
		"exhaust_detector",
		100,
	)

	// 用无预算校验的 CreateTask 造出一个"已超额"的脏状态。
	if err := store.CreateTask(ctx, &task.Task{
		CameraID: "cam_exhausted",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_exhaust", Enabled: true, SampleFPS: 100, ConfigJSON: "{}"},
		},
	}); err != nil {
		t.Fatalf("seed exhausted task: %v", err)
	}

	// 任何后续写入都必须被拦下——哪怕本次任务本身是停用的。
	err := store.CreateTaskWithinBudget(ctx, &task.Task{CameraID: "cam_after_exhaust"}, 1000)
	if !errors.Is(err, task.ErrComputeBudgetExceeded) {
		t.Fatalf("expected ErrComputeBudgetExceeded, got %v", err)
	}
}

func TestStoreAddTaskComputePointsQueryFailure(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(
		t,
		db,
		"algo_cost_lookup",
		"cost_detector",
		10,
	)
	seeded := seedTask(t, store, "cam_cost_lookup", "algo_cost_lookup")

	fault := errors.New("injected: cost lookup failure")
	// Query 处理器上的顺序：主表定位(1)、摄像头占用检查(2)、算法成本查询(3)。
	// 预算汇总用的是 Scan，走 Row 处理器，不计入这里。
	injectFault(t, db, faultSpec{processor: faultQuery, nth: 3, err: fault})

	err := store.SaveTaskWithinBudget(ctx, &task.Task{
		ID:       seeded.ID,
		CameraID: "cam_cost_lookup",
		Enabled:  true,
		Algorithms: []task.TaskAlgorithm{
			{AlgorithmID: "algo_cost_lookup", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		},
	}, 1000)
	if !errors.Is(err, fault) {
		t.Fatalf("expected the cost lookup failure to surface, got %v", err)
	}
}
