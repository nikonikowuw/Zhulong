package algorunner

import (
	"context"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algoipc"
	"go.uber.org/zap"
)

// workerWatcher supervises one algorithm package process.
type workerWatcher struct {
	supervisor  *Supervisor
	algo        PackageMeta
	sm          *RecoveryStateMachine
	guard       *RollbackGuard
	stopCh      chan struct{}
	doneCh      chan struct{}
	brokerSocks []string

	// mu guards process, status and spawnStopErr. It is only held for field
	// access, never across ResolveEntry/Start/Stop.
	mu      sync.RWMutex
	process *RunnerProcess
	status  string

	// spawnStopErr is the failure of startProcess's complement stop, i.e. of a
	// runner that was spawned concurrently with a stop request and did not die.
	//
	// It has to live on the watcher because that stop happens after stopProcess
	// has already returned nil: StopAlgorithm reads it once doneCh has closed
	// (the only writer, the supervise loop, is finished by then) and joins it into
	// its own result, so "the child is still running" is never reported as a clean
	// stop.
	spawnStopErr error

	// stopMu serialises stopProcess so two concurrent stops cannot signal the
	// same process group twice. It also closes the "stop requested, spawn not yet
	// published" window: startProcess holds it across the stop-request re-check
	// and the publication only (Start runs outside it, see its comment), while
	// stopProcess holds it across requestStop + Stop(currentProcess()).
	//
	// That gives exactly two interleavings, both safe:
	//   - stopProcess takes the lock first: stopCh is already closed, so
	//     startProcess sees stopRequested and never spawns anything.
	//   - startProcess takes the lock first: the process it publishes is the one
	//     stopProcess reads from currentProcess() afterwards, so the child it
	//     starts is the child that gets stopped.
	//
	// Without the publication under the lock a stop could land between the
	// stopRequested check and the spawn: StopAlgorithm would stop the OLD (already
	// exited) process, return, write DISABLED, while the loop's freshly spawned
	// child kept running with nobody left to reap it.
	stopMu   sync.Mutex
	stopOnce sync.Once

	// dependencyRetries counts consecutive "the frame broker is gone" exits.
	//
	// Only the supervise loop goroutine touches it, so it needs no lock. It is
	// deliberately NOT the crash counter: a camera reconnect makes the broker
	// socket disappear, which is an environment condition, not a package defect.
	dependencyRetries int
}

// newWorkerWatcher creates an unsupervised watcher for a package.
func newWorkerWatcher(s *Supervisor, algo PackageMeta, brokerSocks []string) *workerWatcher {
	return &workerWatcher{
		supervisor:  s,
		algo:        algo,
		sm:          NewRecoveryStateMachine(s.opts),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		status:      StatusStarting,
		brokerSocks: brokerSocks,
	}
}

// withRollbackGuard arms the observation window that turns a crash into a
// rollback. Only a watcher created by an upgrade has one.
func (w *workerWatcher) withRollbackGuard(opts RollbackGuardOptions) {
	w.guard = NewRollbackGuard(opts)
}

func (w *workerWatcher) requestStop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

// stopRequested reports whether a stop was requested for this watcher.
func (w *workerWatcher) stopRequested() bool {
	select {
	case <-w.stopCh:
		return true
	default:
		return false
	}
}

// stopProcess stops the watcher's process and asks the loop to exit.
func (w *workerWatcher) stopProcess(timeout time.Duration) error {
	w.stopMu.Lock()
	defer w.stopMu.Unlock()
	w.requestStop()

	process := w.currentProcess()
	if process == nil {
		return nil
	}
	return process.Stop(timeout)
}

func (w *workerWatcher) setProcess(process *RunnerProcess) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.process = process
}

func (w *workerWatcher) currentProcess() *RunnerProcess {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.process
}

func (w *workerWatcher) setStatus(status string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.status = status
}

func (w *workerWatcher) currentStatus() string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.status
}

