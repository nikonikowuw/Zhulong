package app

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// TestSyncBrokerSubscriptionsRequiresWiring guards the defensive path: an unwired
// resolver must not fabricate broker paths, otherwise the supervisor would pass
// bogus --broker arguments to the runner.
func TestSyncBrokerSubscriptionsRequiresWiring(t *testing.T) {
	resolver := &appBrokerPathResolver{}
	paths, err := resolver.SyncBrokerSubscriptions(context.Background(), "algo_a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(paths) != 0 {
		t.Fatalf("expected no paths from an unwired resolver, got %v", paths)
	}
}

// TestSyncBrokerSubscriptionsPropagatesTaskLookupFailure guards that a task lookup
// error surfaces instead of degrading to "no broker" (which would let the
// runner exit 5 and trip the CRASH_LOOP breaker with the cause hidden).
func TestSyncBrokerSubscriptionsPropagatesTaskLookupFailure(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := dbStore.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	// A closed database makes ListTasksByAlgorithm fail.
	store := algorithm.NewStore(dbStore)
	hub := camera.NewStreamHub(nil, nil, nil, nil, nil, zap.NewNop())
	resolver := &appBrokerPathResolver{store: store, streamHub: hub}

	if _, err := resolver.SyncBrokerSubscriptions(context.Background(), "algo_a"); err == nil {
		t.Fatal("expected SyncBrokerSubscriptions to fail when the task lookup fails")
	}
}

// TestPublishRateUsesCrossPackageMax covers AC10: one broker has a single
// publish rate, so the rate for a camera must be the max sample_fps across ALL
// enabled tasks on that camera, not just the package being resolved.
func TestPublishRateUsesCrossPackageMax(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer func() { _ = dbStore.Close() }()

	store := algorithm.NewStore(dbStore)
	resolver := &appBrokerPathResolver{store: store, streamHub: nil}
	ctx := context.Background()

	for _, id := range []string{"algo_fast", "algo_slow"} {
		algo := &algorithm.Algorithm{
			ID: id, Name: id, Version: "1.0.0",
			TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
			TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 1,
			MemoryMB: 64, Status: algorithm.StatusActive,
		}
		if err := store.CreateAlgorithm(ctx, algo); err != nil {
			t.Fatalf("create algorithm %s: %v", id, err)
		}
	}

	// Two packages subscribe to cam_1 with different sample rates.
	tasks := []*algorithm.AnalysisTask{
		{ID: "t_slow", CameraID: "cam_1", AlgorithmID: "algo_slow", Enabled: true, SampleFPS: 5, ConfigJSON: "{}"},
		{ID: "t_fast", CameraID: "cam_1", AlgorithmID: "algo_fast", Enabled: true, SampleFPS: 25, ConfigJSON: "{}"},
		// A disabled task on a distinct camera must not influence any rate.
		{ID: "t_disabled", CameraID: "cam_2", AlgorithmID: "algo_slow", Enabled: false, SampleFPS: 100, ConfigJSON: "{}"},
	}
	for _, task := range tasks {
		if err := store.CreateTask(ctx, task); err != nil {
			t.Fatalf("create task %s: %v", task.ID, err)
		}
	}

	rates, err := resolver.publishRateByCamera(ctx)
	if err != nil {
		t.Fatalf("publishRateByCamera: %v", err)
	}
	if got := rates["cam_1"]; got != 25 {
		t.Fatalf("expected cross-package max 25 fps for cam_1, got %d", got)
	}
	if _, ok := rates["cam_2"]; ok {
		t.Fatalf("a disabled task must not produce a publish rate, got %v", rates)
	}
}
