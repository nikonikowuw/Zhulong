package systeminfo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	// ActionIdentityUpdate is the audit action recorded when an operator edits the
	// host identity. It lives here, next to the only writer, mirroring how every
	// other domain declares its own Action* constants.
	ActionIdentityUpdate = "system.identity.update"

	// CodeIdentityInvalid is the stable machine code for a rejected device_id.
	CodeIdentityInvalid = "SYSTEM_IDENTITY_INVALID"

	// CodeIdentityUnavailable reports that an accepted device_id could not be
	// persisted. It is deliberately separate from CodeIdentityInvalid: a 503 must not
	// carry the "your input is malformed" wording.
	CodeIdentityUnavailable = "SYSTEM_IDENTITY_UNAVAILABLE"

	// CodeTelemetryUnavailable reports that no hardware sample could be produced at
	// all — nothing published yet and the on-demand sweep failed. It maps to 503 so a
	// client can distinguish "telemetry is down" from a genuine server fault.
	CodeTelemetryUnavailable = "SYSTEM_TELEMETRY_UNAVAILABLE"

	// identitySingletonID pins the single row of system_settings (CHECK id = 1).
	identitySingletonID = 1

	// maxDeviceIDLength bounds device_id. 64 characters is enough for
	// "site-building-gate-01"-style names while keeping MQTT topic composition and
	// downstream database columns predictable.
	maxDeviceIDLength = 64

	// generatedDeviceIDPrefix marks an auto-generated identity so an operator can
	// tell at a glance that this box was never explicitly named.
	generatedDeviceIDPrefix = "zhulong-"
)

