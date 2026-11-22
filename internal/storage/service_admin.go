package storage

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

// UpdateConfig updates the storage lifecycle settings and switches backend or directory if changed.
func (s *Service) UpdateConfig(
	ctx context.Context,
	cfg StorageConfig,
	username, ip string,
) (*StorageStatus, error) {
	if strings.TrimSpace(cfg.Backend) == BackendS3 && strings.TrimSpace(cfg.MediaDirectory) == "" {
		if prev, err := s.repo.GetConfig(ctx); err == nil && prev != nil && prev.MediaDirectory != "" {
			cfg.MediaDirectory = prev.MediaDirectory
		}
	}

	// Normalize and reject an out-of-range policy before touching the filesystem:
	// a bad watermark is a client error, not a storage failure.
	if err := ValidateConfig(&cfg); err != nil {
		return nil, invalidStorageConfig(err)
	}

	if cfg.Backend == BackendS3 {
		return s.updateS3Config(
			ctx,
			cfg,
			username,
			ip,
		)
	}
	return s.updateLocalConfig(
		ctx,
		cfg,
		username,
		ip,
	)
}

func (s *Service) updateLocalConfig(
	ctx context.Context,
	cfg StorageConfig,
	username, ip string,
) (*StorageStatus, error) {
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

	if err := validateVolumeEmergencyFloor(cfg, stats); err != nil {
		return nil, invalidStorageConfig(err)
	}

	saved, err := s.repo.UpdateConfig(ctx, cfg)
	if err != nil {
		if errors.Is(err, ErrS3SecretRequired) {
			return nil, invalidStorageConfig(err)
		}
		return nil, databaseFailure(fmt.Errorf("persist storage config: %w", err))
	}

	s.mu.Lock()
	s.activeMediaDir = saved.MediaDirectory
	s.expectedDeviceID = 0 // root filesystem volumes are not dismount-tracked
	if stats.DeviceID != rootDev {
		s.expectedDeviceID = stats.DeviceID
	}
	s.activeSpoolDir = saved.SpoolDirectory
	s.expectedSpoolDeviceID = 0
	s.mu.Unlock()

	s.notifyConfigChanged(ctx, saved)

	status, err := s.RefreshStatus(ctx)
	if err != nil {
		s.logger.Warn("refresh status after config update failed", zap.Error(err))
		if status == nil {
			status = &StorageStatus{
				Backend:        BackendLocal,
				MediaDirectory: saved.MediaDirectory,
				SpoolDirectory: saved.SpoolDirectory,
				Status:         StatusError,
				CanWrite:       s.gate.CanWrite(),
				RemoteHealthy:  true,
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
				"backend=local, mediaDir=%s, recDays=%d, snapDays=%d, expHours=%d, high=%d%%, low=%d%%",
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

func (s *Service) updateS3Config(
	ctx context.Context,
	cfg StorageConfig,
	username, ip string,
) (*StorageStatus, error) {
	spoolDir := cfg.SpoolDirectory
	if err := s.inspector.ProbeWritable(spoolDir); err != nil {
		return nil, invalidStorageConfig(fmt.Errorf("spool path %q is not writable: %w", spoolDir, err))
	}

	stats, err := s.inspector.InspectFS(spoolDir)
	if err != nil {
		return nil, invalidStorageConfig(fmt.Errorf("inspect spool path %q: %w", spoolDir, err))
	}

	rootDev, err := s.inspector.GetRootDeviceID()
	if err != nil {
		return nil, telemetryUnavailable(fmt.Errorf("inspect root device ID: %w", err))
	}

	if err := validateVolumeEmergencyFloor(cfg, stats); err != nil {
		return nil, invalidStorageConfig(err)
	}

	probeCfg, err := s.resolveS3ForProbe(ctx, cfg.S3)
	if err != nil {
		return nil, invalidStorageConfig(redactSecretErr(err, cfg.S3.SecretAccessKey))
	}
	if err := s.s3Probe(ctx, probeCfg); err != nil {
		safeErr := redactSecretErr(err, probeCfg.SecretAccessKey)
		return nil, invalidStorageConfig(fmt.Errorf("s3 connection check failed: %w", safeErr))
	}

	saved, err := s.repo.UpdateConfig(ctx, cfg)
	if err != nil {
		if errors.Is(err, ErrS3SecretRequired) {
			return nil, invalidStorageConfig(err)
		}
		return nil, databaseFailure(fmt.Errorf("persist storage config: %w", err))
	}

	s.mu.Lock()
	s.activeMediaDir = saved.MediaDirectory
	s.activeSpoolDir = saved.SpoolDirectory
	s.expectedSpoolDeviceID = 0
	if stats.DeviceID != rootDev {
		s.expectedSpoolDeviceID = stats.DeviceID
	}
	s.mu.Unlock()

	s.notifyConfigChanged(ctx, saved)

	status, err := s.RefreshStatus(ctx)
	if err != nil {
		s.logger.Warn("refresh status after s3 config update failed", zap.Error(err))
		if status == nil {
			status = &StorageStatus{
				Backend:        BackendS3,
				MediaDirectory: saved.MediaDirectory,
				SpoolDirectory: saved.SpoolDirectory,
				Status:         StatusError,
				CanWrite:       s.gate.CanWrite(),
				UpdatedAt:      time.Now().UTC(),
			}
		}
	}

	if auditor := s.auditLog(); auditor != nil {
		bucket := ""
		if saved.S3 != nil {
			bucket = saved.S3.Bucket
		}
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageUpdateConfig,
			Target:   "media_storage",
			Detail: fmt.Sprintf(
				"backend=s3, bucket=%s, delivery=%s, spoolDir=%s, spoolMaxMb=%d",
				bucket,
				saved.DeliveryMode,
				saved.SpoolDirectory,
				saved.SpoolMaxMB,
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
	mountTarget := cleanPath
	if absPath, err := filepath.Abs(cleanPath); err == nil {
		mountTarget = absPath
	}
	res.IsMount = stats.MountPoint == mountTarget

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

// TestConnection tests connectivity and write access for either local path or S3/MinIO config.
func (s *Service) TestConnection(
	ctx context.Context,
	req ConnectionTestRequest,
	username, ip string,
) (*ConnectionTestResponse, error) {
	backend := strings.ToLower(strings.TrimSpace(req.Backend))
	switch backend {
	case "", BackendLocal:
		if strings.TrimSpace(req.Path) == "" {
			return nil, invalidStorageConfig(errors.New("path is required for local connection test"))
		}
		pathRes, err := s.TestPath(
			ctx,
			req.Path,
			username,
			ip,
		)
		if err != nil {
			return nil, err
		}
		return &ConnectionTestResponse{
			Backend:     BackendLocal,
			Reachable:   pathRes.Exists,
			Writable:    pathRes.Writable,
			ErrorReason: pathRes.ErrorReason,
			MountPoint:  pathRes.MountPoint,
			FSType:      pathRes.FSType,
			TotalBytes:  pathRes.TotalBytes,
			FreeBytes:   pathRes.FreeBytes,
			DeviceID:    pathRes.DeviceID,
			IsExternal:  pathRes.IsExternal,
			IsMount:     pathRes.IsMount,
		}, nil

	case BackendS3:
		if req.S3 == nil {
			return nil, invalidStorageConfig(errors.New("s3 configuration is required"))
		}
		s3Copy := *req.S3
		if err := ValidateS3Config(&s3Copy); err != nil {
			return &ConnectionTestResponse{
				Backend:     BackendS3,
				Reachable:   false,
				Writable:    false,
				ErrorReason: err.Error(),
			}, nil
		}

		probeCfg, err := s.resolveS3ForProbe(ctx, &s3Copy)
		if err != nil {
			return &ConnectionTestResponse{
				Backend:     BackendS3,
				Reachable:   false,
				Writable:    false,
				ErrorReason: sanitizeErrorText(err.Error(), s3Copy.SecretAccessKey),
			}, nil
		}

		res := &ConnectionTestResponse{
			Backend:   BackendS3,
			Reachable: true,
			Writable:  true,
		}
		if probeErr := s.s3Probe(ctx, probeCfg); probeErr != nil {
			res.Reachable = false
			res.Writable = false
			res.ErrorReason = sanitizeErrorText(probeErr.Error(), probeCfg.SecretAccessKey)
		}

		if auditor := s.auditLog(); auditor != nil {
			auditor.Record(audit.Entry{
				Username: username,
				IP:       ip,
				Action:   ActionStorageTestConnection,
				Target:   s3Copy.Bucket,
				Detail: fmt.Sprintf(
					"backend=s3, bucket=%s, reachable=%v, writable=%v",
					s3Copy.Bucket,
					res.Reachable,
					res.Writable,
				),
				Status: audit.StatusSuccess,
			})
		}
		return res, nil

	default:
		return nil, invalidStorageConfig(fmt.Errorf("unsupported storage backend %q", req.Backend))
	}
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
	if mediaDir == "" {
		mediaDir = cfg.MediaDirectory
	}

	summary, err := s.runPruneCycle(
		ctx,
		cfg,
		mediaDir,
		PruneTriggerManual,
	)
	if err != nil {
		if errors.Is(err, ErrCleanupInProgress) {
			return nil, cleanupInProgress(err)
		}

		return nil, cleanupFailed(err)
	}

	refreshCtx, refreshCancel := context.WithTimeout(context.Background(), tickTimeout)
	if _, err := s.RefreshStatus(refreshCtx); err != nil {
		s.logger.Warn("storage status refresh after manual cleanup failed", zap.Error(err))
	}
	refreshCancel()
	s.resetAutoPruneBackoff()

	target := mediaDir
	if cfg.Backend == BackendS3 && cfg.S3 != nil {
		target = "s3://" + cfg.S3.Bucket
	}

	if auditor := s.auditLog(); auditor != nil {
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageManualCleanup,
			Target:   target,
			Detail: fmt.Sprintf(
				"deleted=%d, freedBytes=%d, protectedFiles=%d, protectedBytes=%d, stopReason=%s, durationMs=%d",
				summary.DeletedFiles,
				summary.FreedBytes,
				summary.ProtectedFiles,
				summary.ProtectedBytes,
				summary.StopReason,
				summary.DurationMs,
			),
			Status: audit.StatusSuccess,
		})
	}

	return summary, nil
}

func (s *Service) resolveS3ForProbe(
	ctx context.Context,
	cfg *S3Config,
) (filestore.S3Config, error) {
	if cfg == nil {
		return filestore.S3Config{}, errors.New("s3 configuration is required")
	}
	secret := cfg.SecretAccessKey
	if secret == "" {
		stored, err := s.GetDecryptedS3Config(ctx)
		if err != nil {
			return filestore.S3Config{}, ErrS3SecretRequired
		}
		secret = stored.SecretAccessKey
	}
	return toFilestoreS3Config(cfg, secret), nil
}

func validateVolumeEmergencyFloor(cfg StorageConfig, stats *FSStats) error {
	if stats == nil || stats.TotalBytes == 0 {
		return nil
	}
	freeTarget := uint64(cfg.EmergencyStopMinMB) * 1024 * 1024 * 5 / 4
	if freeTarget >= stats.TotalBytes {
		return fmt.Errorf(
			"emergency stop min size %d MB × 1.25 must be smaller than volume capacity %d bytes",
			cfg.EmergencyStopMinMB,
			stats.TotalBytes,
		)
	}
	return nil
}
