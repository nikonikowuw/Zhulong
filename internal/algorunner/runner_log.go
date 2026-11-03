package algorunner

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Runner output handling: the host never forwards a runner line verbatim. Every
// line is classified, truncated and rate limited first, because the text comes
// from a third-party package and may carry credentials or paths.

const (
	// maxRunnerLogLine caps how much of one runner line is retained.
	maxRunnerLogLine = 64 * 1024
	// runnerLogRate caps how many records per source are emitted per second.
	runnerLogRate = 100
)

// runnerLogSourceCount is the number of streams whose logs are rate limited
// separately (stdout and stderr). It sizes the per-source arrays below.
const runnerLogSourceCount = 2

// runnerLogWindow is the current one-second emission window of one source.
type runnerLogWindow struct {
	started time.Time
	emitted int
}

// runnerLogStats counts records a source produced but that could not be emitted.
//
// The same type holds both the running totals and the last reported snapshot, so
// reportRunnerIssues' diff cannot silently miss a counter added later.
type runnerLogStats struct {
	rateLimited uint64
	invalid     uint64
	oversized   uint64
	readErrors  uint64
}

// minus returns the counters that changed since prev.
func (s runnerLogStats) minus(prev runnerLogStats) runnerLogStats {
	return runnerLogStats{
		rateLimited: s.rateLimited - prev.rateLimited,
		invalid:     s.invalid - prev.invalid,
		oversized:   s.oversized - prev.oversized,
		readErrors:  s.readErrors - prev.readErrors,
	}
}

// total returns the number of dropped records across every category.
func (s runnerLogStats) total() uint64 {
	return s.rateLimited + s.invalid + s.oversized + s.readErrors
}

// runnerIssueKind is a drop-reason counter selector. It is a type rather than a
// bare string so a typo cannot silently count nothing.
type runnerIssueKind int

const (
	runnerIssueInvalid runnerIssueKind = iota
	runnerIssueOversized
	runnerIssueReadError
)

// runnerLogLine is one line read from a runner stream.
type runnerLogLine struct {
	// data is the retained line without its trailing newline. It is nil when the
	// line was oversized: the whole line is dropped, not truncated, so a partially
	// parsed JSON record can never be misread as a full one.
	data []byte
	// bytes is the full length on the wire, which may exceed len(data).
	bytes int64
	// oversized reports that bytes exceeded the retention limit.
	oversized bool
}

// streamLogs pumps one of the runner's output streams into the host log.
func (p *RunnerProcess) streamLogs(r *os.File, level zapcore.Level, isStderr bool) {
	source := "stdout"
	if isStderr {
		source = "stderr"
	}
	p.streamLogReader(r, source, level)
}

func (p *RunnerProcess) streamLogReader(reader io.Reader, source string, fallback zapcore.Level) {
	buf := bufio.NewReaderSize(reader, 4096)
	for {
		line, err := readBoundedLine(buf, maxRunnerLogLine)
		atEOF := errors.Is(err, io.EOF)
		if err != nil && !atEOF {
			// The cause is deliberately not logged: reader errors are produced by
			// the child's own pipe and can embed paths we do not forward.
			p.countRunnerIssue(source, runnerIssueReadError)
			p.logger.Error("runner log reader stopped",
				zap.String("source", source),
				zap.String("reason", "read_error"),
			)
			break
		}
		p.emitLogLine(source, fallback, line)
		if atEOF {
			break
		}
	}
	p.reportRunnerIssues(source)
}

// emitLogLine classifies and emits one retained line, counting the drops.
func (p *RunnerProcess) emitLogLine(source string, fallback zapcore.Level, line runnerLogLine) {
	if line.oversized {
		p.countRunnerIssue(source, runnerIssueOversized)
		p.emitRunnerEvent(source, fallback, "runner output line exceeded limit",
			zap.String("source", source),
			zap.Int64("line_bytes", line.bytes),
		)
		return
	}
	if len(line.data) == 0 {
		return
	}
	parsed := classifyRunnerLine(line.data, source, fallback)
	if parsed.invalid {
		p.countRunnerIssue(source, runnerIssueInvalid)
	}
	// 两条「依赖没了」的信号都要记录：connect 失败带退出码 5，而 mid-run 断连
	// 是干净退出（码 0），只有 stage 标记才能把它与真正的插件崩溃区分开。
	if parsed.stage == algoipc.FailureStageBrokerConnect ||
		parsed.stage == algoipc.FailureStageBrokerLost {
		p.setFailureStage(parsed.stage)
	}
	p.emitRunnerEvent(source, parsed.level, parsed.message, parsed.fields...)
}

type parsedRunnerLine struct {
	message string
	level   zapcore.Level
	fields  []zap.Field
	stage   string
	invalid bool
}

// runnerProtocolField is one allowlisted token of a structured runner record.
type runnerProtocolField struct {
	key string
	// valid reports whether the token value is part of the runner's vocabulary.
	valid func(string) bool
	// recordsStage marks the field whose value is the process failure stage.
	recordsStage bool
}

