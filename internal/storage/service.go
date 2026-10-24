package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
)

// errServiceNotRestartable is returned by Start when the monitoring loop was
// already started (or already stopped) once.
var errServiceNotRestartable = errors.New("storage service already started")

// Service coordinates storage inspection, persistence, hysteresis cleaning, and write safety.
type Service struct {
	mu        sync.RWMutex
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
	wg       sync.WaitGroup
}

// NewService creates and initializes a storage management service.
func NewService(
	repo Repository,
	inspector PathInspector,
	gate *EmergencyGate,
	cleaner *CleanerEngine,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	if inspector == nil {
		inspector = NewDefaultPathInspector()
	}
	if gate == nil {
		gate = NewEmergencyGate(logger)
	}
	if cleaner == nil {
		cleaner = NewCleanerEngine(inspector, logger)
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
			if s.auditor != nil {
				// Background event: no client origin, so it is recorded with an
				// empty IP rather than a faked loopback address.
				s.auditor.RecordSystem(ActionStorageEmergencyStop, "media_storage", reason,
					errEmergencyStop)
			}
		},
		func() {
			if s.auditor != nil {
				s.auditor.RecordSystem(ActionStorageEmergencyResume, "media_storage",
					"storage capacity recovered to safe levels", nil)
			}
		},
	)

	return s
}

// SetAuditor configures the system audit log service.
//
// User-initiated storage operations keep their existing audit path and are out
// of scope for this refactor; only the circuit-breaker callbacks below were
// changed to record as system events.
func (s *Service) SetAuditor(auditor *audit.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditor = auditor
}

func (s *Service) SetAfterPrune(callback func(context.Context)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterPrune = callback
}

func (s *Service) notifyAfterPrune(ctx context.Context) {
	s.mu.RLock()
	callback := s.afterPrune
	s.mu.RUnlock()
	if callback != nil {
		callback(ctx)
	}
}

// errEmergencyStop is the safe summary recorded when the storage circuit
// breaker trips. The detailed reason travels in the detail field.
var errEmergencyStop = errors.New("storage emergency stop engaged")

// CanWrite returns whether storage currently allows writing new files.
func (s *Service) CanWrite() bool {
	return s.gate.CanWrite()
}

// Start begins background monitoring and periodic pruning checks.
//
// Start may only succeed once; after Stop the service is terminal and cannot be
// restarted, because writers and readers already observed its final state.
func (s *Service) Start(ctx context.Context) error {
	if !s.started.CompareAndSwap(false, true) {
		return errServiceNotRestartable
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

		if _, err := s.cleaner.RunPrune(pruneCtx, status.MediaDirectory, cfg, PruneTriggerWatermark); err != nil {
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

	if last != nil && time.Since(last.UpdatedAt) < 5*time.Second {
		return last, nil
	}
	return s.RefreshStatus(ctx)
}

// GetConfig returns the active persistent storage policy.
func (s *Service) GetConfig(ctx context.Context) (*StorageConfig, error) {
	return s.repo.GetConfig(ctx)
}

// RefreshStatus performs an on-demand inspection and updates the cached status.
func (s *Service) RefreshStatus(ctx context.Context) (*StorageStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read storage config: %w", err)
	}
	if s.activeMediaDir == "" {
		s.activeMediaDir = cfg.MediaDirectory
	}

	stats, err := s.inspector.InspectFS(s.activeMediaDir)
	if err != nil {
		// An uninspectable media root is the mount-loss case the gate exists for:
		// block writes instead of only reporting canWrite=false in the DTO.
		s.gate.BlockWrites("storage telemetry unavailable for " + s.activeMediaDir)
		s.lastStatus = &StorageStatus{
			MediaDirectory: s.activeMediaDir,
			Status:         StatusError,
			CanWrite:       s.gate.CanWrite(),
			UpdatedAt:      time.Now().UTC(),
		}
		return s.lastStatus, fmt.Errorf("inspect filesystem %s: %w", s.activeMediaDir, err)
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, fmt.Errorf("get root device ID: %w", err)
	}

	isExternal := stats.DeviceID != rootDev
	// Fallthrough detection: if this volume was registered as an external disk,
	// but suddenly matches root filesystem device ID, the disk dropped/unmounted.
	isDismounted := false
	if s.expectedDeviceID != 0 && s.expectedDeviceID != rootDev && stats.DeviceID == rootDev {
		isDismounted = true
	} else if isExternal && s.expectedDeviceID == 0 {
		// First discovery of external device ID
		s.expectedDeviceID = stats.DeviceID
	}

	health := s.gate.Evaluate(stats, cfg, isDismounted)
	if s.cleaner.IsCleaning() && health != StatusEmergencyStopped && health != StatusError {
		health = StatusCleaning
	}

	var usagePercent int
	if stats.TotalBytes > 0 {
		usagePercent = int((stats.UsedBytes * 100) / stats.TotalBytes)
	}

	// Update breakdown periodically (every 30 seconds to avoid disk I/O thrashing)
	if time.Since(s.lastBreakdownAt) > 30*time.Second {
		bCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		bd, bErr := s.inspector.ScanBreakdown(bCtx, s.activeMediaDir)
		cancel()
		if bErr == nil {
			s.lastBreakdown = bd
			s.lastBreakdownAt = time.Now()
		}
	}

	// Calculate other bytes on same volume
	breakdown := s.lastBreakdown
	mediaBytes := breakdown.RecordingsBytes + breakdown.SnapshotsBytes + breakdown.ExportsBytes
	if stats.UsedBytes > uint64(mediaBytes) {
		breakdown.OtherBytes = int64(stats.UsedBytes) - mediaBytes
	}

	status := &StorageStatus{
		MediaDirectory: s.activeMediaDir,
		MountPoint:     stats.MountPoint,
		FSType:         stats.FSType,
		TotalBytes:     stats.TotalBytes,
		UsedBytes:      stats.UsedBytes,
		FreeBytes:      stats.FreeBytes,
		UsagePercent:   usagePercent,
		Breakdown:      breakdown,
		Status:         health,
		DeviceID:       stats.DeviceID,
		IsExternal:     isExternal,
		CanWrite:       s.gate.CanWrite(),
		UpdatedAt:      time.Now().UTC(),
	}

	s.lastStatus = status
	return status, nil
}

// UpdateConfig updates the storage lifecycle settings and switches to new directory if changed.
func (s *Service) UpdateConfig(ctx context.Context, cfg StorageConfig, username, ip string) (*StorageStatus, error) {
	cleanPath := filepath.Clean(cfg.MediaDirectory)
	cfg.MediaDirectory = cleanPath

	// Pre-test the candidate path writable
	if err := s.inspector.ProbeWritable(cleanPath); err != nil {
		return nil, fmt.Errorf("storage path %s is not writable: %w", cleanPath, err)
	}

	stats, err := s.inspector.InspectFS(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("inspect new storage path: %w", err)
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, fmt.Errorf("inspect root device ID: %w", err)
	}

	saved, err := s.repo.UpdateConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("persist storage config: %w", err)
	}

	s.mu.Lock()
	s.activeMediaDir = saved.MediaDirectory
	if stats.DeviceID != rootDev {
		s.expectedDeviceID = stats.DeviceID
	} else {
		s.expectedDeviceID = 0
	}
	s.mu.Unlock()

	status, err := s.RefreshStatus(ctx)
	if err != nil {
		s.logger.Warn("refresh status after config update failed", zap.Error(err))
	}

	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageUpdateConfig,
			Target:   "media_storage",
			Detail: fmt.Sprintf("mediaDir=%s, recDays=%d, snapDays=%d, expHours=%d, high=%d%%, low=%d%%",
				saved.MediaDirectory, saved.RecordingsRetentionDays, saved.SnapshotsRetentionDays, saved.ExportsRetentionHours, saved.HighWatermarkPercent, saved.LowWatermarkPercent),
			Status: audit.StatusSuccess,
		})
	}

	return status, nil
}

