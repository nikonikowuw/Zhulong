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
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 5
	if err := migrator.Migrate(5); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(5): %v", err)
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

func TestAlgorithmTablesMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_7.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 7 (algorithm tables live in 000007)
	if err := migrator.Migrate(7); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(7): %v", err)
	}

	// Verify algorithm tables exist
	tables := []string{"algorithms", "analysis_tasks"}
	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", tbl, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s after Up, got count=%d", tbl, count)
		}
	}

	// 2. Rollback migration 7 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// Verify algorithm tables are dropped
	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s after down: %v", tbl, err)
		}
		if count != 0 {
			t.Fatalf("expected table %s to be dropped after rollback, got count=%d", tbl, count)
		}
	}

	// 3. Re-apply migration 6 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s after re-up: %v", tbl, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s after re-Up, got count=%d", tbl, count)
		}
	}
}

func TestPersonsTablesMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_6.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 6 (persons tables live in 000006)
	if err := migrator.Migrate(6); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(6): %v", err)
	}

	// Verify person tables exist
	tables := []string{"persons", "person_faces"}
	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s: %v", tbl, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s after Up, got count=%d", tbl, count)
		}
	}

	// 2. Rollback migration 6 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// Verify person tables are dropped
	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s after down: %v", tbl, err)
		}
		if count != 0 {
			t.Fatalf("expected table %s to be dropped after rollback, got count=%d", tbl, count)
		}
	}

	// 3. Re-apply migration 7 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	for _, tbl := range tables {
		var count int
		if err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '%s'", tbl)).Scan(&count); err != nil {
			t.Fatalf("query table %s after re-up: %v", tbl, err)
		}
		if count != 1 {
			t.Fatalf("expected table %s after re-Up, got count=%d", tbl, count)
		}
	}
}

func TestPrimaryFaceIDMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_8.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 8
	if err := migrator.Migrate(8); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(8): %v", err)
	}

	hasColumn := func() bool {
		rows, err := db.Query("PRAGMA table_info(persons)")
		if err != nil {
			t.Fatalf("query table_info(persons): %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, ctype string
			var notnull, pk int
			var dfltValue sql.NullString
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
				t.Fatalf("scan pragma row: %v", err)
			}
			if name == "primary_face_id" {
				return true
			}
		}
		return false
	}

	if !hasColumn() {
		t.Fatalf("expected primary_face_id column in persons table after Up(8)")
	}

	// 2. Rollback migration 8 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	if hasColumn() {
		t.Fatalf("expected primary_face_id column to be removed after Steps(-1)")
	}

	// 3. Re-apply migration 8 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	if !hasColumn() {
		t.Fatalf("expected primary_face_id column to be restored after re-Up(1)")
	}
}

