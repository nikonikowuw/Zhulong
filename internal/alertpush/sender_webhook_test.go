package alertpush

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

// fakeIdentity is a mutable DeviceIDProvider: tests flip the value to prove the
// device_id is resolved per send, not frozen when the sender is constructed.
type fakeIdentity struct {
	value string
}

func (f *fakeIdentity) DeviceID() string { return f.value }

type fakeMediaService struct {
	files map[string][]byte
}

func (m *fakeMediaService) OpenContent(_ context.Context, relPath string) (*os.File, fs.FileInfo, error) {
	data, ok := m.files[relPath]
	if !ok {
		return nil, nil, os.ErrNotExist
	}
	tmp := filepath.Join(os.TempDir(), filepath.Base(relPath))
	_ = os.WriteFile(tmp, data, 0o600)
	f, err := os.Open(tmp)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func TestWebhookSenderURLMode(t *testing.T) {
	var receivedBody []byte
	var receivedAuth string
	var receivedCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom-Key")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	identity := &fakeIdentity{value: "site-a-gate-01"}
	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_01",
		ImageMode:     ImageModeURL,
		PublicBaseURL: "http://10.0.0.12:8000",
		SignedURLTTL:  1 * time.Hour,
		Signer:        signer,
		URL:           server.URL,
		AuthType:      AuthTypeBearer,
		Token:         "secret-token",
		Identity:      identity,
		Headers: map[string]string{
			"X-Custom-Key":  "val-123",
			"Authorization": "should-be-ignored",
		},
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM01",
		CameraID:   "cam_01",
		AlarmType:  "smoke",
		Score:      0.95,
		ImagePath:  "snapshots/cam_01/2026-11-03/01J8Z.jpg",
		CropPath:   "snapshots/cam_01/2026-11-03/01J8Z_crop.jpg",
		ImageReady: true,
		CropReady:  true,
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedAuth != "Bearer secret-token" {
		t.Errorf("expected Bearer secret-token, got %q", receivedAuth)
	}
	if receivedCustom != "val-123" {
		t.Errorf("expected val-123, got %q", receivedCustom)
	}

	var payload AlarmPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal received body: %v", err)
	}
	if !payload.ImageReady || payload.ImageURL == nil || payload.CropURL == nil {
		t.Fatalf("expected both URLs populated, got ImageReady=%v, ImageURL=%v, CropURL=%v",
			payload.ImageReady, payload.ImageURL, payload.CropURL)
	}
	if payload.DeviceID != "site-a-gate-01" {
		t.Fatalf("expected device_id site-a-gate-01, got %q", payload.DeviceID)
	}

	// Renaming the identity must affect the very next send without rebuilding the
	// sender: the dispatcher deliberately resolves it per delivery.
	identity.value = "site-a-gate-02"
	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatalf("second Send failed: %v", err)
	}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal second body: %v", err)
	}
	if payload.DeviceID != "site-a-gate-02" {
		t.Fatalf("expected device_id to follow the provider, got %q", payload.DeviceID)
	}
}

func TestWebhookSenderMultipartMode(t *testing.T) {
	var receivedPayload []byte
	var receivedImage []byte

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			data, _ := io.ReadAll(part)
			switch part.FormName() {
			case "payload":
				receivedPayload = data
			case "image":
				receivedImage = data
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	fakeMedia := &fakeMediaService{
		files: map[string][]byte{
			"snapshots/cam_01/2026-11-03/01J8Z.jpg": []byte("mock-jpeg-bytes-12345"),
		},
	}

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:        "dest_02",
		ImageMode: ImageModeMultipart,
		Media:     fakeMedia,
		URL:       server.URL,
		AuthType:  AuthTypeBasic,
		Username:  "admin",
		Password:  "pass123",
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM02",
		CameraID:   "cam_01",
		AlarmType:  "fire",
		Score:      0.99,
		ImagePath:  "snapshots/cam_01/2026-11-03/01J8Z.jpg",
		ImageReady: true,
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if string(receivedImage) != "mock-jpeg-bytes-12345" {
		t.Errorf("image bytes mismatch, got %q", string(receivedImage))
	}
	var payload AlarmPayload
	if err := json.Unmarshal(receivedPayload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !payload.ImageReady {
		t.Error("expected payload.ImageReady true")
	}
}

func TestWebhookSenderRetryOn500(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := attempts.Add(1)
		if att < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_03",
		ImageMode:     ImageModeURL,
		PublicBaseURL: "http://10.0.0.12:8000",
		URL:           server.URL,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM03",
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), ev); err != nil {
		t.Fatalf("expected successful send after retry, got %v", err)
	}
	if attempts.Load() != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts.Load())
	}
}

func TestWebhookSenderNoRetryOn400(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_04",
		ImageMode:     ImageModeURL,
		PublicBaseURL: "http://10.0.0.12:8000",
		URL:           server.URL,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM04",
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), ev); err == nil {
		t.Fatal("expected error on 400 Bad Request, got nil")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt without retry on 400, got %d", attempts.Load())
	}
}
