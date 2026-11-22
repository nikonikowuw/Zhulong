package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	metadataKeyStorageConfig = "storage_config"
	s3SecretCipherAAD        = "storage:s3_secret"
)

// ErrS3SecretRequired is returned when S3 backend is enabled without providing
// or having a previously persisted encrypted secret access key.
var ErrS3SecretRequired = errors.New("s3 secret access key is required")

// SecretCipher defines symmetric encryption capability for stored object-store
// credentials without coupling storage to internal/camera.
type SecretCipher interface {
	Encrypt(plaintext []byte, aad string) ([]byte, error)
	Decrypt(ciphertext []byte, aad string) ([]byte, error)
}

// Repository manages persistence and retrieval for storage configurations.
type Repository interface {
	GetConfig(ctx context.Context) (*StorageConfig, error)
	UpdateConfig(ctx context.Context, cfg StorageConfig) (*StorageConfig, error)
}

// SecretAwareRepository is an optional extension implemented by the production
// SQLite repository to manage encrypted remote storage credentials.
type SecretAwareRepository interface {
	HasExistingEncryptedData(ctx context.Context) (bool, error)
	GetDecryptedS3Config(ctx context.Context) (*filestore.S3Config, error)
}

type storedStorageRecord struct {
	StorageConfig
	S3SecretCiphertext string `json:"s3SecretCiphertext,omitempty"`
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
	cipher     SecretCipher
	logger     *zap.Logger
}

// NewRepository creates a new persistence repository for storage settings.
func NewRepository(dbProvider database.DBProvider, logger *zap.Logger) Repository {
	return NewRepositoryWithCipher(dbProvider, nil, logger)
}

// NewRepositoryWithCipher creates a storage repository capable of encrypting and
// decrypting remote backend credentials via cipher.
func NewRepositoryWithCipher(
	dbProvider database.DBProvider,
	cipher SecretCipher,
	logger *zap.Logger,
) Repository {
	if logger == nil {
		logger = zap.NewNop()
	}

	return &repositoryImpl{
		dbProvider: dbProvider,
		cipher:     cipher,
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
		r.logger.Warn(
			"stored storage config is unusable; falling back to defaults",
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

	var prevCiphertext string
	if existing, loadErr := r.loadStoredRecord(ctx, db); loadErr == nil && existing != nil {
		prevCiphertext = existing.S3SecretCiphertext
	}

	secretCiphertext, err := r.resolveSecretCiphertext(cfg, prevCiphertext)
	if err != nil {
		return nil, err
	}

	sanitized := sanitizeConfig(cfg, secretCiphertext != "")
	record := storedStorageRecord{
		StorageConfig:      sanitized,
		S3SecretCiphertext: secretCiphertext,
	}

	data, err := json.Marshal(record)
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

	return &sanitized, nil
}

// HasExistingEncryptedData reports whether storage_config currently holds an
// encrypted S3 credential so camera.key is never silently regenerated over it.
func (r *repositoryImpl) HasExistingEncryptedData(ctx context.Context) (bool, error) {
	db, err := r.db(ctx)
	if err != nil {
		return false, err
	}
	rec, err := r.loadStoredRecord(ctx, db)
	if err != nil {
		return false, err
	}
	return rec != nil && rec.S3SecretCiphertext != "", nil
}

// GetDecryptedS3Config resolves and decrypts the persisted S3 configuration.
func (r *repositoryImpl) GetDecryptedS3Config(ctx context.Context) (*filestore.S3Config, error) {
	db, err := r.db(ctx)
	if err != nil {
		return nil, err
	}
	rec, err := r.loadStoredRecord(ctx, db)
	if err != nil {
		return nil, err
	}
	if rec == nil || rec.S3 == nil {
		return nil, ErrS3SecretRequired
	}
	if rec.S3SecretCiphertext == "" {
		return nil, ErrS3SecretRequired
	}
	if r.cipher == nil {
		return nil, errors.New("storage secret cipher is not configured")
	}

	rawCiphertext, err := base64.StdEncoding.DecodeString(rec.S3SecretCiphertext)
	if err != nil {
		return nil, fmt.Errorf("decode s3 secret ciphertext: %w", err)
	}
	plaintext, err := r.cipher.Decrypt(rawCiphertext, s3SecretCipherAAD)
	if err != nil {
		return nil, fmt.Errorf("decrypt s3 secret: %w", err)
	}

	return &filestore.S3Config{
		Endpoint:           rec.S3.Endpoint,
		Region:             rec.S3.Region,
		Bucket:             rec.S3.Bucket,
		Prefix:             rec.S3.Prefix,
		AccessKeyID:        rec.S3.AccessKeyID,
		SecretAccessKey:    string(plaintext),
		UsePathStyle:       rec.S3.UsePathStyle,
		UseTLS:             rec.S3.UseTLS,
		InsecureSkipVerify: rec.S3.InsecureSkipVerify,
	}, nil
}

func (r *repositoryImpl) loadStoredRecord(
	ctx context.Context,
	db *gorm.DB,
) (*storedStorageRecord, error) {
	row, err := gorm.G[systemMetadataRow](db).Where("key = ?", metadataKeyStorageConfig).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("query storage_config metadata: %w", err)
	}
	var rec storedStorageRecord
	if err := json.Unmarshal([]byte(row.Value), &rec); err != nil {
		return nil, fmt.Errorf("unmarshal storage_config metadata: %w", err)
	}
	return &rec, nil
}

func (r *repositoryImpl) resolveSecretCiphertext(
	cfg StorageConfig,
	prevCiphertext string,
) (string, error) {
	if cfg.S3 != nil && cfg.S3.SecretAccessKey != "" {
		if r.cipher == nil {
			return "", errors.New("storage secret cipher is not configured")
		}
		encrypted, err := r.cipher.Encrypt([]byte(cfg.S3.SecretAccessKey), s3SecretCipherAAD)
		if err != nil {
			return "", fmt.Errorf("encrypt s3 secret: %w", err)
		}
		return base64.StdEncoding.EncodeToString(encrypted), nil
	}

	if cfg.Backend == BackendS3 && prevCiphertext == "" {
		return "", ErrS3SecretRequired
	}
	return prevCiphertext, nil
}