func TestMigration000009_DecouplePersonID(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migrate_09.db")

	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
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

	// 1. Run migrations up to 8
	if err := migrator.Migrate(8); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(8): %v", err)
	}

	// Insert test data: one person where person_id == id (dummy), one where person_id == "EMP_001"
	if _, err := db.Exec(`INSERT INTO persons (id, person_id, name) VALUES ('p_dummy', 'p_dummy', 'Dummy');`); err != nil {
		t.Fatalf("insert dummy person: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO persons (id, person_id, name) VALUES ('p_real', 'EMP_001', 'Real');`); err != nil {
		t.Fatalf("insert real person: %v", err)
	}
	// Insert face for p_dummy to verify CASCADE did not delete faces
	if _, err := db.Exec(`INSERT INTO person_faces (id, person_id, feature_vector) VALUES ('f_1', 'p_dummy', x'0001');`); err != nil {
		t.Fatalf("insert face: %v", err)
	}
	// Enrolling a face auto-assigns it as the primary face, so a realistic
	// pre-migration row carries a non-NULL primary_face_id. Migration 9 recreates
	// persons while person_faces still references it, and with foreign keys ON
	// this is exactly the state that used to make the upgrade fail.
	if _, err := db.Exec(`UPDATE persons SET primary_face_id = 'f_1' WHERE id = 'p_dummy';`); err != nil {
		t.Fatalf("assign primary face: %v", err)
	}

	// 2. Apply migration 9
	if err := migrator.Migrate(9); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(9): %v", err)
	}

	// Verify p_dummy now has NULL person_id
	var dummyPersonID sql.NullString
	if err := db.QueryRow(`SELECT person_id FROM persons WHERE id = 'p_dummy'`).Scan(&dummyPersonID); err != nil {
		t.Fatalf("query dummy person_id: %v", err)
	}
	if dummyPersonID.Valid {
		t.Fatalf("expected p_dummy person_id to be NULL, got %q", dummyPersonID.String)
	}

	// Verify p_real preserved "EMP_001"
	var realPersonID sql.NullString
	if err := db.QueryRow(`SELECT person_id FROM persons WHERE id = 'p_real'`).Scan(&realPersonID); err != nil {
		t.Fatalf("query real person_id: %v", err)
	}
	if !realPersonID.Valid || realPersonID.String != "EMP_001" {
		t.Fatalf("expected p_real person_id 'EMP_001', got %v", realPersonID)
	}

	// Verify f_1 was preserved in person_faces
	var faceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM person_faces WHERE id = 'f_1'`).Scan(&faceCount); err != nil {
		t.Fatalf("query face count: %v", err)
	}
	if faceCount != 1 {
		t.Fatalf("expected face f_1 to be preserved, got count %d", faceCount)
	}

	// Verify the primary face pointer survived the table recreation
	var dummyPrimaryFace sql.NullString
	if err := db.QueryRow(`SELECT primary_face_id FROM persons WHERE id = 'p_dummy'`).Scan(&dummyPrimaryFace); err != nil {
		t.Fatalf("query dummy primary_face_id: %v", err)
	}
	if !dummyPrimaryFace.Valid || dummyPrimaryFace.String != "f_1" {
		t.Fatalf("expected p_dummy primary_face_id 'f_1', got %v", dummyPrimaryFace)
	}

	// Verify multiple NULL person_ids do not conflict
	if _, err := db.Exec(`INSERT INTO persons (id, person_id, name) VALUES ('p_null_2', NULL, 'Null2');`); err != nil {
		t.Fatalf("insert second null person_id failed: %v", err)
	}

	// Verify duplicate non-null person_id conflicts
	if _, err := db.Exec(`INSERT INTO persons (id, person_id, name) VALUES ('p_dup', 'EMP_001', 'Dup');`); err == nil {
		t.Fatalf("expected duplicate EMP_001 to fail UNIQUE constraint, but succeeded")
	}

	// 3. Rollback migration 9 (Down step 1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// In migration 8 schema, NULL person_ids should have fallen back to COALESCE(person_id, id)
	var rolledBackID sql.NullString
	if err := db.QueryRow(`SELECT person_id FROM persons WHERE id = 'p_dummy'`).Scan(&rolledBackID); err != nil {
		t.Fatalf("query rolled back person_id: %v", err)
	}
	if !rolledBackID.Valid || rolledBackID.String != "p_dummy" {
		t.Fatalf("expected rolled back person_id 'p_dummy', got %v", rolledBackID)
	}

	// The down migration recreates persons too, so the primary face pointer and
	// the enrolled face must both survive the round trip.
	var rolledBackPrimaryFace sql.NullString
	if err := db.QueryRow(`SELECT primary_face_id FROM persons WHERE id = 'p_dummy'`).Scan(&rolledBackPrimaryFace); err != nil {
		t.Fatalf("query rolled back primary_face_id: %v", err)
	}
	if !rolledBackPrimaryFace.Valid || rolledBackPrimaryFace.String != "f_1" {
		t.Fatalf("expected rolled back primary_face_id 'f_1', got %v", rolledBackPrimaryFace)
	}
	var rolledBackFaceCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM person_faces WHERE id = 'f_1'`).Scan(&rolledBackFaceCount); err != nil {
		t.Fatalf("query rolled back face count: %v", err)
	}
	if rolledBackFaceCount != 1 {
		t.Fatalf("expected face f_1 to survive the down migration, got count %d", rolledBackFaceCount)
	}

	// 4. Re-apply migration 9 (Up step 1)
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	// The re-applied up migration must preserve the pointer as well.
	var reappliedPrimaryFace sql.NullString
	if err := db.QueryRow(`SELECT primary_face_id FROM persons WHERE id = 'p_dummy'`).Scan(&reappliedPrimaryFace); err != nil {
		t.Fatalf("query re-applied primary_face_id: %v", err)
	}
	if !reappliedPrimaryFace.Valid || reappliedPrimaryFace.String != "f_1" {
		t.Fatalf("expected re-applied primary_face_id 'f_1', got %v", reappliedPrimaryFace)
	}
}

