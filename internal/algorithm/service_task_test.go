package algorithm_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
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

// brokerRecordingRunner 写入一个把每次启动的完整参数追加到 $RUN_ARG_FILE 的真实 runner。
// 与 supervisor_test.go 的记录格式一致：每个参数一行，以字面量 "start" 行分界。
func brokerRecordingRunner(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "broker_recording_runner.sh")
	script := "#!/bin/sh\nprintf 'start\\n' >> \"$RUN_ARG_FILE\"\nfor a in \"$@\"; do printf '%s\\n' \"$a\" >> \"$RUN_ARG_FILE\"; done\ntrap 'exit 0' TERM INT\nwhile true; do sleep 1; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write recording runner: %v", err)
	}
	return path
}

// readRunnerBrokerFlags 读出 runner 每次启动的 --broker 路径列表（按启动顺序）。
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
	// resolver 模拟 camera 域：按当前已落库的 enabled 任务派生出每台相机的 broker 路径。
	resolver := &storeBackedBrokerResolver{store: store}
	supervisor.SetBrokerSubscriptionResolver(resolver)
	service := algorithm.NewService(store, algorithm.NewVerifier("", "cpu"), supervisor, dataDir, zap.NewNop())

	ctx := context.Background()
	t.Cleanup(func() {
		supervisor.StopAll(2 * time.Second)
		_ = dbStore.Close()
	})

	// 先建 camera1 的任务，再启动包 → 首轮应为 1 个 --broker
	if err := service.CreateTask(ctx, &algorithm.AnalysisTask{
		ID: "task_cam1", CameraID: "camera1", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 5, ConfigJSON: "{}",
	}); err != nil {
		t.Fatalf("create task for camera1: %v", err)
	}
	service.FlushPendingRestarts()

	if err := supervisor.StartAlgorithm(ctx, *algo); err != nil {
		t.Fatalf("start algorithm: %v", err)
	}
	waitForBrokerInvocations(t, argFile, 1)
	first := readRunnerBrokerFlags(t, argFile)
	if got := first[0]; !reflect.DeepEqual(got, []string{"/tmp/zhulong_broker_camera1.sock"}) {
		t.Fatalf("first start broker flags = %v, want 1 sock for camera1", got)
	}

	// 运行期新建 camera2 的任务 → 该包必须重启并实际带上 2 个 --broker
	if err := service.CreateTask(ctx, &algorithm.AnalysisTask{
		ID: "task_cam2", CameraID: "camera2", AlgorithmID: algo.ID,
		Enabled: true, SampleFPS: 5, ConfigJSON: "{}",
	}); err != nil {
		t.Fatalf("create task for camera2: %v", err)
	}
	service.FlushPendingRestarts()

	waitForBrokerInvocations(t, argFile, 2)
	second := readRunnerBrokerFlags(t, argFile)
	want := []string{"/tmp/zhulong_broker_camera1.sock", "/tmp/zhulong_broker_camera2.sock"}
	if got := second[1]; !reflect.DeepEqual(got, want) {
		t.Fatalf("restart after adding camera2 broker flags = %v, want %v", got, want)
	}
	if got := supervisor.GetStatus(algo.ID); got != algorithm.StatusRunning {
		t.Fatalf("expected package running after task-driven restart, got %s", got)
	}

	// 删除 camera2 的任务 → 回到 1 路
	if err := service.DeleteTask(ctx, "task_cam2"); err != nil {
		t.Fatalf("delete task for camera2: %v", err)
	}
	service.FlushPendingRestarts()

	waitForBrokerInvocations(t, argFile, 3)
	third := readRunnerBrokerFlags(t, argFile)
	if got := third[2]; !reflect.DeepEqual(got, []string{"/tmp/zhulong_broker_camera1.sock"}) {
		t.Fatalf("restart after deleting camera2 broker flags = %v, want 1 sock for camera1", got)
	}
}

// storeBackedBrokerResolver 按库中 enabled 的 AnalysisTask 派生每台相机的 broker 路径，
// 模拟真实 camera 域的 SyncBrokerSubscriptions 行为（同一台相机只产出一个路径）。
type storeBackedBrokerResolver struct {
	store *algorithm.Store
}

func (r *storeBackedBrokerResolver) SyncBrokerSubscriptions(ctx context.Context, algorithmID string) ([]string, error) {
	tasks, err := r.store.ListTasksByAlgorithm(ctx, algorithmID)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var socks []string
	for _, task := range tasks {
		if !task.Enabled || seen[task.CameraID] {
			continue
		}
		seen[task.CameraID] = true
		socks = append(socks, "/tmp/zhulong_broker_"+task.CameraID+".sock")
	}
	return socks, nil
}

// waitForBrokerInvocations 等待 runner 参数文件累计到 want 次启动记录。
func waitForBrokerInvocations(t *testing.T, path string, want int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(readRunnerBrokerFlags(t, path)) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner invocations did not reach %d; got %d", want, len(readRunnerBrokerFlags(t, path)))
}
