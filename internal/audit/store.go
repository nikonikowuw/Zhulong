package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Store provides persistence operations for audit logs.
type Store struct {
	getDB func() *gorm.DB
}

// NewStore creates a new audit Store.
func NewStore(getDB func() *gorm.DB) *Store {
	return &Store{getDB: getDB}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.getDB == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.getDB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

// Create inserts a single audit log entry.
func (s *Store) Create(ctx context.Context, log *AuditLog) error {
	if log == nil {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	} else {
		log.CreatedAt = log.CreatedAt.UTC()
	}
	return db.Create(log).Error
}

// CreateBatch inserts multiple audit logs in a single transaction.
func (s *Store) CreateBatch(ctx context.Context, logs []*AuditLog) error {
	if len(logs) == 0 {
		return nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, l := range logs {
		if l.CreatedAt.IsZero() {
			l.CreatedAt = now
		} else {
			l.CreatedAt = l.CreatedAt.UTC()
		}
	}
	return db.CreateInBatches(logs, 100).Error
}

// List queries audit logs matching the given filter with pagination.
func (s *Store) List(ctx context.Context, filter Filter) ([]AuditLog, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := db.Model(&AuditLog{})

	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.StartTime != nil {
		query = query.Where("created_at >= ?", filter.StartTime.UTC())
	}
	if filter.EndTime != nil {
		query = query.Where("created_at <= ?", filter.EndTime.UTC())
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	query = query.Order("created_at DESC, id DESC")
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	var items []AuditLog
	if err := query.Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("find audit logs: %w", err)
	}
	if items == nil {
		items = []AuditLog{}
	}

	return items, total, nil
}

// Count returns the total number of audit logs.
func (s *Store) Count(ctx context.Context) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	err = db.Model(&AuditLog{}).Count(&total).Error
	return total, err
}

// Prune deletes the oldest audit records if the total count exceeds maxEntries.
// Returns the number of deleted records.
func (s *Store) Prune(ctx context.Context, maxEntries int) (int64, error) {
	if maxEntries <= 0 {
		return 0, nil
	}

	total, err := s.Count(ctx)
	if err != nil {
		return 0, fmt.Errorf("count for prune: %w", err)
	}

	if total <= int64(maxEntries) {
		return 0, nil
	}

	excess := total - int64(maxEntries)
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	res := db.Exec(
		"DELETE FROM audit_logs WHERE id IN (SELECT id FROM audit_logs ORDER BY created_at ASC, id ASC LIMIT ?)",
		excess,
	)
	if res.Error != nil {
		return 0, fmt.Errorf("prune audit logs: %w", res.Error)
	}

	return res.RowsAffected, nil
}

// Clear removes all audit logs or those created before a specified time.
func (s *Store) Clear(ctx context.Context, before ...time.Time) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	query := db
	if len(before) > 0 && !before[0].IsZero() {
		res := query.Where("created_at < ?", before[0].UTC()).Delete(&AuditLog{})
		return res.RowsAffected, res.Error
	}

	res := query.Exec("DELETE FROM audit_logs")
	return res.RowsAffected, res.Error
}
