package algorunner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	IpcMagic                    = algoipc.IpcMagic
	IpcVersion                  = algoipc.IpcVersion
	MaxIpcPayloadSize           = algoipc.MaxIpcPayloadSize
	FailureStageBrokerConnect   = algoipc.FailureStageBrokerConnect
	runnerExitBrokerUnavailable = algoipc.RunnerExitBrokerUnavailable
	MsgResult                   = algoipc.MsgResult

	MsgGallerySync   = algoipc.MsgGallerySync
	MsgGalleryUpsert = algoipc.MsgGalleryUpsert
	MsgGalleryDelete = algoipc.MsgGalleryDelete
	MsgGalleryAck    = algoipc.MsgGalleryAck

	maxRunnerLogLine = 64 * 1024
	runnerLogRate    = 100
)

var ErrIpcPayloadTooLarge = algoipc.ErrIpcPayloadTooLarge

type IpcHeader = algoipc.IpcHeader

var (
	EncodeIpcMessage = algoipc.EncodeIpcMessage
	DecodeIpcHeader  = algoipc.DecodeIpcHeader
)

// LoadDotEnv reads key-value environment variable pairs from a .env file.
func LoadDotEnv(filePath string) (map[string]string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]string), nil
		}
		return nil, err
	}
	defer file.Close()

	env := make(map[string]string)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			val = strings.Trim(val, `"'`)
			if key != "" {
				env[key] = val
			}
		}
	}
	return env, scanner.Err()
}

// ProcessConfig defines options for launching a runner subprocess.
type ProcessConfig struct {
	AlgorithmID   string
	AlgorithmName string
	RunnerPath    string
	PluginPath    string
	BrokerSocks   []string
	EnvFile       string
	ExtraEnv      map[string]string
	Logger        *zap.Logger
}

// RunnerProcess supervises an individual zhulong_algo_runner subprocess.
type RunnerProcess struct {
	config       ProcessConfig
	logger       *zap.Logger
	mu           sync.Mutex
	cmd          *exec.Cmd
	state        string
	doneCh       chan struct{}
	exitErr      error
	exitCode     int
	failureStage string
	startTime    time.Time
	logRateMu    sync.Mutex
	logRate      [2]runnerLogWindow
	logStats     [2]runnerLogStats
}

type runnerLogWindow struct {
	started time.Time
	emitted int
}

type runnerLogStats struct {
	rateLimited uint64
	invalid     uint64
	oversized   uint64
	readErrors  uint64
	reported    runnerLogStatsSnapshot
}

type runnerLogStatsSnapshot struct {
	rateLimited uint64
	invalid     uint64
	oversized   uint64
	readErrors  uint64
}

// NewRunnerProcess creates a new RunnerProcess instance.
func NewRunnerProcess(cfg ProcessConfig) *RunnerProcess {
	l := cfg.Logger
	if l == nil {
		l = zap.NewNop()
	}
	return &RunnerProcess{
		config: cfg,
		logger: l,
		state:  StatusDisabled,
	}
}

