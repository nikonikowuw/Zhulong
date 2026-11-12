package alertpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

var (
	ErrWebhookURLMissing      = errors.New("webhook url is missing")
	ErrPublicBaseURLMissing   = errors.New("public base url is required for url image mode")
	ErrWebhookHTTPStatusError = errors.New("webhook target returned non-success http status")
	ErrCrossOriginRedirect    = errors.New("refusing cross-origin redirect when credentials are configured")
)

// WebhookSenderOptions contains configuration for constructing a WebhookSender.
type WebhookSenderOptions struct {
	ID                string
	Name              string
	ImageMode         string
	SkipImageURLCheck bool
	PublicBaseURL     string
	SignedURLTTL      time.Duration
	Signer            *URLSigner
	Media             MediaReader
	URL               string
	HeartbeatURL      string
	AuthType          string
	Token             string
	Username          string
	Password          string
	Headers           map[string]string
	Timeout           time.Duration
	// Identity supplies the host device_id stamped onto every payload. Nil omits it.
	Identity DeviceIDProvider
	Logger   *zap.Logger
}

// WebhookSender delivers alert and heartbeat events to HTTP/HTTPS webhook endpoints.
type WebhookSender struct {
	opts   WebhookSenderOptions
	client *http.Client
	logger *zap.Logger
}

// NewWebhookSender constructs a WebhookSender instance.
func NewWebhookSender(opts WebhookSenderOptions) (*WebhookSender, error) {
	if strings.TrimSpace(opts.URL) == "" {
		return nil, ErrWebhookURLMissing
	}
	needsPublicBaseURL := !opts.SkipImageURLCheck && opts.ImageMode == ImageModeURL
	if needsPublicBaseURL && strings.TrimSpace(opts.PublicBaseURL) == "" {
		return nil, ErrPublicBaseURLMissing
	}
	timeout := opts.Timeout
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}

	transport := &http.Transport{
		MaxIdleConns:        4,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}

	hasCredentials := (opts.AuthType != "" && opts.AuthType != AuthTypeNone) || len(opts.Headers) > 0
	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("stopped after 3 redirects")
			}
			mustGuardOrigin := hasCredentials && len(via) > 0
			if mustGuardOrigin && !sameOrigin(via[0].URL.String(), req.URL.String()) {
				return ErrCrossOriginRedirect
			}
			return nil
		},
	}

	return &WebhookSender{
		opts:   opts,
		client: client,
		logger: logger.Named("alertpush.webhook").With(zap.String("destination_id", opts.ID)),
	}, nil
}

// Close releases idle connections in the transport pool.
func (s *WebhookSender) Close() error {
	if s.client != nil {
		s.client.CloseIdleConnections()
	}
	return nil
}

// Send dispatches an alarm event via Webhook according to the configured image mode.
func (s *WebhookSender) Send(ctx context.Context, ev record.AlarmEvent) error {
	const attempts = 3
	backoffs := []time.Duration{
		200*time.Millisecond + time.Duration(rand.IntN(50))*time.Millisecond,
		800*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond,
	}

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[attempt-1]):
			}
		}

		retryable, err := s.doSendOnce(ctx, ev)
		if err == nil {
			return nil
		}
		lastErr = err
		if !retryable {
			break
		}
	}
	return lastErr
}

// SendHeartbeat posts a single point-in-time liveness snapshot without retrying stale state.
func (s *WebhookSender) SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error {
	targetURL := strings.TrimSpace(s.opts.HeartbeatURL)
	if targetURL == "" {
		targetURL = s.opts.URL
	}

	payload := buildHeartbeatPayload(snap, resolveDeviceID(s.opts.Identity), seq)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal heartbeat payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("create heartbeat http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyAuthAndHeaders(req)

	_, err = s.executeRequest(ctx, req, targetURL)
	return err
}

func (s *WebhookSender) doSendOnce(ctx context.Context, ev record.AlarmEvent) (bool, error) {
	req, cleanup, err := s.buildRequest(ctx, ev)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return false, err
	}
	return s.executeRequest(ctx, req, s.opts.URL)
}

