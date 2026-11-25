package alertpush

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
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

type nopReadSeekCloser struct {
	*bytes.Reader
}

func (n nopReadSeekCloser) Close() error { return nil }

func (m *fakeMediaService) OpenContent(
	_ context.Context,
	relPath string,
) (io.ReadSeekCloser, filestore.ObjectInfo, error) {
	data, ok := m.files[relPath]
	if !ok {
		return nil, filestore.ObjectInfo{}, os.ErrNotExist
	}
	info := filestore.ObjectInfo{
		Key:        relPath,
		Name:       path.Base(relPath),
		SizeBytes:  int64(len(data)),
		ModifiedAt: time.Now().UTC(),
	}
	return nopReadSeekCloser{bytes.NewReader(data)}, info, nil
}

func TestWebhookSenderURLMode(t *testing.T) {
	var receivedBody []byte
	var receivedAuth string
	var receivedCustom string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedCustom = r.Header.Get("X-Custom-Key")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		receivedBody = body
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
		AllowLoopback: true,
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

	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
		t.Fatalf("Send failed: %v", err)
	}

	if receivedAuth != "Bearer secret-token" {
		t.Errorf("expected Bearer secret-token, got %q", receivedAuth)
	}
	if receivedCustom != "val-123" {
		t.Errorf("expected val-123, got %q", receivedCustom)
	}

	var payload EventPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal received body: %v", err)
	}
	if payload.Images == nil || payload.Images.Scene == nil || payload.Images.Object == nil ||
		payload.Images.Reference != nil {
		t.Fatalf("expected scene+object images, got %+v", payload.Images)
	}
	for _, img := range []*ImageInfo{payload.Images.Scene, payload.Images.Object} {
		if img.URL == "" || img.Part != "" || img.Base64 != "" {
			t.Fatalf("url mode image = %+v", img)
		}
	}
	if payload.DeviceID != "site-a-gate-01" {
		t.Fatalf("expected device_id site-a-gate-01, got %q", payload.DeviceID)
	}

	// Renaming the identity must affect the very next send without rebuilding the
	// sender: the dispatcher deliberately resolves it per delivery.
	identity.value = "site-a-gate-02"
	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
		t.Fatalf("second Send failed: %v", err)
	}
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal second body: %v", err)
	}
	if payload.DeviceID != "site-a-gate-02" {
		t.Fatalf("expected device_id to follow the provider, got %q", payload.DeviceID)
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
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM03",
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), Delivery{Event: ev}); err != nil {
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
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer sender.Close()

	ev := record.AlarmEvent{
		ID:         "01J8ZALARM04",
		CapturedAt: time.Now().UTC(),
	}

	if err := sender.Send(context.Background(), Delivery{Event: ev}); err == nil {
		t.Fatal("expected error on 400 Bad Request, got nil")
	}
	if attempts.Load() != 1 {
		t.Errorf("expected 1 attempt without retry on 400, got %d", attempts.Load())
	}
}

