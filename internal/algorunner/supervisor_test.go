package algorunner_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

type mockLifecycleStore struct {
	mu       sync.Mutex
	packages map[string]algorunner.PackageMeta
}

func newMockLifecycleStore() *mockLifecycleStore {
	return &mockLifecycleStore{packages: make(map[string]algorunner.PackageMeta)}
}

func (m *mockLifecycleStore) GetAlgorithmPackage(ctx context.Context, id string) (algorunner.PackageMeta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, ok := m.packages[id]
	if !ok {
		return algorunner.PackageMeta{}, errors.New("package not found")
	}
	return pkg, nil
}

func (m *mockLifecycleStore) UpdateAlgorithmStatus(ctx context.Context, id string, status string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, ok := m.packages[id]
	if !ok {
		return errors.New("package not found")
	}
	pkg.Status = status
	m.packages[id] = pkg
	return nil
}

func (m *mockLifecycleStore) RollbackAlgorithmVersion(ctx context.Context, id string, targetVersion, failedVersion string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	pkg, ok := m.packages[id]
	if !ok {
		return errors.New("package not found")
	}
	pkg.Version = targetVersion
	pkg.PreviousVersion = failedVersion
	pkg.Status = algorunner.StatusActive
	m.packages[id] = pkg
	return nil
}

func supervisorTestPackage() algorunner.PackageMeta {
	return algorunner.PackageMeta{
		ID:              "supervisor_test",
		Name:            "supervisor_test",
		Version:         "1.0.0",
		PreviousVersion: "",
		Status:          algorunner.StatusDisabled,
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

func writeIdleRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "idle_runner.sh")
	script := "#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write idle runner: %v", err)
	}
	return path
}

func writeBrokerArgRecordingRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recording_runner.sh")
	script := "#!/bin/sh\nprintf 'start\\n' >> \"$RUN_ARG_FILE\"\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\nsleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording runner: %v", err)
	}
	return path
}

func readRecordedRunnerInvocations(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read runner arg file: %v", err)
	}
	var out [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if line == "start" {
			out = append(out, []string{})
			continue
		}
		if len(out) == 0 {
			continue
		}
		out[len(out)-1] = append(out[len(out)-1], line)
	}
	return out
}

func brokerFlagsIn(args []string) []string {
	var socks []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--broker" {
			socks = append(socks, args[i+1])
		}
	}
	return socks
}

func waitForRecordedInvocations(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(readRecordedRunnerInvocations(t, path)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner invocations did not reach %d; got %d", want, len(readRecordedRunnerInvocations(t, path)))
}

func waitForSupervisorStatus(t *testing.T, supervisor *algorunner.Supervisor, id, want string) {
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

func findRunnerPID(t *testing.T, marker string) int {
	t.Helper()
	if runtime.GOOS != "linux" {
		out, err := exec.Command("ps", "-eo", "pid,command").Output()
		if err != nil {
			t.Fatalf("ps: %v", err)
		}
		self := os.Getpid()
		lines := strings.Split(string(out), "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) < 2 {
				continue
			}
			pid, convErr := strconv.Atoi(fields[0])
			if convErr != nil || pid == self {
				continue
			}
			if strings.Contains(line, "recording_runner.sh") && strings.Contains(line, marker) {
				return pid
			}
		}
		return 0
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	self := os.Getpid()
	for _, entry := range entries {
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid == self {
			continue
		}
		cmdline, readErr := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if readErr != nil {
			continue
		}
		line := strings.ReplaceAll(string(cmdline), "\x00", " ")
		if strings.Contains(line, "recording_runner.sh") && strings.Contains(line, marker) {
			return pid
		}
	}
	return 0
}

type mockBrokerPathResolver struct {
	paths []string
}

func (m *mockBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	return m.paths, nil
}

type failingBrokerPathResolver struct{ err error }

func (m *failingBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	return nil, m.err
}

func TestSupervisorResetAllowsRestartAfterCrashLoop(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runner := writeFailingRunner(t)
	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	options.MaxCrashes = 1
	supervisor := algorunner.NewSupervisor(store, t.TempDir(), runner, options, zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorunner.StatusCrashLoop)

	if err := supervisor.ResetAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("reset crashed algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected disabled status after reset, got %s", got)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm after reset: %v", err)
	}
	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm after restart: %v", err)
	}
}

func TestSupervisorCrashRestartKeepsBrokerList(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runner := writeBrokerArgRecordingRunner(t)

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := algorunner.NewSupervisor(store, t.TempDir(), runner, options, zap.NewNop())

	wantSocks := []string{"/tmp/zhulong_broker_7.sock", "/tmp/zhulong_broker_9.sock"}
	supervisor.SetBrokerSubscriptionResolver(&mockBrokerPathResolver{paths: wantSocks})
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	waitForRecordedInvocations(t, argFile, 1)

	first := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(first[0]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("first start broker flags = %v, want %v", got, wantSocks)
	}

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(second[1]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("restart broker flags = %v, want %v (crash restart must reuse the frozen list)", got, wantSocks)
	}

	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a single kill -9; restart lost its broker list")
	}
}

