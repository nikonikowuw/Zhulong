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

// batchInsertSize bounds how many rows one INSERT statement carries, so a large
// drain does not build a single oversized statement.
const batchInsertSize = 100

// defaultListLimit caps queries that omit Limit, preventing unbounded full-table
// reads and ensuring OFFSET is always paired with LIMIT in SQLite.
const defaultListLimit = 100

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
	normalizeAuditLog(log, time.Now().UTC())
	if err := gorm.G[AuditLog](db).Create(ctx, log); err != nil {
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
	validLogs := make([]*AuditLog, 0, len(logs))
	for _, l := range logs {
		if l == nil {
			continue
		}
		normalizeAuditLog(l, now)
		validLogs = append(validLogs, l)
	}
	if len(validLogs) == 0 {
		return nil
	}
	// gorm.G uses T = *AuditLog here because CreateInBatches expects *[]T matching []*AuditLog.
	if err := gorm.G[*AuditLog](db).CreateInBatches(ctx, &validLogs, batchInsertSize); err != nil {
		return fmt.Errorf("create audit log batch: %w", err)
	}
	return nil
}

// normalizeAuditLog fills mandatory defaults on log so direct Store callers
// never trigger the SQLite column default ("admin") or store a zero timestamp.
func normalizeAuditLog(log *AuditLog, now time.Time) {
	log.CreatedAt = normalizeCreatedAt(log.CreatedAt, now)
	if log.Username == "" {
		log.Username = UsernameUnknown
	}
	if log.Status == "" {
		log.Status = StatusSuccess
	}
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
	g := gorm.G[AuditLog](db).Scopes()

	if len(filter.Actions) > 0 {
		g = g.Where("action IN ?", filter.Actions)
	} else if filter.Action != "" {
		g = g.Where("action = ?", filter.Action)
	}
	if filter.Status != "" {
		g = g.Where("status = ?", filter.Status)
	}
	if filter.StartTime != nil && !filter.StartTime.IsZero() {
		g = g.Where("created_at >= ?", filter.StartTime.UTC())
	}
	if filter.EndTime != nil && !filter.EndTime.IsZero() {
		g = g.Where("created_at <= ?", filter.EndTime.UTC())
	}

	total, err := g.Count(ctx, "*")
	if err != nil {
		return nil, 0, fmt.Errorf("count audit logs: %w", err)
	}

	limit := filter.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}
	g = g.Order("created_at DESC, id DESC").Limit(limit)
	if filter.Offset > 0 {
		g = g.Offset(filter.Offset)
	}

	items, err := g.Find(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("find audit logs: %w", err)
	}
	if items == nil {
		items = []AuditLog{}
	}

	return items, total, nil
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
	prunable, err := gorm.G[AuditLog](db).Where("action != ?", ActionAuditClear).Count(ctx, "*")
	if err != nil {
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
		g := gorm.G[AuditLog](tx).Where("action != ?", ActionAuditClear)
		if before != nil && !before.IsZero() {
			g = g.Where("created_at < ?", before.UTC())
		}
		rows, err := g.Delete(ctx)
		if err != nil {
			return fmt.Errorf("clear audit logs: %w", err)
		}
		cleared = int64(rows)

		if entry == nil {
			return nil
		}
		entry.Detail = fmt.Sprintf("deleted=%d", cleared)
		normalizeAuditLog(entry, time.Now().UTC())
		if err := gorm.G[AuditLog](tx).Create(ctx, entry); err != nil {
			return fmt.Errorf("persist audit clear record: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	return cleared, nil
}
