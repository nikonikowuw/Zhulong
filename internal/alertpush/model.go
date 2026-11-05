package alertpush

import "time"

// Audit actions for alert push operations.
const (
	ActionAlertDestinationCreate  = "alertpush.create"
	ActionAlertDestinationUpdate  = "alertpush.update"
	ActionAlertDestinationDelete  = "alertpush.delete"
	ActionAlertDestinationEnable  = "alertpush.enable"
	ActionAlertDestinationDisable = "alertpush.disable"
	ActionAlertDestinationTest    = "alertpush.test"
)

// Supported channel types.
const (
	ChannelTypeWebhook = "webhook"
	ChannelTypeMQTT    = "mqtt"
)

// Supported image modes.
const (
	ImageModeURL       = "url"
	ImageModeMultipart = "multipart"
	ImageModeBase64    = "base64"
)

// Supported Webhook authentication types.
const (
	AuthTypeNone   = "none"
	AuthTypeBearer = "bearer"
	AuthTypeBasic  = "basic"
)

// Status states for alert push destination.
const (
	StateDisabled   = "disabled"
	StateUnverified = "unverified"
	StateConnecting = "connecting"
	StateConnected  = "connected"
	StateFailed     = "failed"
)

// AlertDestination represents the database persistence model for an alert target.
type AlertDestination struct {
	ID               string    `gorm:"primaryKey;column:id" json:"-"`
	Name             string    `gorm:"column:name;not null" json:"-"`
	ChannelType      string    `gorm:"column:channel_type;not null" json:"-"`
	Enabled          bool      `gorm:"column:enabled;not null;default:true" json:"-"`
	ImageMode        string    `gorm:"column:image_mode;not null;default:'url'" json:"-"`
	MQTTClientID     *string   `gorm:"column:mqtt_client_id" json:"-"`
	ConfigJSON       string    `gorm:"column:config_json;not null;default:'{}'" json:"-"`
	SecretCiphertext []byte    `gorm:"column:secret_ciphertext" json:"-"`
	FilterJSON       string    `gorm:"column:filter_json;not null;default:'{}'" json:"-"`
	CreatedAt        time.Time `gorm:"column:created_at;not null" json:"-"`
	UpdatedAt        time.Time `gorm:"column:updated_at;not null" json:"-"`
}

// TableName returns the table name for GORM.
func (AlertDestination) TableName() string {
	return "alert_destinations"
}

// WebhookConfig is the non-sensitive configuration stored in config_json.
// The actual Webhook URL is stored in secret_ciphertext because platforms often include tokens in the query string.
type WebhookConfig struct {
	AuthType       string `json:"auth_type"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// MQTTConfig is the non-sensitive configuration stored in config_json.
// mqtt_client_id is stored in a dedicated column for unique constraint indexing.
type MQTTConfig struct {
	Broker           string `json:"broker"`
	Username         string `json:"username,omitempty"`
	Topic            string `json:"topic"`
	QoS              uint8  `json:"qos"`
	KeepAliveSeconds int    `json:"keep_alive_seconds"`
	TLSSkipVerify    bool   `json:"tls_skip_verify"`
}

// DestinationFilter defines event type, algorithm, and camera filtering criteria stored in filter_json.
type DestinationFilter struct {
	EventTypes   []string `json:"event_types,omitempty"`
	AlgorithmIDs []string `json:"algorithm_ids,omitempty"`
	CameraIDs    []string `json:"camera_ids,omitempty"`
}

// webhookSecret is the sensitive data encrypted in secret_ciphertext.
type webhookSecret struct {
	URL      string            `json:"url"`
	Token    string            `json:"token,omitempty"`
	Username string            `json:"username,omitempty"`
	Password string            `json:"password,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}

// mqttSecret is the sensitive data encrypted in secret_ciphertext.
type mqttSecret struct {
	Password string `json:"password,omitempty"`
}
