package asyncjob

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

// Service coordinates background task submission, queuing, and execution.
type Service struct {
	store      *Store
	handlers   map[string]JobHandler
	handlersMu sync.RWMutex
	queue      chan string
	cancels    map[string]context.CancelFunc
	cancelsMu  sync.Mutex
	wg         sync.WaitGroup
	stopCh     chan struct{}
	startOnce  sync.Once
	stopOnce   sync.Once
	logger     *zap.Logger
}

// NewService constructs an unstarted Service instance. Background workers
// are started during Start lifecycle hook.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:    store,
		handlers: make(map[string]JobHandler),
		queue:    make(chan string, 128),
		cancels:  make(map[string]context.CancelFunc),
		stopCh:   make(chan struct{}),
		logger:   logger.Named("asyncjob.service"),
	}
}

// RegisterHandler registers an execution handler for a specific job type.
func (s *Service) RegisterHandler(h JobHandler) {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()
	s.handlers[h.Type()] = h
	s.logger.Info("registered async job handler", zap.String("type", h.Type()))
}

// Start performs initialization, recovers stale jobs from prior crashes, and starts workers.
func (s *Service) Start(ctx context.Context) error {
	recovered, err := s.store.RecoverOrphanJobs(ctx)
	if err != nil {
		s.logger.Warn("failed to recover orphan async jobs", zap.Error(err))
	} else if recovered > 0 {
		s.logger.Info("recovered orphan async jobs on startup", zap.Int64("count", recovered))
	}

	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.workerLoop()
		s.logger.Info("async job worker started")
	})
	return nil
}

// Stop gracefully shuts down the job queue, cancels active jobs, and waits for in-flight tasks.
func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.cancelsMu.Lock()
		for _, cancelFn := range s.cancels {
			if cancelFn != nil {
				cancelFn()
			}
		}
		s.cancelsMu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		s.logger.Info("async job worker stopped gracefully")
		return nil
	case <-ctx.Done():
		s.logger.Warn("async job worker stop timed out", zap.Error(ctx.Err()))
		return ctx.Err()
	}
}

// SubmitJob creates a new job record and pushes it to the worker queue.
func (s *Service) SubmitJob(ctx context.Context, jobType string, metadataJSON string) (*Job, error) {
	s.handlersMu.RLock()
	_, exists := s.handlers[jobType]
	s.handlersMu.RUnlock()
	if !exists {
		return nil, apperr.Invalid("JOB_TYPE_UNSUPPORTED", fmt.Sprintf("Unsupported job type: %s", jobType), nil)
	}

	if metadataJSON == "" {
		metadataJSON = "{}"
	}

	job := &Job{
		ID:           idutil.New(),
		Type:         jobType,
		Status:       StatusPending,
		Progress:     0,
		MetadataJSON: metadataJSON,
		ResultJSON:   "{}",
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}

	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to create async job record", err)
	}

	select {
	case s.queue <- job.ID:
		s.logger.Info("submitted async job", zap.String("id", job.ID), zap.String("type", job.Type))
		return job, nil
	default:
		s.logger.Error("async job queue is full", zap.String("id", job.ID))
		if failErr := s.store.FinishJob(ctx, job.ID, StatusFailed, "{}", "系统任务队列已满，提交失败"); failErr != nil {
			s.logger.Warn("failed to update job status on queue overflow", zap.String("id", job.ID), zap.Error(failErr))
		}
		return nil, apperr.RateLimited("ASYNC_JOB_QUEUE_FULL", "后台任务队列已满，请稍后重试", nil)
	}
}

// GetJob retrieves a job by ID.
func (s *Service) GetJob(ctx context.Context, id string) (*Job, error) {
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("JOB_NOT_FOUND", "Async job not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get async job", err)
	}
	return job, nil
}

// ListJobs retrieves jobs with optional type filtering and pagination.
func (s *Service) ListJobs(ctx context.Context, jobType string, limit, offset int) ([]Job, int64, error) {
	jobs, total, err := s.store.ListJobs(ctx, jobType, limit, offset)
	if err != nil {
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list async jobs", err)
	}
	return jobs, total, nil
}

// CancelJob signals cancellation for a running job.
func (s *Service) CancelJob(ctx context.Context, id string) error {
	s.cancelsMu.Lock()
	cancelFn, running := s.cancels[id]
	s.cancelsMu.Unlock()

	if running && cancelFn != nil {
		cancelFn()
	}

	if err := s.store.FinishJob(ctx, id, StatusCancelled, "{}", "用户手动取消任务"); err != nil {
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to cancel job", err)
	}
	s.logger.Info("cancelled async job", zap.String("id", id))
	return nil
}

