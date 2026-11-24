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
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

var (
	ErrWebhookURLMissing      = errors.New("webhook url is missing")
	ErrPublicBaseURLMissing   = errors.New("public base url is required for url image mode")
	ErrWebhookHTTPStatusError = errors.New("webhook target returned non-success http status")
	ErrCrossOriginRedirect    = errors.New(
		"refusing cross-origin redirect when credentials are configured",
	)
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

// Send dispatches a perception event via Webhook according to the configured image mode.
// Retries reuse the delivery's id and seq; only sent_at is re-stamped per attempt.
func (s *WebhookSender) Send(ctx context.Context, d Delivery) error {
	const attempts = 3
	backoffs := []time.Duration{
		200*time.Millisecond + time.Duration(rand.IntN(50))*time.Millisecond,
		800*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond,
	}

	var lastErr error
	for attempt := range attempts {
		if attempt > 0 {
			backoffTimer := time.NewTimer(backoffs[attempt-1])
			select {
			case <-ctx.Done():
				backoffTimer.Stop()
				return ctx.Err()
			case <-backoffTimer.C:
			}
		}

		retryable, err := s.doSendOnce(ctx, d)
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
func (s *WebhookSender) SendHeartbeat(
	ctx context.Context,
	snap HeartbeatSnapshot,
	seq uint64,
) error {
	targetURL := strings.TrimSpace(s.opts.HeartbeatURL)
	if targetURL == "" {
		targetURL = s.opts.URL
	}

	payload := buildHeartbeatEnvelope(
		snap,
		resolveDeviceID(s.opts.Identity),
		seq,
		time.Now(),
	)
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
	setEnvelopeHeaders(req, payload)

	_, err = s.executeRequest(ctx, req, targetURL)
	return err
}

func (s *WebhookSender) doSendOnce(ctx context.Context, d Delivery) (bool, error) {
	req, cleanup, err := s.buildRequest(ctx, d)
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

func (s *WebhookSender) buildRequest(
	ctx context.Context,
	d Delivery,
) (*http.Request, func(), error) {
	payload := buildEventPayload(d, resolveDeviceID(s.opts.Identity), time.Now())

	if s.opts.ImageMode == ImageModeMultipart {
		return s.buildMultipartRequest(ctx, payload, d.Event)
	}
	return s.buildJSONRequest(ctx, payload, d.Event)
}

func (s *WebhookSender) imageURLParams() imageURLParams {
	return imageURLParams{
		PublicBaseURL: s.opts.PublicBaseURL,
		Signer:        s.opts.Signer,
		Media:         s.opts.Media,
		TTL:           s.opts.SignedURLTTL,
	}
}

func (s *WebhookSender) buildJSONRequest(
	ctx context.Context,
	payload EventPayload,
	ev record.AlarmEvent,
) (*http.Request, func(), error) {
	payload.Images = resolveImageURLs(ctx, s.imageURLParams(), ev)

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
	setEnvelopeHeaders(req, payload)
	return req, nil, nil
}

// multipartImage is an opened evidence image that becomes one form part.
type multipartImage struct {
	part string
	file io.ReadSeekCloser
}

// openMultipartImages opens every ready image as a part named image_<role>. Images that
// cannot be opened fall back to a URL when one can be minted and are omitted otherwise.
// The caller must close every returned file, on success and on error.
func (s *WebhookSender) openMultipartImages(
	ctx context.Context,
	ev record.AlarmEvent,
) ([]ImageInfo, []multipartImage) {
	images := []ImageInfo{}
	opened := []multipartImage{}
	params := s.imageURLParams()

	for _, c := range imageCandidates(ev) {
		info := newImageInfo(c.role)
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

		if s.opts.Media != nil {
			f, objInfo, err := s.opts.Media.OpenContent(ctx, c.relPath)
			if err == nil {
				partName := "image_" + c.role
				size := objInfo.SizeBytes
				info.Part = &partName
				info.SizeBytes = &size
				opened = append(opened, multipartImage{part: partName, file: f})
			} else {
				s.logger.Warn("failed to open image for multipart payload",
					zap.String("role", c.role),
					zap.String("path", c.relPath),
					zap.Error(err),
				)
			}
		}

		if info.Part == nil && info.URL == nil {
			continue
		}
		images = append(images, info)
	}
	return images, opened
}

func (s *WebhookSender) buildMultipartRequest(
	ctx context.Context,
	payload EventPayload,
	ev record.AlarmEvent,
) (*http.Request, func(), error) {
	images, opened := s.openMultipartImages(ctx, ev)
	cleanup := func() {
		for _, img := range opened {
			if closeErr := img.file.Close(); closeErr != nil {
				s.logger.Debug("close multipart image file", zap.Error(closeErr))
			}
		}
	}
	payload.Images = images

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, cleanup, fmt.Errorf("marshal payload: %w", err)
	}
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="payload"`)
	h.Set("Content-Type", "application/json")
	pWriter, err := w.CreatePart(h)
	if err != nil {
		return nil, cleanup, fmt.Errorf("create payload part: %w", err)
	}
	if _, err := pWriter.Write(payloadBytes); err != nil {
		return nil, cleanup, fmt.Errorf("write payload part: %w", err)
	}

	for _, img := range opened {
		imgHeader := make(textproto.MIMEHeader)
		role := strings.TrimPrefix(img.part, "image_")
		imgHeader.Set(
			"Content-Disposition",
			fmt.Sprintf(`form-data; name=%q; filename=%q`, img.part, role+".jpg"),
		)
		imgHeader.Set("Content-Type", imageMIMEJPEG)
		imgWriter, err := w.CreatePart(imgHeader)
		if err != nil {
			return nil, cleanup, fmt.Errorf("create image part: %w", err)
		}
		if _, err := io.Copy(imgWriter, img.file); err != nil {
			return nil, cleanup, fmt.Errorf("copy image bytes: %w", err)
		}
	}

	if err := w.Close(); err != nil {
		return nil, cleanup, fmt.Errorf("close multipart writer: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return nil, cleanup, fmt.Errorf("create multipart request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.ContentLength = int64(buf.Len())
	s.applyAuthAndHeaders(req)
	setEnvelopeHeaders(req, payload)
	return req, cleanup, nil
}

// setEnvelopeHeaders mirrors routing metadata into reserved headers so gateways can route
// without parsing the body. It runs after custom headers, which can never override these.
func setEnvelopeHeaders(req *http.Request, payload EventPayload) {
	req.Header.Set(headerZhulongEvent, payload.EventType)
	req.Header.Set(headerZhulongDelivery, payload.ID)
	req.Header.Set(headerZhulongSchema, strconv.Itoa(payload.SchemaVersion))
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
