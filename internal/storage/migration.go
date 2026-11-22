package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// MigrationJobType identifies background local-to-remote media migration jobs.
const MigrationJobType = "storage.migrate_remote"

// MigrationAsset describes a single database record and its local object keys to
// copy into the active remote object store.
type MigrationAsset struct {
	Table  string
	ID     string
	Keys   []string
	IsClip bool
}

// MigrationSource lists unmigrated local database records and commits backend
// transitions once all object keys exist in the remote store.
type MigrationSource interface {
	NamespaceRoots() map[string]string
	ListLocalAssets(ctx context.Context) ([]MigrationAsset, error)
	MarkAssetMigrated(ctx context.Context, asset MigrationAsset) error
}

// AsyncJobService abstracts the background job runner consumed by storage.
type AsyncJobService interface {
	SubmitJob(ctx context.Context, jobType string, metadataJSON string) (*asyncjob.Job, error)
	GetJob(ctx context.Context, id string) (*asyncjob.Job, error)
	ListJobs(ctx context.Context, jobType string, limit, offset int) ([]asyncjob.Job, int64, error)
}

// MigrationJobHandler adapts storage migration execution to asyncjob.JobHandler.
type MigrationJobHandler struct {
	service *Service
}

// NewMigrationJobHandler constructs the background migration job handler.
func NewMigrationJobHandler(s *Service) *MigrationJobHandler {
	return &MigrationJobHandler{service: s}
}

// Type returns "storage.migrate_remote".
func (h *MigrationJobHandler) Type() string {
	return MigrationJobType
}

// Execute runs the asynchronous local-to-remote storage migration job.
func (h *MigrationJobHandler) Execute(
	ctx context.Context,
	job *asyncjob.Job,
	updater asyncjob.ProgressUpdater,
) error {
	return h.service.ExecuteMigrationJob(ctx, job, updater)
}

type migrationJobMetadata struct {
	DeleteLocal bool `json:"deleteLocal"`
}

type primaryExister interface {
	ExistsInPrimary(ctx context.Context, key string) (bool, error)
}

type migrationStores struct {
	remote filestore.Store
	local  filestore.Store
	spool  filestore.Store
}

// SetAsyncJobs wires the background job service used for storage migrations.
func (s *Service) SetAsyncJobs(asyncJobs AsyncJobService) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.asyncJobs = asyncJobs
	s.mu.Unlock()
}

// SetMigrationSource configures the SQLite asset enumerator and marker.
func (s *Service) SetMigrationSource(source MigrationSource) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.migrationSource = source
	s.mu.Unlock()
}

// StartMigration queues an asynchronous job migrating historical local media
// assets into the active remote S3 object store.
func (s *Service) StartMigration(
	ctx context.Context,
	req StartMigrationRequest,
	username, ip string,
) (*asyncjob.Job, error) {
	s.mu.RLock()
	asyncJobs := s.asyncJobs
	source := s.migrationSource
	s.mu.RUnlock()
	if asyncJobs == nil || source == nil {
		return nil, migrationUnavailable(errors.New("storage migration dependencies not configured"))
	}

	s.migrateMu.Lock()
	defer s.migrateMu.Unlock()

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("load storage config for migration: %w", err))
	}
	remoteStore := s.currentRemoteStore()
	if cfg.Backend != BackendS3 || remoteStore == nil || remoteStore.Backend() == filestore.BackendLocal {
		return nil, migrationRequiresS3(errors.New("s3 backend must be active before migrating local media"))
	}

	existing, _, err := asyncJobs.ListJobs(
		ctx,
		MigrationJobType,
		10,
		0,
	)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("check active migration jobs: %w", err))
	}
	for _, job := range existing {
		if job.Status.IsActive() {
			return nil, migrationInProgress(fmt.Errorf("migration job %s is active", job.ID))
		}
	}

	metaBytes, err := json.Marshal(migrationJobMetadata{DeleteLocal: req.DeleteLocal})
	if err != nil {
		return nil, fmt.Errorf("encode migration metadata: %w", err)
	}
	job, err := asyncJobs.SubmitJob(ctx, MigrationJobType, string(metaBytes))
	if err != nil {
		return nil, err
	}

	if auditor := s.auditLog(); auditor != nil {
		auditor.Record(audit.Entry{
			Username: username,
			IP:       ip,
			Action:   ActionStorageMigrate,
			Target:   "media_storage",
			Detail:   fmt.Sprintf("jobId=%s, deleteLocal=%v", job.ID, req.DeleteLocal),
			Status:   audit.StatusSuccess,
		})
	}
	return job, nil
}