func TestWebhookSenderHeartbeatRouteSplitAndSingleShot(t *testing.T) {
	var mainHits atomic.Int32
	var hbHits atomic.Int32
	var gotAuth string
	var gotHeaders http.Header
	gotBody := []byte{}

	mux := http.NewServeMux()
	mux.HandleFunc("/events", func(w http.ResponseWriter, r *http.Request) {
		mainHits.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		hbHits.Add(1)
		gotAuth = r.Header.Get("Authorization")
		gotHeaders = r.Header.Clone()
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotBody = body
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/heartbeat-500", func(w http.ResponseWriter, r *http.Request) {
		hbHits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                "dest_hb_01",
		ImageMode:         ImageModeURL,
		SkipImageURLCheck: true,
		URL:               server.URL + "/events",
		HeartbeatURL:      server.URL + "/heartbeat",
		AuthType:          AuthTypeBearer,
		Token:             "shared-token-xyz",
		Identity:          &fakeIdentity{value: "edge-box-09"},
		AllowLoopback:     true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender (heartbeat-only without PublicBaseURL): %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("sender.Close: %v", closeErr)
		}
	}()

	reportedAt, parseErr := time.Parse(time.RFC3339, "2026-11-11T08:00:00Z")
	if parseErr != nil {
		t.Fatalf("parse timestamp: %v", parseErr)
	}
	snap := HeartbeatSnapshot{
		ReportedAt: reportedAt,
		Status:     "degraded",
		Reasons:    []string{"camera_offline"},
		Host:       HeartbeatHostPayload{Arch: "arm64", UptimeSeconds: 3600},
		Storage: HeartbeatStoragePayload{
			Status:      "healthy",
			Writable:    true,
			UsedPercent: 42,
			FreeMB:      8192,
		},
	}

	if err := sender.SendHeartbeat(context.Background(), snap, 7); err != nil {
		t.Fatalf("SendHeartbeat failed: %v", err)
	}
	if mainHits.Load() != 0 || hbHits.Load() != 1 {
		t.Fatalf("expected mainHits=0 hbHits=1, got main=%d hb=%d", mainHits.Load(), hbHits.Load())
	}
	if gotAuth != "Bearer shared-token-xyz" {
		t.Fatalf("expected shared bearer auth, got %q", gotAuth)
	}

	var payload HeartbeatPayload
	if err := json.Unmarshal(gotBody, &payload); err != nil {
		t.Fatalf("unmarshal heartbeat payload: %v", err)
	}
	headerOK := payload.EventType == EventTypeHeartbeat &&
		payload.Seq == 7 &&
		payload.DeviceID == "edge-box-09"
	if !headerOK {
		t.Fatalf("unexpected heartbeat payload header: %+v", payload)
	}
	reasonsOK := len(payload.Reasons) == 1 && payload.Reasons[0] == "camera_offline"
	if payload.Status != "degraded" || !reasonsOK {
		t.Fatalf("unexpected status/reasons: %+v", payload)
	}
	if gotHeaders.Get(headerZhulongEvent) != EventTypeHeartbeat ||
		gotHeaders.Get(headerZhulongDelivery) != payload.ID ||
		gotHeaders.Get(headerZhulongSchema) != "1" {
		t.Fatalf("unexpected envelope headers on heartbeat: %v", gotHeaders)
	}

	// Verify SendHeartbeat does NOT retry on HTTP 500 (single-shot only).
	hbHits.Store(0)
	failSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                "dest_hb_02",
		ImageMode:         ImageModeURL,
		SkipImageURLCheck: true,
		URL:               server.URL + "/events",
		HeartbeatURL:      server.URL + "/heartbeat-500",
		AllowLoopback:     true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender failSender: %v", err)
	}
	defer func() {
		if closeErr := failSender.Close(); closeErr != nil {
			t.Errorf("failSender.Close: %v", closeErr)
		}
	}()

	if err := failSender.SendHeartbeat(context.Background(), snap, 1); err == nil {
		t.Fatal("expected error on HTTP 500 heartbeat")
	}
	if hbHits.Load() != 1 {
		t.Fatalf("expected single-shot (1 attempt) for heartbeat on 500, got %d", hbHits.Load())
	}
}

