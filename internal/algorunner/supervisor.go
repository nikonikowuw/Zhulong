package algorunner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

const (
	// stopAlgorithmTimeout bounds one graceful runner shutdown.
	stopAlgorithmTimeout = 3 * time.Second

	// gallerySyncDelay gives a freshly started runner time to finish its own
	// startup (broker connect, shm attach) before the gallery is pushed. A
	// MSG_GALLERY_SYNC that arrives earlier lands on a socket nobody is reading
	// yet, and the runner's gallery stays empty until the next enrolment.
	gallerySyncDelay = 100 * time.Millisecond

	// gallerySyncTimeout bounds one cold-start gallery push, including the
	// provider query, so the goroutine always has an exit.
	gallerySyncTimeout = 10 * time.Second

	// taskConfigPushTimeout bounds resolving + pushing the per-stream task config
	// before a spawn. It sits on the start path, so it must be much shorter than
	// the gallery's: a slow config query must never look like a hung start.
	taskConfigPushTimeout = 5 * time.Second
)

// Supervisor oversees all algorithm runner subprocesses, watching for crashes,
// managing exponential backoffs, circuit breakers, and automatic rollbacks.
type Supervisor struct {
	store  LifecycleStore
	rt     Runtime
	logger *zap.Logger
	opts   RecoveryOptions

	// mu guards the watcher registry and the late-bound collaborators. It is
	// never held across IO or a spawn: GetStatus takes an RLock and
	// Service.ListAlgorithms calls it once per algorithm, so a write lock held
	// over Lstat/fork turns every status read into a latency spike.
	mu                  sync.RWMutex
	watchers            map[string]*workerWatcher
	resolver            BrokerSubscriptionResolver
	gallerySyncProvider GallerySyncProvider
	galleryBroadcaster  *GalleryBroadcaster
	taskConfigProvider  TaskConfigProvider
	taskConfigCast      *TaskConfigBroadcaster

	// stopCh is closed by StopAll so background goroutines (gallery sync) have a
	// deterministic exit instead of only their own timeout.
	stopCh   chan struct{}
	stopOnce sync.Once
}

var (
	ErrAlgorithmNotInCrashLoop = errors.New("algorithm is not in CRASH_LOOP")
	ErrAlgorithmAlreadyRunning = errors.New("algorithm worker is already running or supervised")
	ErrNoBrokerSockets         = errors.New("cannot start runner without broker sockets")
)

// NewSupervisor creates a Supervisor instance.
//
// Broker socket paths are resolved per algorithm package through
// SetBrokerSubscriptionResolver: the broker host lives in the native engine and derives
// one socket per physical stream, so there is no process-wide default to fall
// back to.
func NewSupervisor(store LifecycleStore, rt Runtime, opts RecoveryOptions, logger *zap.Logger) *Supervisor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Supervisor{
		store:    store,
		rt:       rt,
		logger:   logger.Named("algorithm.supervisor"),
		watchers: make(map[string]*workerWatcher),
		opts:     opts.normalize(),
		stopCh:   make(chan struct{}),
	}
}

// SetBrokerSubscriptionResolver sets the resolver for multi-stream broker subscriptions.
//
// The resolver is late-bound because it lives in the native engine, which is
// created after the supervisor. It must be set before the first
// StartAlgorithm/RestartAlgorithm call; changing it while runners are supervised
// only affects their next (re)start.
func (s *Supervisor) SetBrokerSubscriptionResolver(resolver BrokerSubscriptionResolver) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolver = resolver
}

// SetGallerySync sets the gallery sync provider and broadcaster for runner cold starts.
//
// Like SetBrokerSubscriptionResolver it is late-bound and must be set before the
// first StartAlgorithm call.
func (s *Supervisor) SetGallerySync(provider GallerySyncProvider, broadcaster *GalleryBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gallerySyncProvider = provider
	s.galleryBroadcaster = broadcaster
}

// SetTaskConfig wires the per-stream task configuration path: the provider turns
// (algorithm, streams) into config JSON, the broadcaster pushes it to each
// stream's FrameBroker, which replays it to every package that connects later.
//
// Like SetGallerySync it is late-bound and must be set before the first
// StartAlgorithm call.
func (s *Supervisor) SetTaskConfig(provider TaskConfigProvider, broadcaster *TaskConfigBroadcaster) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskConfigProvider = provider
	s.taskConfigCast = broadcaster
}

