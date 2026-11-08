package algorunner

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// runnerExitBrokerUnavailable is the runner's exit code when it could not
	// reach every frame broker socket.
	runnerExitBrokerUnavailable = algoipc.RunnerExitBrokerUnavailable

	// runnerExitWorkerCrashed is the runner's exit code when one per-stream worker
	// thread failed with an uncaught exception. Secondary channel only: the stderr
	// stage record is the primary signal (see the note in watchExit).
	runnerExitWorkerCrashed = algoipc.RunnerExitWorkerCrashed

	// runnerExitWorkerStalled is the runner's exit code when one per-stream worker
	// stopped making progress and the skeleton's watchdog ended the process.
	runnerExitWorkerStalled = algoipc.RunnerExitWorkerStalled
)

// exitDiagnosis is how a runner failure stage is reported.
//
// It is a table rather than an if/else chain because the same variable (the
// process exit code, and later the failure stage it implied) used to be compared
// three times in a row: a table cannot drift out of sync with the case list, and
// a new stage means a new row.
type exitDiagnosis struct {
	// reason is the stable machine-readable log field.
	reason string
	// message is the human-readable log message.
	message string
	// level is the severity. A distinguishable failure (broker unreachable, a
	// worker thread died) is an Error worth its own investigation; an ordinary
	// non-zero exit is a Warn.
	level zapcore.Level
}

// exitFailureStages maps a runner exit code to the failure stage it implies.
//
// This is the secondary channel: the stderr stage record is primary, because exit
// codes are more fragile to native changes.
var exitFailureStages = map[int]string{
	runnerExitBrokerUnavailable: algoipc.FailureStageBrokerConnect,
	runnerExitWorkerCrashed:     algoipc.FailureStageWorkerException,
	runnerExitWorkerStalled:     algoipc.FailureStageWorkerStalled,
}

// failureStageDiagnoses maps a failure stage to how it is logged. Both channels
// (exit code and stderr marker) converge here, so the same failure never gets two
// different explanations depending on which channel noticed it first.
var failureStageDiagnoses = map[string]exitDiagnosis{
	algoipc.FailureStageBrokerConnect: {
		// The runner could not connect to every broker socket it was given. This is
		// a distinguishable signal, NOT an ordinary plugin crash. We do not fake
		// success or change watchdog restart/circuit-breaker behaviour.
		reason:  "broker_unavailable",
		message: "runner exited because it could not connect to its frame broker sockets",
		level:   zapcore.ErrorLevel,
	},
	algoipc.FailureStageBrokerLost: {
		// broker 在半途断开：相机掉线/重连会把 socket 连同流一起回收。
		// 这不是包缺陷，与 connect 失败归为同一类环境故障（见 isDependencyFailure）。
		reason:  "broker_lost",
		message: "runner lost its frame broker connection",
		level:   zapcore.ErrorLevel,
	},
	algoipc.FailureStageWorkerException: {
		// One per-stream worker threw inside the package skeleton (typically an
		// allocation failure while serializing a result). Same restart/rollback
		// path as any other crash; reported apart so "the algorithm misbehaved"
		// is not buried under "the process exited unexpectedly".
		reason:  "worker_exception",
		message: "runner worker thread failed with an uncaught exception",
		level:   zapcore.ErrorLevel,
	},
	algoipc.FailureStageWorkerStalled: {
		// One per-stream worker hung inside process() and the package watchdog
		// ended the process. Same restart path as any other crash; reported apart
		// because "the algorithm is too slow / deadlocked" is not the same
		// investigation as "the algorithm crashed".
		reason:  "worker_stalled",
		message: "runner worker thread stopped making progress and was terminated by its watchdog",
		level:   zapcore.ErrorLevel,
	},
}

// unexpectedExit is the diagnosis for a non-zero exit code without a dedicated row.
var unexpectedExit = exitDiagnosis{
	reason:  "unexpected_exit",
	message: "runner process exited unexpectedly",
	level:   zapcore.WarnLevel,
}

