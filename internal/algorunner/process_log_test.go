package algorunner

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestClassifyRunnerLinesRedactsUntrustedText is table-driven because every case
// asserts the same contract: no runner-authored text reaches the host log, and
// only allowlisted tokens survive.
func TestClassifyRunnerLinesRedactsUntrustedText(t *testing.T) {
	const secret = "rtsp://user:password@camera/private /tmp/secret.sock"

	cases := []struct {
		name        string
		line        string
		fallback    zapcore.Level
		wantMessage string
		wantLevel   zapcore.Level
		wantStage   string
		wantInvalid bool
		wantFields  map[string]int64
	}{
		{
			name:        "protocol event keeps only allowlisted tokens",
			line:        `{"status":"failed","stage":"connect","rc":5,"error":"` + secret + `"}`,
			fallback:    zapcore.WarnLevel,
			wantMessage: "runner protocol event",
			wantLevel:   zapcore.WarnLevel,
			wantStage:   "connect",
			wantFields:  map[string]int64{"error_present": 1, "rc": 5},
		},
		{
			name: "native log reduces the message to its byte count",
			line: `{"zhulongLog":true,"level":"error","module":"pipeline.stream",` +
				`"streamId":17,"message":"` + secret + `"}`,
			fallback:    zapcore.WarnLevel,
			wantMessage: "runner native log",
			wantLevel:   zapcore.ErrorLevel,
			wantFields:  map[string]int64{"message_bytes": int64(len(secret)), "stream_id": 17},
		},
		{
			name:        "unparsed output is ignored",
			line:        secret,
			fallback:    zapcore.DebugLevel,
			wantMessage: "runner output ignored",
			wantLevel:   zapcore.DebugLevel,
		},
		{
			name:        "unknown protocol token is rejected",
			line:        `{"status":"failed","stage":"` + secret + `"}`,
			fallback:    zapcore.WarnLevel,
			wantMessage: "runner protocol record rejected",
			wantLevel:   zapcore.WarnLevel,
			wantInvalid: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			parsed := classifyRunnerLine([]byte(tc.line), tc.fallback)
			if parsed.message != tc.wantMessage {
				t.Fatalf("message = %q, want %q", parsed.message, tc.wantMessage)
			}
			if parsed.level != tc.wantLevel {
				t.Fatalf("level = %v, want %v", parsed.level, tc.wantLevel)
			}
			if parsed.stage != tc.wantStage {
				t.Fatalf("stage = %q, want %q", parsed.stage, tc.wantStage)
			}
			if parsed.invalid != tc.wantInvalid {
				t.Fatalf("invalid = %v, want %v", parsed.invalid, tc.wantInvalid)
			}
			rendered := zapFieldsString(parsed.fields)
			if strings.Contains(rendered, secret) {
				t.Fatalf("fields leaked runner-authored text: %s", rendered)
			}
			for key, want := range tc.wantFields {
				if got := fieldValue(parsed.fields, key).Integer; got != want {
					t.Fatalf("field %q = %d, want %d", key, got, want)
				}
			}
		})
	}
}

// TestStreamLogReaderAcceptsBrokerDisconnectEvent pins the cross-layer contract
// with native/sdk/src/algo_main.cpp: when a package process loses its broker
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

	if invalid := process.logStats[runnerSourceIndex("stderr")].counts[runnerIssueInvalid]; invalid != 0 {
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
		parsed := classifyRunnerLine([]byte(line), zapcore.WarnLevel)
		if !parsed.invalid || parsed.message != "runner protocol record rejected" {
			t.Fatalf("line %s was not rejected: %+v", line, parsed)
		}
	}
}