// Start launches the runner subprocess with environment injection and output logging.
func (p *RunnerProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == StatusRunning || p.state == StatusStarting {
		return errors.New("process is already running")
	}

	p.state = StatusStarting
	p.doneCh = make(chan struct{})
	p.exitErr = nil
	p.exitCode = 0
	p.failureStage = ""

	args := []string{
		"--plugin", p.config.PluginPath,
	}
	if len(p.config.BrokerSocks) > 0 {
		for _, sock := range p.config.BrokerSocks {
			if sock != "" {
				args = append(args, "--broker", sock)
			}
		}
	}
	if p.config.AlgorithmName != "" {
		args = append(args, "--name", p.config.AlgorithmName)
	}
	if p.config.AlgorithmID != "" {
		args = append(args, "--algorithm-id", p.config.AlgorithmID)
	}

	cmd := exec.Command(p.config.RunnerPath, args...)

	// Run the runner in its own process group. Stop signals the whole group, so
	// grandchildren the runner spawns cannot leak while holding the broker socket
	// or shared memory after the runner itself has exited. On Linux this also
	// arms Pdeathsig, the only defence that survives host death without Go code
	// running (SIGKILL / OOM / native crash). See runnerSysProcAttr.
	cmd.SysProcAttr = runnerSysProcAttr()

	// Load and inject .env variables. A missing file is optional (LoadDotEnv
	// turns os.IsNotExist into an empty map + nil error), so any error here is a
	// real parse failure (e.g. a line longer than bufio.Scanner's 64KB token
	// limit). Do not swallow it: log a Warn and continue with the variables that
	// were parsed, so a broken .env is observable without blocking the runner.
	envMap := make(map[string]string)
	if p.config.EnvFile != "" {
		loaded, err := LoadDotEnv(p.config.EnvFile)
		if err != nil {
			p.logger.Warn("failed to load runner dotenv file", zap.String("reason", "parse_error"))
		}
		for k, v := range loaded {
			envMap[k] = v
		}
	}
	for k, v := range p.config.ExtraEnv {
		envMap[k] = v
	}

	cmd.Env = os.Environ()
	for k, v := range envMap {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// Create pipes manually so cmd.Wait() does not close them prematurely while scanning.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("create stderr pipe: %w", err)
	}

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	// ⚠️ Pdeathsig is delivered when the *OS thread* that forked the child exits,
	// not when the process exits (go.dev/issue/27505). runtime.LockOSThread pins
	// the calling goroutine, and only that goroutine may unlock it, so the spawn
	// and the reap must happen on the SAME goroutine — a separate watcher calling
	// UnlockOSThread is a no-op on its own (unlocked) goroutine and leaves the
	// caller's pin behind. Pinning the caller is also wrong because Start's caller
	// (an HTTP handler, an Fx hook, a worker) is free to return at any time; when
	// that goroutine exits the runtime retires the M and the kernel SIGKILLs a
	// perfectly healthy runner.
	//
	// This goroutine therefore owns the thread for the whole child lifetime: pin,
	// spawn, report, wait for the caller to finish wiring, then reap and unlock.
	// The unlock happens only after cmd.Wait has returned, so a live runner is
	// never exposed to M reclamation.
	var logWg sync.WaitGroup
	started := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		if err := cmd.Start(); err != nil {
			runtime.UnlockOSThread()
			started <- err
			return
		}
		started <- nil
		// Wait for Start to publish p.cmd/state and start the log readers before
		// reaping: an immediately-exiting child would otherwise reach the state
		// bookkeeping in watchExit while Start is still mutating the struct.
		<-ready
		p.watchExit(cmd, stdoutR, stderrR, &logWg)
	}()

	if startErr := <-started; startErr != nil {
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		p.state = StatusDisabled
		close(p.doneCh)
		return fmt.Errorf("start runner process: %w", startErr)
	}

	// Close parent's write ends immediately so EOF propagates when child terminates
	stdoutW.Close()
	stderrW.Close()

	p.cmd = cmd
	p.startTime = time.Now()
	p.state = StatusRunning

	logWg.Add(2)
	go func() {
		defer logWg.Done()
		p.streamLogs(stdoutR, zap.DebugLevel, false)
		stdoutR.Close()
	}()
	go func() {
		defer logWg.Done()
		p.streamLogs(stderrR, zap.WarnLevel, true)
		stderrR.Close()
	}()

	// Everything the watcher needs is now initialised; let it proceed to Wait.
	close(ready)
	return nil
}

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
		line, lineBytes, oversized, err := readBoundedLine(buf, maxRunnerLogLine)
		if err != nil && !errors.Is(err, io.EOF) {
			p.countRunnerIssue(source, "read_error")
			p.logger.Error("runner log reader stopped", zap.String("source", source), zap.String("reason", "read_error"))
			break
		}
		if oversized {
			p.countRunnerIssue(source, "oversized")
			p.emitRunnerEvent(source, fallback, "runner output line exceeded limit",
				zap.String("source", source), zap.Int64("line_bytes", lineBytes))
		} else if len(line) != 0 {
			parsed := classifyRunnerLine(line, source, fallback)
			if parsed.invalid {
				p.countRunnerIssue(source, "invalid")
			}
			if parsed.stage == FailureStageBrokerConnect {
				p.mu.Lock()
				p.failureStage = FailureStageBrokerConnect
				p.mu.Unlock()
			}
			p.emitRunnerEvent(source, parsed.level, parsed.message, parsed.fields...)
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	p.reportRunnerIssues(source)
}

