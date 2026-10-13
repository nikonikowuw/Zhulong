package audit

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Config specifies settings for AuditService.
type Config struct {
	BufferSize    int
	BatchSize     int
	FlushInterval time.Duration
	MaxEntries    int
}

// DefaultConfig returns recommended production defaults.
func DefaultConfig() Config {
	return Config{
		BufferSize:    DefaultBufferSize,
		BatchSize:     50,
		FlushInterval: 1 * time.Second,
		MaxEntries:    DefaultMaxEntries,
	}
}

// Service manages asynchronous recording, batching, pruning, and querying of audit logs.
type Service struct {
	store      *Store
	logger     *zap.Logger
	cfg        Config
	queue      chan *AuditLog
	done       chan struct{}
	workerDone chan struct{}

	mu      sync.RWMutex
	started bool
	stopped bool
}

// NewService constructs an AuditService with the given store, logger, and configuration.
func NewService(store *Store, logger *zap.Logger, cfgs ...Config) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	cfg := DefaultConfig()
	if len(cfgs) > 0 {
		cfg = cfgs[0]
		if cfg.BufferSize <= 0 {
			cfg.BufferSize = DefaultBufferSize
		}
		if cfg.BatchSize <= 0 {
			cfg.BatchSize = 50
		}
		if cfg.FlushInterval <= 0 {
			cfg.FlushInterval = 1 * time.Second
		}
		if cfg.MaxEntries <= 0 {
			cfg.MaxEntries = DefaultMaxEntries
		}
	}

	return &Service{
		store:      store,
		logger:     logger.Named("audit.service"),
		cfg:        cfg,
		queue:      make(chan *AuditLog, cfg.BufferSize),
		done:       make(chan struct{}),
		workerDone: make(chan struct{}),
	}
}

// Start launches the background batch writer goroutine.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("audit service already started")
	}
	s.started = true
	go s.worker()
	s.logger.Info("audit service worker started")
	return nil
}

// Stop signals the background worker to drain queued logs and shut down gracefully.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.started || s.stopped {
		s.mu.Unlock()
		return nil
	}
	s.stopped = true
	close(s.done)
	s.mu.Unlock()

	select {
	case <-s.workerDone:
		s.logger.Info("audit service worker stopped and drained")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// isStopped returns whether the service has been marked stopped.
func (s *Service) isStopped() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stopped
}

// entryToLog converts an external Entry to an internal AuditLog with defaults.
// An empty username becomes "unknown" rather than "admin": the system has a
// single configurable account, and defaulting to a fixed name silently
// misattributes unattributed events. The persisted column keeps its own SQL
// default, so an explicit value is always written here.
func entryToLog(entry Entry) *AuditLog {
	log := &AuditLog{
		CreatedAt: entry.CreatedAt,
		IP:        entry.IP,
		Username:  entry.Username,
		Action:    entry.Action,
		Target:    entry.Target,
		Detail:    entry.Detail,
		Status:    entry.Status,
		ErrorMsg:  entry.ErrorMsg,
	}
	if log.CreatedAt.IsZero() {
		log.CreatedAt = time.Now().UTC()
	} else {
		log.CreatedAt = log.CreatedAt.UTC()
	}
	if log.Username == "" {
		log.Username = UsernameUnknown
	}
	if log.Status == "" {
		log.Status = StatusSuccess
	}
	return log
}

// RecordEvent queues an audit entry derived from the request context and the
// supplied operation description. When the context carries no Actor the event
// is attributed to "unknown" with an empty IP; it is never promoted to a
// system event. Recording is non-blocking.
func (s *Service) RecordEvent(ctx context.Context, action, target, detail string, opErr error) {
	s.Record(eventEntry(ctx, action, target, detail, opErr))
}

// RecordSystem queues an explicit system event. Background work has no client
// origin, so the IP is left empty and the username is "system"; the source
// address is never faked as a loopback address.
func (s *Service) RecordSystem(action, target, detail string, opErr error) {
	s.Record(systemEntry(action, target, detail, opErr))
}

