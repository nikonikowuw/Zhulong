package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

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
		return fmt.Errorf(
			"emergency stop percent (%d%%) must be greater than or equal to high watermark (%d%%)",
			cfg.EmergencyStopPercent,
			cfg.HighWatermarkPercent,
		)
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