func TestWebhookSenderBlocksCrossOriginRedirectWithCredentials(t *testing.T) {
	var evilHits atomic.Int32
	evilServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		evilHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer evilServer.Close()

	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(
			w,
			r,
			evilServer.URL+"/collect",
			http.StatusTemporaryRedirect,
		)
	}))
	defer originServer.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                "dest_redir_guard",
		ImageMode:         ImageModeURL,
		SkipImageURLCheck: true,
		URL:               originServer.URL + "/hb",
		Headers:           map[string]string{"X-Api-Secret": "top-secret"},
		AllowLoopback:     true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("sender.Close: %v", closeErr)
		}
	}()

	snap := HeartbeatSnapshot{ReportedAt: time.Now().UTC(), Status: "ready", Reasons: []string{}}
	sendErr := sender.SendHeartbeat(context.Background(), snap, 1)
	if !errors.Is(sendErr, ErrCrossOriginRedirect) {
		t.Fatalf("expected ErrCrossOriginRedirect, got %v", sendErr)
	}
	if evilHits.Load() != 0 {
		t.Fatalf("credentials leaked across redirect: evilHits=%d", evilHits.Load())
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "implicit default https port",
			a:    "https://api.example.com/v1/events",
			b:    "https://API.example.com:443/v1/hb",
			want: true,
		},
		{
			name: "implicit default http port",
			a:    "http://10.0.0.5:80/events",
			b:    "http://10.0.0.5/hb",
			want: true,
		},
		{
			name: "scheme downgrade forbidden",
			a:    "https://api.example.com/events",
			b:    "http://api.example.com/hb",
			want: false,
		},
		{
			name: "different port forbidden",
			a:    "https://api.example.com:8443/events",
			b:    "https://api.example.com/hb",
			want: false,
		},
		{
			name: "different host forbidden",
			a:    "https://api.example.com/events",
			b:    "https://evil.example.org/hb",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameOrigin(tc.a, tc.b); got != tc.want {
				t.Fatalf(
					"sameOrigin(%q, %q) = %v, want %v",
					tc.a,
					tc.b,
					got,
					tc.want,
				)
			}
		})
	}
}

func TestWebhookSenderSendBudgets(t *testing.T) {
	tests := []struct {
		name      string
		timeout   time.Duration
		wantEvent time.Duration
		wantHB    time.Duration
	}{
		{
			name:      "default 5s timeout",
			timeout:   0,
			wantEvent: 17 * time.Second,
			wantHB:    10 * time.Second,
		},
		{
			name:      "15s timeout exceeds legacy 10s cap",
			timeout:   15 * time.Second,
			wantEvent: 47 * time.Second,
			wantHB:    16 * time.Second,
		},
		{
			name:      "30s max timeout",
			timeout:   30 * time.Second,
			wantEvent: 92 * time.Second,
			wantHB:    31 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sender, err := NewWebhookSender(WebhookSenderOptions{
				ID:                "dest_budget",
				ImageMode:         ImageModeURL,
				SkipImageURLCheck: true,
				URL:               "https://example.com/events",
				Timeout:           tc.timeout,
			})
			if err != nil {
				t.Fatalf("NewWebhookSender: %v", err)
			}
			defer func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Errorf("Close: %v", closeErr)
				}
			}()

			if got := sender.EventSendBudget(); got != tc.wantEvent {
				t.Errorf("EventSendBudget() = %v, want %v", got, tc.wantEvent)
			}
			if got := sender.HeartbeatSendBudget(); got != tc.wantHB {
				t.Errorf("HeartbeatSendBudget() = %v, want %v", got, tc.wantHB)
			}
		})
	}
}

func TestWebhookSenderCrossOriginRedirectDoesNotRetry(t *testing.T) {
	var originHits atomic.Int32
	evilServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer evilServer.Close()

	originServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originHits.Add(1)
		http.Redirect(w, r, evilServer.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer originServer.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_cross_no_retry",
		ImageMode:     ImageModeURL,
		PublicBaseURL: "http://10.0.0.12:8000",
		URL:           originServer.URL + "/events",
		AuthType:      AuthTypeBearer,
		Token:         "secret-tok",
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	err = sender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{ID: "01XORIGIN", CapturedAt: time.Now().UTC()},
	})
	if !errors.Is(err, ErrCrossOriginRedirect) {
		t.Fatalf("expected ErrCrossOriginRedirect, got %v", err)
	}
	if originHits.Load() != 1 {
		t.Fatalf("cross-origin redirect must not retry: got %d attempts, want 1", originHits.Load())
	}
}

