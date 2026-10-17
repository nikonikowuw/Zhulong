package algorunner_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// shardBudget mirrors the unexported algorunner.shardBudget. process_test.go is
// an external (black-box) test package, so it cannot reference the constant
// directly; it must still assert the same bound the implementation guarantees.
const shardBudget = algorunner.MaxIpcPayloadSize * 9 / 10

// testGalleryFeature builds a realistic 512-dim L2-normalized float32 embedding:
// each component is drawn from N(0, 1/512) and the vector is then normalized.
// Serialized to JSON such a component costs ~12.3 bytes (values like -0.044123),
// which is what real face templates look like. The old test's float32(i*1000+j)
// serialized ~2.5x smaller, which is exactly why the fixed-10-items-per-shard
// overflow bug stayed hidden.
func testGalleryFeature(rng *rand.Rand) []float32 {
	feats := make([]float32, 512)
	var sumSq float64
	for i := range feats {
		v := rng.NormFloat64() / math.Sqrt(512)
		feats[i] = float32(v)
		sumSq += float64(feats[i]) * float64(feats[i])
	}
	norm := math.Sqrt(sumSq)
	for i := range feats {
		feats[i] = float32(float64(feats[i]) / norm)
	}
	return feats
}

const testULIDAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// testULIDLike returns a 26-character ULID-shaped identifier.
func testULIDLike(rng *rand.Rand) string {
	b := make([]byte, 26)
	for i := range b {
		b[i] = testULIDAlphabet[rng.Intn(len(testULIDAlphabet))]
	}
	return string(b)
}

var testCJKNames = []string{"张三", "李四", "王五", "赵六", "陈七", "刘八", "黄九", "周十"}

// testRealisticGalleryItems builds n items that match the real production shape:
// 512-dim L2-normalized features, 26-char ULID face/person IDs, a CJK name and
// a realistic image path.
func testRealisticGalleryItems(n int) []algorunner.GallerySyncItem {
	rng := rand.New(rand.NewSource(20241017))
	items := make([]algorunner.GallerySyncItem, n)
	for i := range items {
		faceID := testULIDLike(rng)
		items[i] = algorunner.GallerySyncItem{
			FaceID:     faceID,
			PersonID:   testULIDLike(rng),
			PersonName: testCJKNames[i%len(testCJKNames)],
			ImagePath:  fmt.Sprintf("faces/2024/06/17/%s_112x112.jpg", faceID),
			Features:   testGalleryFeature(rng),
		}
	}
	return items
}

func decodeShard(t *testing.T, payload []byte) algorunner.GallerySyncShardPayload {
	t.Helper()
	var p algorunner.GallerySyncShardPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		t.Fatalf("unmarshal shard payload: %v", err)
	}
	return p
}

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
	exact := make([]byte, algorunner.MaxIpcPayloadSize)
	msgBytes, err := algorunner.EncodeIpcMessage(algorunner.MsgGallerySync, 1, exact)
	if err != nil {
		t.Fatalf("payload of exactly MaxIpcPayloadSize must be accepted, got: %v", err)
	}
	if len(msgBytes) != 16+algorunner.MaxIpcPayloadSize {
		t.Fatalf("unexpected framed length: %d", len(msgBytes))
	}
	hdr, err := algorunner.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("DecodeIpcHeader on boundary payload failed: %v", err)
	}
	if hdr.PayloadSize != uint32(algorunner.MaxIpcPayloadSize) {
		t.Errorf("expected payload_size %d, got %d", algorunner.MaxIpcPayloadSize, hdr.PayloadSize)
	}

	// One byte over the limit must be rejected by the send path.
	tooLarge := make([]byte, algorunner.MaxIpcPayloadSize+1)
	if _, err := algorunner.EncodeIpcMessage(algorunner.MsgGallerySync, 1, tooLarge); !errors.Is(err, algorunner.ErrIpcPayloadTooLarge) {
		t.Errorf("expected ErrIpcPayloadTooLarge for 64KB+1, got: %v", err)
	}

	// The receive path must also reject a header declaring an oversized payload.
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint32(buf[0:4], algorunner.IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], algorunner.MsgGallerySync)
	binary.LittleEndian.PutUint16(buf[6:8], algorunner.IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(algorunner.MaxIpcPayloadSize+1))
	binary.LittleEndian.PutUint32(buf[12:16], 1)
	if _, err := algorunner.DecodeIpcHeader(buf); !errors.Is(err, algorunner.ErrIpcPayloadTooLarge) {
		t.Errorf("expected DecodeIpcHeader to reject oversized payload_size, got: %v", err)
	}
}

