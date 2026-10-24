package systemtime

import (
	"context"
	"slices"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupRepositoryDB(t *testing.T) (database.DBProvider, func()) {
	t.Helper()

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return dbStore, func() {
		_ = dbStore.Close()
	}
}

// TestRepositoryGetConfigDefaults pins the read contract: with no row persisted,
// GetConfig hands back a usable configuration without writing anything.
func TestRepositoryGetConfigDefaults(t *testing.T) {
	dbProvider, cleanup := setupRepositoryDB(t)
	defer cleanup()

	cfg, err := NewRepository(dbProvider).GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}

	if cfg.Mode != ModeNTP {
		t.Errorf("expected the %q mode by default, got %q", ModeNTP, cfg.Mode)
	}
	if cfg.Timezone != defaultTimezone {
		t.Errorf("expected the default timezone %q, got %q", defaultTimezone, cfg.Timezone)
	}
	if cfg.SyncIntervalSeconds != defaultSyncIntervalSeconds {
		t.Errorf("expected the default interval %d, got %d", defaultSyncIntervalSeconds, cfg.SyncIntervalSeconds)
	}
	if !slices.Equal(cfg.NTPServers, defaultServers()) {
		t.Errorf("expected the default NTP pool, got %v", cfg.NTPServers)
	}
	if len(cfg.NTPServersJSON) == 0 {
		t.Error("expected the servers to be encoded for persistence")
	}
}

// TestRepositoryUpdateConfigRoundTrip pins that a write is readable through the
// same single-value read path, including the decoded server list.
func TestRepositoryUpdateConfigRoundTrip(t *testing.T) {
	dbProvider, cleanup := setupRepositoryDB(t)
	defer cleanup()

	repo := NewRepository(dbProvider)
	ctx := context.Background()

	update := TimeConfigUpdate{
		Mode:                ModeManual,
		NTPServers:          []string{"time.example.net"},
		SyncIntervalSeconds: 120,
		Timezone:            "Europe/Berlin",
	}
	if _, err := repo.UpdateConfig(ctx, update); err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	cfg, err := repo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}

	if cfg.Mode != update.Mode || cfg.Timezone != update.Timezone {
		t.Errorf("expected %q/%q, got %q/%q", update.Mode, update.Timezone, cfg.Mode, cfg.Timezone)
	}
	if cfg.SyncIntervalSeconds != update.SyncIntervalSeconds {
		t.Errorf("expected interval %d, got %d", update.SyncIntervalSeconds, cfg.SyncIntervalSeconds)
	}
	if !slices.Equal(cfg.NTPServers, update.NTPServers) {
		t.Errorf("expected servers %v, got %v", update.NTPServers, cfg.NTPServers)
	}
}

// TestRepositoryGetConfigRepairsCorruptServers pins the recovery policy: a
// hand-edited ntp_servers column must not brick time management.
func TestRepositoryGetConfigRepairsCorruptServers(t *testing.T) {
	dbProvider, cleanup := setupRepositoryDB(t)
	defer cleanup()

	ctx := context.Background()
	corruptColumn(t, dbProvider, "{not json")

	got, err := NewRepository(dbProvider).GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if !slices.Equal(got.NTPServers, defaultServers()) {
		t.Errorf("expected the shipped pool as a fallback, got %v", got.NTPServers)
	}
}

// TestRepositoryGetConfigWithoutRow covers the defensive branch the migration's
// seed normally hides: a database whose singleton row was deleted still answers.
func TestRepositoryGetConfigWithoutRow(t *testing.T) {
	dbProvider, cleanup := setupRepositoryDB(t)
	defer cleanup()

	ctx := context.Background()
	db := dbProvider.DB().WithContext(ctx)
	if err := db.Where("id = ?", configSingletonID).Delete(&SystemTimeConfig{}).Error; err != nil {
		t.Fatalf("delete singleton row: %v", err)
	}

	cfg, err := NewRepository(dbProvider).GetConfig(ctx)
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfg.Mode != ModeNTP || !slices.Equal(cfg.NTPServers, defaultServers()) {
		t.Errorf("expected the in-memory defaults, got mode=%q servers=%v", cfg.Mode, cfg.NTPServers)
	}
}

func corruptColumn(t *testing.T, dbProvider database.DBProvider, encoded string) {
	t.Helper()

	ctx := context.Background()
	err := dbProvider.DB().WithContext(ctx).
		Model(&SystemTimeConfig{}).
		Where("id = ?", configSingletonID).
		Update("ntp_servers", encoded).Error
	if err != nil {
		t.Fatalf("corrupt the ntp_servers column: %v", err)
	}
}
