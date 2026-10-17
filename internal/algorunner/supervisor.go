package algorunner

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
	store               LifecycleStore
	algoDir             string
	runnerPath          string
	resolver            BrokerSubscriptionResolver
	gallerySyncProvider GallerySyncProvider
	galleryBroadcaster  *GalleryBroadcaster
	logger              *zap.Logger
	mu                  sync.RWMutex
	watchers            map[string]*workerWatcher
	opts                RecoveryOptions
}

type workerWatcher struct {
	supervisor  *Supervisor
	algo        PackageMeta
	process     *RunnerProcess
	sm          *RecoveryStateMachine
	guard       *RollbackGuard
	stopCh      chan struct{}
	doneCh      chan struct{}
	brokerSocks []string
	mu          sync.Mutex
	stopMu      sync.Mutex
	stopOnce    sync.Once
	status      string
}

var (
	ErrAlgorithmNotInCrashLoop = errors.New("algorithm is not in CRASH_LOOP")
	ErrAlgorithmAlreadyRunning = errors.New("algorithm worker is already running or supervised")
	ErrNoBrokerSockets         = errors.New("cannot start runner without broker sockets")
)

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
//
// Broker socket paths are resolved per algorithm package through
// SetBrokerSubscriptionResolver: the broker host lives in the native engine and derives
// one socket per physical stream, so there is no process-wide default to fall
// back to.
func NewSupervisor(store LifecycleStore, algoDir, runnerPath string, opts RecoveryOptions, logger *zap.Logger) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{
		store:      store,
		algoDir:    algoDir,
		runnerPath: runnerPath,
		logger:     logger.Named("algorithm_supervisor"),
		watchers:   make(map[string]*workerWatcher),
		opts:       opts.normalize(),
	}
}

// SetBrokerSubscriptionResolver sets the resolver for multi-stream broker subscriptions.
func (s *Supervisor) SetBrokerSubscriptionResolver(resolver BrokerSubscriptionResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolver = resolver
}

// SetGallerySync sets the gallery sync provider and broadcaster for runner cold starts.
func (s *Supervisor) SetGallerySync(provider GallerySyncProvider, broadcaster *GalleryBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gallerySyncProvider = provider
	s.galleryBroadcaster = broadcaster
}

func (s *Supervisor) triggerGallerySyncAsyncLocked() {
	s.dispatchGallerySync(s.gallerySyncProvider, s.galleryBroadcaster)
}

func (s *Supervisor) triggerGallerySyncAsync() {
	s.mu.RLock()
	provider := s.gallerySyncProvider
	broadcaster := s.galleryBroadcaster
	s.mu.RUnlock()

	s.dispatchGallerySync(provider, broadcaster)
}

func (s *Supervisor) dispatchGallerySync(provider GallerySyncProvider, broadcaster *GalleryBroadcaster) {
	if provider == nil || broadcaster == nil {
		return
	}

	go func() {
		time.Sleep(100 * time.Millisecond)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		items, err := provider.GetGallerySyncData(ctx)
		if err != nil {
			s.logger.Warn("failed to fetch gallery sync data for cold-start", zap.Error(err))
			return
		}
		syncID := fmt.Sprintf("sync_%d", time.Now().UnixNano())
		if err := broadcaster.BroadcastSync(ctx, syncID, items); err != nil {
			s.logger.Warn("failed to broadcast cold-start gallery sync", zap.String("sync_id", syncID), zap.Error(err))
		} else {
			s.logger.Info("cold-start gallery sync broadcast completed", zap.String("sync_id", syncID), zap.Int("items_count", len(items)))
		}
	}()
}

// brokerResolver snapshots the configured broker subscription resolver under a
// read lock so callers can invoke it without holding s.mu.
func (s *Supervisor) brokerResolver() BrokerSubscriptionResolver {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.resolver
}