// eventEntry builds an Entry for a human-initiated operation from ctx.
func eventEntry(ctx context.Context, action, target, detail string, opErr error) Entry {
	actor, ok := FromContext(ctx)
	if !ok {
		actor = Actor{Username: UsernameUnknown}
	} else if actor.Username == "" {
		actor.Username = UsernameUnknown
	}
	entry := Entry{
		IP:       actor.IP,
		Username: actor.Username,
		Action:   action,
		Target:   target,
		Detail:   detail,
		Status:   StatusSuccess,
	}
	if opErr != nil {
		entry.Status = StatusFailed
		entry.ErrorMsg = opErr.Error()
	}
	return entry
}

// systemEntry builds an Entry for an explicitly recorded system event.
func systemEntry(action, target, detail string, opErr error) Entry {
	entry := Entry{
		IP:       "",
		Username: UsernameSystem,
		Action:   action,
		Target:   target,
		Detail:   detail,
		Status:   StatusSuccess,
	}
	if opErr != nil {
		entry.Status = StatusFailed
		entry.ErrorMsg = opErr.Error()
	}
	return entry
}

// Record queues an audit entry asynchronously without blocking the caller.
// If the buffer is full, it logs a warning and drops the entry to protect the host.
func (s *Service) Record(entry Entry) {
	if s.isStopped() {
		return
	}

	log := entryToLog(entry)

	select {
	case s.queue <- log:
	default:
		s.logger.Warn("audit log queue is full, dropping entry",
			zap.String("action", log.Action),
			zap.String("target", log.Target),
		)
	}
}

// RecordSync writes an audit entry synchronously.
func (s *Service) RecordSync(ctx context.Context, entry Entry) error {
	log := entryToLog(entry)

	if err := s.store.Create(ctx, log); err != nil {
		return err
	}
	_, _ = s.store.Prune(ctx, s.cfg.MaxEntries)
	return nil
}

// List queries audit logs matching the given filter with pagination.
func (s *Service) List(ctx context.Context, filter Filter) ([]AuditLog, int64, error) {
	return s.store.List(ctx, filter)
}

// Clear deletes all audit records or those older than the specified time.
// It is intended for internal callers; the public DELETE endpoint must use
// ClearWithAudit so the clearing operation leaves its own record.
func (s *Service) Clear(ctx context.Context, before ...time.Time) (int64, error) {
	return s.store.Clear(ctx, before...)
}

// ClearWithAudit deletes matching records and persists the audit.clear
// self-audit entry in one transaction, then returns the deleted count.
//
// The actor is resolved from ctx exactly like RecordEvent: an unattributed
// caller is recorded as unknown with an empty IP, never as a system event.
func (s *Service) ClearWithAudit(ctx context.Context, target string, before ...time.Time) (int64, error) {
	actor, ok := FromContext(ctx)
	if !ok {
		actor = Actor{Username: UsernameUnknown}
	} else if actor.Username == "" {
		actor.Username = UsernameUnknown
	}

	entry := entryToLog(Entry{
		IP:       actor.IP,
		Username: actor.Username,
		Action:   ActionAuditClear,
		Target:   target,
		Status:   StatusSuccess,
	})

	cleared, err := s.store.ClearWithAudit(ctx, entry, before...)
	if err != nil {
		return 0, err
	}
	return cleared, nil
}

// Count returns total number of audit logs.
func (s *Service) Count(ctx context.Context) (int64, error) {
	return s.store.Count(ctx)
}

// worker batches audit logs and periodically flushes them to the store.
func (s *Service) worker() {
	defer close(s.workerDone)

	ticker := time.NewTicker(s.cfg.FlushInterval)
	defer ticker.Stop()

	var batch []*AuditLog

	flush := func() {
		if len(batch) == 0 {
			return
		}
		toWrite := batch
		batch = nil

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := s.store.CreateBatch(ctx, toWrite); err != nil {
			s.logger.Error("failed to persist audit batch", zap.Error(err), zap.Int("count", len(toWrite)))
		} else {
			if _, pruneErr := s.store.Prune(ctx, s.cfg.MaxEntries); pruneErr != nil {
				s.logger.Warn("failed to prune audit records", zap.Error(pruneErr))
			}
		}
	}

	for {
		select {
		case item := <-s.queue:
			batch = append(batch, item)
			if len(batch) >= s.cfg.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.done:
			// Drain remaining in queue
			for {
				select {
				case item := <-s.queue:
					batch = append(batch, item)
				default:
					flush()
					return
				}
			}
		}
	}
}
