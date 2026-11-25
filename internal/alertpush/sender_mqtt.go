package alertpush

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

const (
	mqttGracefulOfflineTimeout = 500 * time.Millisecond
	mqttPublishTimeout         = 5 * time.Second
)

// inlinePriority is the order images claim the base64 budget: small, information-dense
// crops first so they survive even when a 4K scene (1–3MB) cannot. Output order is
// unaffected and stays scene → object → reference.
var inlinePriority = []string{ImageRoleObject, ImageRoleReference, ImageRoleScene}

var (
	ErrMQTTBrokerMissing  = errors.New("mqtt broker url is missing")
	ErrMQTTTopicMissing   = errors.New("mqtt topic is missing")
	ErrMQTTWildcardTopic  = errors.New("mqtt publish topic must not contain wildcards + or #")
	ErrMQTTPublishFailed  = errors.New("mqtt publish failed")
	ErrMQTTNotConnected   = errors.New("mqtt client is not connected")
	errMQTTSenderClosed   = errors.New("mqtt sender is closed")
	errMQTTConnectTimeout = errors.New("mqtt connect timed out")
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
	GalleryMedia      MediaReader
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
	ConnectTimeout    time.Duration
	TLSSkipVerify     bool
	// InlineMaxBytes caps the raw bytes of all base64-inlined images per message.
	// Zero or negative means DefaultInlineMaxKB.
	InlineMaxBytes int64
	// NextSeq draws the next destination sequence number for the graceful offline notice.
	// Nil leaves its seq null (connectivity probes never emit one).
	NextSeq func() uint64
	// Identity supplies the host device_id stamped onto every payload. Nil omits it.
	Identity DeviceIDProvider
	Logger   *zap.Logger
}

// MQTTSender dispatches alert and heartbeat events over MQTT protocol.
type MQTTSender struct {
	opts             MQTTSenderOptions
	client           mqtt.Client
	connectTimeout   time.Duration
	willDeviceID     string
	willArmed        bool
	logger           *zap.Logger
	mu               sync.Mutex
	onConnect        func()
	onConnectionLost func(error)
	closed           bool
	closeCh          chan struct{}
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
	hasWildcardHeartbeat := strings.TrimSpace(opts.HeartbeatTopic) != "" &&
		strings.ContainsAny(opts.HeartbeatTopic, "+#")
	if hasWildcardHeartbeat {
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
	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 5 * time.Second
	}

	clientOpts := mqtt.NewClientOptions().
		AddBroker(opts.Broker).
		SetClientID(opts.ClientID).
		SetKeepAlive(keepAlive).
		SetConnectTimeout(connectTimeout).
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
		willPayload := buildOfflineEnvelope(
			OfflineReasonConnectionLost,
			willDeviceID,
			nil,
			nil,
		)
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

	if isSecureMQTTBroker(opts.Broker) {
		clientOpts.SetTLSConfig(&tls.Config{
			InsecureSkipVerify: opts.TLSSkipVerify,
		})
	}

	sender := &MQTTSender{
		opts:           opts,
		connectTimeout: connectTimeout,
		willDeviceID:   willDeviceID,
		willArmed:      willArmed,
		logger:         logger.Named("alertpush.mqtt").With(zap.String("destination_id", opts.ID)),
		closeCh:        make(chan struct{}),
	}

	clientOpts.SetOnConnectHandler(func(_ mqtt.Client) {
		sender.mu.Lock()
		closed := sender.closed
		hook := sender.onConnect
		sender.mu.Unlock()
		if !closed && hook != nil {
			hook()
		}
	})
	clientOpts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		sender.mu.Lock()
		closed := sender.closed
		hook := sender.onConnectionLost
		sender.mu.Unlock()
		if !closed && hook != nil {
			hook(err)
		}
	})

	sender.client = mqtt.NewClient(clientOpts)
	return sender, nil
}