// pushTaskConfigs seeds each stream's broker cache with the current task config.
//
// Called **before** the child is spawned: the package receives the configuration
// as a handshake replay instead of racing its first frames against a push. A push
// that fails is logged and ignored — the package then falls back to its compiled
// defaults, which is exactly the pre-existing behaviour.
func (s *Supervisor) pushTaskConfigs(algoID string) {
	s.mu.RLock()
	provider := s.taskConfigProvider
	broadcaster := s.taskConfigCast
	s.mu.RUnlock()
	if provider == nil || broadcaster == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), taskConfigPushTimeout)
	defer cancel()

	if regions, ok := provider.(TaskRegionProvider); ok {
		snapshots, err := regions.TaskRegionsFor(ctx, algoID)
		if err == nil {
			err = broadcaster.BroadcastRegions(ctx, algoID, snapshots)
		}
		if err != nil {
			s.logger.Warn("failed to push task regions", zap.String("id", algoID), zap.Error(err))
		}
	}
	configs, err := provider.TaskConfigsFor(ctx, algoID)
	if err != nil {
		s.logger.Warn("failed to resolve task configs for package",
			zap.String("id", algoID),
			zap.Error(err),
		)
		return
	}
	if err := broadcaster.BroadcastAlgorithm(ctx, algoID, configs); err != nil {
		s.logger.Warn("failed to push task configs to brokers",
			zap.String("id", algoID),
			zap.Int("streams", len(configs)),
			zap.Error(err),
		)
		return
	}
	if len(configs) > 0 {
		s.logger.Debug("task configs pushed to brokers",
			zap.String("id", algoID),
			zap.Int("streams", len(configs)),
		)
	}
}

// triggerGallerySyncAsync pushes the face gallery to the runners, off the caller's
// goroutine and outside every lock.
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
		select {
		case <-time.After(gallerySyncDelay):
		case <-s.stopCh:
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), gallerySyncTimeout)
		defer cancel()

		items, err := provider.GetGallerySyncData(ctx)
		if err != nil {
			s.logger.Warn("failed to fetch gallery sync data for cold-start", zap.Error(err))
			return
		}
		syncID := "sync_" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err := broadcaster.BroadcastSync(ctx, syncID, items); err != nil {
			s.logger.Warn("failed to broadcast cold-start gallery sync",
				zap.String("sync_id", syncID),
				zap.Error(err),
			)
			return
		}
		s.logger.Info("cold-start gallery sync broadcast completed",
			zap.String("sync_id", syncID),
			zap.Int("items_count", len(items)),
		)
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
func (s *Supervisor) resolveBrokerSocks(
	ctx context.Context,
	resolver BrokerSubscriptionResolver,
	algoID string,
) ([]string, error) {
	if resolver == nil {
		return []string{}, nil
	}
	socks, err := resolver.SyncBrokerSubscriptions(ctx, algoID)
	if err != nil {
		return nil, fmt.Errorf("resolve broker paths for %q: %w", algoID, err)
	}
	return socks, nil
}

// processConfig builds the spawn configuration for one installed package,
// resolving its entry executable.
//
// Resolution happens on every (re)start instead of once at install time: a
// package directory can be replaced by an upgrade, and an old binary must never
// be reached through a stale path. Errors are returned verbatim (wrapped by the
// caller with the algorithm id) so that an unusable entry is reported as a
// package defect — see IsEntryError.
//
// It performs filesystem IO: call it outside s.mu and outside w.mu.
func (s *Supervisor) processConfig(algo PackageMeta, brokerSocks []string) (ProcessConfig, error) {
	entry, err := ResolveEntry(s.rt.PackageDir(algo.ID, algo.Version), algo.ID)
	if err != nil {
		return ProcessConfig{}, err
	}
	return ProcessConfig{
		AlgorithmID:       algo.ID,
		AlgorithmName:     algo.Name,
		ExecutablePath:    entry,
		BrokerSocks:       brokerSocks,
		EnvFile:           s.rt.EnvPath(algo.ID, algo.Version),
		ConfigJSON:        s.processConfigJSON(algo.ID),
		Logger:            s.logger,
		DisableMotionGate: algo.DisableMotionGate,
	}, nil
}

// algoConfigEnv is the host-environment key carrying a package's process-level
// configuration as inline JSON.
//
// It is deliberately the *only* source in this iteration: there is no
// algorithm-level configuration record in the database yet, and inventing one
// before the delivery channel existed would have meant shipping a schema with
// nothing to verify it against. Because the value reaches the package as argv it
// stays visible in `ps` — which is exactly why credentials must never go here.
const algoConfigEnv = "ZHULONG_ALGO_CONFIG"

