package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

const (
	minSpoolMaxMB = int64(128)
	maxSpoolMaxMB = int64(1048576)
)

// ValidateConfig normalizes defaults and paths in place, then enforces the
// storage domain invariants. Callers must treat cfg as an input/output argument.
func ValidateConfig(cfg *StorageConfig) error {
	if cfg == nil {
		return errors.New("storage config cannot be nil")
	}

	if err := normalizeAndValidateBackend(cfg); err != nil {
		return err
	}
	if err := validateRetentionAndWatermarks(cfg); err != nil {
		return err
	}

	if cfg.Backend == BackendS3 {
		if err := ValidateS3Config(cfg.S3); err != nil {
			return err
		}
	}
	return nil
}

// ValidateS3Config normalizes and validates S3/MinIO connection parameters
// (excluding the secret itself, which may be preserved from encrypted storage).
func ValidateS3Config(s3 *S3Config) error {
	if s3 == nil {
		return errors.New("s3 configuration is required when backend is s3")
	}

	endpoint := strings.TrimSpace(s3.Endpoint)
	if endpoint == "" {
		return errors.New("s3 endpoint cannot be empty")
	}
	if strings.Contains(endpoint, "://") {
		parsed, err := url.Parse(endpoint)
		if err != nil || parsed.Host == "" {
			return errors.New("s3 endpoint URL is invalid")
		}
		scheme := strings.ToLower(parsed.Scheme)
		if scheme != "http" && scheme != "https" {
			return errors.New("s3 endpoint scheme must be http or https")
		}
		if (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("s3 endpoint must not include a path")
		}
		s3.UseTLS = scheme == "https"
	} else {
		host := strings.TrimRight(endpoint, "/")
		if strings.Contains(host, "/") {
			return errors.New("s3 endpoint must not include a path")
		}
		endpoint = host
	}
	s3.Endpoint = endpoint
	s3.Region = strings.TrimSpace(s3.Region)
	if s3.Region == "" {
		s3.Region = "us-east-1"
	}
	s3.Bucket = strings.TrimSpace(s3.Bucket)
	if s3.Bucket == "" {
		return errors.New("s3 bucket cannot be empty")
	}
	s3.Prefix = strings.Trim(strings.TrimSpace(s3.Prefix), "/")
	s3.AccessKeyID = strings.TrimSpace(s3.AccessKeyID)
	if s3.AccessKeyID == "" {
		return errors.New("s3 accessKeyId cannot be empty")
	}
	if s3.QuotaGB < 0 {
		return fmt.Errorf("s3 quotaGb must be non-negative, got %d", s3.QuotaGB)
	}
	return nil
}

