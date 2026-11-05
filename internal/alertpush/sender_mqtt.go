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
	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

const MaxMQTTBase64Bytes = 500 * 1024 // 500KB

var (
	ErrMQTTBrokerMissing = errors.New("mqtt broker url is missing")
	ErrMQTTTopicMissing  = errors.New("mqtt topic is missing")
	ErrMQTTWildcardTopic = errors.New("mqtt publish topic must not contain wildcards + or #")
	ErrMQTTPublishFailed = errors.New("mqtt publish failed")
	ErrMQTTNotConnected  = errors.New("mqtt client is not connected")
)

// MQTTSenderOptions contains configuration for MQTTSender.
type MQTTSenderOptions struct {
	ID            string
	Name          string
	ImageMode     string
	PublicBaseURL string
	SignedURLTTL  time.Duration
	Signer        *URLSigner
	Media         MediaReader
	Broker        string
	ClientID      string
	Username      string
	Password      string
	Topic         string
	QoS           uint8
	KeepAlive     time.Duration
	TLSSkipVerify bool
	// Identity supplies the host device_id stamped onto every payload. Nil omits it.
	Identity DeviceIDProvider
	Logger   *zap.Logger
}

// MQTTSender dispatches alert events over MQTT protocol.
type MQTTSender struct {
	opts   MQTTSenderOptions
	client mqtt.Client
	logger *zap.Logger
	mu     sync.Mutex
	closed bool
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
	if opts.ImageMode == ImageModeURL && strings.TrimSpace(opts.PublicBaseURL) == "" {
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

	if strings.HasPrefix(opts.Broker, "ssl://") || strings.HasPrefix(opts.Broker, "tls://") || strings.HasPrefix(opts.Broker, "tcps://") {
		clientOpts.SetTLSConfig(&tls.Config{
			InsecureSkipVerify: opts.TLSSkipVerify,
		})
	}

	client := mqtt.NewClient(clientOpts)
	sender := &MQTTSender{
		opts:   opts,
		client: client,
		logger: logger.Named("alertpush.mqtt").With(zap.String("destination_id", opts.ID)),
	}

	return sender, nil
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

// Close disconnects client and releases resources.
func (s *MQTTSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.client != nil && s.client.IsConnected() {
		s.client.Disconnect(250)
	}
	return nil
}

// Send formats and publishes an alarm event to the target topic.
func (s *MQTTSender) Send(ctx context.Context, ev record.AlarmEvent) error {
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

	payload := BuildAlarmPayload(ev, resolveDeviceID(s.opts.Identity))

	// Format image based on configured ImageMode
	if s.opts.ImageMode == ImageModeBase64 {
		s.attachBase64Image(ctx, &payload, ev)
	} else if s.opts.ImageMode == ImageModeURL && s.opts.Signer != nil && s.opts.PublicBaseURL != "" {
		if ev.ImageReady && ev.ImagePath != "" {
			if u, err := GenerateSignedURL(s.opts.PublicBaseURL, s.opts.Signer, ev.ImagePath, s.opts.SignedURLTTL); err == nil {
				payload.ImageURL = &u
				payload.ImageReady = true
			}
		}
		if ev.CropReady && ev.CropPath != "" {
			if u, err := GenerateSignedURL(s.opts.PublicBaseURL, s.opts.Signer, ev.CropPath, s.opts.SignedURLTTL); err == nil {
				payload.CropURL = &u
			}
		}
		if payload.Face != nil && ev.Face != nil && ev.Face.GalleryImagePath != "" {
			if u, err := GenerateSignedURL(s.opts.PublicBaseURL, s.opts.Signer, ev.Face.GalleryImagePath, s.opts.SignedURLTTL); err == nil {
				payload.Face.GalleryURL = &u
			}
		}
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mqtt payload: %w", err)
	}

	token := s.client.Publish(s.opts.Topic, byte(s.opts.QoS), false, data)
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
	if !ev.ImageReady || ev.ImagePath == "" || s.opts.Media == nil {
		payload.ImageReady = false
		return
	}

	f, info, err := s.opts.Media.OpenContent(ctx, ev.ImagePath)
	if err != nil {
		s.logger.Warn("failed to open media file for base64", zap.String("path", ev.ImagePath), zap.Error(err))
		payload.ImageReady = false
		return
	}
	defer f.Close()

	if info.Size() > MaxMQTTBase64Bytes {
		s.logger.Warn("image size exceeds 500KB limit, dropping base64 image",
			zap.Int64("bytes", info.Size()),
			zap.String("path", ev.ImagePath))
		payload.ImageReady = false
		return
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxMQTTBase64Bytes+1))
	if err != nil || len(data) > MaxMQTTBase64Bytes {
		payload.ImageReady = false
		return
	}

	b64Str := base64.StdEncoding.EncodeToString(data)
	payload.ImageBase64 = &b64Str
	payload.ImageReady = true
}
