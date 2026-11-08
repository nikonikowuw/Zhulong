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
)

// WebhookSenderOptions contains configuration for constructing a WebhookSender.
type WebhookSenderOptions struct {
	ID            string
	Name          string
	ImageMode     string
	PublicBaseURL string
	SignedURLTTL  time.Duration
	Signer        *URLSigner
	Media         MediaReader
	URL           string
	AuthType      string
	Token         string
	Username      string
	Password      string
	Headers       map[string]string
	Timeout       time.Duration
	// Identity supplies the host device_id stamped onto every payload. Nil omits it.
	Identity DeviceIDProvider
	Logger   *zap.Logger
}

// WebhookSender delivers alert events to HTTP/HTTPS webhook endpoints.
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
	if opts.ImageMode == ImageModeURL && strings.TrimSpace(opts.PublicBaseURL) == "" {
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

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return errors.New("stopped after 3 redirects")
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
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoffs[attempt-1]):
			}
		}

		err, retryable := s.doSendOnce(ctx, ev)
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

func (s *WebhookSender) doSendOnce(ctx context.Context, ev record.AlarmEvent) (error, bool) {
	req, cleanup, err := s.buildRequest(ctx, ev)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err, false
	}

	resp, err := s.client.Do(req.WithContext(ctx))
	if err != nil {
		s.logger.Warn("webhook send network error",
			zap.String("target_url", logsafe.URL(s.opts.URL)),
			zap.Error(err),
		)
		return err, true
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil, false
	}

	// 4xx is non-retryable client error (except 429 Too Many Requests)
	if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
		return fmt.Errorf("%w: status %d", ErrWebhookHTTPStatusError, resp.StatusCode), false
	}

	return fmt.Errorf("%w: status %d", ErrWebhookHTTPStatusError, resp.StatusCode), true
}

func (s *WebhookSender) buildRequest(ctx context.Context, ev record.AlarmEvent) (*http.Request, func(), error) {
	payload := BuildAlarmPayload(ev, resolveDeviceID(s.opts.Identity))

	if s.opts.ImageMode == ImageModeMultipart {
		return s.buildMultipartRequest(ctx, payload, ev)
	}
	return s.buildJSONRequest(payload, ev)
}

func (s *WebhookSender) buildJSONRequest(payload AlarmPayload, ev record.AlarmEvent) (*http.Request, func(), error) {
	if s.opts.Signer != nil && s.opts.PublicBaseURL != "" {
		if ev.ImageReady && ev.ImagePath != "" {
			u, err := GenerateSignedURL(
				s.opts.PublicBaseURL,
				s.opts.Signer,
				ev.ImagePath,
				s.opts.SignedURLTTL,
			)
			if err == nil {
				payload.ImageURL = &u
				payload.ImageReady = true
			}
		}
		if ev.CropReady && ev.CropPath != "" {
			u, err := GenerateSignedURL(
				s.opts.PublicBaseURL,
				s.opts.Signer,
				ev.CropPath,
				s.opts.SignedURLTTL,
			)
			if err == nil {
				payload.CropURL = &u
			}
		}
		if payload.Face != nil && ev.Face != nil && ev.Face.GalleryImagePath != "" {
			u, err := GenerateSignedURL(
				s.opts.PublicBaseURL,
				s.opts.Signer,
				ev.Face.GalleryImagePath,
				s.opts.SignedURLTTL,
			)
			if err == nil {
				payload.Face.GalleryURL = &u
			}
		}
	}

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
		if file != nil {
			_ = file.Close()
		}
	}

	var hasImage bool
	if ev.ImageReady && ev.ImagePath != "" && s.opts.Media != nil {
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
		if !IsReservedHeader(k) && strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
}
