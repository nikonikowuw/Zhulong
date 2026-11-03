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

	// mu guards process and status. It is only held for field access, never
	// across ResolveEntry/Start/Stop.
	mu      sync.RWMutex
	process *RunnerProcess
	status  string

	// stopMu serialises stopProcess so two concurrent stops cannot signal the
	// same process group twice.
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
func (w *workerWatcher) withRollbackGuard(opts RollbackGuardOptions) *workerWatcher {
	w.guard = NewRollbackGuard(opts)
	return w
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
			w.sm.CheckStableReset(w.runningDuration())
		case <-w.processDone():
			if w.handleExit() {
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
// 5s, 10s, 20s, 40s … capped at max.
//
// It never trips the circuit breaker: the camera coming back must be enough to
// bring the algorithm back, without an operator resetting the crash counter.
func dependencyBackoff(attempt int, max time.Duration) time.Duration {
	const base = 5 * time.Second
	delay := base
	for i := 1; i < attempt && (max <= 0 || delay < max); i++ {
		delay *= 2
	}
	if max > 0 && delay > max {
		delay = max
	}
	return delay
}

// handleExit reacts to one process exit, reporting whether supervision must end.
func (w *workerWatcher) handleExit() bool {
	crashTime := time.Now()

	// A stop was requested: this is a clean shutdown, not a crash.
	if w.stopRequested() {
		w.setStatus(StatusDisabled)
		return true
	}

	// 依赖不可用（broker socket 没了）不是包缺陷。
	//
	// broker socket 由相机流侧创建：相机掉线/重连时它必然消失，runner 拿着旧路径
	// 连接失败后按契约退出 5。把它算成崩溃，一次 20 秒网络抖动就会把算法固化成
	// CRASH_LOOP；落在升级观察窗内还会把刚装的版本回滚掉。这与规格中「缺 --broker
	// 宁可待命，也不要虚假熔断」是同一条原则。
	if isDependencyFailure(w.exitFailureStage()) {
		// 上一轮跑够久说明依赖是刚断的，重新起算阶梯；否则继续沿用。
		if w.runningDuration() >= w.supervisor.opts.MaxBackoff {
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
		return !w.restartAfterBackoff(delay)
	}
	w.dependencyRetries = 0

	// A crash inside the observation window means the new version is not fit to
	// run: roll back instead of restarting it.
	if w.guard != nil && w.guard.ShouldRollback(crashTime) {
		w.setStatus(StatusRollingBack)
		w.rollbackToPreviousVersion()
		return true
	}

	shouldRestart, delay, circuitBroken := w.sm.RecordCrash(crashTime)
	if circuitBroken {
		w.enterCrashLoop()
		return true
	}

	w.setStatus(StatusCrashed)
	if !shouldRestart {
		return true
	}
	return !w.restartAfterBackoff(delay)
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
// supervision should continue.
func (w *workerWatcher) restartAfterBackoff(delay time.Duration) bool {
	w.supervisor.logger.Info("restarting crashed algorithm after backoff delay",
		zap.String("algorithm", w.algo.ID),
		zap.Duration("delay", delay),
	)

	backoff := time.NewTimer(delay)
	defer backoff.Stop()
	select {
	case <-w.stopCh:
		return false
	case <-backoff.C:
	}

	// Re-resolve the broker list on every restart: the task set may have changed
	// since the runner was first started, so reusing the frozen list would leave
	// the runner subscribed to stale streams. This runs OUTSIDE w.mu (and outside
	// s.mu) because it performs DB / subscription IO.
	w.refreshBrokerSocks()

	if w.stopRequested() {
		w.setStatus(StatusDisabled)
		return false
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
// supervision should continue.
func (w *workerWatcher) respawn() bool {
	w.setStatus(StatusStarting)

	// Entry resolution and the spawn are IO: they must not run under w.mu, which
	// GetStatus / ResetAlgorithm / stopProcess take.
	cfg, cfgErr := w.supervisor.processConfig(w.algo, w.brokerSocks)
	if cfgErr != nil {
		// 入口不可执行是包缺陷，重试不会自愈：直接停在 CRASH_LOOP，
		// 让运维重新安装/重建，而不是每 5s 刷一次 start 失败日志。
		w.setStatus(StatusCrashLoop)
		w.supervisor.logger.Error("package entry became unusable, giving up restart",
			zap.String("algorithm", w.algo.ID),
			zap.Error(cfgErr),
		)
		return false
	}

	process := NewRunnerProcess(cfg)
	// Publish before Start: a start that fails closes the process's done channel,
	// and the next loop iteration must observe that exit to count the crash.
	w.setProcess(process)
	if err := process.Start(); err != nil {
		w.setStatus(StatusCrashed)
		w.supervisor.logger.Warn("failed to restart runner process",
			zap.String("algorithm", w.algo.ID),
			zap.Error(err),
		)
		return true
	}

	w.setStatus(StatusRunning)
	w.supervisor.triggerGallerySyncAsync()
	return true
}
