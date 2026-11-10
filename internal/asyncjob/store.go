package asyncjob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"gorm.io/gorm"
)

var (
	// ErrNotFound reports that no job exists for the requested id.
	ErrNotFound = errors.New("async job not found")
	// ErrNotCancellable reports that the job already reached a terminal state.
	ErrNotCancellable = errors.New("async job is not in a cancellable state")
	// ErrNilJob reports a caller mistake instead of storing a partially formed row.
	ErrNilJob = errors.New("job is nil")
)

// Store handles SQLite persistence for system background jobs.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore constructs a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, database.ErrClosed
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, database.ErrClosed
	}
	return db.WithContext(ctx), nil
}

// CreateJob persists a newly submitted job.
func (s *Store) CreateJob(ctx context.Context, job *Job) error {
	if job == nil {
		return ErrNilJob
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	return gorm.G[Job](db).Create(ctx, job)
}

// GetJob fetches a job by ID.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	job, err := gorm.G[Job](db).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &job, nil
}

// ListJobs returns jobs ordered by creation time descending.
//
// COUNT and the page read share one read transaction so total and items come
// from the same snapshot; without it, writes between the two statements make
// the page silently disagree with its own total.
func (s *Store) ListJobs(ctx context.Context, jobType string, limit, offset int) ([]Job, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = httputil.DefaultPageSize
	}
	if limit > httputil.MaxPageSize {
		limit = httputil.MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}

	var (
		list  []Job
		total int64
	)
	txErr := db.Transaction(func(tx *gorm.DB) error {
		// Scopes() with no arguments is a no-op that yields the filtered chain
		// type, which Where() returns and Count()/Find() are methods on.
		g := gorm.G[Job](tx).Scopes()
		if jobType != "" {
			g = g.Where("type = ?", jobType)
		}
		count, countErr := g.Count(ctx, "*")
		if countErr != nil {
			return fmt.Errorf("count jobs: %w", countErr)
		}
		total = count

		page, findErr := g.Order("created_at DESC, id DESC").Limit(limit).Offset(offset).Find(ctx)
		if findErr != nil {
			return findErr
		}
		list = page
		return nil
	})
	if txErr != nil {
		return nil, 0, txErr
	}
	if list == nil {
		list = []Job{}
	}
	return list, total, nil
}

// ClaimJob moves a pending job to PROCESSING.
//
// The conditional update is the point where queue ownership is decided: the
// worker only runs a job after winning it, and a concurrent CancelJob can only
// flip the row while it is still PENDING. Exactly one of the two succeeds.
func (s *Store) ClaimJob(ctx context.Context, id string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	res := db.Model(&Job{}).
		Where("id = ? AND status = ?", id, StatusPending).
		Updates(map[string]any{
			"status":        StatusProcessing,
			"current_stage": string(StageInitializing),
			"current_file":  "",
			"updated_at":    time.Now().UTC(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateProgress updates the live progress telemetry of an active job.
func (s *Store) UpdateProgress(ctx context.Context, id string, progressPercent int, p ProgressUpdate) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	status := p.Status
	if !status.IsActive() {
		status = StatusProcessing
	}
	res := db.Model(&Job{}).
		Where("id = ? AND status IN ?", id, activeStatuses).
		Updates(map[string]any{
			"status":        status,
			"progress":      progressPercent,
			"total":         p.Total,
			"processed":     p.Processed,
			"succeeded":     p.Succeeded,
			"failed":        p.Failed,
			"current_stage": string(p.Stage),
			"current_file":  p.CurrentFile,
			"updated_at":    time.Now().UTC(),
		})
	return res.Error
}

// FinishJob records final completion or failure for an active job.
//
// It reports false when the job already reached a terminal state, which is the
// normal outcome of a cancellation that won the race; callers must not treat
// that as an error, and the stored result is left untouched.
func (s *Store) FinishJob(ctx context.Context, id string, outcome FinishOutcome) (bool, error) {
	if !outcome.Status.IsTerminal() {
		return false, fmt.Errorf("finish job: %q is not a terminal status", outcome.Status)
	}
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status":        outcome.Status,
		"current_file":  "",
		"error_code":    outcome.ErrorCode,
		"error_message": "",
		"updated_at":    now,
		"finished_at":   now,
	}
	// An empty result means "the handler produced no new summary"; keep whatever
	// is already stored instead of wiping a partially written result.
	if outcome.ResultJSON != "" {
		updates["result_json"] = outcome.ResultJSON
	}
	if outcome.Status == StatusCompleted {
		updates["progress"] = 100
		updates["error_code"] = ""
	}
	res := db.Model(&Job{}).
		Where("id = ? AND status IN ?", id, activeStatuses).
		Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// CancelJobRecord flips an active job to CANCELLED without touching its result
// payload, so partial progress already written by the handler survives.
//
// An unknown id yields ErrNotFound; a job that already finished yields
// ErrNotCancellable. Both are decided from the database, not from memory.
func (s *Store) CancelJobRecord(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	res := db.Model(&Job{}).
		Where("id = ? AND status IN ?", id, activeStatuses).
		Updates(map[string]any{
			"status":        StatusCancelled,
			"current_file":  "",
			"current_stage": "",
			"updated_at":    now,
			"finished_at":   now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}

	job, err := gorm.G[Job](db).Select("id", "status").Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	return fmt.Errorf("%w: status %s", ErrNotCancellable, job.Status)
}

// AbandonJob fails an active job that will never execute (queue overflow or
// shutdown) and records why. It reports false when the job already finished.
func (s *Store) AbandonJob(ctx context.Context, id string, errorCode string) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	now := time.Now().UTC()
	res := db.Model(&Job{}).
		Where("id = ? AND status IN ?", id, activeStatuses).
		Updates(map[string]any{
			"status":        StatusFailed,
			"error_code":    errorCode,
			"error_message": "",
			"current_file":  "",
			"current_stage": "",
			"updated_at":    now,
			"finished_at":   now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// RecoverOrphanJobs marks jobs left unfinished by a previous process as FAILED.
// Stale progress fields are cleared so a FAILED row never reports a file it is
// no longer processing.
func (s *Store) RecoverOrphanJobs(ctx context.Context) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	res := db.Model(&Job{}).
		Where("status IN ?", activeStatuses).
		Updates(map[string]any{
			"status":        StatusFailed,
			"error_code":    CodeJobInterrupted,
			"error_message": "",
			"current_file":  "",
			"current_stage": "",
			"updated_at":    now,
			"finished_at":   now,
		})
	return res.RowsAffected, res.Error
}