// TestPath validates a target candidate directory for permissions, capacity, and volume identity.
func (s *Service) TestPath(ctx context.Context, path string, username, ip string) (*PathTestResponse, error) {
	cleanPath := filepath.Clean(path)
	res := &PathTestResponse{
		Path: cleanPath,
	}

	writeErr := s.inspector.ProbeWritable(cleanPath)
	if writeErr != nil {
		res.Writable = false
		res.ErrorReason = writeErr.Error()
	} else {
		res.Exists = true
		res.Writable = true
	}

	stats, err := s.inspector.InspectFS(cleanPath)
	if err != nil {
		if res.ErrorReason == "" {
			res.ErrorReason = err.Error()
		}
		return res, nil
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, fmt.Errorf("resolve root device ID: %w", err)
	}
	res.MountPoint = stats.MountPoint
	res.FSType = stats.FSType
	res.TotalBytes = stats.TotalBytes
	res.FreeBytes = stats.FreeBytes
	res.DeviceID = stats.DeviceID
	res.IsExternal = stats.DeviceID != rootDev
	res.IsMount = stats.MountPoint == cleanPath

	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStoragePathTest,
			Target:   cleanPath,
			Detail:   fmt.Sprintf("writable=%v, external=%v, mount=%s", res.Writable, res.IsExternal, res.MountPoint),
			Status:   audit.StatusSuccess,
		})
	}

	return res, nil
}

// TriggerManualCleanup forces an immediate cleanup cycle.
func (s *Service) TriggerManualCleanup(ctx context.Context, username, ip string) (*CleanupSummary, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("read storage config: %w", err)
	}

	s.mu.RLock()
	mediaDir := s.activeMediaDir
	s.mu.RUnlock()

	summary, err := s.cleaner.RunPrune(ctx, mediaDir, cfg, "manual")
	if err != nil {
		return nil, err
	}
	s.notifyAfterPrune(ctx)

	_, _ = s.RefreshStatus(ctx)

	if s.auditor != nil {
		s.auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageManualCleanup,
			Target:   mediaDir,
			Detail:   fmt.Sprintf("deleted=%d, freedBytes=%d, durationMs=%d", summary.DeletedFiles, summary.FreedBytes, summary.DurationMs),
			Status:   audit.StatusSuccess,
		})
	}

	return summary, nil
}