type parsedRunnerLine struct {
	message string
	level   zapcore.Level
	fields  []zap.Field
	stage   string
	invalid bool
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

	_, hasStatus := object["status"]
	_, hasStage := object["stage"]
	_, hasStep := object["step"]
	if hasStatus || hasStage || hasStep {
		parsed.message = "runner protocol event"
		parsed.fields = base
		for key, allowed := range map[string]func(string) bool{
			"status": validRunnerStatus,
			"stage":  validRunnerStage,
			"step":   validRunnerStep,
		} {
			raw, exists := object[key]
			if !exists {
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) != nil || !allowed(value) {
				parsed.invalid = true
				parsed.message = "runner protocol record rejected"
				parsed.fields = base
				return parsed
			}
			parsed.fields = append(parsed.fields, zap.String(key, value))
			if key == "stage" {
				parsed.stage = value
			}
		}
		if raw, exists := object["rc"]; exists {
			code, err := strconv.ParseInt(string(raw), 10, 32)
			if err != nil {
				parsed.invalid = true
				parsed.message = "runner protocol record rejected"
				parsed.fields = base
				return parsed
			}
			parsed.fields = append(parsed.fields, zap.Int64("rc", code))
		}
		_, errorExists := object["error"]
		errorPresent := errorExists && string(object["error"]) != "null"
		parsed.fields = append(parsed.fields, zap.Bool("error_present", errorPresent))
		return parsed
	}

	var nativeLog bool
	if raw, exists := object["zhulongLog"]; exists && json.Unmarshal(raw, &nativeLog) == nil && nativeLog {
		var module, level, message string
		if json.Unmarshal(object["module"], &module) != nil || !validNativeModule(module) ||
			json.Unmarshal(object["level"], &level) != nil ||
			json.Unmarshal(object["message"], &message) != nil {
			parsed.invalid = true
			parsed.message = "runner native log rejected"
			parsed.fields = base
			return parsed
		}
		nativeLevel, ok := nativeZapLevel(level)
		if !ok {
			parsed.invalid = true
			parsed.message = "runner native log rejected"
			parsed.fields = base
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
				parsed.invalid = true
				parsed.message = "runner native log rejected"
				parsed.fields = base
				return parsed
			}
			if streamID != 0 {
				parsed.fields = append(parsed.fields, zap.Uint64("stream_id", streamID))
			}
		}
		return parsed
	}

	parsed.message = "runner output ignored"
	parsed.fields = base
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

