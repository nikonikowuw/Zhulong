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
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

var (
	ErrWebhookURLMissing            = errors.New("webhook url is missing")
	ErrPublicBaseURLMissing         = errors.New("public base url is required for url image mode")
	ErrWebhookHTTPStatusError       = errors.New("webhook target returned non-success http status")
	ErrWebhookTooManyRedirects      = errors.New("stopped after 3 redirects")
	ErrWebhookRedirectMethodChanged = errors.New("refusing redirect that changes POST method")
	ErrCrossOriginRedirect          = errors.New(
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
	GalleryMedia      MediaReader
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
				return ErrWebhookTooManyRedirects
			}
			// 301/302/303 rewrite POST to GET and drop the payload body; only 307/308 preserve POST.
			if len(via) > 0 && via[0].Method == http.MethodPost && req.Method != http.MethodPost {
				return ErrWebhookRedirectMethodChanged
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

// EventSendBudget returns the total context budget needed for up to 3 attempts
// plus retry backoffs (~1.15s max) so the worker never truncates client.Timeout.
func (s *WebhookSender) EventSendBudget() time.Duration {
	const (
		attempts       = 3
		backoffCeiling = 2 * time.Second
	)
	if s.client == nil || s.client.Timeout <= 0 {
		return defaultSendBudget
	}
	return attempts*s.client.Timeout + backoffCeiling
}

// HeartbeatSendBudget returns the single-shot context budget for one heartbeat POST.
func (s *WebhookSender) HeartbeatSendBudget() time.Duration {
	if s.client == nil || s.client.Timeout <= 0 {
		return defaultSendBudget
	}
	return max(defaultSendBudget, s.client.Timeout+time.Second)
}

// preparedWebhookImages caches image metadata (and raw bytes in multipart mode) once per
// delivery so retries never re-open disk files or duplicate full multipart buffers.
type preparedWebhookImages struct {
	images []ImageInfo
	parts  []preparedMultipartImage
}

type preparedMultipartImage struct {
	part string
	mime string
	ext  string
	data []byte
}

// Send dispatches a perception event via Webhook according to the configured image mode.
// Retries reuse the delivery's id, seq and prepared images; only sent_at is re-stamped per attempt.
func (s *WebhookSender) Send(ctx context.Context, d Delivery) error {
	const attempts = 3
	backoffs := []time.Duration{
		200*time.Millisecond + time.Duration(rand.IntN(50))*time.Millisecond,
		800*time.Millisecond + time.Duration(rand.IntN(100))*time.Millisecond,
	}

	prepared := s.prepareImages(ctx, d.Event)

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

		retryable, err := s.doSendOnce(ctx, d, prepared)
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

func (s *WebhookSender) doSendOnce(
	ctx context.Context,
	d Delivery,
	prepared preparedWebhookImages,
) (bool, error) {
	req, err := s.buildRequest(d, prepared)
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
		nonRetryableRedirect := errors.Is(err, ErrCrossOriginRedirect) ||
			errors.Is(err, ErrWebhookRedirectMethodChanged) ||
			errors.Is(err, ErrWebhookTooManyRedirects)
		if nonRetryableRedirect || ctx.Err() != nil {
			return false, err
		}
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

func (s *WebhookSender) prepareImages(
	ctx context.Context,
	ev record.AlarmEvent,
) preparedWebhookImages {
	if s.opts.ImageMode == ImageModeMultipart {
		images, parts := s.loadMultipartImages(ctx, ev)
		return preparedWebhookImages{images: images, parts: parts}
	}
	return preparedWebhookImages{
		images: resolveImageURLs(ctx, s.imageURLParams(), ev),
	}
}

func (s *WebhookSender) buildRequest(
	d Delivery,
	prepared preparedWebhookImages,
) (*http.Request, error) {
	payload := buildEventPayload(d, resolveDeviceID(s.opts.Identity), time.Now())
	payload.Images = prepared.images

	if s.opts.ImageMode == ImageModeMultipart {
		return s.buildMultipartRequest(payload, prepared.parts)
	}
	return s.buildJSONRequest(payload)
}

func (s *WebhookSender) imageURLParams() imageURLParams {
	return imageURLParams{
		PublicBaseURL: s.opts.PublicBaseURL,
		Signer:        s.opts.Signer,
		Media:         s.opts.Media,
		GalleryMedia:  s.opts.GalleryMedia,
		TTL:           s.opts.SignedURLTTL,
	}
}

func (s *WebhookSender) buildJSONRequest(payload EventPayload) (*http.Request, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal json payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("create http request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	s.applyAuthAndHeaders(req)
	setEnvelopeHeaders(req, payload)
	return req, nil
}

// loadMultipartImages reads every ready image once and closes file handles before any
// network I/O begins. Images that cannot be read fall back to a URL when available.
func (s *WebhookSender) loadMultipartImages(
	ctx context.Context,
	ev record.AlarmEvent,
) ([]ImageInfo, []preparedMultipartImage) {
	images := []ImageInfo{}
	parts := []preparedMultipartImage{}
	params := s.imageURLParams()

	for _, c := range imageCandidates(ev) {
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

		data, readErr := s.readMediaBytes(ctx, c.role, c.relPath)
		if readErr == nil {
			partName := "image_" + c.role
			size := int64(len(data))
			info.Part = &partName
			info.SizeBytes = &size
			parts = append(parts, preparedMultipartImage{
				part: partName,
				mime: info.MIME,
				ext:  imageExtForMIME(info.MIME),
				data: data,
			})
		}

		if info.Part == nil && info.URL == nil {
			continue
		}
		images = append(images, info)
	}
	return images, parts
}

func (s *WebhookSender) readMediaBytes(ctx context.Context, role, relPath string) ([]byte, error) {
	reader := mediaReaderForPath(s.opts.Media, s.opts.GalleryMedia, relPath)
	if reader == nil {
		return nil, errors.New("media reader is not configured")
	}
	f, _, err := reader.OpenContent(ctx, relPath)
	if err != nil {
		s.logger.Warn("failed to open image for multipart payload",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Error(err),
		)
		return nil, err
	}
	data, readErr := io.ReadAll(f)
	closeErr := f.Close()
	if closeErr != nil {
		s.logger.Debug("close multipart image file", zap.Error(closeErr))
	}
	if readErr != nil {
		s.logger.Warn("failed to read image for multipart payload",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Error(readErr),
		)
		return nil, readErr
	}
	return data, nil
}

func (s *WebhookSender) buildMultipartRequest(
	payload EventPayload,
	parts []preparedMultipartImage,
) (*http.Request, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()
	body, contentLength := buildMultipartBody(boundary, payloadBytes, parts)

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, body)
	if err != nil {
		return nil, fmt.Errorf("create multipart request: %w", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.ContentLength = contentLength
	req.GetBody = func() (io.ReadCloser, error) {
		replayBody, _ := buildMultipartBody(boundary, payloadBytes, parts)
		return replayBody, nil
	}
	s.applyAuthAndHeaders(req)
	setEnvelopeHeaders(req, payload)
	return req, nil
}

// buildMultipartBody streams the JSON payload and cached image slices through io.MultiReader
// with an exact Content-Length, avoiding a second full-size copy into bytes.Buffer.
func buildMultipartBody(
	boundary string,
	payloadBytes []byte,
	parts []preparedMultipartImage,
) (io.ReadCloser, int64) {
	readers := make([]io.Reader, 0, 3+len(parts)*2)
	var total int64
	appendBytes := func(b []byte) {
		readers = append(readers, bytes.NewReader(b))
		total += int64(len(b))
	}

	payloadHeader := fmt.Sprintf(
		"--%s\r\nContent-Disposition: form-data; name=\"payload\"\r\nContent-Type: application/json\r\n\r\n",
		boundary,
	)
	appendBytes([]byte(payloadHeader))
	appendBytes(payloadBytes)

	for _, img := range parts {
		role := strings.TrimPrefix(img.part, "image_")
		imgHeader := fmt.Sprintf(
			"\r\n--%s\r\nContent-Disposition: form-data; name=%q; filename=%q\r\nContent-Type: %s\r\n\r\n",
			boundary,
			img.part,
			role+img.ext,
			img.mime,
		)
		appendBytes([]byte(imgHeader))
		appendBytes(img.data)
	}

	appendBytes([]byte("\r\n--" + boundary + "--\r\n"))
	return io.NopCloser(io.MultiReader(readers...)), total
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
