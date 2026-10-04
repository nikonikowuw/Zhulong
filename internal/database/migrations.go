package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	migratesqlite3 "github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"go.uber.org/zap"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migrationRunner interface {
	up(context.Context, string) error
}

type embeddedMigrationRunner struct {
	logger *zap.Logger
}

func (r embeddedMigrationRunner) up(ctx context.Context, dsn string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	database, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return fmt.Errorf("open SQLite migration connection: %w", err)
	}
	database.SetMaxOpenConns(1)
	databaseDriver, err := migratesqlite3.WithInstance(database, &migratesqlite3.Config{})
	if err != nil {
		return errors.Join(fmt.Errorf("create SQLite migration driver: %w", err), database.Close())
	}

	sourceDriver, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return errors.Join(fmt.Errorf("create embedded migration source: %w", err), databaseDriver.Close())
	}
	migrator, err := migrate.NewWithInstance("iofs", sourceDriver, "sqlite3", databaseDriver)
	if err != nil {
		return errors.Join(fmt.Errorf("create migration runner: %w", err), sourceDriver.Close(), databaseDriver.Close())
	}

	migrationErr := migrator.Up()
	if errors.Is(migrationErr, migrate.ErrNoChange) {
		migrationErr = nil
	}
	if err := ctx.Err(); err != nil {
		migrationErr = errors.Join(migrationErr, err)
	}
	sourceErr, databaseErr := migrator.Close()
	if migrationErr != nil && r.logger != nil {
		r.logger.Error("database migration failed", zap.Error(migrationErr))
	}
	return errors.Join(migrationErr, sourceErr, databaseErr)
}
