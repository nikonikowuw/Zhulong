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

// Package stubs.
//
// These used to be standalone runner scripts injected through Runtime.RunnerPath.
// There is no such seam any more: a package *is* its own executable, so a stub has
// to be installed at the exact path the supervisor resolves
// (<root>/<id>/<version>/<id>). Keeping the installation in one helper means the
// tests exercise the real resolution path instead of bypassing it.

// stubPackageRoot installs script as pkg's entry executable and returns the
// install root to hand to Runtime{AlgorithmsDir: ...}.
func stubPackageRoot(t *testing.T, pkg algorunner.PackageMeta, script string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, pkg.ID, pkg.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pkg.ID), []byte(script), 0o755); err != nil {
		t.Fatalf("write package entry: %v", err)
	}
	return root
}

// stubPackageRootAt installs script as pkg's entry inside an existing root, for
// multi-version scenarios where upgrade/rollback need two versions at once.
func stubPackageRootAt(t *testing.T, root string, pkg algorunner.PackageMeta, script string) {
	t.Helper()
	dir := filepath.Join(root, pkg.ID, pkg.Version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, pkg.ID), []byte(script), 0o755); err != nil {
		t.Fatalf("write package entry: %v", err)
	}
}

// crashingPackageScript exits non-zero on every start, recording each attempt.
func crashingPackageScript() string {
	return "#!/bin/sh\nprintf 'run\\n' >> \"$RUN_COUNT_FILE\"\nexit 1\n"
}

// idlePackageScript stays alive until asked to terminate.
func idlePackageScript() string {
	return "#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
}

// argRecordingPackageScript records every argv it receives, once per start.
func argRecordingPackageScript() string {
	return "#!/bin/sh\nprintf 'start\\n' >> \"$RUN_ARG_FILE\"\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\nsleep 30\n"
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
			// 新形态：进程名就是包 id（<root>/<id>/<version>/<id>），所以只按 id 匹配。
			if len(fields) > 1 && filepath.Base(fields[1]) == marker {
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
		// argv[0] 是 <root>/<id>/<version>/<id>：没有固定的 runner 脚本名可匹配，
		// 但进程名恒等于包 id。
		// 桩是 shell 脚本，内核实际 exec 的是 `/bin/sh <entry>`，所以 argv[1]
		// 才是入口（真实包是 ELF，argv[0] 就是入口）。两者都看。
		fields := strings.Split(strings.ReplaceAll(string(cmdline), "\x00", " "), " ")
		for i := 0; i < 2 && i < len(fields); i++ {
			if filepath.Base(fields[i]) == marker {
				return pid
			}
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

// mutableBrokerPathResolver is a concurrency-safe resolver whose returned paths
// (and failure mode) can be changed between calls. It models a task set that
// changes while the supervisor is running.
type mutableBrokerPathResolver struct {
	mu    sync.Mutex
	paths []string
	err   error
}

func newMutableBrokerPathResolver(paths []string) *mutableBrokerPathResolver {
	return &mutableBrokerPathResolver{paths: paths}
}

func (m *mutableBrokerPathResolver) Set(paths []string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paths = paths
	m.err = nil
}

func (m *mutableBrokerPathResolver) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *mutableBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	out := make([]string, len(m.paths))
	copy(out, m.paths)
	return out, nil
}

// blockingBrokerPathResolver blocks inside SyncBrokerSubscriptions until the
// test closes release, recording every algorithmID it was asked to resolve.
// It lets tests open a deterministic window during broker resolution.
type blockingBrokerPathResolver struct {
	paths       []string
	mu          sync.Mutex
	calls       []string
	entered     chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func newBlockingBrokerPathResolver(paths []string) *blockingBrokerPathResolver {
	return &blockingBrokerPathResolver{
		paths:   paths,
		entered: make(chan struct{}, 8),
		release: make(chan struct{}),
	}
}

func (r *blockingBrokerPathResolver) unblock() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (r *blockingBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, algorithmID)
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
	return r.paths, nil
}

// TestGetStatusNotBlockedByBrokerResolution reproduces R3: resolving broker
// socket paths performs DB/subscription IO and must not run while holding the
// supervisor write lock. While StartAlgorithm is stuck inside the resolver,
// GetStatus (which takes an RLock) must still return promptly.
func TestGetStatusNotBlockedByBrokerResolution(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newBlockingBrokerPathResolver([]string{"/tmp/r3_broker.sock"})
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		resolver.unblock()
		supervisor.StopAll(time.Second)
	})

	startErr := make(chan error, 1)
	go func() { startErr <- supervisor.StartAlgorithm(context.Background(), algo) }()

	// Wait until StartAlgorithm is genuinely blocked inside the resolver.
	select {
	case <-resolver.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("StartAlgorithm never reached the broker resolver")
	}

	statusDone := make(chan string, 1)
	go func() { statusDone <- supervisor.GetStatus(algo.ID) }()

	select {
	case <-statusDone:
		// Good: GetStatus returned while resolution was still in flight.
	case <-time.After(200 * time.Millisecond):
		t.Fatal("GetStatus blocked while StartAlgorithm was resolving broker paths (R3: resolver IO must not run under s.mu)")
	}

	resolver.unblock()
	if err := <-startErr; err != nil {
		t.Fatalf("StartAlgorithm failed: %v", err)
	}
}

