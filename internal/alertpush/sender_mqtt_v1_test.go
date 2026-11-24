package alertpush

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestMQTTInlineBudget(t *testing.T) {
	const (
		scenePath = "snapshots/cam_01/2026-11-24/evt.jpg"
		cropPath  = "snapshots/cam_01/2026-11-24/evt_crop.jpg"
	)
	signer, err := NewURLSigner(make([]byte, 32))
	if err != nil {
		t.Fatalf("NewURLSigner: %v", err)
	}
	scene4K := bytes.Repeat([]byte{0x11}, 2*1024*1024)
	crop := bytes.Repeat([]byte{0x22}, 60*1024)
	ev := record.AlarmEvent{
		ID: "01FOURK", CameraID: "cam_01",
		ImagePath: scenePath, ImageReady: true, CropPath: cropPath, CropReady: true,
	}

	type want struct {
		role      string
		inline    bool
		url       bool
		sizeBytes int64
	}
	tests := []struct {
		name          string
		budget        int64
		publicBaseURL string
		files         map[string][]byte
		want          []want
	}{
		{
			name:          "4K scene falls back to url, crop inlined",
			budget:        500 * 1024,
			publicBaseURL: "http://10.0.0.12:8000",
			files:         map[string][]byte{scenePath: scene4K, cropPath: crop},
			want: []want{
				{role: ImageRoleScene, inline: false, url: true, sizeBytes: int64(len(scene4K))},
				{role: ImageRoleObject, inline: true, url: true, sizeBytes: int64(len(crop))},
			},
		},
		{
			name:   "4K scene without public base url is omitted",
			budget: 500 * 1024,
			files:  map[string][]byte{scenePath: scene4K, cropPath: crop},
			want: []want{
				{role: ImageRoleObject, inline: true, url: false, sizeBytes: int64(len(crop))},
			},
		},
		{
			name:   "budget exactly equal to total inlines everything",
			budget: int64(len(crop)) + 100*1024,
			files: map[string][]byte{
				scenePath: bytes.Repeat([]byte{0x33}, 100*1024),
				cropPath:  crop,
			},
			want: []want{
				{role: ImageRoleScene, inline: true, sizeBytes: 100 * 1024},
				{role: ImageRoleObject, inline: true, sizeBytes: int64(len(crop))},
			},
		},
		{
			name:   "crop claims budget before scene",
			budget: 100 * 1024,
			files: map[string][]byte{
				scenePath: bytes.Repeat([]byte{0x44}, 90*1024),
				cropPath:  crop,
			},
			want: []want{
				{role: ImageRoleObject, inline: true, sizeBytes: int64(len(crop))},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			media := &countingMediaReader{files: tt.files}
			sender, err := NewMQTTSender(MQTTSenderOptions{
				ID: "mqtt_budget", ImageMode: ImageModeBase64,
				Broker: "tcp://127.0.0.1:1", ClientID: "budget-test", Topic: "zhulong/events",
				PublicBaseURL: tt.publicBaseURL, Signer: signer, SignedURLTTL: time.Hour,
				Media: media, InlineMaxBytes: tt.budget,
			})
			if err != nil {
				t.Fatalf("NewMQTTSender: %v", err)
			}

			images := sender.inlineImages(context.Background(), ev)
			if media.opens.Load() != media.closes.Load() {
				t.Fatalf("opens=%d closes=%d", media.opens.Load(), media.closes.Load())
			}
			if len(images) != len(tt.want) {
				t.Fatalf("images = %+v, want %d entries", images, len(tt.want))
			}

			var inlined int64
			for i, w := range tt.want {
				img := images[i]
				if img.Role != w.role || (img.Base64 != nil) != w.inline || (img.URL != nil) != w.url {
					t.Fatalf("image[%d] = role %s base64=%v url=%v, want %+v",
						i, img.Role, img.Base64 != nil, img.URL != nil, w)
				}
				if img.SizeBytes == nil || *img.SizeBytes != w.sizeBytes {
					t.Fatalf("image[%d] size = %v, want %d", i, img.SizeBytes, w.sizeBytes)
				}
				if img.Base64 != nil {
					raw, err := base64.StdEncoding.DecodeString(*img.Base64)
					if err != nil {
						t.Fatalf("decode: %v", err)
					}
					inlined += int64(len(raw))
				}
			}
			if inlined > tt.budget {
				t.Fatalf("inlined %d bytes exceeds budget %d", inlined, tt.budget)
			}
		})
	}
}

func TestMQTTDefaultInlineBudget(t *testing.T) {
	sender, err := NewMQTTSender(MQTTSenderOptions{
		Broker: "tcp://127.0.0.1:1", ClientID: "default-budget", Topic: "zhulong/events",
		ImageMode: ImageModeBase64,
	})
	if err != nil {
		t.Fatalf("NewMQTTSender: %v", err)
	}
	if got := sender.inlineBudget(); got != int64(DefaultInlineMaxKB)*1024 {
		t.Fatalf("inlineBudget = %d, want %d", got, DefaultInlineMaxKB*1024)
	}
}

func TestMQTTWillPayloadBytes(t *testing.T) {
	will := buildOfflineEnvelope(OfflineReasonConnectionLost, "box-lwt", nil, nil)
	raw, err := json.Marshal(will)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, fragment := range []string{
		`"schema_version":1`, `"event_type":"offline"`, `"seq":null`, `"sent_at":null`,
		`"offline":{"reason":"connection_lost"}`, `"device_id":"box-lwt"`,
	} {
		if !strings.Contains(string(raw), fragment) {
			t.Fatalf("will payload %s missing %s", raw, fragment)
		}
	}
}
