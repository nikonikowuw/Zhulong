package alertpush

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestGalleryReferenceImageDeliveryAcrossModes(t *testing.T) {
	t.Parallel()
	const (
		scenePath = "snapshots/cam_01/2026-11-24/evt.jpg"
		cropPath  = "snapshots/cam_01/2026-11-24/evt_crop.jpg"
		// Production person_faces.image_path stores a bare filename; normalized to faces/<file>.
		bareRefName = "person_alice_face_01.png"
		normRefKey  = "faces/person_alice_face_01.png"
	)

	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}

	sceneBytes := bytes.Repeat([]byte{0x11}, 64)
	cropBytes := bytes.Repeat([]byte{0x22}, 32)
	refPNGBytes := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x01, 0x02}

	snapshotReader := &countingMediaReader{
		files: map[string][]byte{
			scenePath: sceneBytes,
			cropPath:  cropBytes,
		},
	}
	galleryReader := &countingMediaReader{
		files: map[string][]byte{
			normRefKey: refPNGBytes,
		},
	}

	faceEvent := record.AlarmEvent{
		ID:         "01FACEREF01",
		EventType:  record.EventTypeFaceRecognition,
		CameraID:   "cam_01",
		CapturedAt: time.Now().UTC(),
		ImagePath:  scenePath,
		ImageReady: true,
		CropPath:   cropPath,
		CropReady:  true,
		Face: &record.FaceEventData{
			Matched:          true,
			PersonID:         "EMP_001",
			PersonName:       "Alice",
			Similarity:       0.92,
			GalleryImagePath: bareRefName,
		},
	}

	t.Run("url mode signs normalized faces/ key with png mime and size", func(t *testing.T) {
		images := resolveImageURLs(
			context.Background(),
			imageURLParams{
				PublicBaseURL: "https://edge.local:8000",
				Signer:        signer,
				Media:         snapshotReader,
				GalleryMedia:  galleryReader,
				TTL:           time.Hour,
			},
			faceEvent,
		)
		if len(images) != 3 {
			t.Fatalf("images = %+v, want 3 entries", images)
		}
		ref := images[2]
		if ref.Role != ImageRoleReference || ref.MIME != imageMIMEPNG {
			t.Fatalf("ref = %+v, want role=reference mime=image/png", ref)
		}
		if ref.SizeBytes == nil || *ref.SizeBytes != int64(len(refPNGBytes)) {
			t.Fatalf("ref size = %v, want %d", ref.SizeBytes, len(refPNGBytes))
		}
		if ref.URL == nil || !strings.Contains(*ref.URL, "/api/v1/media/signed/"+normRefKey+"?") {
			t.Fatalf("ref url = %v, want signed faces/ path", ref.URL)
		}

		// Verify the minted URL streams back from SignedMediaHandler (GET and HEAD).
		gin.SetMode(gin.TestMode)
		router := gin.New()
		api := router.Group("/api/v1")
		h := NewSignedMediaHandlerWithGallery(signer, nil, galleryReader, zap.NewNop())
		h.RegisterRoutes(api)

		parsedURL, parseErr := url.Parse(*ref.URL)
		if parseErr != nil {
			t.Fatalf("parse signed url: %v", parseErr)
		}

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, parsedURL.RequestURI(), nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("GET signed face status = %d: %s", w.Code, w.Body.String())
		}
		if ct := w.Header().Get("Content-Type"); ct != imageMIMEPNG {
			t.Fatalf("GET Content-Type = %q, want %q", ct, imageMIMEPNG)
		}
		if !bytes.Equal(w.Body.Bytes(), refPNGBytes) {
			t.Fatalf("GET body mismatch: got %x, want %x", w.Body.Bytes(), refPNGBytes)
		}

		wHead := httptest.NewRecorder()
		reqHead := httptest.NewRequest(http.MethodHead, parsedURL.RequestURI(), nil)
		router.ServeHTTP(wHead, reqHead)
		if wHead.Code != http.StatusOK || wHead.Body.Len() != 0 {
			t.Fatalf("HEAD signed face status=%d bodyLen=%d", wHead.Code, wHead.Body.Len())
		}
	})

	t.Run("multipart mode reads reference via gallery reader with png filename and mime", func(t *testing.T) {
		capture := &multipartCapture{}
		server := newMultipartServer(t, capture)
		defer server.Close()

		sender, senderErr := NewWebhookSender(WebhookSenderOptions{
			ID:           "dest_mp_ref",
			ImageMode:    ImageModeMultipart,
			URL:          server.URL,
			Signer:       signer,
			SignedURLTTL: time.Hour,
			Media:        snapshotReader,
			GalleryMedia: galleryReader,
		})
		if senderErr != nil {
			t.Fatalf("NewWebhookSender: %v", senderErr)
		}
		defer func() {
			if closeErr := sender.Close(); closeErr != nil {
				t.Errorf("close sender: %v", closeErr)
			}
		}()

		if err := sender.Send(context.Background(), Delivery{Event: faceEvent, Seq: 9}); err != nil {
			t.Fatalf("Send: %v", err)
		}

		capture.mu.Lock()
		defer capture.mu.Unlock()
		if !bytes.Equal(capture.parts["image_reference"], refPNGBytes) {
			t.Fatalf("image_reference bytes mismatch")
		}
		if capture.files["image_reference"] != "reference.png" {
			t.Fatalf("image_reference filename = %q, want reference.png", capture.files["image_reference"])
		}
		var payload EventPayload
		if err := json.Unmarshal(capture.payload, &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if len(payload.Images) != 3 || payload.Images[2].MIME != imageMIMEPNG {
			t.Fatalf("payload images = %+v", payload.Images)
		}
	})

	t.Run("mqtt base64 mode inlines reference from gallery reader with png mime", func(t *testing.T) {
		sender, senderErr := NewMQTTSender(MQTTSenderOptions{
			ID:             "mqtt_ref",
			ImageMode:      ImageModeBase64,
			Broker:         "tcp://127.0.0.1:1",
			ClientID:       "mqtt-ref-test",
			Topic:          "zhulong/events",
			PublicBaseURL:  "https://edge.local:8000",
			Signer:         signer,
			SignedURLTTL:   time.Hour,
			Media:          snapshotReader,
			GalleryMedia:   galleryReader,
			InlineMaxBytes: 1024,
		})
		if senderErr != nil {
			t.Fatalf("NewMQTTSender: %v", senderErr)
		}

		images := sender.inlineImages(context.Background(), faceEvent)
		if len(images) != 3 {
			t.Fatalf("images = %+v, want 3", images)
		}
		ref := images[2]
		if ref.Role != ImageRoleReference || ref.MIME != imageMIMEPNG || ref.Base64 == nil {
			t.Fatalf("ref = %+v, want inlined png reference", ref)
		}
		decoded, decErr := base64.StdEncoding.DecodeString(*ref.Base64)
		if decErr != nil || !bytes.Equal(decoded, refPNGBytes) {
			t.Fatalf("decoded reference mismatch: err=%v", decErr)
		}
	})

	t.Run("invalid or escaping gallery path is silently ignored", func(t *testing.T) {
		for _, bad := range []string{"../etc/passwd", "recordings/cam/a.mp4", "faces/sub/a.jpg", "/abs/path.jpg"} {
			badEvent := faceEvent
			badEvent.ImageReady = false
			badEvent.CropReady = false
			badEvent.Face = &record.FaceEventData{Matched: true, GalleryImagePath: bad}
			if got := imageCandidates(badEvent); len(got) != 0 {
				t.Fatalf("bad gallery path %q produced candidates %+v, want empty", bad, got)
			}
		}
	})

	if snapshotReader.opens.Load() != snapshotReader.closes.Load() {
		t.Fatalf("snapshot reader leaked handles: opens=%d closes=%d", snapshotReader.opens.Load(), snapshotReader.closes.Load())
	}
	if galleryReader.opens.Load() != galleryReader.closes.Load() {
		t.Fatalf("gallery reader leaked handles: opens=%d closes=%d", galleryReader.opens.Load(), galleryReader.closes.Load())
	}
}
