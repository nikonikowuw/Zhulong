package storage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"go.uber.org/zap"
)

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

	stopCh chan struct{}
	doneCh chan struct{}
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
		doneCh:    make(chan struct{}),
	}

	gate.SetCallbacks(
		func(reason string) {
			if s.auditor != nil {
				s.auditor.Record(audit.Entry{
					Action: ActionStorageEmergencyStop,
					Target: "media_storage",
					Detail: reason,
					Status: audit.StatusFailed,
				})
			}
		},
		func() {
			if s.auditor != nil {
				s.auditor.Record(audit.Entry{
					Action: ActionStorageEmergencyResume,
					Target: "media_storage",
					Detail: "storage capacity recovered to safe levels",
					Status: audit.StatusSuccess,
				})
			}
		},
	)

	return s
}

// SetAuditor configures the system audit log service.
func (s *Service) SetAuditor(auditor *audit.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auditor = auditor
}

// CanWrite returns whether storage currently allows writing new files.
func (s *Service) CanWrite() bool {
	return s.gate.CanWrite()
}

// Start begins background monitoring and periodic pruning checks.
func (s *Service) Start(ctx context.Context) error {
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

	go s.runLoop()
	return nil
}

// Stop cleanly terminates the background monitoring loop.
func (s *Service) Stop(ctx context.Context) error {
	select {
	case <-s.stopCh:
		return nil
	default:
		close(s.stopCh)
	}

	select {
	case <-s.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) runLoop() {
	defer close(s.doneCh)

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			status, err := s.RefreshStatus(ctx)
			cancel()
			if err != nil {
				s.logger.Warn("periodic storage refresh failed", zap.Error(err))
				continue
			}

			// Check if watermark cleaner should trigger
			if status.Status == StatusWarning && !s.cleaner.IsCleaning() {
				go func() {
					cCtx, cCancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cCancel()
					cfg, err := s.repo.GetConfig(cCtx)
					if err != nil {
						return
					}
					_, _ = s.cleaner.RunPrune(cCtx, status.MediaDirectory, cfg, "watermark")
					_, _ = s.RefreshStatus(cCtx)
				}()
			}
		}
	}
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
		s.lastStatus = &StorageStatus{
			MediaDirectory: s.activeMediaDir,
			Status:         StatusError,
			CanWrite:       false,
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

	rootDev, _ := s.inspector.GetRootDeviceID()
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