func TestWebhookSenderBlocks302AndPreservesMultipartOn307(t *testing.T) {
	var hits302 atomic.Int32
	var receivedPart []byte

	mux := http.NewServeMux()
	mux.HandleFunc("/downgrade", func(w http.ResponseWriter, r *http.Request) {
		hits302.Add(1)
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/temp-redirect", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f, _, err := r.FormFile("image_scene")
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				t.Errorf("close form file: %v", closeErr)
			}
		}()
		data, err := io.ReadAll(f)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		receivedPart = data
		w.WriteHeader(http.StatusOK)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	// 1. 302 downgrade must be rejected on first attempt without retrying.
	downgradeSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_302",
		ImageMode:     ImageModeMultipart,
		URL:           server.URL + "/downgrade",
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender 302: %v", err)
	}
	defer func() {
		if closeErr := downgradeSender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	err = downgradeSender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{ID: "01DOWNGRADE", CapturedAt: time.Now().UTC()},
	})
	if !errors.Is(err, ErrWebhookRedirectMethodChanged) {
		t.Fatalf("expected ErrWebhookRedirectMethodChanged, got %v", err)
	}
	if hits302.Load() != 1 {
		t.Fatalf("302 method downgrade must not retry: got %d hits, want 1", hits302.Load())
	}

	// 2. Same-origin 307 must replay multipart body cleanly via req.GetBody.
	sceneKey := "snapshots/cam_01/2026-11-24/redir.jpg"
	redirSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_307",
		ImageMode:     ImageModeMultipart,
		URL:           server.URL + "/temp-redirect",
		Media:         &fakeMediaService{files: map[string][]byte{sceneKey: []byte("jpeg-307-bytes")}},
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender 307: %v", err)
	}
	defer func() {
		if closeErr := redirSender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	err = redirSender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{
			ID: "01TEMP307", CameraID: "cam_01", CapturedAt: time.Now().UTC(),
			ImagePath: sceneKey, ImageReady: true,
		},
	})
	if err != nil {
		t.Fatalf("307 same-origin multipart redirect failed: %v", err)
	}
	if string(receivedPart) != "jpeg-307-bytes" {
		t.Fatalf("received multipart image = %q, want jpeg-307-bytes", string(receivedPart))
	}
}

func TestWebhookSenderMultipartSizeCapFallsBackToURL(t *testing.T) {
	var payloadJSON []byte
	var hasScenePart bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payloadJSON = []byte(r.FormValue("payload"))
		_, _, fileErr := r.FormFile("image_scene")
		hasScenePart = fileErr == nil
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	sceneKey := "snapshots/cam_01/2026-11-25/huge.jpg"
	cropKey := "snapshots/cam_01/2026-11-25/small_crop.jpg"
	media := &fakeMediaService{
		files: map[string][]byte{
			sceneKey: bytes.Repeat([]byte{0xAA}, 2048),
			cropKey:  []byte("small-crop"),
		},
	}

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                     "dest_cap",
		ImageMode:              ImageModeMultipart,
		PublicBaseURL:          "http://10.0.0.12:8000",
		SignedURLTTL:           time.Hour,
		Signer:                 signer,
		Media:                  media,
		URL:                    server.URL,
		MaxMultipartImageBytes: 1024,
		AllowLoopback:          true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	err = sender.Send(context.Background(), Delivery{
		Event: record.AlarmEvent{
			ID: "01CAPTEST", CameraID: "cam_01", CapturedAt: time.Now().UTC(),
			ImagePath: sceneKey, ImageReady: true, CropPath: cropKey, CropReady: true,
		},
	})
	if err != nil {
		t.Fatalf("Send failed: %v", err)
	}
	if hasScenePart {
		t.Fatal("oversized scene image must not be attached as multipart binary")
	}

	var payload EventPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Images == nil || payload.Images.Scene == nil || payload.Images.Scene.URL == "" || payload.Images.Scene.Part != "" {
		t.Fatalf("expected oversized scene to fall back to signed URL, got %+v", payload.Images)
	}
	if payload.Images.Object == nil || payload.Images.Object.Part != "image_object" {
		t.Fatalf("expected small crop to remain multipart, got %+v", payload.Images.Object)
	}
}