// SetConnectionHooks registers callbacks invoked on broker connect/reconnect and disconnect.
// Must be called before Connect() so initial transitions are observed.
func (s *MQTTSender) SetConnectionHooks(onConnect func(), onLost func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onConnect = onConnect
	s.onConnectionLost = onLost
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

// Connect establishes the MQTT broker connection with a bounded timeout without
// holding s.mu across the network wait, so Close() is never stalled by an
// unreachable broker.
func (s *MQTTSender) Connect() error {
	return s.ConnectContext(context.Background())
}

// ConnectContext establishes the MQTT broker connection, aborting promptly when ctx
// is canceled or the sender is closed.
func (s *MQTTSender) ConnectContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errMQTTSenderClosed
	}
	client := s.client
	timeout := s.connectTimeout
	closeCh := s.closeCh
	s.mu.Unlock()

	if client.IsConnected() {
		return nil
	}

	token := client.Connect()
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-closeCh:
		return errMQTTSenderClosed
	case <-token.Done():
		if err := token.Error(); err != nil {
			s.logger.Warn("mqtt broker connect failed",
				zap.String("broker", logsafe.URL(s.opts.Broker)),
				zap.Error(err),
			)
			return err
		}
		return nil
	case <-timer.C:
		s.logger.Warn("mqtt broker connect timed out",
			zap.String("broker", logsafe.URL(s.opts.Broker)),
			zap.Duration("timeout", timeout),
		)
		return fmt.Errorf(
			"%w: %w after %s",
			ErrMQTTNotConnected,
			errMQTTConnectTimeout,
			timeout,
		)
	}
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
	if s.closeCh != nil {
		close(s.closeCh)
	}
	if s.client != nil && s.client.IsConnected() {
		if emitOffline && s.willArmed {
			s.publishGracefulOfflineLocked()
		}
		s.client.Disconnect(250)
	}
	return nil
}

func (s *MQTTSender) publishGracefulOfflineLocked() {
	var seq *uint64
	if s.opts.NextSeq != nil {
		next := s.opts.NextSeq()
		seq = &next
	}
	now := time.Now()
	offline := buildOfflineEnvelope(
		OfflineReasonGracefulShutdown,
		resolveDeviceID(s.opts.Identity),
		seq,
		&now,
	)
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

// EventSendBudget returns the total context budget for up to 3 publish attempts
// plus an initial reconnect and backoffs.
func (s *MQTTSender) EventSendBudget() time.Duration {
	const (
		attempts       = 3
		backoffCeiling = 2 * time.Second
	)
	return max(defaultSendBudget, s.connectTimeout+attempts*mqttPublishTimeout+backoffCeiling)
}

// HeartbeatSendBudget returns the single-shot context budget for one heartbeat publish.
func (s *MQTTSender) HeartbeatSendBudget() time.Duration {
	return max(defaultSendBudget, s.connectTimeout+mqttPublishTimeout+time.Second)
}

// Send formats and publishes a perception event to the target topic, retrying transient
// publish or reconnect blips while reusing the delivery's id, seq and prepared images.
func (s *MQTTSender) Send(ctx context.Context, d Delivery) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	const attempts = 3
	backoffs := []time.Duration{
		200*time.Millisecond + time.Duration(rand.IntN(50))*time.Millisecond,
		800*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond,
	}

	var images []ImageInfo
	var imagesReady bool

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			backoffTimer := time.NewTimer(backoffs[attempt-1])
			select {
			case <-ctx.Done():
				backoffTimer.Stop()
				return ctx.Err()
			case <-s.closeCh:
				backoffTimer.Stop()
				return errMQTTSenderClosed
			case <-backoffTimer.C:
			}
		}

		if err := s.ensureConnected(ctx); err != nil {
			lastErr = err
			if !isRetryableMQTTError(ctx, err) {
				break
			}
			continue
		}

		if !imagesReady {
			images = s.prepareImages(ctx, d.Event)
			imagesReady = true
		}

		payload := buildEventPayload(d, resolveDeviceID(s.opts.Identity), time.Now())
		payload.Images = images

		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal mqtt payload: %w", err)
		}

		if err := s.publishBytes(ctx, s.opts.Topic, data); err != nil {
			lastErr = err
			if !isRetryableMQTTError(ctx, err) {
				break
			}
			continue
		}
		return nil
	}
	return lastErr
}

// SendHeartbeat formats and publishes a liveness snapshot to the configured status topic.
func (s *MQTTSender) SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error {
	if err := s.ensureConnected(ctx); err != nil {
		return err
	}

	payload := buildHeartbeatEnvelope(
		snap,
		resolveDeviceID(s.opts.Identity),
		seq,
		time.Now(),
	)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal mqtt heartbeat payload: %w", err)
	}

	return s.publishBytes(ctx, s.statusTopic(), data)
}

func (s *MQTTSender) prepareImages(ctx context.Context, ev record.AlarmEvent) []ImageInfo {
	switch s.opts.ImageMode {
	case ImageModeBase64:
		return s.inlineImages(ctx, ev)
	default:
		return resolveImageURLs(ctx, s.imageURLParams(), ev)
	}
}

func isRetryableMQTTError(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	if errors.Is(err, errMQTTSenderClosed) || errors.Is(err, errMQTTConnectTimeout) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true
}

func isSecureMQTTBroker(rawBroker string) bool {
	u, err := url.Parse(strings.TrimSpace(rawBroker))
	if err != nil {
		return false
	}
	switch strings.ToLower(u.Scheme) {
	case "ssl", "tls", "tcps", "wss", "mqtts":
		return true
	default:
		return false
	}
}

