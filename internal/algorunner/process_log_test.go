package algorunner

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func findRunnerBinaryForLogTest(t *testing.T) string {
	t.Helper()
	for _, candidate := range []string{
		os.Getenv("ZHULONG_ALGO_RUNNER"),
		"../../build/native/host/zhulong_algo_runner",
		"build/native/host/zhulong_algo_runner",
	} {
		if candidate == "" {
			continue
		}
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs
		}
	}
	t.Skip("zhulong_algo_runner not found (set ZHULONG_ALGO_RUNNER), skipping standalone runner log test")
	return ""
}

func TestStandaloneRunnerEmitsSafePluginLoadLog(t *testing.T) {
	runnerPath := findRunnerBinaryForLogTest(t)
	pluginPath := filepath.Join(t.TempDir(), "private-plugin-directory", "missing-plugin.so")
	cmd := exec.Command(runnerPath, "--verify-plugin", pluginPath)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err == nil {
		t.Fatal("runner unexpectedly loaded a missing plugin")
	} else if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("runner exit = %v, want exit code 1", err)
	}
	if stdout.Len() == 0 {
		t.Fatal("runner protocol output was unexpectedly empty")
	}
	for _, stream := range []string{stdout.String(), stderr.String()} {
		for _, line := range strings.Split(stream, "\n") {
			if line != "" && !json.Valid([]byte(line)) {
				t.Fatalf("runner emitted a malformed JSON line (%d bytes)", len(line))
			}
		}
	}

	foundSafeLog := false
	for _, line := range strings.Split(stderr.String(), "\n") {
		if !strings.Contains(line, `"zhulongLog":true`) {
			continue
		}
		classified := classifyRunnerLine([]byte(line), "stderr", zapcore.WarnLevel)
		fields := zapFieldsString(classified.fields)
		if classified.message != "runner native log" || fieldValue(classified.fields, "module").String != "algorithm.runner" {
			t.Fatalf("runner native log classification = %+v", classified)
		}
		if strings.Contains(fields, pluginPath) || fieldValue(classified.fields, "message_bytes").Integer != int64(len("plugin_load_failed reason=dlopen")) {
			t.Fatalf("runner log exposed untrusted detail or lost its safe summary: %s", fields)
		}
		foundSafeLog = true
	}
	if !foundSafeLog {
		t.Fatal("runner did not emit a structured plugin load failure event")
	}
}

func TestClassifyRunnerLinesRedactsUntrustedText(t *testing.T) {
	secret := "rtsp://user:password@camera/private /tmp/secret.sock"
	protocol := classifyRunnerLine([]byte(`{"status":"failed","stage":"connect","rc":5,"error":"`+secret+`"}`), "stderr", zapcore.WarnLevel)
	if protocol.message != "runner protocol event" || protocol.stage != "connect" {
		t.Fatalf("protocol classification = %+v", protocol)
	}
	protocolFields := zapFieldsString(protocol.fields)
	if strings.Contains(protocolFields, secret) || !hasField(protocol.fields, "error_present") || fieldValue(protocol.fields, "error_present").Integer != 1 || fieldValue(protocol.fields, "rc").Integer != 5 {
		t.Fatalf("protocol fields did not preserve only safe diagnostics: %s", protocolFields)
	}

	native := classifyRunnerLine([]byte(`{"zhulongLog":true,"level":"error","module":"pipeline.stream","streamId":17,"message":"`+secret+`"}`), "stderr", zapcore.WarnLevel)
	if native.message != "runner native log" || native.level != zapcore.ErrorLevel {
		t.Fatalf("native log classification = %+v", native)
	}
	nativeFields := zapFieldsString(native.fields)
	if strings.Contains(nativeFields, secret) || fieldValue(native.fields, "message_bytes").Integer != int64(len(secret)) || fieldValue(native.fields, "stream_id").Integer != 17 {
		t.Fatalf("native fields did not redact message: %s", nativeFields)
	}

	unknown := classifyRunnerLine([]byte(secret), "stdout", zapcore.DebugLevel)
	unknownFields := zapFieldsString(unknown.fields)
	if unknown.message != "runner output ignored" || strings.Contains(unknownFields, secret) {
		t.Fatalf("unparsed output was not redacted: %+v / %s", unknown, unknownFields)
	}

	invalid := classifyRunnerLine([]byte(`{"status":"failed","stage":"`+secret+`"}`), "stderr", zapcore.WarnLevel)
	if !invalid.invalid || strings.Contains(zapFieldsString(invalid.fields), secret) {
		t.Fatalf("invalid protocol token was not rejected safely: %+v", invalid)
	}
}

