package algorithm_test

import (
	"context"
	"errors"
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
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, options, zap.NewNop())
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
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, options, zap.NewNop())
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

type mockBrokerPathResolver struct {
	paths []string
}

func (m *mockBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	return m.paths, nil
}

func TestSupervisorBrokerPathResolverAndRestart(t *testing.T) {
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

	runner := "sleep"
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, algorithm.DefaultRecoveryOptions(), zap.NewNop())
	resolver := &mockBrokerPathResolver{
		paths: []string{"/tmp/mock_b1.sock", "/tmp/mock_b2.sock"},
	}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		_ = dbStore.Close()
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected status running, got %s", got)
	}

	// Test RestartAlgorithm (D5)
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm failed: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected status running after restart, got %s", got)
	}

	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm: %v", err)
	}
}

// failingBrokerPathResolver always fails, simulating a task lookup or camera
// resolution error while the supervisor is starting a package.
type failingBrokerPathResolver struct{ err error }

func (m *failingBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	return nil, m.err
}

// TestSupervisorStartFailsWhenBrokerResolutionFails guards the design.md §7.2
// cold-path trap: a swallowed resolution error would degrade to "no broker",
// letting the runner exit 5, back off, and trip the CRASH_LOOP breaker with the
// real cause hidden. The start must instead fail loudly and start nothing.
func TestSupervisorStartFailsWhenBrokerResolutionFails(t *testing.T) {
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

	resolveErr := errors.New("camera lookup exploded")
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), "sleep", algorithm.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(&failingBrokerPathResolver{err: resolveErr})
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		_ = dbStore.Close()
	})

	err := supervisor.StartAlgorithm(context.Background(), *algo)
	if err == nil {
		t.Fatal("expected StartAlgorithm to fail when broker path resolution fails")
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected the resolution error to be wrapped, got %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusDisabled {
		t.Fatalf("expected no watcher to be registered, got status %s", got)
	}
}

// TestSupervisorRestartPreservesActiveStatus guards that a task-driven restart
// is not a disable: StopAlgorithm writes DISABLED, so RestartAlgorithm must
// restore ACTIVE, otherwise a successful task change silently disables the
// package until the next host reboot.
func TestSupervisorRestartPreservesActiveStatus(t *testing.T) {
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

	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), "sleep", algorithm.DefaultRecoveryOptions(), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
		_ = dbStore.Close()
	})

	ctx := context.Background()
	if err := store.UpdateAlgorithmStatus(ctx, algo.ID, algorithm.StatusActive); err != nil {
		t.Fatalf("mark algorithm active: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm: %v", err)
	}

	stored, err := store.GetAlgorithm(ctx, algo.ID)
	if err != nil {
		t.Fatalf("reload algorithm: %v", err)
	}
	if stored.Status != algorithm.StatusActive {
		t.Fatalf("restart must preserve ACTIVE, got %s", stored.Status)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected running status after restart, got %s", got)
	}
}
