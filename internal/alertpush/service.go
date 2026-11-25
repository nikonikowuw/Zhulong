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

// FieldValidationError describes a single field-level validation failure in domain terms
// without coupling the service layer to HTTP transport types.
type FieldValidationError struct {
	Field   string
	Code    string
	Message string
}

// ValidationError aggregates one or more field-level validation failures while matching
// ErrInvalidConfig and preserving an optional sentinel cause.
type ValidationError struct {
	Details []FieldValidationError
	Cause   error
}

// Error returns a concise human-readable description of the first validation failure.
func (e *ValidationError) Error() string {
	if e == nil || len(e.Details) == 0 {
		return ErrInvalidConfig.Error()
	}
	first := e.Details[0]
	if first.Message != "" {
		return fmt.Sprintf("%s: %s", first.Field, first.Message)
	}
	return fmt.Sprintf("%s: %s", first.Field, first.Code)
}

// Unwrap exposes the optional domain cause sentinel.
func (e *ValidationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// Is allows callers checking errors.Is(err, ErrInvalidConfig) to match field validation errors.
func (e *ValidationError) Is(target error) bool {
	return target == ErrInvalidConfig
}

func newFieldError(field, code, message string, cause error) *ValidationError {
	return &ValidationError{
		Details: []FieldValidationError{{
			Field:   field,
			Code:    code,
			Message: message,
		}},
		Cause: cause,
	}
}

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
	normalizedFilter, err := normalizeFilterJSON(dest.FilterJSON)
	if err != nil {
		return err
	}
	dest.FilterJSON = normalizedFilter

	dest.ID = idutil.New()

	// If MQTT, ensure mqtt_client_id is assigned
	if dest.ChannelType == ChannelTypeMQTT {
		if dest.MQTTClientID == nil || strings.TrimSpace(*dest.MQTTClientID) == "" {
			cID := effectiveMQTTClientID(dest.ID, nil)
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

	if s.dispatcher != nil {
		s.dispatcher.RequestReload()
	}
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
	if params.ImageMode != nil {
		trimmed := strings.TrimSpace(*params.ImageMode)
		if trimmed == "" {
			return newFieldError("imageMode", "REQUIRED", "image mode is required", ErrInvalidConfig)
		}
		existing.ImageMode = trimmed
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
			targetClientID = effectiveMQTTClientID(existing.ID, nil)
		}
		existing.MQTTClientID = &targetClientID
	}

	// Resolve effective secret for validation even when the caller omitted SecretRaw,
	// so config changes (e.g. auth_type) cannot bypass credential/URL/header checks.
	effectiveSecret := params.SecretRaw
	var persistedSecret destinationSecret
	if len(existing.SecretCiphertext) > 0 {
		switch existing.ChannelType {
		case ChannelTypeWebhook:
			sec, err := s.store.DecryptWebhookSecret(existing.ID, existing.SecretCiphertext)
			if err != nil {
				return fmt.Errorf("decrypt existing webhook secret: %w", err)
			}
			persistedSecret = sec
		case ChannelTypeMQTT:
			sec, err := s.store.DecryptMQTTSecret(existing.ID, existing.SecretCiphertext)
			if err != nil {
				return fmt.Errorf("decrypt existing mqtt secret: %w", err)
			}
			persistedSecret = sec
		}
	}
	if effectiveSecret == nil {
		effectiveSecret = persistedSecret
	}

	if err := s.validateDestination(existing, effectiveSecret); err != nil {
		return err
	}
	normalizedFilter, err := normalizeFilterJSON(existing.FilterJSON)
	if err != nil {
		return err
	}
	existing.FilterJSON = normalizedFilter

	if existing.ChannelType == ChannelTypeMQTT && existing.MQTTClientID != nil {
		conflict, err := s.store.CheckMQTTClientIDConflict(ctx, existing.ID, *existing.MQTTClientID)
		if err != nil {
			return err
		}
		if conflict {
			return ErrClientIDConflict
		}
	}

	// Skip re-encryption when the secret payload did not change so non-secret updates
	// (such as renaming a destination) preserve ciphertext and worker fingerprint.
	if params.SecretRaw != nil && shouldReencryptSecret(existing.SecretCiphertext, persistedSecret, params.SecretRaw) {
		ciphertext, err := s.store.EncryptSecret(existing.ID, params.SecretRaw)
		if err != nil {
			return fmt.Errorf("encrypt secret: %w", err)
		}
		existing.SecretCiphertext = ciphertext
	}

	if err := s.store.Update(ctx, existing); err != nil {
		return err
	}

	if s.dispatcher != nil {
		s.dispatcher.RequestReload()
	}
	return nil
}

func shouldReencryptSecret(existingCiphertext []byte, persisted, updated destinationSecret) bool {
	if len(existingCiphertext) == 0 || persisted == nil {
		return true
	}
	switch u := updated.(type) {
	case *webhookSecret:
		p, ok := persisted.(*webhookSecret)
		if !ok {
			return true
		}
		return !webhookSecretsEqual(p, u)
	case *mqttSecret:
		p, ok := persisted.(*mqttSecret)
		if !ok {
			return true
		}
		if p == nil {
			p = &mqttSecret{}
		}
		if u == nil {
			u = &mqttSecret{}
		}
		return *p != *u
	default:
		return true
	}
}

// SetEnabled toggles enabled state and requests reload.
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool) error {
	if err := s.store.SetEnabled(ctx, id, enabled); err != nil {
		return err
	}
	if s.dispatcher != nil {
		s.dispatcher.RequestReload()
	}
	return nil
}