// ProcessConfig defines options for launching a package process.
type ProcessConfig struct {
	AlgorithmID   string
	AlgorithmName string
	// ExecutablePath is the package's own executable (resolved by ResolveEntry).
	// The host passes no argument that selects an algorithm: the executable *is*
	// the algorithm, so its argv only carries runtime plumbing.
	ExecutablePath string
	BrokerSocks    []string
	// ConfigJSON is the package's **process-level** configuration, passed as
	// `--config <inline json>`. Empty means "do not pass the flag" so the skeleton
	// keeps its own `{}` default. Per-stream task configuration travels over
	// MSG_TASK_CONFIG instead — argv cannot express a per-broker value.
	ConfigJSON        string
	EnvFile           string
	ExtraEnv          map[string]string
	Logger            *zap.Logger
	DisableMotionGate bool
}

// RunnerProcess supervises one algorithm package subprocess.
type RunnerProcess struct {
	config ProcessConfig
	logger *zap.Logger

	// mu guards the process lifecycle fields below. It is never held while
	// spawning or signalling: those do IO and belong outside the lock.
	mu           sync.Mutex
	cmd          *exec.Cmd
	state        string
	doneCh       chan struct{}
	exitErr      error
	exitCode     int
	failureStage string
	startTime    time.Time

	// logRateMu guards the runner output rate limiter and drop counters. It is
	// separate from mu because the log path is a hot loop that must never contend
	// with lifecycle calls.
	logRateMu sync.Mutex
	logRate   [runnerLogSourceCount]runnerLogWindow
	logStats  [runnerLogSourceCount]runnerLogStats
	reported  [runnerLogSourceCount]runnerLogStats
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

// runnerArgs builds the package's argv. No --plugin and no --max-workers: the
// executable carries its own algorithm class and its own worker pool
// configuration.
func (p *RunnerProcess) runnerArgs() []string {
	args := make([]string, 0, len(p.config.BrokerSocks)*2+4)
	for _, sock := range p.config.BrokerSocks {
		if sock != "" {
			args = append(args, "--broker", sock)
		}
	}
	if p.config.AlgorithmName != "" {
		args = append(args, "--name", p.config.AlgorithmName)
	}
	if p.config.AlgorithmID != "" {
		args = append(args, "--algorithm-id", p.config.AlgorithmID)
	}
	if p.config.ConfigJSON != "" {
		args = append(args, "--config", p.config.ConfigJSON)
	}
	if p.config.DisableMotionGate {
		args = append(args, "--disable-motion-gate")
	}
	return args
}

// runnerEnv builds the child environment: the host environment overlaid with the
// package's .env values and then with the configured extras.
//
// Each key appears exactly once. Appending to os.Environ() instead would leave
// duplicates, and which of the two values a library's getenv() returns then
// depends on scan order rather than on the host's intent.
func (p *RunnerProcess) runnerEnv() []string {
	overlay := make(map[string]string)
	if p.config.EnvFile != "" {
		loaded, err := LoadDotEnv(p.config.EnvFile)
		if err != nil {
			// A missing file is optional (LoadDotEnv turns os.IsNotExist into an
			// empty map + nil error), so any error here is a real read/parse
			// failure (e.g. a line longer than bufio.Scanner's 64KB token limit).
			// Log it and continue with the variables that were parsed: a broken
			// .env must be observable without blocking the runner.
			//
			// The error is reduced to a stable reason instead of being logged
			// verbatim: a read failure surfaces as *fs.PathError, and the package
			// directory path must not reach the host log (same rule the runner
			// output path follows).
			p.logger.Warn("failed to load runner dotenv file",
				zap.String("reason", envFailureReason(err)),
			)
		}
		maps.Copy(overlay, loaded)
	}
	maps.Copy(overlay, p.config.ExtraEnv)

	return mergeEnviron(os.Environ(), overlay)
}

// envFailureReason classifies an env-file error without exposing any path.
//
// The reason set is intentionally small and stable: operators alert on it, so it
// must not become "the raw error text". A bufio.Scanner token-overflow (a line
// longer than 64KB) is reported as parse_error.
func envFailureReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "missing_file"
	case errors.Is(err, fs.ErrPermission):
		return "permission_denied"
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return "read_error"
	}
	return "parse_error"
}

