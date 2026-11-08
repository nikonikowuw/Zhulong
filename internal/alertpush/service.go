package alertpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

var (
	ErrPublicBaseURLNotConfigured = errors.New("public_base_url is required for url image mode")
	ErrInvalidChannelType         = errors.New("invalid channel_type")
	ErrInvalidImageMode           = errors.New("invalid image_mode for channel_type")
	ErrInvalidConfig              = errors.New("invalid alert destination config")
	ErrReservedHeaderUsed         = errors.New("reserved header is not allowed in custom headers")
)

// Service provides high-level domain operations for alert push destinations.
type Service struct {
	store      *Store
	dispatcher *Dispatcher
	signer     *URLSigner
	media      MediaReader
	config     Config
	logger     *zap.Logger
}

// UpdateParams contains mutable fields for updating an alert destination.
type UpdateParams struct {
	Name       *string
	ImageMode  *string
	ConfigJSON *string
	FilterJSON *string
	SecretRaw  any
	ClientID   *string
}

// NewService creates a new alertpush Service instance.
func NewService(
	store *Store,
	dispatcher *Dispatcher,
	signer *URLSigner,
	media MediaReader,
	cfg Config,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:      store,
		dispatcher: dispatcher,
		signer:     signer,
		media:      media,
		config:     cfg,
		logger:     logger.Named("alertpush.service"),
	}
}

// Create validates and saves a new destination, then requests reload.
func (s *Service) Create(ctx context.Context, dest *AlertDestination, secretRaw any) error {
	if err := s.validateDestination(dest); err != nil {
		return err
	}

	dest.ID = idutil.New()

	// If MQTT, ensure mqtt_client_id is assigned
	if dest.ChannelType == ChannelTypeMQTT {
		if dest.MQTTClientID == nil || strings.TrimSpace(*dest.MQTTClientID) == "" {
			cID := "z" + dest.ID[4:]
			dest.MQTTClientID = &cID
		}
		// If derived or provided ID conflicts, retry regenerating ID if derived, or fail if custom
		conflict, err := s.store.CheckMQTTClientIDConflict(ctx, "", *dest.MQTTClientID)
		if err != nil {
			return err
		}
		if conflict {
			// If it's custom, reject with 422 ErrClientIDConflict
			return ErrClientIDConflict
		}
	}

	ciphertext, err := s.store.EncryptSecret(dest.ID, secretRaw)
	if err != nil {
		return fmt.Errorf("encrypt secret: %w", err)
	}
	dest.SecretCiphertext = ciphertext

	if err := s.store.Create(ctx, dest); err != nil {
		return err
	}

	s.dispatcher.RequestReload()
	return nil
}

// Update updates an existing destination.
func (s *Service) Update(ctx context.Context, existing *AlertDestination, params UpdateParams) error {
	if params.Name != nil && strings.TrimSpace(*params.Name) != "" {
		existing.Name = strings.TrimSpace(*params.Name)
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
		targetClientID := ""
		if params.ClientID != nil && strings.TrimSpace(*params.ClientID) != "" {
			targetClientID = strings.TrimSpace(*params.ClientID)
		} else {
			targetClientID = "z" + existing.ID[4:]
		}

		conflict, err := s.store.CheckMQTTClientIDConflict(ctx, existing.ID, targetClientID)
		if err != nil {
			return err
		}
		if conflict {
			return ErrClientIDConflict
		}
		existing.MQTTClientID = &targetClientID
	}

	if err := s.validateDestination(existing); err != nil {
		return err
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

// Delete removes destination and requests reload.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	s.dispatcher.RequestReload()
	return nil
}

// TestExecutionResult holds the result of a connection test.
type TestExecutionResult struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}