// Delete removes destination, clears its cached runtime state, and requests reload.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	if s.dispatcher != nil {
		s.dispatcher.RemoveDestinationState(id)
		s.dispatcher.RequestReload()
	}
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
		if !ok || sec == nil || strings.TrimSpace(sec.URL) == "" {
			return TestExecutionResult{OK: false, Reason: "config_invalid"}, newFieldError(
				"config.url",
				"REQUIRED",
				"url is required for webhook",
				ErrInvalidConfig,
			)
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

	if recordRuntimeStatus && dest.ID != "" && dest.Enabled && s.dispatcher != nil {
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
		ID:         idutil.New(),
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
		URL:               strings.TrimSpace(sec.URL),
		HeartbeatURL:      strings.TrimSpace(sec.HeartbeatURL),
		AuthType:          cfg.AuthType,
		Token:             sec.Token,
		Username:          sec.Username,
		Password:          sec.Password,
		SigningSecret:     sec.SigningSecret,
		Headers:           sec.Headers,
		Timeout:           timeout,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		AllowLoopback:     s.config.AllowLoopback,
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

	keepAlive := 30 * time.Second
	if isValidKeepAliveSeconds(cfg.KeepAliveSeconds) {
		keepAlive = time.Duration(cfg.KeepAliveSeconds) * time.Second
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
		Broker:            strings.TrimSpace(cfg.Broker),
		ClientID:          clientID,
		Username:          cfg.Username,
		Password:          sec.Password,
		Topic:             strings.TrimSpace(cfg.Topic),
		HeartbeatEnabled:  dest.HeartbeatEnabled,
		HeartbeatTopic:    strings.TrimSpace(cfg.HeartbeatTopic),
		LWTEnabled:        false,
		QoS:               cfg.QoS,
		KeepAlive:         keepAlive,
		TLSSkipVerify:     cfg.TLSSkipVerify,
		AllowLoopback:     s.config.AllowLoopback,
		DisableReconnect:  true,
		InlineMaxBytes:    int64(normalizeInlineMaxKB(cfg.InlineMaxKB)) * 1024,
		Identity:          s.config.DeviceID,
		Logger:            s.logger,
	})
}