// processConfigJSON resolves the inline JSON handed to `--config`. Invalid or
// non-object values are dropped with a warning instead of being forwarded: a
// typo in the host environment must fall back to the package defaults (one log
// line), not become an UNDEFINED behaviour inside a package we cannot inspect.
func (s *Supervisor) processConfigJSON(algoID string) string {
	raw := strings.TrimSpace(os.Getenv(algoConfigEnv))
	if raw == "" {
		return ""
	}
	var probe map[string]any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		s.logger.Warn("ignoring malformed process-level algorithm config",
			zap.String("algorithm_id", algoID),
			zap.String("env", algoConfigEnv),
			zap.String("reason", "not a JSON object"),
		)
		return ""
	}
	return raw
}

// IsEntryError reports whether err came from entry resolution, i.e. the package
// on disk cannot be executed at all. Callers must not retry such a failure: a
// missing or legacy entry does not heal by itself, and retrying produces a
// backoff storm that looks like a crash loop while the real cause is an
// obsolete package.
func IsEntryError(err error) bool {
	isMissing := errors.Is(err, ErrMissingEntry)
	isLegacy := errors.Is(err, ErrLegacyEntry)
	isInvalid := errors.Is(err, ErrInvalidEntry)
	return isMissing || isLegacy || isInvalid
}

// persistStatus records an algorithm's runtime status on a background path.
//
// A nil store is allowed (read-only hosts, unit tests) and a write failure is
// logged rather than returned: every caller is a goroutine with no receiver for
// the error. Synchronous paths (ResetAlgorithm, RestartAlgorithm) return the
// error to their caller instead.
func (s *Supervisor) persistStatus(ctx context.Context, algoID, status, action string) {
	if s.store == nil {
		return
	}
	if err := s.store.UpdateAlgorithmStatus(ctx, algoID, status); err != nil {
		s.logger.Error("failed to persist algorithm status",
			zap.String("id", algoID),
			zap.String("status", status),
			zap.String("action", action),
			zap.Error(err),
		)
	}
}

// watcherFor returns the registered watcher for an algorithm, or nil.
func (s *Supervisor) watcherFor(algoID string) *workerWatcher {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.watchers[algoID]
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

// register publishes w as the watcher for its algorithm, reporting false when a
// concurrent caller already owns the slot.
func (s *Supervisor) register(w *workerWatcher) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.watchers[w.algo.ID]; exists {
		return false
	}
	s.watchers[w.algo.ID] = w
	return true
}

// StartAlgorithm spawns and watches a runner subprocess for an algorithm.
//
// Three phases, and the ordering is the point: a cheap existence check, then all
// IO (broker resolution, entry resolution, the spawn itself) outside every lock,
// then a single write-locked publication. Anything expensive inside the lock
// blocks GetStatus for the whole fleet.
func (s *Supervisor) StartAlgorithm(ctx context.Context, algo PackageMeta) error {
	if s.watcherFor(algo.ID) != nil {
		return ErrAlgorithmAlreadyRunning
	}

	resolver := s.brokerResolver()
	brokerSocks, err := s.resolveBrokerSocks(ctx, resolver, algo.ID)
	if err != nil {
		return err
	}
	if resolver != nil && len(brokerSocks) == 0 {
		return ErrNoBrokerSockets
	}

	return s.spawnWatcher(newWorkerWatcher(s, algo, brokerSocks))
}

// spawnWatcher resolves the package entry, spawns the child and registers w.
//
// It must be called without s.mu and without w.mu: both the entry resolution and
// the spawn do IO. The published status is RUNNING; callers that need a
// different pre-spawn status (rollback) set it before calling.
func (s *Supervisor) spawnWatcher(w *workerWatcher) error {
	cfg, err := s.processConfig(w.algo, w.brokerSocks)
	if err != nil {
		return fmt.Errorf("resolve package entry for %q %q: %w", w.algo.ID, w.algo.Version, err)
	}

	// Cheap re-check before paying for a fork: if a concurrent caller registered
	// this ID while the entry was being resolved, do not spawn a child that
	// cannot be owned.
	if s.watcherFor(w.algo.ID) != nil {
		return ErrAlgorithmAlreadyRunning
	}

	// 先把任务级配置灌进各流的 FrameBroker 缓存，再拉起子进程：新进程握手时立刻收到
	// 重放，不需要和首帧赛跑。放在 spawn 之前而不是进程起来之后，是因为「没有客户端在
	// 监听」的广播会打到 0 个连接，只能靠 Broker 侧缓存兜住。
	s.pushTaskConfigs(w.algo.ID)

	process := NewRunnerProcess(cfg)
	w.setProcess(process)
	if err := process.Start(); err != nil {
		w.setStatus(StatusCrashed)
		return fmt.Errorf("start package process: %w", err)
	}

	w.setStatus(StatusRunning)
	if !s.register(w) {
		s.abortSpawn(w)
		return ErrAlgorithmAlreadyRunning
	}

	go w.superviseLoop()
	return nil
}

