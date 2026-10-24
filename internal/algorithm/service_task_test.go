package algorithm_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// newTaskService builds a Service backed by a real migrated DB and a supervisor
// whose runner is a stub shell script, so package restarts are observable without a native
// runner binary.
func newTaskService(t *testing.T) (*algorithm.Service, *algorithm.Supervisor, *algorithm.Store, *database.Store, func()) {
	t.Helper()
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
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
	return service, supervisor, store, dbStore, cleanup
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

// countingBrokerPathResolver counts how many times the supervisor resolved
// broker paths, which is once per StartAlgorithm (i.e. once per restart).
type countingBrokerPathResolver struct {
	calls atomic.Int64
}

func (m *countingBrokerPathResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	m.calls.Add(1)
	return []string{"/tmp/dummy_debounce.sock"}, nil
}

// TestServiceTaskChangeDebouncesRestart guards design.md §7.4: a burst of task
// changes on one package must collapse into a single restart, not N restarts.
func TestServiceTaskChangeDebouncesRestart(t *testing.T) {
	service, supervisor, store, _, cleanup := newTaskService(t)
	defer cleanup()
	algo := seedTaskAlgorithm(t, store)

	resolver := &countingBrokerPathResolver{}
	supervisor.SetBrokerSubscriptionResolver(resolver)

	ctx := context.Background()
	if err := supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	afterInitialStart := resolver.calls.Load()

	// 20 rapid task change notifications must not restart the package 20
	// times. Without the debounce this loop would thrash
	// StopAlgorithm/StartAlgorithm once per task.
	for i := 0; i < 20; i++ {
		service.OnTaskChanged(ctx, algo.ID)
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

// brokerRecordingRunner 写入一个把每次启动的完整参数追加到 $RUN_ARG_FILE 的真实 runner。
func brokerRecordingRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "broker_recording_runner.sh")
	script := "#!/bin/sh\nprintf 'start\\n' >> \"$RUN_ARG_FILE\"\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording runner: %v", err)
	}
	return path
}

func readRunnerBrokerFlags(t *testing.T, path string) [][]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read runner arg file: %v", err)
	}
	var invocations [][]string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if line == "" {
			continue
		}
		if line == "start" {
			invocations = append(invocations, []string{})
			continue
		}
		if len(invocations) == 0 {
			continue
		}
		invocations[len(invocations)-1] = append(invocations[len(invocations)-1], line)
	}
	flags := make([][]string, 0, len(invocations))
	for _, args := range invocations {
		var socks []string
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "--broker" {
				socks = append(socks, args[i+1])
			}
		}
		flags = append(flags, socks)
	}
	return flags
}

// storeBackedBrokerResolver 按库中 enabled 的任务派生每台相机的 broker 路径，
// 模拟真实 camera 域的 SyncBrokerSubscriptions 行为。
type storeBackedBrokerResolver struct {
	dbStore *database.Store
}

func (r *storeBackedBrokerResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	var cameraIDs []string
	err := r.dbStore.DB().WithContext(ctx).Table("task_algorithms").
		Joins("JOIN analysis_tasks ON analysis_tasks.id = task_algorithms.task_id").
		Where("task_algorithms.algorithm_id = ? AND task_algorithms.enabled = 1 AND analysis_tasks.enabled = 1", algorithmID).
		Pluck("DISTINCT analysis_tasks.camera_id", &cameraIDs).Error
	if err != nil {
		return nil, err
	}
	var socks []string
	for _, camID := range cameraIDs {
		socks = append(socks, "/tmp/zhulong_broker_"+camID+".sock")
	}
	return socks, nil
}

func waitForBrokerInvocations(t *testing.T, path string, want int) {
	t.Helper()
	waitForRunnerInvocationCount(t, path, want)
}

