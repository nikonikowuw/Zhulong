package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

var (
	ErrPublicBaseURLNotConfigured = errors.New("public_base_url is required for url image mode")
	ErrInvalidChannelType         = errors.New("invalid channel_type")
	ErrInvalidImageMode           = errors.New("invalid image_mode for channel_type")
	ErrInvalidConfig              = errors.New("invalid alert destination config")
	ErrInvalidFilterEventType     = errors.New("invalid filter event type")
	ErrReservedHeaderUsed         = errors.New("reserved header is not allowed in custom headers")
	ErrCrossOriginHeartbeatURL    = errors.New(
		"heartbeat_url must share origin with url when credentials are configured",
	)
)

// ServiceOptions holds dependencies for constructing a Service.
type ServiceOptions struct {
	Store        *Store
	Dispatcher   *Dispatcher
	Signer       *URLSigner
	Media        MediaReader
	GalleryMedia MediaReader
	Config       Config
	Logger       *zap.Logger
}

// Service provides high-level domain operations for alert push destinations.
type Service struct {
	store        *Store
	dispatcher   *Dispatcher
	signer       *URLSigner
	media        MediaReader
	galleryMedia MediaReader
	config       Config
	logger       *zap.Logger
}

// UpdateParams contains mutable fields for updating an alert destination.
type UpdateParams struct {
	Name             *string
	EventsEnabled    *bool
	HeartbeatEnabled *bool
	ImageMode        *string
	ConfigJSON       *string
	FilterJSON       *string
	SecretRaw        destinationSecret
	ClientID         *string
}

// TestExecutionResult holds the result of a connection test.
type TestExecutionResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}

// NewService creates a new alertpush Service instance.
func NewService(opts ServiceOptions) *Service {
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:        opts.Store,
		dispatcher:   opts.Dispatcher,
		signer:       opts.Signer,
		media:        opts.Media,
		galleryMedia: opts.GalleryMedia,
		config:       opts.Config,
		logger:       logger.Named("alertpush.service"),
	}
}

// Create validates and saves a new destination, then requests reload.
func (s *Service) Create(
	ctx context.Context,
	dest *AlertDestination,
	secretRaw destinationSecret,
) error {
	dest.Name = strings.TrimSpace(dest.Name)
	if err := validateDestinationName(dest.Name); err != nil {
		return err
	}
	if !dest.EventsEnabled && strings.TrimSpace(dest.ImageMode) == "" {
		dest.ImageMode = ImageModeURL
	}
	if err := s.validateDestination(dest, secretRaw); err != nil {
		return err
	}

	dest.ID = idutil.New()

	// If MQTT, ensure mqtt_client_id is assigned
	if dest.ChannelType == ChannelTypeMQTT {
		if dest.MQTTClientID == nil || strings.TrimSpace(*dest.MQTTClientID) == "" {
			cID := "z" + dest.ID[4:]
			dest.MQTTClientID = &cID
		} else {
			cID := strings.TrimSpace(*dest.MQTTClientID)
			dest.MQTTClientID = &cID
		}
		conflict, err := s.store.CheckMQTTClientIDConflict(ctx, "", *dest.MQTTClientID)
		if err != nil {
			return err
		}
		if conflict {
			return ErrClientIDConflict
		}
	}

	ciphertext, err := s.store.EncryptSecret(dest.ID, secretRaw)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	dest.SecretCiphertext = ciphertext

	if err := s.store.CreateWithLimit(ctx, dest, MaxDestinations); err != nil {
		return err
	}

	s.dispatcher.RequestReload()
	return nil
}