func TestWebhookSenderTLSSkipVerify(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ev := record.AlarmEvent{ID: "01TLSTEST", CameraID: "cam_1", CapturedAt: time.Now().UTC()}

	strictSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_tls_strict",
		ImageMode:     ImageModeMultipart,
		URL:           server.URL,
		Timeout:       2 * time.Second,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender strict: %v", err)
	}
	defer func() {
		if closeErr := strictSender.Close(); closeErr != nil {
			t.Errorf("Close strict: %v", closeErr)
		}
	}()

	if sendErr := strictSender.Send(context.Background(), Delivery{Event: ev}); sendErr == nil {
		t.Fatal("expected TLS verification error against self-signed server when TLSSkipVerify=false")
	} else if reason := classifyError(sendErr); reason != "tls" {
		t.Fatalf("classifyError(%v) = %q, want tls", sendErr, reason)
	}

	insecureSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_tls_skip",
		ImageMode:     ImageModeMultipart,
		URL:           server.URL,
		Timeout:       2 * time.Second,
		TLSSkipVerify: true,
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender skip verify: %v", err)
	}
	defer func() {
		if closeErr := insecureSender.Close(); closeErr != nil {
			t.Errorf("Close skip verify: %v", closeErr)
		}
	}()

	if sendErr := insecureSender.Send(context.Background(), Delivery{Event: ev}); sendErr != nil {
		t.Fatalf("expected self-signed webhook delivery to succeed with TLSSkipVerify=true, got %v", sendErr)
	}
}