func TestRunnerProcessInjectsDotEnvEndToEnd(t *testing.T) {
	// This drives the real os/exec environment-injection path: a shell script
	// acts as the runner binary and echoes an env var that only exists in the
	// package's .env file. Testing LoadDotEnv directly would not catch a broken
	// wiring (EnvFile never being set), which is exactly what this task fixes.
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	scriptBody := "#!/bin/sh\necho \"ZHULONG_ENV_PROBE=$ZHULONG_ENV_PROBE\"\nsleep 0.3\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	envFile := filepath.Join(dir, ".env")
	if err := os.WriteFile(envFile, []byte("ZHULONG_ENV_PROBE=injected\n"), 0644); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "env_pkg",
		AlgorithmName: "env_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		EnvFile:       envFile,
		Logger:        logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer proc.Stop(2 * time.Second)

	// Wait for the child to emit its line (up to 5s under concurrent -race load).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, entry := range observed.All() {
			if strings.Contains(entry.Message, "ZHULONG_ENV_PROBE=injected") {
				return
			}
			if f := entry.ContextMap(); f != nil {
				if text, ok := f["text"].(string); ok && strings.Contains(text, "ZHULONG_ENV_PROBE=injected") {
					return
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	var lines []string
	for _, entry := range observed.All() {
		lines = append(lines, entry.Message)
	}
	t.Fatalf("expected injected env var in runner stdout logs; observed: %v", lines)
}

func TestRunnerProcessMissingDotEnvIsOptional(t *testing.T) {
	// A missing .env file must not be an error: .env is optional.
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "no_env_pkg",
		AlgorithmName: "no_env_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		EnvFile:       filepath.Join(dir, "does_not_exist.env"),
		Logger:        zap.NewNop(),
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start must succeed even when .env is missing, got: %v", err)
	}
	if err := proc.Stop(2 * time.Second); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// findRealRunnerBinary resolves the real zhulong_algo_runner binary, honouring
// ZHULONG_ALGO_RUNNER first and then standard build locations. Skips when the
// binary cannot be found.
func findRealRunnerBinary(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("ZHULONG_ALGO_RUNNER"),
		"../../build/native/host/zhulong_algo_runner",
		"build/native/host/zhulong_algo_runner",
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		abs, err := filepath.Abs(c)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
			return abs
		}
	}
	t.Skip("zhulong_algo_runner not found (set ZHULONG_ALGO_RUNNER), skipping broker-host-unavailable test")
	return ""
}

