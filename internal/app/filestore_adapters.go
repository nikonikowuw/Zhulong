package app

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/recording"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const remotePruneQueryLimit = 500

type filestoreMountInspectorAdapter struct {
	inspector storage.PathInspector
}

func (a *filestoreMountInspectorAdapter) InspectMount(dir string) (filestore.MountTelemetry, error) {
	stats, err := a.inspector.InspectFS(dir)
	if err != nil {
		return filestore.MountTelemetry{}, err
	}
	return filestore.MountTelemetry{
		DeviceID:   stats.DeviceID,
		MountPoint: stats.MountPoint,
		FSType:     stats.FSType,
		IsReadOnly: stats.IsReadOnly,
	}, nil
}

func (a *filestoreMountInspectorAdapter) RootDeviceID() (uint64, error) {
	return a.inspector.GetRootDeviceID()
}

func resolveLocalNamespaceRoots(cfg Config) map[string]string {
	facesDir := strings.TrimSpace(cfg.FacesDir)
	if facesDir == "" {
		facesDir = filepath.Join(cfg.DataDir, "faces")
	}
	mapsDir := filepath.Join(cfg.DataDir, "maps")
	return map[string]string{
		filestore.NamespaceFaces: facesDir,
		filestore.NamespaceMaps:  mapsDir,
	}
}

type fileStoreConfigSyncDeps struct {
	mgr            *filestore.Manager
	svc            *storage.Service
	namespaceRoots map[string]string
	inspector      filestore.MountInspector
	logger         *zap.Logger
}

func makeFileStoreConfigSync(deps fileStoreConfigSyncDeps) func(context.Context, *storage.StorageConfig) {
	return func(ctx context.Context, cfg *storage.StorageConfig) {
		if deps.mgr == nil || cfg == nil {
			return
		}
		localCfg := filestore.LocalConfig{
			RootDir:        cfg.MediaDirectory,
			NamespaceRoots: deps.namespaceRoots,
			Inspector:      deps.inspector,
			Logger:         deps.logger,
		}
		if cfg.Backend != storage.BackendS3 {
			if err := deps.mgr.ConfigureLocal(localCfg); err != nil {
				deps.logger.Warn("configure local filestore driver failed", zap.Error(err))
			}
			return
		}

		s3Cfg, err := deps.svc.GetDecryptedS3Config(ctx)
		if err != nil {
			deps.logger.Debug("deferring S3 filestore switch until cipher initializes", zap.Error(err))
			return
		}
		s3Cfg.Logger = deps.logger
		err = deps.mgr.ConfigureS3WithSpool(
			*s3Cfg,
			localCfg,
			cfg.LocalFallback,
			cfg.SpoolDirectory,
		)
		if err != nil {
			deps.logger.Warn("configure S3 filestore driver failed; retaining previous driver", zap.Error(err))
		}
	}
}

type recordingTargetAdapter struct {
	storage *storage.Service
}

func (a recordingTargetAdapter) RecordingTarget(ctx context.Context) (recording.StorageTarget, error) {
	if a.storage == nil {
		return recording.StorageTarget{Backend: storage.BackendLocal}, nil
	}
	cfg, err := a.storage.GetConfig(ctx)
	if err != nil {
		return recording.StorageTarget{}, err
	}
	if cfg == nil {
		return recording.StorageTarget{Backend: storage.BackendLocal}, nil
	}
	return recording.StorageTarget{
		Backend:  cfg.Backend,
		MediaDir: cfg.MediaDirectory,
		SpoolDir: cfg.SpoolDirectory,
	}, nil
}

type appRemotePruneProvider struct {
	dbProvider database.DBProvider
}

type remoteRecognitionRow struct {
	ID            string
	ImagePath     string
	FaceImagePath string
	CapturedAt    time.Time
}

type remoteCaptureRow struct {
	ID         string
	ImagePath  string
	CropPath   string
	CapturedAt time.Time
}

type remoteClipRow struct {
	ID        string
	Path      string
	SizeBytes int64
	StartedAt time.Time
}

