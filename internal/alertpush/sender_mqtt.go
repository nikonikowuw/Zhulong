package alertpush

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

const (
	maxMQTTBase64Bytes         = 500 * 1024 // 500KB
	mqttGracefulOfflineTimeout = 500 * time.Millisecond
)

var (
	ErrMQTTBrokerMissing = errors.New("mqtt broker url is missing")
	ErrMQTTTopicMissing  = errors.New("mqtt topic is missing")
	ErrMQTTWildcardTopic = errors.New("mqtt publish topic must not contain wildcards + or #")
	ErrMQTTPublishFailed = errors.New("mqtt publish failed")
	ErrMQTTNotConnected  = errors.New("mqtt client is not connected")
)

// MQTTSenderOptions contains configuration for MQTTSender.
type MQTTSenderOptions struct {
	ID                string
	Name              string
	ImageMode         string
	SkipImageURLCheck bool
	PublicBaseURL     string
	SignedURLTTL      time.Duration
	Signer            *URLSigner
	Media             MediaReader
	Broker            string
	ClientID          string
	Username          string
	Password          string
	Topic             string
	HeartbeatEnabled  bool
	HeartbeatTopic    string
	LWTEnabled        bool
	QoS               uint8
	KeepAlive         time.Duration
	TLSSkipVerify     bool
	// Identity supplies the host device_id stamped onto every payload. Nil omits it.
	Identity DeviceIDProvider
	Logger   *zap.Logger
}

// MQTTSender dispatches alert and heartbeat events over MQTT protocol.
type MQTTSender struct {
	opts         MQTTSenderOptions
	client       mqtt.Client
	willDeviceID string
	willArmed    bool
	logger       *zap.Logger
	mu           sync.Mutex
	closed       bool
}

// NewMQTTSender constructs a new MQTTSender.
func NewMQTTSender(opts MQTTSenderOptions) (*MQTTSender, error) {
	if strings.TrimSpace(opts.Broker) == "" {
		return nil, ErrMQTTBrokerMissing
	}
	if strings.TrimSpace(opts.Topic) == "" {
		return nil, ErrMQTTTopicMissing
	}
	if strings.ContainsAny(opts.Topic, "+#") {
		return nil, ErrMQTTWildcardTopic
	}
	if strings.TrimSpace(opts.HeartbeatTopic) != "" && strings.ContainsAny(opts.HeartbeatTopic, "+#") {
		return nil, ErrMQTTWildcardTopic
	}
	needsPublicBaseURL := !opts.SkipImageURLCheck && opts.ImageMode == ImageModeURL
	if needsPublicBaseURL && strings.TrimSpace(opts.PublicBaseURL) == "" {
		return nil, ErrPublicBaseURLMissing
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	keepAlive := opts.KeepAlive
	if keepAlive <= 0 {
		keepAlive = 30 * time.Second
	}

	clientOpts := mqtt.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetKeepAlive(keepAlive).
		SetConnectTimeout(5 * time.Second).
		SetAutoReconnect(true).
		SetConnectRetry(true).
		SetConnectRetryInterval(2 * time.Second)

	if opts.Username != "" {
		clientOpts.SetUsername(opts.Username)
	}
	if opts.Password != "" {
		clientOpts.SetPassword(opts.Password)
	}

	var willDeviceID string
	var willArmed bool
	if opts.HeartbeatEnabled && opts.LWTEnabled {
		willDeviceID = resolveDeviceID(opts.Identity)
		willPayload := mqttOfflineWillPayload{
			DeviceID:  willDeviceID,
			EventType: EventTypeOffline,
			Reason:    OfflineReasonConnectionLost,
		}
		willBytes, err := json.Marshal(willPayload)
		if err != nil {
			return nil, fmt.Errorf("marshal mqtt will payload: %w", err)
		}
		statusTopic := strings.TrimSpace(opts.HeartbeatTopic)
		if statusTopic == "" {
			statusTopic = opts.Topic
		}
		clientOpts.SetWill(
			statusTopic,
			string(willBytes),
			byte(opts.QoS),
			false,
		)
		willArmed = true
	}

	isSecureScheme := strings.HasPrefix(opts.Broker, "ssl://") ||
		strings.HasPrefix(opts.Broker, "tls://") ||
		strings.HasPrefix(opts.Broker, "tcps://")
	if isSecureScheme {
		clientOpts.SetTLSConfig(&tls.Config{
			InsecureSkipVerify: opts.TLSSkipVerify,
		})
	}

	client := mqtt.NewClient(clientOpts)
	sender := &MQTTSender{
		opts:         opts,
		client:       client,
		willDeviceID: willDeviceID,
		willArmed:    willArmed,
		logger:       logger.Named("alertpush.mqtt").With(zap.String("destination_id", opts.ID)),
	}

	return sender, nil
}

func (s *MQTTSender) statusTopic() string {
	if topic := strings.TrimSpace(s.opts.HeartbeatTopic); topic != "" {
		return topic
	}
	return s.opts.Topic
}

// hasStaleWillDeviceID reports whether an armed LWT carries an outdated device_id.
func (s *MQTTSender) hasStaleWillDeviceID() bool {
	if !s.willArmed {
		return false
	}
	return s.willDeviceID != resolveDeviceID(s.opts.Identity)
}

// Connect initiates connection to the MQTT broker asynchronously or returns error.
func (s *MQTTSender) Connect() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("mqtt sender is closed")
	}
	if s.client.IsConnected() {
		return nil
	}
	token := s.client.Connect()
	if token.WaitTimeout(5*time.Second) && token.Error() != nil {
		s.logger.Warn("mqtt broker connect failed",
			zap.String("broker", logsafe.URL(s.opts.Broker)),
			zap.Error(token.Error()),
		)
		return token.Error()
	}
	return nil
}

