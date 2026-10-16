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

type mockAlgoProvider struct {
	specs map[string]*task.AlgorithmSpec
}

func (m *mockAlgoProvider) GetAlgorithmSpec(ctx context.Context, idOrName string) (*task.AlgorithmSpec, error) {
	if spec, ok := m.specs[idOrName]; ok {
		return spec, nil
	}
	return nil, errors.New("algorithm not found")
}

type mockTaskChangeListener struct {
	mu            sync.Mutex
	notifications []string
}

func (m *mockTaskChangeListener) OnTaskChanged(ctx context.Context, algorithmID string) {
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

	insertMockAlgorithm(t, db, "algo_helmet", "helmet_detector", 20)

	provider := &mockAlgoProvider{
		specs: map[string]*task.AlgorithmSpec{
			"algo_helmet": {
				ID:               "algo_helmet",
				Name:             "helmet_detector",
				ComputeCost:      20,
				ConfigSchemaJSON: `{"type": "object", "properties": {"threshold": {"type": "number"}}}`,
			},
		},
	}
	listener := &mockTaskChangeListener{}
	svc := task.NewService(store, provider, listener, zap.NewNop())

	return svc, listener, cleanup
}

func TestServiceCreateTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	// 1. Missing CameraID
	err := svc.CreateTask(ctx, &task.Task{AlgorithmID: "algo_helmet"})
	if err == nil {
		t.Fatal("expected error on missing CameraID")
	}

	// 2. Normal creation
	tsk := &task.Task{
		CameraID:    "cam_100",
		AlgorithmID: "algo_helmet",
		SampleFPS:   5,
		ConfigJSON:  `{"threshold": 0.8}`,
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

	// 3. Duplicate task for same camera and algorithm
	dup := &task.Task{
		CameraID:    "cam_100",
		AlgorithmID: "algo_helmet",
	}
	err = svc.CreateTask(ctx, dup)
	if err == nil {
		t.Fatal("expected conflict on duplicate task")
	}
	var appErr *apperr.Error
	if errors.As(err, &appErr) {
		if appErr.Code != "TASK_ALREADY_EXISTS" {
			t.Fatalf("expected TASK_ALREADY_EXISTS code, got %s", appErr.Code)
		}
	} else {
		t.Fatalf("expected apperr.Error, got %T", err)
	}

	// 4. Invalid config schema
	invalidCfg := &task.Task{
		CameraID:    "cam_101",
		AlgorithmID: "algo_helmet",
		ConfigJSON:  `{"threshold": "not_a_number"}`,
	}
	err = svc.CreateTask(ctx, invalidCfg)
	if err == nil {
		t.Fatal("expected schema validation failure")
	}
}

func TestServiceUpdateTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tsk := &task.Task{
		CameraID:    "cam_200",
		AlgorithmID: "algo_helmet",
		SampleFPS:   5,
		ConfigJSON:  `{"threshold": 0.5}`,
	}
	if err := svc.CreateTask(ctx, tsk); err != nil {
		t.Fatalf("CreateTask failed: %v", err)
	}

	fps := 15
	cfg := `{"threshold": 0.9}`
	updated, err := svc.UpdateTask(ctx, tsk.ID, &task.UpdateTaskRequest{
		SampleFPS:  fps,
		ConfigJSON: cfg,
	})
	if err != nil {
		t.Fatalf("UpdateTask failed: %v", err)
	}
	if updated.SampleFPS != 15 || updated.ConfigJSON != cfg {
		t.Fatalf("unexpected updated fields: %+v", updated)
	}
	if listener.count() != 2 {
		t.Fatalf("expected 2 notifications, got %d", listener.count())
	}
}

func TestServiceDeleteTask(t *testing.T) {
	svc, listener, cleanup := setupTestService(t)
	defer cleanup()
	ctx := context.Background()

	tsk := &task.Task{
		CameraID:    "cam_300",
		AlgorithmID: "algo_helmet",
		SampleFPS:   5,
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