// BuildRemotePrunePlan queries SQLite indexes on storage_backend='s3' records to
// determine expired and over-quota objects without scanning the remote bucket.
func (p *appRemotePruneProvider) BuildRemotePrunePlan(
	ctx context.Context,
	cfg *storage.StorageConfig,
	now time.Time,
) (storage.RemotePrunePlan, error) {
	var plan storage.RemotePrunePlan
	if p == nil || p.dbProvider == nil || cfg == nil {
		return plan, nil
	}
	db := p.dbProvider.DB()
	if db == nil {
		return plan, nil
	}

	snapCutoff := now.AddDate(0, 0, -max(cfg.SnapshotsRetentionDays, 1))
	recCutoff := now.AddDate(0, 0, -max(cfg.RecordingsRetentionDays, 1))

	expiredRecs, err := p.queryRecognitions(ctx, &snapCutoff)
	if err != nil {
		return plan, err
	}
	plan.Expired = append(plan.Expired, expiredRecs...)

	expiredCaps, err := p.queryCaptures(ctx, &snapCutoff)
	if err != nil {
		return plan, err
	}
	plan.Expired = append(plan.Expired, expiredCaps...)

	expiredClips, err := p.queryUploadedClips(ctx, &recCutoff)
	if err != nil {
		return plan, err
	}
	plan.Expired = append(plan.Expired, expiredClips...)

	if cfg.S3 != nil && cfg.S3.QuotaGB > 0 {
		quotaBytes := cfg.S3.QuotaGB * 1024 * 1024 * 1024
		var totalBytes int64
		sumErr := db.WithContext(ctx).
			Table("event_clips").
			Select("COALESCE(SUM(size_bytes), 0)").
			Where("storage_backend = ? AND upload_status = ?", storage.BackendS3, "uploaded").
			Scan(&totalBytes).Error
		if sumErr != nil {
			return plan, sumErr
		}
		if totalBytes > quotaBytes {
			plan.ExcessBytes = totalBytes - quotaBytes
			quotaClips, err := p.queryUploadedClips(ctx, nil)
			if err != nil {
				return plan, err
			}
			plan.QuotaExcess = append(plan.QuotaExcess, quotaClips...)
		}
	}

	return plan, nil
}

func (p *appRemotePruneProvider) queryRecognitions(
	ctx context.Context,
	before *time.Time,
) ([]storage.RemotePruneUnit, error) {
	db := p.dbProvider.DB().WithContext(ctx)
	query := db.Table("recognitions").
		Select("id, image_path, face_image_path, captured_at").
		Where("storage_backend = ?", storage.BackendS3)
	if before != nil {
		query = query.Where("captured_at < ?", *before)
	}
	var rows []remoteRecognitionRow
	if err := query.Order("captured_at ASC, id ASC").Limit(remotePruneQueryLimit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	units := make([]storage.RemotePruneUnit, 0, len(rows))
	for _, r := range rows {
		keys := normalizeObjectKeys(filestore.NamespaceSnapshots, r.ImagePath, r.FaceImagePath)
		if len(keys) == 0 {
			continue
		}
		units = append(units, storage.RemotePruneUnit{
			ID:         "rec:" + r.ID,
			SnapshotID: r.ID,
			Keys:       keys,
			CreatedAt:  r.CapturedAt,
		})
	}
	return units, nil
}

func (p *appRemotePruneProvider) queryCaptures(
	ctx context.Context,
	before *time.Time,
) ([]storage.RemotePruneUnit, error) {
	db := p.dbProvider.DB().WithContext(ctx)
	query := db.Table("captures").
		Select("id, image_path, crop_path, captured_at").
		Where("storage_backend = ?", storage.BackendS3)
	if before != nil {
		query = query.Where("captured_at < ?", *before)
	}
	var rows []remoteCaptureRow
	if err := query.Order("captured_at ASC, id ASC").Limit(remotePruneQueryLimit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	units := make([]storage.RemotePruneUnit, 0, len(rows))
	for _, r := range rows {
		keys := normalizeObjectKeys(filestore.NamespaceSnapshots, r.ImagePath, r.CropPath)
		if len(keys) == 0 {
			continue
		}
		units = append(units, storage.RemotePruneUnit{
			ID:         "cap:" + r.ID,
			SnapshotID: r.ID,
			Keys:       keys,
			CreatedAt:  r.CapturedAt,
		})
	}
	return units, nil
}

func (p *appRemotePruneProvider) queryUploadedClips(
	ctx context.Context,
	before *time.Time,
) ([]storage.RemotePruneUnit, error) {
	db := p.dbProvider.DB().WithContext(ctx)
	query := db.Table("event_clips").
		Select("id, path, size_bytes, started_at").
		Where("storage_backend = ? AND upload_status = ?", storage.BackendS3, "uploaded").
		Where("NOT EXISTS (SELECT 1 FROM alarms WHERE alarms.clip_id = event_clips.id)")
	if before != nil {
		query = query.Where("started_at < ?", *before)
	}
	var rows []remoteClipRow
	if err := query.Order("started_at ASC, id ASC").Limit(remotePruneQueryLimit).Scan(&rows).Error; err != nil {
		return nil, err
	}
	units := make([]storage.RemotePruneUnit, 0, len(rows))
	for _, r := range rows {
		keys := normalizeObjectKeys(filestore.NamespaceRecordings, r.Path)
		if len(keys) == 0 {
			continue
		}
		units = append(units, storage.RemotePruneUnit{
			ID:        "clip:" + r.ID,
			ClipID:    r.ID,
			Keys:      keys,
			SizeBytes: max(r.SizeBytes, int64(0)),
			CreatedAt: r.StartedAt,
		})
	}
	return units, nil
}

func normalizeObjectKeys(defaultNamespace string, rawPaths ...string) []string {
	keys := make([]string, 0, len(rawPaths))
	seen := make(map[string]struct{}, len(rawPaths))
	for _, raw := range rawPaths {
		cleaned := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(raw)), "/")
		if cleaned == "" || cleaned == "." {
			continue
		}
		ns, _ := filestore.SplitKey(cleaned)
		if !strings.Contains(cleaned, "/") || !filestore.IsKnownNamespace(ns) {
			cleaned = path.Join(defaultNamespace, cleaned)
		}
		if _, dup := seen[cleaned]; dup {
			continue
		}
		seen[cleaned] = struct{}{}
		keys = append(keys, cleaned)
	}
	return keys
}

