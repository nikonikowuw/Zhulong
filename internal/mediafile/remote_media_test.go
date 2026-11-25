package mediafile

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"go.uber.org/zap"
)

type staticDeliveryMode string

func (d staticDeliveryMode) DeliveryMode(context.Context) string { return string(d) }

type mutableDeliveryMode struct {
	mu   sync.RWMutex
	mode string
}

func (m *mutableDeliveryMode) DeliveryMode(context.Context) string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mode
}

func (m *mutableDeliveryMode) set(mode string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mode = mode
}

type mockS3MediaServer struct {
	mu          sync.Mutex
	objects     map[string][]byte
	getRequests int
	server      *httptest.Server
}

func newMockS3MediaServer(t *testing.T) *mockS3MediaServer {
	t.Helper()
	m := &mockS3MediaServer{objects: map[string][]byte{}}
	m.server = httptest.NewServer(http.HandlerFunc(m.serve))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockS3MediaServer) resetGetRequests() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getRequests = 0
}

func (m *mockS3MediaServer) totalGetRequests() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.getRequests
}

func (m *mockS3MediaServer) put(key string, payload []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = payload
}

func (m *mockS3MediaServer) serve(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(key, "/", 2)
	if len(parts) < 2 || parts[1] == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	objKey := parts[1]

	m.mu.Lock()
	data, ok := m.objects[objKey]
	m.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusNotFound)
		_, err := fmt.Fprint(w, `<Error><Code>NoSuchKey</Code><Message>not found</Message></Error>`)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
		}
		return
	}

	sum := md5.Sum(data)
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum[:])+`"`)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		return
	}

	m.mu.Lock()
	m.getRequests++
	m.mu.Unlock()

	if rangeHdr := r.Header.Get("Range"); strings.HasPrefix(rangeHdr, "bytes=") {
		spec := strings.TrimPrefix(rangeHdr, "bytes=")
		bounds := strings.SplitN(spec, "-", 2)
		start, err := strconv.Atoi(bounds[0])
		if err != nil || start < 0 || start >= len(data) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end := len(data) - 1
		if len(bounds) == 2 && bounds[1] != "" {
			if parsedEnd, err := strconv.Atoi(bounds[1]); err == nil && parsedEnd < end {
				end = parsedEnd
			}
		}
		slice := data[start : end+1]
		w.Header().Set("Content-Range", fmt.Sprintf(
			"bytes %d-%d/%d",
			start,
			end,
			len(data),
		))
		w.Header().Set("Content-Length", strconv.Itoa(len(slice)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.Copy(w, bytes.NewReader(slice))
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, bytes.NewReader(data))
}

func TestHandler_S3ProxyRangeAndRedirectAndFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	localRoot := t.TempDir()

	writeFixtureFile(
		t,
		localRoot,
		"snapshots/2026/fallback.jpg",
		"local-fallback-jpg",
	)

	mockS3 := newMockS3MediaServer(t)
	mockS3.put("edge/recordings/2026/remote.mp4", []byte("0123456789abcdef"))

	mgr := filestore.NewManager(filestore.ManagerConfig{
		Local:         filestore.LocalConfig{RootDir: localRoot},
		LocalFallback: true,
		Logger:        zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close filestore manager: %v", err)
		}
	}()

	err := mgr.ConfigureS3(
		filestore.S3Config{
			Endpoint:        mockS3.server.URL,
			Region:          "us-east-1",
			Bucket:          "zhulong",
			Prefix:          "edge",
			AccessKeyID:     "ak",
			SecretAccessKey: "sk",
			UsePathStyle:    true,
		},
		filestore.LocalConfig{RootDir: localRoot},
		true,
	)
	if err != nil {
		t.Fatalf("ConfigureS3 failed: %v", err)
	}

	mode := &mutableDeliveryMode{mode: "proxy"}
	comp := NewMediaFileComponentsWithStore(StoreOptions{
		Store:        mgr,
		DeliveryMode: mode,
		PresignTTL:   5 * time.Minute,
		Logger:       zap.NewNop(),
	})
	router := gin.New()
	comp.ProtectedRoutes.RegisterRoutes(router.Group("/api/v1"))

	// Proxy mode streams 206 Range directly from remote S3.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/recordings/2026/remote.mp4", nil)
	req.Header.Set("Range", "bytes=4-9")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "456789" {
		t.Fatalf("expected 206 '456789', got %d body=%q", rec.Code, rec.Body.String())
	}

	// Explicit HEAD works in proxy mode.
	headReq := httptest.NewRequest(http.MethodHead, "/api/v1/media/content/recordings/2026/remote.mp4", nil)
	headRec := httptest.NewRecorder()
	router.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 ||
		headRec.Header().Get("Content-Type") != "video/mp4" {
		t.Fatalf(
			"unexpected HEAD response: code=%d headers=%v bodyLen=%d",
			headRec.Code,
			headRec.Header(),
			headRec.Body.Len(),
		)
	}

	// Non-media namespaces stay blocked with 422 even in S3 mode.
	facesReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/faces/secret.jpg", nil)
	facesRec := httptest.NewRecorder()
	router.ServeHTTP(facesRec, facesReq)
	if facesRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for faces namespace, got %d (%s)", facesRec.Code, facesRec.Body.String())
	}

	// Switch to redirect mode: primary S3 key issues 302 Location without caching.
	mode.set("redirect")
	redirReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/recordings/2026/remote.mp4", nil)
	redirRec := httptest.NewRecorder()
	router.ServeHTTP(redirRec, redirReq)
	if redirRec.Code != http.StatusFound || redirRec.Header().Get("Cache-Control") != "no-store" ||
		!strings.Contains(redirRec.Header().Get("Location"), "edge/recordings/2026/remote.mp4") {
		t.Fatalf(
			"expected 302 presigned redirect, got %d loc=%q cc=%q",
			redirRec.Code,
			redirRec.Header().Get("Location"),
			redirRec.Header().Get("Cache-Control"),
		)
	}

	// HEAD in redirect mode stays on ServeContent (200 metadata without GET body or GET-signed 302).
	mockS3.resetGetRequests()
	headRedirReq := httptest.NewRequest(http.MethodHead, "/api/v1/media/content/recordings/2026/remote.mp4", nil)
	headRedirRec := httptest.NewRecorder()
	router.ServeHTTP(headRedirRec, headRedirReq)
	if headRedirRec.Code != http.StatusOK || headRedirRec.Body.Len() != 0 ||
		headRedirRec.Header().Get("Content-Type") != "video/mp4" ||
		headRedirRec.Header().Get("Content-Length") != "16" ||
		mockS3.totalGetRequests() != 0 {
		t.Fatalf(
			"unexpected HEAD in redirect mode: code=%d headers=%v bodyLen=%d getReqs=%d",
			headRedirRec.Code,
			headRedirRec.Header(),
			headRedirRec.Body.Len(),
			mockS3.totalGetRequests(),
		)
	}

	// Unmigrated legacy local key still falls back to proxy 200 instead of broken S3 302.
	fbReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/snapshots/2026/fallback.jpg", nil)
	fbRec := httptest.NewRecorder()
	router.ServeHTTP(fbRec, fbReq)
	if fbRec.Code != http.StatusOK || fbRec.Body.String() != "local-fallback-jpg" {
		t.Fatalf("expected fallback 200 local content, got %d body=%q", fbRec.Code, fbRec.Body.String())
	}

	// PresignMediaURL returns true for remote object and false for fallback object.
	u, handled, err := comp.Service.PresignMediaURL(ctx, "recordings/2026/remote.mp4", time.Minute)
	if err != nil || !handled || !strings.Contains(u, "X-Amz-Signature=") {
		t.Fatalf(
			"expected presigned URL for remote clip, got u=%q handled=%v err=%v",
			u,
			handled,
			err,
		)
	}
	_, handledFB, err := comp.Service.PresignMediaURL(ctx, "snapshots/2026/fallback.jpg", time.Minute)
	if err != nil || handledFB {
		t.Fatalf("expected handled=false for fallback key, got handled=%v err=%v", handledFB, err)
	}

	// ResolveRedirectWithMaxTTL / ServeContentWithMaxRedirectTTL caps S3 X-Amz-Expires to min(presignTTL, maxTTL).
	shortURL, okShort, err := comp.Service.ResolveRedirectWithMaxTTL(ctx, "recordings/2026/remote.mp4", 30*time.Second)
	if err != nil || !okShort || !strings.Contains(shortURL, "X-Amz-Expires=30") {
		t.Fatalf("expected capped 30s presigned redirect, got url=%q ok=%v err=%v", shortURL, okShort, err)
	}
	// Larger maxTTL must not exceed configured s.presignTTL (5m = 300s).
	cappedURL, okCap, err := comp.Service.ResolveRedirectWithMaxTTL(ctx, "recordings/2026/remote.mp4", 24*time.Hour)
	if err != nil || !okCap || !strings.Contains(cappedURL, "X-Amz-Expires=300") {
		t.Fatalf("expected default 300s ceiling, got url=%q ok=%v err=%v", cappedURL, okCap, err)
	}

	signedRouter := gin.New()
	signedRouter.GET("/signed/*path", func(c *gin.Context) {
		comp.Handler.ServeContentWithMaxRedirectTTL(c, strings.TrimPrefix(c.Param("path"), "/"), 25*time.Second)
	})
	capRec := httptest.NewRecorder()
	signedRouter.ServeHTTP(capRec, httptest.NewRequest(http.MethodGet, "/signed/recordings/2026/remote.mp4", nil))
	if capRec.Code != http.StatusFound || !strings.Contains(capRec.Header().Get("Location"), "X-Amz-Expires=25") {
		t.Fatalf("ServeContentWithMaxRedirectTTL got %d loc=%q, want 302 with X-Amz-Expires=25",
			capRec.Code, capRec.Header().Get("Location"))
	}
}

