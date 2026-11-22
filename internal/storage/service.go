package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

const (
	// statusRefreshInterval is how often the background loop re-probes the volume.
	statusRefreshInterval = 15 * time.Second
	// statusCacheTTL is how long a status snapshot is served without re-probing.
	statusCacheTTL = 5 * time.Second
	// breakdownScanInterval throttles the recursive size scan, which is far more
	// expensive than a statfs probe.
	breakdownScanInterval = 30 * time.Second
	// breakdownScanTimeout bounds a single recursive scan.
	breakdownScanTimeout = 3 * time.Second
	// pruneRunTimeout bounds one automatic prune cycle.
	pruneRunTimeout = 5 * time.Minute
	// pruneCallbackTimeout bounds the record reconciliation callback after a
	// prune. It is deliberately independent of pruneRunTimeout: a long cleanup
	// must not cancel the durable DB cascade.
	pruneCallbackTimeout = 30 * time.Second
	// autoCleanupAuditInterval is the minimum gap between audits for cycles that
	// actually deleted files; autoCleanupIdleAuditInterval is the slower cadence
	// for zero-progress cycles.
	autoCleanupAuditInterval     = 5 * time.Minute
	autoCleanupIdleAuditInterval = time.Hour
	// remotePruneInterval throttles periodic SQLite-indexed TTL/quota checks in S3 mode.
	remotePruneInterval = 5 * time.Minute
	// tickTimeout bounds one status refresh inside the background loop.
	tickTimeout = 10 * time.Second
)

var (
	// errServiceAlreadyStarted is returned by Start while the monitoring loop runs.
	errServiceAlreadyStarted = errors.New("storage service already started")
	// errServiceAlreadyStopped is returned by Start after Stop: writers and
	// readers already observed the final state, so the service is terminal.
	errServiceAlreadyStopped = errors.New("storage service already stopped")
	// errEmergencyStop is the safe summary recorded when the storage circuit
	// breaker trips. The detailed reason travels in the detail field.
	errEmergencyStop = errors.New("storage emergency stop engaged")
)

// ServiceDeps groups the collaborators of Service.
//
// NewService substitutes defaults for a nil Inspector, Gate, Cleaner, Repo and
// Logger, so tests and embedding hosts can supply only what they exercise.
// Production wiring (NewStorageComponents) passes every field explicitly.
type ServiceDeps struct {
	Repo      Repository
	Inspector PathInspector
	Gate      *EmergencyGate
	Cleaner   *CleanerEngine
	Cipher    SecretCipher
	S3Probe   func(ctx context.Context, cfg filestore.S3Config) error
	S3Health  func(ctx context.Context, cfg filestore.S3Config) (filestore.HealthStatus, error)
	Logger    *zap.Logger
}

// Service coordinates storage inspection, persistence, hysteresis cleaning, and write safety.
type Service struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	migrateMu sync.Mutex
	logger    *zap.Logger
	repo      Repository
	inspector PathInspector
	gate      *EmergencyGate
	cleaner   *CleanerEngine
	auditor   *audit.Service
	cipher    SecretCipher
	s3Probe   func(context.Context, filestore.S3Config) error
	s3Health  func(context.Context, filestore.S3Config) (filestore.HealthStatus, error)

	activeMediaDir        string
	expectedDeviceID      uint64
	activeSpoolDir        string
	expectedSpoolDeviceID uint64
	lastStatus            *StorageStatus
	lastBreakdown         StorageUsageBreakdown
	lastBreakdownAt       time.Time
	lastRemotePruneAt     time.Time
	afterPrune            func(context.Context, []string)
	onConfigChanged       func(context.Context, *StorageConfig)
	remoteStore           filestore.Store
	remoteProvider        RemotePruneProvider
	onRemoteClipsDeleted  RemoteClipReconciler
	asyncJobs             AsyncJobService
	migrationSource       MigrationSource

	backoffMu            sync.Mutex
	nextAutoPrune        time.Time
	lastAutoCleanupAudit time.Time
	autoPruneFailCount   int

	stopCh   chan struct{}
	drained  chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
	stopped  atomic.Bool
	wg       sync.WaitGroup
}