// Test attempts sending a dummy ping/test event to the destination.
func (s *Service) Test(ctx context.Context, dest *AlertDestination, secretRaw any) (TestExecutionResult, error) {
	testTimeout := 10 * time.Second
	testCtx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()

	var sender Sender
	if dest.ChannelType == ChannelTypeWebhook {
		sec, ok := secretRaw.(*webhookSecret)
		if !ok || sec == nil || sec.URL == "" {
			return TestExecutionResult{OK: false, Reason: "config_invalid"}, ErrInvalidConfig
		}
		var cfg WebhookConfig
		_ = json.Unmarshal([]byte(dest.ConfigJSON), &cfg)

		wSender, err := NewWebhookSender(WebhookSenderOptions{
			ID:            dest.ID,
			Name:          dest.Name,
			ImageMode:     dest.ImageMode,
			PublicBaseURL: s.config.PublicBaseURL,
			SignedURLTTL:  s.config.SignedURLTTL,
			Signer:        s.signer,
			Media:         s.media,
			URL:           sec.URL,
			AuthType:      cfg.AuthType,
			Token:         sec.Token,
			Username:      sec.Username,
			Password:      sec.Password,
			Headers:       sec.Headers,
			Timeout:       testTimeout,
			Identity:      s.config.DeviceID,
			Logger:        s.logger,
		})
		if err != nil {
			return TestExecutionResult{OK: false, Reason: classifyError(err)}, nil
		}
		defer wSender.Close()
		sender = wSender
	} else if dest.ChannelType == ChannelTypeMQTT {
		sec, ok := secretRaw.(*mqttSecret)
		if !ok || sec == nil {
			sec = &mqttSecret{}
		}
		var cfg MQTTConfig
		_ = json.Unmarshal([]byte(dest.ConfigJSON), &cfg)

		// Probe connectivity must strictly use a transient ClientID to avoid kicking online workers off broker
		clientID := "test-probe-" + idutil.New()[:12]

		mSender, err := NewMQTTSender(MQTTSenderOptions{
			ID:            dest.ID,
			Name:          dest.Name,
			ImageMode:     dest.ImageMode,
			PublicBaseURL: s.config.PublicBaseURL,
			SignedURLTTL:  s.config.SignedURLTTL,
			Signer:        s.signer,
			Media:         s.media,
			Broker:        cfg.Broker,
			ClientID:      clientID,
			Username:      cfg.Username,
			Password:      sec.Password,
			Topic:         cfg.Topic,
			QoS:           cfg.QoS,
			KeepAlive:     30 * time.Second,
			TLSSkipVerify: cfg.TLSSkipVerify,
			Identity:      s.config.DeviceID,
			Logger:        s.logger,
		})
		if err != nil {
			return TestExecutionResult{OK: false, Reason: classifyError(err)}, nil
		}
		defer mSender.Close()
		sender = mSender
	} else {
		return TestExecutionResult{OK: false, Reason: "unsupported_channel"}, ErrInvalidChannelType
	}

	testEvent := record.AlarmEvent{
		ID:         "01TESTALARM0000000000000",
		CameraID:   "test_cam",
		AlarmType:  "test_ping",
		Score:      1.0,
		CapturedAt: time.Now().UTC(),
	}

	start := time.Now()
	err := sender.Send(testCtx, testEvent)
	latency := time.Since(start).Milliseconds()

	success := err == nil
	reason := ""
	if err != nil {
		reason = classifyError(err)
	}

	if dest.ID != "" && dest.Enabled {
		s.dispatcher.UpdateTestResult(dest.ID, success, reason)
	}

	return TestExecutionResult{
		OK:        success,
		LatencyMs: latency,
		Reason:    reason,
	}, nil
}

func (s *Service) validateDestination(dest *AlertDestination) error {
	if dest.ChannelType != ChannelTypeWebhook && dest.ChannelType != ChannelTypeMQTT {
		return ErrInvalidChannelType
	}

	// Validate protocol x image_mode matrix
	switch dest.ChannelType {
	case ChannelTypeWebhook:
		if dest.ImageMode != ImageModeURL && dest.ImageMode != ImageModeMultipart {
			return ErrInvalidImageMode
		}
	case ChannelTypeMQTT:
		if dest.ImageMode != ImageModeURL && dest.ImageMode != ImageModeBase64 {
			return ErrInvalidImageMode
		}
	}

	if dest.ImageMode == ImageModeURL && strings.TrimSpace(s.config.PublicBaseURL) == "" {
		return ErrPublicBaseURLNotConfigured
	}

	return nil
}
