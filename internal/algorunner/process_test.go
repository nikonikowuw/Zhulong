package algorunner_test

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func findMockDetectorPlugin(t *testing.T) string {
	t.Helper()
	candidates := []string{
		"../../build/native/host/libMockDetector.so",
		"build/native/host/libMockDetector.so",
	}
	for _, c := range candidates {
		abs, err := filepath.Abs(c)
		if err == nil {
			if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
				return abs
			}
		}
	}
	t.Skip("libMockDetector.so not found, skipping dynamic runner test")
	return ""
}

func TestIpcPayloadSizeGate(t *testing.T) {
	// Exactly at the limit must pass.
	exact := make([]byte, algoipc.MaxIpcPayloadSize)
	msgBytes, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, exact)
	if err != nil {
		t.Fatalf("payload of exactly algoipc.MaxIpcPayloadSize must be accepted, got: %v", err)
	}
	if len(msgBytes) != 16+algoipc.MaxIpcPayloadSize {
		t.Fatalf("unexpected framed length: %d", len(msgBytes))
	}
	hdr, err := algoipc.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("algoipc.DecodeIpcHeader on boundary payload failed: %v", err)
	}
	if hdr.PayloadSize != uint32(algoipc.MaxIpcPayloadSize) {
		t.Errorf("expected payload_size %d, got %d", algoipc.MaxIpcPayloadSize, hdr.PayloadSize)
	}

	// One byte over the limit must be rejected by the send path.
	tooLarge := make([]byte, algoipc.MaxIpcPayloadSize+1)
	if _, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, tooLarge); !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected algoipc.ErrIpcPayloadTooLarge for 64KB+1, got: %v", err)
	}

	// The receive path must also reject a header declaring an oversized payload.
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint32(buf[0:4], algoipc.IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], algoipc.MsgGallerySync)
	binary.LittleEndian.PutUint16(buf[6:8], algoipc.IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(algoipc.MaxIpcPayloadSize+1))
	binary.LittleEndian.PutUint32(buf[12:16], 1)
	if _, err := algoipc.DecodeIpcHeader(buf); !errors.Is(err, algoipc.ErrIpcPayloadTooLarge) {
		t.Errorf("expected algoipc.DecodeIpcHeader to reject oversized payload_size, got: %v", err)
	}
}

func TestRunnerProcessInjectsDotEnvEndToEnd(t *testing.T) {
	// This drives the real os/exec environment-injection path: a shell script
	// acts as the runner binary and echoes an env var that only exists in the
	// package's .env file. Testing LoadDotEnv directly would not catch a broken
	// wiring (EnvFile never being set), which is exactly what this task fixes.
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("ZHULONG_ENV_PROBE=injected\n"), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	envProbePath := filepath.Join(dir, "env-probe")
	scriptBody := fmt.Sprintf("#!/bin/sh\nprintf '%%s' \"$ZHULONG_ENV_PROBE\" > '%s'\necho \"ZHULONG_ENV_PROBE=$ZHULONG_ENV_PROBE\"\nsleep 0.3\n", envProbePath)
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "env_pkg",
		AlgorithmName:  "env_pkg",
		ExecutablePath: script,
		EnvFile:        envFile,
		Logger:         logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer proc.Stop(2 * time.Second)

	select {
	case <-proc.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not exit after emitting its probe line")
	}
	value, err := os.ReadFile(envProbePath)
	if err != nil || string(value) != "injected" {
		t.Fatalf("runner did not receive .env value: value=%q err=%v", value, err)
	}
	foundRedactedLine := false
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if entry.Message == "runner output ignored" && fields["source"] == "stdout" && fields["line_bytes"] == int64(len("ZHULONG_ENV_PROBE=injected")) {
			foundRedactedLine = true
		}
		if strings.Contains(entry.Message, "ZHULONG_ENV_PROBE=injected") || strings.Contains(fmt.Sprint(fields), "ZHULONG_ENV_PROBE=injected") {
			t.Fatalf("runner stdout content was not redacted: %+v", entry)
		}
	}
	if !foundRedactedLine {
		t.Fatalf("expected a redacted stdout record with byte count; observed: %+v", observed.All())
	}
}

func TestRunnerProcessMissingDotEnvIsOptional(t *testing.T) {
	// A missing .env file must not be an error: .env is optional.
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0o755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "no_env_pkg",
		AlgorithmName:  "no_env_pkg",
		ExecutablePath: script,
		EnvFile:        filepath.Join(dir, "does_not_exist.env"),
		Logger:         zap.NewNop(),
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start must succeed even when .env is missing, got: %v", err)
	}
	if err := proc.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// TestPackageProcessDetectsBrokerUnavailable drives a package process that exits
