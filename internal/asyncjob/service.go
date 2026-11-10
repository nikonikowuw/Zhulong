package asyncjob

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	// defaultQueueDepth bounds how many submitted jobs may wait for the single
	// worker. Overflow is rejected with ASYNC_JOB_QUEUE_FULL instead of growing
	// memory without limit.
	defaultQueueDepth = 128
	// databaseTimeout bounds every persistence call made by the worker. Without
	// it a stuck SQLite writer could pin a worker (or shutdown) forever.
	databaseTimeout = 10 * time.Second
)

// JobStore is the persistence contract the service consumes. *Store implements
// it; declaring it here keeps the service independent of the GORM details.
type JobStore interface {
	CreateJob(ctx context.Context, job *Job) error
	GetJob(ctx context.Context, id string) (*Job, error)
	ListJobs(ctx context.Context, jobType string, limit, offset int) ([]Job, int64, error)
	ClaimJob(ctx context.Context, id string) (bool, error)
	UpdateProgress(ctx context.Context, id string, progressPercent int, p ProgressUpdate) error
	FinishJob(ctx context.Context, id string, outcome FinishOutcome) (bool, error)
	CancelJobRecord(ctx context.Context, id string) error
	AbandonJob(ctx context.Context, id string, errorCode string) (bool, error)
	RecoverOrphanJobs(ctx context.Context) (int64, error)
}

// Service coordinates background task submission, queuing, and execution.
type Service struct {
	store      JobStore
	handlers   map[string]JobHandler
	handlersMu sync.RWMutex
	queue      chan string
	cancels    map[string]context.CancelFunc
	cancelsMu  sync.Mutex
	wg         sync.WaitGroup
	stopCh     chan struct{}
	done       chan struct{}
	waitOnce   sync.Once
	submitMu   sync.Mutex
	startOnce  sync.Once
	stopOnce   sync.Once
	stopping   atomic.Bool
	logger     *zap.Logger
}

// NewService constructs an unstarted Service instance. Background workers are
// started during the Start lifecycle hook.
//
// The logger must not be nil: it arrives through the Fx graph, which fails
// assembly when it cannot supply one. Substituting a Nop logger here would hide
// that wiring bug instead of surfacing it.
func NewService(store JobStore, logger *zap.Logger) *Service {
	return &Service{
		store:    store,
		handlers: make(map[string]JobHandler),
		queue:    make(chan string, defaultQueueDepth),
		cancels:  make(map[string]context.CancelFunc),
		stopCh:   make(chan struct{}),
		done:     make(chan struct{}),
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

// Start performs initialization, recovers stale jobs from prior crashes, and
// starts the worker. Recovery failures are returned instead of swallowed: a
// host that cannot reconcile its job ledger must report a degraded start.
func (s *Service) Start(ctx context.Context) error {
	if s.stopping.Load() {
		return errors.New("async job service is already stopped")
	}

	recovered, err := s.store.RecoverOrphanJobs(ctx)
	if err != nil {
		return fmt.Errorf("recover orphan async jobs: %w", err)
	}
	if recovered > 0 {
		s.logger.Info("recovered orphan async jobs on startup", zap.Int64("count", recovered))
	}

	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.workerLoop()
		s.logger.Info("async job worker started")
	})
	return nil
}

// Stop gracefully shuts down the job queue, cancels active jobs, and waits for
// in-flight tasks. Jobs that never started are failed with JOB_SHUTDOWN rather
// than left PENDING for the next boot to misreport as a crash.
func (s *Service) Stop(ctx context.Context) error {
	s.stopOnce.Do(func() {
		// Submissions and the shutdown flag share one lock: a job is either fully
		// enqueued before the drain can run, or it is rejected. Without this the
		// queue push could land after the drain and leave a PENDING orphan.
		s.submitMu.Lock()
		s.stopping.Store(true)
		close(s.stopCh)
		s.submitMu.Unlock()

		s.cancelActiveJobs()
		s.drainQueue()
	})

	s.waitOnce.Do(func() {
		go func() {
			s.wg.Wait()
			close(s.done)
		}()
	})

	select {
	case <-s.done:
		s.logger.Info("async job worker stopped gracefully")
		return nil
	case <-ctx.Done():
		s.logger.Warn("async job worker stop timed out", zap.Error(ctx.Err()))
		return ctx.Err()
	}
}

func (s *Service) cancelActiveJobs() {
	s.cancelsMu.Lock()
	defer s.cancelsMu.Unlock()
	for _, cancelFn := range s.cancels {
		if cancelFn != nil {
			cancelFn()
		}
	}
}

// drainQueue fails whatever is still waiting so shutdown leaves no PENDING rows
// behind. The worker performs the same abandonment for an id it dequeues after
// the flag flips, so whichever path wins the race is safe: AbandonJob only
// updates active rows.
func (s *Service) drainQueue() {
	for {
		select {
		case jobID := <-s.queue:
			s.abandon(jobID, CodeJobShutdown, "service stopped before the job started")
		default:
			return
		}
	}
}

