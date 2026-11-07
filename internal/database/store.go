package database

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
)

// ErrClosed is the shared sentinel every domain store returns when the database
// has not been opened yet or has already been closed. It lives here so modules
// do not each declare their own equivalent error.
var ErrClosed = errors.New("database connection is not available")

// DBProvider abstracts access to the underlying *gorm.DB.
type DBProvider interface {
	DB() *gorm.DB
}

// Store owns the GORM connection pool and applies versioned SQL migrations before use.
type Store struct {
	dataDirectory string
	logger        *zap.Logger
	migrator      migrationRunner

	mu sync.RWMutex
	db *gorm.DB
}

var _ DBProvider = (*Store)(nil)

// SQLite 连接参数集中定义，供生产 DSN 与测试断言共用，避免魔法字符串散落。
const (
	// sqliteBusyTimeoutMS 是锁冲突时的最长退避等待。
	sqliteBusyTimeoutMS = 5000
	// sqlitePageCacheKiB 是单连接页缓存上限（负值表示单位为 KiB）。
	// 边缘设备（如 RK3568，2-4 GiB 内存）需与 NPU 推理、视频缓冲共享内存，
	// 32 MiB 足以缓存热索引页，同时不会挤压推理侧。
	sqlitePageCacheKiB = 32768
)

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
	return databaseURL.String() + "?" + sqlitePragmas().Encode(), nil
}

// sqlitePragmas 返回生产与测试共用的连接级 PRAGMA 参数。
func sqlitePragmas() url.Values {
	params := url.Values{}
	params.Set("_busy_timeout", strconv.Itoa(sqliteBusyTimeoutMS))
	params.Set("_foreign_keys", "on")
	params.Set("_journal_mode", "WAL")
	// WAL 下 synchronous=NORMAL 仅在 checkpoint 时 fsync：进程崩溃或掉电不会损坏
	// 数据库文件，最坏丢失最近若干毫秒未 checkpoint 的事务。SQLite 默认的 FULL 会让
	// 每次提交都 fsync 数据页与 WAL，在 eMMC/SD 卡上把写入吞吐压到百 TPS 量级。
	params.Set("_synchronous", "NORMAL")
	params.Set("_cache_size", strconv.Itoa(-sqlitePageCacheKiB))
	return params
}