func TestMigration000010NormalizeAuditActions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test_migration_10.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_busy_timeout=5000", dbPath))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()

	dbDriver, err := migratesqlite3.WithInstance(db, &migratesqlite3.Config{})
	if err != nil {
		t.Fatalf("create sqlite driver: %v", err)
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

	// 1. Run migrations up to 9
	if err := migrator.Migrate(9); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(9): %v", err)
	}

	// Insert legacy audit log actions
	legacyRecords := []string{
		"system.time.update_config",
		"system.time.ntp_sync",
		"system.time.manual_set",
		"system.time.rtc_heal",
		"system.storage.update_config",
		"system.storage.manual_cleanup",
		"system.storage.test_path",
		"system.storage.path_test",
		"system.storage.emergency_stop",
		"system.storage.emergency_resume",
		"camera.create", // should remain untouched
	}
	for i, action := range legacyRecords {
		if _, err := db.Exec(`INSERT INTO audit_logs (id, action, target, detail, status) VALUES (?, ?, 'target', '', 'success')`, i+1, action); err != nil {
			t.Fatalf("insert legacy audit log %q: %v", action, err)
		}
	}

	// 2. Apply migration 10
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}

	// Verify actions are normalized
	expectedNormalized := map[int]string{
		1:  "time.update_config",
		2:  "time.ntp_sync",
		3:  "time.manual_set",
		4:  "time.rtc_heal",
		5:  "storage.update_config",
		6:  "storage.manual_cleanup",
		7:  "storage.test_path",
		8:  "storage.test_path",
		9:  "storage.emergency_stop",
		10: "storage.emergency_resume",
		11: "camera.create",
	}
	for id, expectedAction := range expectedNormalized {
		var action string
		if err := db.QueryRow(`SELECT action FROM audit_logs WHERE id = ?`, id).Scan(&action); err != nil {
			t.Fatalf("query normalized action for id %d: %v", id, err)
		}
		if action != expectedAction {
			t.Errorf("id %d: expected action %q, got %q", id, expectedAction, action)
		}
	}

	// 3. Rollback migration 10
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator Steps(-1): %v", err)
	}

	// Verify rollback restored legacy prefix
	var rolledBackAction string
	if err := db.QueryRow(`SELECT action FROM audit_logs WHERE id = 2`).Scan(&rolledBackAction); err != nil {
		t.Fatalf("query rolled back action: %v", err)
	}
	if rolledBackAction != "system.time.ntp_sync" {
		t.Errorf("expected rolled back action 'system.time.ntp_sync', got %q", rolledBackAction)
	}

	// 4. Re-apply migration 10
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator Steps(1): %v", err)
	}
}

func TestPerceptionAndAlarmTablesMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_11.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 11
	if err := migrator.Migrate(11); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(11): %v", err)
	}

	tableExists := func(tableName string) bool {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", tableName).Scan(&name)
		return err == nil && name == tableName
	}

	for _, tbl := range []string{"recognitions", "alarms", "captures"} {
		if !tableExists(tbl) {
			t.Errorf("expected table %s to exist after migration 11 up", tbl)
		}
	}

	// 2. Roll back migration 11
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator rollback (-1): %v", err)
	}

	for _, tbl := range []string{"recognitions", "alarms", "captures"} {
		if tableExists(tbl) {
			t.Errorf("expected table %s to be dropped after migration 11 down", tbl)
		}
	}

	// 3. Re-apply migration 11
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator re-apply (1): %v", err)
	}

	for _, tbl := range []string{"recognitions", "alarms", "captures"} {
		if !tableExists(tbl) {
			t.Errorf("expected table %s to exist after re-applying migration 11 up", tbl)
		}
	}
}

func TestRegionConfigMigrationUpAndDownSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_12.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 12
	if err := migrator.Migrate(12); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(12): %v", err)
	}

	columnExists := func(tableName, colName string) bool {
		rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", tableName))
		if err != nil {
			return false
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, colType string
			var notnull, pk int
			var dfltValue sql.NullString
			if err := rows.Scan(&cid, &name, &colType, &notnull, &dfltValue, &pk); err == nil {
				if name == colName {
					return true
				}
			}
		}
		return false
	}

	if !columnExists("analysis_tasks", "region_config_json") {
		t.Errorf("expected region_config_json column on analysis_tasks after migration 12 up")
	}

	// 2. Roll back migration 12
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator rollback (-1): %v", err)
	}

	if columnExists("analysis_tasks", "region_config_json") {
		t.Errorf("expected region_config_json column to be dropped after migration 12 down")
	}

	// 3. Re-apply migration 12
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator re-apply (1): %v", err)
	}

	if !columnExists("analysis_tasks", "region_config_json") {
		t.Errorf("expected region_config_json column on analysis_tasks after re-applying migration 12 up")
	}
}