// setSpawnedStopError records why the complement stop in startProcess failed.
func (w *workerWatcher) setSpawnedStopError(err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spawnStopErr = err
}

// spawnedStopError reports the complement-stop failure, if any.
//
// Callers must read it only after doneCh has closed: the supervise loop is the
// single writer and has provably finished by then.
func (w *workerWatcher) spawnedStopError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.spawnStopErr
}

// processDone exposes the current process's termination channel, so the loop can
// select on it without reading the field outside its lock.
func (w *workerWatcher) processDone() <-chan struct{} {
	process := w.currentProcess()
	if process == nil {
		return nil
	}
	return process.Done()
}

// runningDuration reports how long the current process has been alive.
func (w *workerWatcher) runningDuration() time.Duration {
	process := w.currentProcess()
	if process == nil {
		return 0
	}
	return process.RunningDuration()
}

// lastRunDuration reports how long the process that just exited ran.
//
// It must NOT be runningDuration: by the time the loop reacts to the exit the
// process state has left StatusRunning, so RunningDuration already reports 0 and
// the dependency ladder could never be reset.
func (w *workerWatcher) lastRunDuration() time.Duration {
	process := w.currentProcess()
	if process == nil {
		return 0
	}
	return process.LastRunDuration()
}

// supervisionVerdict is the supervise loop's answer to "keep watching?".
//
// It replaces a bare bool whose polarity was easy to misread: `if w.handleExit()
// { return }` read like "if the exit was handled, return", which is the opposite
// of what it meant.
type supervisionVerdict int

const (
	// supervisionContinues keeps the loop watching and restarting.
	supervisionContinues supervisionVerdict = iota
	// supervisionEnds stops the loop: a clean stop, a rollback, or CRASH_LOOP.
	supervisionEnds
)

// superviseLoop is the watcher state machine: it reacts to stops, the rollback
// observation window, stable uptime and process exits.
func (w *workerWatcher) superviseLoop() {
	defer close(w.doneCh)

	guardCh, stopGuard := w.startRollbackGuard()
	defer stopGuard()

	stableTicker := time.NewTicker(w.supervisor.opts.StableRunningThreshold)
	defer stableTicker.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-guardCh:
			w.markGuardStable()
			guardCh = nil
		case <-stableTicker.C:
			if w.sm.CheckStableReset(w.runningDuration()) {
				w.supervisor.logger.Info("algorithm stayed stable, crash history reset",
					zap.String("algorithm", w.algo.ID),
				)
			}
		case <-w.processDone():
			if w.handleExit() == supervisionEnds {
				return
			}
		}
	}
}

// startRollbackGuard arms the observation window of a freshly upgraded package.
// The returned channel is nil — and therefore never selected — when the watcher
// has no guard to observe.
func (w *workerWatcher) startRollbackGuard() (<-chan time.Time, func()) {
	if w.guard == nil {
		return nil, func() {}
	}
	timer := time.NewTimer(w.supervisor.opts.RollbackGuardWindow)
	return timer.C, func() { timer.Stop() }
}

func (w *workerWatcher) markGuardStable() {
	if w.guard != nil {
		w.guard.MarkStable()
	}
}

// exitFailureStage reports the failure stage recorded for the process that
// just exited, so the loop can tell an unusable dependency apart from a broken
// package.
func (w *workerWatcher) exitFailureStage() string {
	process := w.currentProcess()
	if process == nil {
		return ""
	}
	return process.FailureStage()
}

// isDependencyFailure reports whether this exit means "something the package
// depends on is unavailable" rather than "the package is broken".
func isDependencyFailure(stage string) bool {
	return stage == algoipc.FailureStageBrokerConnect ||
		stage == algoipc.FailureStageBrokerLost
}

