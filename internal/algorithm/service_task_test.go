package algorithm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// newTaskService builds a Service backed by a real migrated DB and a supervisor
// whose runner is `sleep`, so package restarts are observable without a native
// runner binary.
func newTaskService(t *testing.T) (*algorithm.Service, *algorithm.Supervisor, *algorithm.Store, func()) {
	t.Helper()
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
	// A long-lived stub runner: `sleep` with no argument exits immediately, which
	// would let the package crash out from under the debounce assertions.
	runnerPath := filepath.Join(dataDir, "stub_runner.sh")
	if err := os.WriteFile(runnerPath, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatalf("write stub runner: %v", err)
	}
	supervisor := algorithm.NewSupervisor(store, dataDir, runnerPath, algorithm.DefaultRecoveryOptions(), zap.NewNop())
	verifier := algorithm.NewVerifier("", "cpu")
	service := algorithm.NewService(store, verifier, supervisor, dataDir, zap.NewNop())

	cleanup := func() {
		supervisor.StopAll(time.Second)
		_ = dbStore.Close()
	}
	return service, supervisor, store, cleanup
}

func seedTaskAlgorithm(t *testing.T, store *algorithm.Store) *algorithm.Algorithm {
	t.Helper()
	algo := &algorithm.Algorithm{
		ID: "task_algo", Name: "task_algo", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 1,
		MemoryMB: 64, Status: algorithm.StatusActive,
	}
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}
	return algo
}

// TestServiceUpdateTaskPersistsAndTriggersRestart covers the Service.UpdateTask
// path that the task-change trigger (D5) requires: it did not exist before, so
// sample_fps / enabled edits had no way to reach the running package.
func TestServiceUpdateTaskPersistsAndTriggersRestart(t *testing.T) {
	service, supervisor, store, cleanup := newTaskService(t)
	defer cleanup()
	algo := seedTaskAlgorithm(t, store)

	ctx := context.Background()
	task := &algorithm.AnalysisTask{
		ID: "task_1", CameraID: "cam_1", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 5, ConfigJSON: "{}",
	}
	if err := service.CreateTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	task.SampleFPS = 20
	if err := service.UpdateTask(ctx, task); err != nil {
		t.Fatalf("update task: %v", err)
	}
	service.FlushPendingRestarts()

	stored, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if stored.SampleFPS != 20 {
		t.Fatalf("expected persisted sample_fps 20, got %d", stored.SampleFPS)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected package running after task update, got %s", got)
	}
}

// TestServiceUpdateTaskRejectsNegativeSampleFPS guards the int -> uint32
// conversion: Store.UpdateTask does no validation, so a negative sample_fps
// would otherwise become ~4.29e9 fps at the publish throttle.
func TestServiceUpdateTaskRejectsNegativeSampleFPS(t *testing.T) {
	service, _, store, cleanup := newTaskService(t)
	defer cleanup()
	algo := seedTaskAlgorithm(t, store)

	ctx := context.Background()
	task := &algorithm.AnalysisTask{
		ID: "task_neg", CameraID: "cam_1", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 5, ConfigJSON: "{}",
	}
	if err := service.CreateTask(ctx, task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	task.SampleFPS = -1
	err := service.UpdateTask(ctx, task)
	if !errors.Is(err, algorithm.ErrInvalidTaskConfig) {
		t.Fatalf("expected ErrInvalidTaskConfig for negative sample_fps, got %v", err)
	}
}

// countingBrokerPathResolver counts how many times the supervisor resolved
// broker paths, which is once per StartAlgorithm (i.e. once per restart).
type countingBrokerPathResolver struct {
	calls atomic.Int64
}

func (m *countingBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	m.calls.Add(1)
	return nil, nil
}

// TestServiceTaskChangeDebouncesRestart guards design.md §7.4: a burst of task
// changes on one package must collapse into a single restart, not N restarts.
func TestServiceTaskChangeDebouncesRestart(t *testing.T) {
	service, supervisor, store, cleanup := newTaskService(t)
	defer cleanup()
	algo := seedTaskAlgorithm(t, store)

	resolver := &countingBrokerPathResolver{}
	supervisor.SetBrokerSubscriptionResolver(resolver)

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	afterInitialStart := resolver.calls.Load()

	// 20 rapid task changes on distinct cameras must not restart the package 20
	// times. Without the debounce this loop would thrash
	// StopAlgorithm/StartAlgorithm once per task.
	for i := 0; i < 20; i++ {
		task := &algorithm.AnalysisTask{
			ID: "burst_" + strconv.Itoa(i), CameraID: "cam_" + strconv.Itoa(i),
			AlgorithmID: algo.ID, Enabled: true, SampleFPS: 5, ConfigJSON: "{}",
		}
		if err := service.CreateTask(ctx, task); err != nil {
			t.Fatalf("create task %d: %v", i, err)
		}
	}

	service.FlushPendingRestarts()

	restarts := resolver.calls.Load() - afterInitialStart
	if restarts != 1 {
		t.Fatalf("expected the 20-task burst to coalesce into exactly 1 restart, got %d", restarts)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected package running after burst, got %s", got)
	}
}