// TestConcurrentStartAlgorithmRegistersOnce verifies the second validation added
// by R3: when two StartAlgorithm calls race for the same ID (both pass the fast
// existence check, both resolve broker paths outside the lock), exactly one must
// win registration and the other must report "already running".
func TestConcurrentStartAlgorithmRegistersOnce(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newBlockingBrokerPathResolver([]string{"/tmp/r3_race.sock"})
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		resolver.unblock()
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	results := make([]error, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			<-start
			results[i] = supervisor.StartAlgorithm(ctx, algo)
		}(i)
	}
	close(start)

	// Both goroutines must be inside the resolver before we release, so the
	// second registration genuinely races the first (the whole point of R3).
	for i := 0; i < 2; i++ {
		select {
		case <-resolver.entered:
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d StartAlgorithm call(s) reached the resolver", i)
		}
	}
	resolver.unblock()
	wg.Wait()

	successes := 0
	alreadyRunning := 0
	for _, err := range results {
		switch {
		case err == nil:
			successes++
		case err != nil && strings.Contains(err.Error(), "already running"):
			alreadyRunning++
		default:
			t.Fatalf("unexpected StartAlgorithm error: %v", err)
		}
	}
	if successes != 1 || alreadyRunning != 1 {
		t.Fatalf("expected exactly one success and one 'already running' error, got successes=%d alreadyRunning=%d (errors=%v)",
			successes, alreadyRunning, results)
	}
}

func TestSupervisorResetAllowsRestartAfterCrashLoop(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runnerScript := crashingPackageScript()
	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	options.MaxCrashes = 1
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, runnerScript)}, options, zap.NewNop())
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

// TestSupervisorCrashRestartReResolvesBrokerList pins R6: the crash-restart
// branch must re-resolve the broker list (the task set may have changed since
// the initial start) instead of reusing the frozen list captured at start time.
func TestSupervisorCrashRestartReResolvesBrokerList(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, runnerScript)}, options, zap.NewNop())

	initialSocks := []string{"/tmp/zhulong_broker_1.sock"}
	newSocks := []string{"/tmp/zhulong_broker_2.sock", "/tmp/zhulong_broker_3.sock"}
	resolver := newMutableBrokerPathResolver(initialSocks)
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForRecordedInvocations(t, argFile, 1)

	first := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(first[0]); !reflect.DeepEqual(got, initialSocks) {
		t.Fatalf("first start broker flags = %v, want %v", got, initialSocks)
	}

	// The task set changes, then the runner crashes. The restart must observe the
	// NEW broker list, not the one frozen at start time.
	resolver.Set(newSocks)

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	gotSecond := brokerFlagsIn(second[1])
	t.Logf("first invocation --broker=%v; second invocation --broker=%v", brokerFlagsIn(first[0]), gotSecond)
	if !reflect.DeepEqual(gotSecond, newSocks) {
		t.Fatalf("restart broker flags = %v, want %v (R6: crash restart must re-resolve the broker list)", gotSecond, newSocks)
	}
}

