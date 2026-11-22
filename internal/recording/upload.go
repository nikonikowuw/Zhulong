package recording

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
)

const (
	defaultUploadWorkers   = 2
	defaultUploadQueueSize = 64
	uploadJobTimeout       = 2 * time.Minute
	uploadStatusTimeout    = 5 * time.Second
)

type uploadJob struct {
	clipID         string
	relPath        string
	absPath        string
	storageBackend string
	protected      bool
}

// SetFileStore injects the unified filestore backend for remote clip uploads and checks.
func (s *Service) SetFileStore(store filestore.Store) {
	s.depsMu.Lock()
	s.fileStore = store
	s.depsMu.Unlock()
}

// SetTargetProvider injects a provider resolving active local vs spool target directories.
func (s *Service) SetTargetProvider(p StorageTargetProvider) {
	s.depsMu.Lock()
	s.targetProvider = p
	s.depsMu.Unlock()
}

func (s *Service) currentFileStore() filestore.Store {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.fileStore
}

func (s *Service) currentTargetProvider() StorageTargetProvider {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.targetProvider
}

func (s *Service) startUploadWorkers(ctx context.Context) {
	workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))

	s.uploadMu.Lock()
	s.uploadCancel = cancel
	s.uploadCh = make(chan uploadJob, s.uploadQueueSize)
	s.uploadOpen = true
	if s.uploading == nil {
		s.uploading = make(map[string]bool)
	}
	ch := s.uploadCh
	s.uploadMu.Unlock()

	for range s.uploadWorkers {
		s.uploadWG.Add(1)
		go s.runUploadWorker(workerCtx, ch)
	}
}

func (s *Service) drainUploadWorkers(ctx context.Context) error {
	s.uploadMu.Lock()
	if !s.uploadOpen {
		s.uploadMu.Unlock()
		return nil
	}
	s.uploadOpen = false
	close(s.uploadCh)
	cancel := s.uploadCancel
	s.uploadCancel = nil
	s.uploadMu.Unlock()

	done := make(chan struct{})
	go func() {
		s.uploadWG.Wait()
		close(done)
	}()

	select {
	case <-done:
		if cancel != nil {
			cancel()
		}
		return nil
	case <-ctx.Done():
		if cancel != nil {
			cancel()
		}
		<-done
		return ctx.Err()
	}
}

func (s *Service) runUploadWorker(ctx context.Context, ch <-chan uploadJob) {
	defer s.uploadWG.Done()
	for job := range ch {
		s.processUploadJob(ctx, job)
	}
}

func (s *Service) enqueueUpload(job uploadJob) bool {
	if job.clipID == "" || job.relPath == "" || job.absPath == "" {
		return false
	}

	s.uploadMu.Lock()
	if !s.uploadOpen || s.uploading[job.clipID] {
		s.uploadMu.Unlock()
		return false
	}
	select {
	case s.uploadCh <- job:
		s.uploading[job.clipID] = true
		s.uploadMu.Unlock()
		return true
	default:
		s.uploadMu.Unlock()
		s.markUploadFailed(job)
		s.logger.Warn("event clip upload queue full; deferring to recovery",
			zap.String("clip_id", job.clipID),
			zap.String("path", job.relPath),
		)
		return false
	}
}

func (s *Service) finishTrackingUpload(clipID string) {
	s.uploadMu.Lock()
	delete(s.uploading, clipID)
	s.uploadMu.Unlock()
}

func (s *Service) processUploadJob(ctx context.Context, job uploadJob) {
	defer s.finishTrackingUpload(job.clipID)

	store := s.currentFileStore()
	if store == nil || store.Backend() == filestore.BackendLocal {
		s.markUploadFailed(job)
		return
	}

	relKey := filepath.ToSlash(job.relPath)
	uploadCtx, cancel := context.WithTimeout(ctx, uploadJobTimeout)
	defer cancel()

	if _, statErr := os.Stat(job.absPath); os.IsNotExist(statErr) {
		exists, exErr := store.Exists(uploadCtx, relKey)
		if exErr == nil && exists {
			s.markUploadSucceeded(job)
			return
		}
		s.markUploadFailed(job)
		return
	}

	err := filestore.PutLocalFile(
		uploadCtx,
		filestore.LocalFilePutInput{
			Store:      store,
			Key:        relKey,
			SourcePath: job.absPath,
			Options: filestore.PutOptions{
				ContentType: "video/mp4",
				Mode:        0o640,
				Protected:   job.protected,
			},
		},
	)
	if err != nil {
		s.logger.Warn(
			"upload spooled event clip failed",
			zap.String("clip_id", job.clipID),
			zap.String("key", relKey),
			zap.Error(err),
		)
		s.markUploadFailed(job)
		return
	}

	s.markUploadSucceeded(job)
	s.logger.Info(
		"uploaded spooled event clip to remote filestore",
		zap.String("clip_id", job.clipID),
		zap.String("key", relKey),
		zap.String("backend", store.Backend()),
	)
}

func (s *Service) markUploadSucceeded(job uploadJob) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), uploadStatusTimeout)
	defer cancel()
	updErr := s.store.SetUploadStatus(
		ctx,
		job.clipID,
		job.storageBackend,
		UploadStatusUploaded,
	)
	if updErr != nil {
		s.logger.Warn("mark clip upload_status=uploaded failed",
			zap.String("clip_id", job.clipID),
			zap.Error(updErr),
		)
	}
}

func (s *Service) markUploadFailed(job uploadJob) {
	if s.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), uploadStatusTimeout)
	defer cancel()
	updErr := s.store.SetUploadStatus(
		ctx,
		job.clipID,
		job.storageBackend,
		UploadStatusFailed,
	)
	if updErr != nil {
		s.logger.Warn("mark clip upload_status=failed failed",
			zap.String("clip_id", job.clipID),
			zap.Error(updErr),
		)
	}
}

// RecoverPendingUploads re-enqueues pending or previously failed spooled remote
// clips once the remote filestore driver is active (and heals rows already uploaded).
func (s *Service) RecoverPendingUploads(ctx context.Context) error {
	if s.store == nil {
		return nil
	}
	store := s.currentFileStore()
	if store == nil || store.Backend() == filestore.BackendLocal {
		return nil
	}

	target, err := s.resolveTarget(ctx)
	if err != nil {
		return err
	}
	spoolDir := strings.TrimSpace(target.SpoolDir)

	rows, err := s.store.PendingUploadClips(ctx, defaultCleanupBatch)
	if err != nil {
		return err
	}
	for i := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		row := rows[i]
		relKey := filepath.ToSlash(row.Path)

		var absPath string
		if spoolDir != "" {
			absPath = filepath.Join(spoolDir, filepath.FromSlash(row.Path))
		}
		if absPath != "" {
			if absRes, absErr := filepath.Abs(absPath); absErr == nil {
				absPath = absRes
			}
			if _, statErr := os.Stat(absPath); statErr == nil {
				s.enqueueUpload(uploadJob{
					clipID:         row.ID,
					relPath:        row.Path,
					absPath:        absPath,
					storageBackend: row.StorageBackend,
				})
				continue
			}
		}

		// Spool file gone: check whether remote object already completed before crash.
		if exists, exErr := store.Exists(ctx, relKey); exErr == nil && exists {
			updErr := s.store.SetUploadStatus(
				ctx,
				row.ID,
				row.StorageBackend,
				UploadStatusUploaded,
			)
			if updErr != nil {
				s.logger.Warn("heal uploaded clip status failed", zap.String("clip_id", row.ID), zap.Error(updErr))
			}
		}
	}
	return nil
}