// NewService creates and initializes a storage management service.
func NewService(deps ServiceDeps) *Service {
	logger := deps.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	inspector := deps.Inspector
	if inspector == nil {
		inspector = NewDefaultPathInspector()
	}

	gate := deps.Gate
	if gate == nil {
		gate = NewEmergencyGate(logger)
	}

	cleaner := deps.Cleaner
	if cleaner == nil {
		cleaner = NewCleanerEngine(inspector, logger, CleanerOptions{CapacityProbe: probeCapacity})
	}

	repo := deps.Repo
	if repo == nil {
		repo = NewRepositoryWithCipher(nil, deps.Cipher, logger)
	}

	s3Probe := deps.S3Probe
	if s3Probe == nil {
		s3Probe = defaultS3Probe
	}
	s3Health := deps.S3Health
	if s3Health == nil {
		s3Health = defaultS3Health
	}

	s := &Service{
		logger:    logger,
		repo:      repo,
		inspector: inspector,
		gate:      gate,
		cleaner:   cleaner,
		cipher:    deps.Cipher,
		s3Probe:   s3Probe,
		s3Health:  s3Health,
		stopCh:    make(chan struct{}),
		drained:   make(chan struct{}),
	}

	gate.SetCallbacks(
		func(reason string) {
			if auditor := s.auditLog(); auditor != nil {
				// Background event: no client origin, so it is recorded with an
				// empty IP rather than a faked loopback address.
				auditor.RecordSystem(
					ActionStorageEmergencyStop,
					"media_storage",
					reason,
					errEmergencyStop,
				)
			}
		},
		func() {
			if auditor := s.auditLog(); auditor != nil {
				auditor.RecordSystem(
					ActionStorageEmergencyResume,
					"media_storage",
					"storage capacity recovered to safe levels",
					nil,
				)
			}
		},
	)

	return s
}

// RegisterWriteObserver forwards filesystem write-block transitions to the host.
// The host uses them to shed log volume while an emergency stop is active.
func (s *Service) RegisterWriteObserver(observer func(blocked bool)) {
	if s == nil || s.gate == nil {
		return
	}
	s.gate.RegisterObserver(observer)
}

// SetAuditor configures the system audit log service.
//
// User-initiated storage operations keep their existing audit path and are out
// of scope for this refactor; only the circuit-breaker callbacks below were
// changed to record as system events. The setter stays because the auditor is
// only available once the audit service itself is wired, which happens after
// the storage service is constructed.
func (s *Service) SetAuditor(auditor *audit.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditor = auditor
}

// SetAfterPrune registers the callback the record domain uses to reconcile rows
// after files were deleted. IDs are the snapshot units whose files are fully
// gone; the callback must enqueue them durably before attempting DB deletion.
func (s *Service) SetAfterPrune(callback func(context.Context, []string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterPrune = callback
}

// SetEvidenceGuard installs the optional alarm-evidence guard used before
// deleting snapshot units. The guard is fail-closed: an error protects the
// batch rather than risking historical alarm evidence.
func (s *Service) SetEvidenceGuard(guard EvidenceGuard) {
	if s == nil || s.cleaner == nil {
		return
	}
	s.cleaner.SetEvidenceGuard(guard)
}

// SetConfigChangeListener registers a callback fired after storage configuration
// updates persist successfully (used by filestore.Manager for hot backend switching).
func (s *Service) SetConfigChangeListener(fn func(context.Context, *StorageConfig)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onConfigChanged = fn
	s.mu.Unlock()
}

// SetRemoteStore configures the filestore.Store used by SQLite-indexed remote cleanup.
func (s *Service) SetRemoteStore(store filestore.Store) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.remoteStore = store
	s.mu.Unlock()
}

// SetRemotePruneProvider configures the SQLite candidate source for remote cleanup.
func (s *Service) SetRemotePruneProvider(provider RemotePruneProvider) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.remoteProvider = provider
	s.mu.Unlock()
}

// SetRemoteClipReconciler registers a callback to delete or reconcile SQLite
// event_clips rows after their remote objects are removed.
func (s *Service) SetRemoteClipReconciler(fn RemoteClipReconciler) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.onRemoteClipsDeleted = fn
	s.mu.Unlock()
}

// SyncStore loads the stored configuration and notifies the config-change listener
// so runtime stores switch onto S3 once camera encryption keys unlock.
func (s *Service) SyncStore(ctx context.Context) error {
	if s == nil || s.repo == nil {
		return nil
	}
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return databaseFailure(fmt.Errorf("load storage config for sync: %w", err))
	}
	s.notifyConfigChanged(ctx, cfg)
	return nil
}

func (s *Service) currentRemoteStore() filestore.Store {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.remoteStore
}

func (s *Service) currentRemoteProvider() RemotePruneProvider {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.remoteProvider
}

