package algorithm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Supervisor oversees all algorithm runner subprocesses, watching for crashes,
// managing exponential backoffs, circuit breakers, and automatic rollbacks.
type Supervisor struct {
	store      *Store
	algoDir    string
	runnerPath string
	brokerSock string
	logger     *zap.Logger
	mu         sync.RWMutex
	watchers   map[string]*workerWatcher
	opts       RecoveryOptions
}

type workerWatcher struct {
	supervisor *Supervisor
	algo       Algorithm
	process    *RunnerProcess
	sm         *RecoveryStateMachine
	guard      *RollbackGuard
	stopCh     chan struct{}
	doneCh     chan struct{}
	mu         sync.Mutex
	stopMu     sync.Mutex
	stopOnce   sync.Once
	status     string
}

var ErrAlgorithmNotInCrashLoop = errors.New("algorithm is not in CRASH_LOOP")

func (w *workerWatcher) requestStop() {
	w.stopOnce.Do(func() { close(w.stopCh) })
}

func (w *workerWatcher) stopProcess(timeout time.Duration) error {
	w.stopMu.Lock()
	defer w.stopMu.Unlock()
	w.requestStop()

	w.mu.Lock()
	process := w.process
	w.mu.Unlock()
	if process == nil {
		return nil
	}
	return process.Stop(timeout)
}

// NewSupervisor creates a Supervisor instance.
func NewSupervisor(store *Store, algoDir, runnerPath, brokerSock string, opts RecoveryOptions, logger *zap.Logger) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{
		store:      store,
		algoDir:    algoDir,
		runnerPath: runnerPath,
		brokerSock: brokerSock,
		logger:     logger.Named("algorithm_supervisor"),
		watchers:   make(map[string]*workerWatcher),
		opts:       opts,
	}
}

// StartAlgorithm spawns and watches a runner subprocess for an algorithm.
func (s *Supervisor) StartAlgorithm(ctx context.Context, algo Algorithm) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.watchers[algo.ID]; exists {
		return errors.New("algorithm worker is already running or supervised")
	}

	w := &workerWatcher{
		supervisor: s,
		algo:       algo,
		sm:         NewRecoveryStateMachine(s.opts),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusStarting,
	}

	cfg := ProcessConfig{
		AlgorithmID:   algo.ID,
		AlgorithmName: algo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    algo.EntryPath(s.algoDir),
		BrokerSock:    s.brokerSock,
		EnvFile:       algo.EnvPath(s.algoDir),
		Logger:        s.logger,
	}
	w.process = NewRunnerProcess(cfg)

	if err := w.process.Start(); err != nil {
		return fmt.Errorf("start runner process: %w", err)
	}

	w.status = StatusRunning
	s.watchers[algo.ID] = w

	// Start background watcher loop
	go w.superviseLoop()

	return nil
}

// StopAlgorithm gracefully stops an algorithm runner and ceases supervision.
func (s *Supervisor) StopAlgorithm(ctx context.Context, algoID string) error {
	s.mu.Lock()
	w, exists := s.watchers[algoID]
	if !exists {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	err := w.stopProcess(3 * time.Second)
	<-w.doneCh

	s.mu.Lock()
	if s.watchers[algoID] == w {
		delete(s.watchers, algoID)
	}
	s.mu.Unlock()

	if s.store != nil {
		if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
			s.logger.Error("failed to update algorithm status to disabled", zap.String("id", algoID), zap.Error(err))
		}
	}
	return err
}

// UpgradeAlgorithm gracefully transitions from an old version to a new version with rollback observation.
func (s *Supervisor) UpgradeAlgorithm(ctx context.Context, newAlgo Algorithm, oldAlgo Algorithm) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 1. Stop old worker if running
	if oldWatcher, exists := s.watchers[oldAlgo.ID]; exists {
		delete(s.watchers, oldAlgo.ID)
		close(oldWatcher.stopCh)
		if stopErr := oldWatcher.process.Stop(3 * time.Second); stopErr != nil {
			s.logger.Warn("failed to stop old algorithm process during upgrade", zap.String("id", oldAlgo.ID), zap.Error(stopErr))
		}
		<-oldWatcher.doneCh
	}

	// 2. Set up new worker with RollbackGuard
	w := &workerWatcher{
		supervisor: s,
		algo:       newAlgo,
		sm:         NewRecoveryStateMachine(s.opts),
		guard: NewRollbackGuard(
			newAlgo.ID,
			newAlgo.Version,
			oldAlgo.Version,
			s.opts.RollbackGuardWindow,
			s.logger,
		),
		stopCh: make(chan struct{}),
		doneCh: make(chan struct{}),
		status: StatusStarting,
	}

	cfg := ProcessConfig{
		AlgorithmID:   newAlgo.ID,
		AlgorithmName: newAlgo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    newAlgo.EntryPath(s.algoDir),
		BrokerSock:    s.brokerSock,
		EnvFile:       newAlgo.EnvPath(s.algoDir),
		Logger:        s.logger,
	}
	w.process = NewRunnerProcess(cfg)

	if err := w.process.Start(); err != nil {
		// New algorithm failed to start immediately: rollback right away
		s.logger.Warn("new algorithm failed to start, attempting immediate rollback",
			zap.String("algorithm", newAlgo.ID),
			zap.Error(err),
		)
		return s.rollbackToOldLocked(ctx, oldAlgo)
	}

	w.status = StatusRunning
	s.watchers[newAlgo.ID] = w

	go w.superviseLoop()
	return nil
}

