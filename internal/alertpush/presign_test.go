package alertpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

type stubPresigningMediaReader struct {
	*fakeMediaService
	presigned map[string]string
}

func (m *stubPresigningMediaReader) PresignMediaURL(
	_ context.Context,
	relPath string,
	_ time.Duration,
) (string, bool, error) {
	if u, ok := m.presigned[relPath]; ok {
		return u, true, nil
	}
	return "", false, nil
}

func TestWebhookSender_UsesPresignerWithHMACFallback(t *testing.T) {
	t.Parallel()
	var received EventPayload
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if closeErr := r.Body.Close(); closeErr != nil {
				t.Errorf("close body: %v", closeErr)
			}
		}()
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	media := &stubPresigningMediaReader{
		fakeMediaService: &fakeMediaService{files: map[string][]byte{
			"snapshots/cam-01/2026-11-22/evt_crop.jpg": []byte("local-crop-bytes"),
		}},
		presigned: map[string]string{
			"snapshots/cam-01/2026-11-22/evt.jpg": "https://minio.local:9000/zhulong/snapshots/cam-01/2026-11-22/evt.jpg?X-Amz-Signature=abc",
		},
	}

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                "dest_presign",
		URL:               srv.URL,
		ImageMode:         ImageModeURL,
		PublicBaseURL:     "https://edge.local",
		SkipImageURLCheck: true,
		SignedURLTTL:      10 * time.Minute,
		Signer:            signer,
		Media:             media,
		AllowLoopback:     true,
		Logger:            zap.NewNop(),
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("close sender: %v", closeErr)
		}
	}()

	ev := record.AlarmEvent{
		ID:         "alm-1",
		CameraID:   "cam-01",
		AlarmType:  "intrusion",
		ImagePath:  "snapshots/cam-01/2026-11-22/evt.jpg",
		ImageReady: true,
		CropPath:   "snapshots/cam-01/2026-11-22/evt_crop.jpg",
		CropReady:  true,
		CapturedAt: time.Now().UTC(),
	}
	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if received.Images == nil || received.Images.Scene == nil || received.Images.Object == nil {
		t.Fatalf("expected scene and object images, got %+v", received.Images)
	}
	scene, object := received.Images.Scene, received.Images.Object
	if scene.URL != media.presigned[ev.ImagePath] {
		t.Fatalf("expected presigned scene URL, got %q", scene.URL)
	}
	if !strings.HasPrefix(object.URL, "https://edge.local/api/v1/media/signed/") {
		t.Fatalf("expected HMAC fallback for unpresigned object URL, got %q", object.URL)
	}
}

