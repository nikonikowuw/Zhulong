package alertpush

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

// countingMediaReader tracks Open/Close pairs so tests can prove no file handle leaks.
type countingMediaReader struct {
	files  map[string][]byte
	failOn map[string]bool
	opens  atomic.Int32
	closes atomic.Int32
}

type countingFile struct {
	*bytes.Reader
	owner *countingMediaReader
}

func (f countingFile) Close() error {
	f.owner.closes.Add(1)
	return nil
}

func (m *countingMediaReader) OpenContent(
	_ context.Context,
	relPath string,
) (io.ReadSeekCloser, filestore.ObjectInfo, error) {
	if m.failOn[relPath] {
		return nil, filestore.ObjectInfo{}, errors.New("open failed")
	}
	data, ok := m.files[relPath]
	if !ok {
		return nil, filestore.ObjectInfo{}, os.ErrNotExist
	}
	m.opens.Add(1)
	return countingFile{Reader: bytes.NewReader(data), owner: m},
		filestore.ObjectInfo{Key: relPath, SizeBytes: int64(len(data))},
		nil
}

type multipartCapture struct {
	mu      sync.Mutex
	payload []byte
	parts   map[string][]byte
	files   map[string]string
	headers http.Header
}

func newMultipartServer(t *testing.T, capture *multipartCapture) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mediaType, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		capture.mu.Lock()
		defer capture.mu.Unlock()
		capture.headers = r.Header.Clone()
		capture.parts = map[string][]byte{}
		capture.files = map[string]string{}
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
			data, err := io.ReadAll(part)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			if part.FormName() == "payload" {
				capture.payload = data
				continue
			}
			capture.parts[part.FormName()] = data
			capture.files[part.FormName()] = part.FileName()
		}
		w.WriteHeader(http.StatusOK)
	}))
}

func TestWebhookMultipartV1(t *testing.T) {
	const (
		scenePath = "snapshots/cam_01/2026-11-24/evt.jpg"
		cropPath  = "snapshots/cam_01/2026-11-24/evt_crop.jpg"
		refPath   = "snapshots/gallery/p1.jpg"
	)
	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	faceEvent := record.AlarmEvent{
		ID: "01FACE", EventType: record.EventTypeFaceRecognition, CameraID: "cam_01",
		CapturedAt: time.Now().UTC(),
		ImagePath:  scenePath, ImageReady: true, CropPath: cropPath, CropReady: true,
		Face: &record.FaceEventData{Matched: true, PersonID: "EMP_1", GalleryImagePath: refPath},
	}
	scene4K := bytes.Repeat([]byte{0xAB}, 2*1024*1024)

	tests := []struct {
		name          string
		publicBaseURL string
		media         *countingMediaReader
		event         record.AlarmEvent
		wantParts     map[string]int
		wantImages    []string
		check         func(t *testing.T, images []ImageInfo)
	}{
		{
			name: "all images as parts, 4K scene uncapped",
			media: &countingMediaReader{files: map[string][]byte{
				scenePath: scene4K, cropPath: []byte("crop"), refPath: []byte("ref"),
			}},
			event:      faceEvent,
			wantParts:  map[string]int{"image_scene": len(scene4K), "image_object": 4, "image_reference": 3},
			wantImages: []string{ImageRoleScene, ImageRoleObject, ImageRoleReference},
			check: func(t *testing.T, images []ImageInfo) {
				for _, img := range images {
					if img.Part == nil || *img.Part != "image_"+img.Role || img.URL != nil || img.Base64 != nil {
						t.Fatalf("image = %+v", img)
					}
				}
				if *images[0].SizeBytes != int64(len(scene4K)) {
					t.Fatalf("scene size = %d", *images[0].SizeBytes)
				}
			},
		},
		{
			name:          "open failure falls back to url when base url configured",
			publicBaseURL: "http://10.0.0.12:8000",
			media: &countingMediaReader{
				files:  map[string][]byte{scenePath: []byte("scene"), cropPath: []byte("crop")},
				failOn: map[string]bool{cropPath: true},
			},
			event:      record.AlarmEvent{ID: "01A", CameraID: "cam_01", ImagePath: scenePath, ImageReady: true, CropPath: cropPath, CropReady: true},
			wantParts:  map[string]int{"image_scene": 5},
			wantImages: []string{ImageRoleScene, ImageRoleObject},
			check: func(t *testing.T, images []ImageInfo) {
				if images[0].Part == nil || images[0].URL == nil {
					t.Fatalf("scene = %+v, want part and backup url", images[0])
				}
				if images[1].Part != nil || images[1].URL == nil || images[1].SizeBytes != nil {
					t.Fatalf("object = %+v, want url only", images[1])
				}
			},
		},
		{
			name: "open failure without url omits the image",
			media: &countingMediaReader{
				files:  map[string][]byte{scenePath: []byte("scene")},
				failOn: map[string]bool{scenePath: true},
			},
			event:      record.AlarmEvent{ID: "01B", CameraID: "cam_01", ImagePath: scenePath, ImageReady: true},
			wantParts:  map[string]int{},
			wantImages: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capture := &multipartCapture{}
			server := newMultipartServer(t, capture)
			defer server.Close()

			sender, err := NewWebhookSender(WebhookSenderOptions{
				ID: "dest_mp", ImageMode: ImageModeMultipart, URL: server.URL,
				PublicBaseURL: tt.publicBaseURL, Signer: signer, SignedURLTTL: time.Hour,
				Media: tt.media,
			})
			if err != nil {
				t.Fatalf("NewWebhookSender: %v", err)
			}
			defer func() {
				if closeErr := sender.Close(); closeErr != nil {
					t.Errorf("close: %v", closeErr)
				}
			}()

			if err := sender.Send(context.Background(), Delivery{Event: tt.event, Seq: 3}); err != nil {
				t.Fatalf("Send: %v", err)
			}
			if tt.media.opens.Load() != tt.media.closes.Load() {
				t.Fatalf("opens=%d closes=%d, file handles leaked", tt.media.opens.Load(), tt.media.closes.Load())
			}

			capture.mu.Lock()
			defer capture.mu.Unlock()
			if len(capture.parts) != len(tt.wantParts) {
				t.Fatalf("parts = %v, want %v", capture.files, tt.wantParts)
			}
			for name, size := range tt.wantParts {
				if len(capture.parts[name]) != size {
					t.Fatalf("part %s size = %d, want %d", name, len(capture.parts[name]), size)
				}
				if want := strings.TrimPrefix(name, "image_") + ".jpg"; capture.files[name] != want {
					t.Fatalf("part %s filename = %q, want %q", name, capture.files[name], want)
				}
			}

			var payload EventPayload
			if err := json.Unmarshal(capture.payload, &payload); err != nil {
				t.Fatalf("unmarshal payload: %v", err)
			}
			roles := []string{}
			for _, img := range payload.Images {
				roles = append(roles, img.Role)
			}
			if strings.Join(roles, ",") != strings.Join(tt.wantImages, ",") {
				t.Fatalf("roles = %v, want %v", roles, tt.wantImages)
			}
			if capture.headers.Get(headerZhulongDelivery) != payload.ID ||
				capture.headers.Get(headerZhulongEvent) != payload.EventType ||
				capture.headers.Get(headerZhulongSchema) != "1" {
				t.Fatalf("envelope headers = %v", capture.headers)
			}
			if tt.check != nil {
				tt.check(t, payload.Images)
			}
		})
	}
}