func TestReadBoundedLineDrainsAndRecovers(t *testing.T) {
	long := strings.Repeat("x", maxRunnerLogLine+257)
	input := long + "\n{\"status\":\"failed\",\"stage\":\"connect\"}\nfinal"
	reader := bufio.NewReaderSize(strings.NewReader(input), 31)

	line, err := readBoundedLine(reader, maxRunnerLogLine)
	oversized := line.oversized && line.data == nil && err == nil
	if !oversized || line.bytes != int64(len(long)) {
		t.Fatalf("oversized read = (%d bytes, %d, %v, %v)", len(line.data), line.bytes, line.oversized, err)
	}

	line, err = readBoundedLine(reader, maxRunnerLogLine)
	wantRecovered := `{"status":"failed","stage":"connect"}`
	if line.oversized || string(line.data) != wantRecovered || err != nil {
		t.Fatalf("recovered line = (%q, %d, %v, %v)", line.data, line.bytes, line.oversized, err)
	}
	if line.bytes != int64(len(wantRecovered)) {
		t.Fatalf("recovered line byte count = %d, want %d", line.bytes, len(wantRecovered))
	}
	line, err = readBoundedLine(reader, maxRunnerLogLine)
	if line.oversized || string(line.data) != "final" || line.bytes != 5 || !errors.Is(err, io.EOF) {
		t.Fatalf("unterminated EOF line = (%q, %d, %v, %v)", line.data, line.bytes, line.oversized, err)
	}
	if _, err := readBoundedLine(reader, maxRunnerLogLine); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal read = %v, want EOF", err)
	}
}

func TestStreamLogReaderRecoversAfterOversizedLine(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	long := strings.Repeat("x", maxRunnerLogLine+1)
	process.streamLogReader(
		strings.NewReader(long+"\n{\"status\":\"failed\",\"stage\":\"connect\"}\n"),
		"stderr",
		zapcore.WarnLevel,
	)

	if process.failureStage != algoipc.FailureStageBrokerConnect {
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
		process.emitRunnerEvent("stdout", zapcore.DebugLevel, "runner output ignored")
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

// TestRunnerProcessTagsStderrLogsAndCounters drives the real stderr pipe of a
// package process: the "stderr" tag is a literal on the Start wiring, and a
// regression there would attribute package noise to stdout (or nowhere) while
// every direct streamLogReader test kept passing.
func TestRunnerProcessTagsStderrLogsAndCounters(t *testing.T) {
	const noise = "package text that is not a protocol record"
	oversized := strings.Repeat("x", maxRunnerLogLine+1)

	// The oversized line lives in its own directory: the stub reads it through
	// $OVERSIZED_LINE_FILE, so the two do not have to share a path.
	oversizedFile := filepath.Join(t.TempDir(), "oversized-line")
	if err := os.WriteFile(oversizedFile, []byte(oversized+"\n"), 0o644); err != nil {
		t.Fatalf("write oversized line: %v", err)
	}
	body := "#!/bin/sh\nprintf '" + noise + "\\n' >&2\ncat \"$OVERSIZED_LINE_FILE\" >&2\nexit 0\n"
	script := writeExecutableScript(t, "stderr_pkg", body)
	t.Setenv("OVERSIZED_LINE_FILE", oversizedFile)

	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{
		AlgorithmID:    "stderr_pkg",
		ExecutablePath: script,
		Logger:         zap.New(core),
	})
	if err := process.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("stub process did not exit within timeout")
	}

	foundNoise := false
	foundOversized := false
	for _, entry := range observed.All() {
		fields := entry.ContextMap()
		if fields["source"] != "stderr" {
			continue
		}
		switch entry.Message {
		case "runner output ignored":
			if fields["line_bytes"] != int64(len(noise)) {
				t.Fatalf("stderr record byte count = %v, want %d", fields["line_bytes"], len(noise))
			}
			foundNoise = true
		case "runner output line exceeded limit":
			if fields["line_bytes"] != int64(len(oversized)) {
				t.Fatalf("oversized record byte count = %v, want %d", fields["line_bytes"], len(oversized))
			}
			foundOversized = true
		}
	}
	if !foundNoise {
		t.Fatalf("no stderr-tagged record for package stderr output; observed: %+v", observed.All())
	}
	if !foundOversized {
		t.Fatalf("no stderr-tagged record for the oversized package line; observed: %+v", observed.All())
	}

	stderrSlot := runnerSourceIndex("stderr")
	stdoutSlot := runnerSourceIndex("stdout")
	process.logRateMu.Lock()
	stderrOversized := process.logStats[stderrSlot].counts[runnerIssueOversized]
	stdoutOversized := process.logStats[stdoutSlot].counts[runnerIssueOversized]
	process.logRateMu.Unlock()
	if stderrOversized != 1 {
		t.Fatalf("stderr oversized counter = %d, want 1", stderrOversized)
	}
	if stdoutOversized != 0 {
		t.Fatalf("stderr output was counted against the stdout slot: %d", stdoutOversized)
	}
}