// runnerProtocolFields is ordered on purpose: the emitted log fields follow this
// slice, so two collections of the same record have the same field order. A map
// would additionally allocate on every line of the runner's hot output path.
var runnerProtocolFields = []runnerProtocolField{
	{key: "status", valid: validRunnerStatus},
	{key: "stage", valid: validRunnerStage, recordsStage: true},
	{key: "step", valid: validRunnerStep},
}

func classifyRunnerLine(line []byte, source string, fallback zapcore.Level) parsedRunnerLine {
	base := []zap.Field{zap.String("source", source), zap.Int("line_bytes", len(line))}
	parsed := parsedRunnerLine{level: fallback}

	var object map[string]json.RawMessage
	if json.Unmarshal(line, &object) != nil || object == nil {
		parsed.message = "runner output ignored"
		parsed.fields = base
		return parsed
	}

	if hasProtocolEvent(object) {
		return classifyProtocolEvent(object, base, parsed)
	}
	if nativeLog, ok := decodeLogFlag(object["zhulongLog"]); ok && nativeLog {
		return classifyNativeLog(object, base, parsed)
	}

	parsed.message = "runner output ignored"
	parsed.fields = base
	return parsed
}

// hasProtocolEvent reports whether the record carries any skeleton protocol token.
func hasProtocolEvent(object map[string]json.RawMessage) bool {
	for _, field := range runnerProtocolFields {
		if _, exists := object[field.key]; exists {
			return true
		}
	}
	return false
}

// classifyProtocolEvent validates the allowlisted tokens of a skeleton record and
// turns them into log fields. The raw values are never forwarded: only tokens
// that match the runner's vocabulary become fields.
func classifyProtocolEvent(
	object map[string]json.RawMessage,
	base []zap.Field,
	parsed parsedRunnerLine,
) parsedRunnerLine {
	parsed.message = "runner protocol event"
	parsed.fields = base
	for _, field := range runnerProtocolFields {
		raw, exists := object[field.key]
		if !exists {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) != nil || !field.valid(value) {
			return rejectedProtocolRecord(base)
		}
		parsed.fields = append(parsed.fields, zap.String(field.key, value))
		if field.recordsStage {
			parsed.stage = value
		}
	}
	if raw, exists := object["rc"]; exists {
		code, err := strconv.ParseInt(string(raw), 10, 32)
		if err != nil {
			return rejectedProtocolRecord(base)
		}
		parsed.fields = append(parsed.fields, zap.Int64("rc", code))
	}
	_, errorExists := object["error"]
	errorPresent := errorExists && string(object["error"]) != "null"
	parsed.fields = append(parsed.fields, zap.Bool("error_present", errorPresent))
	return parsed
}

func rejectedProtocolRecord(base []zap.Field) parsedRunnerLine {
	return parsedRunnerLine{
		message: "runner protocol record rejected",
		fields:  base,
		invalid: true,
	}
}

// classifyNativeLog turns a native skeleton log record into a host log event. The
// message body is reduced to its byte count: it is package-authored text and may
// contain frames' URLs or file paths.
func classifyNativeLog(object map[string]json.RawMessage, base []zap.Field, parsed parsedRunnerLine) parsedRunnerLine {
	module, level, message, fieldsOK := decodeNativeLogFields(object)
	nativeLevel, levelOK := nativeZapLevel(level)
	if !fieldsOK || !levelOK {
		parsed.message = "runner native log rejected"
		parsed.fields = base
		parsed.invalid = true
		return parsed
	}

	parsed.message = "runner native log"
	parsed.level = nativeLevel
	parsed.fields = append(base,
		zap.String("module", module),
		zap.String("level", level),
		zap.Int("message_bytes", len(message)),
	)
	if rawStreamID, exists := object["streamId"]; exists {
		streamID, err := strconv.ParseUint(string(rawStreamID), 10, 64)
		if err != nil {
			parsed.message = "runner native log rejected"
			parsed.fields = base
			parsed.invalid = true
			return parsed
		}
		if streamID != 0 {
			parsed.fields = append(parsed.fields, zap.Uint64("stream_id", streamID))
		}
	}
	return parsed
}

func validRunnerStatus(value string) bool {
	switch value {
	case "failed", "passed", "warning", "info":
		return true
	default:
		return false
	}
}

// validRunnerStage must stay in sync with the stages the package skeleton emits
// (native/sdk/src/algo_main.cpp). A stage the runner emits but this
// allowlist rejects is classified as "runner protocol record rejected", which
// hides the real event: `broker_disconnected` is exactly the signal an operator
// needs to tell "host died" apart from "plugin crashed".
func validRunnerStage(value string) bool {
	switch value {
	// 注意：曾经还有 "dlopen"，随 dlopen 通道下线；包骨架不会再发这个 stage。
	case "args",
		"model_create",
		"worker_create",
		"task_open",
		"process",
		"connect",
		"name_check",
		"shm_init",
		"gallery_sync",
		// 与 algoipc.FailureStageBrokerLost 同值，保留字面量方便对照包骨架源码。
		"broker_disconnected",
		"worker_exception",
		"worker_stalled":
		return true
	default:
		return false
	}
}

