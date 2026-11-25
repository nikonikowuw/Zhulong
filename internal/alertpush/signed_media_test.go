package alertpush

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
)

type recordingPresignGalleryReader struct {
	*fakeMediaService
	lastTTL     time.Duration
	redirectURL string
}

func (r *recordingPresignGalleryReader) PresignMediaURL(
	_ context.Context,
	_ string,
	ttl time.Duration,
) (string, bool, error) {
	r.lastTTL = ttl
	if r.redirectURL != "" {
		return r.redirectURL, true, nil
	}
	return "", false, nil
}

type fakeMediaRootProvider struct {
	root string
}

func (p fakeMediaRootProvider) MediaRoot(context.Context) (string, error) { return p.root, nil }

func TestSignedMediaHandler_ErrorsExpiryNoOracleAndSignerUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 1. Unconfigured signer returns 503 MEDIA_SIGNER_UNAVAILABLE (never 403 MEDIA_URL_INVALID).
	unconfiguredRouter := gin.New()
	NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer: NewEmptyURLSigner(),
		Logger: zap.NewNop(),
	}).RegisterRoutes(unconfiguredRouter.Group("/api/v1"))

	w503 := httptest.NewRecorder()
	unconfiguredRouter.ServeHTTP(
		w503,
		httptest.NewRequest(http.MethodGet, "/api/v1/media/signed/snapshots/cam_1/a.jpg?exp=9999999999&sig=abc", nil),
	)
	if w503.Code != http.StatusServiceUnavailable {
		t.Fatalf("unconfigured signer status = %d, want 503 (%s)", w503.Code, w503.Body.String())
	}
	var env503 httputil.Response
	if err := json.Unmarshal(w503.Body.Bytes(), &env503); err != nil {
		t.Fatalf("unmarshal 503: %v", err)
	}
	if env503.Code != CodeMediaSignerUnavailable {
		t.Fatalf("503 code = %q, want %q", env503.Code, CodeMediaSignerUnavailable)
	}

	// 2. Configured signer: test valid expired, forged expired (no oracle), tampered sig, disallowed path.
	signer, err := NewURLSigner(bytes.Repeat([]byte{0x33}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	router := gin.New()
	NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:     signer,
		RatePerMin: 600,
		RateBurst:  50,
		Logger:     zap.NewNop(),
	}).RegisterRoutes(router.Group("/api/v1"))

	pastExp := time.Now().Add(-time.Minute).Unix()
	validExpiredSig, err := signer.Sign("snapshots/cam_1/a.jpg", pastExp)
	if err != nil {
		t.Fatalf("Sign expired: %v", err)
	}
	futureExp := time.Now().Add(time.Hour).Unix()
	validFutureSig, err := signer.Sign("snapshots/cam_1/a.jpg", futureExp)
	if err != nil {
		t.Fatalf("Sign future: %v", err)
	}

	cases := []struct {
		name     string
		uri      string
		wantCode string
	}{
		{
			name:     "valid signature but expired returns MEDIA_URL_EXPIRED",
			uri:      "/api/v1/media/signed/snapshots/cam_1/a.jpg?exp=" + strconv.FormatInt(pastExp, 10) + "&sig=" + validExpiredSig,
			wantCode: CodeMediaURLExpired,
		},
		{
			name:     "forged signature with past exp returns MEDIA_URL_INVALID (no expiry oracle)",
			uri:      "/api/v1/media/signed/snapshots/cam_1/a.jpg?exp=1&sig=forged-garbage",
			wantCode: CodeMediaURLInvalid,
		},
		{
			name:     "tampered signature returns MEDIA_URL_INVALID",
			uri:      "/api/v1/media/signed/snapshots/cam_1/a.jpg?exp=" + strconv.FormatInt(futureExp, 10) + "&sig=" + validFutureSig + "x",
			wantCode: CodeMediaURLInvalid,
		},
		{
			name:     "disallowed namespace recordings rejected",
			uri:      "/api/v1/media/signed/recordings/cam_1/a.mp4?exp=" + strconv.FormatInt(futureExp, 10) + "&sig=" + validFutureSig,
			wantCode: CodeMediaURLInvalid,
		},
		{
			name:     "nested faces path rejected",
			uri:      "/api/v1/media/signed/faces/nested/a.jpg?exp=" + strconv.FormatInt(futureExp, 10) + "&sig=" + validFutureSig,
			wantCode: CodeMediaURLInvalid,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.uri, nil))
			if w.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403 (%s)", w.Code, w.Body.String())
			}
			var env httputil.Response
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if env.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", env.Code, tc.wantCode)
			}
		})
	}
}