// TestSupervisorCrashRestartFallsBackWhenResolveFails pins the fallback half of
// R6: when re-resolution fails during a crash restart, the supervisor must fall
// back to the cached broker list and keep the runner alive rather than degrading
// into CRASH_LOOP.
func TestSupervisorCrashRestartFallsBackWhenResolveFails(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, runnerScript)}, options, zap.NewNop())

	cachedSocks := []string{"/tmp/zhulong_broker_keep.sock"}
	resolver := newMutableBrokerPathResolver(cachedSocks)
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForRecordedInvocations(t, argFile, 1)

	// Resolution starts failing before the crash restart.
	resolver.SetError(errors.New("broker resolution unavailable"))

	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(second[1]); !reflect.DeepEqual(got, cachedSocks) {
		t.Fatalf("restart broker flags = %v, want cached %v (fallback must reuse the last good list)", got, cachedSocks)
	}

	// Give the crash accounting a moment, then assert no circuit breaker.
	time.Sleep(50 * time.Millisecond)
	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a resolve failure; fallback must keep it running")
	}
}

// TestSupervisorCrashRestartKeepsUsableBrokerList keeps the anti-exit-5 intent
// of the original test (a crash restart must still hand the runner a usable
// broker list so it does not exit 5) while no longer asserting that the list is
// the frozen one: under R6 the list is re-resolved on every restart, and with a
// static resolver that re-resolution yields the same usable set.
func TestSupervisorCrashRestartKeepsUsableBrokerList(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runnerScript := argRecordingPackageScript()

	options := algorunner.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, runnerScript)}, options, zap.NewNop())

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
		t.Fatalf("restart broker flags = %v, want a usable list %v", got, wantSocks)
	}

	if got := supervisor.GetStatus(algo.ID); got == algorunner.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a single kill -9; restart lost its broker list")
	}
}

func TestSupervisorBrokerPathResolverAndRestart(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	runnerScript := idlePackageScript()
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, runnerScript)}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
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
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
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

	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
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

// TestSupervisorRollbackThenStopKeepsStatusConsistent is the black-box
// concurrency counterpart to the white-box R1 test. A version-sensitive runner
// crashes on 2.0.0 inside the rollback guard window, which triggers automatic
// rollback to 1.0.0; concurrently StopAlgorithm is hammered. The invariant:
// whenever the supervisor reports RUNNING, the persisted status must not be
// DISABLED (a running process must never be described as stopped in the DB).
//
// The deterministic white-box test
// (TestStopAlgorithmDoesNotDisableAReplacedWatcher) pins the exact defect; this
// test cross-checks it under real scheduling with -race. The invariant is
// asserted once the round has settled, to avoid flagging benign mid-flight
// interleavings where a subsequent status write is still in progress.
func TestSupervisorRollbackThenStopKeepsStatusConsistent(t *testing.T) {
	const rounds = 6
	ctx := context.Background()

	for round := 0; round < rounds; round++ {
		store := newMockLifecycleStore()
		algoV1 := supervisorTestPackage()
		algoV1.Version = "1.0.0"
		store.packages[algoV1.ID] = algoV1

		// 版本敏感的桩：新形态的 argv 里没有版本号，但入口路径自带版本目录
		// （<root>/<id>/<version>/<id>），所以按 $0 分支才是对等写法。
		scriptContent := `#!/bin/sh
case "$0" in
	*2.0.0*)
		exit 1
		;;
esac
trap 'exit 0' TERM INT
while true; do sleep 0.05; done
`

		options := algorunner.DefaultRecoveryOptions()
		options.RollbackGuardWindow = 3 * time.Second
		options.InitialBackoff = 5 * time.Millisecond
		options.MaxBackoff = 10 * time.Millisecond
		root := stubPackageRoot(t, algoV1, scriptContent)
		algoV2 := algoV1
		algoV2.Version = "2.0.0"
		stubPackageRootAt(t, root, algoV2, scriptContent)
		supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, options, zap.NewNop())

		if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
			supervisor.StopAll(time.Second)
			t.Fatalf("round %d: start v1: %v", round, err)
		}
		waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

		algoV2.PreviousVersion = algoV1.Version
		store.packages[algoV2.ID] = algoV2

		if err := supervisor.UpgradeAlgorithm(ctx, algoV2, algoV1); err != nil {
			supervisor.StopAll(time.Second)
			t.Fatalf("round %d: upgrade: %v", round, err)
		}

		// Hammer StopAlgorithm while the crash-triggered rollback is in flight.
		stopDone := make(chan struct{})
		go func() {
			defer close(stopDone)
			for i := 0; i < 30; i++ {
				_ = supervisor.StopAlgorithm(ctx, algoV1.ID)
				time.Sleep(3 * time.Millisecond)
			}
		}()
		<-stopDone

		// Let any in-flight rollback finish before reading the settled state.
		time.Sleep(150 * time.Millisecond)

		if got := supervisor.GetStatus(algoV1.ID); got == algorunner.StatusRunning {
			pkg, err := store.GetAlgorithmPackage(ctx, algoV1.ID)
			if err == nil && pkg.Status == algorunner.StatusDisabled {
				supervisor.StopAll(time.Second)
				t.Fatalf("round %d: supervisor reports RUNNING while the stored status is DISABLED", round)
			}
		}

		supervisor.StopAll(time.Second)
	}
}