// with code 5 (broker connect failure) and asserts the host classifies it as
// broker_unavailable rather than as an ordinary crash.
//
// The process is a stub script on purpose: the classification lives entirely in
// the host (exit-code channel + stderr stage marker), so driving the real
// inference binary here would only make the test slower and arch-dependent.
//
// --plugin is gone from the argv: the executable *is* the algorithm. --broker
// stays, because broker paths are per-task plumbing the host still owns.
func TestPackageProcessDetectsBrokerUnavailable(t *testing.T) {
	script := filepath.Join(t.TempDir(), "broker_probe_pkg")
	body := "#!/bin/sh\nprintf '{\"step\":\"failed\",\"stage\":\"connect\"}\\n' >&2\nexit 5\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("write stub entry: %v", err)
	}

	missingSock := filepath.Join(t.TempDir(), "zhulong_broker_4242.sock")

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "broker_probe_pkg",
		AlgorithmName:  "broker_probe_pkg",
		ExecutablePath: script,
		BrokerSocks:    []string{missingSock},
		Logger:         logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("stub process did not exit within timeout")
	}

	foundReason := false
	foundExit5 := false
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if fields == nil {
			continue
		}
		if fields["reason"] == "broker_unavailable" {
			foundReason = true
		}
		if code, ok := fields["exit_code"]; ok {
			if n, isInt := code.(int64); isInt && n == 5 {
				foundExit5 = true
			}
		}
	}
	if !foundReason || !foundExit5 {
		t.Fatalf("expected broker_unavailable classification with exit code 5; reason=%v exit5=%v logs=%v",
			foundReason, foundExit5, observed.All())
	}
}

func TestRunnerProcessReportsDotEnvParseFailure(t *testing.T) {
	// A .env whose single line exceeds bufio.Scanner's 64KB token limit makes
	// LoadDotEnv return a real error (ErrTooLong). Before R5 that error was
	// swallowed by `if loaded, err := LoadDotEnv(...); err == nil`, silently
	// disabling the whole .env with no observability. The fix must log a Warn
	// and still start the process (a broken .env must not block the runner).
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0o755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	envFile := filepath.Join(dir, ".env")
	oversizedLine := "ZHULONG_OVERSIZED=" + strings.Repeat("x", 70000)
	if err := os.WriteFile(envFile, []byte(oversizedLine+"\n"), 0o644); err != nil {
		t.Fatalf("write oversized .env: %v", err)
	}

	// Sanity: the oversized line really is a parse error, not a silent no-op.
	if _, err := algorunner.LoadDotEnv(envFile); err == nil {
		t.Fatalf("expected LoadDotEnv to report an error for a >64KB line")
	}

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "bad_env_pkg",
		AlgorithmName:  "bad_env_pkg",
		ExecutablePath: script,
		EnvFile:        envFile,
		Logger:         logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start must succeed even when .env fails to parse, got: %v", err)
	}
	if err := proc.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	foundWarn := false
	for _, entry := range observed.All() {
		if entry.Level == zapcore.WarnLevel && strings.Contains(entry.Message, "failed to load runner dotenv file") {
			foundWarn = true
			if fields := entry.ContextMap(); fields != nil {
				if reason, ok := fields["reason"].(string); !ok || reason != "parse_error" {
					t.Errorf("expected stable dotenv parse reason, got %v", fields["reason"])
				}
				if _, hasPath := fields["path"]; hasPath {
					t.Error("dotenv warning must not expose its path")
				}
			}
		}
	}
	if !foundWarn {
		t.Fatalf("expected a Warn log about the failed dotenv load; observed: %+v", observed.All())
	}
}

func TestLoadDotEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	content := `
# Comment line
RKNN_LOG_LEVEL=3
RGA_DEBUG=1
EMPTY_LINE=
OMP_NUM_THREADS="4"
QUOTED_VAL='hello world'
`
	if err := os.WriteFile(envPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	envMap, err := algorunner.LoadDotEnv(envPath)
	if err != nil {
		t.Fatalf("LoadDotEnv failed: %v", err)
	}

	if envMap["RKNN_LOG_LEVEL"] != "3" {
		t.Errorf("expected RKNN_LOG_LEVEL=3, got: %s", envMap["RKNN_LOG_LEVEL"])
	}
	if envMap["RGA_DEBUG"] != "1" {
		t.Errorf("expected RGA_DEBUG=1, got: %s", envMap["RGA_DEBUG"])
	}
	if envMap["OMP_NUM_THREADS"] != "4" {
		t.Errorf("expected OMP_NUM_THREADS=4, got: %s", envMap["OMP_NUM_THREADS"])
	}
	if envMap["QUOTED_VAL"] != "hello world" {
		t.Errorf("expected QUOTED_VAL='hello world', got: %s", envMap["QUOTED_VAL"])
	}
}