// TestStreamLogReaderAcceptsBrokerDisconnectEvent pins the cross-layer contract
// with native/src/algorithm/runner/runner.cpp: when the runner loses its broker
// it emits {"status":"info","stage":"broker_disconnected"} on stderr. Both
// tokens must be in the allowlist — otherwise the one event that distinguishes
// "host died" from "plugin crashed" is downgraded to "record rejected" and
// counted as invalid output.
func TestStreamLogReaderAcceptsBrokerDisconnectEvent(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	process.streamLogReader(strings.NewReader(
		`{"status":"info","stage":"broker_disconnected","index":0,"frames_processed":9001}`+"\n"+
			`{"status":"failed","stage":"broker_disconnected"}`+"\n"), "stderr", zapcore.WarnLevel)

	if invalid := process.logStats[runnerSourceIndex("stderr")].invalid; invalid != 0 {
		t.Fatalf("broker disconnect events counted as invalid runner output: %d", invalid)
	}
	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("expected two protocol events, got %+v", entries)
	}
	if entries[0].Message != "runner protocol event" {
		t.Fatalf("broker disconnect classified as %q", entries[0].Message)
	}
	fields := entries[0].ContextMap()
	if fields["status"] != "info" || fields["stage"] != "broker_disconnected" {
		t.Fatalf("broker disconnect event lost its tokens: %+v", fields)
	}
	// It must stay a one-line event: the runner's numeric diagnostics are not
	// logged verbatim, only the allowlisted protocol tokens plus presence flags.
	if _, exists := fields["frames_processed"]; exists {
		t.Fatalf("runner-specific numeric field leaked into the log event: %+v", fields)
	}
	if entries[1].ContextMap()["status"] != "failed" {
		t.Fatalf("failed/disconnected event = %+v", entries[1].ContextMap())
	}
}

// TestClassifyRunnerLineStillRejectsUnknownTokens guards the other half of the
// allowlist: accepting the runner's real vocabulary must not turn the check into
// a wildcard, or arbitrary runner text would be promoted to a structured event.
func TestClassifyRunnerLineStillRejectsUnknownTokens(t *testing.T) {
	for _, line := range []string{
		`{"status":"totally_new_status"}`,
		`{"status":"failed","stage":"totally_new_stage"}`,
		`{"status":"info","stage":"broker_disconnected","rc":"not-a-number"}`,
	} {
		parsed := classifyRunnerLine([]byte(line), "stderr", zapcore.WarnLevel)
		if !parsed.invalid || parsed.message != "runner protocol record rejected" {
			t.Fatalf("line %s was not rejected: %+v", line, parsed)
		}
	}
}

