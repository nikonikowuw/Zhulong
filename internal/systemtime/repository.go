package systemtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Repository manages persistence for system time configurations.
type Repository interface {
	GetConfig(ctx context.Context) (*SystemTimeConfig, []string, error)
	UpdateConfig(ctx context.Context, mode string, ntpServers []string, syncInterval int, timezone string) (*SystemTimeConfig, []string, error)
}

type repositoryImpl struct {
	dbProvider database.DBProvider
}

// NewRepository creates a new persistence repository for system time configs.
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

func defaultServers() []string {
	return []string{"ntp.aliyun.com", "cn.pool.ntp.org", "pool.ntp.org"}
}

// GetConfig retrieves the singleton time configuration (id=1).
func (r *repositoryImpl) GetConfig(ctx context.Context) (*SystemTimeConfig, []string, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, nil, err
	}

	var cfg SystemTimeConfig
	err = db.Where("id = ?", 1).First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Return default configuration
			servers := defaultServers()
			serversBytes, _ := json.Marshal(servers)
			defaultCfg := &SystemTimeConfig{
				ID:                  1,
				Mode:                ModeNTP,
				NTPServersJSON:      string(serversBytes),
				SyncIntervalSeconds: 900,
				Timezone:            "Asia/Shanghai",
				UpdatedAt:           time.Now().UTC(),
			}
			return defaultCfg, servers, nil
		}
		return nil, nil, fmt.Errorf("query system_time_configs: %w", err)
	}

	var servers []string
	if err := json.Unmarshal([]byte(cfg.NTPServersJSON), &servers); err != nil || len(servers) == 0 {
		servers = defaultServers()
	}
	return &cfg, servers, nil
}

// UpdateConfig updates or creates the singleton time configuration.
func (r *repositoryImpl) UpdateConfig(ctx context.Context, mode string, ntpServers []string, syncInterval int, timezone string) (*SystemTimeConfig, []string, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, nil, err
	}

	if len(ntpServers) == 0 {
		ntpServers = defaultServers()
	}
	serversBytes, err := json.Marshal(ntpServers)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal ntp servers: %w", err)
	}

	cfg := SystemTimeConfig{
		ID:                  1,
		Mode:                mode,
		NTPServersJSON:      string(serversBytes),
		SyncIntervalSeconds: syncInterval,
		Timezone:            timezone,
		UpdatedAt:           time.Now().UTC(),
	}

	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"mode", "ntp_servers", "sync_interval_seconds", "timezone", "updated_at"}),
	}).Create(&cfg).Error
	if err != nil {
		return nil, nil, fmt.Errorf("save system_time_configs: %w", err)
	}

	return &cfg, ntpServers, nil
}
