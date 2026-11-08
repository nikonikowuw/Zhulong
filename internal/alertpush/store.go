package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
)

var (
	ErrDestinationNotFound     = errors.New("alert destination not found")
	ErrDestinationNameConflict = errors.New("alert destination name conflict")
	ErrClientIDConflict        = errors.New("mqtt client id conflict")
)

// Cipher defines symmetric encryption capability for credentials.
type Cipher interface {
	Encrypt(plaintext []byte, aad string) ([]byte, error)
	Decrypt(ciphertext []byte, aad string) ([]byte, error)
}

// Store handles persistence operations for alert destinations.
type Store struct {
	dbProvider database.DBProvider
	cipher     Cipher
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider, cipher Cipher) *Store {
	return &Store{
		dbProvider: dbProvider,
		cipher:     cipher,
	}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, errors.New("database provider is nil")
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, errors.New("database is not ready")
	}
	return db.WithContext(ctx), nil
}

// HasExistingEncryptedData reports whether any destination holds encrypted secrets.
// Implements camera.EncryptedDataProbe to prevent key regeneration while data exists.
func (s *Store) HasExistingEncryptedData(ctx context.Context) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	count, err := gorm.G[AlertDestination](db).
		Where("secret_ciphertext IS NOT NULL AND length(secret_ciphertext) > 0").
		Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("check alert destination ciphertext: %w", err)
	}
	return count > 0, nil
}

// EncryptSecret encrypts the given secret struct with AAD bound to alert-destination:<id>.
func (s *Store) EncryptSecret(id string, secret any) ([]byte, error) {
	if s.cipher == nil {
		return nil, errors.New("cipher is not configured")
	}
	data, err := json.Marshal(secret)
	if err != nil {
		return nil, fmt.Errorf("marshal secret: %w", err)
	}
	aad := "alert-destination:" + id
	return s.cipher.Encrypt(data, aad)
}

// DecryptWebhookSecret decrypts ciphertext into webhookSecret.
func (s *Store) DecryptWebhookSecret(id string, ciphertext []byte) (*webhookSecret, error) {
	if len(ciphertext) == 0 {
		return &webhookSecret{}, nil
	}
	if s.cipher == nil {
		return nil, errors.New("cipher is not configured")
	}
	aad := "alert-destination:" + id
	plaintext, err := s.cipher.Decrypt(ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt webhook secret: %w", err)
	}
	var secret webhookSecret
	if err := json.Unmarshal(plaintext, &secret); err != nil {
		return nil, fmt.Errorf("unmarshal webhook secret: %w", err)
	}
	return &secret, nil
}

// DecryptMQTTSecret decrypts ciphertext into mqttSecret.
func (s *Store) DecryptMQTTSecret(id string, ciphertext []byte) (*mqttSecret, error) {
	if len(ciphertext) == 0 {
		return &mqttSecret{}, nil
	}
	if s.cipher == nil {
		return nil, errors.New("cipher is not configured")
	}
	aad := "alert-destination:" + id
	plaintext, err := s.cipher.Decrypt(ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("decrypt mqtt secret: %w", err)
	}
	var secret mqttSecret
	if err := json.Unmarshal(plaintext, &secret); err != nil {
		return nil, fmt.Errorf("unmarshal mqtt secret: %w", err)
	}
	return &secret, nil
}

// Create inserts a new alert destination.
func (s *Store) Create(ctx context.Context, dest *AlertDestination) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	dest.CreatedAt = now
	dest.UpdatedAt = now
	if err := db.Create(dest).Error; err != nil {
		if conflictErr := classifyStoreConflict(err); conflictErr != nil {
			return conflictErr
		}
		return fmt.Errorf("create alert destination: %w", err)
	}
	return nil
}

// Update updates an existing alert destination with explicit columns to avoid zero-value omission.
func (s *Store) Update(ctx context.Context, dest *AlertDestination) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	dest.UpdatedAt = time.Now().UTC()
	result := db.Model(&AlertDestination{}).
		Where("id = ?", dest.ID).
		Select(
			"name",
			"channel_type",
			"enabled",
			"image_mode",
			"mqtt_client_id",
			"config_json",
			"secret_ciphertext",
			"filter_json",
			"updated_at",
		).
		Updates(dest)
	if result.Error != nil {
		if conflictErr := classifyStoreConflict(result.Error); conflictErr != nil {
			return conflictErr
		}
		return fmt.Errorf("update alert destination: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

func classifyStoreConflict(err error) error {
	if err == nil {
		return nil
	}
	errStr := err.Error()
	if strings.Contains(errStr, "ux_alert_destinations_name") ||
		strings.Contains(errStr, "alert_destinations.name") {
		return ErrDestinationNameConflict
	}
	if strings.Contains(errStr, "ux_alert_destinations_mqtt_client_id") ||
		strings.Contains(errStr, "alert_destinations.mqtt_client_id") {
		return ErrClientIDConflict
	}
	return nil
}

// SetEnabled toggles destination enabled state.
func (s *Store) SetEnabled(ctx context.Context, id string, enabled bool) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result := db.Model(&AlertDestination{}).
		Where("id = ?", id).
		Select("enabled", "updated_at").
		Updates(map[string]any{
			"enabled":    enabled,
			"updated_at": now,
		})
	if result.Error != nil {
		return fmt.Errorf("set alert destination enabled: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}

// GetByID queries a destination by primary key ID.
func (s *Store) GetByID(ctx context.Context, id string) (*AlertDestination, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	dest, err := gorm.G[AlertDestination](db).Where("id = ?", id).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDestinationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get alert destination by id: %w", err)
	}
	return &dest, nil
}

// GetByName queries a destination by name.
func (s *Store) GetByName(ctx context.Context, name string) (*AlertDestination, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	dest, err := gorm.G[AlertDestination](db).Where("name = ?", name).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDestinationNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get alert destination by name: %w", err)
	}
	return &dest, nil
}

// CheckMQTTClientIDConflict checks if another destination holds the clientID.
func (s *Store) CheckMQTTClientIDConflict(ctx context.Context, excludeID, clientID string) (bool, error) {
	if clientID == "" {
		return false, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	query := gorm.G[AlertDestination](db).
		Where("channel_type = ? AND mqtt_client_id = ?", ChannelTypeMQTT, clientID)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	count, err := query.Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("check mqtt client id conflict: %w", err)
	}
	return count > 0, nil
}

// List returns all alert destinations ordered by created_at asc.
func (s *Store) List(ctx context.Context) ([]*AlertDestination, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	items, err := gorm.G[AlertDestination](db).Order("created_at asc").Find(ctx)
	if err != nil {
		return nil, fmt.Errorf("list alert destinations: %w", err)
	}
	res := make([]*AlertDestination, len(items))
	for i := range items {
		res[i] = &items[i]
	}
	return res, nil
}

// ListEnabled returns all enabled alert destinations.
func (s *Store) ListEnabled(ctx context.Context) ([]*AlertDestination, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	items, err := gorm.G[AlertDestination](db).Where("enabled = ?", true).Order("created_at asc").Find(ctx)
	if err != nil {
		return nil, fmt.Errorf("list enabled alert destinations: %w", err)
	}
	res := make([]*AlertDestination, len(items))
	for i := range items {
		res[i] = &items[i]
	}
	return res, nil
}

// Delete removes an alert destination by ID.
func (s *Store) Delete(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	result := db.Where("id = ?", id).Delete(&AlertDestination{})
	if result.Error != nil {
		return fmt.Errorf("delete alert destination: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrDestinationNotFound
	}
	return nil
}