// TestRunnerProcessDetectsBrokerUnavailable drives the REAL runner binary
// against a broker socket path with no listening host. The runner must exit with
// code 5 / stage=connect, which must surface as a distinguishable
// broker_unavailable signal rather than an ordinary crash.
func TestRunnerProcessDetectsBrokerHostUnavailable(t *testing.T) {
	runnerPath := findRealRunnerBinary(t)
	pluginPath := findMockDetectorPlugin(t)

	// Point --broker at a literal socket path with no listening host. The path
	// must match the host's derivation rule (<base_dir>/zhulong_broker_<sid>.sock)
	// because the runner now derives its task identity from it.
	missingSock := filepath.Join(t.TempDir(), "zhulong_broker_4242.sock")

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "broker_probe_pkg",
		AlgorithmName: "broker_probe_pkg",
		RunnerPath:    runnerPath,
		PluginPath:    pluginPath,
		BrokerSocks:   []string{missingSock},
		Logger:        logger,
	}

	proc := algorunner.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	select {
	case <-proc.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("runner did not exit within timeout")
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
			if code, ok := fields["exit_code"].(int64); ok && code == 5 {
				foundExit5 = true
			}
		}
	}

	if !foundReason {
		t.Errorf("expected a broker_unavailable log entry")
	}
	if !foundExit5 {
		t.Errorf("expected exit_code=5 in the broker_unavailable log entry")
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
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	envFile := filepath.Join(dir, ".env")
	oversizedLine := "ZHULONG_OVERSIZED=" + strings.Repeat("x", 70000)
	if err := os.WriteFile(envFile, []byte(oversizedLine+"\n"), 0644); err != nil {
		t.Fatalf("write oversized .env: %v", err)
	}

	// Sanity: the oversized line really is a parse error, not a silent no-op.
	if _, err := algorunner.LoadDotEnv(envFile); err == nil {
		t.Fatalf("expected LoadDotEnv to report an error for a >64KB line")
	}

	core, observed := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "bad_env_pkg",
		AlgorithmName: "bad_env_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		EnvFile:       envFile,
		Logger:        logger,
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
				if path, ok := fields["path"].(string); !ok || path != envFile {
					t.Errorf("expected dotenv warn to carry path=%q, got %v", envFile, fields["path"])
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
	if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
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
	msgBytes, err := algorunner.EncodeIpcMessage(algorunner.MsgGalleryUpsert, 42, payload)
	if err != nil {
		t.Fatalf("EncodeIpcMessage failed: %v", err)
	}

	if len(msgBytes) != 16+len(payload) {
		t.Fatalf("expected length %d, got %d", 16+len(payload), len(msgBytes))
	}

	hdr, err := algorunner.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("DecodeIpcHeader failed: %v", err)
	}

	if hdr.Magic != algorunner.IpcMagic {
		t.Errorf("expected magic 0x%X, got 0x%X", algorunner.IpcMagic, hdr.Magic)
	}
	if hdr.MsgType != algorunner.MsgGalleryUpsert {
		t.Errorf("expected msg_type %d, got %d", algorunner.MsgGalleryUpsert, hdr.MsgType)
	}
	if hdr.Sequence != 42 {
		t.Errorf("expected sequence 42, got %d", hdr.Sequence)
	}
	if hdr.PayloadSize != uint32(len(payload)) {
		t.Errorf("expected payload_size %d, got %d", len(payload), hdr.PayloadSize)
	}

	// 2. Full Gallery Sync Message
	syncItems := []algorunner.GallerySyncItem{
		{
			FaceID:     "f_01",
			PersonID:   "emp_9527",
			PersonName: "张三",
			Features:   []float32{0.1, 0.2, 0.3},
		},
	}
	syncBytes, err := algorunner.CreateGallerySyncMsg(1, syncItems)
	if err != nil {
		t.Fatalf("CreateGallerySyncMsg failed: %v", err)
	}
	syncHdr, err := algorunner.DecodeIpcHeader(syncBytes)
	if err != nil {
		t.Fatalf("decode sync header failed: %v", err)
	}
	if syncHdr.MsgType != algorunner.MsgGallerySync {
		t.Errorf("expected MsgGallerySync type, got %d", syncHdr.MsgType)
	}

	var parsedSync algorunner.GallerySyncPayload
	if err := json.Unmarshal(syncBytes[16:], &parsedSync); err != nil {
		t.Fatalf("unmarshal sync payload failed: %v", err)
	}
	if len(parsedSync.Items) != 1 || parsedSync.Items[0].PersonName != "张三" {
		t.Errorf("unexpected sync payload: %+v", parsedSync)
	}
}