func TestChannelMultiAlgoTaskMigrationSymmetry(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "migration_symmetry_13.db")
	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)

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

	// 1. Run migrations up to 12
	if err := migrator.Migrate(12); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(12): %v", err)
	}

	// Insert parent algorithms
	_, err = db.Exec(`
		INSERT INTO algorithms (id, name, version, task_type, category, target_arch, target_device, compute_cost)
		VALUES 
			('algo_1', 'Algorithm 1', '1.0.0', 'detection', 'general', 'x86_64', 'cpu', 10),
			('algo_2', 'Algorithm 2', '1.0.0', 'recognition', 'face', 'x86_64', 'cpu', 20)
	`)
	if err != nil {
		t.Fatalf("insert algorithms: %v", err)
	}

	// Insert old flat analysis_tasks:
	// cam_1 has 2 algorithms (algo_1 enabled, algo_2 disabled), algo_1 has region_config_json
	// cam_2 has 1 algorithm (algo_1 enabled)
	_, err = db.Exec(`
		INSERT INTO analysis_tasks (id, camera_id, algorithm_id, enabled, sample_fps, config_json, region_config_json, created_at, updated_at)
		VALUES 
			('task_01ARZ3NDEKTSV4RRFFQ69G5FAV', 'cam_1', 'algo_1', 1, 10, '{"thresh":0.5}', '{"roi":[[0.1,0.1,0.9,0.9]]}', '2026-10-18 10:00:00', '2026-10-18 10:00:00'),
			('task_01ARZ3NDEKTSV4RRFFQ69G5FAW', 'cam_1', 'algo_2', 0, 5, '{}', '{}', '2026-10-18 10:05:00', '2026-10-18 10:05:00'),
			('task_01ARZ3NDEKTSV4RRFFQ69G5FAX', 'cam_2', 'algo_1', 1, 15, '{"thresh":0.8}', '{"mask":[[0.2,0.2,0.5,0.5]]}', '2026-10-18 11:00:00', '2026-10-18 11:00:00')
	`)
	if err != nil {
		t.Fatalf("insert test tasks at v12: %v", err)
	}

	// 2. Run migration 13 up
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator step up (1) to 13: %v", err)
	}

	// Verify analysis_tasks has 2 rows (1 per camera)
	var taskCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM analysis_tasks").Scan(&taskCount); err != nil {
		t.Fatalf("count analysis_tasks: %v", err)
	}
	if taskCount != 2 {
		t.Fatalf("expected 2 analysis_tasks (1 per camera), got %d", taskCount)
	}

	// Verify task_algorithms has 3 rows
	var detailCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM task_algorithms").Scan(&detailCount); err != nil {
		t.Fatalf("count task_algorithms: %v", err)
	}
	if detailCount != 3 {
		t.Fatalf("expected 3 task_algorithms rows, got %d", detailCount)
	}

	// Verify cam_1's task id reuses MIN(id): 'task_01ARZ3NDEKTSV4RRFFQ69G5FAV'
	var cam1TaskID string
	var cam1Enabled bool
	if err := db.QueryRow("SELECT id, enabled FROM analysis_tasks WHERE camera_id = 'cam_1'").Scan(&cam1TaskID, &cam1Enabled); err != nil {
		t.Fatalf("query cam_1 task: %v", err)
	}
	if cam1TaskID != "task_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Errorf("expected cam_1 task id to be task_01ARZ3NDEKTSV4RRFFQ69G5FAV, got %s", cam1TaskID)
	}
	if !cam1Enabled {
		t.Errorf("expected cam_1 master enabled to be true (MAX(enabled))")
	}

	// Verify region_config_json is preserved on task_algorithms
	var algo1Region string
	if err := db.QueryRow("SELECT region_config_json FROM task_algorithms WHERE id = 'task_01ARZ3NDEKTSV4RRFFQ69G5FAV'").Scan(&algo1Region); err != nil {
		t.Fatalf("query algo1 region: %v", err)
	}
	if algo1Region != `{"roi":[[0.1,0.1,0.9,0.9]]}` {
		t.Errorf("expected preserved region_config_json, got %s", algo1Region)
	}

	// Verify foreign key integrity
	var fkCheck string
	if err := db.QueryRow("PRAGMA foreign_key_check").Scan(&fkCheck); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("foreign_key_check failed: %v", err)
	}

	// 3. Roll back migration 13 (step down -1)
	if err := migrator.Steps(-1); err != nil {
		t.Fatalf("migrator step down (-1) from 13: %v", err)
	}

	// Verify analysis_tasks is restored to 3 rows
	if err := db.QueryRow("SELECT COUNT(*) FROM analysis_tasks").Scan(&taskCount); err != nil {
		t.Fatalf("count restored analysis_tasks: %v", err)
	}
	if taskCount != 3 {
		t.Fatalf("expected 3 restored analysis_tasks, got %d", taskCount)
	}

	// Verify algo_2 for cam_1 still has enabled=0
	var cam1Algo2Enabled bool
	if err := db.QueryRow("SELECT enabled FROM analysis_tasks WHERE camera_id = 'cam_1' AND algorithm_id = 'algo_2'").Scan(&cam1Algo2Enabled); err != nil {
		t.Fatalf("query restored cam_1 algo_2: %v", err)
	}
	if cam1Algo2Enabled {
		t.Errorf("expected cam_1 algo_2 enabled to remain false after rollback")
	}

	// 4. Re-apply migration 13
	if err := migrator.Steps(1); err != nil {
		t.Fatalf("migrator re-apply (1) to 13: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM task_algorithms").Scan(&detailCount); err != nil {
		t.Fatalf("count task_algorithms after re-apply: %v", err)
	}
	if detailCount != 3 {
		t.Fatalf("expected 3 task_algorithms after re-apply, got %d", detailCount)
	}
}