func reconcileRemoteDeletedClips(
	dbProvider database.DBProvider,
	logger *zap.Logger,
) storage.RemoteClipReconciler {
	return func(ctx context.Context, clipIDs []string) error {
		if len(clipIDs) == 0 || dbProvider == nil {
			return nil
		}
		db := dbProvider.DB()
		if db == nil {
			return nil
		}
		var empty remoteClipRow
		res := db.WithContext(ctx).Table("event_clips").Where("id IN ?", clipIDs).Delete(&empty)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			logger.Info("reconciled pruned remote event clips", zap.Int64("deleted_rows", res.RowsAffected))
		}
		return nil
	}
}

type sqliteStorageMigrationSource struct {
	dbProvider     database.DBProvider
	namespaceRoots map[string]string
}

func newSQLiteStorageMigrationSource(
	dbProvider database.DBProvider,
	namespaceRoots map[string]string,
) storage.MigrationSource {
	copied := make(map[string]string, len(namespaceRoots))
	for k, v := range namespaceRoots {
		copied[k] = v
	}
	return &sqliteStorageMigrationSource{
		dbProvider:     dbProvider,
		namespaceRoots: copied,
	}
}

func (s *sqliteStorageMigrationSource) NamespaceRoots() map[string]string {
	out := make(map[string]string, len(s.namespaceRoots))
	for k, v := range s.namespaceRoots {
		out[k] = v
	}
	return out
}

func (s *sqliteStorageMigrationSource) ListLocalAssets(ctx context.Context) ([]storage.MigrationAsset, error) {
	if s.dbProvider == nil {
		return nil, nil
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, nil
	}

	assets := make([]storage.MigrationAsset, 0)
	var err error
	if assets, err = appendLocalSnapshotAssets(ctx, db, assets); err != nil {
		return nil, err
	}
	if assets, err = appendLocalClipAssets(ctx, db, assets); err != nil {
		return nil, err
	}
	if assets, err = appendLocalNamespacedAssets(ctx, db, assets); err != nil {
		return nil, err
	}
	return assets, nil
}

