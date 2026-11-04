package storage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
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
	Logger    *zap.Logger
}

// Service coordinates storage inspection, persistence, hysteresis cleaning, and write safety.
type Service struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	logger    *zap.Logger
	repo      Repository
	inspector PathInspector
	gate      *EmergencyGate
	cleaner   *CleanerEngine
	auditor   *audit.Service

	activeMediaDir   string
	expectedDeviceID uint64
	lastStatus       *StorageStatus
	lastBreakdown    StorageUsageBreakdown
	lastBreakdownAt  time.Time
	afterPrune       func(context.Context, []string)

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
		repo = NewRepository(nil, logger)
	}

	s := &Service{
		logger:    logger,
		repo:      repo,
		inspector: inspector,
		gate:      gate,
		cleaner:   cleaner,
		stopCh:    make(chan struct{}),
		drained:   make(chan struct{}),
	}

	gate.SetCallbacks(
		func(reason string) {
			if auditor := s.auditLog(); auditor != nil {
				// Background event: no client origin, so it is recorded with an
				// empty IP rather than a faked loopback address.
				auditor.RecordSystem(ActionStorageEmergencyStop, "media_storage", reason, errEmergencyStop)
			}
		},
		func() {
			if auditor := s.auditLog(); auditor != nil {
				auditor.RecordSystem(ActionStorageEmergencyResume, "media_storage",
					"storage capacity recovered to safe levels", nil)
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
	s.mu.Unlock()

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

		summary, err := s.cleaner.RunPrune(pruneCtx, status.MediaDirectory, cfg, trigger)
		if err != nil {
			if !errors.Is(err, ErrCleanupInProgress) {
				s.logger.Warn("automatic storage cleanup failed", zap.Error(err))
			}
			s.recordAutoCleanupFailure()
			return
		}

		// The DB cascade must not be canceled by a 5-minute prune timeout. The
		// IDs are already durable in the record outbox before deletion is tried.
		callbackCtx, callbackCancel := context.WithTimeout(context.WithoutCancel(pruneCtx), pruneCallbackTimeout)
		s.notifyAfterPrune(callbackCtx, summary.DeletedSnapshotIDs)
		callbackCancel()

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

	switch {
	case summary.StopReason == PruneStopTargetReached,
		summary.StopReason == PruneStopSkippedBelowHigh,
		summary.DeletedFiles > 0 && summary.StopReason != PruneStopCandidatesExhausted && summary.StopReason != PruneStopContextCanceled:
		s.nextAutoPrune = time.Time{}
		s.autoPruneFailCount = 0
	default:
		s.autoPruneFailCount++
		s.nextAutoPrune = time.Now().Add(autoPruneBackoff(s.autoPruneFailCount))
	}
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

	auditor.RecordSystem(
		ActionStorageAutoCleanup,
		"media_storage",
		fmt.Sprintf(
			"trigger=%s, deleted=%d, freed=%d, protectedFiles=%d, protectedBytes=%d, target=%s, stopReason=%s, durationMs=%d",
			summary.TriggerReason,
			summary.DeletedFiles,
			summary.FreedBytes,
			summary.ProtectedFiles,
			summary.ProtectedBytes,
			summary.TargetStatus,
			summary.StopReason,
			summary.DurationMs,
		),
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
