package gb28181

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// maxPasswordLength bounds the Digest secret before encryption, counted in
// characters so it matches the API's `max=128` binding rule (the validator counts
// runes, not bytes). GB28181 devices commonly use short numeric passwords, so this
// is an upper bound only.
const maxPasswordLength = 128

// Cipher is the narrow view of camera.Cipher that this domain needs. It is
// declared here rather than imported so the dependency direction stays
// gb28181 -> (interface) and camera never learns about GB28181.
type Cipher interface {
	Encrypt(plaintext []byte, aad string) ([]byte, error)
	Decrypt(ciphertext []byte, aad string) ([]byte, error)
}

// EncryptedDataProbe is implemented by any domain holding data protected by
// camera.key. The host aggregates these before deciding whether a missing key file
// may be regenerated: regenerating after a GB-only database would silently destroy
// every device credential.
type EncryptedDataProbe interface {
	HasExistingEncryptedData(ctx context.Context) (bool, error)
}

// Device is a pre-provisioned GB28181 device. The encrypted password is never part
// of this struct's API projection: it does not cross the API boundary or reach logs.
//
// The GORM models in this package deliberately carry no json tags: the HTTP layer
// answers with the *Response/*Info projections, and a tag would let someone serialize
// a row — ciphertext included — straight onto the wire.
type Device struct {
	DeviceID     string  `gorm:"column:device_id;primaryKey"`
	Name         string  `gorm:"column:name;type:text;not null;default:''"`
	Manufacturer *string `gorm:"column:manufacturer;type:text"`
	Model        *string `gorm:"column:model;type:text"`
	Firmware     *string `gorm:"column:firmware;type:text"`
	Transport    string  `gorm:"column:transport;type:text;not null;default:'udp'"`
	RemoteAddr   *string `gorm:"column:remote_addr;type:text"`
	// ExpiresSeconds is the lease in force: the pre-provisioned value acts as the
	// default when a device omits Expires, and every accepted REGISTER overwrites it
	// with what the device actually asked for (the sweeper enforces this value).
	ExpiresSeconds int    `gorm:"column:expires_seconds;not null;default:3600"`
	State          string `gorm:"column:state;type:text;not null;default:'offline'"`
	// ChannelCount is a projection, not a column: only the list endpoint fills it, so
	// the SIP path does not pay for a count nobody reads. A failed count is returned as
	// an error rather than reported as 0, because "no channels" and "we could not count"
	// must not look the same to an operator.
	ChannelCount    int        `gorm:"-"`
	LastKeepaliveAt *time.Time `gorm:"column:last_keepalive_at"`
	RegisteredAt    *time.Time `gorm:"column:registered_at"`
	EncryptedPass   []byte     `gorm:"column:encrypted_password"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null"`
}

// TableName pins the table so a future model rename cannot silently retarget it.
func (Device) TableName() string { return "gb_devices" }

// DeviceUpsert is one pre-provisioning write. Pointer fields distinguish "not
// provided" (keep the stored value) from "provided" (overwrite), which the REST
// layer needs so updating a device name does not require re-typing the password —
// and rotating the password does not wipe the name.
type DeviceUpsert struct {
	DeviceID       string
	Name           *string
	Password       *string
	Transport      *string
	ExpiresSeconds *int
}

// ConfigRepository persists the singleton configuration row.
type ConfigRepository interface {
	GetConfig(ctx context.Context) (*Config, error)
	SaveConfig(ctx context.Context, cfg Config) (*Config, error)
}

// DeviceStore persists the device allowlist and answers key-integrity questions.
type DeviceStore interface {
	EncryptedDataProbe
	UpsertDevice(ctx context.Context, upd DeviceUpsert) (*Device, error)
	GetDevice(ctx context.Context, deviceID string) (*Device, error)
	ListDevices(ctx context.Context) ([]Device, error)
	DeleteDevice(ctx context.Context, deviceID string) error
	DeviceCounts(ctx context.Context) (total, online int64, err error)
}