// GetLatestMigration returns the most recent storage migration job, or nil when none exists.
func (s *Service) GetLatestMigration(ctx context.Context) (*asyncjob.Job, error) {
	s.mu.RLock()
	asyncJobs := s.asyncJobs
	s.mu.RUnlock()
	if asyncJobs == nil {
		return nil, migrationUnavailable(errors.New("async job service not configured"))
	}

	jobs, _, err := asyncJobs.ListJobs(
		ctx,
		MigrationJobType,
		1,
		0,
	)
	if err != nil {
		return nil, databaseFailure(fmt.Errorf("list migration jobs: %w", err))
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	latest := jobs[0]
	return &latest, nil
}

// ExecuteMigrationJob streams each pending local asset to the configured remote
// object store and marks migrated SQLite records as backend='s3'.
func (s *Service) ExecuteMigrationJob(
	ctx context.Context,
	job *asyncjob.Job,
	updater asyncjob.ProgressUpdater,
) error {
	meta := parseMigrationMetadata(job.MetadataJSON)
	s.mu.RLock()
	source := s.migrationSource
	s.mu.RUnlock()
	remoteStore := s.currentRemoteStore()
	if source == nil || remoteStore == nil || remoteStore.Backend() == filestore.BackendLocal {
		return migrationRequiresS3(errors.New("remote s3 store unavailable for migration"))
	}

	cfg, err := s.repo.GetConfig(ctx)
	if err != nil {
		return fmt.Errorf("load storage config: %w", err)
	}
	stores, err := buildMigrationStores(cfg, remoteStore, source.NamespaceRoots())
	if err != nil {
		return migrationFailed(fmt.Errorf("initialize migration storage drivers: %w", err))
	}
	defer func() {
		if closeErr := closeMigrationStores(stores); closeErr != nil && s.logger != nil {
			s.logger.Warn("close migration local stores failed", zap.Error(closeErr))
		}
	}()

	assets, err := source.ListLocalAssets(ctx)
	if err != nil {
		return migrationFailed(fmt.Errorf("list local media assets: %w", err))
	}

	total := len(assets)
	s.reportMigrationProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage:  asyncjob.StageProcessing,
		Status: asyncjob.StatusProcessing,
		Total:  total,
	})
	if total == 0 {
		s.reportMigrationProgress(ctx, updater, asyncjob.ProgressUpdate{
			Stage:  asyncjob.StageCompleted,
			Status: asyncjob.StatusCompleted,
		})
		return nil
	}

	processed, succeeded, failed := s.runMigrationLoop(ctx, migrationLoopInput{
		stores:      stores,
		source:      source,
		assets:      assets,
		deleteLocal: meta.DeleteLocal,
		updater:     updater,
	})
	if err := ctx.Err(); err != nil {
		return err
	}

	s.reportMigrationProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage:     asyncjob.StageCompleted,
		Status:    asyncjob.StatusCompleted,
		Total:     total,
		Processed: processed,
		Succeeded: succeeded,
		Failed:    failed,
	})
	return nil
}

type migrationLoopInput struct {
	stores      migrationStores
	source      MigrationSource
	assets      []MigrationAsset
	deleteLocal bool
	updater     asyncjob.ProgressUpdater
}