// validRunnerStage must stay in sync with the stages zhulong_algo_runner emits
// (native/src/algorithm/runner/runner.cpp). A stage the runner emits but this
// allowlist rejects is classified as "runner protocol record rejected", which
// hides the real event: `broker_disconnected` is exactly the signal an operator
// needs to tell "host died" apart from "plugin crashed".
func validRunnerStage(value string) bool {
	switch value {
	case "args", "dlopen", "model_create", "worker_create", "task_open", "process", "connect", "name_check", "shm_init", "gallery_sync", "broker_disconnected":
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
	for _, char := range []byte(value) {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			continue
		}
		return false
	}
	return true
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

func readBoundedLine(reader *bufio.Reader, max int) ([]byte, int64, bool, error) {
	line := make([]byte, 0, min(max, 4096))
	var total int64
	oversized := false
	const maxInt64 = int64(1<<63 - 1)
	for {
		part, err := reader.ReadSlice('\n')
		content := part
		if len(content) > 0 && content[len(content)-1] == '\n' {
			content = content[:len(content)-1]
		}
		if int64(len(content)) > maxInt64-total {
			total = maxInt64
		} else {
			total += int64(len(content))
		}
		if !oversized {
			if len(content) > max-len(line) {
				oversized = true
				line = nil
			} else {
				line = append(line, content...)
			}
		}
		if err == nil {
			return line, total, oversized, nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line, total, oversized, err
	}
}

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

func runnerSourceIndex(source string) int {
	if source == "stderr" {
		return 1
	}
	return 0
}

func (p *RunnerProcess) countRunnerIssue(source, kind string) {
	p.logRateMu.Lock()
	stats := &p.logStats[runnerSourceIndex(source)]
	switch kind {
	case "invalid":
		stats.invalid++
	case "oversized":
		stats.oversized++
	case "read_error":
		stats.readErrors++
	}
	p.logRateMu.Unlock()
}

func (p *RunnerProcess) reportRunnerIssues(source string) {
	p.logRateMu.Lock()
	stats := &p.logStats[runnerSourceIndex(source)]
	rateLimited := stats.rateLimited - stats.reported.rateLimited
	invalid := stats.invalid - stats.reported.invalid
	oversized := stats.oversized - stats.reported.oversized
	readErrors := stats.readErrors - stats.reported.readErrors
	stats.reported = runnerLogStatsSnapshot{
		rateLimited: stats.rateLimited,
		invalid:     stats.invalid,
		oversized:   stats.oversized,
		readErrors:  stats.readErrors,
	}
	p.logRateMu.Unlock()
	if rateLimited+invalid+oversized+readErrors == 0 {
		return
	}
	p.logger.Warn("runner output records dropped",
		zap.String("source", source),
		zap.Uint64("rate_limited", rateLimited),
		zap.Uint64("invalid", invalid),
		zap.Uint64("oversized", oversized),
		zap.Uint64("read_errors", readErrors),
	)
}

func (p *RunnerProcess) watchExit(cmd *exec.Cmd, stdoutR, stderrR *os.File, logWg *sync.WaitGroup) {
	// Never release the spawn thread before the child is reaped, or Pdeathsig
	// would fire on a healthy runner. This function runs on the goroutine that
	// called runtime.LockOSThread and cmd.Start (see Start); it is the only
	// unlock site, and only a goroutine that actually holds the lock may unlock it.
	defer runtime.UnlockOSThread()

	err := cmd.Wait()

	// Wait for pipe readers to consume all remaining buffered logs.
	// If an orphaned child process keeps the write pipe open, unblock after 100ms.
	logDone := make(chan struct{})
	go func() {
		logWg.Wait()
		close(logDone)
	}()

	select {
	case <-logDone:
	case <-time.After(100 * time.Millisecond):
		stdoutR.Close()
		stderrR.Close()
		<-logDone
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	p.exitErr = err
	if cmd.ProcessState != nil {
		p.exitCode = cmd.ProcessState.ExitCode()
	} else if err != nil {
		p.exitCode = -1
	}

	// Secondary failure-stage channel: exit code 5 means the runner could not
	// connect to the broker. Robustness note: the stderr marker above is the
	// primary signal because exit codes are more fragile to native changes.
	if p.exitCode == runnerExitBrokerUnavailable {
		p.failureStage = FailureStageBrokerConnect
	}

	if p.state == StatusRunning {
		p.state = StatusCrashed

		if p.failureStage == FailureStageBrokerConnect {
			// The runner could not connect to every broker socket it was given.
			// This is a distinguishable signal, NOT an ordinary plugin crash. We do
			// not fake success or change watchdog restart/circuit-breaker behaviour.
			p.logger.Error("runner exited because it could not connect to its frame broker sockets",
				zap.Int("exit_code", p.exitCode),
				zap.String("reason", "broker_unavailable"),
				zap.String("source", "process_exit"),
			)
		} else {
			p.logger.Warn("runner process exited unexpectedly",
				zap.Int("exit_code", p.exitCode),
				zap.Bool("error_present", err != nil),
				zap.String("reason", "unexpected_exit"),
				zap.String("source", "process_exit"),
			)
		}
	}

	close(p.doneCh)
}

// signalRunner sends sig to the runner's process group. The runner is started
// with Setpgid (see Start), so its pgid equals its pid and the negative-pid
// form reaches every descendant. If the process group is unavailable (for
// example the process is already gone, reported as ESRCH), the call is treated
// as a no-op; any other error falls back to signalling the process directly.
//
// cmd is passed in rather than read from p.cmd so callers keep using the same
// lock-captured handle they already validated, instead of re-reading a field
// that Start owns under p.mu.
func (p *RunnerProcess) signalRunner(cmd *exec.Cmd, sig syscall.Signal) error {
	pid := cmd.Process.Pid
	if err := syscall.Kill(-pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return cmd.Process.Signal(sig)
	}
	return nil
}

// Stop gracefully shuts down the process: SIGTERM -> wait up to timeout -> SIGKILL.
func (p *RunnerProcess) Stop(timeout time.Duration) error {
	p.mu.Lock()
	cmd := p.cmd
	state := p.state
	doneCh := p.doneCh
	p.mu.Unlock()

	if cmd == nil || cmd.Process == nil || state == StatusDisabled {
		return nil
	}

	// Mark target state as disabled to prevent treating clean stop as crash
	p.mu.Lock()
	p.state = StatusDisabled
	p.mu.Unlock()

	// 1. Send SIGTERM to the whole process group.
	if err := p.signalRunner(cmd, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		p.logger.Debug("signal SIGTERM to runner process", zap.Error(err))
	}

	termTimer := time.NewTimer(timeout)
	defer termTimer.Stop()

	select {
	case <-doneCh:
		// Clean exit
		return nil
	case <-termTimer.C:
		// 2. Timeout: Force kill the whole process group with SIGKILL.
		p.logger.Warn("runner process did not exit within timeout, killing with SIGKILL",
			zap.Int("pid", cmd.Process.Pid),
		)
		if err := p.signalRunner(cmd, syscall.SIGKILL); err != nil && !errors.Is(err, os.ErrProcessDone) {
			p.logger.Warn("kill runner process with SIGKILL", zap.Error(err))
		}
		killTimer := time.NewTimer(timeout)
		defer killTimer.Stop()
		select {
		case <-doneCh:
			return nil
		case <-killTimer.C:
			p.logger.Error("runner process did not exit after SIGKILL timeout", zap.Int("pid", cmd.Process.Pid))
			return errors.New("runner process failed to terminate after SIGKILL")
		}
	}
}

// Done returns a channel that is closed when the runner process terminates.
func (p *RunnerProcess) Done() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.doneCh
}

// State returns the current status of the process.
func (p *RunnerProcess) State() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// PID returns the OS process ID, or 0 if not running.
func (p *RunnerProcess) PID() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		return p.cmd.Process.Pid
	}
	return 0
}

