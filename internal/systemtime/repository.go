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

const (
	// configSingletonID is the only row the time configuration ever uses.
	configSingletonID = 1
	// defaultSyncIntervalSeconds matches the column default.
	defaultSyncIntervalSeconds = 900
	defaultTimezone            = "Asia/Shanghai"
)

// Repository manages persistence for system time configurations.
type Repository interface {
	GetConfig(ctx context.Context) (*SystemTimeConfig, error)
	UpdateConfig(ctx context.Context, update TimeConfigUpdate) (*SystemTimeConfig, error)
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

// GetConfig retrieves the singleton time configuration (id=1). When the row is
// missing it returns the in-memory defaults without writing them: the first
// UpdateConfig is what persists the row.
func (r *repositoryImpl) GetConfig(ctx context.Context) (*SystemTimeConfig, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, err
	}

	var cfg SystemTimeConfig
	if err := db.Where("id = ?", configSingletonID).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return defaultConfig()
		}
		return nil, fmt.Errorf("query system_time_configs: %w", err)
	}

	// A corrupt or hand-edited column must not brick time management; fall back
	// to the shipped pool and let the next write repair the row.
	servers, err := decodeServers(cfg.NTPServersJSON)
	if err != nil || len(servers) == 0 {
		servers = defaultServers()
	}
	cfg.NTPServers = servers
	return &cfg, nil
}

// UpdateConfig updates or creates the singleton time configuration.
func (r *repositoryImpl) UpdateConfig(ctx context.Context, update TimeConfigUpdate) (*SystemTimeConfig, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, err
	}

	servers := update.NTPServers
	if len(servers) == 0 {
		servers = defaultServers()
	}
	serversJSON, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("marshal ntp servers: %w", err)
	}

	cfg := SystemTimeConfig{
		ID:                  configSingletonID,
		Mode:                update.Mode,
		NTPServersJSON:      string(serversJSON),
		SyncIntervalSeconds: update.SyncIntervalSeconds,
		Timezone:            update.Timezone,
		UpdatedAt:           time.Now().UTC(),
		NTPServers:          servers,
	}

	err = db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "id"}},
		DoUpdates: clause.AssignmentColumns([]string{"mode", "ntp_servers", "sync_interval_seconds", "timezone", "updated_at"}),
	}).Create(&cfg).Error
	if err != nil {
		return nil, fmt.Errorf("save system_time_configs: %w", err)
	}

	return &cfg, nil
}

func defaultConfig() (*SystemTimeConfig, error) {
	servers := defaultServers()
	serversJSON, err := json.Marshal(servers)
	if err != nil {
		return nil, fmt.Errorf("marshal default ntp servers: %w", err)
	}
	return &SystemTimeConfig{
		ID:                  configSingletonID,
		Mode:                ModeNTP,
		NTPServersJSON:      string(serversJSON),
		SyncIntervalSeconds: defaultSyncIntervalSeconds,
		Timezone:            defaultTimezone,
		UpdatedAt:           time.Now().UTC(),
		NTPServers:          servers,
	}, nil
}

func decodeServers(encoded string) ([]string, error) {
	var servers []string
	if err := json.Unmarshal([]byte(encoded), &servers); err != nil {
		return nil, fmt.Errorf("decode ntp servers: %w", err)
	}
	return servers, nil
}