// resolveBrokerSocks 解析算法包应当订阅的 broker socket 路径列表，
// 并驱动实现侧同步订阅关系。
//
// 解析失败必须向上传播：退化为「没有 broker」会让 runner 拿不到 --broker 而 exit 5，
// 进而触发退避重启与 CRASH_LOOP 熔断，且失败原因会被完全掩盖。
//
// ⚠️ 本函数会执行 IO（DB 查询 + 逐相机路径解析），因此**不得在持有 s.mu 时调用**：
// 否则整个解析期间所有 GetStatus（RLock）都会被阻塞，而 Service.ListAlgorithms
// 会对每个算法调用一次 GetStatus，导致列表接口出现随规模放大的延迟尖刺。
// 调用方应先用 brokerResolver() 取 resolver 快照，再在锁外调用本函数。
func (s *Supervisor) resolveBrokerSocks(ctx context.Context, resolver BrokerSubscriptionResolver, algoID string) ([]string, error) {
	if resolver != nil {
		socks, err := resolver.SyncBrokerSubscriptions(ctx, algoID)
		if err != nil {
			return nil, fmt.Errorf("resolve broker paths for %s: %w", algoID, err)
		}
		return socks, nil
	}
	return nil, nil
}

// StartAlgorithm spawns and watches a runner subprocess for an algorithm.
func (s *Supervisor) StartAlgorithm(ctx context.Context, algo PackageMeta) error {
	// 1. 快路径：锁内只做存在性判断，并顺带取出 resolver 引用。
	s.mu.RLock()
	_, exists := s.watchers[algo.ID]
	resolver := s.resolver
	s.mu.RUnlock()
	if exists {
		return ErrAlgorithmAlreadyRunning
	}

	// 2. 锁外做 IO（DB 查询 + 订阅同步），避免阻塞 GetStatus。
	brokerSocks, err := s.resolveBrokerSocks(ctx, resolver, algo.ID)
	if err != nil {
		return err
	}
	if resolver != nil && len(brokerSocks) == 0 {
		return ErrNoBrokerSockets
	}

	// 3. 锁内注册：二次校验兜底解析期间产生的并发注册。
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watchers[algo.ID]; exists {
		return ErrAlgorithmAlreadyRunning
	}

	w := &workerWatcher{
		supervisor:  s,
		algo:        algo,
		sm:          NewRecoveryStateMachine(s.opts),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		status:      StatusStarting,
		brokerSocks: brokerSocks,
	}

	cfg := ProcessConfig{
		AlgorithmID:   algo.ID,
		AlgorithmName: algo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    algo.EntryPath(s.algoDir),
		BrokerSocks:   w.brokerSocks,
		EnvFile:       algo.EnvPath(s.algoDir),
		Logger:        s.logger,
	}
	w.process = NewRunnerProcess(cfg)

	if err := w.process.Start(); err != nil {
		return fmt.Errorf("start runner process: %w", err)
	}

	w.status = StatusRunning
	s.watchers[algo.ID] = w

	s.triggerGallerySyncAsyncLocked()

	// Start background watcher loop
	go w.superviseLoop()

	return nil
}

// retireWatcher removes w from the watcher registry if it is still the watcher
// registered for algoID, reporting whether it actually removed w.
//
// The return value is the guard for status writes: when a rollback or upgrade has
// already re-registered the same algorithm ID, the registered watcher is a
// different (live) process, so this caller must not touch the persisted status.
func (s *Supervisor) retireWatcher(algoID string, w *workerWatcher) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.watchers[algoID] != w {
		return false
	}
	delete(s.watchers, algoID)
	return true
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

	// Only persist DISABLED when this call actually retired its own watcher. If a
	// rollback/upgrade re-registered a live watcher under the same ID, the DB
	// must keep reflecting that running process.
	if s.retireWatcher(algoID, w) && s.store != nil {
		if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
			s.logger.Error("failed to update algorithm status to disabled", zap.String("id", algoID), zap.Error(err))
		}
	}
	return err
}

