package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// Store owns the GORM connection pool and applies versioned SQL migrations before use.
type Store struct {
	dataDirectory string
	logger        *zap.Logger
	migrator      migrationRunner

	mu sync.RWMutex
	db *gorm.DB
}

// New creates a Store without opening files or database connections.
func New(dataDirectory string, logger *zap.Logger) *Store {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Store{
		dataDirectory: dataDirectory,
		logger:        logger,
		migrator:      embeddedMigrationRunner{logger: logger},
	}
}

func newWithMigrator(dataDirectory string, logger *zap.Logger, migrator migrationRunner) *Store {
	store := New(dataDirectory, logger)
	store.migrator = migrator
	return store
}

// OpenAndMigrate opens SQLite, configures connection settings, and applies SQL migrations.
func (s *Store) OpenAndMigrate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db != nil {
		return errors.New("database is already open")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("database startup canceled: %w", err)
	}
	if err := os.MkdirAll(s.dataDirectory, 0o750); err != nil {
		return fmt.Errorf("create database directory: %w", err)
	}
	dsn, err := sqliteDSN(s.dataDirectory)
	if err != nil {
		return fmt.Errorf("build SQLite DSN: %w", err)
	}

	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:  gormlog.Default.LogMode(gormlog.Silent),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return fmt.Errorf("open GORM SQLite connection: %w", err)
	}
	if err := configureConnection(ctx, db); err != nil {
		return errors.Join(err, closeGORM(db))
	}
	if err := s.migrator.up(ctx, dsn); err != nil {
		return errors.Join(fmt.Errorf("apply SQLite migrations: %w", err), closeGORM(db))
	}

	s.db = db
	s.logger.Info("database ready")
	return nil
}

// Ready reports whether the database has opened and completed all migrations.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db != nil
}

// DB returns the active GORM database instance or nil when unready.
func (s *Store) DB() *gorm.DB {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.db
}

// Close closes the SQL connection pool. Calling Close more than once is safe.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.db == nil {
		return nil
	}
	db := s.db
	s.db = nil
	if err := closeGORM(db); err != nil {
		return fmt.Errorf("close SQLite connection: %w", err)
	}
	s.logger.Info("database closed")
	return nil
}

func configureConnection(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get SQL connection pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("ping SQLite: %w", err)
	}
	return nil
}

func closeGORM(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func sqliteDSN(dataDirectory string) (string, error) {
	absolutePath, err := filepath.Abs(filepath.Join(dataDirectory, "zhulong.db"))
	if err != nil {
		return "", err
	}
	databaseURL := url.URL{Scheme: "file", Path: filepath.ToSlash(absolutePath)}
	return databaseURL.String() + "?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", nil
}