// mergeEnviron overrides base with overlay, keeping one entry per key.
func mergeEnviron(base []string, overlay map[string]string) []string {
	merged := make(map[string]string, len(base)+len(overlay))
	order := make([]string, 0, len(base)+len(overlay))
	for _, kv := range base {
		key, value, found := strings.Cut(kv, "=")
		if !found {
			continue
		}
		if _, seen := merged[key]; !seen {
			order = append(order, key)
		}
		merged[key] = value
	}
	for key, value := range overlay {
		if _, seen := merged[key]; !seen {
			order = append(order, key)
		}
		merged[key] = value
	}

	env := make([]string, 0, len(order))
	for _, key := range order {
		env = append(env, key+"="+merged[key])
	}
	return env
}

// runnerPipes holds both output pipes of a spawn so every failure path can close
// them through one call.
type runnerPipes struct {
	stdoutR *os.File
	stdoutW *os.File
	stderrR *os.File
	stderrW *os.File
}

func openRunnerPipes() (*runnerPipes, error) {
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		closePipes(stdoutR, stdoutW)
		return nil, fmt.Errorf("create stderr pipe: %w", err)
	}
	return &runnerPipes{stdoutR: stdoutR, stdoutW: stdoutW, stderrR: stderrR, stderrW: stderrW}, nil
}

// closePipes closes whatever is non-nil. Close errors are deliberately ignored:
// these descriptors belong to a spawn that is already failing, and a close error
// cannot change that outcome.
func closePipes(files ...*os.File) {
	for _, file := range files {
		closeFileBestEffort(file)
	}
}

// closeFileBestEffort closes an inspection handle (a pipe end, a /proc read, a
// lock descriptor).
//
// The close error is ignored and documented once here rather than at each call
// site: the handle is read-only and its data is already consumed, so a close
// failure cannot change any decision; callers are either on a path that already
// has a more specific failure or on a success path with nothing left to do.
func closeFileBestEffort(file *os.File) {
	if file == nil {
		return
	}
	if err := file.Close(); err != nil {
		// best effort: nothing left to flush, no decision depends on it
	}
}

// Start launches the runner subprocess with environment injection and output logging.
func (p *RunnerProcess) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == StatusRunning || p.state == StatusStarting {
		return errors.New("process is already running")
	}
	p.beginStart()

	cmd := exec.Command(p.config.ExecutablePath, p.runnerArgs()...)

	// Run the runner in its own process group. Stop signals the whole group, so
	// grandchildren the runner spawns cannot leak while holding the broker socket
	// or shared memory after the runner itself has exited. On Linux this also
	// arms Pdeathsig, the only defence that survives host death without Go code
	// running (SIGKILL / OOM / native crash). See runnerSysProcAttr.
	cmd.SysProcAttr = runnerSysProcAttr()
	cmd.Env = p.runnerEnv()

	// Create pipes manually so cmd.Wait() does not close them prematurely while scanning.
	pipes, err := openRunnerPipes()
	if err != nil {
		p.failStart()
		return err
	}
	cmd.Stdout = pipes.stdoutW
	cmd.Stderr = pipes.stderrW

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
		p.watchExit(cmd, pipes, &logWg)
	}()

	if startErr := <-started; startErr != nil {
		pipes.close()
		p.failStart()
		return fmt.Errorf("start runner process: %w", startErr)
	}

	// Close parent's write ends immediately so EOF propagates when child terminates
	closePipes(pipes.stdoutW, pipes.stderrW)

	p.cmd = cmd
	p.startTime = time.Now()
	p.state = StatusRunning

	logWg.Add(2)
	go func() {
		defer logWg.Done()
		p.streamLogs(pipes.stdoutR, zapcore.DebugLevel, false)
		closePipes(pipes.stdoutR)
	}()
	go func() {
		defer logWg.Done()
		p.streamLogs(pipes.stderrR, zapcore.WarnLevel, true)
		closePipes(pipes.stderrR)
	}()

	// Everything the watcher needs is now initialised; let it proceed to Wait.
	close(ready)
	return nil
}

// beginStart resets the lifecycle fields for a fresh child. Callers must hold mu.
func (p *RunnerProcess) beginStart() {
	p.state = StatusStarting
	p.doneCh = make(chan struct{})
	p.exitErr = nil
	p.exitCode = 0
	p.failureStage = ""
}

// failStart publishes "not running" after a spawn that never produced a child.
// Callers must hold mu.
func (p *RunnerProcess) failStart() {
	p.state = StatusDisabled
	close(p.doneCh)
}

// close closes every pipe end of a spawn.
func (pipes *runnerPipes) close() {
	closePipes(
		pipes.stdoutR,
		pipes.stdoutW,
		pipes.stderrR,
		pipes.stderrW,
	)
}

