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
	afterPrune       func(context.Context)

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
		cleaner = NewCleanerEngine(inspector, logger)
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
// after files were deleted. Like SetAuditor it is a setter because the callback
// closes over components that do not exist yet at construction time.
func (s *Service) SetAfterPrune(callback func(context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterPrune = callback
}

// auditLog returns the configured auditor, if any. Auditors are installed at
// wiring time, so this is only for honest locking of the field rather than for
// racing writers.
func (s *Service) auditLog() *audit.Service {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.auditor
}

func (s *Service) notifyAfterPrune(ctx context.Context) {
	s.mu.RLock()
	callback := s.afterPrune
	s.mu.RUnlock()
	if callback != nil {
		callback(ctx)
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
	if _, err := s.RefreshStatus(ctx); err != nil {
		s.logger.Warn("initial storage status inspection encountered warning", zap.Error(err))
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

// tick refreshes the status once and kicks off a watermark prune when needed.
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

	if status.Status != StatusWarning || s.cleaner.IsCleaning() {
		return
	}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()

		pruneCtx, pruneCancel := context.WithTimeout(context.Background(), pruneRunTimeout)
		defer pruneCancel()

		cfg, err := s.repo.GetConfig(pruneCtx)
		if err != nil {
			s.logger.Warn("automatic storage cleanup skipped: config unavailable", zap.Error(err))
			return
		}

		_, err = s.cleaner.RunPrune(pruneCtx, status.MediaDirectory, cfg, PruneTriggerWatermark)
		if err != nil {
			if !errors.Is(err, ErrCleanupInProgress) {
				s.logger.Warn("automatic storage cleanup failed", zap.Error(err))
			}
			return
		}

		s.notifyAfterPrune(pruneCtx)
		if _, err := s.RefreshStatus(pruneCtx); err != nil {
			s.logger.Warn("storage status refresh after automatic cleanup failed", zap.Error(err))
		}
	}()
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