func (s *Supervisor) rollbackToOld(ctx context.Context, oldAlgo Algorithm) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rollbackToOldLocked(ctx, oldAlgo)
}

func (s *Supervisor) rollbackToOldLocked(ctx context.Context, oldAlgo Algorithm) error {
	w := &workerWatcher{
		supervisor: s,
		algo:       oldAlgo,
		sm:         NewRecoveryStateMachine(s.opts),
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
		status:     StatusRollingBack,
	}

	cfg := ProcessConfig{
		AlgorithmID:   oldAlgo.ID,
		AlgorithmName: oldAlgo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    oldAlgo.EntryPath(s.algoDir),
		BrokerSock:    s.brokerSock,
		EnvFile:       oldAlgo.EnvPath(s.algoDir),
		Logger:        s.logger,
	}
	w.process = NewRunnerProcess(cfg)

	if err := w.process.Start(); err != nil {
		w.status = StatusCrashLoop
		if s.store != nil {
			if updateErr := s.store.UpdateAlgorithmStatus(ctx, oldAlgo.ID, StatusCrashLoop); updateErr != nil {
				s.logger.Error("failed to update algorithm status to crash loop in db", zap.String("id", oldAlgo.ID), zap.Error(updateErr))
			}
		}
		return fmt.Errorf("rollback failed: %w", err)
	}

	w.status = StatusRunning
	s.watchers[oldAlgo.ID] = w
	if s.store != nil {
		if err := s.store.RollbackAlgorithmVersion(ctx, oldAlgo.ID, oldAlgo.Version, oldAlgo.PreviousVersion); err != nil {
			s.logger.Error("failed to persist rolled back algorithm version to db",
				zap.String("id", oldAlgo.ID),
				zap.String("version", oldAlgo.Version),
				zap.Error(err),
			)
		}
	}

	go w.superviseLoop()
	return nil
}

// ResetAlgorithm clears the circuit breaker for an algorithm in CRASH_LOOP.
func (s *Supervisor) ResetAlgorithm(ctx context.Context, algoID string) error {
	s.mu.Lock()
	w, exists := s.watchers[algoID]
	if exists {
		w.mu.Lock()
		status := w.status
		w.mu.Unlock()
		if status != StatusCrashLoop {
			s.mu.Unlock()
			return ErrAlgorithmNotInCrashLoop
		}
		select {
		case <-w.doneCh:
		case <-ctx.Done():
			s.mu.Unlock()
			return ctx.Err()
		}
		if s.store != nil {
			if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
				s.mu.Unlock()
				return err
			}
		}
		delete(s.watchers, algoID)
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	if s.store == nil {
		return nil
	}
	algo, err := s.store.GetAlgorithm(ctx, algoID)
	if err != nil {
		return err
	}
	if algo.Status == StatusDisabled {
		return nil
	}
	if algo.Status != StatusCrashLoop {
		return ErrAlgorithmNotInCrashLoop
	}
	return s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled)
}

