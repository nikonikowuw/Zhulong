package alertpush

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	var received AlarmPayload
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
	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if received.ImageURL == nil || *received.ImageURL != media.presigned[ev.ImagePath] {
		t.Fatalf("expected presigned ImageURL, got %v", received.ImageURL)
	}
	if received.CropURL == nil || *received.CropURL == "" {
		t.Fatalf("expected HMAC fallback for unpresigned CropURL, got nil")
	}
}
