package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/alertpush"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
)

func TestAlertGalleryMediaAdapter_IsolatesContentEndpointAndServesSignedFace(t *testing.T) {
	t.Parallel()
	gin.SetMode(gin.TestMode)

	mediaRoot := t.TempDir()
	facesRoot := t.TempDir()

	faceName := "person_alice_face_01.png"
	faceBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x42}
	if err := os.WriteFile(filepath.Join(facesRoot, faceName), faceBytes, 0o600); err != nil {
		t.Fatalf("write face file: %v", err)
	}

	fsMgr := filestore.NewManager(filestore.ManagerConfig{
		Local: filestore.LocalConfig{
			RootDir: mediaRoot,
			NamespaceRoots: map[string]string{
				filestore.NamespaceFaces: facesRoot,
			},
			Logger: zap.NewNop(),
		},
		Logger: zap.NewNop(),
	})
	t.Cleanup(func() {
		if err := fsMgr.Close(); err != nil {
			t.Errorf("close filestore: %v", err)
		}
	})

	mediaSvc := mediafile.NewServiceWithStore(mediafile.StoreOptions{
		Store:  fsMgr,
		Logger: zap.NewNop(),
	})
	mediaHandler := mediafile.NewHandler(mediaSvc)

	galleryAdapter := newAlertGalleryMediaAdapter(fsMgr, nil, 10*time.Minute)

	signer, err := alertpush.NewURLSigner(bytes.Repeat([]byte{0x5A}, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	signedHandler := alertpush.NewSignedMediaHandlerWithGallery(
		signer,
		mediaHandler,
		galleryAdapter,
		zap.NewNop(),
	)

	router := gin.New()
	api := router.Group("/api/v1")
	mediaHandler.RegisterRoutes(api)
	signedHandler.RegisterRoutes(api)

	// 1. /api/v1/media/content/faces/* MUST remain blocked (422 MEDIA_KIND_INVALID).
	wContent := httptest.NewRecorder()
	reqContent := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/faces/"+faceName, nil)
	router.ServeHTTP(wContent, reqContent)
	if wContent.Code != http.StatusUnprocessableEntity {
		t.Fatalf("/media/content/faces/* status = %d, want 422 (%s)", wContent.Code, wContent.Body.String())
	}

	// 2. Signed /api/v1/media/signed/faces/<file> succeeds with real bytes and image/png.
	relKey := "faces/" + faceName
	exp := time.Now().Add(time.Hour).Unix()
	sig, err := signer.Sign(relKey, exp)
	if err != nil {
		t.Fatalf("Sign(%q): %v", relKey, err)
	}

	wSigned := httptest.NewRecorder()
	signedURI := "/api/v1/media/signed/" + relKey + "?exp=" + strconv.FormatInt(exp, 10) + "&sig=" + sig
	reqSigned := httptest.NewRequest(http.MethodGet, signedURI, nil)
	router.ServeHTTP(wSigned, reqSigned)
	if wSigned.Code != http.StatusOK {
		t.Fatalf("/media/signed/faces/* status = %d, want 200 (%s)", wSigned.Code, wSigned.Body.String())
	}
	if ct := wSigned.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if !bytes.Equal(wSigned.Body.Bytes(), faceBytes) {
		t.Fatalf("signed face bytes mismatch")
	}

	// 3. Gallery adapter strictly refuses non-face namespaces and multi-segment keys.
	for _, rejected := range []string{
		"snapshots/cam_01/evt.jpg",
		"recordings/cam_01/clip.mp4",
		"faces/sub/nested.png",
		"faces/../secret.png",
		faceName,
	} {
		rc, _, openErr := galleryAdapter.OpenContent(context.Background(), rejected)
		if openErr == nil {
			if closeErr := rc.Close(); closeErr != nil {
				t.Errorf("close unexpected handle: %v", closeErr)
			}
			t.Fatalf("OpenContent(%q) unexpectedly succeeded", rejected)
		}
	}
}

type stubGalleryPresignStore struct {
	filestore.Store
	lastKey string
	lastTTL time.Duration
}

func (s *stubGalleryPresignStore) SupportsPresign() bool { return true }

func (s *stubGalleryPresignStore) ExistsInSpool(context.Context, string) (bool, error) {
	return false, nil
}

func (s *stubGalleryPresignStore) ExistsInPrimary(context.Context, string) (bool, error) {
	return true, nil
}

func (s *stubGalleryPresignStore) PresignGet(
	_ context.Context,
	key string,
	ttl time.Duration,
) (string, error) {
	s.lastKey = key
	s.lastTTL = ttl
	return "https://s3.example.com/" + key + "?ttl=" + ttl.String(), nil
}

type stubRedirectDeliveryMode struct{}

func (stubRedirectDeliveryMode) DeliveryMode(context.Context) string { return "redirect" }

func TestAlertGalleryMediaAdapter_PresignMediaURLClampsTTL(t *testing.T) {
	t.Parallel()

	defaultTTL := 10 * time.Minute
	tests := []struct {
		name    string
		reqTTL  time.Duration
		wantTTL time.Duration
	}{
		{name: "zero falls back to default", reqTTL: 0, wantTTL: defaultTTL},
		{name: "negative falls back to default", reqTTL: -5 * time.Second, wantTTL: defaultTTL},
		{name: "shorter positive TTL preserved", reqTTL: 90 * time.Second, wantTTL: 90 * time.Second},
		{name: "equal TTL preserved", reqTTL: defaultTTL, wantTTL: defaultTTL},
		{name: "larger TTL clamped to default", reqTTL: 2 * time.Hour, wantTTL: defaultTTL},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &stubGalleryPresignStore{}
			adapter := newAlertGalleryMediaAdapter(store, stubRedirectDeliveryMode{}, defaultTTL)

			presigned, ok, err := adapter.PresignMediaURL(
				context.Background(),
				"faces/person_alice_face_01.png",
				tt.reqTTL,
			)
			if err != nil {
				t.Fatalf("PresignMediaURL(%v) error = %v", tt.reqTTL, err)
			}
			if !ok || presigned == "" {
				t.Fatalf("PresignMediaURL(%v) = (%q, %v), want presigned URL", tt.reqTTL, presigned, ok)
			}
			if store.lastKey != "faces/person_alice_face_01.png" {
				t.Fatalf("PresignGet key = %q, want faces/person_alice_face_01.png", store.lastKey)
			}
			if store.lastTTL != tt.wantTTL {
				t.Fatalf("PresignGet ttl = %v, want %v", store.lastTTL, tt.wantTTL)
			}
		})
	}
}