func TestIpcMessageFramingAndGalleryMessages(t *testing.T) {
	// 1. Framing and header
	payload := []byte("hello_ipc")
	msgBytes, err := algoipc.EncodeIpcMessage(algoipc.MsgGalleryUpsert, 42, payload)
	if err != nil {
		t.Fatalf("algoipc.EncodeIpcMessage failed: %v", err)
	}

	if len(msgBytes) != 16+len(payload) {
		t.Fatalf("expected length %d, got %d", 16+len(payload), len(msgBytes))
	}

	hdr, err := algoipc.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("algoipc.DecodeIpcHeader failed: %v", err)
	}

	if hdr.Magic != algoipc.IpcMagic {
		t.Errorf("expected magic 0x%X, got 0x%X", algoipc.IpcMagic, hdr.Magic)
	}
	if hdr.MsgType != algoipc.MsgGalleryUpsert {
		t.Errorf("expected msg_type %d, got %d", algoipc.MsgGalleryUpsert, hdr.MsgType)
	}
	if hdr.Sequence != 42 {
		t.Errorf("expected sequence 42, got %d", hdr.Sequence)
	}
	if hdr.PayloadSize != uint32(len(payload)) {
		t.Errorf("expected payload_size %d, got %d", len(payload), hdr.PayloadSize)
	}

	// 2. Gallery sync message on the path the host actually uses: a shard payload
	// framed as MSG_GALLERY_SYNC. (CreateGallerySyncMsg used to frame a single
	// unsharded payload; nothing but this test called it, so the shard path — the
	// one GalleryBroadcaster ships — is what is pinned here.)
	syncItems := []algorunner.GallerySyncItem{
		{
			FaceID:     "f_01",
			PersonID:   "emp_9527",
			PersonName: "张三",
			Features:   []float32{0.1, 0.2, 0.3},
		},
	}
	shards, err := algorunner.CreateGallerySyncShards("sync_test", syncItems)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("expected a single shard for one item, got %d", len(shards))
	}
	syncBytes, err := algoipc.EncodeIpcMessage(algoipc.MsgGallerySync, 1, shards[0])
	if err != nil {
		t.Fatalf("frame gallery sync shard: %v", err)
	}
	syncHdr, err := algoipc.DecodeIpcHeader(syncBytes)
	if err != nil {
		t.Fatalf("decode sync header failed: %v", err)
	}
	if syncHdr.MsgType != algoipc.MsgGallerySync {
		t.Errorf("expected algoipc.MsgGallerySync type, got %d", syncHdr.MsgType)
	}

	var parsedSync algorunner.GallerySyncShardPayload
	if err := json.Unmarshal(syncBytes[16:], &parsedSync); err != nil {
		t.Fatalf("unmarshal sync payload failed: %v", err)
	}
	if parsedSync.ShardTotal != 1 || parsedSync.Items[0].PersonName != "张三" {
		t.Errorf("unexpected sync payload: %+v", parsedSync)
	}
}

func TestRunnerProcessLifecycleGracefulStop(t *testing.T) {
	// Use sleep as a test runner process
	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "test_pkg",
		AlgorithmName:  "test_pkg",
		ExecutablePath: "sleep",
		Logger:         zap.NewNop(),
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start runner process failed: %v", err)
	}

	if proc.State() != algorunner.StatusRunning {
		t.Errorf("expected StatusRunning, got: %s", proc.State())
	}
	if proc.PID() <= 0 {
		t.Errorf("expected valid PID, got: %d", proc.PID())
	}

	// Graceful stop with SIGTERM
	err := proc.Stop(2 * time.Second)
	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	select {
	case <-proc.Done():
		// Clean exit
	case <-time.After(1 * time.Second):
		t.Fatal("process did not terminate after Stop()")
	}

	if proc.State() != algorunner.StatusDisabled {
		t.Errorf("expected StatusDisabled after stop, got: %s", proc.State())
	}
}