// Update updates an existing destination.
func (s *Service) Update(
	ctx context.Context,
	existing *AlertDestination,
	params UpdateParams,
) error {
	if params.Name != nil {
		trimmed := strings.TrimSpace(*params.Name)
		if err := validateDestinationName(trimmed); err != nil {
			return err
		}
		existing.Name = trimmed
	}
	if params.EventsEnabled != nil {
		existing.EventsEnabled = *params.EventsEnabled
	}
	if params.HeartbeatEnabled != nil {
		existing.HeartbeatEnabled = *params.HeartbeatEnabled
	}
	if params.ImageMode != nil && strings.TrimSpace(*params.ImageMode) != "" {
		existing.ImageMode = strings.TrimSpace(*params.ImageMode)
	}
	if params.ConfigJSON != nil && strings.TrimSpace(*params.ConfigJSON) != "" {
		existing.ConfigJSON = strings.TrimSpace(*params.ConfigJSON)
	}
	if params.FilterJSON != nil && strings.TrimSpace(*params.FilterJSON) != "" {
		existing.FilterJSON = strings.TrimSpace(*params.FilterJSON)
	}

	if existing.ChannelType == ChannelTypeMQTT {
		// Preserve custom ClientID when params.ClientID is omitted; allow explicit empty
		// string to revert back to the deterministic auto-derived ClientID.
		var targetClientID string
		switch {
		case params.ClientID != nil:
			targetClientID = strings.TrimSpace(*params.ClientID)
		case existing.MQTTClientID != nil:
			targetClientID = strings.TrimSpace(*existing.MQTTClientID)
		}
		if targetClientID == "" {
			targetClientID = "z" + existing.ID[4:]
		}
		existing.MQTTClientID = &targetClientID
	}

	if err := s.validateDestination(existing, params.SecretRaw); err != nil {
		return err
	}

	if existing.ChannelType == ChannelTypeMQTT && existing.MQTTClientID != nil {
		conflict, err := s.store.CheckMQTTClientIDConflict(ctx, existing.ID, *existing.MQTTClientID)
		if err != nil {
			return err
		}
		if conflict {
			return ErrClientIDConflict
		}
	}

	if params.SecretRaw != nil {
		ciphertext, err := s.store.EncryptSecret(existing.ID, params.SecretRaw)
		if err != nil {
			return fmt.Errorf("encrypt secret: %w", err)
		}
		existing.SecretCiphertext = ciphertext
	}

	if err := s.store.Update(ctx, existing); err != nil {
		return err
	}

	s.dispatcher.RequestReload()
	return nil
}

// SetEnabled toggles enabled state and requests reload.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.store.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	s.dispatcher.RequestReload()
	return nil
}

// Delete removes destination, clears its cached runtime state, and requests reload.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.dispatcher.RemoveDestinationState(id)
	s.dispatcher.RequestReload()
	return nil
}

// Test attempts sending a dummy ping/test event (or an immediate heartbeat when
// events are disabled) to the destination and records the result against the active
// destination status when testing a saved enabled destination.
func (s *Service) Test(
	ctx context.Context,
	dest *AlertDestination,
	secretRaw destinationSecret,
) (TestExecutionResult, error) {
	return s.TestWithOptions(ctx, dest, secretRaw, true)
}

// TestWithOptions executes a connectivity probe and updates the live destination's
// status only when recordRuntimeStatus is true (saved-target quick test, never edited drafts).
func (s *Service) TestWithOptions(
	ctx context.Context,
	dest *AlertDestination,
	secretRaw destinationSecret,
	recordRuntimeStatus bool,
) (TestExecutionResult, error) {
	const testTimeout = 10 * time.Second
	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	if !dest.EventsEnabled && strings.TrimSpace(dest.ImageMode) == "" {
		dest.ImageMode = ImageModeURL
	}
	if err := s.validateDestination(dest, secretRaw); err != nil {
		return TestExecutionResult{OK: false, Reason: "config_invalid"}, err
	}

	var (
		sender Sender
		err    error
	)
	switch dest.ChannelType {
	case ChannelTypeWebhook:
		sec, ok := secretRaw.(*webhookSecret)
		if !ok || sec == nil || sec.URL == "" {
			return TestExecutionResult{OK: false, Reason: "config_invalid"}, ErrInvalidConfig
		}
		sender, err = s.buildTestWebhookSender(dest, sec, testTimeout)
		if err != nil {
			return TestExecutionResult{OK: false, Reason: classifyError(err)}, nil
		}
	case ChannelTypeMQTT:
		sec, ok := secretRaw.(*mqttSecret)
		if !ok || sec == nil {
			sec = &mqttSecret{}
		}
		sender, err = s.buildTestMQTTSender(dest, sec)
		if err != nil {
			return TestExecutionResult{OK: false, Reason: classifyError(err)}, nil
		}
	default:
		return TestExecutionResult{OK: false, Reason: "unsupported_channel"}, ErrInvalidChannelType
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			s.logger.Debug("close test sender", zap.Error(closeErr))
		}
	}()

	start := time.Now()
	sendErr := s.sendTestProbe(testCtx, sender, dest)
	latency := time.Since(start).Milliseconds()

	success := sendErr == nil
	var reason string
	if sendErr != nil {
		reason = classifyError(sendErr)
	}

	if recordRuntimeStatus && dest.ID != "" && dest.Enabled {
		s.dispatcher.UpdateTestResult(dest.ID, success, reason)
	}

	return TestExecutionResult{
		OK:        success,
		LatencyMs: latency,
		Reason:    reason,
	}, nil
}