// abortSpawn reaps a child that lost the registration race. Leaving it running
// would mean an unsupervised process holding broker sockets that the winning
// watcher's restart could never bind again.
func (s *Supervisor) abortSpawn(w *workerWatcher) {
	process := w.currentProcess()
	if process == nil {
		return
	}
	if err := process.Stop(time.Second); err != nil {
		s.logger.Warn("failed to stop runner that lost the registration race",
			zap.String("id", w.algo.ID),
			zap.Error(err),
		)
	}
}

// StopAlgorithm gracefully stops an algorithm runner and ceases supervision.
func (s *Supervisor) StopAlgorithm(ctx context.Context, algoID string) error {
	w := s.watcherFor(algoID)
	if w == nil {
		return nil
	}

	stopErr := w.stopProcess(stopAlgorithmTimeout)

	// The watcher loop must be observed to finish before the registry entry is
	// dropped, but the wait is bounded by the caller's context: an HTTP request
	// that already gave up must not keep this goroutine parked.
	select {
	case <-w.doneCh:
	case <-ctx.Done():
		return fmt.Errorf("wait for algorithm %q to stop: %w", algoID, ctx.Err())
	}

	// Only persist DISABLED when this call actually retired its own watcher. If a
	// rollback/upgrade re-registered a live watcher under the same ID, the DB
	// must keep reflecting that running process.
	if s.retireWatcher(algoID, w) {
		s.persistStatus(ctx, algoID, StatusDisabled, "stop")
	}
	return stopErr
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

	// ErrNoBrokerSockets is not a failure here: every task was removed or
	// disabled, so the runner retires to standby and the package stays ACTIVE.
	startErr := s.StartAlgorithm(ctx, algo)
	if startErr != nil && !errors.Is(startErr, ErrNoBrokerSockets) {
		return startErr
	}

	// StopAlgorithm wrote DISABLED above; both the standby and the restarted case
	// must end up ACTIVE again.
	if wasActive {
		if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusActive); err != nil {
			return fmt.Errorf("restore algorithm status after restart: %w", err)
		}
	}
	return nil
}

// UpgradeAlgorithm gracefully transitions from an old version to a new version with rollback observation.
func (s *Supervisor) UpgradeAlgorithm(ctx context.Context, newAlgo PackageMeta, oldAlgo PackageMeta) error {
	if err := s.stopForUpgrade(ctx, oldAlgo.ID); err != nil {
		return err
	}

	// 锁外解析新版本的 broker 路径（resolver 会做 DB/订阅 IO）。
	resolver := s.brokerResolver()
	brokerSocks, err := s.resolveBrokerSocks(ctx, resolver, newAlgo.ID)
	if err != nil {
		// 解析失败时不启动新版本，直接回滚到旧版本。
		s.logger.Warn("failed to resolve broker paths for new version, attempting immediate rollback",
			zap.String("algorithm", newAlgo.ID),
			zap.Error(err),
		)
		return s.rollbackToOld(ctx, oldAlgo)
	}
	if resolver != nil && len(brokerSocks) == 0 {
		// 新版本无活跃任务，平滑进入待命态，无需拉起子进程
		return nil
	}

	w := newWorkerWatcher(s, newAlgo, brokerSocks)
	w.withRollbackGuard(RollbackGuardOptions{
		PackageID:       newAlgo.ID,
		NewVersion:      newAlgo.Version,
		PreviousVersion: oldAlgo.Version,
		GuardWindow:     s.opts.RollbackGuardWindow,
		Logger:          s.logger,
	})

	startErr := s.spawnWatcher(w)
	if startErr == nil {
		return nil
	}
	if errors.Is(startErr, ErrAlgorithmAlreadyRunning) {
		// 解析期间已有并发调用为同一 ID 注册了活跃 watcher：不覆盖它。
		return startErr
	}
	// 新版本启动即失败：立即回滚。
	s.logger.Warn("new algorithm failed to start, attempting immediate rollback",
		zap.String("algorithm", newAlgo.ID),
		zap.Error(startErr),
	)
	return s.rollbackToOld(ctx, oldAlgo)
}