func (s *Service) notifyRemoteClipsDeleted(ctx context.Context, clipIDs []string) {
	if len(clipIDs) == 0 {
		return
	}
	s.mu.RLock()
	fn := s.onRemoteClipsDeleted
	s.mu.RUnlock()
	if fn == nil {
		return
	}
	if err := fn(ctx, clipIDs); err != nil {
		s.logger.Warn("reconcile remote clip rows after storage cleanup failed", zap.Error(err))
	}
}

func (s *Service) notifyConfigChanged(ctx context.Context, cfg *StorageConfig) {
	s.mu.RLock()
	fn := s.onConfigChanged
	s.mu.RUnlock()
	if fn != nil && cfg != nil {
		fn(ctx, cfg)
	}
}

// HasExistingEncryptedData reports whether storage config stores encrypted S3 credentials.
func (s *Service) HasExistingEncryptedData(ctx context.Context) (bool, error) {
	if s == nil || s.repo == nil {
		return false, nil
	}
	aware, ok := s.repo.(SecretAwareRepository)
	if !ok {
		return false, nil
	}
	return aware.HasExistingEncryptedData(ctx)
}

// GetDecryptedS3Config returns the decrypted active S3 driver configuration.
func (s *Service) GetDecryptedS3Config(ctx context.Context) (*filestore.S3Config, error) {
	if s == nil || s.repo == nil {
		return nil, ErrS3SecretRequired
	}
	aware, ok := s.repo.(SecretAwareRepository)
	if !ok {
		return nil, ErrS3SecretRequired
	}
	return aware.GetDecryptedS3Config(ctx)
}

func defaultS3Probe(ctx context.Context, cfg filestore.S3Config) error {
	driver, err := filestore.NewS3Driver(cfg)
	if err != nil {
		return err
	}
	return driver.ProbeWritable(ctx)
}

func defaultS3Health(ctx context.Context, cfg filestore.S3Config) (filestore.HealthStatus, error) {
	driver, err := filestore.NewS3Driver(cfg)
	if err != nil {
		return filestore.HealthStatus{Backend: filestore.BackendS3, Message: err.Error()}, nil
	}
	return driver.Health(ctx)
}

// auditLog returns the configured auditor, if any. Auditors are installed at
// wiring time, so this is only for honest locking of the field rather than for
// racing writers.
func (s *Service) auditLog() *audit.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.auditor
}

func (s *Service) notifyAfterPrune(ctx context.Context, deletedSnapshotIDs []string) {
	s.mu.RLock()
	callback := s.afterPrune
	s.mu.RUnlock()
	if callback != nil {
		callback(ctx, deletedSnapshotIDs)
	}
}

// CanWrite returns whether storage currently allows writing new files.
func (s *Service) CanWrite() bool {
	return s.gate.CanWrite()
}

// Start begins background monitoring and periodic pruning checks.
//
// Start may only succeed once, and only before Stop. A failed initial load is
// retryable, because the monitoring loop never started; after Stop the service is
// terminal and Start reports errServiceAlreadyStopped.
func (s *Service) Start(ctx context.Context) error {
	if s.stopped.Load() {
		return errServiceAlreadyStopped
	}
	if !s.started.CompareAndSwap(false, true) {
		return errServiceAlreadyStarted
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		// Nothing was started, so let a caller retry once the database is
		// reachable instead of leaving the service permanently unstartable.
		s.started.Store(false)
		return fmt.Errorf("load initial storage config: %w", err)
	}

	s.mu.Lock()
	s.activeMediaDir = cfg.MediaDirectory
	s.activeSpoolDir = cfg.SpoolDirectory
	s.mu.Unlock()

	s.notifyConfigChanged(ctx, cfg)

	// Initial probe and status refresh
	status, err := s.RefreshStatus(ctx)
	if err != nil {
		s.logger.Warn("initial storage status inspection encountered warning", zap.Error(err))
	}
	if status != nil && status.TotalBytes > 0 {
		freeTarget := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024 * 5 / 4
		if freeTarget >= status.TotalBytes {
			s.logger.Warn("storage emergency reserve is too large for this volume; emergency stop may stay latched",
				zap.Uint64("reserve_bytes", freeTarget),
				zap.Uint64("volume_total_bytes", status.TotalBytes),
				zap.Int64("emergency_stop_min_mb", cfg.EmergencyStopMinMB),
			)
		}
	}

	s.wg.Add(1)
	go s.runLoop()
	return nil
}