// ChannelStore persists device channels and manages camera bindings.
type ChannelStore interface {
	// SyncChannels reconciles the stored catalog of one device with the catalog it
	// just reported: rows are upserted, and channels the device no longer reports are
	// deleted unless a camera is still bound to them.
	SyncChannels(ctx context.Context, deviceID string, channels []Channel) error
	ListChannels(ctx context.Context, deviceID string) ([]Channel, error)
	GetChannel(ctx context.Context, deviceID, channelID string) (*Channel, error)
	BindChannelCamera(ctx context.Context, deviceID, channelID, cameraID string) error
}

type Store struct {
	dbProvider database.DBProvider
	cipher     Cipher
}

// NewStore constructs the repository. It performs no I/O: the database may not be
// open yet, and the cipher is not initialized until the runtime lifecycle starts.
func NewStore(dbProvider database.DBProvider, cipher Cipher) *Store {
	return &Store{dbProvider: dbProvider, cipher: cipher}
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

// GetConfig reads the singleton row. A missing row (never migrated or manually
// deleted) falls back to DefaultConfig without writing, so a read never mutates.
func (s *Store) GetConfig(ctx context.Context) (*Config, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := gorm.G[Config](db).Where("id = ?", ConfigSingletonID).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fallback := DefaultConfig()
		return &fallback, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read gb28181 config: %w", err)
	}
	return &cfg, nil
}

// SaveConfig writes the whole configuration atomically. The caller has already
// normalized and validated it; this layer only guarantees the row exists exactly
// once (id = 1). GORM's Save updates the existing row and inserts it when the
// singleton row is missing, so no explicit transaction is needed for one write.
func (s *Store) SaveConfig(ctx context.Context, cfg Config) (*Config, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	cfg = cfg.Normalize()
	cfg.ID = ConfigSingletonID
	cfg.UpdatedAt = now

	if err := db.Save(&cfg).Error; err != nil {
		return nil, fmt.Errorf("save gb28181 config: %w", err)
	}
	return &cfg, nil
}