func TestWebhookRetryKeepsIDAndSeq(t *testing.T) {
	var mu sync.Mutex
	bodies := []EventPayload{}
	headers := []http.Header{}
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var p EventPayload
		if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		bodies = append(bodies, p)
		headers = append(headers, r.Header.Clone())
		mu.Unlock()
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	sender, err := NewWebhookSender(WebhookSenderOptions{
		ID: "dest_retry", ImageMode: ImageModeURL, PublicBaseURL: "http://10.0.0.12:8000",
		URL: server.URL,
		// A stored custom header with a reserved name must not override envelope headers.
		Headers: map[string]string{"X-Zhulong-Event": "spoofed"},
	})
	if err != nil {
		t.Fatalf("NewWebhookSender: %v", err)
	}
	defer func() {
		if closeErr := sender.Close(); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	ev := record.AlarmEvent{
		ID: "01RETRY", EventType: record.EventTypePlateRecognition, CameraID: "cam_01",
		CapturedAt: time.Now().UTC(), Vehicle: &record.VehicleEventData{PlateNumber: "A1"},
	}
	if err := sender.Send(context.Background(), Delivery{Event: ev, Seq: 41}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 3 {
		t.Fatalf("attempts = %d, want 3", len(bodies))
	}
	for i, p := range bodies {
		if p.ID != "01RETRY" || p.Seq == nil || *p.Seq != 41 || p.EventType != EventTypeVehicle {
			t.Fatalf("attempt %d payload = %+v", i, p)
		}
		if headers[i].Get(headerZhulongEvent) != EventTypeVehicle ||
			headers[i].Get(headerZhulongDelivery) != "01RETRY" {
			t.Fatalf("attempt %d headers = %v", i, headers[i])
		}
	}
}

func TestReservedEnvelopeHeadersRejected(t *testing.T) {
	for _, name := range []string{"X-Zhulong-Event", "x-zhulong-delivery", "X-ZHULONG-SCHEMA"} {
		sec := &webhookSecret{}
		err := applyWebhookSecretOverrides(sec, &AlertDestinationSecretInfo{
			Headers: map[string]string{name: "x"},
		}, true)
		if !errors.Is(err, ErrReservedHeaderUsed) {
			t.Fatalf("header %q: err = %v, want ErrReservedHeaderUsed", name, err)
		}
	}
}