// watchExit reaps the child and records how it ended. It runs on the goroutine
// that called runtime.LockOSThread and cmd.Start (see Start).
func (p *RunnerProcess) watchExit(cmd *exec.Cmd, pipes *runnerPipes, logWg *sync.WaitGroup) {
	// Never release the spawn thread before the child is reaped, or Pdeathsig
	// would fire on a healthy runner. This is the only unlock site, and only a
	// goroutine that actually holds the lock may unlock it.
	defer runtime.UnlockOSThread()

	err := cmd.Wait()
	p.drainRunnerLogs(pipes, logWg)
	p.recordExit(cmd, err)
}

// drainRunnerLogs waits for the pipe readers to consume what the child already
// wrote. If an orphaned grandchild keeps a write end open, the wait is bounded
// and the read ends are closed to unblock the readers.
func (p *RunnerProcess) drainRunnerLogs(pipes *runnerPipes, logWg *sync.WaitGroup) {
	logDone := make(chan struct{})
	go func() {
		logWg.Wait()
		close(logDone)
	}()

	select {
	case <-logDone:
	case <-time.After(100 * time.Millisecond):
		closePipes(pipes.stdoutR, pipes.stderrR)
		<-logDone
	}
}

// recordExit publishes the exit status, diagnosis and state of a reaped child.
func (p *RunnerProcess) recordExit(cmd *exec.Cmd, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.exitErr = err
	switch {
	case cmd.ProcessState != nil:
		p.exitCode = cmd.ProcessState.ExitCode()
	case err != nil:
		p.exitCode = -1
	}

	// Secondary failure-stage channel: exit codes are more fragile to native
	// changes than the stderr marker, so the stderr marker is primary and an exit
	// code only fills in or overrides the stage for codes the skeleton defines.
	if stage, ok := exitFailureStages[p.exitCode]; ok {
		p.failureStage = stage
	}

	if p.state == StatusRunning {
		p.state = StatusCrashed
		p.logCrashLocked(err)
	}

	close(p.doneCh)
}

// logCrashLocked reports how the runner ended, using the failure stage as the
// single source of the explanation. Callers must hold mu.
func (p *RunnerProcess) logCrashLocked(err error) {
	diagnosis, known := failureStageDiagnoses[p.failureStage]
	if !known {
		diagnosis = unexpectedExit
	}

	fields := []zap.Field{
		zap.Int("exit_code", p.exitCode),
		zap.String("reason", diagnosis.reason),
		zap.String("source", "process_exit"),
	}
	if !known {
		fields = append(fields, zap.Bool("error_present", err != nil))
	}
	p.logger.Log(diagnosis.level, diagnosis.message, fields...)
}

// FailureStage 返回本次退出的失败阶段诊断（空字符串表示未识别）。
//
// 看护循环用它区分「环境依赖不可用」与「算法包缺陷」：两者的自愈策略必须不同，
// 把相机掉线算成包崩溃会让一次网络抖动固化成 CRASH_LOOP。
func (p *RunnerProcess) FailureStage() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.failureStage
}

// setFailureStage records a failure stage observed on the runner's stderr.
func (p *RunnerProcess) setFailureStage(stage string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failureStage = stage
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
	noProcessHandle := cmd == nil || cmd.Process == nil
	if !noProcessHandle && state != StatusDisabled {
		// Mark target state as disabled to prevent treating clean stop as crash.
		p.state = StatusDisabled
	}
	p.mu.Unlock()

	if noProcessHandle || state == StatusDisabled {
		return nil
	}

	return p.terminate(cmd, doneCh, timeout)
}

// terminate is Stop's signalling ladder, split out so Stop's locking stays
// readable.
func (p *RunnerProcess) terminate(cmd *exec.Cmd, doneCh <-chan struct{}, timeout time.Duration) error {
	if err := p.signalRunner(cmd, syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		p.logger.Debug("signal SIGTERM to runner process", zap.Error(err))
	}

	termTimer := time.NewTimer(timeout)
	defer termTimer.Stop()

	select {
	case <-doneCh:
		return nil
	case <-termTimer.C:
	}

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
	if p.state != StatusRunning {
		return 0
	}
	return time.Since(p.startTime)
}