func TestSupervisorBrokerPathResolverAndRestart(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runner := writeIdleRunner(t)
	supervisor := algorunner.NewSupervisor(store, t.TempDir(), runner, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	resolver := &mockBrokerPathResolver{
		paths: []string{"/tmp/mock_b1.sock", "/tmp/mock_b2.sock"},
	}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running, got %s", got)
	}

	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm failed: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running after restart, got %s", got)
	}

	if err := supervisor.StopAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("stop algorithm: %v", err)
	}
}

func TestSupervisorStartFailsWhenBrokerResolutionFails(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolveErr := errors.New("camera lookup exploded")
	supervisor := algorunner.NewSupervisor(store, t.TempDir(), "sleep", algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(&failingBrokerPathResolver{err: resolveErr})
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	err := supervisor.StartAlgorithm(context.Background(), algo)
	if err == nil {
		t.Fatal("expected StartAlgorithm to fail when broker path resolution fails")
	}
	if !errors.Is(err, resolveErr) {
		t.Fatalf("expected the resolution error to be wrapped, got %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected no watcher to be registered, got status %s", got)
	}
}

func TestSupervisorRestartPreservesActiveStatus(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	supervisor := algorunner.NewSupervisor(store, t.TempDir(), writeIdleRunner(t), algorunner.DefaultRecoveryOptions(), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := store.UpdateAlgorithmStatus(ctx, algo.ID, algorunner.StatusActive); err != nil {
		t.Fatalf("mark algorithm active: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm: %v", err)
	}

	stored, err := store.GetAlgorithmPackage(ctx, algo.ID)
	if err != nil {
		t.Fatalf("reload algorithm: %v", err)
	}
	if stored.Status != algorunner.StatusActive {
		t.Fatalf("restart must preserve ACTIVE, got %s", stored.Status)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected running status after restart, got %s", got)
	}
}

func TestSupervisorUpgradeAlgorithmSuccess(t *testing.T) {
	store := newMockLifecycleStore()
	algoV1 := supervisorTestPackage()
	store.packages[algoV1.ID] = algoV1

	supervisor := algorunner.NewSupervisor(store, t.TempDir(), writeIdleRunner(t), algorunner.DefaultRecoveryOptions(), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	algoV2.PreviousVersion = algoV1.Version

	if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
		t.Fatalf("upgrade algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algoV1.ID); got != algorunner.StatusRunning {
		t.Fatalf("expected status running after upgrade, got %s", got)
	}
	if err := supervisor.StopAlgorithm(ctx, algoV1.ID); err != nil {
		t.Fatalf("stop upgraded algorithm: %v", err)
	}
}

func TestSupervisorUpgradeAlgorithmRollbackOnCrash(t *testing.T) {
	store := newMockLifecycleStore()
	algoV1 := supervisorTestPackage()
	algoV1.Version = "1.0.0"
	store.packages[algoV1.ID] = algoV1

	// Script that crashes immediately when receiving 2.0.0 in args, but stays alive for 1.0.0
	runnerScript := filepath.Join(t.TempDir(), "version_sensitive_runner.sh")
	scriptContent := `#!/bin/sh
for a in "$@"; do
	case "$a" in
		*2.0.0*)
			exit 1
			;;
	esac
done
trap 'exit 0' TERM INT
while true; do sleep 0.1; done
`
	if err := os.WriteFile(runnerScript, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write version sensitive runner: %v", err)
	}

	options := algorunner.DefaultRecoveryOptions()
	options.RollbackGuardWindow = 2 * time.Second
	supervisor := algorunner.NewSupervisor(store, t.TempDir(), runnerScript, options, zaptest.NewLogger(t))
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	algoV2.PreviousVersion = algoV1.Version
	store.packages[algoV2.ID] = algoV2

	if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
		t.Fatalf("upgrade algorithm: %v", err)
	}

	// Wait for automatic rollback to restore version 1.0.0 in store
	deadline := time.Now().Add(5 * time.Second)
	rolledBack := false
	for time.Now().Before(deadline) {
		pkg, err := store.GetAlgorithmPackage(ctx, algoV1.ID)
		if err == nil && pkg.Version == "1.0.0" {
			rolledBack = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rolledBack {
		pkg, _ := store.GetAlgorithmPackage(ctx, algoV1.ID)
		t.Fatalf("expected version to be rolled back to 1.0.0, got %s", pkg.Version)
	}

	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)
}