// TestMigration000014_PerceptionForeignKeys locks down the three reference-integrity
// fixes introduced by migration 14. Each assertion corresponds to a defect that was
// reproduced against the pre-14 schema:
//
//  1. An empty algorithm_id must be storable (it means "source not annotated").
//     Before 14 the NOT NULL + REFERENCES combination rejected it, and
//     record.Service swallowed the error, silently dropping every such record.
//  2. Deleting a camera that has perception records must succeed and cascade them.
//     Before 14 the bare REFERENCES cameras(id) made such cameras永久 undeletable.
//  3. Deleting an algorithm that produced records must succeed and cascade them.
func TestMigration000014_PerceptionForeignKeys(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migrate_14.db")

	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
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

	// Bring the schema to 13, i.e. the state that shipped with the defects.
	if err := migrator.Migrate(13); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(13): %v", err)
	}

	// Seed prerequisite rows plus one record per table.
	for _, stmt := range []string{
		`INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		 VALUES ('cam_a', 'A', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		 VALUES ('cam_b', 'B', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO algorithms (id, name, version, task_type, category, target_arch,
		   target_device, compute_cost, memory_mb, description, manifest_json,
		   config_schema_json, status, previous_version, created_at, updated_at)
		 VALUES ('algo_a', 'Det', '1.0.0', 'detection', 'safety', 'aarch64', 'rknn',
		   10, 64, '', '{}', '{}', 'ACTIVE', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO recognitions (id, camera_id, algorithm_id, pts, captured_at, created_at)
		 VALUES ('rec_a', 'cam_a', 'algo_a', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO alarms (id, camera_id, algorithm_id, alarm_type, pts, captured_at, created_at)
		 VALUES ('alm_a', 'cam_a', 'algo_a', 'fire', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO captures (id, camera_id, target_type, pts, captured_at, created_at)
		 VALUES ('cap_a', 'cam_a', 'person', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO captures (id, camera_id, target_type, pts, captured_at, created_at)
		 VALUES ('cap_b', 'cam_b', 'person', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// Apply migrations 14 and 15. 14 relaxed the algorithm_id constraint, 15
	// replaced its ON DELETE CASCADE with ON DELETE SET NULL and added the
	// algorithm_name / algorithm_version snapshots. The assertions below target
	// the post-15 schema, which is the one that ships.
	if err := migrator.Migrate(16); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(16): %v", err)
	}

	// ── Defect 1: unattributed records (NULL algorithm_id) are storable ────
	// NULL is the only value SQLite skips foreign-key validation for; an empty
	// string would still be checked against algorithms(id) and rejected.
	if _, err := db.Exec(`INSERT INTO recognitions
		(id, camera_id, algorithm_id, pts, captured_at, created_at)
		VALUES ('rec_unattributed', 'cam_a', NULL, 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("NULL algorithm_id must be storable after migration 14: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO alarms
		(id, camera_id, algorithm_id, alarm_type, pts, captured_at, created_at)
		VALUES ('alm_unattributed', 'cam_a', NULL, 'fire', 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("NULL algorithm_id must be storable in alarms: %v", err)
	}

	// A non-empty but nonexistent algorithm must still be rejected: the fix
	// relaxes "not empty", it does not disable referential integrity.
	if _, err := db.Exec(`INSERT INTO recognitions
		(id, camera_id, algorithm_id, pts, captured_at, created_at)
		VALUES ('rec_bogus', 'cam_a', 'no_such_algo', 3, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("nonexistent algorithm_id must still violate the foreign key")
	}

	// ── Defect 2: camera deletion cascades perception records ─────────────
	if _, err := db.Exec(`DELETE FROM cameras WHERE id = 'cam_b'`); err != nil {
		t.Fatalf("deleting a camera with perception records must succeed: %v", err)
	}
	var orphanCaptures int
	if err := db.QueryRow(`SELECT COUNT(*) FROM captures WHERE camera_id = 'cam_b'`).Scan(&orphanCaptures); err != nil {
		t.Fatalf("count orphan captures: %v", err)
	}
	if orphanCaptures != 0 {
		t.Errorf("expected cascade to remove captures of deleted camera, %d left", orphanCaptures)
	}
	// Records of the surviving camera must be untouched.
	var keptCaptures int
	if err := db.QueryRow(`SELECT COUNT(*) FROM captures WHERE camera_id = 'cam_a'`).Scan(&keptCaptures); err != nil {
		t.Fatalf("count kept captures: %v", err)
	}
	if keptCaptures != 1 {
		t.Errorf("expected cam_a's capture to survive, got %d", keptCaptures)
	}

	// ── Defect 3: algorithm deletion PRESERVES perception records ─────────
	// 卸载算法是卸载工具，不是销毁历史。记录必须留着，只把来源引用置 NULL，
	// 并依赖 algorithm_name / algorithm_version 快照回答「这批告警是谁产生的」。
	// 迁移 14 曾经错误地用了 ON DELETE CASCADE，15 纠正为 SET NULL。
	//
	// 模拟卸载前的状态：写入时快照了名称与版本。
	if _, err := db.Exec(`UPDATE recognitions
		SET algorithm_name = 'Det', algorithm_version = '1.0.0'
		WHERE algorithm_id = 'algo_a'`); err != nil {
		t.Fatalf("seed algorithm snapshot on recognitions: %v", err)
	}
	if _, err := db.Exec(`UPDATE alarms
		SET algorithm_name = 'Det', algorithm_version = '1.0.0'
		WHERE algorithm_id = 'algo_a'`); err != nil {
		t.Fatalf("seed algorithm snapshot on alarms: %v", err)
	}

	if _, err := db.Exec(`DELETE FROM algorithms WHERE id = 'algo_a'`); err != nil {
		t.Fatalf("deleting an algorithm with perception records must succeed: %v", err)
	}

	// 记录仍在，且来源引用被置 NULL。
	var algoRecs int
	if err := db.QueryRow(`SELECT COUNT(*) FROM recognitions
		WHERE id = 'rec_a' AND algorithm_id IS NULL`).Scan(&algoRecs); err != nil {
		t.Fatalf("count surviving recognitions: %v", err)
	}
	if algoRecs != 1 {
		t.Errorf("perception record must survive algorithm deletion with NULL algorithm_id, got %d", algoRecs)
	}
	var algoAlarms int
	if err := db.QueryRow(`SELECT COUNT(*) FROM alarms
		WHERE id = 'alm_a' AND algorithm_id IS NULL`).Scan(&algoAlarms); err != nil {
		t.Fatalf("count surviving alarms: %v", err)
	}
	if algoAlarms != 1 {
		t.Errorf("alarm must survive algorithm deletion with NULL algorithm_id, got %d", algoAlarms)
	}

	// 快照仍能回答「哪个算法产生的」—— 这正是冗余列的全部意义。
	var snapName, snapVersion string
	if err := db.QueryRow(`SELECT algorithm_name, algorithm_version FROM recognitions
		WHERE id = 'rec_a'`).Scan(&snapName, &snapVersion); err != nil {
		t.Fatalf("read algorithm snapshot: %v", err)
	}
	if snapName != "Det" || snapVersion != "1.0.0" {
		t.Errorf("algorithm snapshot lost after uninstall: name=%q version=%q", snapName, snapVersion)
	}

	// The unattributed rows survive untouched.
	var unattributed int
	if err := db.QueryRow(`SELECT COUNT(*) FROM recognitions WHERE algorithm_id IS NULL`).Scan(&unattributed); err != nil {
		t.Fatalf("count unattributed recognitions: %v", err)
	}
	if unattributed != 2 {
		t.Errorf("expected both unattributed recognitions to survive, got %d", unattributed)
	}
}

// TestMigration000016_CaptureAlgorithmRef locks down the captures-side algorithm
// attribution added by migration 16. Unlike recognitions/alarms (which needed a
// table rebuild to fix their foreign keys), captures was a plain ADD COLUMN, so
// the interesting assertions are the column defaults and the SET NULL semantics.
func TestMigration000016_CaptureAlgorithmRef(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_migrate_16.db")

	dsn := fmt.Sprintf("%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", dbPath)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
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

	// 1. Schema to 15, seed a capture written before the column existed.
	if err := migrator.Migrate(15); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(15): %v", err)
	}
	for _, stmt := range []string{
		`INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
		 VALUES ('cam_c', 'C', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO algorithms (id, name, version, task_type, category, target_arch,
		   target_device, compute_cost, memory_mb, description, manifest_json,
		   config_schema_json, status, previous_version, created_at, updated_at)
		 VALUES ('algo_c', 'Intrusion', '3.2.1', 'detection', 'security', 'aarch64',
		   'rknn', 10, 64, '', '{}', '{}', 'ACTIVE', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO captures (id, camera_id, target_type, pts, captured_at, created_at)
		 VALUES ('cap_legacy', 'cam_c', 'person', 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// 2. Apply migration 16.
	if err := migrator.Migrate(16); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("migrator Migrate(16): %v", err)
	}

	// 3. The legacy row survives with NULL id and empty snapshots: historical
	//    captures cannot be attributed retroactively, and guessing would be worse
	//    than leaving it unknown.
	var legacyAlgoID sql.NullString
	var legacyName, legacyVersion string
	if err := db.QueryRow(`SELECT algorithm_id, algorithm_name, algorithm_version
		FROM captures WHERE id = 'cap_legacy'`).
		Scan(&legacyAlgoID, &legacyName, &legacyVersion); err != nil {
		t.Fatalf("read legacy capture: %v", err)
	}
	if legacyAlgoID.Valid {
		t.Errorf("legacy capture must keep NULL algorithm_id, got %q", legacyAlgoID.String)
	}
	if legacyName != "" || legacyVersion != "" {
		t.Errorf("legacy capture must not have guessed snapshots, got name=%q version=%q",
			legacyName, legacyVersion)
	}

	// 4. A new capture can carry full attribution.
	if _, err := db.Exec(`INSERT INTO captures
		(id, camera_id, algorithm_id, algorithm_name, algorithm_version,
		 target_type, pts, captured_at, created_at)
		VALUES ('cap_new', 'cam_c', 'algo_c', 'Intrusion', '3.2.1',
		        'person', 2, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("insert attributed capture: %v", err)
	}

	// 5. Uninstalling the algorithm must NOT delete the capture — it only clears
	//    the reference, exactly like recognitions/alarms.
	if _, err := db.Exec(`DELETE FROM algorithms WHERE id = 'algo_c'`); err != nil {
		t.Fatalf("uninstall algorithm: %v", err)
	}
	var newAlgoID sql.NullString
	var newName, newVersion string
	if err := db.QueryRow(`SELECT algorithm_id, algorithm_name, algorithm_version
		FROM captures WHERE id = 'cap_new'`).
		Scan(&newAlgoID, &newName, &newVersion); err != nil {
		t.Fatalf("capture disappeared after algorithm uninstall: %v", err)
	}
	if newAlgoID.Valid {
		t.Errorf("expected NULL algorithm_id after uninstall, got %q", newAlgoID.String)
	}
	if newName != "Intrusion" || newVersion != "3.2.1" {
		t.Errorf("algorithm snapshot lost: name=%q version=%q", newName, newVersion)
	}

	// 6. A nonexistent algorithm is still rejected: the column is a real FK.
	if _, err := db.Exec(`INSERT INTO captures
		(id, camera_id, algorithm_id, target_type, pts, captured_at, created_at)
		VALUES ('cap_bogus', 'cam_c', 'no_such_algo', 'person', 3,
		        CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("nonexistent algorithm_id must still violate the foreign key")
	}
}
