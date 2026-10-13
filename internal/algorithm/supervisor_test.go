package algorithm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func TestSupervisorResetAllowsRestartAfterCrashLoop(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
	algo := supervisorTestAlgorithm()
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	runner := writeFailingRunner(t)
	options := algorithm.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	options.MaxCrashes = 1
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, "", options, zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorithm.StatusCrashLoop)

	if err := supervisor.ResetAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("reset crashed algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusDisabled {
		t.Fatalf("expected disabled status after reset, got %s", got)
	}
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm after reset: %v", err)
	}
	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm after restart: %v", err)
	}
}

func TestServiceDeleteStopsRestartDuringCrashBackoff(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
	algo := supervisorTestAlgorithm()
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	counterPath := filepath.Join(t.TempDir(), "runner-count")
	t.Setenv("RUN_COUNT_FILE", counterPath)
	runner := writeFailingRunner(t)
	options := algorithm.DefaultRecoveryOptions()
	options.InitialBackoff = 250 * time.Millisecond
	options.MaxBackoff = time.Second
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, "", options, zap.NewNop())
	service := algorithm.NewService(store, nil, supervisor, filepath.Join(dataDir, "algorithms"), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorithm.StatusCrashed)

	if err := service.DeleteAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("delete crashed algorithm: %v", err)
	}
	time.Sleep(350 * time.Millisecond)

	countBytes, err := os.ReadFile(counterPath)
	if err != nil {
		t.Fatalf("read runner count: %v", err)
	}
	if count := strings.Count(string(countBytes), "run\n"); count != 1 {
		t.Fatalf("expected deleted algorithm not to restart, runner executed %d times", count)
	}
}

func supervisorTestAlgorithm() *algorithm.Algorithm {
	return &algorithm.Algorithm{
		ID: "supervisor_test", Name: "supervisor_test", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySafety,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 1,
		MemoryMB: 64, Status: algorithm.StatusDisabled,
	}
}

func writeFailingRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "runner.sh")
	script := "#!/bin/sh\nprintf 'run\\n' >> \"$RUN_COUNT_FILE\"\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake runner: %v", err)
	}
	return path
}

func waitForSupervisorStatus(t *testing.T, supervisor *algorithm.Supervisor, id, want string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := supervisor.GetStatus(id); got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("supervisor status did not reach %s; got %s", want, supervisor.GetStatus(id))
}