func (s *Service) workerLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stopCh:
			return
		case jobID, ok := <-s.queue:
			if !ok {
				return
			}
			s.executeJob(jobID)
		}
	}
}

type jobProgressUpdater struct {
	store  *Store
	jobID  string
	logger *zap.Logger
}

func (u *jobProgressUpdater) Update(ctx context.Context, p ProgressUpdate) error {
	var percent int
	if p.Total > 0 {
		percent = (p.Processed * 100) / p.Total
		if percent > 100 {
			percent = 100
		}
	}
	err := u.store.UpdateProgress(ctx, u.jobID, percent, p)
	if err != nil && u.logger != nil {
		u.logger.Debug("failed to update job progress in store", zap.String("id", u.jobID), zap.Error(err))
	}
	return err
}

func (s *Service) executeJob(jobID string) {
	ctx := context.Background()
	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		s.logger.Error("failed to find queued job", zap.String("id", jobID), zap.Error(err))
		return
	}

	if job.Status == StatusCancelled {
		return
	}

	s.handlersMu.RLock()
	handler, exists := s.handlers[job.Type]
	s.handlersMu.RUnlock()
	if !exists {
		if finishErr := s.store.FinishJob(ctx, jobID, StatusFailed, "{}", fmt.Sprintf("No handler for job type %s", job.Type)); finishErr != nil {
			s.logger.Error("failed to finish job without handler", zap.String("id", jobID), zap.Error(finishErr))
		}
		return
	}

	jobCtx, cancel := context.WithCancel(context.Background())
	s.cancelsMu.Lock()
	s.cancels[jobID] = cancel
	s.cancelsMu.Unlock()

	defer func() {
		cancel()
		s.cancelsMu.Lock()
		delete(s.cancels, jobID)
		s.cancelsMu.Unlock()

		if r := recover(); r != nil {
			s.logger.Error("async job panicked", zap.String("id", jobID), zap.Any("panic", r))
			if finishErr := s.store.FinishJob(ctx, jobID, StatusFailed, "{}", fmt.Sprintf("panic: %v", r)); finishErr != nil {
				s.logger.Error("failed to mark panicked job as failed", zap.String("id", jobID), zap.Error(finishErr))
			}
		}
	}()

	initUpdate := ProgressUpdate{
		Stage:       "INITIALIZING",
		Total:       job.Total,
		Processed:   job.Processed,
		Succeeded:   job.Succeeded,
		Failed:      job.Failed,
		CurrentFile: "",
	}
	if updateErr := s.store.UpdateProgress(jobCtx, jobID, 0, initUpdate); updateErr != nil {
		s.logger.Warn("failed to record initial progress", zap.String("id", jobID), zap.Error(updateErr))
	}

	updater := &jobProgressUpdater{store: s.store, jobID: jobID, logger: s.logger}

	s.logger.Info("executing async job", zap.String("id", jobID), zap.String("type", job.Type))
	execErr := handler.Execute(jobCtx, job, updater)

	// Use ResultJSON from job memory if populated by handler, or fallback to database
	resultJSON := "{}"
	if job != nil && job.ResultJSON != "" {
		resultJSON = job.ResultJSON
	} else if latestJob, _ := s.store.GetJob(ctx, jobID); latestJob != nil && latestJob.ResultJSON != "" {
		resultJSON = latestJob.ResultJSON
	}

	// Check if already cancelled
	latest, getErr := s.store.GetJob(ctx, jobID)
	if getErr == nil && latest.Status == StatusCancelled {
		return
	}

	if errors.Is(jobCtx.Err(), context.Canceled) {
		if finishErr := s.store.FinishJob(ctx, jobID, StatusCancelled, resultJSON, "任务已取消"); finishErr != nil {
			s.logger.Error("failed to mark cancelled job", zap.String("id", jobID), zap.Error(finishErr))
		}
		return
	}

	if execErr != nil {
		s.logger.Warn("async job failed", zap.String("id", jobID), zap.Error(execErr))
		if finishErr := s.store.FinishJob(ctx, jobID, StatusFailed, resultJSON, execErr.Error()); finishErr != nil {
			s.logger.Error("failed to mark failed job", zap.String("id", jobID), zap.Error(finishErr))
		}
	} else {
		s.logger.Info("async job completed successfully", zap.String("id", jobID))
		if finishErr := s.store.FinishJob(ctx, jobID, StatusCompleted, resultJSON, ""); finishErr != nil {
			s.logger.Error("failed to mark completed job", zap.String("id", jobID), zap.Error(finishErr))
		}
	}
}