func (s *WebhookSender) executeRequest(
	ctx context.Context,
	req *http.Request,
	targetURL string,
) (bool, error) {
	resp, err := s.client.Do(req.WithContext(ctx))
	if err != nil {
		s.logger.Warn("webhook send network error",
			zap.String("target_url", logsafe.URL(targetURL)),
			zap.Error(err),
		)
		return true, err
	}
	defer func() {
		if _, copyErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 4096)); copyErr != nil {
			s.logger.Debug("drain webhook response body", zap.Error(copyErr))
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			s.logger.Debug("close webhook response body", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}

	// 4xx is non-retryable client error (except 429 Too Many Requests)
	isClientError := resp.StatusCode >= 400 && resp.StatusCode < 500
	if isClientError && resp.StatusCode != http.StatusTooManyRequests {
		return false, fmt.Errorf("%w: status %d", ErrWebhookHTTPStatusError, resp.StatusCode)
	}

	return true, fmt.Errorf("%w: status %d", ErrWebhookHTTPStatusError, resp.StatusCode)
}

func (s *WebhookSender) buildRequest(ctx context.Context, ev record.AlarmEvent) (*http.Request, func(), error) {
	payload := buildAlarmPayload(ev, resolveDeviceID(s.opts.Identity))

	if s.opts.ImageMode == ImageModeMultipart {
		return s.buildMultipartRequest(ctx, payload, ev)
	}
	return s.buildJSONRequest(payload, ev)
}

func (s *WebhookSender) buildJSONRequest(payload AlarmPayload, ev record.AlarmEvent) (*http.Request, func(), error) {
	populateSignedURLs(
		&payload,
		ev,
		s.opts.PublicBaseURL,
		s.opts.Signer,
		s.opts.SignedURLTTL,
	)

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal json payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyAuthAndHeaders(req)
	return req, nil, nil
}

func (s *WebhookSender) buildMultipartRequest(
	ctx context.Context,
	payload AlarmPayload,
	ev record.AlarmEvent,
) (*http.Request, func(), error) {
	var file *os.File
	cleanup := func() {
		if file == nil {
			return
		}
		if closeErr := file.Close(); closeErr != nil {
			s.logger.Debug("close multipart snapshot file", zap.Error(closeErr))
		}
	}

	var hasImage bool
	hasSnapshotSource := ev.ImageReady && ev.ImagePath != "" && s.opts.Media != nil
	if hasSnapshotSource {
		f, _, err := s.opts.Media.OpenContent(ctx, ev.ImagePath)
		if err == nil {
			file = f
			hasImage = true
			payload.ImageReady = true
		} else {
			s.logger.Warn("failed to open snapshot file for multipart payload",
				zap.String("path", ev.ImagePath),
				zap.Error(err),
			)
			payload.ImageReady = false
		}
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	// 1. Part: payload
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("marshal payload: %w", err)
	}

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="payload"`)
	h.Set("Content-Type", "application/json")
	pWriter, err := w.CreatePart(h)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("create payload part: %w", err)
	}
	if _, err := pWriter.Write(payloadBytes); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("write payload part: %w", err)
	}

	// 2. Part: image (if available)
	if hasImage && file != nil {
		imgHeader := make(textproto.MIMEHeader)
		imgHeader.Set("Content-Disposition", `form-data; name="image"; filename="snapshot.jpg"`)
		imgHeader.Set("Content-Type", "image/jpeg")
		imgWriter, err := w.CreatePart(imgHeader)
		if err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("create image part: %w", err)
		}
		if _, err := io.Copy(imgWriter, file); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("copy image bytes: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("create multipart request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.ContentLength = int64(buf.Len())
	s.applyAuthAndHeaders(req)
	return req, cleanup, nil
}

func (s *WebhookSender) applyAuthAndHeaders(req *http.Request) {
	switch s.opts.AuthType {
	case AuthTypeBearer:
		if s.opts.Token != "" {
			req.Header.Set("Authorization", "Bearer "+s.opts.Token)
		}
	case AuthTypeBasic:
		if s.opts.Username != "" || s.opts.Password != "" {
			req.SetBasicAuth(s.opts.Username, s.opts.Password)
		}
	}

	for k, v := range s.opts.Headers {
		if !isReservedHeader(k) && strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
}