func TestRunnerProcessStopKillsProcessGroup(t *testing.T) {
	// R7: the runner is started in its own process group and Stop must signal the
	// whole group, so grandchildren the runner spawned cannot leak. The script
	// backgrounds a long sleep (recording its pid) and then waits on it; after
	// Stop returns the grandchild must be gone.
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "spawning_runner.sh")
	scriptBody := "#!/bin/sh\nsleep 30 &\necho $! > \"$CHILD_PID_FILE\"\nwait\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write spawning runner script: %v", err)
	}

	t.Setenv("CHILD_PID_FILE", pidFile)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "pg_pkg",
		AlgorithmName:  "pg_pkg",
		ExecutablePath: script,
		Logger:         zap.NewNop(),
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Wait for the grandchild pid to be recorded.
	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				childPID = pid
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPID <= 0 {
		t.Fatalf("grandchild pid was never recorded")
	}

	// Sanity: the grandchild is alive before Stop.
	if err := syscall.Kill(childPID, 0); err != nil {
		t.Fatalf("grandchild %d should be alive before Stop: %v", childPID, err)
	}

	if err := proc.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	// After Stop the whole process group must be gone. Give the kernel a moment
	// to reap, then require ESRCH.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived Stop; the runner process group was not killed", childPID)
}

func TestRunnerProcessForceKillOnTimeout(t *testing.T) {
	// Run a shell script that traps SIGTERM and refuses to exit
	trapScript := t.TempDir()
	scriptFile := filepath.Join(trapScript, "trap.sh")
	scriptContent := `#!/bin/sh
trap '' TERM
sleep 10
`
	if err := os.WriteFile(scriptFile, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write trap script: %v", err)
	}

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "stub_pkg",
		AlgorithmName:  "stub_pkg",
		ExecutablePath: "/bin/sh",
		Logger:         zap.NewNop(),
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Short timeout (100ms) will trigger SIGKILL
	start := time.Now()
	err := proc.Stop(100 * time.Millisecond)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	select {
	case <-proc.Done():
		// Process terminated by SIGKILL
	case <-time.After(1 * time.Second):
		t.Fatal("process was not killed")
	}

	if elapsed < 90*time.Millisecond || elapsed > 1*time.Second {
		t.Logf("SIGKILL applied after: %v", elapsed)
	}
}

func TestRunnerProcessMultipleBrokersArgs(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "echo_args.sh")
	argsPath := filepath.Join(dir, "args.txt")
	scriptBody := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_CAPTURE\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:    "multi_broker_pkg",
		AlgorithmName:  "multi_broker_pkg",
		ExecutablePath: script,
		BrokerSocks:    []string{"/tmp/sock1.sock", "/tmp/sock2.sock"},
		ExtraEnv:       map[string]string{"ARGS_CAPTURE": argsPath},
		Logger:         logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer proc.Stop(2 * time.Second)

	select {
	case <-proc.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for script to finish")
	}

	gotBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read captured arguments: %v", err)
	}
	gotArgs := strings.Fields(string(gotBytes))
	// argv 里没有 --plugin：可执行文件本身就是算法。也没有 --max-workers：
	// worker 池规模是包自己的事，宿主不该越权指定。
	wantArgs := []string{
		"--broker", "/tmp/sock1.sock",
		"--broker", "/tmp/sock2.sock",
		"--name", "multi_broker_pkg",
		"--algorithm-id", "multi_broker_pkg",
	}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		t.Fatalf("runner arguments = %#v, want %#v", gotArgs, wantArgs)
	}
	for _, entry := range observed.All() {
		if strings.Contains(fmt.Sprint(entry.ContextMap()), "/tmp/sock") {
			t.Fatalf("runner output leaked broker socket path: %+v", entry)
		}
	}
}

func TestRunnerProcessDisableMotionGateFlag(t *testing.T) {
	for _, disable := range []bool{true, false} {
		t.Run(fmt.Sprintf("disable=%v", disable), func(t *testing.T) {
			dir := t.TempDir()
			script := filepath.Join(dir, "echo_args.sh")
			argsPath := filepath.Join(dir, "args.txt")
			scriptBody := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$ARGS_CAPTURE\"\nexit 0\n"
			if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
				t.Fatalf("write script: %v", err)
			}

			cfg := algorunner.ProcessConfig{
				AlgorithmID:       "temporal_pkg",
				AlgorithmName:     "temporal_pkg",
				ExecutablePath:    script,
				BrokerSocks:       []string{"/tmp/sock.sock"},
				DisableMotionGate: disable,
				ExtraEnv:          map[string]string{"ARGS_CAPTURE": argsPath},
				Logger:            zap.NewNop(),
			}

			proc := algorunner.NewRunnerProcess(cfg)
			if err := proc.Start(); err != nil {
				t.Fatalf("Start failed: %v", err)
			}
			defer proc.Stop(2 * time.Second)

			select {
			case <-proc.Done():
			case <-time.After(15 * time.Second):
				t.Fatal("timed out waiting for script to finish")
			}

			gotBytes, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatalf("read captured arguments: %v", err)
			}
			gotArgs := strings.Fields(string(gotBytes))
			hasFlag := slices.Contains(gotArgs, "--disable-motion-gate")

			if hasFlag != disable {
				t.Errorf("gotArgs = %v, has --disable-motion-gate = %v, want %v", gotArgs, hasFlag, disable)
			}
		})
	}
}

