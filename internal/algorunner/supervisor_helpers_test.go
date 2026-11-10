package algorunner_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
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

func (m *mockLifecycleStore) RollbackAlgorithmVersion(
	ctx context.Context,
	id string,
	targetVersion string,
	failedVersion string,
) error {
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

// newTestSupervisor returns a supervisor whose algorithms root holds one stub
// package for algo, running the idle package script.
func newTestSupervisor(
	t *testing.T,
	store algorunner.LifecycleStore,
	algo algorunner.PackageMeta,
	opts algorunner.RecoveryOptions,
) *algorunner.Supervisor {
	t.Helper()
	return newTestSupervisorWithRoot(t, store, stubPackageRoot(t, algo, idlePackageScript()), opts)
}

// newTestSupervisorWithRoot is the variant for tests that install several
// versions into one root or need a custom package script.
func newTestSupervisorWithRoot(
	t *testing.T,
	store algorunner.LifecycleStore,
	algorithmsDir string,
	opts algorunner.RecoveryOptions,
) *algorunner.Supervisor {
	t.Helper()
	return algorunner.NewSupervisor(
		store,
		algorunner.Runtime{AlgorithmsDir: algorithmsDir},
		opts,
		zap.NewNop(),
	)
}

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
	return "#!/bin/sh\n" +
		"printf 'start\\n' >> \"$RUN_ARG_FILE\"\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\n" +
		"sleep 30\n"
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

func (r *blockingBrokerPathResolver) SyncBrokerSubscriptions(
	ctx context.Context,
	algorithmID string,
) ([]string, error) {
	r.mu.Lock()
	r.calls = append(r.calls, algorithmID)
	r.mu.Unlock()
	r.entered <- struct{}{}
	<-r.release
	return r.paths, nil
}