func TestSupervisorUpgradeAlgorithmSuccess(t *testing.T) {
	store := newMockLifecycleStore()
	algoV1 := supervisorTestPackage()
	store.packages[algoV1.ID] = algoV1

	// 升级要拉起 2.0.0，所以两个版本的入口都得在盘上。
	root := stubPackageRoot(t, algoV1, idlePackageScript())
	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	stubPackageRootAt(t, root, algoV2, idlePackageScript())

	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

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
	// 同上：按 $0 里的版本目录分支
	scriptContent := `#!/bin/sh
case "$0" in
	*2.0.0*)
		exit 1
		;;
esac
trap 'exit 0' TERM INT
while true; do sleep 0.1; done
`

	options := algorunner.DefaultRecoveryOptions()
	options.RollbackGuardWindow = 2 * time.Second
	root := stubPackageRoot(t, algoV1, scriptContent)
	algoV2 := algoV1
	algoV2.Version = "2.0.0"
	stubPackageRootAt(t, root, algoV2, scriptContent)
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: root}, options, zaptest.NewLogger(t))
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algoV1); err != nil {
		t.Fatalf("start v1 algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algoV1.ID, algorunner.StatusRunning)

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

func TestSupervisorStartFailsWithNoBrokerSocketsWhenResolverConfigured(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(&mockBrokerPathResolver{paths: []string{}})
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	err := supervisor.StartAlgorithm(context.Background(), algo)
	if !errors.Is(err, algorunner.ErrNoBrokerSockets) {
		t.Fatalf("expected ErrNoBrokerSockets, got %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected no watcher to be registered, got %s", got)
	}
}

func TestSupervisorRestartWithZeroBrokersRetiresRunnerToStandby(t *testing.T) {
	store := newMockLifecycleStore()
	algo := supervisorTestPackage()
	store.packages[algo.ID] = algo

	resolver := newMutableBrokerPathResolver([]string{"/tmp/stream1.sock"})
	supervisor := algorunner.NewSupervisor(store, algorunner.Runtime{AlgorithmsDir: stubPackageRoot(t, algo, idlePackageScript())}, algorunner.DefaultRecoveryOptions(), zap.NewNop())
	supervisor.SetBrokerSubscriptionResolver(resolver)
	t.Cleanup(func() {
		supervisor.StopAll(time.Second)
	})

	ctx := context.Background()
	if err := store.UpdateAlgorithmStatus(ctx, algo.ID, algorunner.StatusActive); err != nil {
		t.Fatalf("update status to active: %v", err)
	}
	if err := supervisor.StartAlgorithm(ctx, algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForSupervisorStatus(t, supervisor, algo.ID, algorunner.StatusRunning)

	// Now remove all tasks / broker sockets
	resolver.Set([]string{})
	if err := supervisor.RestartAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("restart algorithm with 0 brokers failed: %v", err)
	}

	// Runner should be gracefully retired, status in supervisor is DISABLED (standby)
	if got := supervisor.GetStatus(algo.ID); got != algorunner.StatusDisabled {
		t.Fatalf("expected supervisor status to be DISABLED (standby), got %s", got)
	}

	// Persisted status in DB must remain ACTIVE
	pkg, err := store.GetAlgorithmPackage(ctx, algo.ID)
	if err != nil {
		t.Fatalf("get package: %v", err)
	}
	if pkg.Status != algorunner.StatusActive {
		t.Fatalf("expected DB status to remain ACTIVE, got %s", pkg.Status)
	}
}