func (s *Service) validateDestination(dest *AlertDestination, secretRaw destinationSecret) error {
	if !dest.EventsEnabled && !dest.HeartbeatEnabled {
		return newFieldError(
			"eventsEnabled",
			CodeAlertCapabilityRequired,
			"at least one of eventsEnabled or heartbeatEnabled must be true",
			ErrInvalidConfig,
		)
	}

	if dest.ChannelType != ChannelTypeWebhook && dest.ChannelType != ChannelTypeMQTT {
		return newFieldError(
			"channelType",
			"INVALID_VALUE",
			fmt.Sprintf("unsupported channel type %q", dest.ChannelType),
			ErrInvalidChannelType,
		)
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
		if err := validateWebhookTarget(
			dest.ConfigJSON,
			secretRaw,
			dest.HeartbeatEnabled,
			s.config.AllowLoopback,
		); err != nil {
			return err
		}
	case ChannelTypeMQTT:
		if dest.ImageMode != ImageModeURL && dest.ImageMode != ImageModeBase64 {
			return ErrInvalidImageMode
		}
		if err := validateMQTTTarget(
			dest.ConfigJSON,
			dest.MQTTClientID,
			dest.HeartbeatEnabled,
			s.config.AllowLoopback,
		); err != nil {
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
		return newFieldError("name", "REQUIRED", "destination name is required", ErrInvalidConfig)
	}
	if utf8.RuneCountInString(name) > MaxDestinationNameLength {
		return newFieldError(
			"name",
			"MAX_LENGTH",
			fmt.Sprintf("destination name must not exceed %d characters", MaxDestinationNameLength),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateWebhookTarget(
	configJSON string,
	secretRaw destinationSecret,
	heartbeatEnabled bool,
	allowLoopback bool,
) error {
	var cfg webhookConfig
	if strings.TrimSpace(configJSON) != "" {
		if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
			return newFieldError("config", "INVALID_FORMAT", err.Error(), ErrInvalidConfig)
		}
		if !isValidWebhookAuthType(cfg.AuthType) {
			return newFieldError(
				"config.authType",
				"INVALID_VALUE",
				fmt.Sprintf("unsupported webhook auth_type %q", cfg.AuthType),
				ErrInvalidConfig,
			)
		}
		if cfg.TimeoutSeconds != 0 {
			if err := validateWebhookTimeoutField(cfg.TimeoutSeconds); err != nil {
				return err
			}
		}
		if heartbeatEnabled && cfg.HeartbeatIntervalSeconds != 0 {
			if err := validateHeartbeatIntervalField(cfg.HeartbeatIntervalSeconds); err != nil {
				return err
			}
		}
	}

	sec, ok := secretRaw.(*webhookSecret)
	if !ok || sec == nil {
		return newFieldError("config.url", "REQUIRED", "url is required for webhook", ErrInvalidConfig)
	}
	if err := validateWebhookURLField("config.url", sec.URL, allowLoopback); err != nil {
		return err
	}
	if err := validateWebhookHeaders(sec.Headers); err != nil {
		return err
	}
	if err := validateWebhookAuthCredentials(cfg.AuthType, sec); err != nil {
		return err
	}
	if heartbeatEnabled {
		if err := validateWebhookHeartbeatOrigin(cfg.AuthType, sec, allowLoopback); err != nil {
			return err
		}
	}
	return nil
}

func validateWebhookTimeoutField(sec int) error {
	if sec < MinWebhookTimeoutSeconds {
		return newFieldError(
			"config.timeoutSeconds",
			"MIN_VALUE",
			fmt.Sprintf("timeoutSeconds must be at least %d", MinWebhookTimeoutSeconds),
			ErrInvalidConfig,
		)
	}
	if sec > MaxWebhookTimeoutSeconds {
		return newFieldError(
			"config.timeoutSeconds",
			"MAX_VALUE",
			fmt.Sprintf("timeoutSeconds must not exceed %d", MaxWebhookTimeoutSeconds),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateHeartbeatIntervalField(sec int) error {
	if sec < MinHeartbeatIntervalSeconds {
		return newFieldError(
			"config.heartbeatIntervalSeconds",
			"MIN_VALUE",
			fmt.Sprintf("heartbeatIntervalSeconds must be at least %d", MinHeartbeatIntervalSeconds),
			ErrInvalidConfig,
		)
	}
	if sec > MaxHeartbeatIntervalSeconds {
		return newFieldError(
			"config.heartbeatIntervalSeconds",
			"MAX_VALUE",
			fmt.Sprintf("heartbeatIntervalSeconds must not exceed %d", MaxHeartbeatIntervalSeconds),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateKeepAliveField(sec int) error {
	if sec < MinKeepAliveSeconds {
		return newFieldError(
			"config.keepAliveSeconds",
			"MIN_VALUE",
			fmt.Sprintf("keepAliveSeconds must be at least %d", MinKeepAliveSeconds),
			ErrInvalidConfig,
		)
	}
	if sec > MaxKeepAliveSeconds {
		return newFieldError(
			"config.keepAliveSeconds",
			"MAX_VALUE",
			fmt.Sprintf("keepAliveSeconds must not exceed %d", MaxKeepAliveSeconds),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateInlineMaxKBField(kb int) error {
	if kb < MinInlineMaxKB {
		return newFieldError(
			"config.inlineMaxKb",
			"MIN_VALUE",
			fmt.Sprintf("inlineMaxKb must be at least %d", MinInlineMaxKB),
			ErrInvalidConfig,
		)
	}
	if kb > MaxInlineMaxKB {
		return newFieldError(
			"config.inlineMaxKb",
			"MAX_VALUE",
			fmt.Sprintf("inlineMaxKb must not exceed %d", MaxInlineMaxKB),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateWebhookURLField(field, rawURL string, allowLoopback bool) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return newFieldError(field, "REQUIRED", "url is required for webhook", ErrInvalidConfig)
	}
	u, err := url.Parse(trimmed)
	scheme := ""
	if u != nil {
		scheme = strings.ToLower(u.Scheme)
	}
	isValidScheme := err == nil && u != nil && u.Host != "" && (scheme == "http" || scheme == "https")
	if !isValidScheme {
		return newFieldError(field, "INVALID_URL", "webhook url must be a valid http or https url", ErrInvalidConfig)
	}
	if u.User != nil {
		return newFieldError(
			field,
			CodeAlertURLUserinfoForbidden,
			"webhook url must not contain userinfo",
			ErrInvalidConfig,
		)
	}
	if err := validateWebhookTargetHost(u.Hostname(), allowLoopback); err != nil {
		return newFieldError(field, CodeAlertURLBlocked, err.Error(), err)
	}
	return nil
}

func validateWebhookHeartbeatOrigin(authType string, sec *webhookSecret, allowLoopback bool) error {
	hbURL := strings.TrimSpace(sec.HeartbeatURL)
	if hbURL == "" {
		return nil
	}
	if err := validateWebhookURLField("config.heartbeatUrl", hbURL, allowLoopback); err != nil {
		return err
	}
	hasCredentials := (authType != "" && authType != AuthTypeNone) ||
		len(sec.Headers) > 0 ||
		sec.SigningSecret != ""
	if hasCredentials && !sameOrigin(sec.URL, hbURL) {
		return newFieldError(
			"config.heartbeatUrl",
			CodeAlertHeartbeatCrossOrigin,
			ErrCrossOriginHeartbeatURL.Error(),
			ErrCrossOriginHeartbeatURL,
		)
	}
	return nil
}

func validateWebhookHeaders(headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	if len(headers) > MaxCustomHeaders {
		return newFieldError(
			"secret.headers",
			CodeAlertHeaderLimitExceeded,
			fmt.Sprintf("custom headers must not exceed %d entries", MaxCustomHeaders),
			ErrInvalidConfig,
		)
	}
	seen := make(map[string]struct{}, len(headers))
	for hk, hv := range headers {
		if !isValidHTTPHeaderName(hk) {
			return newFieldError(
				"secret.headers",
				CodeAlertHeaderInvalid,
				"invalid custom header name",
				ErrInvalidConfig,
			)
		}
		if isReservedHeader(hk) {
			return newFieldError(
				"secret.headers",
				CodeAlertHeaderReserved,
				ErrReservedHeaderUsed.Error(),
				ErrReservedHeaderUsed,
			)
		}
		lower := strings.ToLower(hk)
		if _, dup := seen[lower]; dup {
			return newFieldError(
				"secret.headers",
				CodeAlertHeaderDuplicate,
				"duplicate custom header name",
				ErrInvalidConfig,
			)
		}
		seen[lower] = struct{}{}

		if !isValidHTTPHeaderValue(hv) {
			return newFieldError(
				"secret.headers",
				CodeAlertHeaderInvalid,
				"invalid custom header value",
				ErrInvalidConfig,
			)
		}
	}
	return nil
}

// isValidHTTPHeaderName validates RFC 7230 §3.2.6 field-name token characters.
func isValidHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		if !isHTTPTokenByte(c) {
			return false
		}
	}
	return true
}

func isHTTPTokenByte(c byte) bool {
	if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

// isValidHTTPHeaderValue rejects control bytes (NUL, CR, LF, DEL, etc.) per RFC 7230 §3.2.6.
func isValidHTTPHeaderValue(val string) bool {
	for i := range len(val) {
		c := val[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
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
		return newFieldError("filter", "INVALID_FORMAT", err.Error(), ErrInvalidConfig)
	}
	for _, eventType := range filter.EventTypes {
		if !slices.Contains(filterEventTypes, strings.TrimSpace(eventType)) {
			return fmt.Errorf("%w: %q", ErrInvalidFilterEventType, eventType)
		}
	}
	return nil
}

// normalizeFilterJSON trims and deduplicates filter slices after validation succeeds.
func normalizeFilterJSON(filterJSON string) (string, error) {
	if strings.TrimSpace(filterJSON) == "" {
		return "{}", nil
	}
	var filter destinationFilter
	if err := json.Unmarshal([]byte(filterJSON), &filter); err != nil {
		return "", newFieldError("filter", "INVALID_FORMAT", err.Error(), ErrInvalidConfig)
	}
	filter.EventTypes = dedupePreserveEmpty(filter.EventTypes)
	filter.AlgorithmIDs = dedupeTrimmed(filter.AlgorithmIDs)
	filter.CameraIDs = dedupeTrimmed(filter.CameraIDs)
	b, err := json.Marshal(filter)
	if err != nil {
		return "", fmt.Errorf("marshal destination filter: %w", err)
	}
	return string(b), nil
}

func dedupePreserveEmpty(items []string) []string {
	if len(items) == 0 {
		return []string{}
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

func validateMQTTBrokerField(rawBroker string, allowLoopback bool) error {
	trimmed := strings.TrimSpace(rawBroker)
	if trimmed == "" {
		return newFieldError("config.broker", "REQUIRED", "broker url is required for mqtt", ErrInvalidConfig)
	}
	u, err := url.Parse(trimmed)
	if err != nil || u == nil || u.Scheme == "" || u.Host == "" {
		return newFieldError("config.broker", "INVALID_URL", "mqtt broker url must be valid", ErrInvalidConfig)
	}
	if u.User != nil {
		return newFieldError(
			"config.broker",
			CodeAlertURLUserinfoForbidden,
			"mqtt broker url must not contain userinfo",
			ErrInvalidConfig,
		)
	}
	if !isAllowedMQTTScheme(u.Scheme) {
		return newFieldError(
			"config.broker",
			CodeAlertMQTTSchemeUnsupported,
			fmt.Sprintf("unsupported mqtt broker scheme %q", u.Scheme),
			ErrInvalidMQTTScheme,
		)
	}
	if err := validateWebhookTargetHost(u.Hostname(), allowLoopback); err != nil {
		return newFieldError("config.broker", CodeAlertURLBlocked, err.Error(), err)
	}
	return nil
}

func validateMQTTClientID(clientID string) error {
	if len(clientID) > MaxMQTTClientIDLength {
		return newFieldError(
			"config.clientId",
			"MAX_LENGTH",
			fmt.Sprintf("mqtt client id must not exceed %d bytes", MaxMQTTClientIDLength),
			ErrInvalidConfig,
		)
	}
	return nil
}

func validateMQTTTarget(
	configJSON string,
	clientID *string,
	heartbeatEnabled bool,
	allowLoopback bool,
) error {
	if clientID != nil {
		if err := validateMQTTClientID(strings.TrimSpace(*clientID)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(configJSON) == "" {
		return &ValidationError{
			Details: []FieldValidationError{
				{Field: "config.broker", Code: "REQUIRED", Message: "broker url is required for mqtt"},
				{Field: "config.topic", Code: "REQUIRED", Message: "topic is required for mqtt"},
			},
			Cause: ErrInvalidConfig,
		}
	}
	var cfg mqttConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return newFieldError("config", "INVALID_FORMAT", err.Error(), ErrInvalidConfig)
	}
	if err := validateMQTTBrokerField(cfg.Broker, allowLoopback); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Topic) == "" {
		return newFieldError("config.topic", "REQUIRED", "topic is required for mqtt", ErrInvalidConfig)
	}
	if strings.ContainsAny(cfg.Topic, "+#") {
		return newFieldError(
			"config.topic",
			CodeAlertMQTTWildcardForbidden,
			"mqtt publish topic must not contain wildcards",
			ErrInvalidConfig,
		)
	}
	if !isValidMQTTQoS(cfg.QoS) {
		return newFieldError("config.qos", "MAX_VALUE", "mqtt qos must be 0, 1 or 2", ErrInvalidConfig)
	}
	if cfg.KeepAliveSeconds != 0 {
		if err := validateKeepAliveField(cfg.KeepAliveSeconds); err != nil {
			return err
		}
	}
	if heartbeatEnabled {
		if cfg.HeartbeatIntervalSeconds != 0 {
			if err := validateHeartbeatIntervalField(cfg.HeartbeatIntervalSeconds); err != nil {
				return err
			}
		}
		if strings.TrimSpace(cfg.HeartbeatTopic) != "" && strings.ContainsAny(cfg.HeartbeatTopic, "+#") {
			return newFieldError(
				"config.heartbeatTopic",
				CodeAlertMQTTWildcardForbidden,
				"mqtt heartbeat_topic must not contain wildcards",
				ErrInvalidConfig,
			)
		}
	}
	if cfg.InlineMaxKB != 0 {
		if err := validateInlineMaxKBField(cfg.InlineMaxKB); err != nil {
			return err
		}
	}
	return nil
}

func sameMQTTBroker(rawA, rawB string) bool {
	uA, errA := url.Parse(strings.TrimSpace(rawA))
	uB, errB := url.Parse(strings.TrimSpace(rawB))
	if errA != nil || errB != nil || uA == nil || uB == nil {
		return false
	}
	schemeA := strings.ToLower(uA.Scheme)
	schemeB := strings.ToLower(uB.Scheme)
	if schemeA == "" || schemeA != schemeB {
		return false
	}
	hostA := strings.ToLower(uA.Hostname())
	hostB := strings.ToLower(uB.Hostname())
	if hostA == "" || hostA != hostB {
		return false
	}
	if effectiveMQTTPort(schemeA, uA.Port()) != effectiveMQTTPort(schemeB, uB.Port()) {
		return false
	}
	return uA.Path == uB.Path
}

func effectiveMQTTPort(scheme, explicit string) string {
	if explicit != "" {
		return explicit
	}
	switch scheme {
	case "ssl", "tls", "tcps", "mqtts":
		return "8883"
	case "ws":
		return "80"
	case "wss":
		return "443"
	default:
		return "1883"
	}
}
