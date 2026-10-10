package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

type failingMigrationRunner struct {
	err error
}

func (r failingMigrationRunner) up(context.Context, string) error {
	return r.err
}

func TestOpenAndMigrateCreatesVersionedSchema(t *testing.T) {
	store := New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	if !store.Ready() {
		t.Fatal("database should be ready after migration")
	}

	var metadataTable string
	if err := store.db.Raw("SELECT name FROM sqlite_master WHERE type = ? AND name = ?", "table", "system_metadata").Scan(&metadataTable).Error; err != nil {
		t.Fatalf("query migrated table system_metadata: %v", err)
	}
	if metadataTable != "system_metadata" {
		t.Fatalf("expected migrated system_metadata table, got %q", metadataTable)
	}

	var usersTable string
	if err := store.db.Raw("SELECT name FROM sqlite_master WHERE type = ? AND name = ?", "table", "users").Scan(&usersTable).Error; err != nil {
		t.Fatalf("query migrated table users: %v", err)
	}
	if usersTable != "users" {
		t.Fatalf("expected migrated users table, got %q", usersTable)
	}

	var camerasTable string
	if err := store.db.Raw("SELECT name FROM sqlite_master WHERE type = ? AND name = ?", "table", "cameras").Scan(&camerasTable).Error; err != nil {
		t.Fatalf("query migrated table cameras: %v", err)
	}
	if camerasTable != "cameras" {
		t.Fatalf("expected migrated cameras table, got %q", camerasTable)
	}

	var streamsTable string
	if err := store.db.Raw("SELECT name FROM sqlite_master WHERE type = ? AND name = ?", "table", "camera_streams").Scan(&streamsTable).Error; err != nil {
		t.Fatalf("query migrated table camera_streams: %v", err)
	}
	if streamsTable != "camera_streams" {
		t.Fatalf("expected migrated camera_streams table, got %q", streamsTable)
	}

	var auditLogsTable string
	if err := store.db.Raw("SELECT name FROM sqlite_master WHERE type = ? AND name = ?", "table", "audit_logs").Scan(&auditLogsTable).Error; err != nil {
		t.Fatalf("query migrated table audit_logs: %v", err)
	}
	if auditLogsTable != "audit_logs" {
		t.Fatalf("expected migrated audit_logs table, got %q", auditLogsTable)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if store.Ready() {
		t.Fatal("database should not be ready after Close")
	}
}

func TestGORMClockUsesUTC(t *testing.T) {
	store := New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	if got := store.db.NowFunc(); got.Location() != time.UTC {
		t.Fatalf("expected GORM clock in UTC, got %s", got.Location())
	}
}

func TestSystemMetadataTimestampDefaults(t *testing.T) {
	store := New(t.TempDir(), zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

	const key = "test.setting"
	if err := store.db.Exec("INSERT INTO system_metadata (key, value) VALUES (?, ?)", key, "before").Error; err != nil {
		t.Fatalf("insert setting: %v", err)
	}
	var createdAt, updatedAt string
	if err := store.db.Raw("SELECT created_at, updated_at FROM system_metadata WHERE key = ?", key).Row().Scan(&createdAt, &updatedAt); err != nil {
		t.Fatalf("read timestamps: %v", err)
	}
	if createdAt == "" || updatedAt == "" {
		t.Fatalf("expected insert timestamps, got created_at=%q updated_at=%q", createdAt, updatedAt)
	}
}

func TestMigrationFailureClosesDatabase(t *testing.T) {
	migrationError := errors.New("injected migration failure")
	store := newWithMigrator(t.TempDir(), zap.NewNop(), failingMigrationRunner{err: migrationError})

	err := store.OpenAndMigrate(context.Background())
	if !errors.Is(err, migrationError) {
		t.Fatalf("expected migration failure, got %v", err)
	}
	if store.Ready() {
		t.Fatal("database must not become ready after migration failure")
	}
	if store.db != nil {
		t.Fatal("GORM connection must be closed after migration failure")
	}
}

func TestOpenAndMigrateRejectsUnusableDataDirectory(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("create file: %v", err)
	}

	store := New(filePath, zap.NewNop())
	if err := store.OpenAndMigrate(context.Background()); err == nil {
		t.Fatal("expected directory creation error")
	}
	if store.Ready() {
		t.Fatal("database must not be ready")
	}
}

func TestAuditLogsMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migrate.db")
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	defer db.Close()

	dbDriver, err := migratesqlite3.WithInstance(db, &migratesqlite3.Config{})
	if err != nil {
		t.Fatalf("create sqlite3 driver: %v", err)
	}

	srcDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("create source driver: %v", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", srcDriver, "sqlite3", dbDriver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer migrator.Close()

	// 1. Run migrations up to 4
	if err := migrator.Migrate(4); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(4): %v", err)
	}

	// Verify audit_logs table and indexes exist
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'audit_logs'").Scan(&count); err != nil {
		t.Fatalf("query audit_logs table: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected audit_logs table after Up, got count=%d", count)
	}

	var indexCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_audit_logs_%'").Scan(&indexCount); err != nil {
		t.Fatalf("query audit_logs indexes: %v", err)
	}
	if indexCount != 3 {
		t.Fatalf("expected 3 audit_logs indexes after Up, got %d", indexCount)
	}

	// 2. Rollback migration 4 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// Verify audit_logs table and indexes are dropped
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'audit_logs'").Scan(&count); err != nil {
		t.Fatalf("query audit_logs table after down: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected audit_logs table to be dropped after rollback, got count=%d", count)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name LIKE 'idx_audit_logs_%'").Scan(&indexCount); err != nil {
		t.Fatalf("query audit_logs indexes after down: %v", err)
	}
	if indexCount != 0 {
		t.Fatalf("expected 0 audit_logs indexes after rollback, got %d", indexCount)
	}

	// 3. Re-apply migration 4 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	// Verify audit_logs table exists again
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'audit_logs'").Scan(&count); err != nil {
		t.Fatalf("query audit_logs table after re-up: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected audit_logs table after re-Up, got count=%d", count)
	}
}

func TestSystemTimeConfigsMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_5.db")
	dsn := fmt.Sprintf("%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(ON)", dbPath)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite3: %v", err)
	}
	defer db.Close()

	dbDriver, err := migratesqlite3.WithInstance(db, &migratesqlite3.Config{})
	if err != nil {
		t.Fatalf("create sqlite3 driver: %v", err)
	}

	srcDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		t.Fatalf("create source driver: %v", err)
	}

	migrator, err := migrate.NewWithInstance("iofs", srcDriver, "sqlite3", dbDriver)
	if err != nil {
		t.Fatalf("create migrator: %v", err)
	}
	defer migrator.Close()

	// 1. Run all migrations Up (including 5)
	if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Up: %v", err)
	}

	// Verify system_time_configs table exists
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'system_time_configs'").Scan(&count); err != nil {
		t.Fatalf("query system_time_configs table: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected system_time_configs table after Up, got count=%d", count)
	}

	// 2. Rollback migration 5 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// Verify system_time_configs table is dropped
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'system_time_configs'").Scan(&count); err != nil {
		t.Fatalf("query system_time_configs table after down: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected system_time_configs table to be dropped after rollback, got count=%d", count)
	}

	// 3. Re-apply migration 5 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'system_time_configs'").Scan(&count); err != nil {
		t.Fatalf("query system_time_configs table after re-up: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected system_time_configs table after re-Up, got count=%d", count)
	}
}