// GetStatus returns the current runtime status of an algorithm.
func (s *Supervisor) GetStatus(algoID string) string {
	s.mu.RLock()
	w, exists := s.watchers[algoID]
	s.mu.RUnlock()

	if !exists {
		return StatusDisabled
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status
}

// StopAll stops all managed processes gracefully on application shutdown.
func (s *Supervisor) StopAll(timeout time.Duration) {
	s.mu.Lock()
	watchers := make([]*workerWatcher, 0, len(s.watchers))
	for _, w := range s.watchers {
		watchers = append(watchers, w)
	}
	s.watchers = make(map[string]*workerWatcher)
	s.mu.Unlock()

	var wg sync.WaitGroup
	for _, w := range watchers {
		wg.Add(1)
		go func(watcher *workerWatcher) {
			defer wg.Done()
			if stopErr := watcher.stopProcess(timeout); stopErr != nil {
				s.logger.Warn("failed to stop worker process on shutdown",
					zap.String("id", watcher.algo.ID),
					zap.Error(stopErr),
				)
			}
			<-watcher.doneCh
		}(w)
	}
	wg.Wait()
}

func (w *workerWatcher) superviseLoop() {
	defer close(w.doneCh)

	var guardTimer *time.Timer
	if w.guard != nil {
		guardTimer = time.NewTimer(w.supervisor.opts.RollbackGuardWindow)
		defer guardTimer.Stop()
	}
	stableTicker := time.NewTicker(w.supervisor.opts.StableRunningThreshold)
	defer stableTicker.Stop()

	for {
		var guardCh <-chan time.Time
		if guardTimer != nil {
			guardCh = guardTimer.C
		}

		select {
		case <-w.stopCh:
			return
		case <-guardCh:
			if w.guard != nil {
				w.guard.MarkStable()
			}
			guardTimer = nil
		case <-stableTicker.C:
			w.sm.CheckStableReset(w.process.RunningDuration())
		case <-w.process.Done():
			crashTime := time.Now()

			w.mu.Lock()
			// Check if clean stop was initiated
			select {
			case <-w.stopCh:
				w.status = StatusDisabled
				w.mu.Unlock()
				return
			default:
			}

			// Check rollback guard first
			if w.guard != nil && w.guard.ShouldRollback(crashTime) {
				w.status = StatusRollingBack
				w.mu.Unlock()

				// Execute rollback
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

				w.supervisor.mu.Lock()
				delete(w.supervisor.watchers, w.algo.ID)
				w.supervisor.mu.Unlock()

				if err := w.supervisor.rollbackToOld(context.Background(), oldAlgo); err != nil {
					w.supervisor.logger.Error("automatic rollback failed to restore previous version",
						zap.String("algorithm", w.algo.ID),
						zap.String("target_version", prevVer),
						zap.Error(err),
					)
				}
				return
			}

			// Record crash in state machine
			shouldRestart, delay, isCircuit := w.sm.RecordCrash(crashTime)
			if isCircuit {
				w.status = StatusCrashLoop
				w.mu.Unlock()

				w.supervisor.logger.Error("algorithm entered CRASH_LOOP (circuit broken), halting auto-restart",
					zap.String("algorithm", w.algo.ID),
				)
				if w.supervisor.store != nil {
					if updateErr := w.supervisor.store.UpdateAlgorithmStatus(context.Background(), w.algo.ID, StatusCrashLoop); updateErr != nil {
						w.supervisor.logger.Error("failed to update algorithm status to crash loop in db",
							zap.String("id", w.algo.ID),
							zap.Error(updateErr),
						)
					}
				}
				return
			}

			w.status = StatusCrashed
			w.mu.Unlock()

			if !shouldRestart {
				return
			}

			w.supervisor.logger.Info("restarting crashed algorithm after backoff delay",
				zap.String("algorithm", w.algo.ID),
				zap.Duration("delay", delay),
			)

			// Wait for backoff delay or stop signal.
			timer := time.NewTimer(delay)
			select {
			case <-w.stopCh:
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return
			case <-timer.C:
			}

			// Re-create process and start.
			w.mu.Lock()
			select {
			case <-w.stopCh:
				w.status = StatusDisabled
				w.mu.Unlock()
				return
			default:
			}
			w.status = StatusStarting
			cfg := ProcessConfig{
				AlgorithmID:   w.algo.ID,
				AlgorithmName: w.algo.Name,
				RunnerPath:    w.supervisor.runnerPath,
				PluginPath:    w.algo.EntryPath(w.supervisor.algoDir),
				BrokerSock:    w.supervisor.brokerSock,
				EnvFile:       w.algo.EnvPath(w.supervisor.algoDir),
				Logger:        w.supervisor.logger,
			}
			w.process = NewRunnerProcess(cfg)
			if err := w.process.Start(); err != nil {
				w.status = StatusCrashed
				w.mu.Unlock()
				w.supervisor.logger.Warn("failed to restart runner process",
					zap.String("algorithm", w.algo.ID),
					zap.Error(err),
				)
				continue
			}

			w.status = StatusRunning
			w.mu.Unlock()
		}
	}
}