// waitForRunnerInvocationCount 轮询到第 count 次 runner 启动，返回全部调用的 broker 参数。
//
// 包重启是异步的，固定 sleep 猜时序会让用例在慢机上随机失败；这里等的是
// 「第 count 次启动且参数符合预期」这一状态本身。
func waitForRunnerInvocationCount(t *testing.T, path string, count int) [][]string {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if invocations := readRunnerBrokerFlags(t, path); len(invocations) >= count {
			return invocations
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner invocations did not reach %d; got %d", count, len(readRunnerBrokerFlags(t, path)))
	return nil
}

// waitForLastBrokerFlags 等到最近一次 runner 启动的 broker 参数等于 want。
//
// 不能用固定下标取：重启过程中可能同时存在旧进程的残留记录，只有「最后一次」
// 才代表当前生效的订阅集合。
func waitForLastBrokerFlags(t *testing.T, path string, want []string) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if invocations := readRunnerBrokerFlags(t, path); len(invocations) > 0 && reflect.DeepEqual(invocations[len(invocations)-1], want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("last runner broker flags = %v, want %v", readRunnerBrokerFlags(t, path), want)
}

// TestServiceTaskChangeRestartsPackageWithNewBrokerSet covers AC9's end-to-end
// half that the resolver-level tests do not: creating an AnalysisTask on a second
// camera must make the owning package restart and actually receive BOTH broker
// sockets on its command line (not just a changed resolver return value).
func TestServiceTaskChangeRestartsPackageWithNewBrokerSet(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)
	algo := seedTaskAlgorithm(t, store)

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)

	supervisor := algorithm.NewSupervisor(store, dataDir, brokerRecordingRunner(t, dataDir), algorithm.DefaultRecoveryOptions(), zap.NewNop())
	resolver := &storeBackedBrokerResolver{dbStore: dbStore}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	service := algorithm.NewService(store, algorithm.NewVerifier("", "cpu"), supervisor, dataDir, zap.NewNop())

	ctx := context.Background()
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
		_ = dbStore.Close()
	})

	// 先建 camera1 的任务，再启动包 → 首轮应为 1 个 --broker
	if err := dbStore.DB().Exec(`
		INSERT INTO analysis_tasks (id, camera_id, enabled, created_at, updated_at) VALUES ('task_cam1', 'camera1', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, created_at, updated_at) VALUES ('sub_cam1', 'task_cam1', ?, 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, algo.ID).Error; err != nil {
		t.Fatalf("insert task for camera1: %v", err)
	}

	if err := supervisor.StartAlgorithm(ctx, algo.ToPackageMeta()); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForBrokerInvocations(t, argFile, 1)
	first := readRunnerBrokerFlags(t, argFile)
	if got := first[0]; !reflect.DeepEqual(got, []string{"/tmp/zhulong_broker_camera1.sock"}) {
		t.Fatalf("first start broker flags = %v, want 1 sock for camera1", got)
	}

	// 运行期新建 camera2 的任务 → 该包必须重启并实际带上 2 个 --broker
	if err := dbStore.DB().Exec(`
		INSERT INTO analysis_tasks (id, camera_id, enabled, created_at, updated_at) VALUES ('task_cam2', 'camera2', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, created_at, updated_at) VALUES ('sub_cam2', 'task_cam2', ?, 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, algo.ID).Error; err != nil {
		t.Fatalf("insert task for camera2: %v", err)
	}
	service.OnTaskChanged(ctx, algo.ID)
	service.FlushPendingRestarts()

	want := []string{"/tmp/zhulong_broker_camera1.sock", "/tmp/zhulong_broker_camera2.sock"}
	waitForLastBrokerFlags(t, argFile, want)
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected package running after task-driven restart, got %s", got)
	}

	// 删除 camera2 的任务 → 回到 1 路
	if err := dbStore.DB().Exec("DELETE FROM analysis_tasks WHERE id = 'task_cam2'").Error; err != nil {
		t.Fatalf("delete task for camera2: %v", err)
	}
	service.OnTaskChanged(ctx, algo.ID)
	service.FlushPendingRestarts()

	wantThird := []string{"/tmp/zhulong_broker_camera1.sock"}
	waitForLastBrokerFlags(t, argFile, wantThird)
}

