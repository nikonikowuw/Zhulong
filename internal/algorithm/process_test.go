package algorithm_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestIpcPayloadSizeGate(t *testing.T) {
	// Exactly at the limit must pass.
	exact := make([]byte, algorithm.MaxIpcPayloadSize)
	msgBytes, err := algorithm.EncodeIpcMessage(algorithm.MsgGallerySync, 1, exact)
	if err != nil {
		t.Fatalf("payload of exactly MaxIpcPayloadSize must be accepted, got: %v", err)
	}
	if len(msgBytes) != 16+algorithm.MaxIpcPayloadSize {
		t.Fatalf("unexpected framed length: %d", len(msgBytes))
	}
	hdr, err := algorithm.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("DecodeIpcHeader on boundary payload failed: %v", err)
	}
	if hdr.PayloadSize != uint32(algorithm.MaxIpcPayloadSize) {
		t.Errorf("expected payload_size %d, got %d", algorithm.MaxIpcPayloadSize, hdr.PayloadSize)
	}

	// One byte over the limit must be rejected by the send path.
	tooLarge := make([]byte, algorithm.MaxIpcPayloadSize+1)
	if _, err := algorithm.EncodeIpcMessage(algorithm.MsgGallerySync, 1, tooLarge); !errors.Is(err, algorithm.ErrIpcPayloadTooLarge) {
		t.Errorf("expected ErrIpcPayloadTooLarge for 64KB+1, got: %v", err)
	}

	// The receive path must also reject a header declaring an oversized payload.
	buf := make([]byte, 16)
	binary.LittleEndian.PutUint32(buf[0:4], algorithm.IpcMagic)
	binary.LittleEndian.PutUint16(buf[4:6], algorithm.MsgGallerySync)
	binary.LittleEndian.PutUint16(buf[6:8], algorithm.IpcVersion)
	binary.LittleEndian.PutUint32(buf[8:12], uint32(algorithm.MaxIpcPayloadSize+1))
	binary.LittleEndian.PutUint32(buf[12:16], 1)
	if _, err := algorithm.DecodeIpcHeader(buf); !errors.Is(err, algorithm.ErrIpcPayloadTooLarge) {
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

	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "env_pkg",
		AlgorithmName: "env_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		EnvFile:       envFile,
		Logger:        logger,
	}

	proc := algorithm.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer proc.Stop(2 * time.Second)

	// Wait for the child to emit its line.
	deadline := time.Now().Add(2 * time.Second)
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
	// A missing .env file must not be an error: .env is optional per model.go.
	dir := t.TempDir()
	script := filepath.Join(dir, "runner.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 0.3\n"), 0755); err != nil {
		t.Fatalf("write runner script: %v", err)
	}

	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "no_env_pkg",
		AlgorithmName: "no_env_pkg",
		RunnerPath:    script,
		PluginPath:    "unused.so",
		EnvFile:       filepath.Join(dir, "does_not_exist.env"),
		Logger:        zap.NewNop(),
	}

	proc := algorithm.NewRunnerProcess(cfg)
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

	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "broker_probe_pkg",
		AlgorithmName: "broker_probe_pkg",
		RunnerPath:    runnerPath,
		PluginPath:    pluginPath,
		BrokerSocks:   []string{missingSock},
		Logger:        logger,
	}

	proc := algorithm.NewRunnerProcess(cfg)
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

	envMap, err := algorithm.LoadDotEnv(envPath)
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
	msgBytes, err := algorithm.EncodeIpcMessage(algorithm.MsgGalleryUpsert, 42, payload)
	if err != nil {
		t.Fatalf("EncodeIpcMessage failed: %v", err)
	}

	if len(msgBytes) != 16+len(payload) {
		t.Fatalf("expected length %d, got %d", 16+len(payload), len(msgBytes))
	}

	hdr, err := algorithm.DecodeIpcHeader(msgBytes)
	if err != nil {
		t.Fatalf("DecodeIpcHeader failed: %v", err)
	}

	if hdr.Magic != algorithm.IpcMagic {
		t.Errorf("expected magic 0x%X, got 0x%X", algorithm.IpcMagic, hdr.Magic)
	}
	if hdr.MsgType != algorithm.MsgGalleryUpsert {
		t.Errorf("expected msg_type %d, got %d", algorithm.MsgGalleryUpsert, hdr.MsgType)
	}
	if hdr.Sequence != 42 {
		t.Errorf("expected sequence 42, got %d", hdr.Sequence)
	}
	if hdr.PayloadSize != uint32(len(payload)) {
		t.Errorf("expected payload_size %d, got %d", len(payload), hdr.PayloadSize)
	}

	// 2. Full Gallery Sync Message
	syncItems := []algorithm.GallerySyncItem{
		{
			FaceID:     "f_01",
			PersonID:   "emp_9527",
			PersonName: "张三",
			Features:   []float32{0.1, 0.2, 0.3},
		},
	}
	syncBytes, err := algorithm.CreateGallerySyncMsg(1, syncItems)
	if err != nil {
		t.Fatalf("CreateGallerySyncMsg failed: %v", err)
	}
	syncHdr, err := algorithm.DecodeIpcHeader(syncBytes)
	if err != nil {
		t.Fatalf("decode sync header failed: %v", err)
	}
	if syncHdr.MsgType != algorithm.MsgGallerySync {
		t.Errorf("expected MsgGallerySync type, got %d", syncHdr.MsgType)
	}

	var parsedSync algorithm.GallerySyncPayload
	if err := json.Unmarshal(syncBytes[16:], &parsedSync); err != nil {
		t.Fatalf("unmarshal sync payload failed: %v", err)
	}
	if len(parsedSync.Items) != 1 || parsedSync.Items[0].PersonName != "张三" {
		t.Errorf("unexpected sync payload: %+v", parsedSync)
	}
}

