package gb28181

import (
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// SIP transport choices persisted in gb28181_configs.sip_transport.
const (
	TransportUDP  = "udp"
	TransportTCP  = "tcp"
	TransportBoth = "both"
)

// SecurityProfileStandard is the only profile P0 implements. GB 35114 A/B/C and
// the 2022 high-security profile are explicitly out of scope; accepting other
// values here would silently promise TLS/media encryption that does not exist.
const SecurityProfileStandard = "standard"

// Runtime states reported to the UI. They describe the *current process*, not the
// persisted desired configuration: an operator may save any valid config while
// the running listener keeps the old one until restart.
const (
	RuntimeStateDisabled = "disabled"
	RuntimeStateRunning  = "running"
	RuntimeStateError    = "error"
)

// Device session states persisted in gb_devices.state.
const (
	DeviceStateOffline = "offline"
	DeviceStateOnline  = "online"
)

// MediaPortPairSize is how many UDP ports one playback session occupies:
// RTP plus RTCP. RTCP is held and drained by the Go side (see design.md §2), but
// it still consumes a port, so the pool must budget for it.
const MediaPortPairSize = 2

// Defaults match the migration's column defaults; they are the single source for
// DefaultConfig so tests and runtime cannot drift from the schema.
const (
	DefaultSIPListen         = "0.0.0.0:5060"
	DefaultMediaBindAddress  = "0.0.0.0"
	DefaultRTPPortMin        = 30000
	DefaultRTPPortMax        = 30100
	DefaultExpiresSeconds    = 3600
	ConfigSingletonID        = 1
	deviceIDAADPrefix        = "v1:gb:"
	maxSIPIdentityLength     = 64
	deviceIDLength           = 20
	minPlaybackPortSpanPairs = 1
)

// Audit actions recorded by the audit middleware tag. Only security summaries are
// auditable: passwords and Authorization headers must never reach the audit trail.
const (
	ActionConfigUpdate      = "gb28181.config_update"
	ActionDeviceUpsert      = "gb28181.device_upsert"
	ActionDeviceDelete      = "gb28181.device_delete"
	ActionCatalogSync       = "gb28181.catalog_sync"
	ActionChannelBindCamera = "gb28181.channel_bind_camera"
)

// Stable machine-readable error codes. Text lives in locale.go.
const (
	CodeConfigInvalid            = "GB28181_CONFIG_INVALID"
	CodePortRangeInvalid         = "GB28181_PORT_RANGE_INVALID"
	CodeAddressUnresolvable      = "GB28181_ADDRESS_UNRESOLVABLE"
	CodeAddressInvalid           = "GB28181_ADDRESS_INVALID"
	CodeAddressProbeFailed       = "GB28181_ADDRESS_PROBE_FAILED"
	CodeDeviceNotFound           = "GB28181_DEVICE_NOT_FOUND"
	CodeDeviceIDInvalid          = "GB28181_DEVICE_ID_INVALID"
	CodeRegisteredDeviceRequired = "GB28181_REGISTERED_DEVICE_REQUIRED"
	CodePasswordRequired         = "GB28181_PASSWORD_REQUIRED"
	CodeTransportInvalid         = "GB28181_TRANSPORT_INVALID"
	CodeProfileUnsupported       = "GB28181_PROFILE_UNSUPPORTED"
	CodeRuntimeNotReady          = "GB28181_RUNTIME_NOT_READY"
	CodeDeviceOffline            = "GB28181_DEVICE_OFFLINE"
	CodeDeviceFlowClosed         = "GB28181_DEVICE_FLOW_CLOSED"
	CodeCatalogSyncFailed        = "GB28181_CATALOG_SYNC_FAILED"
	CodeCatalogTimeout           = "GB28181_CATALOG_TIMEOUT"
	CodeChannelNotFound          = "GB28181_CHANNEL_NOT_FOUND"
	CodeChannelIDInvalid         = "GB28181_CHANNEL_ID_INVALID"
	CodeChannelAlreadyBound      = "GB28181_CHANNEL_ALREADY_BOUND"
	CodeCameraBound              = "GB28181_CAMERA_BOUND"

	// CodeDatabaseError is the shared store-failure code owned by
	// internal/httputil/locale.go. Store failures must not reuse a validation
	// code: the frontend branches on the code, and a 500 reported as
	// GB28181_CONFIG_INVALID would tell the operator to fix a config that is fine.
	CodeDatabaseError = "DATABASE_ERROR"
)

// Channel represents an imported video channel of a GB28181 device.
//
// The struct tags below exceed the 120-column guideline: a tag is one atomic literal,
// and splitting it across lines would change its value. Do not reflow them.
type Channel struct {
	DeviceID         string    `gorm:"column:device_id;primaryKey" json:"deviceId"`
	ChannelID        string    `gorm:"column:channel_id;primaryKey" json:"channelId"`
	Name             string    `gorm:"column:name;type:text;not null;default:''" json:"name"`
	Manufacturer     *string   `gorm:"column:manufacturer;type:text" json:"manufacturer"`
	Model            *string   `gorm:"column:model;type:text" json:"model"`
	Owner            *string   `gorm:"column:owner;type:text" json:"owner"`
	CivilCode        *string   `gorm:"column:civil_code;type:text" json:"civilCode"`
	Address          *string   `gorm:"column:address;type:text" json:"address"`
	Parental         int       `gorm:"column:parental;not null;default:0" json:"parental"`
	ParentID         *string   `gorm:"column:parent_id;type:text" json:"parentId"`
	SafetyWay        *int      `gorm:"column:safety_way" json:"safetyWay"`
	RegisterWay      *int      `gorm:"column:register_way" json:"registerWay"`
	PTZType          *int      `gorm:"column:ptz_type" json:"ptzType"`
	ResolutionWidth  *int      `gorm:"column:resolution_width" json:"resolutionWidth"`
	ResolutionHeight *int      `gorm:"column:resolution_height" json:"resolutionHeight"`
	CameraID         *string   `gorm:"column:camera_id;type:text" json:"cameraId"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName pins the table for GORM.
func (Channel) TableName() string { return "gb_channels" }

// Config is the singleton GB28181 runtime configuration. The persisted row in
// gb28181_configs is the only source of truth; config.toml deliberately does not
// carry a duplicate [gb28181] section.
type Config struct {
	ID                      int64     `gorm:"column:id;primaryKey" json:"-"`
	Enabled                 bool      `gorm:"column:enabled;not null;default:0" json:"enabled"`
	SIPID                   string    `gorm:"column:sip_id;type:text;not null;default:''" json:"sipId"`
	SIPRealm                string    `gorm:"column:sip_realm;type:text;not null;default:''" json:"sipRealm"`
	SIPListen               string    `gorm:"column:sip_listen;type:text;not null;default:'0.0.0.0:5060'" json:"sipListen"`
	SIPTransport            string    `gorm:"column:sip_transport;type:text;not null;default:'both'" json:"sipTransport"`
	SecurityProfile         string    `gorm:"column:security_profile;type:text;not null;default:'standard'" json:"securityProfile"`
	MediaBindAddress        string    `gorm:"column:media_bind_address;type:text;not null;default:'0.0.0.0'" json:"mediaBindAddress"`
	MediaAdvertisedIP       string    `gorm:"column:media_advertised_ip;type:text;not null;default:''" json:"mediaAdvertisedIp"`
	RTPPortMin              int       `gorm:"column:rtp_port_min;not null;default:30000" json:"rtpPortMin"`
	RTPPortMax              int       `gorm:"column:rtp_port_max;not null;default:30100" json:"rtpPortMax"`
	RequireRegisteredDevice bool      `gorm:"column:require_registered_device;not null;default:1" json:"requireRegisteredDevice"`
	UpdatedAt               time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName pins the singleton configuration table so GORM cannot infer a generic
// "configs" name that does not exist.
func (Config) TableName() string { return "gb28181_configs" }

// DefaultConfig returns the same values the migration inserts, so a fresh database
// and an in-memory default agree without a read.
func DefaultConfig() Config {
	return Config{
		ID:                      ConfigSingletonID,
		Enabled:                 false,
		SIPListen:               DefaultSIPListen,
		SIPTransport:            TransportBoth,
		SecurityProfile:         SecurityProfileStandard,
		MediaBindAddress:        DefaultMediaBindAddress,
		RTPPortMin:              DefaultRTPPortMin,
		RTPPortMax:              DefaultRTPPortMax,
		RequireRegisteredDevice: true,
	}
}

// Normalize trims user input and applies the documented fallbacks. It does not
// validate: call Validate after normalize so error messages describe the value
// that would actually be persisted.
func (c Config) Normalize() Config {
	c.ID = ConfigSingletonID
	c.SIPID = strings.TrimSpace(c.SIPID)
	c.SIPRealm = strings.TrimSpace(c.SIPRealm)
	c.SIPListen = strings.TrimSpace(c.SIPListen)
	c.SIPTransport = strings.ToLower(strings.TrimSpace(c.SIPTransport))
	c.SecurityProfile = strings.ToLower(strings.TrimSpace(c.SecurityProfile))
	c.MediaBindAddress = strings.TrimSpace(c.MediaBindAddress)
	c.MediaAdvertisedIP = strings.TrimSpace(c.MediaAdvertisedIP)

	// An empty realm means "the device authenticates against our own identity",
	// which is the overwhelmingly common single-box deployment.
	if c.SIPRealm == "" {
		c.SIPRealm = c.SIPID
	}
	if c.SIPListen == "" {
		c.SIPListen = DefaultSIPListen
	}
	if c.SIPTransport == "" {
		c.SIPTransport = TransportBoth
	}
	if c.SecurityProfile == "" {
		c.SecurityProfile = SecurityProfileStandard
	}
	if c.MediaBindAddress == "" {
		c.MediaBindAddress = DefaultMediaBindAddress
	}
	// Only a completely empty pair means "not provided". A single zero is a real
	// input error and must reach Validate instead of being silently repaired:
	// otherwise a client asking for port 0 gets a working default and no error.
	if c.RTPPortMin == 0 && c.RTPPortMax == 0 {
		c.RTPPortMin = DefaultRTPPortMin
		c.RTPPortMax = DefaultRTPPortMax
	}
	return c
}

// Validate checks the configuration statically. Address resolution against the
// host's interfaces is a separate step (resolveAdvertisedIP) because it depends on
// runtime facts and must not be baked into persisted values.
func (c Config) Validate() error {
	if !validTransport(c.SIPTransport) {
		return apperr.Invalid(CodeTransportInvalid,
			"SIP transport must be udp, tcp or both", nil)
	}
	if c.SecurityProfile != SecurityProfileStandard {
		return apperr.Invalid(CodeProfileUnsupported,
			"only the standard security profile is supported", nil)
	}
	if err := validatePortRange(c.RTPPortMin, c.RTPPortMax); err != nil {
		return err
	}
	if !c.RequireRegisteredDevice {
		return apperr.Invalid(CodeRegisteredDeviceRequired,
			"only pre-registered devices may register", nil)
	}
	if !c.Enabled {
		// Disabled stays permissive on purpose: an operator must be able to save a
		// half-filled draft and fill in the identity later without the UI refusing
		// to persist anything at all.
		return nil
	}

	if err := validateIdentity("sipId", c.SIPID, maxSIPIdentityLength); err != nil {
		return err
	}
	if err := validateIdentity("sipRealm", c.SIPRealm, maxSIPIdentityLength); err != nil {
		return err
	}
	if err := validateListenAddress(c.SIPListen); err != nil {
		return err
	}
	if err := validateIPv4("mediaBindAddress", c.MediaBindAddress); err != nil {
		return err
	}
	if c.MediaAdvertisedIP == "" {
		return nil
	}
	return validateIPv4("mediaAdvertisedIp", c.MediaAdvertisedIP)
}

// validTransport reports whether value names a SIP transport this build can bind.
func validTransport(value string) bool {
	switch value {
	case TransportUDP, TransportTCP, TransportBoth:
		return true
	default:
		return false
	}
}

// MediaCapacity is the number of concurrent playback sessions the port range can
// serve. It is derived, never configured: keeping a separate "max sessions" knob
// would let the two disagree and produce a session that cannot get its socket.
func (c Config) MediaCapacity() int {
	return mediaCapacity(c.RTPPortMin, c.RTPPortMax)
}

// mediaCapacity is the only definition of how many complete RTP/RTCP pairs fit into
// a range, so validation and the reported capacity cannot drift apart.
func mediaCapacity(minPort, maxPort int) int {
	span := maxPort - minPort + 1
	if span <= 0 {
		return 0
	}
	return span / MediaPortPairSize
}

// Equal reports whether two configurations describe the same target state,
// ignoring the audit timestamp. restartRequired is computed from this comparison
// and is never persisted separately.
//
// The comparison zeroes the bookkeeping fields and then compares the whole struct, so
// a field added later is covered automatically — the field-by-field && chain this
// replaced would have silently stopped reporting restartRequired for any new knob.
func (c Config) Equal(other Config) bool {
	c.ID, other.ID = 0, 0
	c.UpdatedAt, other.UpdatedAt = time.Time{}, time.Time{}
	return c == other
}

// deviceCredentialsAAD binds a device's ciphertext to its identity, so a blob
// copied between rows cannot be decrypted successfully.
func deviceCredentialsAAD(deviceID string) string {
	return deviceIDAADPrefix + deviceID
}

func validatePortRange(minPort, maxPort int) error {
	if portOutOfRange(minPort) || portOutOfRange(maxPort) {
		return apperr.Invalid(CodePortRangeInvalid,
			"RTP port range must stay between 1 and 65535", nil)
	}
	if minPort > maxPort {
		return apperr.Invalid(CodePortRangeInvalid,
			"RTP port range minimum must not exceed maximum", nil)
	}
	if mediaCapacity(minPort, maxPort) < minPlaybackPortSpanPairs {
		return apperr.Invalid(CodePortRangeInvalid,
			fmt.Sprintf("RTP port range must hold at least one RTP/RTCP pair (%d ports)",
				MediaPortPairSize*minPlaybackPortSpanPairs), nil)
	}
	return nil
}

func validateIdentity(field, value string, maxLen int) error {
	if value == "" {
		return apperr.Invalid(CodeConfigInvalid, field+" must not be empty", nil)
	}
	if len(value) > maxLen {
		return apperr.Invalid(CodeConfigInvalid,
			fmt.Sprintf("%s must not exceed %d characters", field, maxLen), nil)
	}
	for _, r := range value {
		if !validIdentityRune(r) {
			return apperr.Invalid(CodeConfigInvalid,
				field+" may only contain letters, digits, '.', '-' and '_'", nil)
		}
	}
	return nil
}

// validIdentityRune reports whether r may appear in a persisted SIP identity. The set
// is deliberately narrow: it is what devices and proxies put in From/Realm headers.
func validIdentityRune(r rune) bool {
	switch {
	case r >= '0' && r <= '9':
		return true
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	default:
		return r == '.' || r == '-' || r == '_'
	}
}

func validateDeviceID(value string) error {
	if len(value) != deviceIDLength {
		return apperr.Invalid(CodeDeviceIDInvalid,
			"deviceId must be exactly 20 ASCII digits", nil)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return apperr.Invalid(CodeDeviceIDInvalid,
				"deviceId must be exactly 20 ASCII digits", nil)
		}
	}
	return nil
}