func appendLocalSnapshotAssets(
	ctx context.Context,
	db *gorm.DB,
	out []storage.MigrationAsset,
) ([]storage.MigrationAsset, error) {
	type recRow struct {
		ID            string `gorm:"column:id"`
		ImagePath     string `gorm:"column:image_path"`
		FaceImagePath string `gorm:"column:face_image_path"`
	}
	var recs []recRow
	if err := db.WithContext(ctx).Table("recognitions").
		Select("id, image_path, face_image_path").
		Where("storage_backend = ?", "local").
		Order("captured_at ASC, id ASC").
		Find(&recs).Error; err != nil {
		return nil, fmt.Errorf("query local recognitions: %w", err)
	}
	for _, r := range recs {
		keys := normalizeObjectKeys(filestore.NamespaceSnapshots, r.ImagePath, r.FaceImagePath)
		if len(keys) > 0 {
			out = append(out, storage.MigrationAsset{Table: "recognitions", ID: r.ID, Keys: keys})
		}
	}

	type dualRow struct {
		ID        string `gorm:"column:id"`
		ImagePath string `gorm:"column:image_path"`
		CropPath  string `gorm:"column:crop_path"`
	}
	for _, table := range []string{"captures", "alarms"} {
		var rows []dualRow
		if err := db.WithContext(ctx).Table(table).
			Select("id, image_path, crop_path").
			Where("storage_backend = ?", "local").
			Order("captured_at ASC, id ASC").
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("query local %s: %w", table, err)
		}
		for _, row := range rows {
			keys := normalizeObjectKeys(filestore.NamespaceSnapshots, row.ImagePath, row.CropPath)
			if len(keys) > 0 {
				out = append(out, storage.MigrationAsset{Table: table, ID: row.ID, Keys: keys})
			}
		}
	}
	return out, nil
}

func appendLocalClipAssets(
	ctx context.Context,
	db *gorm.DB,
	out []storage.MigrationAsset,
) ([]storage.MigrationAsset, error) {
	type clipRow struct {
		ID   string `gorm:"column:id"`
		Path string `gorm:"column:path"`
	}
	var clips []clipRow
	if err := db.WithContext(ctx).Table("event_clips").
		Select("id, path").
		Where("storage_backend = ? AND status IN ?", "local", []string{"completed", "truncated"}).
		Order("created_at ASC, id ASC").
		Find(&clips).Error; err != nil {
		return nil, fmt.Errorf("query local event clips: %w", err)
	}
	for _, c := range clips {
		keys := normalizeObjectKeys(filestore.NamespaceRecordings, c.Path)
		if len(keys) > 0 {
			out = append(out, storage.MigrationAsset{Table: "event_clips", ID: c.ID, Keys: keys, IsClip: true})
		}
	}
	return out, nil
}

func appendLocalNamespacedAssets(
	ctx context.Context,
	db *gorm.DB,
	out []storage.MigrationAsset,
) ([]storage.MigrationAsset, error) {
	type pathRow struct {
		ID        string `gorm:"column:id"`
		ImagePath string `gorm:"column:image_path"`
	}
	specs := []struct {
		table     string
		namespace string
	}{
		{table: "person_faces", namespace: filestore.NamespaceFaces},
		{table: "canvas_maps", namespace: filestore.NamespaceMaps},
	}
	for _, spec := range specs {
		var rows []pathRow
		if err := db.WithContext(ctx).Table(spec.table).
			Select("id, image_path").
			Where("storage_backend = ?", "local").
			Order("created_at ASC, id ASC").
			Find(&rows).Error; err != nil {
			return nil, fmt.Errorf("query local %s: %w", spec.table, err)
		}
		for _, row := range rows {
			key := normalizeNamespacedKey(row.ImagePath, spec.namespace)
			if key != "" {
				out = append(out, storage.MigrationAsset{Table: spec.table, ID: row.ID, Keys: []string{key}})
			}
		}
	}
	return out, nil
}

func normalizeNamespacedKey(raw, namespace string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	slash := filepath.ToSlash(trimmed)
	prefix := namespace + "/"
	if strings.HasPrefix(strings.TrimPrefix(slash, "/"), prefix) {
		return strings.TrimPrefix(slash, "/")
	}
	return path.Join(namespace, filepath.Base(trimmed))
}

func (s *sqliteStorageMigrationSource) MarkAssetMigrated(ctx context.Context, asset storage.MigrationAsset) error {
	if s.dbProvider == nil {
		return nil
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil
	}
	switch asset.Table {
	case "recognitions", "captures", "alarms", "person_faces", "canvas_maps":
		return db.WithContext(ctx).
			Table(asset.Table).
			Where("id = ?", asset.ID).
			Update("storage_backend", "s3").Error
	case "event_clips":
		return db.WithContext(ctx).
			Table("event_clips").
			Where("id = ?", asset.ID).
			Updates(map[string]any{
				"storage_backend": "s3",
				"upload_status":   recording.UploadStatusUploaded,
			}).Error
	default:
		return fmt.Errorf("unsupported migration table %q", asset.Table)
	}
}