func TestRunnerProcessLifecycleGracefulStop(t *testing.T) {
	// Use sleep as a test runner process
	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "test_pkg",
		AlgorithmName: "test_pkg",
		RunnerPath:    "sleep",
		PluginPath:    "5", // sleep for 5 seconds
		Logger:        zap.NewNop(),
	}

	proc := algorithm.NewRunnerProcess(cfg)
	if err := proc.Start(); err != nil {
		t.Fatalf("Start runner process failed: %v", err)
	}

	if proc.State() != algorithm.StatusRunning {
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

	if proc.State() != algorithm.StatusDisabled {
		t.Errorf("expected StatusDisabled after stop, got: %s", proc.State())
	}
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

	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "stub_pkg",
		AlgorithmName: "stub_pkg",
		RunnerPath:    "/bin/sh",
		PluginPath:    scriptFile,
		Logger:        zap.NewNop(),
	}

	proc := algorithm.NewRunnerProcess(cfg)
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

	cfg := algorithm.ProcessConfig{
		AlgorithmID:   "multi_broker_pkg",
		AlgorithmName: "multi_broker_pkg",
		RunnerPath:    script,
		PluginPath:    "plugin.so",
		BrokerSocks:   []string{"/tmp/sock1.sock", "/tmp/sock2.sock"},
		Logger:        logger,
	}

	proc := algorithm.NewRunnerProcess(cfg)
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
	shards, err := algorithm.CreateGallerySyncShards("sync_empty", nil)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) != 1 {
		t.Fatalf("expected 1 shard, got %d", len(shards))
	}
	var p algorithm.GallerySyncShardPayload
	if err := json.Unmarshal(shards[0], &p); err != nil {
		t.Fatalf("unmarshal shard payload: %v", err)
	}
	if p.SyncID != "sync_empty" || p.ShardIndex != 0 || p.ShardTotal != 1 || len(p.Items) != 0 {
		t.Fatalf("unexpected shard payload: %+v", p)
	}
}

func TestCreateGallerySyncShards_MultiShardUnder64KB(t *testing.T) {
	// 构造 25 个人脸项（应切分为 3 个 shard：10 + 10 + 5）
	items := make([]algorithm.GallerySyncItem, 25)
	for i := range items {
		feats := make([]float32, 512)
		for j := range feats {
			feats[j] = float32(i*1000 + j)
		}
		items[i] = algorithm.GallerySyncItem{
			FaceID:     fmt.Sprintf("f_%d", i),
			PersonID:   fmt.Sprintf("p_%d", i),
			PersonName: fmt.Sprintf("人员%d", i),
			ImagePath:  fmt.Sprintf("img_%d.jpg", i),
			Features:   feats,
		}
	}

	shards, err := algorithm.CreateGallerySyncShards("sync_multi", items)
	if err != nil {
		t.Fatalf("CreateGallerySyncShards failed: %v", err)
	}
	if len(shards) != 3 {
		t.Fatalf("expected 3 shards for 25 items, got %d", len(shards))
	}

	for i, s := range shards {
		if len(s) > int(algorithm.MaxIpcPayloadSize) {
			t.Errorf("shard %d exceeds 64KB: %d bytes", i, len(s))
		}
		var p algorithm.GallerySyncShardPayload
		if err := json.Unmarshal(s, &p); err != nil {
			t.Fatalf("unmarshal shard %d: %v", i, err)
		}
		if p.ShardIndex != i || p.ShardTotal != 3 {
			t.Errorf("shard %d metadata mismatch: index=%d, total=%d", i, p.ShardIndex, p.ShardTotal)
		}
		if i < 2 && len(p.Items) != 10 {
			t.Errorf("shard %d expected 10 items, got %d", i, len(p.Items))
		}
		if i == 2 && len(p.Items) != 5 {
			t.Errorf("shard 2 expected 5 items, got %d", len(p.Items))
		}
	}
}

func TestGalleryBroadcaster_SyncUpsertDelete(t *testing.T) {
	mock := &mockBroadcaster{}
	broadcaster := algorithm.NewGalleryBroadcaster(mock, zap.NewNop())
	ctx := context.Background()

	// 1. Sync
	items := []algorithm.GallerySyncItem{{FaceID: "f1", PersonID: "p1", PersonName: "张三"}}
	if err := broadcaster.BroadcastSync(ctx, "sync_test", items); err != nil {
		t.Fatalf("BroadcastSync failed: %v", err)
	}

	// 2. Upsert
	upsertItem := algorithm.GalleryUpsertPayload{FaceID: "f2", PersonID: "p2", PersonName: "李四"}
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
	if mock.messages[0].msgType != algorithm.MsgGallerySync {
		t.Errorf("expected MsgGallerySync (10), got %d", mock.messages[0].msgType)
	}
	if mock.messages[1].msgType != algorithm.MsgGalleryUpsert {
		t.Errorf("expected MsgGalleryUpsert (11), got %d", mock.messages[1].msgType)
	}
	if mock.messages[2].msgType != algorithm.MsgGalleryDelete {
		t.Errorf("expected MsgGalleryDelete (12), got %d", mock.messages[2].msgType)
	}
}