func (s *Service) sendTestProbe(ctx context.Context, sender Sender, dest *AlertDestination) error {
	if !dest.EventsEnabled && dest.HeartbeatEnabled {
		// Connectivity probes always use seq 0 so they never collide with real monotonic stream seq >= 1.
		snap := resolveHeartbeatSnapshot(ctx, s.config.HeartbeatSnapshot, s.config.PublicBaseURL)
		return sender.SendHeartbeat(ctx, snap, 0)
	}
	// The probe is a v1 alarm with seq 0: a real destination counter is never consumed.
	testEvent := record.AlarmEvent{
		ID:         "01TESTALARM0000000000000",
		EventType:  record.EventTypeAlarm,
		CameraID:   "test_cam",
		CapturedAt: time.Now().UTC(),
		Alarm: &record.AlarmEventData{
			AlarmType: "test_ping",
			Score:     1.0,
		},
	}
	return sender.Send(ctx, Delivery{Event: testEvent, Seq: 0})
}

func (s *Service) buildTestWebhookSender(
	dest *AlertDestination,
	sec *webhookSecret,
	maxTimeout time.Duration,
) (Sender, error) {
	var cfg webhookConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal webhook config: %w", err)
		}
	}

	timeout := 5 * time.Second
	if cfg.TimeoutSeconds >= 1 && cfg.TimeoutSeconds <= 30 {
		timeout = time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	if maxTimeout > 0 && timeout > maxTimeout {
		timeout = maxTimeout
	}

	return NewWebhookSender(WebhookSenderOptions{
		ID:                dest.ID,
		Name:              dest.Name,
		ImageMode:         dest.ImageMode,
		SkipImageURLCheck: !dest.EventsEnabled,
		PublicBaseURL:     s.config.PublicBaseURL,
		SignedURLTTL:      s.config.SignedURLTTL,
		Signer:            s.signer,
		Media:             s.media,
		GalleryMedia:      s.galleryMedia,
		URL:               sec.URL,
		HeartbeatURL:      sec.HeartbeatURL,
		AuthType:          cfg.AuthType,
		Token:             sec.Token,
		Username:          sec.Username,
		Password:          sec.Password,
		Headers:           sec.Headers,
		Timeout:           timeout,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		Identity:          s.config.DeviceID,
		Logger:            s.logger,
	})
}