// RunningDuration returns how long the current process has been alive.
func (p *RunnerProcess) RunningDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state == StatusRunning {
		return time.Since(p.startTime)
	}
	return 0
}

// GallerySyncPayload represents the legacy data payload for MSG_GALLERY_SYNC.
type GallerySyncPayload struct {
	Items []GallerySyncItem `json:"items"`
}

// GallerySyncShardPayload represents a sharded MSG_GALLERY_SYNC payload safely bounded within 64KB.
type GallerySyncShardPayload struct {
	SyncID     string            `json:"sync_id"`
	ShardIndex int               `json:"shard_index"`
	ShardTotal int               `json:"shard_total"`
	Items      []GallerySyncItem `json:"items"`
}

// GalleryAckPayload represents MSG_GALLERY_ACK.
type GalleryAckPayload struct {
	SyncID     string `json:"sync_id"`
	Success    bool   `json:"success"`
	TotalFaces int    `json:"total_faces"`
	ErrorMsg   string `json:"error_msg,omitempty"`
}

// GalleryUpsertPayload represents MSG_GALLERY_UPSERT.
type GalleryUpsertPayload struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GalleryDeletePayload represents MSG_GALLERY_DELETE.
type GalleryDeletePayload struct {
	FaceID   string `json:"face_id,omitempty"`
	PersonID string `json:"person_id,omitempty"`
}

