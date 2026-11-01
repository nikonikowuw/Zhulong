package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"go.uber.org/zap"
)

// UpdateConfig updates the storage lifecycle settings and switches to new directory if changed.
func (s *Service) UpdateConfig(
	ctx context.Context,
	cfg StorageConfig,
	username, ip string,
) (*StorageStatus, error) {
	// Normalize and reject an out-of-range policy before touching the filesystem:
	// a bad watermark is a client error, not a storage failure.
	if err := ValidateConfig(&cfg); err != nil {
		return nil, invalidStorageConfig(err)
	}
	cleanPath := cfg.MediaDirectory

	if err := s.inspector.ProbeWritable(cleanPath); err != nil {
		return nil, invalidStorageConfig(fmt.Errorf("storage path %q is not writable: %w", cleanPath, err))
	}

	stats, err := s.inspector.InspectFS(cleanPath)
	if err != nil {
		return nil, invalidStorageConfig(fmt.Errorf("inspect new storage path %q: %w", cleanPath, err))
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, telemetryUnavailable(fmt.Errorf("inspect root device ID: %w", err))
	}

	saved, err := s.repo.UpdateConfig(ctx, cfg)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("persist storage config: %w", err))
	}

	s.mu.Lock()
	s.activeMediaDir = saved.MediaDirectory
	s.expectedDeviceID = 0 // root filesystem volumes are not dismount-tracked
	if stats.DeviceID != rootDev {
		s.expectedDeviceID = stats.DeviceID
	}
	s.mu.Unlock()

	status, err := s.RefreshStatus(ctx)
	if err != nil {
		s.logger.Warn("refresh status after config update failed", zap.Error(err))
		if status == nil {
			// The config is persisted; never answer 200 with a null payload just
			// because the follow-up probe could not read the volume.
			status = &StorageStatus{
				MediaDirectory: saved.MediaDirectory,
				Status:         StatusError,
				CanWrite:       s.gate.CanWrite(),
				UpdatedAt:      time.Now().UTC(),
			}
		}
	}

	if auditor := s.auditLog(); auditor != nil {
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageUpdateConfig,
			Target:   "media_storage",
			Detail: fmt.Sprintf(
				"mediaDir=%s, recDays=%d, snapDays=%d, expHours=%d, high=%d%%, low=%d%%",
				saved.MediaDirectory,
				saved.RecordingsRetentionDays,
				saved.SnapshotsRetentionDays,
				saved.ExportsRetentionHours,
				saved.HighWatermarkPercent,
				saved.LowWatermarkPercent,
			),
			Status: audit.StatusSuccess,
		})
	}

	return status, nil
}

// TestPath validates a target candidate directory for permissions, capacity, and volume identity.
func (s *Service) TestPath(ctx context.Context, path, username, ip string) (*PathTestResponse, error) {
	cleanPath := filepath.Clean(path)
	res := &PathTestResponse{
		Path:     cleanPath,
		Exists:   true,
		Writable: true,
	}

	if err := s.inspector.ProbeWritable(cleanPath); err != nil {
		// A failed probe is an answer, not an error: the settings dialog shows the
		// reason next to the path field instead of failing the whole request.
		res.Exists = false
		res.Writable = false
		res.ErrorReason = err.Error()
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
		return nil, telemetryUnavailable(fmt.Errorf("resolve root device ID: %w", err))
	}
	res.MountPoint = stats.MountPoint
	res.FSType = stats.FSType
	res.TotalBytes = stats.TotalBytes
	res.FreeBytes = stats.FreeBytes
	res.DeviceID = stats.DeviceID
	res.IsExternal = stats.DeviceID != rootDev
	res.IsMount = stats.MountPoint == cleanPath

	if auditor := s.auditLog(); auditor != nil {
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStoragePathTest,
			Target:   cleanPath,
			Detail: fmt.Sprintf(
				"writable=%v, external=%v, mount=%s",
				res.Writable,
				res.IsExternal,
				res.MountPoint,
			),
			Status: audit.StatusSuccess,
		})
	}

	return res, nil
}

// TriggerManualCleanup forces an immediate cleanup cycle.
func (s *Service) TriggerManualCleanup(ctx context.Context, username, ip string) (*CleanupSummary, error) {
	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("read storage config: %w", err))
	}

	s.mu.RLock()
	mediaDir := s.activeMediaDir
	s.mu.RUnlock()

	summary, err := s.cleaner.RunPrune(ctx, mediaDir, cfg, PruneTriggerManual)
	if err != nil {
		if errors.Is(err, ErrCleanupInProgress) {
			return nil, cleanupInProgress(err)
		}

		return nil, cleanupFailed(err)
	}
	s.notifyAfterPrune(ctx)

	if _, err := s.RefreshStatus(ctx); err != nil {
		s.logger.Warn("storage status refresh after manual cleanup failed", zap.Error(err))
	}

	if auditor := s.auditLog(); auditor != nil {
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageManualCleanup,
			Target:   mediaDir,
			Detail: fmt.Sprintf(
				"deleted=%d, freedBytes=%d, durationMs=%d",
				summary.DeletedFiles,
				summary.FreedBytes,
				summary.DurationMs,
			),
			Status: audit.StatusSuccess,
		})
	}

	return summary, nil
}