func normalizeAndValidateBackend(cfg *StorageConfig) error {
	backend := strings.ToLower(strings.TrimSpace(cfg.Backend))
	if backend == "" {
		backend = BackendLocal
	}
	switch backend {
	case BackendLocal, BackendS3:
	default:
		return fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
	cfg.Backend = backend

	delivery := strings.ToLower(strings.TrimSpace(cfg.DeliveryMode))
	if delivery == "" {
		delivery = DeliveryModeProxy
	}
	switch delivery {
	case DeliveryModeProxy, DeliveryModeRedirect:
	default:
		return fmt.Errorf("unsupported delivery mode %q", cfg.DeliveryMode)
	}
	cfg.DeliveryMode = delivery

	rawMedia := strings.TrimSpace(cfg.MediaDirectory)
	if rawMedia == "" && cfg.Backend == BackendS3 {
		rawMedia = "data/media"
	}
	cleanDir := filepath.Clean(rawMedia)
	if rawMedia == "" || cleanDir == "" || cleanDir == "." {
		return errors.New("media directory cannot be empty")
	}
	cfg.MediaDirectory = cleanDir

	rawSpool := strings.TrimSpace(cfg.SpoolDirectory)
	if rawSpool == "" {
		rawSpool = DefaultSpoolDirectory
	}
	cleanSpool := filepath.Clean(rawSpool)
	if cleanSpool == "" || cleanSpool == "." {
		return errors.New("spool directory cannot be empty")
	}
	cfg.SpoolDirectory = cleanSpool

	if cfg.SpoolMaxMB == 0 {
		cfg.SpoolMaxMB = DefaultSpoolMaxMB
	}
	if cfg.SpoolMaxMB < minSpoolMaxMB || cfg.SpoolMaxMB > maxSpoolMaxMB {
		return fmt.Errorf(
			"spool max MB must be between %d and %d, got %d",
			minSpoolMaxMB,
			maxSpoolMaxMB,
			cfg.SpoolMaxMB,
		)
	}
	return nil
}

func validateRetentionAndWatermarks(cfg *StorageConfig) error {
	if cfg.RecordingsRetentionDays < 1 || cfg.RecordingsRetentionDays > 365 {
		return fmt.Errorf(
			"recordings retention days must be between 1 and 365, got %d",
			cfg.RecordingsRetentionDays,
		)
	}
	if cfg.SnapshotsRetentionDays < 1 || cfg.SnapshotsRetentionDays > 730 {
		return fmt.Errorf(
			"snapshots retention days must be between 1 and 730, got %d",
			cfg.SnapshotsRetentionDays,
		)
	}
	if cfg.ExportsRetentionHours < 1 || cfg.ExportsRetentionHours > 168 {
		return fmt.Errorf(
			"exports retention hours must be between 1 and 168, got %d",
			cfg.ExportsRetentionHours,
		)
	}
	if cfg.HighWatermarkPercent < 50 || cfg.HighWatermarkPercent > 95 {
		return fmt.Errorf(
			"high watermark percent must be between 50 and 95, got %d",
			cfg.HighWatermarkPercent,
		)
	}
	if cfg.LowWatermarkPercent < 40 || cfg.LowWatermarkPercent > 85 {
		return fmt.Errorf(
			"low watermark percent must be between 40 and 85, got %d",
			cfg.LowWatermarkPercent,
		)
	}
	if cfg.LowWatermarkPercent >= cfg.HighWatermarkPercent {
		return fmt.Errorf(
			"low watermark (%d%%) must be strictly less than high watermark (%d%%)",
			cfg.LowWatermarkPercent,
			cfg.HighWatermarkPercent,
		)
	}
	if cfg.EmergencyStopPercent < 90 || cfg.EmergencyStopPercent > 99 {
		return fmt.Errorf(
			"emergency stop percent must be between 90 and 99, got %d",
			cfg.EmergencyStopPercent,
		)
	}
	if cfg.EmergencyStopPercent < cfg.HighWatermarkPercent {
		return fmt.Errorf(
			"emergency stop percent (%d%%) must be greater than or equal to high watermark (%d%%)",
			cfg.EmergencyStopPercent,
			cfg.HighWatermarkPercent,
		)
	}
	if cfg.EmergencyStopMinMB < 100 || cfg.EmergencyStopMinMB > 102400 {
		return fmt.Errorf(
			"emergency stop min MB must be between 100 and 102400, got %d",
			cfg.EmergencyStopMinMB,
		)
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
	rec, err := decodeStoredRecord(raw)
	if err != nil {
		return nil, err
	}
	cfg := sanitizeConfig(rec.StorageConfig, rec.S3SecretCiphertext != "")
	return &cfg, nil
}

func decodeStoredRecord(raw string) (*storedStorageRecord, error) {
	var rec storedStorageRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal storage config: %w", err)
	}
	if err := ValidateConfig(&rec.StorageConfig); err != nil {
		return nil, fmt.Errorf("validate stored storage config: %w", err)
	}
	return &rec, nil
}

func sanitizeConfig(cfg StorageConfig, hasStoredSecret bool) StorageConfig {
	if cfg.S3 == nil {
		return cfg
	}
	s3Copy := *cfg.S3
	s3Copy.SecretAccessKey = ""
	s3Copy.HasSecretAccessKey = hasStoredSecret
	cfg.S3 = &s3Copy
	return cfg
}
