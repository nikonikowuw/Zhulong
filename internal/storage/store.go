package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	metadataKeyStorageConfig = "storage_config"
)

// Repository manages persistence and retrieval for storage configurations.
type Repository interface {
	GetConfig(ctx context.Context) (*StorageConfig, error)
	UpdateConfig(ctx context.Context, cfg StorageConfig) (*StorageConfig, error)
}

type systemMetadataRow struct {
	Key       string    `gorm:"primaryKey;column:key"`
	Value     string    `gorm:"column:value"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

func (systemMetadataRow) TableName() string {
	return "system_metadata"
}

type repositoryImpl struct {
	dbProvider database.DBProvider
}

// NewRepository creates a new persistence repository for storage settings.
func NewRepository(dbProvider database.DBProvider) Repository {
	return &repositoryImpl{dbProvider: dbProvider}
}

func (r *repositoryImpl) db(ctx context.Context) (*gorm.DB, error) {
	if r.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := r.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

// ValidateConfig normalizes the media directory in place, then enforces the
// domain invariants. Callers must treat the argument as an output as well.
func ValidateConfig(cfg *StorageConfig) error {
	if cfg == nil {
		return errors.New("storage config cannot be nil")
	}

	cleanDir := filepath.Clean(strings.TrimSpace(cfg.MediaDirectory))
	if cleanDir == "" || cleanDir == "." {
		return errors.New("media directory cannot be empty")
	}
	cfg.MediaDirectory = cleanDir

	if cfg.RecordingsRetentionDays < 1 || cfg.RecordingsRetentionDays > 365 {
		return fmt.Errorf("recordings retention days must be between 1 and 365, got %d",
			cfg.RecordingsRetentionDays)
	}
	if cfg.SnapshotsRetentionDays < 1 || cfg.SnapshotsRetentionDays > 730 {
		return fmt.Errorf("snapshots retention days must be between 1 and 730, got %d",
			cfg.SnapshotsRetentionDays)
	}
	if cfg.ExportsRetentionHours < 1 || cfg.ExportsRetentionHours > 168 {
		return fmt.Errorf("exports retention hours must be between 1 and 168, got %d",
			cfg.ExportsRetentionHours)
	}
	if cfg.HighWatermarkPercent < 50 || cfg.HighWatermarkPercent > 95 {
		return fmt.Errorf("high watermark percent must be between 50 and 95, got %d",
			cfg.HighWatermarkPercent)
	}
	if cfg.LowWatermarkPercent < 40 || cfg.LowWatermarkPercent > 85 {
		return fmt.Errorf("low watermark percent must be between 40 and 85, got %d",
			cfg.LowWatermarkPercent)
	}
	if cfg.LowWatermarkPercent >= cfg.HighWatermarkPercent {
		return fmt.Errorf("low watermark (%d%%) must be strictly less than high watermark (%d%%)",
			cfg.LowWatermarkPercent, cfg.HighWatermarkPercent)
	}
	if cfg.EmergencyStopPercent < 90 || cfg.EmergencyStopPercent > 99 {
		return fmt.Errorf("emergency stop percent must be between 90 and 99, got %d",
			cfg.EmergencyStopPercent)
	}
	if cfg.EmergencyStopPercent < cfg.HighWatermarkPercent {
		return fmt.Errorf("emergency stop percent (%d%%) must be greater than or equal to high watermark (%d%%)",
			cfg.EmergencyStopPercent, cfg.HighWatermarkPercent)
	}
	if cfg.EmergencyStopMinMB < 100 || cfg.EmergencyStopMinMB > 102400 {
		return fmt.Errorf("emergency stop min MB must be between 100 and 102400, got %d",
			cfg.EmergencyStopMinMB)
	}

	return nil
}

// defaultConfig returns the fallback policy used when nothing valid is stored.
func defaultConfig() *StorageConfig {
	fallback := DefaultStorageConfig()
	return &fallback
}

// GetConfig retrieves the current storage configuration, falling back to defaults
// when no row exists yet.
func (r *repositoryImpl) GetConfig(ctx context.Context) (*StorageConfig, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, err
	}

	row, err := gorm.G[systemMetadataRow](db).Where("key = ?", metadataKeyStorageConfig).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return defaultConfig(), nil
		}

		return nil, fmt.Errorf("query storage_config metadata: %w", err)
	}

	cfg, err := decodeStoredConfig(row.Value)
	if err != nil {
		// A corrupt row must not take recording down with it, but reverting to
		// defaults silently would hide the corruption from operators entirely.
		zap.L().Warn("stored storage config is unusable; falling back to defaults",
			zap.String("key", metadataKeyStorageConfig),
			zap.Error(err),
		)

		return defaultConfig(), nil
	}

	return cfg, nil
}

// decodeStoredConfig parses and validates one persisted config row.
func decodeStoredConfig(raw string) (*StorageConfig, error) {
	var cfg StorageConfig
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal storage config: %w", err)
	}
	if err := ValidateConfig(&cfg); err != nil {
		return nil, fmt.Errorf("validate stored storage config: %w", err)
	}

	return &cfg, nil
}

// UpdateConfig persists the validated storage configuration atomically.
func (r *repositoryImpl) UpdateConfig(ctx context.Context, cfg StorageConfig) (*StorageConfig, error) {
	if err := ValidateConfig(&cfg); err != nil {
		return nil, err
	}

	db, err := r.db(ctx)
	if err != nil {
		return nil, err
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal storage config: %w", err)
	}

	now := time.Now().UTC()
	row := systemMetadataRow{
		Key:       metadataKeyStorageConfig,
		Value:     string(data),
		CreatedAt: now,
		UpdatedAt: now,
	}

	err = gorm.G[systemMetadataRow](db, clause.OnConflict{
		Columns: []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"value":      row.Value,
			"updated_at": row.UpdatedAt,
		}),
	}).Create(ctx, &row)
	if err != nil {
		return nil, fmt.Errorf("upsert storage_config metadata: %w", err)
	}

	return &cfg, nil
}