// SubmitJob creates a new job record and pushes it to the worker queue.
func (s *Service) SubmitJob(ctx context.Context, jobType string, metadataJSON string) (*Job, error) {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()

	if s.stopping.Load() {
		return nil, apperr.Unavailable(CodeJobServiceStopped, "Background job service is shutting down", nil)
	}

	s.handlersMu.RLock()
	_, exists := s.handlers[jobType]
	s.handlersMu.RUnlock()
	if !exists {
		return nil, apperr.Invalid(CodeJobTypeUnsupported, "Unsupported job type", nil)
	}

	if metadataJSON == "" {
		metadataJSON = "{}"
	}

	now := time.Now().UTC()
	job := &Job{
		ID:           idutil.New(),
		Type:         jobType,
		Status:       StatusPending,
		Progress:     0,
		MetadataJSON: metadataJSON,
		ResultJSON:   "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, apperr.Internal("DATABASE_ERROR", "Failed to create async job record", err)
	}

	select {
	case s.queue <- job.ID:
		s.logger.Info("submitted async job", zap.String("id", job.ID), zap.String("type", job.Type))
		return job, nil
	default:
		s.logger.Error("async job queue is full", zap.String("id", job.ID))
		s.abandon(job.ID, CodeJobQueueFull, "job queue is full")
		return nil, apperr.RateLimited(CodeJobQueueFull, "Async job queue is full", nil)
	}
}

// GetJob retrieves a job by ID.
func (s *Service) GetJob(ctx context.Context, id string) (*Job, error) {
	job, err := s.store.GetJob(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound(CodeJobNotFound, "Async job not found", err)
		}
		return nil, apperr.Internal("DATABASE_ERROR", "Failed to get async job", err)
	}
	return job, nil
}

// ListJobs retrieves jobs with optional type filtering and pagination.
func (s *Service) ListJobs(ctx context.Context, jobType string, limit, offset int) ([]Job, int64, error) {
	jobs, total, err := s.store.ListJobs(ctx, jobType, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal("DATABASE_ERROR", "Failed to list async jobs", err)
	}
	return jobs, total, nil
}

// CancelJob signals cancellation for a running job and marks it CANCELLED.
//
// A running job receives a real context cancellation. A queued job is flipped
// to CANCELLED before the worker claims it, so the worker skips execution and
// releases the handler's artifacts through JobAborter. Unknown ids and jobs
// that already finished are rejected instead of silently acknowledged.
func (s *Service) CancelJob(ctx context.Context, id string) error {
	s.cancelsMu.Lock()
	cancelFn, running := s.cancels[id]
	s.cancelsMu.Unlock()
	if running && cancelFn != nil {
		cancelFn()
	}

	if err := s.store.CancelJobRecord(ctx, id); err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			return apperr.NotFound(CodeJobNotFound, "Async job not found", err)
		case errors.Is(err, ErrNotCancellable):
			return apperr.Conflict(CodeJobNotCancellable, "Async job has already finished", err)
		default:
			return apperr.Internal("DATABASE_ERROR", "Failed to cancel async job", err)
		}
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
		case jobID := <-s.queue:
			if s.stopping.Load() {
				s.abandon(jobID, CodeJobShutdown, "queue drained during shutdown")
				return
			}
			s.executeJob(jobID)
		}
	}
}

type jobProgressUpdater struct {
	store  JobStore
	jobID  string
	logger *zap.Logger
}

func (u *jobProgressUpdater) Update(ctx context.Context, p ProgressUpdate) error {
	var percent int
	if p.Total > 0 {
		percent = min(max((p.Processed*100)/p.Total, 0), 100)
	}
	dbCtx, cancel := context.WithTimeout(ctx, databaseTimeout)
	defer cancel()
	if err := u.store.UpdateProgress(
		dbCtx,
		u.jobID,
		percent,
		p,
	); err != nil {
		u.logger.Debug("failed to update job progress in store", zap.String("id", u.jobID), zap.Error(err))
		return err
	}
	return nil
}