func (s *Service) runMigrationLoop(ctx context.Context, in migrationLoopInput) (int, int, int) {
	total := len(in.assets)
	processed := 0
	succeeded := 0
	failed := 0

	for _, asset := range in.assets {
		if ctx.Err() != nil {
			return processed, succeeded, failed
		}
		uploadedKeys, err := migrateSingleAsset(
			ctx,
			in.stores,
			in.source,
			asset,
		)
		processed++
		if err != nil {
			failed++
			s.logger.Warn(
				"migrate local asset to remote failed",
				zap.String("table", asset.Table),
				zap.String("id", asset.ID),
				zap.Error(err),
			)
		} else {
			succeeded++
			if in.deleteLocal {
				s.deleteLocalMigratedKeys(
					ctx,
					in.stores,
					uploadedKeys,
					asset.IsClip,
				)
			}
		}

		currentFile := ""
		if len(asset.Keys) > 0 {
			currentFile = asset.Keys[0]
		}
		s.reportMigrationProgress(ctx, in.updater, asyncjob.ProgressUpdate{
			Stage:       asyncjob.StageProcessing,
			Status:      asyncjob.StatusProcessing,
			Total:       total,
			Processed:   processed,
			Succeeded:   succeeded,
			Failed:      failed,
			CurrentFile: currentFile,
		})
	}
	return processed, succeeded, failed
}

func buildMigrationStores(
	cfg *StorageConfig,
	remote filestore.Store,
	nsRoots map[string]string,
) (migrationStores, error) {
	if strings.TrimSpace(cfg.MediaDirectory) == "" {
		return migrationStores{}, errors.New("media directory is empty for migration")
	}
	localStore := filestore.NewLocalDriver(filestore.LocalConfig{
		RootDir:        cfg.MediaDirectory,
		NamespaceRoots: nsRoots,
	})

	var spoolStore filestore.Store
	if strings.TrimSpace(cfg.SpoolDirectory) != "" && cfg.SpoolDirectory != cfg.MediaDirectory {
		spoolStore = filestore.NewLocalDriver(filestore.LocalConfig{
			RootDir: cfg.SpoolDirectory,
		})
	}

	return migrationStores{
		remote: remote,
		local:  localStore,
		spool:  spoolStore,
	}, nil
}

