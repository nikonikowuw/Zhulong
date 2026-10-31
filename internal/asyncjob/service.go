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
	ctx        context.Context
	cancel     context.CancelFunc
	logger     *zap.Logger
}

// NewService constructs a Service with background workers.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{
		store:    store,
		handlers: make(map[string]JobHandler),
		queue:    make(chan string, 128),
		cancels:  make(map[string]context.CancelFunc),
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger.Named("asyncjob.service"),
	}
	s.wg.Add(1)
	go s.workerLoop()
	return s
}

// RegisterHandler registers an execution handler for a specific job type.
func (s *Service) RegisterHandler(h JobHandler) {
	s.handlersMu.Lock()
	defer s.handlersMu.Unlock()
	s.handlers[h.Type()] = h
	s.logger.Info("registered async job handler", zap.String("type", h.Type()))
}

// Start performs initialization and recovers stale jobs from prior crashes.
func (s *Service) Start(ctx context.Context) error {
	recovered, err := s.store.RecoverOrphanJobs(ctx)
	if err != nil {
		s.logger.Warn("failed to recover orphan async jobs", zap.Error(err))
	} else if recovered > 0 {
		s.logger.Info("recovered orphan async jobs on startup", zap.Int64("count", recovered))
	}
	return nil
}

// Stop gracefully shuts down the job queue and waits for in-flight tasks.
func (s *Service) Stop(ctx context.Context) error {
	s.cancel()
	s.wg.Wait()
	return nil
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
	default:
		s.logger.Error("async job queue is full", zap.String("id", job.ID))
	}

	return job, nil
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
		case <-s.ctx.Done():
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
	store *Store
	jobID string
}

func (u *jobProgressUpdater) Update(ctx context.Context, p ProgressUpdate) error {
	var percent int
	if p.Total > 0 {
		percent = (p.Processed * 100) / p.Total
		if percent > 100 {
			percent = 100
		}
	}
	return u.store.UpdateProgress(ctx, u.jobID, percent, p.Total, p.Processed, p.Succeeded, p.Failed, p.Stage, p.CurrentFile)
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
		_ = s.store.FinishJob(ctx, jobID, StatusFailed, "{}", fmt.Sprintf("No handler for job type %s", job.Type))
		return
	}

	jobCtx, cancel := context.WithCancel(s.ctx)
	s.cancelsMu.Lock()
	s.cancels[jobID] = cancel
	s.cancelsMu.Unlock()

	defer func() {
		cancel()
		s.cancelsMu.Lock()
		delete(s.cancels, jobID)
		s.cancelsMu.Unlock()
	}()

	_ = s.store.UpdateProgress(jobCtx, jobID, 0, job.Total, job.Processed, job.Succeeded, job.Failed, "INITIALIZING", "")
	updater := &jobProgressUpdater{store: s.store, jobID: jobID}

	s.logger.Info("executing async job", zap.String("id", jobID), zap.String("type", job.Type))
	execErr := handler.Execute(jobCtx, job, updater)

	// Use ResultJSON from job memory if populated by handler, or fallback to database
	resultJSON := "{}"
	if job != nil && job.ResultJSON != "" {
		resultJSON = job.ResultJSON
	} else if latestJob, _ := s.store.GetJob(ctx, jobID); latestJob != nil && latestJob.ResultJSON != "" {
		resultJSON = latestJob.ResultJSON
	}

	if errors.Is(jobCtx.Err(), context.Canceled) {
		_ = s.store.FinishJob(ctx, jobID, StatusCancelled, resultJSON, "任务已取消")
		return
	}

	if execErr != nil {
		s.logger.Warn("async job failed", zap.String("id", jobID), zap.Error(execErr))
		_ = s.store.FinishJob(ctx, jobID, StatusFailed, resultJSON, execErr.Error())
	} else {
		s.logger.Info("async job completed successfully", zap.String("id", jobID))
		_ = s.store.FinishJob(ctx, jobID, StatusCompleted, resultJSON, "")
	}
}