// Stop cleanly terminates the background monitoring loop and waits for any
// in-flight prune run to finish.
//
// Stop is safe to call concurrently and repeatedly: the loop is signalled once
// and every caller waits on the same drained channel, so a stop that races with
// another stop can no longer double-close the stop channel.
func (s *Service) Stop(ctx context.Context) error {
	s.stopped.Store(true)
	s.stopOnce.Do(func() {
		close(s.stopCh)
		go func() {
			s.wg.Wait()
			close(s.drained)
		}()
	})

	select {
	case <-s.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) runLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(statusRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.tick()
		}
	}
}

// tick refreshes the status once and kicks off a watermark or emergency prune
// when needed.
//
// The prune runs in its own tracked goroutine so Stop can wait for it; the
// cleaner's own in-progress flag makes a duplicate start a no-op error.
func (s *Service) tick() {
	ctx, cancel := context.WithTimeout(context.Background(), tickTimeout)
	status, err := s.RefreshStatus(ctx)
	cancel()
	if err != nil {
		s.logger.Warn("periodic storage refresh failed", zap.Error(err))
		return
	}

	if status.Backend == BackendS3 {
		s.tickRemote(status)
		return
	}

	switch status.Status {
	case StatusWarning, StatusEmergencyStopped:
	case StatusCleaning:
		// An in-flight cleanup is already the throttle. Do not reset the
		// backoff just because the status payload says "cleaning".
		return
	default:
		// Only a healthy state resets the backoff. Resetting on every warning
		// tick would defeat the zero-progress throttle entirely.
		s.resetAutoPruneBackoff()
		return
	}
	if s.cleaner.IsCleaning() {
		return
	}

	s.backoffMu.Lock()
	shouldSkip := time.Now().Before(s.nextAutoPrune)
	s.backoffMu.Unlock()
	if shouldSkip {
		return
	}

	trigger := PruneTriggerWatermark
	if status.Status == StatusEmergencyStopped {
		trigger = PruneTriggerEmergency
	}

	s.startAsyncAutoPrune(status.MediaDirectory, trigger)
}

func (s *Service) tickRemote(status *StorageStatus) {
	if s.cleaner.IsCleaning() || !status.RemoteHealthy || s.currentRemoteStore() == nil || s.currentRemoteProvider() == nil {
		return
	}

	s.backoffMu.Lock()
	shouldSkip := time.Now().Before(s.nextAutoPrune)
	s.backoffMu.Unlock()
	if shouldSkip {
		return
	}

	s.mu.RLock()
	lastRun := s.lastRemotePruneAt
	s.mu.RUnlock()
	if !lastRun.IsZero() && time.Since(lastRun) < remotePruneInterval {
		return
	}

	trigger := PruneTriggerWatermark
	if status.Status == StatusEmergencyStopped {
		trigger = PruneTriggerEmergency
	}
	s.startAsyncAutoPrune(status.MediaDirectory, trigger)
}

func (s *Service) startAsyncAutoPrune(mediaDir string, trigger PruneTrigger) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		pruneCtx, pruneCancel := context.WithTimeout(context.Background(), pruneRunTimeout)
		defer pruneCancel()

		cfg, err := s.repo.GetConfig(pruneCtx)
		if err != nil {
			s.logger.Warn("automatic storage cleanup skipped: config unavailable", zap.Error(err))
			s.recordAutoCleanupFailure()
			return
		}

		summary, err := s.runPruneCycle(
			pruneCtx,
			cfg,
			mediaDir,
			trigger,
		)
		if err != nil {
			if !errors.Is(err, ErrCleanupInProgress) {
				s.logger.Warn("automatic storage cleanup failed", zap.Error(err))
			}
			s.recordAutoCleanupFailure()
			return
		}

		// RefreshStatus must not inherit a prune context that may be canceled
		// right after a long cleanup; otherwise the gate can stay latched until
		// the next tick. Use a fresh bounded context.
		refreshCtx, refreshCancel := context.WithTimeout(context.Background(), tickTimeout)
		if _, err := s.RefreshStatus(refreshCtx); err != nil {
			s.logger.Warn("storage status refresh after automatic cleanup failed", zap.Error(err))
		}
		refreshCancel()

		s.recordAutoCleanupResult(summary)
		s.writeAutoCleanupAudit(summary)
	}()
}