func closeMigrationStores(stores migrationStores) error {
	var errs []error
	// Only close temporary local/spool drivers created for this job; remote is shared.
	for _, store := range []filestore.Store{stores.local, stores.spool} {
		if closer, ok := store.(io.Closer); ok {
			if err := closer.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func migrateSingleAsset(
	ctx context.Context,
	stores migrationStores,
	source MigrationSource,
	asset MigrationAsset,
) ([]string, error) {
	uploadedKeys := make([]string, 0, len(asset.Keys))
	for _, rawKey := range asset.Keys {
		clean := strings.TrimSpace(rawKey)
		if clean == "" {
			continue
		}
		key, err := filestore.ValidateKey(clean)
		if err != nil {
			return nil, fmt.Errorf("validate key %q: %w", rawKey, err)
		}
		err = ensureRemoteKey(
			ctx,
			stores,
			key,
			asset.IsClip,
		)
		if err != nil {
			return nil, err
		}
		uploadedKeys = append(uploadedKeys, key)
	}
	if err := source.MarkAssetMigrated(ctx, asset); err != nil {
		return nil, fmt.Errorf(
			"mark asset %s/%s remote: %w",
			asset.Table,
			asset.ID,
			err,
		)
	}
	return uploadedKeys, nil
}

func ensureRemoteKey(
	ctx context.Context,
	stores migrationStores,
	key string,
	isClip bool,
) error {
	exists, err := existsInPrimaryStore(ctx, stores.remote, key)
	if err != nil {
		return fmt.Errorf("check primary object %q: %w", key, err)
	}
	if exists {
		return nil
	}

	rc, info, err := openMigrationSourceObject(
		ctx,
		stores,
		key,
		isClip,
	)
	if err != nil {
		return fmt.Errorf("open source object %q: %w", key, err)
	}

	contentType := info.ContentType
	if contentType == "" {
		contentType = filestore.ContentTypeForKey(key)
	}
	putErr := stores.remote.Put(
		ctx,
		key,
		rc,
		filestore.PutOptions{
			Size:        info.SizeBytes,
			ContentType: contentType,
		},
	)
	closeErr := rc.Close()
	if putErr != nil {
		return fmt.Errorf("upload remote object %q: %w", key, putErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close source object %q: %w", key, closeErr)
	}
	return nil
}

func existsInPrimaryStore(ctx context.Context, remote filestore.Store, key string) (bool, error) {
	if pe, ok := remote.(primaryExister); ok {
		return pe.ExistsInPrimary(ctx, key)
	}
	return remote.Exists(ctx, key)
}

func openMigrationSourceObject(
	ctx context.Context,
	stores migrationStores,
	key string,
	isClip bool,
) (filestore.ReadSeekCloser, filestore.ObjectInfo, error) {
	rc, info, err := stores.local.Open(ctx, key)
	if err == nil {
		return rc, info, nil
	}
	if isClip && stores.spool != nil && filestore.IsNotFound(err) {
		return stores.spool.Open(ctx, key)
	}
	return nil, filestore.ObjectInfo{}, err
}

func (s *Service) deleteLocalMigratedKeys(
	ctx context.Context,
	stores migrationStores,
	keys []string,
	isClip bool,
) {
	for _, key := range keys {
		if err := stores.local.Delete(ctx, key); err != nil && !filestore.IsNotFound(err) {
			s.logger.Warn("delete migrated local file failed", zap.String("key", key), zap.Error(err))
		}
		if isClip && stores.spool != nil {
			if err := stores.spool.Delete(ctx, key); err != nil && !filestore.IsNotFound(err) {
				s.logger.Warn("delete migrated spool file failed", zap.String("key", key), zap.Error(err))
			}
		}
	}
}

func (s *Service) reportMigrationProgress(
	ctx context.Context,
	updater asyncjob.ProgressUpdater,
	update asyncjob.ProgressUpdate,
) {
	if updater == nil {
		return
	}
	if err := updater.Update(ctx, update); err != nil {
		s.logger.Debug("update migration job progress failed", zap.Error(err))
	}
}

func parseMigrationMetadata(raw string) migrationJobMetadata {
	if strings.TrimSpace(raw) == "" || raw == "{}" {
		return migrationJobMetadata{}
	}
	var meta migrationJobMetadata
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		return migrationJobMetadata{}
	}
	return meta
}

func newMigrationJobResponse(job *asyncjob.Job, locale string) MigrationJobResponse {
	var finishedAt *time.Time
	if job.FinishedAt != nil {
		utc := job.FinishedAt.UTC()
		finishedAt = &utc
	}
	errorMessage := job.ErrorMessage
	if job.ErrorCode != "" {
		if localized, ok := httputil.LookupMessage(job.ErrorCode, locale); ok && localized != "" {
			errorMessage = localized
		}
	}
	meta := parseMigrationMetadata(job.MetadataJSON)
	return MigrationJobResponse{
		JobID:        job.ID,
		Status:       job.Status,
		Progress:     job.Progress,
		Total:        job.Total,
		Processed:    job.Processed,
		Succeeded:    job.Succeeded,
		Failed:       job.Failed,
		CurrentStage: job.CurrentStage,
		CurrentFile:  job.CurrentFile,
		DeleteLocal:  meta.DeleteLocal,
		ErrorCode:    job.ErrorCode,
		ErrorMessage: errorMessage,
		CreatedAt:    job.CreatedAt.UTC(),
		UpdatedAt:    job.UpdatedAt.UTC(),
		FinishedAt:   finishedAt,
	}
}
