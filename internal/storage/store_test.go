package storage

import (
	"context"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupTestRepo(t *testing.T) (Repository, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	repo := NewRepository(dbStore)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return repo, cleanup
}

func TestStore_GetDefaultConfig(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	cfg, err := repo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("unexpected error getting default config: %v", err)
	}
	if cfg.MediaDirectory != "data/media" {
		t.Errorf("expected default media directory 'data/media', got %q", cfg.MediaDirectory)
	}
	if cfg.RecordingsRetentionDays != 15 {
		t.Errorf("expected 15 recordings retention days, got %d", cfg.RecordingsRetentionDays)
	}
	if cfg.HighWatermarkPercent != 90 {
		t.Errorf("expected 90 high watermark, got %d", cfg.HighWatermarkPercent)
	}
	if cfg.LowWatermarkPercent != 80 {
		t.Errorf("expected 80 low watermark, got %d", cfg.LowWatermarkPercent)
	}
}

func TestStore_UpdateAndGetConfig(t *testing.T) {
	repo, cleanup := setupTestRepo(t)
	defer cleanup()

	ctx := context.Background()
	newCfg := StorageConfig{
		MediaDirectory:          "/mnt/storage/media",
		RecordingsRetentionDays: 30,
		SnapshotsRetentionDays:  180,
		ExportsRetentionHours:   72,
		HighWatermarkPercent:    88,
		LowWatermarkPercent:     75,
		EmergencyStopPercent:    96,
		EmergencyStopMinMB:      4096,
	}

	saved, err := repo.UpdateConfig(ctx, newCfg)
	if err != nil {
		t.Fatalf("unexpected error updating config: %v", err)
	}
	if saved.MediaDirectory != "/mnt/storage/media" {
		t.Errorf("expected saved media dir '/mnt/storage/media', got %q", saved.MediaDirectory)
	}

	fetched, err := repo.GetConfig(ctx)
	if err != nil {
		t.Fatalf("unexpected error fetching updated config: %v", err)
	}
	if fetched.MediaDirectory != "/mnt/storage/media" {
		t.Errorf("expected fetched media dir '/mnt/storage/media', got %q", fetched.MediaDirectory)
	}
	if fetched.RecordingsRetentionDays != 30 {
		t.Errorf("expected 30 days, got %d", fetched.RecordingsRetentionDays)
	}
	if fetched.HighWatermarkPercent != 88 {
		t.Errorf("expected 88 high watermark, got %d", fetched.HighWatermarkPercent)
	}
}

func TestStore_ValidateConfigConstraints(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(c *StorageConfig)
		wantErr bool
	}{
		{
			name:    "valid config",
			modify:  func(c *StorageConfig) {},
			wantErr: false,
		},
		{
			name: "empty directory",
			modify: func(c *StorageConfig) {
				c.MediaDirectory = "   "
			},
			wantErr: true,
		},
		{
			name: "recordings retention zero",
			modify: func(c *StorageConfig) {
				c.RecordingsRetentionDays = 0
			},
			wantErr: true,
		},
		{
			name: "snapshots retention too large",
			modify: func(c *StorageConfig) {
				c.SnapshotsRetentionDays = 1000
			},
			wantErr: true,
		},
		{
			name: "low watermark >= high watermark",
			modify: func(c *StorageConfig) {
				c.LowWatermarkPercent = 90
				c.HighWatermarkPercent = 90
			},
			wantErr: true,
		},
		{
			name: "emergency stop < high watermark",
			modify: func(c *StorageConfig) {
				c.HighWatermarkPercent = 95
				c.EmergencyStopPercent = 92
			},
			wantErr: true,
		},
		{
			name: "emergency stop min MB too small",
			modify: func(c *StorageConfig) {
				c.EmergencyStopMinMB = 50
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultStorageConfig()
			tt.modify(&cfg)
			err := ValidateConfig(&cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestStore_NilDBProvider(t *testing.T) {
	repo := NewRepository(nil)
	ctx := context.Background()

	_, err := repo.GetConfig(ctx)
	if err == nil {
		t.Error("expected error with nil dbProvider, got nil")
	}

	_, err = repo.UpdateConfig(ctx, DefaultStorageConfig())
	if err == nil {
		t.Error("expected error updating with nil dbProvider, got nil")
	}
}