// deviceIDPattern accepts what a business system can safely use as a key: letters,
// digits, dot, underscore and hyphen, starting with an alphanumeric. Whitespace,
// slashes, quotes and non-ASCII are rejected because this value ends up in payloads,
// logs and (potentially) MQTT topics — anywhere a free-form string becomes a parsing
// hazard.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// systemSettingsRow is the GORM model for the singleton system_settings row.
// Like every persistence model in this package it carries no json tags: the API
// answers with *Response projections.
type systemSettingsRow struct {
	ID           int64     `gorm:"column:id;primaryKey"`
	DeviceID     string    `gorm:"column:device_id;type:text;not null;default:''"`
	DeviceIDAuto bool      `gorm:"column:device_id_auto;not null;default:1"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

// TableName pins the table name so a future struct rename cannot retarget it.
func (systemSettingsRow) TableName() string { return "system_settings" }

// identityStore is the persistence adapter for the singleton identity row.
type identityStore struct {
	db database.DBProvider
}

func newIdentityStore(db database.DBProvider) *identityStore {
	return &identityStore{db: db}
}

func (s *identityStore) Load(ctx context.Context) (systemSettingsRow, error) {
	var row systemSettingsRow
	err := s.db.DB().WithContext(ctx).
		Where("id = ?", identitySingletonID).
		Take(&row).Error
	return row, err
}

// Save writes the singleton row.
//
// It deliberately uses a column map instead of Create+OnConflict: DeviceIDAuto
// carries `default:1`, so GORM omits the zero value (false) from the INSERT and an
// upsert would then copy the column default back over the operator's value — the
// identity would silently stay "auto generated" forever.
func (s *identityStore) Save(ctx context.Context, deviceID string, auto bool) error {
	now := time.Now().UTC()
	return s.db.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		updates := map[string]any{
			"device_id":      deviceID,
			"device_id_auto": auto,
			"updated_at":     now,
		}
		res := tx.Model(&systemSettingsRow{}).
			Where("id = ?", identitySingletonID).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			return nil
		}
		// The migration seeds the row, so this only runs against a database whose
		// seed was deleted by hand; insert explicitly rather than upserting.
		return tx.Exec(
			`INSERT INTO system_settings (id, device_id, device_id_auto, updated_at) VALUES (?, ?, ?, ?)`,
			identitySingletonID, deviceID, auto, now,
		).Error
	})
}

// IdentityService owns this host's business identity: a stable device_id that is
// stamped onto every dispatched perception event.
//
// Why a host-level identity instead of reusing the hostname: hostnames on flashed
// edge images are frequently identical (or plain "rk3588"), so an aggregation
// platform cannot tell two boxes apart. The device_id is explicit, editable and
// persisted; it survives hostname changes and image re-flashing of the OS layer.
type IdentityService struct {
	store  *identityStore
	logger *zap.Logger

	mu            sync.RWMutex
	deviceID      string
	autoGenerated bool
	updatedAt     time.Time
}

// NewIdentityService constructs the identity service without touching the database;
// the row is read (and the fallback generated) in Start.
func NewIdentityService(db database.DBProvider, logger *zap.Logger) *IdentityService {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &IdentityService{
		store:  newIdentityStore(db),
		logger: logger.Named("systeminfo.identity"),
	}
}

// Start loads the persisted identity, generating and persisting a fallback on a
// fresh database. A failure is returned so the host refuses to start with an
// identity it cannot guarantee — a silent empty device_id would make every pushed
// event unattributable.
func (s *IdentityService) Start(ctx context.Context) error {
	if s.store == nil || s.store.db == nil {
		return nil
	}

	row, err := s.store.Load(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s.regenerate(ctx)
		}
		return err
	}

	if strings.TrimSpace(row.DeviceID) == "" {
		return s.regenerate(ctx)
	}

	s.set(row.DeviceID, row.DeviceIDAuto, row.UpdatedAt)
	s.logger.Info("device identity loaded",
		zap.String("device_id", row.DeviceID),
		zap.Bool("auto_generated", row.DeviceIDAuto),
	)
	return nil
}

// regenerate mints and persists a fallback identity, then publishes it.
func (s *IdentityService) regenerate(ctx context.Context) error {
	generated, err := generateDeviceID()
	if err != nil {
		return err
	}
	if err := s.store.Save(ctx, generated, true); err != nil {
		return err
	}

	s.set(generated, true, time.Now().UTC())
	s.logger.Info("generated fallback device identity", zap.String("device_id", generated))
	return nil
}

// DeviceID implements the alertpush.DeviceIDProvider contract. It never blocks and
// never errors: dispatchers call it on their own goroutine and an empty string
// simply omits the field.
func (s *IdentityService) DeviceID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deviceID
}

// Snapshot returns the current identity projection for the API.
func (s *IdentityService) Snapshot() SystemIdentityResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return SystemIdentityResponse{
		DeviceID:      s.deviceID,
		AutoGenerated: s.autoGenerated,
		UpdatedAt:     s.updatedAt.UTC(),
	}
}

// Update validates and persists a new operator-supplied device_id, then publishes it
// to the in-memory cache so in-flight dispatchers pick it up without a restart.
func (s *IdentityService) Update(ctx context.Context, raw string) (SystemIdentityResponse, error) {
	deviceID := NormalizeDeviceID(raw)
	if !IsValidDeviceID(deviceID) {
		return SystemIdentityResponse{}, apperr.New(
			apperr.KindInvalid,
			CodeIdentityInvalid,
			"device id must be 1-64 characters of letters, digits, dot, underscore or hyphen",
			nil,
		)
	}

	// Without a database there is nothing to persist to; report it as a dependency
	// failure instead of dereferencing a nil provider inside the store.
	if s.store == nil || s.store.db == nil {
		return SystemIdentityResponse{}, apperr.New(
			apperr.KindUnavailable,
			CodeIdentityUnavailable,
			"identity persistence is unavailable",
			nil,
		)
	}

	if err := s.store.Save(ctx, deviceID, false); err != nil {
		return SystemIdentityResponse{}, err
	}

	s.set(deviceID, false, time.Now().UTC())
	s.logger.Info("device identity updated", zap.String("device_id", deviceID))
	return s.Snapshot(), nil
}

func (s *IdentityService) set(deviceID string, auto bool, updatedAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deviceID = deviceID
	s.autoGenerated = auto
	s.updatedAt = updatedAt
}

// NormalizeDeviceID trims the operator input; it deliberately does not lower-case,
// because site codes are often case-sensitive business keys.
func NormalizeDeviceID(raw string) string {
	return strings.TrimSpace(raw)
}

// IsValidDeviceID reports whether the normalized value is an acceptable device_id.
func IsValidDeviceID(deviceID string) bool {
	if deviceID == "" || len(deviceID) > maxDeviceIDLength {
		return false
	}
	return deviceIDPattern.MatchString(deviceID)
}

// generateDeviceID produces a random fallback identity. The suffix is random rather
// than derived from the hostname or /etc/machine-id: vendor images are frequently
// cloned across units, so any hardware-derived default would collide exactly where a
// unique source key matters most.
func generateDeviceID() (string, error) {
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return generatedDeviceIDPrefix + hex.EncodeToString(buf), nil
}