func TestHandler_LocalManagerWithRedirectDeliveryFallsBackToProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx := context.Background()
	localRoot := t.TempDir()
	const payload = "local-redirect-fallback-jpg"
	writeFixtureFile(
		t,
		localRoot,
		"snapshots/2026/local.jpg",
		payload,
	)

	mgr := filestore.NewManager(filestore.ManagerConfig{
		Local:  filestore.LocalConfig{RootDir: localRoot},
		Logger: zap.NewNop(),
	})
	defer func() {
		if err := mgr.Close(); err != nil {
			t.Errorf("close filestore manager: %v", err)
		}
	}()

	comp := NewMediaFileComponentsWithStore(StoreOptions{
		Store:        mgr,
		DeliveryMode: staticDeliveryMode("redirect"),
		PresignTTL:   time.Minute,
		Logger:       zap.NewNop(),
	})
	router := gin.New()
	comp.ProtectedRoutes.RegisterRoutes(router.Group("/api/v1"))

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/snapshots/2026/local.jpg", nil)
	getRec := httptest.NewRecorder()
	router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK || getRec.Body.String() != payload {
		t.Fatalf("expected 200 proxy fallback for local+redirect, got %d body=%q", getRec.Code, getRec.Body.String())
	}

	headReq := httptest.NewRequest(http.MethodHead, "/api/v1/media/content/snapshots/2026/local.jpg", nil)
	headRec := httptest.NewRecorder()
	router.ServeHTTP(headRec, headReq)
	if headRec.Code != http.StatusOK || headRec.Body.Len() != 0 ||
		headRec.Header().Get("Content-Type") != "image/jpeg" ||
		headRec.Header().Get("Content-Length") != strconv.Itoa(len(payload)) {
		t.Fatalf(
			"expected 200 HEAD metadata for local+redirect, got %d headers=%v bodyLen=%d",
			headRec.Code,
			headRec.Header(),
			headRec.Body.Len(),
		)
	}

	urlStr, handled, err := comp.Service.PresignMediaURL(ctx, "snapshots/2026/local.jpg", time.Minute)
	if err != nil || handled || urlStr != "" {
		t.Fatalf(
			"expected (\"\", false, nil) for PresignMediaURL on local manager, got url=%q handled=%v err=%v",
			urlStr,
			handled,
			err,
		)
	}
}

func TestDeliveryModeProvider_LocalForcesProxy(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(ctx); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	defer func() {
		if err := dbStore.Close(); err != nil {
			t.Logf("close db: %v", err)
		}
	}()

	comp := storage.NewStorageComponents(dbStore, nil, zap.NewNop())
	cfg := storage.DefaultStorageConfig()
	cfg.Backend = storage.BackendLocal
	cfg.DeliveryMode = storage.DeliveryModeRedirect
	cfg.MediaDirectory = tempDir
	if _, err := comp.Repository.UpdateConfig(ctx, cfg); err != nil {
		t.Fatalf("UpdateConfig: %v", err)
	}

	provider := NewDeliveryModeProvider(comp.Service)
	if got := provider.DeliveryMode(ctx); got != storage.DeliveryModeProxy {
		t.Fatalf("expected proxy delivery mode when backend=local, got %q", got)
	}
}