// TestTaskDrivenLazyStartupAndTeardownLifecycle verifies the complete lifecycle:
// 1. Enabling an algorithm with 0 tasks enters standby (DB ACTIVE, supervisor DISABLED, 0 runner invocations).
// 2. Creating a task dynamically wakes the algorithm and launches the runner.
// 3. Deleting all tasks dynamically stops the runner and retires it back to standby (DB stays ACTIVE).
func TestTaskDrivenLazyStartupAndTeardownLifecycle(t *testing.T) {
	dataDir := t.TempDir()
	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	store := algorithm.NewStore(dbStore)

	algo := &algorithm.Algorithm{
		ID: "algo_lazy_lifecycle", Name: "lazy_lifecycle", Version: "1.0.0",
		TaskType: algorithm.TaskTypeDetection, Category: algorithm.CategorySecurity,
		TargetArch: "x86_64", TargetDevice: "cpu", ComputeCost: 10,
		MemoryMB: 64, Status: algorithm.StatusDisabled,
	}
	if err := store.CreateAlgorithm(context.Background(), algo); err != nil {
		t.Fatalf("create algorithm: %v", err)
	}

	argFile := filepath.Join(t.TempDir(), "runner-args")
	t.Setenv("RUN_ARG_FILE", argFile)

	supervisor := algorithm.NewSupervisor(store, dataDir, brokerRecordingRunner(t, dataDir), algorithm.DefaultRecoveryOptions(), zap.NewNop())
	resolver := &storeBackedBrokerResolver{dbStore: dbStore}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	service := algorithm.NewService(store, algorithm.NewVerifier("", "cpu"), supervisor, dataDir, zap.NewNop())

	ctx := context.Background()
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
		_ = dbStore.Close()
	})

	// Step 1: Enable algorithm when 0 tasks exist in DB -> Standby state
	if err := service.EnableAlgorithm(ctx, algo.ID); err != nil {
		t.Fatalf("enable algorithm: %v", err)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusDisabled {
		t.Fatalf("expected supervisor to have no watcher (standby), got %s", got)
	}
	loadedAlgo, err := store.GetAlgorithm(ctx, algo.ID)
	if err != nil {
		t.Fatalf("get algorithm: %v", err)
	}
	if loadedAlgo.Status != algorithm.StatusActive {
		t.Fatalf("expected DB status to be ACTIVE, got %s", loadedAlgo.Status)
	}
	if flags := readRunnerBrokerFlags(t, argFile); len(flags) != 0 {
		t.Fatalf("expected 0 runner invocations during standby, got %d", len(flags))
	}

	// Step 2: Dynamically add a task -> runner must auto-start
	if err := dbStore.DB().Exec(`
		INSERT INTO analysis_tasks (id, camera_id, enabled, created_at, updated_at) VALUES ('task_lazy_1', 'camera_lazy_1', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
		INSERT INTO task_algorithms (id, task_id, algorithm_id, enabled, sample_fps, config_json, created_at, updated_at) VALUES ('sub_lazy_1', 'task_lazy_1', ?, 1, 5, '{}', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
	`, algo.ID).Error; err != nil {
		t.Fatalf("insert task: %v", err)
	}
	service.OnTaskChanged(ctx, algo.ID)
	service.FlushPendingRestarts()

	waitForBrokerInvocations(t, argFile, 1)
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected algorithm running after task added, got %s", got)
	}
	invocations := readRunnerBrokerFlags(t, argFile)
	if got := invocations[0]; !reflect.DeepEqual(got, []string{"/tmp/zhulong_broker_camera_lazy_1.sock"}) {
		t.Fatalf("first start broker flags = %v, want /tmp/zhulong_broker_camera_lazy_1.sock", got)
	}

	// Step 3: Dynamically remove all tasks -> runner must gracefully stop and return to standby
	if err := dbStore.DB().Exec("DELETE FROM analysis_tasks WHERE id = 'task_lazy_1'").Error; err != nil {
		t.Fatalf("delete task: %v", err)
	}
	service.OnTaskChanged(ctx, algo.ID)
	service.FlushPendingRestarts()

	// Wait for status to become DISABLED in supervisor
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if supervisor.GetStatus(algo.ID) == algorithm.StatusDisabled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusDisabled {
		t.Fatalf("expected runner to be stopped (DISABLED in supervisor), got %s", got)
	}

	// DB status must still be ACTIVE
	loadedAlgo, err = store.GetAlgorithm(ctx, algo.ID)
	if err != nil {
		t.Fatalf("get algorithm: %v", err)
	}
	if loadedAlgo.Status != algorithm.StatusActive {
		t.Fatalf("expected DB status to remain ACTIVE, got %s", loadedAlgo.Status)
	}
}