func TestSignedMediaHandler_HighRateLimitAndInvalidSignatureIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer, err := NewURLSigner(bytes.Repeat([]byte{0x44}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	gallery := &fakeMediaService{
		files: map[string][]byte{
			"faces/alice.jpg": []byte("alice-jpeg"),
		},
	}

	// Configure >120/min and >8 burst to prove the legacy 120/8 process cap no longer truncates it.
	handler := NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:       signer,
		GalleryMedia: gallery,
		RatePerMin:   600,
		RateBurst:    160,
		Logger:       zap.NewNop(),
	})
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	exp := time.Now().Add(time.Hour).Unix()
	sig, err := signer.Sign("faces/alice.jpg", exp)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	validURI := "/api/v1/media/signed/faces/alice.jpg?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig

	// 1. 150 valid requests in one burst (> old 120/min global cap) all succeed with Cache-Control: no-store.
	for i := range 150 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, validURI, nil)
		req.RemoteAddr = "198.51.100.10:12345"
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("valid request #%d status = %d, want 200 (%s)", i+1, w.Code, w.Body.String())
		}
		if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("expected Cache-Control: no-store on direct gallery stream, got %q", cc)
		}
	}

	// 2. Exceeding burst (160) returns 429 with MEDIA_URL_RATE_LIMITED (never CLIENT_LOG_*).
	for range 10 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, validURI, nil)
		req.RemoteAddr = "198.51.100.10:12345"
		router.ServeHTTP(w, req)
	}
	w429 := httptest.NewRecorder()
	req429 := httptest.NewRequest(http.MethodGet, validURI, nil)
	req429.RemoteAddr = "198.51.100.10:12345"
	router.ServeHTTP(w429, req429)
	if w429.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after burst exhaustion, got %d", w429.Code)
	}
	var env429 httputil.Response
	if err := json.Unmarshal(w429.Body.Bytes(), &env429); err != nil {
		t.Fatalf("unmarshal 429: %v", err)
	}
	if env429.Code != CodeMediaURLRateLimited {
		t.Fatalf("429 code = %q, want %q", env429.Code, CodeMediaURLRateLimited)
	}

	// 3. Invalid-signature flood from many attacker IPs must NOT consume the global process budget
	// reserved for verified requests.
	smallHandler := NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:       signer,
		GalleryMedia: gallery,
		RatePerMin:   10,
		RateBurst:    5,
		Logger:       zap.NewNop(),
	})
	smallRouter := gin.New()
	smallHandler.RegisterRoutes(smallRouter.Group("/api/v1"))

	// Send 100 invalid requests across 50 attacker IPs (well over process budget = 40/min).
	for i := range 100 {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/media/signed/faces/alice.jpg?exp=9999999999&sig=bad", nil)
		req.RemoteAddr = "203.0.113." + strconv.Itoa(i%50+1) + ":4444"
		smallRouter.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Fatalf("attacker invalid sig #%d status = %d, want 403", i, w.Code)
		}
	}

	// Legitimate receiver IP with valid signature still succeeds!
	wLegit := httptest.NewRecorder()
	reqLegit := httptest.NewRequest(http.MethodGet, validURI, nil)
	reqLegit.RemoteAddr = "192.0.2.99:5555"
	smallRouter.ServeHTTP(wLegit, reqLegit)
	if wLegit.Code != http.StatusOK {
		t.Fatalf("legitimate signed media request DoSed by invalid flood: status=%d body=%s", wLegit.Code, wLegit.Body.String())
	}
}

func TestSignedMediaHandler_TruncatesRedirectTTLForGalleryAndSnapshots(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer, err := NewURLSigner(bytes.Repeat([]byte{0x55}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	// 1. Gallery S3 redirect passes remaining TTL (~20s) rather than 0 (24h default).
	gallery := &recordingPresignGalleryReader{
		fakeMediaService: &fakeMediaService{files: map[string][]byte{}},
		redirectURL:      "https://s3.local/faces/alice.jpg?X-Amz-Expires=20",
	}

	// 2. Snapshot local mediafile handler also serves valid webp via ServeContentWithMaxRedirectTTL.
	root := t.TempDir()
	webpPath := filepath.Join(root, "snapshots", "cam_1", "pic.webp")
	if err := os.MkdirAll(filepath.Dir(webpPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(webpPath, []byte("RIFFxxxxWEBP"), 0o644); err != nil {
		t.Fatalf("write webp: %v", err)
	}
	mediaSvc := mediafile.NewService(fakeMediaRootProvider{root: root}, nil, zap.NewNop())
	mediaHandler := mediafile.NewHandler(mediaSvc)

	handler := NewSignedMediaHandlerWithOptions(SignedMediaHandlerOptions{
		Signer:       signer,
		MediaHandler: mediaHandler,
		GalleryMedia: gallery,
		Logger:       zap.NewNop(),
	})
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	shortExp := time.Now().Add(25 * time.Second).Unix()
	faceSig, err := signer.Sign("faces/alice.jpg", shortExp)
	if err != nil {
		t.Fatalf("Sign face: %v", err)
	}
	wFace := httptest.NewRecorder()
	router.ServeHTTP(
		wFace,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/media/signed/faces/alice.jpg?exp="+strconv.FormatInt(shortExp, 10)+"&sig="+faceSig,
			nil,
		),
	)
	if wFace.Code != http.StatusFound {
		t.Fatalf("face redirect status = %d, want 302 (%s)", wFace.Code, wFace.Body.String())
	}
	if gallery.lastTTL <= 0 || gallery.lastTTL > 26*time.Second {
		t.Fatalf("gallery PresignMediaURL ttl = %v, want bounded near remaining ~25s", gallery.lastTTL)
	}

	snapSig, err := signer.Sign("snapshots/cam_1/pic.webp", shortExp)
	if err != nil {
		t.Fatalf("Sign webp: %v", err)
	}
	wWebp := httptest.NewRecorder()
	router.ServeHTTP(
		wWebp,
		httptest.NewRequest(
			http.MethodGet,
			"/api/v1/media/signed/snapshots/cam_1/pic.webp?exp="+strconv.FormatInt(shortExp, 10)+"&sig="+snapSig,
			nil,
		),
	)
	if wWebp.Code != http.StatusOK {
		t.Fatalf("webp status = %d, want 200 (%s)", wWebp.Code, wWebp.Body.String())
	}
	if ct := wWebp.Header().Get("Content-Type"); !strings.EqualFold(ct, "image/webp") {
		t.Fatalf("webp Content-Type = %q, want image/webp", ct)
	}
}

var _ filestore.ObjectInfo