func validRunnerStep(value string) bool {
	switch value {
	case "start", "abi_verified":
		return true
	default:
		return false
	}
}

func validNativeModule(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	return strings.IndexFunc(value, func(char rune) bool {
		return !isNativeModuleChar(char)
	}) < 0
}

// isNativeModuleChar reports whether char may appear in a native module name
// (C++ style identifiers plus the project's "_32" style suffixes).
func isNativeModuleChar(char rune) bool {
	switch {
	case char >= 'a' && char <= 'z', char >= 'A' && char <= 'Z', char >= '0' && char <= '9':
		return true
	case char == '_', char == '-', char == '.':
		return true
	}
	return false
}

// decodeLogFlag decodes the optional zhulongLog marker of a native log record.
// A missing or malformed marker is not an error: the line simply falls through
// to the plain-text classification paths.
func decodeLogFlag(raw json.RawMessage) (flag bool, ok bool) {
	if raw == nil || json.Unmarshal(raw, &flag) != nil {
		return false, false
	}
	return flag, true
}

// decodeNativeLogFields decodes the module/level/message triple of a native
// runner log record and validates the module name, which ends up as a log field.
func decodeNativeLogFields(object map[string]json.RawMessage) (module, level, message string, ok bool) {
	if json.Unmarshal(object["module"], &module) != nil || !validNativeModule(module) {
		return "", "", "", false
	}
	if json.Unmarshal(object["level"], &level) != nil || json.Unmarshal(object["message"], &message) != nil {
		return "", "", "", false
	}
	return module, level, message, true
}

func nativeZapLevel(value string) (zapcore.Level, bool) {
	switch value {
	case "debug":
		return zapcore.DebugLevel, true
	case "info":
		return zapcore.InfoLevel, true
	case "warn":
		return zapcore.WarnLevel, true
	case "error":
		return zapcore.ErrorLevel, true
	default:
		return 0, false
	}
}

// readBoundedLine reads one newline-terminated line, draining an oversized one
// instead of buffering it.
func readBoundedLine(reader *bufio.Reader, max int) (runnerLogLine, error) {
	line := runnerLogLine{data: make([]byte, 0, min(max, 4096))}
	for {
		part, err := reader.ReadSlice('\n')
		content := part
		if len(content) > 0 && content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
		}
		if int64(len(content)) > math.MaxInt64-line.bytes {
			line.bytes = math.MaxInt64
		} else {
			line.bytes += int64(len(content))
		}
		if !line.oversized {
			if len(content) > max-len(line.data) {
				line.oversized = true
				line.data = nil
			} else {
				line.data = append(line.data, content...)
			}
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, err
	}
}

// emitRunnerEvent applies the per-source rate limit, flushes any pending drop
// report, and then logs the event.
func (p *RunnerProcess) emitRunnerEvent(source string, level zapcore.Level, message string, fields ...zap.Field) {
	index := runnerSourceIndex(source)

	p.logRateMu.Lock()
	window := &p.logRate[index]
	now := time.Now()
	if window.started.IsZero() || now.Sub(window.started) >= time.Second {
		window.started = now
		window.emitted = 0
	}
	if window.emitted >= runnerLogRate {
		p.logStats[index].rateLimited++
		p.logRateMu.Unlock()
		return
	}
	window.emitted++
	p.logRateMu.Unlock()

	p.reportRunnerIssues(source)
	p.logger.Log(level, message, fields...)
}

// runnerSourceIndex maps a stream name to its slot in the per-source arrays.
func runnerSourceIndex(source string) int {
	if source == "stderr" {
		return 1
	}
	return 0
}

func (p *RunnerProcess) countRunnerIssue(source string, kind runnerIssueKind) {
	p.logRateMu.Lock()
	defer p.logRateMu.Unlock()

	stats := &p.logStats[runnerSourceIndex(source)]
	switch kind {
	case runnerIssueInvalid:
		stats.invalid++
	case runnerIssueOversized:
		stats.oversized++
	case runnerIssueReadError:
		stats.readErrors++
	}
}

// reportRunnerIssues emits the drops that happened since the last report, so a
// silent rate limit cannot hide a runner that is flooding the pipe.
func (p *RunnerProcess) reportRunnerIssues(source string) {
	index := runnerSourceIndex(source)

	p.logRateMu.Lock()
	delta := p.logStats[index].minus(p.reported[index])
	p.reported[index] = p.logStats[index]
	p.logRateMu.Unlock()

	if delta.total() == 0 {
		return
	}
	p.logger.Warn("runner output records dropped",
		zap.String("source", source),
		zap.Uint64("rate_limited", delta.rateLimited),
		zap.Uint64("invalid", delta.invalid),
		zap.Uint64("oversized", delta.oversized),
		zap.Uint64("read_errors", delta.readErrors),
	)
}