// stopForUpgrade detaches and stops the old watcher.
//
// The watcher is removed from the registry before it is stopped, so a concurrent
// StartAlgorithm sees "no watcher" instead of a half-stopped one, and the stop
// itself happens outside s.mu.
func (s *Supervisor) stopForUpgrade(ctx context.Context, algoID string) error {
	s.mu.Lock()
	oldWatcher, exists := s.watchers[algoID]
	if exists {
		delete(s.watchers, algoID)
	}
	s.mu.Unlock()

	if !exists {
		return nil
	}

	stopErr := oldWatcher.stopProcess(stopAlgorithmTimeout)
	select {
	case <-oldWatcher.doneCh:
	case <-ctx.Done():
		return fmt.Errorf("wait for algorithm %q to stop: %w", algoID, ctx.Err())
	}
	if stopErr != nil {
		s.logger.Warn("failed to stop old algorithm process during upgrade",
			zap.String("id", algoID),
			zap.Error(stopErr),
		)
	}
	return nil
}

// rollbackToOld restores the previous version after a failed upgrade.
//
// Both IO steps (broker resolution, entry resolution + spawn) run outside s.mu;
// only the registration takes the write lock.
func (s *Supervisor) rollbackToOld(ctx context.Context, oldAlgo PackageMeta) error {
	brokerSocks, err := s.resolveBrokerSocks(ctx, s.brokerResolver(), oldAlgo.ID)
	if err != nil {
		// 回滚目标同样解析不出 broker 路径：不启动半瘫的回滚进程，向上报错。
		return fmt.Errorf("resolve broker paths for rollback target %q: %w", oldAlgo.ID, err)
	}

	w := newWorkerWatcher(s, oldAlgo, brokerSocks)
	w.setStatus(StatusRollingBack)

	if startErr := s.spawnWatcher(w); startErr != nil {
		if errors.Is(startErr, ErrAlgorithmAlreadyRunning) {
			return startErr
		}
		// The rollback target itself cannot run: the package is definitively
		// broken, so record CRASH_LOOP instead of leaving a stale ACTIVE status.
		w.setStatus(StatusCrashLoop)
		s.persistStatus(ctx, oldAlgo.ID, StatusCrashLoop, "rollback_start_failure")
		return fmt.Errorf("rollback failed: %w", startErr)
	}

	if s.store == nil {
		return nil
	}
	if err := s.store.RollbackAlgorithmVersion(
		ctx,
		oldAlgo.ID,
		oldAlgo.Version,
		oldAlgo.PreviousVersion,
	); err != nil {
		s.logger.Error("failed to persist rolled back algorithm version to db",
			zap.String("id", oldAlgo.ID),
			zap.String("version", oldAlgo.Version),
			zap.Error(err),
		)
	}
	return nil
}

// ResetAlgorithm clears the circuit breaker for an algorithm in CRASH_LOOP.
func (s *Supervisor) ResetAlgorithm(ctx context.Context, algoID string) error {
	w := s.watcherFor(algoID)
	if w == nil {
		return s.resetFromStore(ctx, algoID)
	}

	if w.currentStatus() != StatusCrashLoop {
		return ErrAlgorithmNotInCrashLoop
	}

	select {
	case <-w.doneCh:
	case <-ctx.Done():
		return ctx.Err()
	}

	s.retireWatcher(algoID, w)
	if s.store == nil {
		return nil
	}
	if err := s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled); err != nil {
		return fmt.Errorf("persist disabled status for reset: %w", err)
	}
	return nil
}

// resetFromStore clears a persisted CRASH_LOOP that has no live watcher, e.g. the
// host restarted while the package was broken.
func (s *Supervisor) resetFromStore(ctx context.Context, algoID string) error {
	if s.store == nil {
		return nil
	}
	algo, err := s.store.GetAlgorithmPackage(ctx, algoID)
	if err != nil {
		return err
	}
	switch algo.Status {
	case StatusDisabled:
		return nil
	case StatusCrashLoop:
		return s.store.UpdateAlgorithmStatus(ctx, algoID, StatusDisabled)
	default:
		return ErrAlgorithmNotInCrashLoop
	}
}

// GetStatus returns the current runtime status of an algorithm.
func (s *Supervisor) GetStatus(algoID string) string {
	w := s.watcherFor(algoID)
	if w == nil {
		return StatusDisabled
	}
	return w.currentStatus()
}

// StopAll stops all managed processes gracefully on application shutdown.
func (s *Supervisor) StopAll(timeout time.Duration) {
	s.stopOnce.Do(func() { close(s.stopCh) })

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
			// One-shot wait, not a loop, so time.After is safe here: it bounds the
			// watcher loop's own exit and reports if it never happens.
			select {
			case <-watcher.doneCh:
			case <-time.After(timeout):
				s.logger.Error("worker watcher did not finish after shutdown timeout",
					zap.String("id", watcher.algo.ID),
				)
			}
		}(w)
	}
	wg.Wait()
}