func TestReadBoundedLineDrainsAndRecovers(t *testing.T) {
	long := strings.Repeat("x", maxRunnerLogLine+257)
	input := long + "\n{\"status\":\"failed\",\"stage\":\"connect\"}\nfinal"
	reader := bufio.NewReaderSize(strings.NewReader(input), 31)

	line, count, oversized, err := readBoundedLine(reader, maxRunnerLogLine)
	if !oversized || line != nil || count != int64(len(long)) || err != nil {
		t.Fatalf("oversized read = (%d bytes, %d, %v, %v)", len(line), count, oversized, err)
	}
	line, count, oversized, err = readBoundedLine(reader, maxRunnerLogLine)
	if oversized || string(line) != `{"status":"failed","stage":"connect"}` || count != int64(len(line)) || err != nil {
		t.Fatalf("recovered line = (%q, %d, %v, %v)", line, count, oversized, err)
	}
	line, count, oversized, err = readBoundedLine(reader, maxRunnerLogLine)
	if oversized || string(line) != "final" || count != 5 || !errors.Is(err, io.EOF) {
		t.Fatalf("unterminated EOF line = (%q, %d, %v, %v)", line, count, oversized, err)
	}
	if _, _, _, err := readBoundedLine(reader, maxRunnerLogLine); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal read = %v, want EOF", err)
	}
}

func TestStreamLogReaderRecoversAfterOversizedLine(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	long := strings.Repeat("x", maxRunnerLogLine+1)
	process.streamLogReader(strings.NewReader(long+"\n{\"status\":\"failed\",\"stage\":\"connect\"}\n"), "stderr", zapcore.WarnLevel)

	if process.failureStage != FailureStageBrokerConnect {
		t.Fatalf("failure stage = %q, want broker connect", process.failureStage)
	}
	foundProtocol := false
	foundOversized := false
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if entry.Message == "runner protocol event" && fields["stage"] == "connect" {
			foundProtocol = true
		}
		if entry.Message == "runner output line exceeded limit" && fields["line_bytes"] == int64(len(long)) {
			foundOversized = true
		}
		if strings.Contains(zapFieldsString(entry.Context), long) {
			t.Fatal("oversized raw line was logged")
		}
	}
	if !foundProtocol || !foundOversized {
		t.Fatalf("reader did not report oversize and recover: %+v", observed.All())
	}
}

func TestRunnerOutputRateLimitReportsDrops(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	for i := 0; i < 1000; i++ {
		process.emitRunnerEvent("stdout", zapcore.DebugLevel, "runner output ignored", zap.String("source", "stdout"))
	}
	process.reportRunnerIssues("stdout")

	emitted := 0
	dropped := uint64(0)
	for _, entry := range observed.All() {
		if entry.Message == "runner output ignored" {
			emitted++
		}
		if entry.Message == "runner output records dropped" {
			dropped = entry.ContextMap()["rate_limited"].(uint64)
		}
	}
	if emitted > runnerLogRate || emitted == 0 {
		t.Fatalf("rate-limited events = %d, want 1..%d", emitted, runnerLogRate)
	}
	if dropped == 0 {
		t.Fatal("rate-limited records were not reported")
	}
}

type failingLogReader struct{}

func (failingLogReader) Read([]byte) (int, error) { return 0, errors.New("secret reader error") }

func TestStreamLogReaderRedactsTerminalReaderError(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	process.streamLogReader(failingLogReader{}, "stderr", zapcore.WarnLevel)
	entries := observed.All()
	if len(entries) != 2 {
		t.Fatalf("reader error should be logged with an observable counter: %+v", entries)
	}
	if entries[0].Message != "runner log reader stopped" || entries[0].ContextMap()["reason"] != "read_error" {
		t.Fatalf("reader error log = %+v", entries[0])
	}
	if strings.Contains(zapFieldsString(entries[0].Context), "secret reader error") {
		t.Fatalf("reader error leaked details: %+v", entries[0])
	}
	if entries[1].Message != "runner output records dropped" || entries[1].ContextMap()["read_errors"] != uint64(1) {
		t.Fatalf("reader error counter = %+v", entries[1])
	}
}

func hasField(fields []zapcore.Field, key string) bool {
	for _, field := range fields {
		if field.Key == key {
			return true
		}
	}
	return false
}

func fieldValue(fields []zapcore.Field, key string) zapcore.Field {
	for _, field := range fields {
		if field.Key == key {
			return field
		}
	}
	return zapcore.Field{}
}

func zapFieldsString(fields []zapcore.Field) string {
	return fmt.Sprintf("%#v", fields)
}