func (s *Service) buildTestMQTTSender(dest *AlertDestination, sec *mqttSecret) (Sender, error) {
	var cfg mqttConfig
	if dest.ConfigJSON != "" {
		if err := json.Unmarshal([]byte(dest.ConfigJSON), &cfg); err != nil {
			return nil, fmt.Errorf("unmarshal mqtt config: %w", err)
		}
	}

	// Probe connectivity must strictly use a transient ClientID to avoid kicking
	// online workers off broker, and never arm LWT / graceful offline notices.
	clientID := "test-probe-" + idutil.New()[:12]

	return NewMQTTSender(MQTTSenderOptions{
		ID:                dest.ID,
		Name:              dest.Name,
		ImageMode:         dest.ImageMode,
		SkipImageURLCheck: !dest.EventsEnabled,
		PublicBaseURL:     s.config.PublicBaseURL,
		SignedURLTTL:      s.config.SignedURLTTL,
		Signer:            s.signer,
		Media:             s.media,
		GalleryMedia:      s.galleryMedia,
		Broker:            cfg.Broker,
		ClientID:          clientID,
		Username:          cfg.Username,
		Password:          sec.Password,
		Topic:             cfg.Topic,
		HeartbeatEnabled:  dest.HeartbeatEnabled,
		HeartbeatTopic:    cfg.HeartbeatTopic,
		LWTEnabled:        false,
		QoS:               cfg.QoS,
		KeepAlive:         30 * time.Second,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		DisableReconnect:  true,
		InlineMaxBytes:    int64(normalizeInlineMaxKB(cfg.InlineMaxKB)) * 1024,
		Identity:          s.config.DeviceID,
		Logger:            s.logger,
	})
}

func (s *Service) validateDestination(dest *AlertDestination, secretRaw destinationSecret) error {
	if !dest.EventsEnabled && !dest.HeartbeatEnabled {
		return ErrInvalidConfig
	}

	if dest.ChannelType != ChannelTypeWebhook && dest.ChannelType != ChannelTypeMQTT {
		return ErrInvalidChannelType
	}
	if err := validateFilterEventTypes(dest.FilterJSON); err != nil {
		return err
	}

	// Validate protocol x image_mode matrix
	switch dest.ChannelType {
	case ChannelTypeWebhook:
		if dest.ImageMode != ImageModeURL && dest.ImageMode != ImageModeMultipart {
			return ErrInvalidImageMode
		}
		if err := validateWebhookTarget(dest.ConfigJSON, secretRaw); err != nil {
			return err
		}
	case ChannelTypeMQTT:
		if dest.ImageMode != ImageModeURL && dest.ImageMode != ImageModeBase64 {
			return ErrInvalidImageMode
		}
		if err := validateMQTTTarget(dest.ConfigJSON, dest.MQTTClientID); err != nil {
			return err
		}
	}

	needsPublicBaseURL := dest.EventsEnabled && dest.ImageMode == ImageModeURL
	if needsPublicBaseURL && strings.TrimSpace(s.config.PublicBaseURL) == "" {
		return ErrPublicBaseURLNotConfigured
	}

	return nil
}

func validateDestinationName(name string) error {
	if name == "" {
		return fmt.Errorf("%w: destination name is required", ErrInvalidConfig)
	}
	if utf8.RuneCountInString(name) > MaxDestinationNameLength {
		return fmt.Errorf(
			"%w: destination name must not exceed %d characters",
			ErrInvalidConfig,
			MaxDestinationNameLength,
		)
	}
	return nil
}

func validateWebhookTarget(configJSON string, secretRaw destinationSecret) error {
	var cfg webhookConfig
	if configJSON != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if !isValidWebhookAuthType(cfg.AuthType) {
			return fmt.Errorf("%w: unsupported webhook auth_type %q", ErrInvalidConfig, cfg.AuthType)
		}
		if cfg.TimeoutSeconds != 0 && !isValidWebhookTimeout(cfg.TimeoutSeconds) {
			return fmt.Errorf(
				"%w: timeout_seconds must be between %d and %d",
				ErrInvalidConfig,
				MinWebhookTimeoutSeconds,
				MaxWebhookTimeoutSeconds,
			)
		}
		if cfg.HeartbeatIntervalSeconds != 0 && !isValidHeartbeatInterval(cfg.HeartbeatIntervalSeconds) {
			return fmt.Errorf("%w: heartbeat_interval_seconds must be between 15 and 3600", ErrInvalidConfig)
		}
	}
	if sec, ok := secretRaw.(*webhookSecret); ok && sec != nil {
		if err := validateWebhookURL(sec.URL); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := validateWebhookAuthCredentials(cfg.AuthType, sec); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
		if err := validateWebhookHeartbeatOrigin(cfg.AuthType, sec); err != nil {
			return err
		}
	}
	return nil
}

