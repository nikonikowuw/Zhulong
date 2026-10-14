package algorithm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
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

// writeBrokerArgRecordingRunner 写入一个把每次启动的完整参数追加到 $RUN_ARG_FILE 的真实 runner。
// 它会在记录后长时间睡眠（模拟真实 runner 常驻），因此可以被外部 kill -9。
func writeBrokerArgRecordingRunner(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recording_runner.sh")
	// 每次启动写一个 "start" 标记行，随后逐行写一个参数（POSIX sh 的 printf 不可靠地
	// 支持 \x1f 转义，因此用标记行 + 逐行参数这种无歧义格式）。
	script := "#!/bin/sh\nprintf 'start\\n' >> \"$RUN_ARG_FILE\"\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\nsleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording runner: %v", err)
	}
	return path
}

// readRecordedRunnerInvocations 解析 runner 记录的每次启动参数。
// 格式：每个参数一行，以字面量 "start" 行作为一次启动的分界。
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

// brokerFlagsIn 从一次启动的参数中提取 --broker 后面的路径列表。
func brokerFlagsIn(args []string) []string {
	var socks []string
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--broker" {
			socks = append(socks, args[i+1])
		}
	}
	return socks
}

// TestSupervisorCrashRestartKeepsBrokerList 守护 design.md §7.2 的冷路径陷阱（AC11）：
// superviseLoop 的崩溃重启路径没有 ctx、无法重新解析 broker 路径，必须复用启动时
// 固化到 workerWatcher 的 brokerSocks。若它退回空列表或全局单值，runner 会因缺少
// --broker 而 exit 5，进而退避并熔断成 CRASH_LOOP —— 即「能启动但不能恢复」。
func TestSupervisorCrashRestartKeepsBrokerList(t *testing.T) {
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

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)
	runner := writeBrokerArgRecordingRunner(t)

	options := algorithm.DefaultRecoveryOptions()
	options.InitialBackoff = 5 * time.Millisecond
	options.MaxBackoff = 10 * time.Millisecond
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), runner, options, zap.NewNop())

	wantSocks := []string{"/tmp/zhulong_broker_7.sock", "/tmp/zhulong_broker_9.sock"}
	supervisor.SetBrokerSubscriptionResolver(&mockBrokerPathResolver{paths: wantSocks})
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
		_ = dbStore.Close()
	})

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}

	// 等首轮 runner 真正启动并记录参数
	waitForRecordedInvocations(t, argFile, 1)

	first := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(first[0]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("first start broker flags = %v, want %v", got, wantSocks)
	}

	// 模拟 runner 被 kill -9：定位并强杀该 runner 进程，触发 superviseLoop 自动重启
	pid := findRunnerPID(t, algo.ID)
	if pid <= 0 {
		t.Fatalf("could not locate runner pid for %s", algo.ID)
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 runner pid %d: %v", pid, err)
	}

	// 自动重启必须发生，且第二次启动仍带完整 --broker 列表
	waitForRecordedInvocations(t, argFile, 2)
	second := readRecordedRunnerInvocations(t, argFile)
	if got := brokerFlagsIn(second[1]); !reflect.DeepEqual(got, wantSocks) {
		t.Fatalf("restart broker flags = %v, want %v (crash restart must reuse the frozen list)", got, wantSocks)
	}

	// 不得因缺参 exit 5 而熔断
	if got := supervisor.GetStatus(algo.ID); got == algorithm.StatusCrashLoop {
		t.Fatalf("algorithm entered CRASH_LOOP after a single kill -9; restart lost its broker list")
	}
}

// waitForRecordedInvocations 等待 runner 参数文件累计到 want 条启动记录。
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

// findRunnerPID 在 /proc 中按命令行查找当前 runner 子进程的 PID。
func findRunnerPID(t *testing.T, marker string) int {
	t.Helper()
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
		// 只认本测试的 recording runner，且必须携带该算法的 marker 参数
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

	runner := writeIdleRunner(t)
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

	// ⚠️ 不能用裸 "sleep"：无参数时它会立即以 exit 1 退出，supervisor 随即判定崩溃，
	// 与下面的 StatusRunning 断言竞态（既有 flake，复现率约 1/8）。
	// writeIdleRunner 是一个常驻且响应 TERM/INT 的 runner，能稳定满足该断言。
	supervisor := algorithm.NewSupervisor(store, filepath.Join(dataDir, "algorithms"), writeIdleRunner(t), algorithm.DefaultRecoveryOptions(), zap.NewNop())
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