// shardBudget caps a single gallery sync shard at 90% of the protocol limit so
// the shard header and marshalling drift cannot push a payload past
// MaxIpcPayloadSize. Measured sizes: a 512-dim L2-normalized float32 embedding
// serializes to ~12.3 bytes per component (~6.3KB per item), so the old fixed
// 10-items-per-shard packing left under 1% headroom on real data (10 items
// measured at 64204~65373 bytes against the 65536 limit).
const shardBudget = MaxIpcPayloadSize * 9 / 10

// CreateGallerySyncShards fragments gallery items into a sequence of JSON
// payloads, each strictly within shardBudget (and therefore well under the 64KB
// IPC limit).
//
// Items are packed by measured byte size, not by a fixed count: a shard is
// closed as soon as adding the next item would exceed the budget. An empty
// input still yields exactly one shard with no items (preserved for existing
// callers). If a single item cannot fit on its own, an error naming that item's
// face_id is returned rather than silently dropping it.
func CreateGallerySyncShards(syncID string, items []GallerySyncItem) ([][]byte, error) {
	if len(items) == 0 {
		payload, err := json.Marshal(GallerySyncShardPayload{
			SyncID:     syncID,
			ShardIndex: 0,
			ShardTotal: 1,
			Items:      []GallerySyncItem{},
		})
		if err != nil {
			return nil, fmt.Errorf("marshal empty gallery sync shard: %w", err)
		}
		return [][]byte{payload}, nil
	}

	// Marshal each item exactly once. The JSON encoding of an item is identical
	// whether it is serialized alone or inside a shard's items array, so these
	// measured sizes can be summed arithmetically instead of re-marshalling the
	// whole shard on every packing step (which would be O(n^2) on 10k faces).
	itemSizes := make([]int, len(items))
	for i, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, fmt.Errorf("marshal gallery sync item %s: %w", item.FaceID, err)
		}
		itemSizes[i] = len(encoded)
	}

	// Measure the per-shard overhead with the widest header we can produce
	// (6-digit shard numbers for both index and total) so the budget stays valid
	// for any shard count.
	overheadPayload, err := json.Marshal(GallerySyncShardPayload{
		SyncID:     syncID,
		ShardIndex: 999999,
		ShardTotal: 999999,
		Items:      []GallerySyncItem{},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync shard header: %w", err)
	}
	overhead := len(overheadPayload)

	// Greedy packing: accumulate items while the projected shard size stays
	// within budget. Each additional item costs its own bytes plus one comma.
	type shardRange struct{ start, end int }
	var ranges []shardRange
	start := 0
	used := 0
	for i, size := range itemSizes {
		if overhead+size > shardBudget {
			return nil, fmt.Errorf("gallery sync item %s exceeds the shard budget (%d bytes > %d)",
				items[i].FaceID, overhead+size, shardBudget)
		}
		additional := size
		if i > start {
			additional++ // comma separating this item from the previous one
		}
		if used+additional > shardBudget-overhead {
			ranges = append(ranges, shardRange{start: start, end: i})
			start = i
			used = size
			continue
		}
		used += additional
	}
	ranges = append(ranges, shardRange{start: start, end: len(items)})

	shardTotal := len(ranges)
	shards := make([][]byte, 0, shardTotal)
	for i, r := range ranges {
		payload, err := json.Marshal(GallerySyncShardPayload{
			SyncID:     syncID,
			ShardIndex: i,
			ShardTotal: shardTotal,
			Items:      items[r.start:r.end],
		})
		if err != nil {
			return nil, fmt.Errorf("marshal gallery sync shard %d/%d: %w", i, shardTotal, err)
		}
		// Defensive gate: the packing math above should make this unreachable, but
		// it is the last line of defence before the 64KB protocol limit.
		if len(payload) > int(MaxIpcPayloadSize) {
			return nil, fmt.Errorf("gallery sync shard %d exceeds 64KB (%d bytes)", i, len(payload))
		}
		shards = append(shards, payload)
	}
	return shards, nil
}

