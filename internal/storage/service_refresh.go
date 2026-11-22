package storage

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
)

// refreshSnapshot is the set of cached fields one refresh run needs, read under
// the snapshot lock and used after it is released.
type refreshSnapshot struct {
	mediaDir              string
	expectedDeviceID      uint64
	spoolDir              string
	expectedSpoolDeviceID uint64
	breakdownDue          bool
	breakdown             StorageUsageBreakdown
}

// RefreshStatus performs an on-demand inspection and updates the cached status.
//
// Refreshes are serialized by refreshMu so concurrent callers do not duplicate
// the expensive scans, while mu only guards the snapshot fields. Holding one
// lock across InspectFS/ScanBreakdown would stall every GetStatus caller for the
// whole probe, and would deadlock any gate listener that reads the status.
func (s *Service) RefreshStatus(ctx context.Context) (*StorageStatus, error) {
	s.refreshMu.Lock()
	defer s.refreshMu.Unlock()

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("read storage config: %w", err))
	}

	if cfg.Backend == BackendS3 {
		return s.refreshS3Status(ctx, cfg)
	}
	return s.refreshLocalStatus(ctx, cfg)
}

func (s *Service) refreshLocalStatus(ctx context.Context, cfg *StorageConfig) (*StorageStatus, error) {
	snap := s.prepareRefresh(cfg.MediaDirectory, cfg.SpoolDirectory)

	stats, err := s.inspector.InspectFS(snap.mediaDir)
	if err != nil {
		// An uninspectable media root is the mount-loss case the gate exists for:
		// block writes instead of only reporting canWrite=false in the payload.
		s.gate.BlockWrites("storage telemetry unavailable for " + snap.mediaDir)
		status := &StorageStatus{
			Backend:        BackendLocal,
			MediaDirectory: snap.mediaDir,
			SpoolDirectory: snap.spoolDir,
			Status:         StatusError,
			CanWrite:       s.gate.CanWrite(),
			RemoteHealthy:  true,
			UpdatedAt:      time.Now().UTC(),
		}
		s.storeStatus(status)
		return status, telemetryUnavailable(fmt.Errorf("inspect filesystem %q: %w", snap.mediaDir, err))
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, telemetryUnavailable(fmt.Errorf("get root device ID: %w", err))
	}

	isExternal := stats.DeviceID != rootDev
	// Fallthrough detection: if this volume was registered as an external disk,
	// but suddenly matches root filesystem device ID, the disk dropped/unmounted.
	wasExternal := snap.expectedDeviceID != 0 && snap.expectedDeviceID != rootDev
	isDismounted := wasExternal && stats.DeviceID == rootDev

	// Remember the identity of a newly discovered external volume so the next
	// probe can tell a dismount apart from a first sighting.
	isFirstExternalDiscovery := !isDismounted && isExternal && snap.expectedDeviceID == 0
	if isFirstExternalDiscovery {
		s.mu.Lock()
		if s.activeMediaDir == snap.mediaDir && s.expectedDeviceID == 0 {
			s.expectedDeviceID = stats.DeviceID
		}
		s.mu.Unlock()
	}

	health := s.gate.Evaluate(stats, cfg, isDismounted)
	isCleanerOverride := s.cleaner.IsCleaning() &&
		health != StatusEmergencyStopped && health != StatusError
	if isCleanerOverride {
		health = StatusCleaning
	}

	breakdown := snap.breakdown
	if snap.breakdownDue {
		scanned, ok := s.scanBreakdown(ctx, snap.mediaDir)
		if ok {
			breakdown = scanned
		}
	}

	// Other bytes are derived, so they must be recomputed on every refresh
	// instead of inheriting the value cached with the last breakdown scan.
	breakdown.OtherBytes = 0
	mediaBytes := breakdown.RecordingsBytes + breakdown.SnapshotsBytes + breakdown.ExportsBytes
	if mediaBytes >= 0 && stats.UsedBytes >= uint64(mediaBytes) && stats.UsedBytes <= math.MaxInt64 {
		breakdown.OtherBytes = int64(stats.UsedBytes) - mediaBytes
	}

	status := &StorageStatus{
		Backend:        BackendLocal,
		MediaDirectory: snap.mediaDir,
		SpoolDirectory: snap.spoolDir,
		MountPoint:     stats.MountPoint,
		FSType:         stats.FSType,
		TotalBytes:     stats.TotalBytes,
		UsedBytes:      stats.UsedBytes,
		FreeBytes:      stats.FreeBytes,
		UsagePercent:   usagePercent(stats),
		Breakdown:      breakdown,
		Status:         health,
		DeviceID:       stats.DeviceID,
		IsExternal:     isExternal,
		CanWrite:       s.gate.CanWrite(),
		RemoteHealthy:  true,
		UpdatedAt:      time.Now().UTC(),
	}
	s.storeStatus(status)

	return status, nil
}

