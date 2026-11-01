package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	logger     *zap.Logger
}

// NewRepository creates a new persistence repository for storage settings.
func NewRepository(dbProvider database.DBProvider, logger *zap.Logger) Repository {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &repositoryImpl{
		dbProvider: dbProvider,
		logger:     logger,
	}
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
		r.logger.Warn("stored storage config is unusable; falling back to defaults",
			zap.String("key", metadataKeyStorageConfig),
			zap.Error(err),
		)

		return defaultConfig(), nil
	}

	return cfg, nil
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