// UpsertDevice creates or updates a pre-provisioned device. An omitted password
// keeps the stored ciphertext; a new device without one is rejected, because an
// allowlist entry nobody can authenticate against is a trap rather than a draft.
func (s *Store) UpsertDevice(ctx context.Context, upd DeviceUpsert) (*Device, error) {
	if err := validateDeviceUpsert(upd); err != nil {
		return nil, err
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if s.cipher == nil {
		return nil, apperr.Internal(CodeRuntimeNotReady, "encryption cipher is not initialized", nil)
	}

	var saved Device
	err = db.Transaction(func(tx *gorm.DB) error {
		return s.upsertDeviceTx(ctx, tx, upd, &saved)
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

// validateDeviceUpsert checks the write shape before any row is touched.
func validateDeviceUpsert(upd DeviceUpsert) error {
	if err := validateDeviceID(strings.TrimSpace(upd.DeviceID)); err != nil {
		return err
	}
	if upd.Transport != nil {
		transport := strings.ToLower(strings.TrimSpace(*upd.Transport))
		if transport != TransportUDP && transport != TransportTCP {
			return apperr.Invalid(CodeTransportInvalid,
				"device transport must be udp or tcp", nil)
		}
	}
	if upd.ExpiresSeconds != nil && *upd.ExpiresSeconds < 60 {
		return apperr.Invalid(CodeConfigInvalid,
			"expiresSeconds must be at least 60", nil)
	}
	return nil
}

// upsertDeviceTx reads, merges, and writes one device inside a transaction.
func (s *Store) upsertDeviceTx(ctx context.Context, tx *gorm.DB, upd DeviceUpsert, saved *Device) error {
	deviceID := strings.TrimSpace(upd.DeviceID)
	existing, exists, err := readDeviceForUpdate(ctx, tx, deviceID)
	if err != nil {
		return err
	}
	next := mergeDeviceWrite(existing, upd, exists, time.Now().UTC())
	if err := s.applyDevicePassword(&next, upd, exists); err != nil {
		return err
	}
	if err := tx.Save(&next).Error; err != nil {
		return fmt.Errorf("save gb device %s: %w", deviceID, err)
	}
	*saved = next
	return nil
}

// readDeviceForUpdate loads the current row, reporting whether it exists.
func readDeviceForUpdate(ctx context.Context, tx *gorm.DB, deviceID string) (Device, bool, error) {
	existing, err := gorm.G[Device](tx).Where("device_id = ?", deviceID).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Device{}, false, nil
	}
	if err != nil {
		return Device{}, false, fmt.Errorf("read gb device %s: %w", deviceID, err)
	}
	return existing, true, nil
}

// mergeDeviceWrite applies the fields a provisioning request can change. A nil
// pointer keeps the stored value; only an explicitly provided value overwrites it,
// so rotating the Digest password does not erase the operator-facing label.
func mergeDeviceWrite(existing Device, upd DeviceUpsert, exists bool, now time.Time) Device {
	merged := existing
	merged.DeviceID = strings.TrimSpace(upd.DeviceID)
	if upd.Name != nil {
		merged.Name = strings.TrimSpace(*upd.Name)
	}
	if !exists {
		merged.State = DeviceStateOffline
		merged.CreatedAt = now
		merged.applyDefaults()
	}
	if upd.Transport != nil {
		merged.Transport = strings.ToLower(strings.TrimSpace(*upd.Transport))
	}
	if upd.ExpiresSeconds != nil {
		merged.ExpiresSeconds = *upd.ExpiresSeconds
	}
	merged.UpdatedAt = now
	return merged
}

// applyDevicePassword encrypts a provided password under the device's AAD. An
// omitted password keeps the stored ciphertext; a new device without one is
// rejected, because an allowlist entry nobody can authenticate against is a trap.
func (s *Store) applyDevicePassword(device *Device, upd DeviceUpsert, exists bool) error {
	if upd.Password == nil {
		if exists {
			return nil
		}
		return apperr.Invalid(CodePasswordRequired,
			"a new device must be provisioned with a Digest password", nil)
	}
	password := *upd.Password
	if password == "" {
		return apperr.Invalid(CodePasswordRequired, "password must not be empty", nil)
	}
	if utf8.RuneCountInString(password) > maxPasswordLength {
		return apperr.Invalid(CodePasswordRequired,
			fmt.Sprintf("password must not exceed %d characters", maxPasswordLength), nil)
	}
	encrypted, err := s.cipher.Encrypt([]byte(password), deviceCredentialsAAD(device.DeviceID))
	if err != nil {
		return fmt.Errorf("encrypt gb device password: %w", err)
	}
	device.EncryptedPass = encrypted
	return nil
}

// applyDefaults fills the columns the migration defaults would apply, so the
// object returned to the caller matches what was persisted.
func (d *Device) applyDefaults() {
	if d.Transport == "" {
		d.Transport = TransportUDP
	}
	if d.ExpiresSeconds == 0 {
		d.ExpiresSeconds = DefaultExpiresSeconds
	}
}

// GetDevice returns one device without its ciphertext. It is the read used by the SIP
// path (every keepalive), so it stays a single row read: the channel count is a UI
// projection and only the list endpoint pays for it.
func (s *Store) GetDevice(ctx context.Context, deviceID string) (*Device, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	device, err := gorm.G[Device](db).Where("device_id = ?", deviceID).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound(CodeDeviceNotFound, "GB28181 device not found", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("read gb device %s: %w", deviceID, err)
	}
	device.EncryptedPass = nil
	return &device, nil
}

// ListDevices returns the allowlist ordered by device ID (stable for UI tables).
func (s *Store) ListDevices(ctx context.Context) ([]Device, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := gorm.G[Device](db).Order("device_id ASC").Find(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gb devices: %w", err)
	}

	type countResult struct {
		DeviceID string
		Count    int
	}
	counts := []countResult{}
	if err := db.Model(&Channel{}).
		Select("device_id, count(*) as count").
		Group("device_id").
		Scan(&counts).Error; err != nil {
		return nil, fmt.Errorf("count gb channels per device: %w", err)
	}
	countMap := make(map[string]int, len(counts))
	for _, c := range counts {
		countMap[c.DeviceID] = c.Count
	}

	for i := range devices {
		devices[i].EncryptedPass = nil
		devices[i].ChannelCount = countMap[devices[i].DeviceID]
	}
	return devices, nil
}

// HasExistingEncryptedData reports whether any device password ciphertext exists.
// The host consults every probe before it allows a missing camera.key to be
// regenerated; a GB-only database must block that path just as camera streams do.
func (s *Store) HasExistingEncryptedData(ctx context.Context) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	count, err := gorm.G[Device](db).
		Where("encrypted_password IS NOT NULL AND length(encrypted_password) > 0").
		Count(ctx, "*")
	if err != nil {
		return false, fmt.Errorf("check gb device credentials: %w", err)
	}
	return count > 0, nil
}

// DeviceCounts returns the allowlist size and how many devices are currently
// registered, for the runtime snapshot.
//
// The two counts are separate statements on purpose: they only feed diagnostics, so
// a total that lags an online count by one refresh is harmless, whereas folding them
// into one row would need SQLite-specific conditional aggregation.
func (s *Store) DeviceCounts(ctx context.Context) (total, online int64, err error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, 0, err
	}
	total, err = gorm.G[Device](db).Count(ctx, "*")
	if err != nil {
		return 0, 0, fmt.Errorf("count gb devices: %w", err)
	}
	online, err = gorm.G[Device](db).Where("state = ?", DeviceStateOnline).Count(ctx, "*")
	if err != nil {
		return 0, 0, fmt.Errorf("count online gb devices: %w", err)
	}
	return total, online, nil
}

// Registration is the outcome of an accepted REGISTER that must be persisted.
type Registration struct {
	DeviceID       string
	Transport      string
	RemoteAddr     string
	ExpiresSeconds int
	RegisteredAt   time.Time
}

// MarkRegistered records a successful REGISTER. It only updates an existing
// allowlist row: provisioning stays an explicit operator action, so a device that is
// not in gb_devices can never appear in the table through SIP.
func (s *Store) MarkRegistered(ctx context.Context, registration Registration) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	result := db.Model(&Device{}).
		Where("device_id = ?", registration.DeviceID).
		Updates(map[string]any{
			"state":           DeviceStateOnline,
			"remote_addr":     registration.RemoteAddr,
			"transport":       registration.Transport,
			"expires_seconds": registration.ExpiresSeconds,
			"registered_at":   registration.RegisteredAt.UTC(),
			"updated_at":      registration.RegisteredAt.UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("mark gb device %s registered: %w", registration.DeviceID, result.Error)
	}
	if result.RowsAffected == 0 {
		return apperr.NotFound(CodeDeviceNotFound, "GB28181 device not found", nil)
	}
	return nil
}

// MarkUnregistered clears a registration (EXPIRES=0 or an expired lease). The remote
// address is cleared with it so a stale address cannot be used for outbound requests.
func (s *Store) MarkUnregistered(ctx context.Context, deviceID string, at time.Time) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	result := db.Model(&Device{}).
		Where("device_id = ?", deviceID).
		Updates(map[string]any{
			"state":       DeviceStateOffline,
			"remote_addr": nil,
			"updated_at":  at.UTC(),
		})
	if result.Error != nil {
		return fmt.Errorf("mark gb device %s unregistered: %w", deviceID, result.Error)
	}
	return nil
}