func (s *Service) refreshS3Status(ctx context.Context, cfg *StorageConfig) (*StorageStatus, error) {
	spoolDir := cfg.SpoolDirectory
	if strings.TrimSpace(spoolDir) == "" {
		spoolDir = DefaultSpoolDirectory
	}
	snap := s.prepareRefresh(cfg.MediaDirectory, spoolDir)

	stats, err := s.inspector.InspectFS(snap.spoolDir)
	if err != nil {
		s.gate.BlockWrites("spool telemetry unavailable for " + snap.spoolDir)
		status := &StorageStatus{
			Backend:        BackendS3,
			MediaDirectory: snap.mediaDir,
			SpoolDirectory: snap.spoolDir,
			Status:         StatusError,
			CanWrite:       s.gate.CanWrite(),
			RemoteHealthy:  false,
			RemoteError:    "spool volume unavailable",
			UpdatedAt:      time.Now().UTC(),
		}
		s.storeStatus(status)
		return status, telemetryUnavailable(fmt.Errorf("inspect spool filesystem %q: %w", snap.spoolDir, err))
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, telemetryUnavailable(fmt.Errorf("get root device ID: %w", err))
	}

	isExternal := stats.DeviceID != rootDev
	wasExternal := snap.expectedSpoolDeviceID != 0 && snap.expectedSpoolDeviceID != rootDev
	isDismounted := wasExternal && stats.DeviceID == rootDev

	isFirstExternalDiscovery := !isDismounted && isExternal && snap.expectedSpoolDeviceID == 0
	if isFirstExternalDiscovery {
		s.mu.Lock()
		if s.activeSpoolDir == snap.spoolDir && s.expectedSpoolDeviceID == 0 {
			s.expectedSpoolDeviceID = stats.DeviceID
		}
		s.mu.Unlock()
	}

	scanCtx, cancel := context.WithTimeout(ctx, breakdownScanTimeout)
	spoolUsedBytes, dirErr := dirSize(scanCtx, snap.spoolDir)
	cancel()
	if dirErr != nil {
		spoolUsedBytes = 0
	}

	health := s.gate.EvaluateSpool(
		stats,
		cfg,
		spoolUsedBytes,
		isDismounted,
	)
	isCleanerOverride := s.cleaner.IsCleaning() &&
		health != StatusEmergencyStopped && health != StatusError
	if isCleanerOverride {
		health = StatusCleaning
	}

	remoteHealthy, remoteErr := s.checkRemoteHealth(ctx)

	status := &StorageStatus{
		Backend:        BackendS3,
		MediaDirectory: snap.mediaDir,
		SpoolDirectory: snap.spoolDir,
		SpoolUsedBytes: uint64(max(spoolUsedBytes, 0)),
		SpoolFreeBytes: stats.FreeBytes,
		MountPoint:     stats.MountPoint,
		FSType:         stats.FSType,
		TotalBytes:     stats.TotalBytes,
		UsedBytes:      stats.UsedBytes,
		FreeBytes:      stats.FreeBytes,
		UsagePercent:   usagePercent(stats),
		Status:         health,
		DeviceID:       stats.DeviceID,
		IsExternal:     isExternal,
		CanWrite:       s.gate.CanWrite(),
		RemoteHealthy:  remoteHealthy,
		RemoteError:    remoteErr,
		UpdatedAt:      time.Now().UTC(),
	}
	s.storeStatus(status)

	return status, nil
}

func (s *Service) checkRemoteHealth(ctx context.Context) (bool, string) {
	s3Cfg, err := s.GetDecryptedS3Config(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "not initialized") {
			return false, "storage encryption key initializing"
		}
		return false, "S3 credentials unavailable"
	}

	health, err := s.s3Health(ctx, *s3Cfg)
	if err != nil {
		return false, "S3 health check failed"
	}
	return health.Available, health.Message
}

// prepareRefresh initializes the active media/spool directories on first use and
// copies the cached fields one refresh run needs. The lock is released before any
// filesystem I/O so a slow probe cannot stall GetStatus readers.
func (s *Service) prepareRefresh(configuredMediaDir, configuredSpoolDir string) refreshSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeMediaDir == "" {
		s.activeMediaDir = configuredMediaDir
	}
	if s.activeSpoolDir == "" {
		s.activeSpoolDir = configuredSpoolDir
	}

	return refreshSnapshot{
		mediaDir:              s.activeMediaDir,
		expectedDeviceID:      s.expectedDeviceID,
		spoolDir:              s.activeSpoolDir,
		expectedSpoolDeviceID: s.expectedSpoolDeviceID,
		breakdownDue:          time.Since(s.lastBreakdownAt) > breakdownScanInterval,
		breakdown:             s.lastBreakdown,
	}
}

// storeStatus publishes a fresh snapshot for GetStatus readers.
func (s *Service) storeStatus(status *StorageStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastStatus = status
}

// scanBreakdown refreshes the cached per-category usage breakdown.
//
// It reports false when the scan failed or exceeded its time budget, in which
// case the previously cached breakdown stays in effect.
func (s *Service) scanBreakdown(ctx context.Context, mediaDir string) (StorageUsageBreakdown, bool) {
	scanCtx, cancel := context.WithTimeout(ctx, breakdownScanTimeout)
	defer cancel()

	scanned, err := s.inspector.ScanBreakdown(scanCtx, mediaDir)
	if err != nil {
		return StorageUsageBreakdown{}, false
	}

	s.mu.Lock()
	s.lastBreakdown = scanned
	s.lastBreakdownAt = time.Now()
	s.mu.Unlock()

	return scanned, true
}

func toFilestoreS3Config(cfg *S3Config, secret string) filestore.S3Config {
	return filestore.S3Config{
		Endpoint:           cfg.Endpoint,
		Region:             cfg.Region,
		Bucket:             cfg.Bucket,
		Prefix:             cfg.Prefix,
		AccessKeyID:        cfg.AccessKeyID,
		SecretAccessKey:    secret,
		UsePathStyle:       cfg.UsePathStyle,
		UseTLS:             cfg.UseTLS,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
	}
}
