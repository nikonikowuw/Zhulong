package alertpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		fakeMediaService: &fakeMediaService{files: map[string][]byte{}},
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

	if len(received.Images) != 2 {
		t.Fatalf("expected scene and object images, got %+v", received.Images)
	}
	scene, object := received.Images[0], received.Images[1]
	if scene.URL == nil || *scene.URL != media.presigned[ev.ImagePath] {
		t.Fatalf("expected presigned scene URL, got %v", scene.URL)
	}
	if object.URL == nil || !strings.HasPrefix(*object.URL, "https://edge.local/api/v1/media/signed/") {
		t.Fatalf("expected HMAC fallback for unpresigned object URL, got %v", object.URL)
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
		check     func(t *testing.T, images []ImageInfo)
	}{
		{
			name: "fixed order with local sizes",
			params: imageURLParams{
				PublicBaseURL: "https://edge.local", Signer: signer, TTL: time.Minute,
				Media: &fakeMediaService{files: map[string][]byte{
					scenePath: make([]byte, 2048), cropPath: make([]byte, 64), refPath: make([]byte, 32),
				}},
			},
			event:     faceEvent,
			wantRoles: []string{ImageRoleScene, ImageRoleObject, ImageRoleReference},
			check: func(t *testing.T, images []ImageInfo) {
				if *images[0].SizeBytes != 2048 || *images[1].SizeBytes != 64 {
					t.Fatalf("sizes = %v %v", *images[0].SizeBytes, *images[1].SizeBytes)
				}
				for _, img := range images {
					if img.URL == nil || !strings.HasPrefix(*img.URL, "https://edge.local/api/v1/media/signed/") {
						t.Fatalf("url = %v", img.URL)
					}
					if img.URLExpiresAt == nil || !payloadTimePattern.MatchString(*img.URLExpiresAt) {
						t.Fatalf("url_expires_at = %v", img.URLExpiresAt)
					}
					if img.MIME != imageMIMEJPEG || img.Part != nil || img.Base64 != nil {
						t.Fatalf("image = %+v", img)
					}
				}
			},
		},
		{
			name:      "no signer and no presigner omits every image",
			params:    imageURLParams{PublicBaseURL: "https://edge.local", TTL: time.Minute},
			event:     faceEvent,
			wantRoles: []string{},
		},
		{
			name: "presigned scene keeps null size, unready crop omitted",
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
			check: func(t *testing.T, images []ImageInfo) {
				if *images[0].URL != "https://minio.local/evt.jpg?sig=1" || images[0].SizeBytes != nil {
					t.Fatalf("scene = %+v", images[0])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			images := resolveImageURLs(context.Background(), tt.params, tt.event)
			roles := make([]string, 0, len(images))
			for _, img := range images {
				roles = append(roles, img.Role)
			}
			if strings.Join(roles, ",") != strings.Join(tt.wantRoles, ",") {
				t.Fatalf("roles = %v, want %v", roles, tt.wantRoles)
			}
			if tt.check != nil {
				tt.check(t, images)
			}
		})
	}
}