// CreateGallerySyncMsg serializes a full gallery sync message into framed IPC bytes.
func CreateGallerySyncMsg(seq uint32, items []GallerySyncItem) ([]byte, error) {
	payload, err := json.Marshal(GallerySyncPayload{Items: items})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery sync payload: %w", err)
	}
	return EncodeIpcMessage(MsgGallerySync, seq, payload)
}

// CreateGalleryUpsertMsg serializes an incremental face enrollment update into framed IPC bytes.
func CreateGalleryUpsertMsg(seq uint32, item GalleryUpsertPayload) ([]byte, error) {
	payload, err := json.Marshal(item)
	if err != nil {
		return nil, fmt.Errorf("marshal gallery upsert payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryUpsert, seq, payload)
}

// CreateGalleryDeleteMsg serializes a delete face/person update into framed IPC bytes.
func CreateGalleryDeleteMsg(seq uint32, faceID, personID string) ([]byte, error) {
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return nil, fmt.Errorf("marshal gallery delete payload: %w", err)
	}
	return EncodeIpcMessage(MsgGalleryDelete, seq, payload)
}

// GalleryBroadcaster distributes full and incremental gallery updates to running algorithm processes.
type GalleryBroadcaster struct {
	broadcaster AIMessageBroadcaster
	logger      *zap.Logger
}

// NewGalleryBroadcaster creates a GalleryBroadcaster instance.
func NewGalleryBroadcaster(broadcaster AIMessageBroadcaster, logger *zap.Logger) *GalleryBroadcaster {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GalleryBroadcaster{
		broadcaster: broadcaster,
		logger:      logger.Named("algorithm.gallery_broadcaster"),
	}
}

// BroadcastSync performs cold-start or full synchronization of the face gallery to all active runners.
func (b *GalleryBroadcaster) BroadcastSync(ctx context.Context, syncID string, items []GallerySyncItem) error {
	if b.broadcaster == nil {
		return nil
	}
	shards, err := CreateGallerySyncShards(syncID, items)
	if err != nil {
		return fmt.Errorf("create gallery sync shards: %w", err)
	}
	for i, shard := range shards {
		if err := b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGallerySync, shard); err != nil {
			return fmt.Errorf("broadcast sync shard %d/%d: %w", i, len(shards), err)
		}
	}
	return nil
}

// BroadcastUpsert broadcasts an incremental face template enrollment or update.
func (b *GalleryBroadcaster) BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(item)
	if err != nil {
		return fmt.Errorf("marshal gallery upsert: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGalleryUpsert, payload)
}

// BroadcastDelete broadcasts face or person deletion.
func (b *GalleryBroadcaster) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	if b.broadcaster == nil {
		return nil
	}
	payload, err := json.Marshal(GalleryDeletePayload{FaceID: faceID, PersonID: personID})
	if err != nil {
		return fmt.Errorf("marshal gallery delete: %w", err)
	}
	return b.broadcaster.BroadcastAIMessage(ctx, 0, MsgGalleryDelete, payload)
}
