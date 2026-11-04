package storage

import (
	"context"
	"fmt"
	"time"
)

// refreshSnapshot is the set of cached fields one refresh run needs, read under
// the snapshot lock and used after it is released.
type refreshSnapshot struct {
	mediaDir         string
	expectedDeviceID uint64
	breakdownDue     bool
	breakdown        StorageUsageBreakdown
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

	snap := s.prepareRefresh(cfg.MediaDirectory)

	stats, err := s.inspector.InspectFS(snap.mediaDir)
	if err != nil {
		// An uninspectable media root is the mount-loss case the gate exists for:
		// block writes instead of only reporting canWrite=false in the payload.
		s.gate.BlockWrites("storage telemetry unavailable for " + snap.mediaDir)
		status := &StorageStatus{
			MediaDirectory: snap.mediaDir,
			Status:         StatusError,
			CanWrite:       s.gate.CanWrite(),
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
	isFirstExternalDiscovery := !isDismounted && isExternal && s.expectedDeviceID == 0
	if isFirstExternalDiscovery {
		s.mu.Lock()
		s.expectedDeviceID = stats.DeviceID
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
	if mediaBytes > 0 && stats.UsedBytes > uint64(mediaBytes) {
		breakdown.OtherBytes = int64(stats.UsedBytes) - mediaBytes
	}

	status := &StorageStatus{
		MediaDirectory: snap.mediaDir,
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
		UpdatedAt:      time.Now().UTC(),
	}
	s.storeStatus(status)

	return status, nil
}

// prepareRefresh initializes the active media directory on first use and copies
// the cached fields one refresh run needs. The lock is released before any
// filesystem I/O so a slow probe cannot stall GetStatus readers.
func (s *Service) prepareRefresh(configuredMediaDir string) refreshSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeMediaDir == "" {
		s.activeMediaDir = configuredMediaDir
	}

	return refreshSnapshot{
		mediaDir:         s.activeMediaDir,
		expectedDeviceID: s.expectedDeviceID,
		breakdownDue:     time.Since(s.lastBreakdownAt) > breakdownScanInterval,
		breakdown:        s.lastBreakdown,
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
