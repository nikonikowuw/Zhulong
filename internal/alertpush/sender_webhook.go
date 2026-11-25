package alertpush

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/logsafe"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

const (
	defaultMaxWebhookMultipartImageBytes = 16 * 1024 * 1024 // 16 MiB per image part
	defaultMaxWebhookMultipartTotalBytes = 24 * 1024 * 1024 // 24 MiB total across all parts per event
)

var (
	ErrWebhookURLMissing            = errors.New("webhook url is missing")
	ErrPublicBaseURLMissing         = errors.New("public base url is required for url image mode")
	ErrWebhookHTTPStatusError       = errors.New("webhook target returned non-success http status")
	ErrWebhookTooManyRedirects      = errors.New("stopped after 3 redirects")
	ErrWebhookRedirectMethodChanged = errors.New("refusing redirect that changes POST method")
	ErrWebhookHTTPSDowngrade        = errors.New("refusing redirect that downgrades https to http")
	ErrCrossOriginRedirect          = errors.New(
		"refusing cross-origin redirect when credentials are configured",
	)
	errWebhookImageTooLarge = errors.New("image exceeds maximum webhook multipart size")
	errWebhookImageEmpty    = errors.New("image is empty")
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
	// SigningSecret, when non-empty, signs every outbound request body using
	// HMAC-SHA256(SigningSecret, timestamp + "." + rawBody) in X-Zhulong-Signature.
	SigningSecret string
	Headers       map[string]string
	Timeout       time.Duration
	TLSSkipVerify bool
	// AllowLoopback permits connecting to loopback hosts (127.0.0.1 / ::1 / localhost).
	// Production defaults to false; unit tests talking to httptest servers opt in explicitly.
	AllowLoopback bool
	// MaxMultipartImageBytes caps raw bytes read per image part in multipart mode.
	// Zero or negative uses defaultMaxWebhookMultipartImageBytes (16 MiB).
	MaxMultipartImageBytes int64
	// MaxMultipartTotalBytes caps total raw bytes read across all image parts in one event.
	// Zero or negative uses defaultMaxWebhookMultipartTotalBytes (24 MiB).
	MaxMultipartTotalBytes int64
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
	trimmedURL := strings.TrimSpace(opts.URL)
	if trimmedURL == "" {
		return nil, ErrWebhookURLMissing
	}
	parsedURL, err := url.Parse(trimmedURL)
	if err != nil || parsedURL.Host == "" ||
		(parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return nil, fmt.Errorf("%w: invalid webhook url", ErrWebhookURLMissing)
	}
	if err := validateWebhookTargetHost(parsedURL.Host, opts.AllowLoopback); err != nil {
		return nil, err
	}
	if hbURL := strings.TrimSpace(opts.HeartbeatURL); hbURL != "" {
		parsedHB, hbErr := url.Parse(hbURL)
		if hbErr != nil || parsedHB.Host == "" ||
			(parsedHB.Scheme != "http" && parsedHB.Scheme != "https") {
			return nil, fmt.Errorf("%w: invalid webhook heartbeat url", ErrWebhookURLMissing)
		}
		if err := validateWebhookTargetHost(parsedHB.Host, opts.AllowLoopback); err != nil {
			return nil, err
		}
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

	transport := newWebhookTransport(timeout, opts.TLSSkipVerify, opts.AllowLoopback)

	hasCredentials := (opts.AuthType != "" && opts.AuthType != AuthTypeNone) ||
		len(opts.Headers) > 0 ||
		opts.SigningSecret != ""
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
			// Always refuse HTTPS -> HTTP scheme downgrades and non-HTTP schemes regardless of auth.
			reqScheme := strings.ToLower(req.URL.Scheme)
			if reqScheme != "http" && reqScheme != "https" {
				return ErrSSRFBlocked
			}
			if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") &&
				reqScheme != "https" {
				return ErrWebhookHTTPSDowngrade
			}
			if err := validateWebhookTargetHost(req.URL.Host, opts.AllowLoopback); err != nil {
				return err
			}
			mustGuardOrigin := hasCredentials && len(via) > 0
			if mustGuardOrigin && !sameOrigin(via[0].URL.String(), req.URL.String()) {
				return ErrCrossOriginRedirect
			}
			return nil
		},
	}

	opts.URL = trimmedURL
	return &WebhookSender{
		opts:   opts,
		client: client,
		logger: logger.Named("alertpush.webhook").With(zap.String("destination_id", opts.ID)),
	}, nil
}

