package alertpush

import (
	"encoding/json"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

var payloadTimePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

var envelopeKeys = []string{
	"schema_version", "id", "seq", "event_type", "device_id",
	"source", "captured_at", "sent_at", "images",
}

func TestExternalEventType(t *testing.T) {
	tests := []struct {
		in   record.EventType
		want string
	}{
		{in: record.EventTypeAlarm, want: EventTypeAlarm},
		{in: record.EventTypeFaceRecognition, want: EventTypeFace},
		{in: record.EventTypePlateRecognition, want: EventTypeVehicle},
		{in: "", want: EventTypeAlarm},
		{in: "unknown_kind", want: EventTypeAlarm},
	}
	for _, tt := range tests {
		if got := externalEventType(tt.in); got != tt.want {
			t.Errorf("externalEventType(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestFormatPayloadTime(t *testing.T) {
	loc := time.FixedZone("UTC+8", 8*3600)
	tests := []struct {
		in   time.Time
		want string
	}{
		{in: time.Date(2026, 11, 24, 11, 22, 46, 123456789, loc), want: "2026-11-24T03:22:46.123Z"},
		{in: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), want: "2026-01-02T03:04:05.000Z"},
	}
	for _, tt := range tests {
		got := formatPayloadTime(tt.in)
		if got != tt.want {
			t.Errorf("formatPayloadTime(%v) = %q, want %q", tt.in, got, tt.want)
		}
		if !payloadTimePattern.MatchString(got) {
			t.Errorf("formatPayloadTime(%v) = %q does not match fixed millisecond layout", tt.in, got)
		}
	}
}

// decodeKeys marshals v and returns its top-level JSON object.
func decodeKeys(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return out
}

func assertExactKeys(t *testing.T, got map[string]json.RawMessage, subObject string) {
	t.Helper()
	want := append(slices.Clone(envelopeKeys), subObject)
	slices.Sort(want)
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, want) {
		t.Fatalf("payload keys = %v, want %v", keys, want)
	}
}

func assertNull(t *testing.T, obj map[string]json.RawMessage, key string) {
	t.Helper()
	if string(obj[key]) != "null" {
		t.Fatalf("%s = %s, want null", key, obj[key])
	}
}

func TestBuildEventPayload(t *testing.T) {
	captured := time.Date(2026, 11, 24, 3, 22, 46, 123000000, time.UTC)
	sentAt := captured.Add(750 * time.Millisecond)
	cameraName := "东门入口"
	algoName := "安全帽检测"
	algoVersion := "1.1.0"
	resolved := SourceInfo{
		Camera:    &CameraSourceInfo{ID: "ignored", Name: &cameraName},
		Algorithm: &AlgorithmSourceInfo{ID: "ignored", Name: &algoName, Version: &algoVersion},
	}

	tests := []struct {
		name      string
		delivery  Delivery
		deviceID  string
		subObject string
		check     func(t *testing.T, p EventPayload, obj map[string]json.RawMessage)
	}{
		{
			name: "alarm with nested data and resolved source",
			delivery: Delivery{
				Seq:    7,
				Source: resolved,
				Event: record.AlarmEvent{
					ID: "01ALARM", EventType: record.EventTypeAlarm, CameraID: "cam_1",
					AlgorithmID: "ppe_helmet", CapturedAt: captured,
					Alarm: &record.AlarmEventData{
						AlarmType: "no_helmet", Score: 0.94,
						Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Extra: map[string]any{"zone": "a"},
					},
				},
			},
			deviceID:  "site-a",
			subObject: "alarm",
			check: func(t *testing.T, p EventPayload, obj map[string]json.RawMessage) {
				if p.SchemaVersion != 1 || p.ID != "01ALARM" || *p.Seq != 7 || p.EventType != "alarm" {
					t.Fatalf("envelope = %+v", p)
				}
				if *p.DeviceID != "site-a" || *p.CapturedAt != "2026-11-24T03:22:46.123Z" ||
					*p.SentAt != "2026-11-24T03:22:46.873Z" {
					t.Fatalf("envelope strings = %v %v %v", *p.DeviceID, *p.CapturedAt, *p.SentAt)
				}
				if p.Source.Camera.ID != "cam_1" || *p.Source.Camera.Name != cameraName {
					t.Fatalf("camera = %+v", p.Source.Camera)
				}
				if p.Source.Algorithm.ID != "ppe_helmet" || *p.Source.Algorithm.Version != "1.1.0" {
					t.Fatalf("algorithm = %+v", p.Source.Algorithm)
				}
				if *p.Alarm.AlarmType != "no_helmet" || *p.Alarm.Box != [4]float64{0.1, 0.2, 0.3, 0.4} ||
					p.Alarm.Attributes["zone"] != "a" {
					t.Fatalf("alarm = %+v", p.Alarm)
				}
				if string(obj["images"]) != "[]" {
					t.Fatalf("images = %s, want []", obj["images"])
				}
				alarm := map[string]json.RawMessage{}
				if err := json.Unmarshal(obj["alarm"], &alarm); err != nil {
					t.Fatal(err)
				}
				if _, ok := alarm["extra"]; ok {
					t.Fatal("alarm must not expose an extra key")
				}
			},
		},
		{
			name: "legacy flat alarm with zero box and blank device",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01LEGACY", CameraID: "cam_2", CapturedAt: captured,
				AlarmType: "intrusion", Score: 0.5,
			}},
			subObject: "alarm",
			check: func(t *testing.T, p EventPayload, obj map[string]json.RawMessage) {
				assertNull(t, obj, "device_id")
				if *p.Alarm.AlarmType != "intrusion" || p.Alarm.Box != nil || p.Alarm.Attributes == nil {
					t.Fatalf("alarm = %+v", p.Alarm)
				}
				if p.Source.Camera.Name != nil || p.Source.Algorithm != nil {
					t.Fatalf("source = %+v, want unresolved camera and null algorithm", p.Source)
				}
			},
		},
		{
			name: "face matched",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01FACE", EventType: record.EventTypeFaceRecognition, CameraID: "cam_1",
				AlgorithmID: "face_recognition", CapturedAt: captured,
				Face: &record.FaceEventData{
					Matched: true, PersonName: "张三", PersonID: "EMP_10086", Similarity: 0.94,
					Box: [4]float64{0.3, 0.2, 0.4, 0.35},
				},
			}},
			subObject: "face",
			check: func(t *testing.T, p EventPayload, _ map[string]json.RawMessage) {
				if p.EventType != "face" || *p.Face.Person.ID != "EMP_10086" || *p.Face.Person.Name != "张三" ||
					*p.Face.Similarity != 0.94 {
					t.Fatalf("face = %+v", p.Face)
				}
				if p.Source.Algorithm == nil || p.Source.Algorithm.Name != nil {
					t.Fatalf("algorithm = %+v, want id with null name", p.Source.Algorithm)
				}
			},
		},
		{
			name: "face unmatched",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01STRANGER", EventType: record.EventTypeFaceRecognition, CameraID: "cam_1",
				CapturedAt: captured,
				Face:       &record.FaceEventData{Matched: false, Similarity: 0.41, PersonID: ""},
			}},
			subObject: "face",
			check: func(t *testing.T, _ EventPayload, obj map[string]json.RawMessage) {
				face := map[string]json.RawMessage{}
				if err := json.Unmarshal(obj["face"], &face); err != nil {
					t.Fatal(err)
				}
				assertNull(t, face, "person")
				assertNull(t, face, "similarity")
				assertNull(t, face, "box")
				if string(face["attributes"]) != "{}" {
					t.Fatalf("attributes = %s, want {}", face["attributes"])
				}
			},
		},
		{
			name: "vehicle with plate strips promoted attribute keys",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01CAR", EventType: record.EventTypePlateRecognition, CameraID: "cam_3",
				CapturedAt: captured,
				Vehicle: &record.VehicleEventData{
					PlateNumber: "粤BD12345", PlateColor: "green", VehicleType: "suv", VehicleColor: "",
					Score: 0.985, Box: [4]float64{0.2, 0.4, 0.7, 0.85},
					Attributes: map[string]any{
						"plate_number": "粤BD12345", "plate_color": "green", "vehicle_type": "suv",
						"vehicle_color": "", "plate_type": 5,
					},
				},
			}},
			subObject: "vehicle",
			check: func(t *testing.T, p EventPayload, _ map[string]json.RawMessage) {
				if p.Vehicle.Plate == nil || p.Vehicle.Plate.Number != "粤BD12345" || *p.Vehicle.Plate.Color != "green" {
					t.Fatalf("plate = %+v", p.Vehicle.Plate)
				}
				if *p.Vehicle.VehicleType != "suv" || p.Vehicle.VehicleColor != nil {
					t.Fatalf("vehicle = %+v", p.Vehicle)
				}
				if len(p.Vehicle.Attributes) != 1 || p.Vehicle.Attributes["plate_type"] != 5 {
					t.Fatalf("attributes = %v, want only plate_type", p.Vehicle.Attributes)
				}
			},
		},
		{
			name: "vehicle without plate",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01CAR2", EventType: record.EventTypePlateRecognition, CameraID: "cam_3",
				CapturedAt: captured, Vehicle: &record.VehicleEventData{VehicleType: "truck"},
			}},
			subObject: "vehicle",
			check: func(t *testing.T, p EventPayload, _ map[string]json.RawMessage) {
				if p.Vehicle.Plate != nil || p.Vehicle.Attributes == nil {
					t.Fatalf("vehicle = %+v, want null plate and empty attributes", p.Vehicle)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildEventPayload(tt.delivery, tt.deviceID, sentAt)
			obj := decodeKeys(t, p)
			assertExactKeys(t, obj, tt.subObject)
			for _, key := range []string{"captured_at", "sent_at"} {
				var s string
				if err := json.Unmarshal(obj[key], &s); err != nil || !payloadTimePattern.MatchString(s) {
					t.Fatalf("%s = %s, want fixed millisecond UTC", key, obj[key])
				}
			}
			tt.check(t, p, obj)
		})
	}
}