// executeJob runs one claimed job.
//
// The cancellation hook is installed before the claim on purpose: whichever of
// "cancel" and "claim" lands first in the database decides the outcome, and a
// cancel that arrives in between still reaches the context the handler gets.
func (s *Service) executeJob(jobID string) {
	jobCtx, cancel := context.WithCancel(context.Background())
	s.cancelsMu.Lock()
	s.cancels[jobID] = cancel
	s.cancelsMu.Unlock()

	defer func() {
		cancel()
		s.cancelsMu.Lock()
		delete(s.cancels, jobID)
		s.cancelsMu.Unlock()
		if recovered := recover(); recovered != nil {
			s.logger.Error("async job panicked", zap.String("id", jobID), zap.Any("panic", recovered))
			s.finish(jobID, FinishOutcome{
				Status:    StatusFailed,
				ErrorCode: CodeJobPanicked,
			})
		}
	}()

	claimCtx, cancelClaim := context.WithTimeout(jobCtx, databaseTimeout)
	claimed, claimErr := s.store.ClaimJob(claimCtx, jobID)
	cancelClaim()
	if claimErr != nil {
		s.logger.Error("failed to claim queued job", zap.String("id", jobID), zap.Error(claimErr))
		return
	}
	if !claimed {
		// Cancelled (or finished elsewhere) before we owned it. The work must not
		// run, but the handler may still own temp directories to release.
		s.abortNeverRun(jobID, "cancelled before execution")
		return
	}

	readCtx, cancelRead := context.WithTimeout(jobCtx, databaseTimeout)
	job, loadErr := s.store.GetJob(readCtx, jobID)
	cancelRead()
	if loadErr != nil {
		s.logger.Error("failed to load claimed job", zap.String("id", jobID), zap.Error(loadErr))
		s.finish(jobID, FinishOutcome{
			Status:    StatusFailed,
			ErrorCode: CodeJobExecutionFailed,
		})
		return
	}

	// A cancellation that landed between the claim and this read still has to be
	// honoured: the handler must not start side effects we already promised to stop.
	if job.Status == StatusCancelled || jobCtx.Err() != nil {
		if s.stopping.Load() && job.Status != StatusCancelled {
			// Shutdown interrupted a job that never got to run. Nothing else will
			// write its terminal state, so fail it here with the honest reason.
			s.abandon(jobID, CodeJobShutdown, "service stopped before the handler started")
		} else {
			// A user cancellation is being written by CancelJob; the handler still
			// must not run, and its artifacts are released now.
			s.abortNeverRun(jobID, "cancelled before the handler started")
		}
		return
	}

	s.handlersMu.RLock()
	handler, exists := s.handlers[job.Type]
	s.handlersMu.RUnlock()
	if !exists {
		s.finish(jobID, FinishOutcome{
			Status:    StatusFailed,
			ErrorCode: CodeJobTypeUnsupported,
		})
		return
	}

	s.logger.Info("executing async job", zap.String("id", jobID), zap.String("type", job.Type))
	execErr := handler.Execute(jobCtx, job, &jobProgressUpdater{store: s.store, jobID: jobID, logger: s.logger})

	switch {
	case execErr == nil:
		s.logger.Info("async job completed successfully", zap.String("id", jobID))
		s.finish(jobID, FinishOutcome{
			Status:     StatusCompleted,
			ResultJSON: job.ResultJSON,
		})
	case errors.Is(execErr, context.Canceled) && s.stopping.Load():
		s.logger.Warn("async job interrupted by shutdown", zap.String("id", jobID))
		s.finish(jobID, FinishOutcome{
			Status:     StatusFailed,
			ResultJSON: job.ResultJSON,
			ErrorCode:  CodeJobShutdown,
		})
	default:
		// The raw cause belongs to the server log; the job row stores a stable code.
		s.logger.Warn("async job failed", zap.String("id", jobID), zap.Error(execErr))
		s.finish(jobID, FinishOutcome{
			Status:     StatusFailed,
			ResultJSON: job.ResultJSON,
			ErrorCode:  CodeJobExecutionFailed,
		})
	}
}

// finish writes a terminal state with its own bounded context. It deliberately
// does not reuse the job context: finalization happens after cancellation and
// panics too, when that context is already done.
func (s *Service) finish(jobID string, outcome FinishOutcome) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseTimeout)
	defer cancel()

	updated, err := s.store.FinishJob(ctx, jobID, outcome)
	if err != nil {
		s.logger.Error("failed to finalize async job", zap.String("id", jobID), zap.Error(err))
		return
	}
	if !updated {
		s.logger.Info("async job already reached a terminal state; keeping its result", zap.String("id", jobID))
	}
}

// abandon fails a job that will never execute and releases its side artifacts.
func (s *Service) abandon(jobID, errorCode, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseTimeout)
	failed, err := s.store.AbandonJob(ctx, jobID, errorCode)
	cancel()
	if err != nil {
		s.logger.Warn("failed to abandon async job", zap.String("id", jobID), zap.Error(err))
	} else if failed {
		s.logger.Info(
			"abandoned async job that never started",
			zap.String("id", jobID),
			zap.String("reason", reason),
		)
	}
	s.abortNeverRun(jobID, reason)
}

// abortNeverRun lets a handler release artifacts for a job that will not run.
func (s *Service) abortNeverRun(jobID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), databaseTimeout)
	defer cancel()

	job, err := s.store.GetJob(ctx, jobID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.logger.Warn("failed to load abandoned job for cleanup", zap.String("id", jobID), zap.Error(err))
		}
		return
	}

	s.handlersMu.RLock()
	handler := s.handlers[job.Type]
	s.handlersMu.RUnlock()

	aborter, ok := handler.(JobAborter)
	if !ok {
		return
	}
	if err := aborter.Abort(ctx, job); err != nil {
		s.logger.Warn(
			"failed to release artifacts of a job that never ran",
			zap.String("id", jobID),
			zap.String("reason", reason),
			zap.Error(err),
		)
	}
}