// TestRunnerOutputRateLimitUsesTheStderrSlot pins the second half of the stderr
// accounting: a rate-limited stderr record must land in the stderr slot, through
// the same counting entry point as every other drop category.
func TestRunnerOutputRateLimitUsesTheStderrSlot(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})
	for i := 0; i < runnerLogRate+10; i++ {
		process.emitRunnerEvent("stderr", zapcore.DebugLevel, "runner output ignored")
	}
	process.reportRunnerIssues("stderr")

	stderrSlot := runnerSourceIndex("stderr")
	stdoutSlot := runnerSourceIndex("stdout")
	process.logRateMu.Lock()
	stderrLimited := process.logStats[stderrSlot].counts[runnerIssueRateLimited]
	stdoutLimited := process.logStats[stdoutSlot].counts[runnerIssueRateLimited]
	process.logRateMu.Unlock()
	if stderrLimited != 10 {
		t.Fatalf("stderr rate-limited counter = %d, want 10", stderrLimited)
	}
	if stdoutLimited != 0 {
		t.Fatalf("stderr rate limiting was counted against the stdout slot: %d", stdoutLimited)
	}
	if len(observed.All()) == 0 {
		t.Fatal("the drop report was not emitted")
	}
}

// TestRunnerIssueDropReportNamesOversizedAndInvalidSeparately pins the two field
// names that are easiest to swap in the drop report.
//
// The oversized drop and the invalid protocol record are triggered separately, so
// each produces its own report: trading the "oversized" and "invalid" rows of
// runnerLogCategoryFields would put the 1 under the wrong name in both reports,
// which these assertions reject.
func TestRunnerIssueDropReportNamesOversizedAndInvalidSeparately(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	process := NewRunnerProcess(ProcessConfig{Logger: zap.New(core)})

	// One oversized line: counted as oversized and reported under "oversized".
	process.emitLogLine("stderr", zapcore.WarnLevel, runnerLogLine{
		oversized: true,
		bytes:     maxRunnerLogLine + 1,
	})
	// Then one invalid protocol record: counted as invalid, reported under "invalid".
	process.emitLogLine("stderr", zapcore.WarnLevel, runnerLogLine{
		data: []byte(`{"status":"totally_new_status"}`),
	})
	// Flush anything the two emissions above did not already report.
	process.reportRunnerIssues("stderr")

	var reports []map[string]any
	for _, entry := range observed.All() {
		if entry.Message == "runner output records dropped" {
			reports = append(reports, entry.ContextMap())
		}
	}
	if len(reports) != 2 {
		t.Fatalf("expected one drop report per kind, got %+v", reports)
	}
	if reports[0]["oversized"] != uint64(1) || reports[0]["invalid"] != uint64(0) {
		t.Fatalf("an oversized drop was reported with the wrong field names: %+v", reports[0])
	}
	if reports[1]["invalid"] != uint64(1) || reports[1]["oversized"] != uint64(0) {
		t.Fatalf("an invalid record was reported with the wrong field names: %+v", reports[1])
	}
}

// TestRunnerLogCategoryFieldsAreNamed pins the promise runnerLogCategoryFields
// makes: every drop counter has a report field name, and no two counters share
// one. Without it a category could be added with an empty name (an unnamed
// counter in the report) or collision (two counters folded into one field).
func TestRunnerLogCategoryFieldsAreNamed(t *testing.T) {
	seen := make(map[string]runnerIssueKind, runnerLogCategoryCount)
	for kind, name := range runnerLogCategoryFields {
		if name == "" {
			t.Fatalf("category %d has no report field name", kind)
		}
		if other, exists := seen[name]; exists {
			t.Fatalf("categories %d and %d share the report field name %q", other, kind, name)
		}
		seen[name] = runnerIssueKind(kind)
	}
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