func (s *MQTTSender) ensureConnected(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errMQTTSenderClosed
	}
	s.mu.Unlock()

	if !s.client.IsConnected() {
		if err := s.ConnectContext(ctx); err != nil {
			isDirectErr := errors.Is(err, ErrMQTTNotConnected) ||
				errors.Is(err, errMQTTSenderClosed) ||
				errors.Is(err, context.Canceled) ||
				errors.Is(err, context.DeadlineExceeded)
			if isDirectErr {
				return err
			}
			return fmt.Errorf("%w: %w", ErrMQTTNotConnected, err)
		}
	}
	return nil
}

func (s *MQTTSender) publishBytes(ctx context.Context, topic string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	token := s.client.Publish(
		topic,
		byte(s.opts.QoS),
		false,
		data,
	)
	timer := time.NewTimer(mqttPublishTimeout)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.closeCh:
		return errMQTTSenderClosed
	case <-token.Done():
		if err := token.Error(); err != nil {
			s.logger.Warn("mqtt publish failed", zap.Error(err))
			return fmt.Errorf("%w: %v", ErrMQTTPublishFailed, err)
		}
	case <-timer.C:
		return fmt.Errorf("%w: timeout waiting for ack", ErrMQTTPublishFailed)
	}
	return nil
}

func (s *MQTTSender) imageURLParams() imageURLParams {
	return imageURLParams{
		PublicBaseURL: s.opts.PublicBaseURL,
		Signer:        s.opts.Signer,
		Media:         s.opts.Media,
		GalleryMedia:  s.opts.GalleryMedia,
		TTL:           s.opts.SignedURLTTL,
	}
}

func (s *MQTTSender) inlineBudget() int64 {
	if s.opts.InlineMaxBytes <= 0 {
		return int64(DefaultInlineMaxKB) * 1024
	}
	return s.opts.InlineMaxBytes
}

// inlineImages implements the base64 image mode. Images claim the raw-byte budget in
// inlinePriority order; an image that does not fit falls back to a URL when one can be
// minted and is omitted (with a warning) otherwise.
func (s *MQTTSender) inlineImages(ctx context.Context, ev record.AlarmEvent) []ImageInfo {
	candidates := imageCandidates(ev)
	byRole := make(map[string]ImageInfo, len(candidates))
	remaining := s.inlineBudget()
	params := s.imageURLParams()

	for _, role := range inlinePriority {
		idx := slices.IndexFunc(candidates, func(c imageCandidate) bool { return c.role == role })
		if idx < 0 {
			continue
		}
		c := candidates[idx]
		info := newImageInfo(c.role, c.relPath)
		signed, ok := mintImageURL(
			ctx,
			params,
			c.relPath,
			false,
		)
		if ok {
			info.URL = &signed.url
			info.URLExpiresAt = &signed.expiresAt
		}

		data, size, err := s.readWithinBudget(ctx, c.relPath, remaining)
		if size >= 0 {
			info.SizeBytes = &size
		}
		if err == nil {
			encoded := base64.StdEncoding.EncodeToString(data)
			info.Base64 = &encoded
			remaining -= int64(len(data))
		}

		if info.Base64 == nil && info.URL == nil {
			s.logger.Warn("image omitted from mqtt payload: exceeds inline budget and no url available",
				zap.String("role", c.role),
				zap.Int64("size_bytes", size),
				zap.Int64("remaining_budget_bytes", remaining),
				zap.Error(err),
			)
			continue
		}
		byRole[c.role] = info
	}

	images := make([]ImageInfo, 0, len(byRole))
	for _, c := range candidates {
		if info, ok := byRole[c.role]; ok {
			images = append(images, info)
		}
	}
	return images
}

var errInlineBudgetExceeded = errors.New("image exceeds remaining inline budget")

// readWithinBudget reads an image only if it fits the remaining budget. size is -1 when
// the file could not be opened. The LimitReader guards against the file growing between
// stat and read.
func (s *MQTTSender) readWithinBudget(
	ctx context.Context,
	relPath string,
	remaining int64,
) ([]byte, int64, error) {
	reader := mediaReaderForPath(s.opts.Media, s.opts.GalleryMedia, relPath)
	if reader == nil {
		return nil, -1, errors.New("media reader is not configured")
	}
	f, info, err := reader.OpenContent(ctx, relPath)
	if err != nil {
		return nil, -1, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			s.logger.Debug("close inline media file", zap.Error(closeErr))
		}
	}()

	if info.SizeBytes > remaining {
		return nil, info.SizeBytes, errInlineBudgetExceeded
	}
	data, err := io.ReadAll(io.LimitReader(f, remaining+1))
	if err != nil {
		return nil, info.SizeBytes, err
	}
	if int64(len(data)) > remaining {
		return nil, info.SizeBytes, errInlineBudgetExceeded
	}
	return data, info.SizeBytes, nil
}