// IsConnected returns whether the underlying client is connected to broker.
func (s *MQTTSender) IsConnected() bool {
	if s.client == nil {
		return false
	}
	return s.client.IsConnected()
}

// Close publishes a bounded graceful-offline notice when LWT is armed, then disconnects.
func (s *MQTTSender) Close() error {
	return s.closeInternal(true)
}

// closeWithoutOffline cleanly disconnects so the broker discards any armed LWT without
// publishing a false graceful_shutdown event during in-place configuration hot-reloads.
func (s *MQTTSender) closeWithoutOffline() error {
	return s.closeInternal(false)
}

func (s *MQTTSender) closeInternal(emitOffline bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.client != nil && s.client.IsConnected() {
		if emitOffline && s.willArmed {
			s.publishGracefulOfflineLocked()
		}
		s.client.Disconnect(250)
	}
	return nil
}

func (s *MQTTSender) publishGracefulOfflineLocked() {
	offline := mqttOfflineGracefulPayload{
		ID:         idutil.New(),
		DeviceID:   resolveDeviceID(s.opts.Identity),
		EventType:  EventTypeOffline,
		ReportedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Reason:     OfflineReasonGracefulShutdown,
	}
	data, err := json.Marshal(offline)
	if err != nil {
		return
	}
	tok := s.client.Publish(
		s.statusTopic(),
		byte(s.opts.QoS),
		false,
		data,
	)
	if !tok.WaitTimeout(mqttGracefulOfflineTimeout) {
		s.logger.Debug("publish graceful offline notice timed out")
		return
	}
	if pubErr := tok.Error(); pubErr != nil {
		s.logger.Debug("publish graceful offline notice failed", zap.Error(pubErr))
	}
}

// Send formats and publishes an alarm event to the target topic.
func (s *MQTTSender) Send(ctx context.Context, ev record.AlarmEvent) error {
	if err := s.ensureConnected(); err != nil {
		return err
	}

	payload := buildAlarmPayload(ev, resolveDeviceID(s.opts.Identity))

	// Format image based on configured ImageMode
	switch s.opts.ImageMode {
	case ImageModeBase64:
		s.attachBase64Image(ctx, &payload, ev)
	case ImageModeURL:
		populateSignedURLs(
			&payload,
			ev,
			s.opts.PublicBaseURL,
			s.opts.Signer,
			s.opts.SignedURLTTL,
		)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mqtt payload: %w", err)
	}

	return s.publishBytes(ctx, s.opts.Topic, data)
}

// SendHeartbeat formats and publishes a liveness snapshot to the configured status topic.
func (s *MQTTSender) SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error {
	if err := s.ensureConnected(); err != nil {
		return err
	}

	payload := buildHeartbeatPayload(snap, resolveDeviceID(s.opts.Identity), seq)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mqtt heartbeat payload: %w", err)
	}

	return s.publishBytes(ctx, s.statusTopic(), data)
}

func (s *MQTTSender) ensureConnected() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errors.New("sender closed")
	}
	s.mu.Unlock()

	if !s.client.IsConnected() {
		if err := s.Connect(); err != nil {
			return fmt.Errorf("%w: %v", ErrMQTTNotConnected, err)
		}
	}
	return nil
}

func (s *MQTTSender) publishBytes(ctx context.Context, topic string, data []byte) error {
	token := s.client.Publish(
		topic,
		byte(s.opts.QoS),
		false,
		data,
	)
	publishTimeout := 5 * time.Second
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-token.Done():
		if err := token.Error(); err != nil {
			s.logger.Warn("mqtt publish failed", zap.Error(err))
			return fmt.Errorf("%w: %v", ErrMQTTPublishFailed, err)
		}
	case <-time.After(publishTimeout):
		return fmt.Errorf("%w: timeout waiting for ack", ErrMQTTPublishFailed)
	}
	return nil
}

func (s *MQTTSender) attachBase64Image(ctx context.Context, payload *AlarmPayload, ev record.AlarmEvent) {
	canReadImage := ev.ImageReady && ev.ImagePath != "" && s.opts.Media != nil
	if !canReadImage {
		payload.ImageReady = false
		return
	}

	f, info, err := s.opts.Media.OpenContent(ctx, ev.ImagePath)
	if err != nil {
		s.logger.Warn("failed to open media file for base64", zap.String("path", ev.ImagePath), zap.Error(err))
		payload.ImageReady = false
		return
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			s.logger.Debug("close base64 media file", zap.Error(closeErr))
		}
	}()

	if info.Size() > maxMQTTBase64Bytes {
		s.logger.Warn(
			"image size exceeds 500KB limit, dropping base64 image",
			zap.Int64("bytes", info.Size()),
			zap.String("path", ev.ImagePath),
		)
		payload.ImageReady = false
		return
	}

	data, err := io.ReadAll(io.LimitReader(f, maxMQTTBase64Bytes+1))
	if err != nil || len(data) > maxMQTTBase64Bytes {
		payload.ImageReady = false
		return
	}

	b64Str := base64.StdEncoding.EncodeToString(data)
	payload.ImageBase64 = &b64Str
	payload.ImageReady = true
}