func TestRunnerProcessLifecycleGracefulStop(t *testing.T) {
	// Use sleep as a test runner process
	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "test_pkg",
		AlgorithmName: "test_pkg",
		RunnerPath:    "sleep",
		PluginPath:    "5", // sleep for 5 seconds
		Logger:        zap.NewNop(),
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
	if err := os.WriteFile(script, []byte(scriptBody), 0755); err != nil {
		t.Fatalf("write spawning runner script: %v", err)
	}

	t.Setenv("CHILD_PID_FILE", pidFile)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "pg_pkg",
		AlgorithmName: "pg_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		Logger:        zap.NewNop(),
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
	if err := os.WriteFile(scriptFile, []byte(scriptContent), 0755); err != nil {
		t.Fatalf("write trap script: %v", err)
	}

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "stub_pkg",
		AlgorithmName: "stub_pkg",
		RunnerPath:    "/bin/sh",
		PluginPath:    scriptFile,
		Logger:        zap.NewNop(),
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
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho \"ARGS: $@\"\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	cfg := algorunner.ProcessConfig{
		AlgorithmID:   "multi_broker_pkg",
		AlgorithmName: "multi_broker_pkg",
		RunnerPath:    script,
		PluginPath:    "plugin.so",
		BrokerSocks:   []string{"/tmp/sock1.sock", "/tmp/sock2.sock"},
		Logger:        logger,
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

	found := false
	for _, entry := range observed.All() {
		for _, f := range entry.Context {
			if strings.Contains(f.String, "--broker /tmp/sock1.sock --broker /tmp/sock2.sock") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("expected command line to contain multiple --broker flags, got logs: %+v", observed.All())
	}
}

type mockBroadcaster struct {
	messages []struct {
		streamID uint64
		msgType  uint16
		payload  []byte
	}
	err error
}

func (m *mockBroadcaster) BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error {
	if m.err != nil {
		return m.err
	}
	m.messages = append(m.messages, struct {
		streamID uint64
		msgType  uint16
		payload  []byte
	}{streamID: streamID, msgType: msgType, payload: payload})
	return nil
}

func TestCreateGallerySyncShards_Empty(t *testing.T) {
	shards, err := algorunner.CreateGallerySyncShards("sync_empty", nil)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}
	var p algorunner.GallerySyncShardPayload
	if err := json.Unmarshal(shards[0], &p); err != nil {
		t.Fatalf("unmarshal shard payload: %v", err)
	}
	if p.SyncID != "sync_empty" || p.ShardIndex != 0 || p.ShardTotal != 1 || len(p.Items) != 0 {
		t.Fatalf("unexpected shard payload: %+v", p)
	}
}

func TestCreateGallerySyncShards_MultiShardUnder64KB(t *testing.T) {
	// Realistic magnitude: 25 real-size items must pack into multiple shards,
	// each strictly within shardBudget. The old fixed 10-items-per-shard packing
	// produced ~64KB shards on this data (over budget), which this asserts.
	items := testRealisticGalleryItems(25)

	shards, err := algorunner.CreateGallerySyncShards("sync_multi", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) < 2 {
		t.Fatalf("expected multiple shards for 25 realistic items, got %d", len(shards))
	}

	for i, s := range shards {
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget (%d): %d bytes", i, shardBudget, len(s))
		}
		p := decodeShard(t, s)
		if p.ShardIndex != i || p.ShardTotal != len(shards) {
			t.Errorf("shard %d metadata mismatch: index=%d, total=%d, want total=%d", i, p.ShardIndex, p.ShardTotal, len(shards))
		}
		if len(p.Items) == 0 {
			t.Errorf("shard %d unexpectedly empty", i)
		}
	}
}

func TestCreateGallerySyncShards_SingleItemExceedsBudget(t *testing.T) {
	// A single item larger than shardBudget cannot be sharded. The returned error
	// must be locatable (contains the face_id) so operators can find the record.
	item := testRealisticGalleryItems(1)[0]
	item.PersonName = strings.Repeat("很", 20000) // ~60KB of UTF-8, well over budget on its own

	_, err := algorunner.CreateGallerySyncShards("sync_oversized", []algorunner.GallerySyncItem{item})
	if err == nil {
		t.Fatalf("expected an error for a single item exceeding the shard budget")
	}
	if !strings.Contains(err.Error(), item.FaceID) {
		t.Fatalf("expected the error to contain the face_id %q, got: %v", item.FaceID, err)
	}
}

