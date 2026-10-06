package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

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