func validateWebhookHeartbeatOrigin(authType string, sec *webhookSecret) error {
	hbURL := strings.TrimSpace(sec.HeartbeatURL)
	if hbURL == "" {
		return nil
	}
	if err := validateWebhookURL(hbURL); err != nil {
		return err
	}
	hasCredentials := (authType != "" && authType != AuthTypeNone) || len(sec.Headers) > 0
	if hasCredentials && !sameOrigin(sec.URL, hbURL) {
		return ErrCrossOriginHeartbeatURL
	}
	return nil
}

// validateFilterEventTypes rejects anything outside the v1 short names. Unknown values are
// refused rather than dropped: silently dropping the last entry would turn the filter into
// "match everything".
func validateFilterEventTypes(filterJSON string) error {
	if strings.TrimSpace(filterJSON) == "" {
		return nil
	}
	var filter destinationFilter
	if err := json.Unmarshal([]byte(filterJSON), &filter); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	for _, eventType := range filter.EventTypes {
		if !slices.Contains(filterEventTypes, eventType) {
			return fmt.Errorf("%w: %q", ErrInvalidFilterEventType, eventType)
		}
	}
	return nil
}

func validateMQTTBroker(rawBroker string) error {
	trimmed := strings.TrimSpace(rawBroker)
	if trimmed == "" {
		return errors.New("broker url is required for mqtt")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil {
		return errors.New("mqtt broker url must be valid and must not contain userinfo")
	}
	return nil
}

func validateMQTTClientID(clientID string) error {
	if len(clientID) > MaxMQTTClientIDLength {
		return fmt.Errorf("mqtt client id must not exceed %d characters", MaxMQTTClientIDLength)
	}
	return nil
}

func validateMQTTTarget(configJSON string, clientID *string) error {
	if clientID != nil {
		if err := validateMQTTClientID(strings.TrimSpace(*clientID)); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
		}
	}
	if configJSON == "" {
		return nil
	}
	var cfg mqttConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if err := validateMQTTBroker(cfg.Broker); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfig, err)
	}
	if strings.TrimSpace(cfg.Topic) == "" {
		return fmt.Errorf("%w: topic is required for mqtt", ErrInvalidConfig)
	}
	if strings.ContainsAny(cfg.Topic, "+#") {
		return fmt.Errorf("%w: mqtt publish topic must not contain wildcards", ErrInvalidConfig)
	}
	if !isValidMQTTQoS(cfg.QoS) {
		return fmt.Errorf("%w: mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
	}
	if cfg.KeepAliveSeconds != 0 && !isValidKeepAliveSeconds(cfg.KeepAliveSeconds) {
		return fmt.Errorf(
			"%w: keep_alive_seconds must be between %d and %d",
			ErrInvalidConfig,
			MinKeepAliveSeconds,
			MaxKeepAliveSeconds,
		)
	}
	if cfg.HeartbeatIntervalSeconds != 0 && !isValidHeartbeatInterval(cfg.HeartbeatIntervalSeconds) {
		return fmt.Errorf("%w: heartbeat_interval_seconds must be between 15 and 3600", ErrInvalidConfig)
	}
	if strings.TrimSpace(cfg.HeartbeatTopic) != "" && strings.ContainsAny(cfg.HeartbeatTopic, "+#") {
		return fmt.Errorf("%w: mqtt heartbeat_topic must not contain wildcards", ErrInvalidConfig)
	}
	if cfg.InlineMaxKB != 0 && !isValidInlineMaxKB(cfg.InlineMaxKB) {
		return fmt.Errorf(
			"%w: inline_max_kb must be between %d and %d",
			ErrInvalidConfig,
			MinInlineMaxKB,
			MaxInlineMaxKB,
		)
	}
	return nil
}