// RestartAlgorithm stops the running algorithm instance (if active) and restarts it,
// re-resolving broker paths for newly added or removed tasks.
//
// ⚠️ 重启是「同版本换订阅集」，不是停用：DB 中的状态必须保持 ACTIVE。
// StopAlgorithm 会把状态写成 DISABLED，因此这里在重新拉起后要写回 ACTIVE；
// 重启失败时同样要写回，否则一次失败的任务变更会静默停用该算法包。
func (s *Supervisor) RestartAlgorithm(ctx context.Context, algoID string) error {
	status := s.GetStatus(algoID)
	wasActive := status == StatusRunning || status == StatusStarting
	if wasActive {
		if err := s.StopAlgorithm(ctx, algoID); err != nil {
			return fmt.Errorf("stop algorithm for restart: %w", err)
		}
	}
	if s.store == nil {
		return errors.New("no store configured to load algorithm for restart")
	}
	algo, err := s.store.GetAlgorithmPackage(ctx, algoID)
	if err != nil {
		return fmt.Errorf("get algorithm for restart: %w", err)
	}

	if startErr := s.StartAlgorithm(ctx, algo); startErr != nil {
		if errors.Is(startErr, ErrNoBrokerSockets) {
			// 所有任务均被移除/禁用，平滑回退待命态
			// 此时 StopAlgorithm 已将 DB 状态写为 DISABLED，需恢复为 ACTIVE
			if wasActive {
				if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusActive); err != nil {
					return fmt.Errorf("restore algorithm status after retiring tasks: %w", err)
				}
			}
			return nil
		}
		return startErr
	}

	// StartAlgorithm 成功，但 StopAlgorithm 已把 DB 状态置为 DISABLED，需恢复为 ACTIVE。
	if wasActive {
		if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusActive); err != nil {
			return fmt.Errorf("restore algorithm status after restart: %w", err)
		}
	}
	return nil
}

// UpgradeAlgorithm gracefully transitions from an old version to a new version with rollback observation.
func (s *Supervisor) UpgradeAlgorithm(ctx context.Context, newAlgo PackageMeta, oldAlgo PackageMeta) error {
	// 1. 锁内摘下旧 watcher，解锁后停止旧进程，避免与 superviseLoop 死锁。
	s.mu.Lock()
	oldWatcher, exists := s.watchers[oldAlgo.ID]
	if exists {
		delete(s.watchers, oldAlgo.ID)
	}
	s.mu.Unlock()

	if exists {
		if stopErr := oldWatcher.stopProcess(3 * time.Second); stopErr != nil {
			s.logger.Warn("failed to stop old algorithm process during upgrade", zap.String("id", oldAlgo.ID), zap.Error(stopErr))
		}
		<-oldWatcher.doneCh
	}

	// 2. 锁外解析新版本的 broker 路径（resolver 会做 DB/订阅 IO）。
	brokerSocks, err := s.resolveBrokerSocks(ctx, s.brokerResolver(), newAlgo.ID)
	if err != nil {
		// 解析失败时不启动新版本，直接回滚到旧版本。
		// ⚠️ 此处不能持有 s.mu：rollbackToOld 自己会取锁。
		s.logger.Warn("failed to resolve broker paths for new version, attempting immediate rollback",
			zap.String("algorithm", newAlgo.ID),
			zap.Error(err),
		)
		return s.rollbackToOld(ctx, oldAlgo)
	}
	if s.brokerResolver() != nil && len(brokerSocks) == 0 {
		// 新版本无活跃任务，平滑进入待命态，无需拉起子进程
		return nil
	}

	// 3. 锁内二次校验 + 构建 watcher + 启动进程 + 注册。
	s.mu.Lock()
	if _, exists := s.watchers[newAlgo.ID]; exists {
		// 解析期间已有并发调用为同一 ID 注册了活跃 watcher：不覆盖它。
		s.mu.Unlock()
		return ErrAlgorithmAlreadyRunning
	}

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
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		status:      StatusStarting,
		brokerSocks: brokerSocks,
	}

	cfg := ProcessConfig{
		AlgorithmID:   newAlgo.ID,
		AlgorithmName: newAlgo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    newAlgo.EntryPath(s.algoDir),
		BrokerSocks:   w.brokerSocks,
		EnvFile:       newAlgo.EnvPath(s.algoDir),
		Logger:        s.logger,
	}
	w.process = NewRunnerProcess(cfg)

	if err := w.process.Start(); err != nil {
		// 新版本启动即失败：立即回滚。
		// ⚠️ 必须先解锁，rollbackToOld 自己会取锁，否则自死锁。
		s.mu.Unlock()
		s.logger.Warn("new algorithm failed to start, attempting immediate rollback",
			zap.String("algorithm", newAlgo.ID),
			zap.Error(err),
		)
		return s.rollbackToOld(ctx, oldAlgo)
	}

	w.status = StatusRunning
	s.watchers[newAlgo.ID] = w
	s.mu.Unlock()

	go w.superviseLoop()
	return nil
}