func TestBuildEventPayload_DoesNotMutateSharedAttributes(t *testing.T) {
	attrs := map[string]any{"plate_number": "A1", "plate_color": "blue", "plate_type": 1}
	ev := record.AlarmEvent{
		ID: "01SHARED", EventType: record.EventTypePlateRecognition, CameraID: "cam_1",
		Vehicle: &record.VehicleEventData{PlateNumber: "A1", Attributes: attrs},
	}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := buildEventPayload(Delivery{Event: ev}, "dev", time.Now())
			if _, err := json.Marshal(p); err != nil {
				t.Errorf("marshal: %v", err)
			}
		}()
	}
	wg.Wait()

	for _, key := range []string{"plate_number", "plate_color", "plate_type"} {
		if _, ok := attrs[key]; !ok {
			t.Fatalf("original attributes lost %q: %v", key, attrs)
		}
	}
}

func TestBuildHeartbeatEnvelope(t *testing.T) {
	sentAt := time.Date(2026, 11, 24, 3, 23, 0, 4000000, time.UTC)
	snap := HeartbeatSnapshot{
		Status:  "degraded",
		Reasons: []string{"camera_offline"},
		Host:    HeartbeatHostPayload{Arch: "arm64", UptimeSeconds: 10},
	}
	p := buildHeartbeatEnvelope(snap, "site-a", 42, sentAt)
	obj := decodeKeys(t, p)
	assertExactKeys(t, obj, "heartbeat")
	assertNull(t, obj, "source")
	assertNull(t, obj, "captured_at")
	if string(obj["images"]) != "[]" {
		t.Fatalf("images = %s, want []", obj["images"])
	}
	if p.EventType != EventTypeHeartbeat || *p.Seq != 42 || *p.SentAt != "2026-11-24T03:23:00.004Z" {
		t.Fatalf("envelope = %+v", p)
	}
	if p.Heartbeat.Status != "degraded" || p.Heartbeat.Host.Arch != "arm64" || p.ID == "" {
		t.Fatalf("heartbeat = %+v", p.Heartbeat)
	}

	defaults := buildHeartbeatEnvelope(HeartbeatSnapshot{}, "", 1, sentAt)
	if defaults.Heartbeat.Status != "ready" || defaults.Heartbeat.Reasons == nil || defaults.DeviceID != nil {
		t.Fatalf("defaults = %+v", defaults)
	}
}

func TestBuildOfflineEnvelope(t *testing.T) {
	will := buildOfflineEnvelope(OfflineReasonConnectionLost, "site-a", nil, nil)
	obj := decodeKeys(t, will)
	assertExactKeys(t, obj, "offline")
	assertNull(t, obj, "seq")
	assertNull(t, obj, "sent_at")
	assertNull(t, obj, "source")
	assertNull(t, obj, "captured_at")
	if will.Offline.Reason != OfflineReasonConnectionLost || will.ID == "" {
		t.Fatalf("will = %+v", will)
	}

	seq := uint64(9)
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	graceful := buildOfflineEnvelope(OfflineReasonGracefulShutdown, "site-a", &seq, &now)
	if graceful.Seq == nil || *graceful.Seq != 9 || graceful.SentAt == nil ||
		*graceful.SentAt != "2026-11-24T04:00:00.000Z" {
		t.Fatalf("graceful = %+v", graceful)
	}
}