func TestWebhookSenderOutboundHMACSignatureJSONHeartbeatAndMultipart(t *testing.T) {
	const signingSecret = "whsec_test_secret_12345"

	type capturedReq struct {
		body []byte
		ts   string
		sig  string
	}
	var last capturedReq
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		last = capturedReq{
			body: raw,
			ts:   r.Header.Get(headerZhulongTimestamp),
			sig:  r.Header.Get(headerZhulongSignature),
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	verifySig := func(t *testing.T, label string, c capturedReq) {
		t.Helper()
		if _, err := strconv.ParseInt(c.ts, 10, 64); err != nil {
			t.Fatalf("%s: invalid X-Zhulong-Timestamp %q: %v", label, c.ts, err)
		}
		mac := hmac.New(sha256.New, []byte(signingSecret))
		mac.Write([]byte(c.ts + "."))
		mac.Write(c.body)
		want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		if c.sig != want {
			t.Fatalf("%s: signature = %q, want %q", label, c.sig, want)
		}
	}

	// 1. JSON event + Heartbeat.
	jsonSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                "dest_hmac_json",
		ImageMode:         ImageModeURL,
		SkipImageURLCheck: true,
		URL:               server.URL + "/events",
		SigningSecret:     signingSecret,
		Headers: map[string]string{
			"X-Zhulong-Signature": "sha256=spoofed",
			"X-Zhulong-Timestamp": "0",
		},
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender json: %v", err)
	}
	defer func() {
		if closeErr := jsonSender.Close(); closeErr != nil {
			t.Errorf("Close json: %v", closeErr)
		}
	}()

	ev := record.AlarmEvent{ID: "01HMACJSON", CameraID: "cam_1", CapturedAt: time.Now().UTC()}
	if err := jsonSender.Send(context.Background(), Delivery{Event: ev, Seq: 1}); err != nil {
		t.Fatalf("Send json: %v", err)
	}
	verifySig(t, "json-event", last)

	if err := jsonSender.SendHeartbeat(context.Background(), HeartbeatSnapshot{Status: "ready"}, 2); err != nil {
		t.Fatalf("SendHeartbeat: %v", err)
	}
	verifySig(t, "heartbeat", last)

	// 2. Multipart event (streaming incremental HMAC matches wire bytes exactly).
	sceneKey := "snapshots/cam_1/2026-11-25/hmac_scene.jpg"
	mpSender, err := NewWebhookSender(WebhookSenderOptions{
		ID:            "dest_hmac_mp",
		ImageMode:     ImageModeMultipart,
		URL:           server.URL + "/mp",
		SigningSecret: signingSecret,
		Media:         &fakeMediaService{files: map[string][]byte{sceneKey: []byte("fake-jpeg-binary-data")}},
		AllowLoopback: true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender multipart: %v", err)
	}
	defer func() {
		if closeErr := mpSender.Close(); closeErr != nil {
			t.Errorf("Close mp: %v", closeErr)
		}
	}()

	evMP := record.AlarmEvent{
		ID: "01HMACMP", CameraID: "cam_1", CapturedAt: time.Now().UTC(),
		ImagePath: sceneKey, ImageReady: true,
	}
	if err := mpSender.Send(context.Background(), Delivery{Event: evMP, Seq: 3}); err != nil {
		t.Fatalf("Send multipart: %v", err)
	}
	verifySig(t, "multipart-event", last)
}

func TestWebhookSenderMultipartTotalBudgetFallbackPreservesSizeAndTransportDefaults(t *testing.T) {
	var payloadJSON []byte
	var hasScenePart, hasObjectPart bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(4 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		payloadJSON = []byte(r.FormValue("payload"))
		_, _, errScene := r.FormFile("image_scene")
		hasScenePart = errScene == nil
		_, _, errObj := r.FormFile("image_object")
		hasObjectPart = errObj == nil
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	const (
		sceneKey = "snapshots/cam_1/2026-11-25/s.jpg"
		cropKey  = "snapshots/cam_1/2026-11-25/c.jpg"
		refKey   = "faces/empty.jpg"
	)
	media := &fakeMediaService{
		files: map[string][]byte{
			sceneKey: bytes.Repeat([]byte{0x11}, 900),
			cropKey:  bytes.Repeat([]byte{0x22}, 500),
			refKey:   {}, // 0-byte corrupted image must be omitted
		},
	}

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID:                     "dest_total_cap",
		ImageMode:              ImageModeMultipart,
		PublicBaseURL:          "http://10.0.0.12:8000",
		SignedURLTTL:           time.Hour,
		Signer:                 signer,
		Media:                  media,
		URL:                    server.URL,
		MaxMultipartImageBytes: 1000,
		MaxMultipartTotalBytes: 1000, // Scene (900B) fits, crop (500B) exceeds remaining 100B -> falls back to URL with SizeBytes=500.
		AllowLoopback:          true,
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("Close: %v", closeErr)
		}
	}()

	// Verify transport inherits http.DefaultTransport defaults (Proxy, TLSHandshakeTimeout, HTTP/2).
	tr, ok := sender.client.Transport.(*http.Transport)
	if !ok || tr == nil {
		t.Fatalf("expected *http.Transport, got %T", sender.client.Transport)
	}
	if tr.Proxy == nil || tr.TLSHandshakeTimeout == 0 || !tr.ForceAttemptHTTP2 {
		t.Fatalf("transport missing DefaultTransport properties: proxy=%v tlsHandshake=%v h2=%v",
			tr.Proxy != nil, tr.TLSHandshakeTimeout, tr.ForceAttemptHTTP2)
	}

	ev := record.AlarmEvent{
		ID:         "01TOTCAP",
		EventType:  record.EventTypeFaceRecognition,
		CameraID:   "cam_1",
		CapturedAt: time.Now().UTC(),
		ImagePath:  sceneKey,
		ImageReady: true,
		CropPath:   cropKey,
		CropReady:  true,
		Face:       &record.FaceEventData{Matched: true, GalleryImagePath: refKey},
	}
	if err := sender.Send(context.Background(), Delivery{Event: ev, Seq: 1}); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !hasScenePart || hasObjectPart {
		t.Fatalf("parts: scene=%v object=%v, want scene=true object=false", hasScenePart, hasObjectPart)
	}

	var payload EventPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Images == nil || payload.Images.Object == nil || payload.Images.Object.URL == "" {
		t.Fatalf("expected crop to fall back to URL, got %+v", payload.Images)
	}
	if payload.Images.Object.SizeBytes == nil || *payload.Images.Object.SizeBytes != 500 {
		t.Fatalf("expected fallback crop to preserve SizeBytes=500, got %v", payload.Images.Object.SizeBytes)
	}
	if payload.Images.Reference != nil {
		t.Fatalf("0-byte empty reference image must be omitted, got %+v", payload.Images.Reference)
	}
}