// rollbackToOld is the unlocked outer layer: it resolves the rollback target's
// broker paths (IO) outside s.mu, then delegates registration to
// rollbackToOldLocked.
func (s *Supervisor) rollbackToOld(ctx context.Context, oldAlgo PackageMeta) error {
	brokerSocks, err := s.resolveBrokerSocks(ctx, s.brokerResolver(), oldAlgo.ID)
	if err != nil {
		// 回滚目标同样解析不出 broker 路径：不启动半瘫的回滚进程，向上报错。
		return fmt.Errorf("resolve broker paths for rollback target %s: %w", oldAlgo.ID, err)
	}
	return s.rollbackToOldLocked(ctx, oldAlgo, brokerSocks)
}

// rollbackToOldLocked registers the rollback watcher under s.mu. It acquires the
// write lock itself and must NOT be called while holding s.mu.
func (s *Supervisor) rollbackToOldLocked(ctx context.Context, oldAlgo PackageMeta, brokerSocks []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	w := &workerWatcher{
		supervisor:  s,
		algo:        oldAlgo,
		sm:          NewRecoveryStateMachine(s.opts),
		stopCh:      make(chan struct{}),
		doneCh:      make(chan struct{}),
		status:      StatusRollingBack,
		brokerSocks: brokerSocks,
	}

	cfg := ProcessConfig{
		AlgorithmID:   oldAlgo.ID,
		AlgorithmName: oldAlgo.Name,
		RunnerPath:    s.runnerPath,
		PluginPath:    oldAlgo.EntryPath(s.algoDir),
		BrokerSocks:   w.brokerSocks,
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
		s.mu.Unlock()

		select {
		case <-w.doneCh:
		case <-ctx.Done():
			return ctx.Err()
		}

		s.retireWatcher(algoID, w)

		if s.store != nil {
			if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
				return err
			}
		}
		return nil
	}
	s.mu.Unlock()

	if s.store == nil {
		return nil
	}
	algo, err := s.store.GetAlgorithmPackage(ctx, algoID)
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

			// Re-resolve the broker list on every restart: the task set may have
			// changed since the runner was first started, so reusing the frozen
			// list would leave the runner subscribed to stale streams. This runs
			// OUTSIDE w.mu (and outside s.mu) because it performs DB / subscription
			// IO; w.mu is held by GetStatus / ResetAlgorithm / stopProcess, so IO
			// under it would reintroduce the very blocking R3 removed.
			//
			// On failure fall back to the cached list (never degrade into
			// CRASH_LOOP just because re-resolution failed). w.brokerSocks has a
			// single writer — this loop — so the assignment needs no extra lock.
			if newSocks, resolveErr := w.supervisor.resolveBrokerSocks(context.Background(), w.supervisor.brokerResolver(), w.algo.ID); resolveErr != nil {
				w.supervisor.logger.Warn("failed to re-resolve broker paths on crash restart, reusing cached list",
					zap.String("algorithm", w.algo.ID),
					zap.Error(resolveErr),
				)
			} else {
				w.brokerSocks = newSocks
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
				BrokerSocks:   w.brokerSocks,
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
			w.supervisor.triggerGallerySyncAsync()
		}
	}
}
