package asyncjob

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var (
	ErrNotFound       = errors.New("async job not found")
	ErrDatabaseClosed = errors.New("database connection is not available")
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
		return nil, ErrDatabaseClosed
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	return db.WithContext(ctx), nil
}

// CreateJob persists a newly submitted job.
func (s *Store) CreateJob(ctx context.Context, job *Job) error {
	if job == nil {
		return errors.New("job is nil")
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
	return db.Create(job).Error
}

// GetJob fetches a job by ID.
func (s *Store) GetJob(ctx context.Context, id string) (*Job, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var job Job
	if err := db.Where("id = ?", id).First(&job).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &job, nil
}

// ListJobs returns jobs ordered by creation time descending.
func (s *Store) ListJobs(ctx context.Context, jobType string, limit, offset int) ([]Job, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := db.Model(&Job{})
	if jobType != "" {
		query = query.Where("type = ?", jobType)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	if limit <= 0 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}

	list := make([]Job, 0)
	if err := query.Order("created_at DESC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// UpdateProgress updates the live progress telemetry of a job.
func (s *Store) UpdateProgress(ctx context.Context, id string, progressPercent int, p ProgressUpdate) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	updates := map[string]any{
		"status":        StatusProcessing,
		"progress":      progressPercent,
		"total":         p.Total,
		"processed":     p.Processed,
		"succeeded":     p.Succeeded,
		"failed":        p.Failed,
		"current_stage": p.Stage,
		"current_file":  p.CurrentFile,
		"updated_at":    time.Now().UTC(),
	}
	return db.Model(&Job{}).Where("id = ? AND status <> ?", id, StatusCancelled).Updates(updates).Error
}

// FinishJob records final completion or failure for a job.
func (s *Store) FinishJob(ctx context.Context, id string, status JobStatus, resultJSON, errorMsg string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status":        status,
		"current_file":  "",
		"result_json":   resultJSON,
		"error_message": errorMsg,
		"updated_at":    now,
		"finished_at":   now,
	}
	if status == StatusCompleted {
		updates["progress"] = 100
	}
	return db.Model(&Job{}).Where("id = ?", id).Updates(updates).Error
}

// RecoverOrphanJobs marks any unfinished jobs from prior crashed processes as FAILED.
func (s *Store) RecoverOrphanJobs(ctx context.Context) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	res := db.Model(&Job{}).
		Where("status IN ?", []JobStatus{StatusPending, StatusProcessing}).
		Updates(map[string]any{
			"status":        StatusFailed,
			"error_message": "系统重启中断，任务已终止",
			"updated_at":    now,
			"finished_at":   now,
		})
	return res.RowsAffected, res.Error
}