// dependencyBackoff is the restart ladder for an unavailable dependency:
// 5s, 10s, 20s, 40s … capped at maxDelay (zero or negative means uncapped).
//
// It never trips the circuit breaker: the camera coming back must be enough to
// bring the algorithm back, without an operator resetting the crash counter.
func dependencyBackoff(attempt int, maxDelay time.Duration) time.Duration {
	const base = 5 * time.Second
	delay := base
	for range max(attempt-1, 0) {
		if maxDelay > 0 && delay >= maxDelay {
			break
		}
		delay *= 2
	}
	// The trailing clamp is load-bearing: with attempt == 1 and a maxDelay below
	// the base, the loop body never runs, so this is the only cap.
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

// handleExit reacts to one process exit, reporting whether supervision must end.
func (w *workerWatcher) handleExit() supervisionVerdict {
	crashTime := time.Now()

	// A stop was requested: this is a clean shutdown, not a crash.
	if w.stopRequested() {
		w.setStatus(StatusDisabled)
		return supervisionEnds
	}

	// 依赖不可用（broker socket 没了）不是包缺陷。
	//
	// broker socket 由相机流侧创建：相机掉线/重连时它必然消失，runner 拿着旧路径
	// 连接失败后按契约退出 5。把它算成崩溃，一次 20 秒网络抖动就会把算法固化成
	// CRASH_LOOP；落在升级观察窗内还会把刚装的版本回滚掉。这与规格中「缺 --broker
	// 宁可待命，也不要虚假熔断」是同一条原则。
	if isDependencyFailure(w.exitFailureStage()) {
		// 上一轮跑够久说明依赖是刚断的，重新起算阶梯；否则继续沿用。
		//
		// 这里读的是 LastRunDuration 而不是 RunningDuration：进程退出后状态已不是
		// StatusRunning，RunningDuration 恒返回 0，阶梯就永远不会重置。
		if w.lastRunDuration() >= w.supervisor.opts.dependencyStableReset() {
			w.dependencyRetries = 0
		}
		w.dependencyRetries++
		delay := dependencyBackoff(w.dependencyRetries, w.supervisor.opts.MaxBackoff)
		w.setStatus(StatusCrashed)
		w.supervisor.logger.Warn("runner lost its frame broker; restarting without tripping the crash breaker",
			zap.String("algorithm", w.algo.ID),
			zap.Int("consecutive", w.dependencyRetries),
			zap.Duration("delay", delay),
		)
		return w.restartAfterBackoff(delay)
	}
	w.dependencyRetries = 0

	// A crash inside the observation window means the new version is not fit to
	// run: roll back instead of restarting it.
	if w.guard != nil && w.guard.ShouldRollback(crashTime) {
		w.setStatus(StatusRollingBack)
		w.rollbackToPreviousVersion()
		return supervisionEnds
	}

	shouldRestart, delay, circuitBroken := w.sm.RecordCrash(crashTime)
	if circuitBroken {
		w.enterCrashLoop()
		return supervisionEnds
	}

	w.setStatus(StatusCrashed)
	if !shouldRestart {
		return supervisionEnds
	}
	return w.restartAfterBackoff(delay)
}

// enterCrashLoop halts auto-restart and records the state. The watcher stays
// registered so GetStatus keeps reporting CRASH_LOOP until an operator resets it.
func (w *workerWatcher) enterCrashLoop() {
	w.setStatus(StatusCrashLoop)
	w.supervisor.logger.Error("algorithm entered CRASH_LOOP (circuit broken), halting auto-restart",
		zap.String("algorithm", w.algo.ID),
	)
	w.supervisor.persistStatus(context.Background(), w.algo.ID, StatusCrashLoop, "crash_loop")
}

// rollbackToPreviousVersion hands this watcher's slot to the previous version.
func (w *workerWatcher) rollbackToPreviousVersion() {
	prevVer := w.guard.PreviousVersion()
	failedVer := w.algo.Version

	oldAlgo := w.algo
	oldAlgo.Version = prevVer
	oldAlgo.PreviousVersion = failedVer

	w.supervisor.logger.Warn("initiating automatic rollback due to crash in observation window",
		zap.String("algorithm", w.algo.ID),
		zap.String("target_version", prevVer),
		zap.String("failed_version", failedVer),
	)

	w.supervisor.retireWatcher(w.algo.ID, w)

	if err := w.supervisor.rollbackToOld(context.Background(), oldAlgo); err != nil {
		w.supervisor.logger.Error("automatic rollback failed to restore previous version",
			zap.String("algorithm", w.algo.ID),
			zap.String("target_version", prevVer),
			zap.Error(err),
		)
	}
}

// restartAfterBackoff waits out the backoff delay and respawns, reporting whether
// supervision must end (the same polarity as handleExit).
func (w *workerWatcher) restartAfterBackoff(delay time.Duration) supervisionVerdict {
	w.supervisor.logger.Info("restarting crashed algorithm after backoff delay",
		zap.String("algorithm", w.algo.ID),
		zap.Duration("delay", delay),
	)

	backoff := time.NewTimer(delay)
	defer backoff.Stop()
	select {
	case <-w.stopCh:
		return supervisionEnds
	case <-backoff.C:
	}

	// Re-resolve the broker list on every restart: the task set may have changed
	// since the runner was first started, so reusing the frozen list would leave
	// the runner subscribed to stale streams. This runs OUTSIDE w.mu (and outside
	// s.mu) because it performs DB / subscription IO.
	w.refreshBrokerSocks()

	if w.stopRequested() {
		w.setStatus(StatusDisabled)
		return supervisionEnds
	}
	return w.respawn()
}

// refreshBrokerSocks re-resolves the subscription list, falling back to the
// cached one. Never degrade into CRASH_LOOP just because re-resolution failed:
// the previous list is still a working subscription set.
//
// w.brokerSocks has a single writer — this loop — so the assignment needs no
// extra lock.
func (w *workerWatcher) refreshBrokerSocks() {
	newSocks, resolveErr := w.supervisor.resolveBrokerSocks(
		context.Background(),
		w.supervisor.brokerResolver(),
		w.algo.ID,
	)
	if resolveErr != nil {
		w.supervisor.logger.Warn("failed to re-resolve broker paths on crash restart, reusing cached list",
			zap.String("algorithm", w.algo.ID),
			zap.Error(resolveErr),
		)
		return
	}
	w.brokerSocks = newSocks
}

// respawn creates and starts a fresh process for the watcher, reporting whether
// supervision must end.
func (w *workerWatcher) respawn() supervisionVerdict {
	w.setStatus(StatusStarting)

	// Entry resolution is IO: it must not run under w.mu, which GetStatus /
	// ResetAlgorithm / stopProcess take.
	cfg, cfgErr := w.supervisor.processConfig(w.algo, w.brokerSocks)
	if cfgErr != nil {
		// 入口不可执行是包缺陷，重试不会自愈：直接停在 CRASH_LOOP，
		// 让运维重新安装/重建，而不是每 5s 刷一次 start 失败日志。
		w.setStatus(StatusCrashLoop)
		w.supervisor.logger.Error("package entry became unusable, giving up restart",
			zap.String("algorithm", w.algo.ID),
			zap.Error(cfgErr),
		)
		return supervisionEnds
	}

	process := NewRunnerProcess(cfg)
	switch w.startProcess(process) {
	case spawnStopped:
		return supervisionEnds
	case spawnFailed:
		// The failed start closed the process's done channel, so the next loop
		// iteration observes that exit and counts the crash.
		return supervisionContinues
	case spawnStarted:
		w.supervisor.triggerGallerySyncAsync()
		return supervisionContinues
	}
	// Every constant of spawnOutcome has a case above, so this return exists only
	// for the compiler's exhaustiveness check; it is not a decision about a state.
	// A fallback arm labelled "spawnStarted" would be worse: a state added later
	// would be silently treated as a running child.
	return supervisionEnds
}

// spawnOutcome is startProcess's single three-way answer.
//
// The states are mutually exclusive, and spawnStopped is deliberately not "the
// child never ran": it covers both stop sources — a stop that was already
// requested before anything was spawned, and a start that had to be undone by the
// complement stop after it had already run (see startProcess).
type spawnOutcome int

const (
	// spawnStopped means a stop was requested. In the common case nothing was
	// spawned; when the stop raced the start, this function stopped the child it
	// had already spawned. A stop that times out is only recorded as an Error:
	// the leftover is collected by Pdeathsig or the next boot's orphan sweep.
	spawnStopped spawnOutcome = iota
	// spawnFailed means Start failed; the process's done channel is closed.
	spawnFailed
	// spawnStarted means the child is running.
	spawnStarted
)

// startProcess publishes and starts a freshly built process so that it cannot be
// orphaned by a concurrent stop.
//
// Under stopMu it does exactly two things: re-check the stop request, then publish
// the process. Everything else — the fork/exec and the ready handshake — happens
// outside it, because §6「锁外 IO 铁律」 of the algorithm guidelines forbids IO
// under a lock: stopProcess needs the same stopMu, and nothing in the start path
// bounds fork/exec, so a spawn under the lock would make every stop wait for it.
// The ordering argument that makes the two interleavings safe is written once, on
// workerWatcher.stopMu.
//
// One window survives that ordering, and it is why this function re-checks after
// Start: Stop may take stopMu after the publication but reach RunnerProcess.Stop
// before Start has taken the process's own mu, in which case Stop sees cmd == nil
// and returns nil without killing anything. By then Start has spawned a child that
// belongs to nobody, so this function has to stop it itself.
func (w *workerWatcher) startProcess(process *RunnerProcess) spawnOutcome {
	w.stopMu.Lock()
	if w.stopRequested() {
		w.stopMu.Unlock()
		w.setStatus(StatusDisabled)
		return spawnStopped
	}

	// Publish before Start: a start that fails closes the process's done channel,
	// and the next loop iteration must observe that exit to count the crash.
	w.setProcess(process)
	w.stopMu.Unlock()

	// 锁外 fork/exec：Start 会一直持有进程自己的 mu 直到 ready 握手完成，放进 stopMu
	// 只会让停止路径无谓地等一次完全无关的 spawn。
	if err := process.Start(); err != nil {
		w.setStatus(StatusCrashed)
		w.supervisor.logger.Warn("failed to restart runner process",
			zap.String("algorithm", w.algo.ID),
			zap.Error(err),
		)
		return spawnFailed
	}

	// 见上：Stop 可能在 Start 拿到进程 mu 之前看到 cmd == nil 而直接返回 nil。此时
	// 子进程已经真实存在，必须在这里补一次 Stop，否则它无人回收。
	if w.stopRequested() {
		stopStarted := time.Now()
		stopErr := process.Stop(stopAlgorithmTimeout)
		// How long the complement stop itself took: a value close to
		// stopAlgorithmTimeout means the raced child ignored SIGTERM and needed
		// SIGKILL, which is what oncall needs to tell apart from a clean handoff.
		stopDelay := time.Since(stopStarted)
		if stopErr != nil {
			// stopProcess already returned nil for this watcher, so the failure is
			// recorded on the watcher for StopAlgorithm to pick up: a runner that
			// survived the complement stop must not be reported as stopped.
			w.setSpawnedStopError(stopErr)
			w.supervisor.logger.Error("failed to stop runner spawned concurrently with a stop request",
				zap.String("algorithm", w.algo.ID),
				zap.Int("pid", process.PID()),
				zap.Duration("delay", stopDelay),
				zap.Error(stopErr),
			)
		} else {
			w.supervisor.logger.Info("stopped runner spawned concurrently with a stop request",
				zap.String("algorithm", w.algo.ID),
				zap.Int("pid", process.PID()),
				zap.Duration("delay", stopDelay),
			)
		}
		w.setStatus(StatusDisabled)
		return spawnStopped
	}
	w.setStatus(StatusRunning)
	return spawnStarted
}