// TouchKeepalive records a keepalive for an online device. It reports whether a row
// matched: a keepalive from a device that the sweeper already took offline must not
// silently revive it, because only a REGISTER proves liveness.
func (s *Store) TouchKeepalive(ctx context.Context, deviceID string, at time.Time) (bool, error) {
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	result := db.Model(&Device{}).
		Where("device_id = ? AND state = ?", deviceID, DeviceStateOnline).
		Updates(map[string]any{
			"last_keepalive_at": at.UTC(),
			"updated_at":        at.UTC(),
		})
	if result.Error != nil {
		return false, fmt.Errorf("record gb device %s keepalive: %w", deviceID, result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ResetOnlineStates clears registrations left behind by a previous process. Persisted
// "online" is not evidence of liveness after a restart: only a fresh REGISTER is.
func (s *Store) ResetOnlineStates(ctx context.Context, at time.Time) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	result := db.Model(&Device{}).
		Where("state = ?", DeviceStateOnline).
		Updates(map[string]any{
			"state":       DeviceStateOffline,
			"remote_addr": nil,
			"updated_at":  at.UTC(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("reset online gb devices: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// SweepStale takes devices offline whose registration expired or whose keepalives
// stopped. The comparison runs in Go against an injected clock so the sweeper is
// testable without waiting for wall time, and the allowlist is small enough (hundreds
// of devices at most) that a full scan is cheaper than SQLite datetime arithmetic on
// driver-formatted timestamps.
func (s *Store) SweepStale(ctx context.Context, now time.Time, keepaliveTimeout, grace time.Duration) (int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return 0, err
	}
	var online []Device
	if err := db.Select(
		"device_id", "registered_at", "expires_seconds", "last_keepalive_at",
	).Where("state = ?", DeviceStateOnline).Find(&online).Error; err != nil {
		return 0, fmt.Errorf("list online gb devices: %w", err)
	}

	stale := make([]string, 0, len(online))
	for _, device := range online {
		if device.RegisteredAt == nil {
			stale = append(stale, device.DeviceID)
			continue
		}
		expiry := device.RegisteredAt.Add(time.Duration(device.ExpiresSeconds)*time.Second + grace)
		if !now.Before(expiry) {
			stale = append(stale, device.DeviceID)
			continue
		}
		// A device must keep proving liveness even while its lease is valid: silence
		// means the link is down, not that it will come back. The clock starts at the
		// REGISTER, so a device that registers and then never sends a keepalive ages
		// out just like one that stopped sending them.
		lastSeen := *device.RegisteredAt
		if device.LastKeepaliveAt != nil && device.LastKeepaliveAt.After(lastSeen) {
			lastSeen = *device.LastKeepaliveAt
		}
		if keepaliveTimeout > 0 && now.Sub(lastSeen) > keepaliveTimeout {
			stale = append(stale, device.DeviceID)
		}
	}
	if len(stale) == 0 {
		return 0, nil
	}
	result := db.Model(&Device{}).
		Where("device_id IN ?", stale).
		Where("state = ?", DeviceStateOnline).
		Updates(map[string]any{
			"state":       DeviceStateOffline,
			"remote_addr": nil,
			"updated_at":  now.UTC(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("mark stale gb devices offline: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// deviceForAuthentication returns one provisioned device together with its plaintext
// Digest secret.
//
// This is the only path that materialises the secret, it is called from the SIP
// ingress on REGISTER, and the value never leaves this package: the API returns
// DeviceResponse, which has no field for it. The device row comes back with the
// ciphertext cleared, because the caller only needs its provisioned lease.
func (s *Store) deviceForAuthentication(ctx context.Context, deviceID string) (*Device, string, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, "", err
	}
	if s.cipher == nil {
		return nil, "", apperr.Internal(CodeRuntimeNotReady, "encryption cipher is not initialized", nil)
	}
	device, err := gorm.G[Device](db).
		Select("device_id", "expires_seconds", "encrypted_password").
		Where("device_id = ?", deviceID).
		First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, "", apperr.NotFound(CodeDeviceNotFound, "GB28181 device not found", nil)
	}
	if err != nil {
		return nil, "", fmt.Errorf("read gb device %s credential: %w", deviceID, err)
	}
	if len(device.EncryptedPass) == 0 {
		return nil, "", apperr.Internal(CodePasswordRequired, "GB28181 device has no Digest password", nil)
	}
	plaintext, decErr := s.cipher.Decrypt(device.EncryptedPass, deviceCredentialsAAD(deviceID))
	if decErr != nil {
		return nil, "", fmt.Errorf("decrypt gb device %s credential: %w", deviceID, decErr)
	}
	device.EncryptedPass = nil
	return &device, string(plaintext), nil
}

const (
	// SQLite's host-parameter limit varies with build options; 50 rows x 16 columns keeps
	// every batch far below the conservative 999 interface value.
	channelUpsertBatchSize = 50
	// Delete chunks stay separate from the upsert batching: pruning is computed in Go
	// (no NOT IN list), so the chunk only bounds the IN clause.
	channelDeleteChunkSize = 200
)

// SyncChannels reconciles the stored catalog of one device with the catalog it just
// reported.
//
// Upsert alone would keep every channel the device ever had, so an operator could add
// a channel the device no longer exposes as a camera. Channels that disappeared from
// the report are therefore deleted — except the ones still bound to a camera: dropping
// the device's list must not delete a camera behind the operator's back, and the FK
// (ON DELETE SET NULL) already covers the opposite direction.
//
// The whole reconciliation is one transaction, so a partially applied catalog cannot
// be observed by a concurrent list.
func (s *Store) SyncChannels(ctx context.Context, deviceID string, channels []Channel) error {
	for _, ch := range channels {
		if ch.DeviceID != deviceID {
			return apperr.Invalid(CodeDeviceIDInvalid, "channel deviceId does not match target device", nil)
		}
		if !camera.IsGB28181ID(ch.ChannelID) {
			return apperr.Invalid(CodeChannelIDInvalid, "channelId must be 20 digits", nil)
		}
	}

	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	reported := make(map[string]struct{}, len(channels))
	for _, ch := range channels {
		reported[ch.ChannelID] = struct{}{}
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for batch := range slices.Chunk(channels, channelUpsertBatchSize) {
			stamped := make([]Channel, len(batch))
			for j := range batch {
				item := batch[j]
				item.CreatedAt = now
				item.UpdatedAt = now
				stamped[j] = item
			}

			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "device_id"}, {Name: "channel_id"}},
				// camera_id and created_at are deliberately absent: a catalog refresh must
				// not clear a binding, and the first import time is a fact.
				DoUpdates: clause.AssignmentColumns([]string{
					"name", "manufacturer", "model", "owner", "civil_code", "address",
					"parental", "parent_id", "safety_way", "register_way", "ptz_type",
					"resolution_width", "resolution_height", "updated_at",
				}),
			}).Create(&stamped).Error; err != nil {
				return fmt.Errorf("upsert gb channels batch: %w", err)
			}
		}

		boundFree := []string{}
		if err := tx.Model(&Channel{}).
			Where("device_id = ? AND camera_id IS NULL", deviceID).
			Pluck("channel_id", &boundFree).Error; err != nil {
			return fmt.Errorf("list prunable gb channels for %s: %w", deviceID, err)
		}

		stale := make([]string, 0, len(boundFree))
		for _, id := range boundFree {
			if _, ok := reported[id]; !ok {
				stale = append(stale, id)
			}
		}
		for chunk := range slices.Chunk(stale, channelDeleteChunkSize) {
			if err := tx.Where("device_id = ? AND camera_id IS NULL AND channel_id IN ?", deviceID, chunk).
				Delete(&Channel{}).Error; err != nil {
				return fmt.Errorf("prune stale gb channels for %s: %w", deviceID, err)
			}
		}
		return nil
	})
}

// ListChannels returns all channels for a given device ordered by channel_id.
func (s *Store) ListChannels(ctx context.Context, deviceID string) ([]Channel, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	count, err := gorm.G[Device](db).Where("device_id = ?", deviceID).Count(ctx, "*")
	if err != nil {
		return nil, fmt.Errorf("check device %s: %w", deviceID, err)
	}
	if count == 0 {
		return nil, apperr.NotFound(CodeDeviceNotFound, "GB28181 device not found", nil)
	}

	channels, err := gorm.G[Channel](db).Where("device_id = ?", deviceID).Order("channel_id ASC").Find(ctx)
	if err != nil {
		return nil, fmt.Errorf("list gb channels for %s: %w", deviceID, err)
	}
	return channels, nil
}

// GetChannel returns a single channel by composite key.
func (s *Store) GetChannel(ctx context.Context, deviceID, channelID string) (*Channel, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	ch, err := gorm.G[Channel](db).Where("device_id = ? AND channel_id = ?", deviceID, channelID).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperr.NotFound(CodeChannelNotFound, "GB28181 channel not found", nil)
	}
	if err != nil {
		return nil, fmt.Errorf("read gb channel %s/%s: %w", deviceID, channelID, err)
	}
	return &ch, nil
}

// BindChannelCamera associates a channel with a created camera record.
//
// The update is conditional on the channel still being unbound: the service already
// serialises the read-then-write, and this makes the "one camera per channel"
// invariant hold in SQL too, instead of resting on that serialisation alone.
func (s *Store) BindChannelCamera(ctx context.Context, deviceID, channelID, cameraID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	res := db.Model(&Channel{}).
		Where("device_id = ? AND channel_id = ? AND camera_id IS NULL", deviceID, channelID).
		Updates(map[string]any{
			"camera_id":  cameraID,
			"updated_at": now,
		})
	if res.Error != nil {
		return fmt.Errorf("bind gb channel camera: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// Either the channel is gone or another writer bound it first. The two mean
		// different things to the operator, so they are told apart with one extra read
		// on the sad path only.
		exists, countErr := gorm.G[Channel](db).
			Where("device_id = ? AND channel_id = ?", deviceID, channelID).
			Count(ctx, "*")
		if countErr != nil {
			return fmt.Errorf("check gb channel %s/%s before binding: %w", deviceID, channelID, countErr)
		}
		if exists == 0 {
			return apperr.NotFound(CodeChannelNotFound, "GB28181 channel not found", nil)
		}
		return apperr.Conflict(CodeChannelAlreadyBound, "channel is already bound to a camera", nil)
	}
	return nil
}

// DeleteDevice removes a device, its catalog rows (FK ON DELETE CASCADE) and its
// pre-provisioned credential, provided none of its channels are bound to a camera.
func (s *Store) DeleteDevice(ctx context.Context, deviceID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	return db.Transaction(func(tx *gorm.DB) error {
		boundCount, err := gorm.G[Channel](tx).
			Where("device_id = ? AND camera_id IS NOT NULL", deviceID).
			Count(ctx, "*")
		if err != nil {
			return fmt.Errorf("check bound cameras for %s: %w", deviceID, err)
		}
		if boundCount > 0 {
			return apperr.Conflict(CodeCameraBound, "device has bound cameras; unbind or delete cameras first", nil)
		}

		rows, err := gorm.G[Device](tx).Where("device_id = ?", deviceID).Delete(ctx)
		if err != nil {
			return fmt.Errorf("delete gb device %s: %w", deviceID, err)
		}
		if rows == 0 {
			return apperr.NotFound(CodeDeviceNotFound, "GB28181 device not found", nil)
		}
		return nil
	})
}