func newWebhookTransport(timeout time.Duration, tlsSkipVerify, allowLoopback bool) *http.Transport {
	var transport *http.Transport
	if dt, ok := http.DefaultTransport.(*http.Transport); ok && dt != nil {
		transport = dt.Clone()
	} else {
		transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			ForceAttemptHTTP2:     true,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
	}
	transport.MaxIdleConns = 4
	transport.MaxIdleConnsPerHost = 4
	transport.IdleConnTimeout = 30 * time.Second
	dialTimeout := min(timeout, 10*time.Second)
	transport.DialContext = newSSRFSafeDialContext(&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}, allowLoopback)
	if tlsSkipVerify {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		} else {
			transport.TLSClientConfig = transport.TLSClientConfig.Clone()
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
	}
	return transport
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
	images *EventImages
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

	now := time.Now()
	payload := buildHeartbeatPayload(
		snap,
		resolveDeviceID(s.opts.Identity),
		seq,
		now,
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
	s.setEnvelopeAndSignatureHeaders(req, payload.EventType, payload.ID, payload.SchemaVersion, now, bodyBytes)

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
		nonRetryablePolicy := errors.Is(err, ErrCrossOriginRedirect) ||
			errors.Is(err, ErrWebhookRedirectMethodChanged) ||
			errors.Is(err, ErrWebhookTooManyRedirects) ||
			errors.Is(err, ErrWebhookHTTPSDowngrade) ||
			errors.Is(err, ErrSSRFBlocked)
		if nonRetryablePolicy || ctx.Err() != nil {
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
	now := time.Now()
	payload := buildEventPayload(d, resolveDeviceID(s.opts.Identity), now)
	payload.Images = prepared.images

	if s.opts.ImageMode == ImageModeMultipart {
		return s.buildMultipartRequest(payload, prepared.parts, now)
	}
	return s.buildJSONRequest(payload, now)
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

func (s *WebhookSender) buildJSONRequest(payload EventPayload, now time.Time) (*http.Request, error) {
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
	s.setEnvelopeAndSignatureHeaders(req, payload.EventType, payload.ID, payload.SchemaVersion, now, bodyBytes)
	return req, nil
}

// loadMultipartImages reads every ready image once and closes file handles before any
// network I/O begins. Per-image (16 MiB) and per-event total (24 MiB) caps bound memory;
// images that exceed a cap or fail to read fall back to a URL when available.
func (s *WebhookSender) loadMultipartImages(
	ctx context.Context,
	ev record.AlarmEvent,
) (*EventImages, []preparedMultipartImage) {
	images := &EventImages{}
	parts := []preparedMultipartImage{}
	params := s.imageURLParams()
	perImageLimit := s.maxMultipartImageBytes()
	remainingTotal := s.maxMultipartTotalBytes()

	for _, c := range imageCandidates(ev) {
		info := newImageInfo(c.relPath)
		effectiveLimit := min(perImageLimit, remainingTotal)
		var (
			data    []byte
			size    int64
			readErr error
		)
		if effectiveLimit <= 0 {
			size = s.probeSizeBestEffort(ctx, c.relPath)
			readErr = errWebhookImageTooLarge
			s.logger.Warn("webhook multipart total size cap reached; falling back to URL if configured",
				zap.String("role", c.role),
				zap.String("path", c.relPath),
				zap.Int64("total_limit_bytes", s.maxMultipartTotalBytes()),
			)
		} else {
			data, size, readErr = s.readMediaBytes(ctx, c.role, c.relPath, effectiveLimit)
		}

		if readErr == nil {
			partName := "image_" + c.role
			actualSize := int64(len(data))
			info.Part = partName
			info.SizeBytes = &actualSize
			remainingTotal -= actualSize
			parts = append(parts, preparedMultipartImage{
				part: partName,
				mime: info.MIME,
				ext:  imageExtForMIME(info.MIME),
				data: data,
			})
			images.set(c.role, info)
			continue
		}

		// Missing or empty local file cannot produce a valid local signed URL; only fall back
		// to URL when the image exists (oversized/transient read error) or has a presigner.
		if isMediaMissingErr(readErr) || errors.Is(readErr, errWebhookImageEmpty) {
			continue
		}

		signed, ok := mintImageURL(
			ctx,
			params,
			c.relPath,
			false,
		)
		if !ok {
			continue
		}
		info.URL = signed.url
		info.ExpiresAt = signed.expiresAt
		if size > 0 {
			knownSize := size
			info.SizeBytes = &knownSize
		}
		images.set(c.role, info)
	}
	if images.Empty() {
		return nil, parts
	}
	return images, parts
}

func (s *WebhookSender) probeSizeBestEffort(ctx context.Context, relPath string) int64 {
	reader := mediaReaderForPath(s.opts.Media, s.opts.GalleryMedia, relPath)
	if size, exists := statImageSize(ctx, reader, relPath); exists && size != nil {
		return *size
	}
	return -1
}

func (s *WebhookSender) maxMultipartImageBytes() int64 {
	if s.opts.MaxMultipartImageBytes <= 0 {
		return defaultMaxWebhookMultipartImageBytes
	}
	return s.opts.MaxMultipartImageBytes
}

func (s *WebhookSender) maxMultipartTotalBytes() int64 {
	if s.opts.MaxMultipartTotalBytes <= 0 {
		return defaultMaxWebhookMultipartTotalBytes
	}
	return s.opts.MaxMultipartTotalBytes
}

func (s *WebhookSender) readMediaBytes(
	ctx context.Context,
	role, relPath string,
	limit int64,
) ([]byte, int64, error) {
	reader := mediaReaderForPath(s.opts.Media, s.opts.GalleryMedia, relPath)
	if reader == nil {
		return nil, -1, errors.New("media reader is not configured")
	}
	f, info, err := reader.OpenContent(ctx, relPath)
	if err != nil {
		s.logger.Warn("failed to open image for multipart payload",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Error(err),
		)
		return nil, -1, err
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			s.logger.Debug("close multipart image file", zap.Error(closeErr))
		}
	}()

	if info.SizeBytes > limit {
		s.logger.Warn("image exceeds webhook multipart size cap; falling back to URL if configured",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Int64("size_bytes", info.SizeBytes),
			zap.Int64("limit_bytes", limit),
		)
		return nil, info.SizeBytes, errWebhookImageTooLarge
	}

	data, readErr := io.ReadAll(io.LimitReader(f, limit+1))
	if readErr != nil {
		s.logger.Warn("failed to read image for multipart payload",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Error(readErr),
		)
		return nil, info.SizeBytes, readErr
	}
	if int64(len(data)) > limit {
		s.logger.Warn("image grew beyond webhook multipart size cap; falling back to URL if configured",
			zap.String("role", role),
			zap.String("path", relPath),
			zap.Int64("limit_bytes", limit),
		)
		return nil, info.SizeBytes, errWebhookImageTooLarge
	}
	if len(data) == 0 {
		s.logger.Warn("image is empty for multipart payload; omitting",
			zap.String("role", role),
			zap.String("path", relPath),
		)
		return nil, 0, errWebhookImageEmpty
	}
	return data, int64(len(data)), nil
}

func (s *WebhookSender) buildMultipartRequest(
	payload EventPayload,
	parts []preparedMultipartImage,
	now time.Time,
) (*http.Request, error) {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	boundary := multipart.NewWriter(io.Discard).Boundary()
	chunks, contentLength := multipartBodyChunks(boundary, payloadBytes, parts)
	body := readerFromChunks(chunks)

	req, err := http.NewRequest(http.MethodPost, s.opts.URL, body)
	if err != nil {
		return nil, fmt.Errorf("create multipart request: %w", err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	req.ContentLength = contentLength
	req.GetBody = func() (io.ReadCloser, error) {
		return readerFromChunks(chunks), nil
	}
	s.applyAuthAndHeaders(req)
	s.setEnvelopeAndSignatureHeaders(req, payload.EventType, payload.ID, payload.SchemaVersion, now, chunks...)
	return req, nil
}

// multipartBodyChunks assembles the exact byte slices of a multipart body without copying
// cached image buffers so both streaming upload and incremental HMAC signing share them.
func multipartBodyChunks(
	boundary string,
	payloadBytes []byte,
	parts []preparedMultipartImage,
) ([][]byte, int64) {
	chunks := make([][]byte, 0, 3+len(parts)*2)
	var total int64
	appendBytes := func(b []byte) {
		chunks = append(chunks, b)
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
	return chunks, total
}

func readerFromChunks(chunks [][]byte) io.ReadCloser {
	readers := make([]io.Reader, len(chunks))
	for i, c := range chunks {
		readers[i] = bytes.NewReader(c)
	}
	return io.NopCloser(io.MultiReader(readers...))
}

// setEnvelopeAndSignatureHeaders mirrors routing metadata and optional HMAC-SHA256 body
// authenticity signature into reserved headers after custom headers so they can never be spoofed.
func (s *WebhookSender) setEnvelopeAndSignatureHeaders(
	req *http.Request,
	eventType, deliveryID string,
	schemaVersion int,
	now time.Time,
	bodyChunks ...[]byte,
) {
	setEnvelopeHeaders(req, eventType, deliveryID, schemaVersion)
	ts := strconv.FormatInt(now.UTC().Unix(), 10)
	req.Header.Set(headerZhulongTimestamp, ts)
	if s.opts.SigningSecret != "" {
		req.Header.Set(headerZhulongSignature, computeWebhookSignature(s.opts.SigningSecret, ts, bodyChunks...))
	}
}

func computeWebhookSignature(secret, timestamp string, bodyChunks ...[]byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	for _, chunk := range bodyChunks {
		mac.Write(chunk)
	}
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// setEnvelopeHeaders mirrors routing metadata into reserved headers so gateways can route
// without parsing the body. It runs after custom headers, which can never override these.
func setEnvelopeHeaders(
	req *http.Request,
	eventType, deliveryID string,
	schemaVersion int,
) {
	req.Header.Set(headerZhulongEvent, eventType)
	req.Header.Set(headerZhulongDelivery, deliveryID)
	req.Header.Set(headerZhulongSchema, strconv.Itoa(schemaVersion))
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