func TestRunnerProcessSpawnThreadSurvivesWhileChildRuns(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "long_lived_runner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntrap 'exit 0' TERM INT\nwhile true; do sleep 0.05; done\n"), 0o755); err != nil {
		t.Fatalf("write long-lived runner: %v", err)
	}

	const runnerCount = 5
	procs := make([]*algorunner.RunnerProcess, 0, runnerCount)
	pids := make([]int, 0, runnerCount)
	t.Cleanup(func() {
		for _, proc := range procs {
			_ = proc.Stop(2 * time.Second)
		}
	})

	for i := 0; i < runnerCount; i++ {
		proc := algorunner.NewRunnerProcess(algorunner.ProcessConfig{
			AlgorithmID:    fmt.Sprintf("thread_pin_pkg_%d", i),
			AlgorithmName:  "thread_pin_pkg",
			ExecutablePath: script,
			Logger:         zap.NewNop(),
		})
		// The spawning goroutine is deliberately not kept alive: it exits as soon
		// as Start returns, which is exactly the host shape this test guards.
		startErr := make(chan error, 1)
		go func() { startErr <- proc.Start() }()
		if err := <-startErr; err != nil {
			t.Fatalf("Start failed: %v", err)
		}
		procs = append(procs, proc)
		pids = append(pids, proc.PID())
	}

	for i, pid := range pids {
		if pid <= 0 {
			t.Fatalf("runner %d pid was never recorded", i)
		}
	}

	// 1.5s is far longer than the ~1s window in which the old implementation lost
	// the thread. A steady timer keeps the scheduler busy so an unpinned spawn
	// goroutine would almost certainly be migrated off its thread.
	deadline := time.Now().Add(1500 * time.Millisecond)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		<-ticker.C
		for i, pid := range pids {
			if err := syscall.Kill(pid, 0); err != nil {
				t.Fatalf("runner %d (pid %d) was killed while it should still be running: %v "+
					"(Pdeathsig fired because the spawn thread was released with its caller)", i, pid, err)
			}
			if state := procs[i].State(); state != algorunner.StatusRunning {
				t.Fatalf("runner %d state = %q, want %q", i, state, algorunner.StatusRunning)
			}
		}
	}

	// The pin must not defeat Stop: the process still has to be killable.
	for i, proc := range procs {
		if err := proc.Stop(2 * time.Second); err != nil {
			t.Fatalf("runner %d Stop failed: %v", i, err)
		}
		select {
		case <-proc.Done():
		case <-time.After(2 * time.Second):
			t.Fatalf("runner %d did not terminate after Stop; the pinned spawn thread leaked", i)
		}
	}
}

// TestRunnerProcessStartFailureLeavesNoHalfStartedState pins the failure half of
// the spawn handshake: a spawn error must settle the process state, close Done
// and release the pinned thread instead of stranding a watcher.
func TestRunnerProcessStartFailureLeavesNoHalfStartedState(t *testing.T) {
	proc := algorunner.NewRunnerProcess(algorunner.ProcessConfig{
		AlgorithmID:    "broken_pkg",
		AlgorithmName:  "broken_pkg",
		ExecutablePath: filepath.Join(t.TempDir(), "does-not-exist-runner"),
		Logger:         zap.NewNop(),
	})
	if err := proc.Start(); err == nil {
		t.Fatal("Start succeeded for a nonexistent runner binary")
	}
	if state := proc.State(); state != algorunner.StatusDisabled {
		t.Fatalf("state after failed Start = %q, want %q", state, algorunner.StatusDisabled)
	}
	if pid := proc.PID(); pid != 0 {
		t.Fatalf("PID after failed Start = %d, want 0", pid)
	}
	select {
	case <-proc.Done():
	case <-time.After(time.Second):
		t.Fatal("Done() was not closed after a failed Start; the lifecycle would hang")
	}
	// Stop must stay a no-op rather than blocking on a half-built process.
	if err := proc.Stop(100 * time.Millisecond); err != nil {
		t.Fatalf("Stop after failed Start = %v", err)
	}
}