func TestCreateGallerySyncShards_RoundTripIntegrity(t *testing.T) {
	// 137 is deliberately not a multiple of any packing size: assert the shard
	// metadata and that concatenating the shards reproduces the input exactly
	// (no loss, no duplication, no reordering).
	items := testRealisticGalleryItems(137)

	shards, err := algorunner.CreateGallerySyncShards("sync_roundtrip", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}

	var rebuilt []algorunner.GallerySyncItem
	for i, s := range shards {
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget: %d bytes", i, len(s))
		}
		p := decodeShard(t, s)
		if p.SyncID != "sync_roundtrip" {
			t.Errorf("shard %d sync_id = %q", i, p.SyncID)
		}
		if p.ShardIndex != i {
			t.Errorf("shard %d shard_index = %d, want %d", i, p.ShardIndex, i)
		}
		if p.ShardTotal != len(shards) {
			t.Errorf("shard %d shard_total = %d, want %d", i, p.ShardTotal, len(shards))
		}
		rebuilt = append(rebuilt, p.Items...)
	}

	if len(rebuilt) != len(items) {
		t.Fatalf("rebuilt item count = %d, want %d", len(rebuilt), len(items))
	}
	for i := range items {
		if !reflect.DeepEqual(rebuilt[i], items[i]) {
			t.Errorf("item %d mismatch after round trip:\n got %+v\nwant %+v", i, rebuilt[i], items[i])
		}
	}
}

func TestCreateGallerySyncShards_ShardBudgetHoldsForRealisticGallery(t *testing.T) {
	// Full-scale cold start: 200 real-size items. This MUST fail against the old
	// fixed 10-items-per-shard implementation (each ~64KB > shardBudget).
	items := testRealisticGalleryItems(200)

	shards, err := algorunner.CreateGallerySyncShards("sync_full", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}

	maxSize := 0
	total := 0
	for i, s := range shards {
		if len(s) > maxSize {
			maxSize = len(s)
		}
		if len(s) > shardBudget {
			t.Errorf("shard %d exceeds shardBudget (%d): %d bytes", i, shardBudget, len(s))
		}
		if len(s) > int(algorunner.MaxIpcPayloadSize) {
			t.Errorf("shard %d exceeds the protocol limit: %d bytes", i, len(s))
		}
		p := decodeShard(t, s)
		total += len(p.Items)
	}
	if total != len(items) {
		t.Errorf("packed %d items across shards, want %d", total, len(items))
	}
	t.Logf("shardBudget=%d, shards=%d, maxShardSize=%d", shardBudget, len(shards), maxSize)
}

func TestGalleryBroadcaster_SyncUpsertDelete(t *testing.T) {
	mock := &mockBroadcaster{}
	broadcaster := algorunner.NewGalleryBroadcaster(mock, zap.NewNop())
	ctx := context.Background()

	// 1. Sync
	items := []algorunner.GallerySyncItem{{FaceID: "f1", PersonID: "p1", PersonName: "张三"}}
	if err := broadcaster.BroadcastSync(ctx, "sync_test", items); err != nil {
		t.Fatalf("BroadcastSync failed: %v", err)
	}

	// 2. Upsert
	upsertItem := algorunner.GalleryUpsertPayload{FaceID: "f2", PersonID: "p2", PersonName: "李四"}
	if err := broadcaster.BroadcastUpsert(ctx, upsertItem); err != nil {
		t.Fatalf("BroadcastUpsert failed: %v", err)
	}

	// 3. Delete
	if err := broadcaster.BroadcastDelete(ctx, "f2", "p2"); err != nil {
		t.Fatalf("BroadcastDelete failed: %v", err)
	}

	if len(mock.messages) != 3 {
		t.Fatalf("expected 3 broadcast messages, got %d", len(mock.messages))
	}
	if mock.messages[0].msgType != algorunner.MsgGallerySync {
		t.Errorf("expected MsgGallerySync (10), got %d", mock.messages[0].msgType)
	}
	if mock.messages[1].msgType != algorunner.MsgGalleryUpsert {
		t.Errorf("expected MsgGalleryUpsert (11), got %d", mock.messages[1].msgType)
	}
	if mock.messages[2].msgType != algorunner.MsgGalleryDelete {
		t.Errorf("expected MsgGalleryDelete (12), got %d", mock.messages[2].msgType)
	}
}