func TestResolveImageURLs(t *testing.T) {
	t.Parallel()
	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	const (
		scenePath = "snapshots/cam-01/2026-11-22/evt.jpg"
		cropPath  = "snapshots/cam-01/2026-11-22/evt_crop.jpg"
		refPath   = "snapshots/gallery/p1.jpg"
	)
	faceEvent := record.AlarmEvent{
		ID: "face-1", EventType: record.EventTypeFaceRecognition, CameraID: "cam-01",
		ImagePath: scenePath, ImageReady: true, CropPath: cropPath, CropReady: true,
		Face: &record.FaceEventData{Matched: true, GalleryImagePath: refPath},
	}

	tests := []struct {
		name      string
		params    imageURLParams
		event     record.AlarmEvent
		wantRoles []string
		check     func(t *testing.T, images *EventImages)
	}{
		{
			name: "all three roles with local sizes",
			params: imageURLParams{
				PublicBaseURL: "https://edge.local", Signer: signer, TTL: time.Minute,
				Media: &fakeMediaService{files: map[string][]byte{
					scenePath: make([]byte, 2048), cropPath: make([]byte, 64), refPath: make([]byte, 32),
				}},
			},
			event:     faceEvent,
			wantRoles: []string{ImageRoleScene, ImageRoleObject, ImageRoleReference},
			check: func(t *testing.T, images *EventImages) {
				if *images.Scene.SizeBytes != 2048 || *images.Object.SizeBytes != 64 {
					t.Fatalf("sizes = %v %v", *images.Scene.SizeBytes, *images.Object.SizeBytes)
				}
				for _, img := range []*ImageInfo{images.Scene, images.Object, images.Reference} {
					if !strings.HasPrefix(img.URL, "https://edge.local/api/v1/media/signed/") {
						t.Fatalf("url = %q", img.URL)
					}
					if !payloadTimePattern.MatchString(img.ExpiresAt) {
						t.Fatalf("expires_at = %q", img.ExpiresAt)
					}
					if img.MIME != imageMIMEJPEG || img.Part != "" || img.Base64 != "" {
						t.Fatalf("image = %+v", img)
					}
				}
			},
		},
		{
			name:      "no signer and no presigner returns nil",
			params:    imageURLParams{PublicBaseURL: "https://edge.local", TTL: time.Minute},
			event:     faceEvent,
			wantRoles: []string{},
			check: func(t *testing.T, images *EventImages) {
				if images != nil {
					t.Fatalf("images = %+v, want nil", images)
				}
			},
		},
		{
			name: "presigned scene omits size, unready crop omitted",
			params: imageURLParams{
				PublicBaseURL: "https://edge.local", Signer: signer, TTL: time.Minute,
				Media: &stubPresigningMediaReader{
					fakeMediaService: &fakeMediaService{files: map[string][]byte{scenePath: make([]byte, 10)}},
					presigned:        map[string]string{scenePath: "https://minio.local/evt.jpg?sig=1"},
				},
			},
			event: record.AlarmEvent{
				ID: "alm-1", CameraID: "cam-01", ImagePath: scenePath, ImageReady: true,
				CropPath: cropPath, CropReady: false,
			},
			wantRoles: []string{ImageRoleScene},
			check: func(t *testing.T, images *EventImages) {
				if images.Scene.URL != "https://minio.local/evt.jpg?sig=1" || images.Scene.SizeBytes != nil {
					t.Fatalf("scene = %+v", images.Scene)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			images := resolveImageURLs(context.Background(), tt.params, tt.event)
			roles := imageRolesPresent(images)
			if strings.Join(roles, ",") != strings.Join(tt.wantRoles, ",") {
				t.Fatalf("roles = %v, want %v", roles, tt.wantRoles)
			}
			if tt.check != nil {
				tt.check(t, images)
			}
		})
	}
}

type statterOnlyMediaReader struct {
	*countingMediaReader
	statCalls atomic.Int32
	sizes     map[string]int64
}

func (s *statterOnlyMediaReader) GetEntry(_ context.Context, relPath string) (*mediafile.MediaEntry, error) {
	s.statCalls.Add(1)
	sz, ok := s.sizes[relPath]
	if !ok {
		return nil, os.ErrNotExist
	}
	return &mediafile.MediaEntry{Path: relPath, SizeBytes: sz}, nil
}

func TestResolveImageURLs_MediaStatterMissingFileUnmatchedFaceAndTTLSync(t *testing.T) {
	t.Parallel()
	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	const (
		scenePath   = "snapshots/cam-01/2026-11-25/present.jpg"
		missingPath = "snapshots/cam-01/2026-11-25/missing.jpg"
	)

	reader := &statterOnlyMediaReader{
		countingMediaReader: &countingMediaReader{files: map[string][]byte{scenePath: []byte("abc")}},
		sizes:               map[string]int64{scenePath: 4096},
	}

	ev := record.AlarmEvent{
		ID:         "face-unmatched-missing-crop",
		EventType:  record.EventTypeFaceRecognition,
		CameraID:   "cam-01",
		ImagePath:  scenePath,
		ImageReady: true,
		CropPath:   missingPath,
		CropReady:  true,
		Face: &record.FaceEventData{
			Matched:          false, // Unmatched face must NOT attach GalleryImagePath.
			GalleryImagePath: "alice.jpg",
		},
	}

	// Pass TTL=0 to also verify zero-TTL fallback and exp == expires_at exact sync.
	images := resolveImageURLs(context.Background(), imageURLParams{
		PublicBaseURL: "https://edge.local",
		Signer:        signer,
		Media:         reader,
		TTL:           0,
	}, ev)
	if images == nil || images.Scene == nil {
		t.Fatalf("expected scene image, got %+v", images)
	}
	if images.Object != nil {
		t.Fatalf("confirmed missing local crop must not mint 404 URL, got %+v", images.Object)
	}
	if images.Reference != nil {
		t.Fatalf("unmatched face must not attach reference image, got %+v", images.Reference)
	}
	if reader.opens.Load() != 0 {
		t.Fatalf("MediaStatter should avoid OpenContent, got opens=%d", reader.opens.Load())
	}
	if reader.statCalls.Load() != 2 {
		t.Fatalf("expected 2 GetEntry calls, got %d", reader.statCalls.Load())
	}
	if images.Scene.SizeBytes == nil || *images.Scene.SizeBytes != 4096 {
		t.Fatalf("scene size = %v, want 4096", images.Scene.SizeBytes)
	}

	parsed, err := url.Parse(images.Scene.URL)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	expUnix, err := strconv.ParseInt(parsed.Query().Get("exp"), 10, 64)
	if err != nil {
		t.Fatalf("parse exp: %v", err)
	}
	if got := formatPayloadTime(time.Unix(expUnix, 0).UTC()); got != images.Scene.ExpiresAt {
		t.Fatalf("exp (%s) != expires_at (%s)", got, images.Scene.ExpiresAt)
	}
	if expUnix <= time.Now().Unix()+3000 {
		t.Fatalf("zero TTL should fall back to default (~1h), got exp=%d", expUnix)
	}
}