func (s *Service) runPruneCycle(
	ctx context.Context,
	cfg *StorageConfig,
	mediaDir string,
	trigger PruneTrigger,
) (*CleanupSummary, error) {
	if cfg != nil && cfg.Backend == BackendS3 {
		summary, deletedClipIDs, err := s.cleaner.RunRemotePrune(
			ctx,
			RemotePruneRequest{
				Config:   cfg,
				Trigger:  trigger,
				Store:    s.currentRemoteStore(),
				Provider: s.currentRemoteProvider(),
			},
		)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		s.lastRemotePruneAt = time.Now()
		s.mu.Unlock()

		callbackCtx, callbackCancel := context.WithTimeout(context.WithoutCancel(ctx), pruneCallbackTimeout)
		s.notifyRemoteClipsDeleted(callbackCtx, deletedClipIDs)
		s.notifyAfterPrune(callbackCtx, summary.DeletedSnapshotIDs)
		callbackCancel()
		return summary, nil
	}

	summary, err := s.cleaner.RunPrune(
		ctx,
		mediaDir,
		cfg,
		trigger,
	)
	if err != nil {
		return nil, err
	}

	// The DB cascade must not be canceled by a 5-minute prune timeout. The
	// IDs are already durable in the record outbox before deletion is tried.
	callbackCtx, callbackCancel := context.WithTimeout(context.WithoutCancel(ctx), pruneCallbackTimeout)
	s.notifyAfterPrune(callbackCtx, summary.DeletedSnapshotIDs)
	callbackCancel()
	return summary, nil
}

func (s *Service) resetAutoPruneBackoff() {
	s.backoffMu.Lock()
	s.nextAutoPrune = time.Time{}
	s.autoPruneFailCount = 0
	s.backoffMu.Unlock()
}

func (s *Service) recordAutoCleanupFailure() {
	s.backoffMu.Lock()
	s.autoPruneFailCount++
	s.nextAutoPrune = time.Now().Add(autoPruneBackoff(s.autoPruneFailCount))
	s.backoffMu.Unlock()
}

func (s *Service) recordAutoCleanupResult(summary *CleanupSummary) {
	if summary == nil {
		return
	}

	s.backoffMu.Lock()
	defer s.backoffMu.Unlock()

	madePartialProgress := summary.DeletedFiles > 0 &&
		summary.StopReason != PruneStopCandidatesExhausted &&
		summary.StopReason != PruneStopContextCanceled

	switch {
	case summary.StopReason == PruneStopTargetReached,
		summary.StopReason == PruneStopSkippedBelowHigh,
		madePartialProgress:
		s.nextAutoPrune = time.Time{}
		s.autoPruneFailCount = 0
	default:
		s.autoPruneFailCount++
		s.nextAutoPrune = time.Now().Add(autoPruneBackoff(s.autoPruneFailCount))
	}
}

func (s *Service) writeAutoCleanupAudit(summary *CleanupSummary) {
	if summary == nil {
		return
	}
	auditor := s.auditLog()
	if auditor == nil {
		return
	}

	interval := autoCleanupIdleAuditInterval
	if summary.DeletedFiles > 0 {
		interval = autoCleanupAuditInterval
	}
	now := time.Now()

	s.backoffMu.Lock()
	if !s.lastAutoCleanupAudit.IsZero() && now.Sub(s.lastAutoCleanupAudit) < interval {
		s.backoffMu.Unlock()
		return
	}
	s.lastAutoCleanupAudit = now
	s.backoffMu.Unlock()

	detail := fmt.Sprintf(
		"trigger=%s, deleted=%d, freed=%d, protectedFiles=%d, "+
			"protectedBytes=%d, target=%s, stopReason=%s, durationMs=%d",
		summary.TriggerReason,
		summary.DeletedFiles,
		summary.FreedBytes,
		summary.ProtectedFiles,
		summary.ProtectedBytes,
		summary.TargetStatus,
		summary.StopReason,
		summary.DurationMs,
	)
	auditor.RecordSystem(
		ActionStorageAutoCleanup,
		"media_storage",
		detail,
		nil,
	)
}

// GetStatus returns the current storage telemetry snapshot.
func (s *Service) GetStatus(ctx context.Context) (*StorageStatus, error) {
	s.mu.RLock()
	last := s.lastStatus
	s.mu.RUnlock()

	if last != nil && time.Since(last.UpdatedAt) < statusCacheTTL {
		return last, nil
	}
	return s.RefreshStatus(ctx)
}

// GetConfig returns the active persistent storage policy.
func (s *Service) GetConfig(ctx context.Context) (*StorageConfig, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, databaseFailure(err)
	}

	return cfg, nil
}

func autoPruneBackoff(failCount int) time.Duration {
	switch {
	case failCount <= 1:
		return time.Minute
	case failCount == 2:
		return 5 * time.Minute
	default:
		return 15 * time.Minute
	}
}
