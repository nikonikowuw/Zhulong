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
		_ = dbStore.Close()
	}
	return store, dbStore.DB(), cleanup
}

func insertMockAlgorithm(t *testing.T, db *gorm.DB, id, name string, computeCost int) {
	t.Helper()
	err := db.Exec(`INSERT INTO algorithms (
		id, name, version, task_type, category, target_arch, target_device,
		compute_cost, memory_mb, description, manifest_json, config_schema_json,
		status, previous_version, created_at, updated_at
	) VALUES (?, ?, '1.0.0', 'detection', 'safety', 'x86_64', 'cpu', ?, 64, '', '{}', '{}', 'ACTIVE', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, name, computeCost,
	).Error
	if err != nil {
		t.Fatalf("failed to insert mock algorithm: %v", err)
	}
}

func TestStoreTaskCRUD(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_01", "helmet_detector", 10)

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
	if found.CameraID != "cam_01" || len(found.Algorithms) != 1 || found.Algorithms[0].SampleFPS != 5 {
		t.Fatalf("unexpected found task: %+v", found)
	}

	foundByCam, err := store.GetTaskByCameraAndAlgorithm(ctx, "cam_01", "algo_01")
	if err != nil {
		t.Fatalf("GetTaskByCameraAndAlgorithm failed: %v", err)
	}
	if foundByCam.ID != "task_01" {
		t.Fatalf("expected task_01, got %s", foundByCam.ID)
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
	if err := store.UpdateTask(ctx, tsk); err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	updated, _ := store.GetTask(ctx, "task_01")
	if len(updated.Algorithms) != 1 || updated.Algorithms[0].SampleFPS != 10 {
		t.Fatalf("expected updated SampleFPS 10, got %d", updated.Algorithms[0].SampleFPS)
	}

	if err := store.DeleteTask(ctx, "task_01"); err != nil {
		t.Fatalf("DeleteTask failed: %v", err)
	}

	if _, err := store.GetTask(ctx, "task_01"); err != task.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestStoreTaskBudget(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// 算法算力为 100 点
	insertMockAlgorithm(t, db, "heavy_algo", "heavy_detector", 100)

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

	insertMockAlgorithm(t, db, "algo_02", "face_detector", 10)

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
	if stored.Enabled != false {
		t.Fatalf("expected parent Enabled=false to persist, got Enabled=%v", stored.Enabled)
	}
	if len(stored.Algorithms) != 1 || stored.Algorithms[0].Enabled != false {
		t.Fatalf("expected child Enabled=false to persist, got %+v", stored.Algorithms)
	}
}

func TestStoreSaveTaskSyncAlgorithms(t *testing.T) {
	store, db, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	insertMockAlgorithm(t, db, "algo_a", "algo_a", 10)
	insertMockAlgorithm(t, db, "algo_b", "algo_b", 20)
	insertMockAlgorithm(t, db, "algo_c", "algo_c", 30)

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

	algoMap := make(map[string]task.TaskAlgorithm)
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
