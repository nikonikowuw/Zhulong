package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

// pruneAuditLogsSQL deletes the oldest rows by id. The nested subquery is
// required because SQLite only accepts DELETE ... LIMIT when built with
// SQLITE_ENABLE_UPDATE_DELETE_LIMIT, which is not a portable assumption.
const pruneAuditLogsSQL = "DELETE FROM audit_logs WHERE id IN (" +
	"SELECT id FROM audit_logs WHERE action != ? ORDER BY created_at ASC, id ASC LIMIT ?)"

// Store provides persistence operations for audit logs.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new audit Store.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
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
	log.CreatedAt = normalizeCreatedAt(log.CreatedAt, time.Now().UTC())
	if err := db.Create(log).Error; err != nil {
		return fmt.Errorf("create audit log: %w", err)
	}
	return nil
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
		l.CreatedAt = normalizeCreatedAt(l.CreatedAt, now)
	}
	if err := db.CreateInBatches(logs, 100).Error; err != nil {
		return fmt.Errorf("create audit log batch: %w", err)
	}
	return nil
}

// normalizeCreatedAt returns a UTC creation time, substituting now for a zero
// value so every persisted row carries an explicit timestamp.
func normalizeCreatedAt(createdAt, now time.Time) time.Time {
	if createdAt.IsZero() {
		return now
	}
	return createdAt.UTC()
}

// List queries audit logs matching the given filter with pagination.
func (s *Store) List(ctx context.Context, filter Filter) ([]AuditLog, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}
	query := db.Model(&AuditLog{})

	if len(filter.Actions) > 0 {
		query = query.Where("action IN ?", filter.Actions)
	} else if filter.Action != "" {
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
	if err := db.Model(&AuditLog{}).Count(&total).Error; err != nil {
		return 0, fmt.Errorf("count audit logs: %w", err)
	}
	return total, nil
}

// Prune deletes the oldest audit records if the prunable count exceeds
// maxEntries. Returns the number of deleted records.
//
// Records with ActionAuditClear are exempt: they are the audit trail's own
// proof of clearing, so capacity trimming must never remove them. Without the
// exemption the "previous clear records survive" guarantee fails as soon as
// the table grows past maxEntries. Because they are exempt, maxEntries bounds
// the prunable rows only, not the table size.
func (s *Store) Prune(ctx context.Context, maxEntries int) (int64, error) {
	if maxEntries <= 0 {
		return 0, nil
	}

	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}

	// Only prunable rows count against maxEntries. Counting every row would
	// include the exempt audit.clear history, so the excess could exceed the
	// number of deletable rows and the table would stay above the limit for
	// good once clear records dominate.
	var prunable int64
	if err := db.Model(&AuditLog{}).Where("action != ?", ActionAuditClear).Count(&prunable).Error; err != nil {
		return 0, fmt.Errorf("count prunable audit logs: %w", err)
	}

	excess := prunable - int64(maxEntries)
	if excess <= 0 {
		return 0, nil
	}

	res := db.Exec(pruneAuditLogsSQL, ActionAuditClear, excess)
	if res.Error != nil {
		return 0, fmt.Errorf("prune audit logs: %w", res.Error)
	}

	return res.RowsAffected, nil
}

// ClearWithAudit removes matching non-audit.clear records and persists the
// clearing event itself in a single transaction.
//
// This is the only strong-guarantee audit path: the asynchronous queue may drop
// entries under pressure, but a completed clear must always leave its own
// record behind. The transaction commits before success is reported, so a
// failure surfaces to the caller instead of being silently lost.
//
// entry supplies the already-resolved actor identity, action, and target. Its
// Detail is overwritten with the deleted count, which is only known after the
// delete statement runs inside this transaction. A nil before means "delete
// everything prunable".
func (s *Store) ClearWithAudit(ctx context.Context, entry *AuditLog, before *time.Time) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}

	var cleared int64
	err = db.Transaction(func(tx *gorm.DB) error {
		query := tx.Where("action != ?", ActionAuditClear)
		if before != nil && !before.IsZero() {
			query = query.Where("created_at < ?", before.UTC())
		}
		res := query.Delete(&AuditLog{})
		if res.Error != nil {
			return fmt.Errorf("clear audit logs: %w", res.Error)
		}
		cleared = res.RowsAffected

		if entry == nil {
			return nil
		}
		entry.Detail = fmt.Sprintf("deleted=%d", cleared)
		entry.CreatedAt = normalizeCreatedAt(entry.CreatedAt, time.Now().UTC())
		if err := tx.Create(entry).Error; err != nil {
			return fmt.Errorf("persist audit clear record: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return cleared, nil
}
