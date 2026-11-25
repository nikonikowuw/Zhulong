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

func assertKeysEqual(t *testing.T, got map[string]json.RawMessage, want ...string) {
	t.Helper()
	wantSorted := slices.Clone(want)
	slices.Sort(wantSorted)
	keys := make([]string, 0, len(got))
	for k := range got {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, wantSorted) {
		t.Fatalf("payload keys = %v, want %v", keys, wantSorted)
	}
}

func TestBuildEventPayload(t *testing.T) {
	captured := time.Date(2026, 11, 24, 3, 22, 46, 123000000, time.UTC)
	sentAt := captured.Add(750 * time.Millisecond)
	resolved := SourceInfo{
		Camera:    &CameraSourceInfo{ID: "ignored", Name: "东门入口"},
		Algorithm: &AlgorithmSourceInfo{ID: "ignored", Name: "安全帽检测", Version: "1.1.0"},
	}

	tests := []struct {
		name     string
		delivery Delivery
		deviceID string
		wantKeys []string
		check    func(t *testing.T, p EventPayload, obj map[string]json.RawMessage)
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
			deviceID: "site-a",
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type", "device_id",
				"camera", "algorithm", "captured_at", "sent_at", "alarm",
			},
			check: func(t *testing.T, p EventPayload, obj map[string]json.RawMessage) {
				if p.SchemaVersion != 1 || p.ID != "01ALARM" || p.Seq != 7 || p.EventType != "alarm" {
					t.Fatalf("envelope = %+v", p)
				}
				if p.DeviceID != "site-a" || p.CapturedAt != "2026-11-24T03:22:46.123Z" ||
					p.SentAt != "2026-11-24T03:22:46.873Z" {
					t.Fatalf("envelope strings = %q %q %q", p.DeviceID, p.CapturedAt, p.SentAt)
				}
				if p.Camera == nil || p.Camera.ID != "cam_1" || p.Camera.Name != "东门入口" {
					t.Fatalf("camera = %+v", p.Camera)
				}
				if p.Algorithm == nil || p.Algorithm.ID != "ppe_helmet" || p.Algorithm.Version != "1.1.0" {
					t.Fatalf("algorithm = %+v", p.Algorithm)
				}
				if p.Alarm.Type != "no_helmet" || *p.Alarm.Box != [4]float64{0.1, 0.2, 0.3, 0.4} ||
					p.Alarm.Attributes["zone"] != "a" {
					t.Fatalf("alarm = %+v", p.Alarm)
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
			name: "legacy flat alarm omits blank device, zero box, empty attributes, and missing algorithm",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01LEGACY", CameraID: "cam_2", CapturedAt: captured,
				AlarmType: "intrusion", Score: 0.5,
			}},
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type",
				"camera", "captured_at", "sent_at", "alarm",
			},
			check: func(t *testing.T, p EventPayload, obj map[string]json.RawMessage) {
				if p.Alarm.Type != "intrusion" || p.Alarm.Box != nil || p.Alarm.Attributes != nil {
					t.Fatalf("alarm = %+v", p.Alarm)
				}
				if p.Camera == nil || p.Camera.ID != "cam_2" || p.Camera.Name != "" || p.Algorithm != nil {
					t.Fatalf("camera=%+v algorithm=%+v", p.Camera, p.Algorithm)
				}
				alarm := map[string]json.RawMessage{}
				if err := json.Unmarshal(obj["alarm"], &alarm); err != nil {
					t.Fatal(err)
				}
				assertKeysEqual(t, alarm, "type", "score")
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
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type",
				"camera", "algorithm", "captured_at", "sent_at", "face",
			},
			check: func(t *testing.T, p EventPayload, _ map[string]json.RawMessage) {
				if p.EventType != "face" || p.Face.Person == nil ||
					p.Face.Person.ID != "EMP_10086" || p.Face.Person.Name != "张三" ||
					p.Face.Similarity == nil || *p.Face.Similarity != 0.94 {
					t.Fatalf("face = %+v", p.Face)
				}
				if p.Algorithm == nil || p.Algorithm.ID != "face_recognition" || p.Algorithm.Name != "" {
					t.Fatalf("algorithm = %+v", p.Algorithm)
				}
			},
		},
		{
			name: "face unmatched omits person, similarity, box and empty attributes",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01STRANGER", EventType: record.EventTypeFaceRecognition, CameraID: "cam_1",
				CapturedAt: captured,
				Face:       &record.FaceEventData{Matched: false, Similarity: 0.41, PersonID: ""},
			}},
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type",
				"camera", "captured_at", "sent_at", "face",
			},
			check: func(t *testing.T, _ EventPayload, obj map[string]json.RawMessage) {
				face := map[string]json.RawMessage{}
				if err := json.Unmarshal(obj["face"], &face); err != nil {
					t.Fatal(err)
				}
				assertKeysEqual(t, face, "matched")
				if string(face["matched"]) != "false" {
					t.Fatalf("matched = %s, want false", face["matched"])
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
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type",
				"camera", "captured_at", "sent_at", "vehicle",
			},
			check: func(t *testing.T, p EventPayload, _ map[string]json.RawMessage) {
				if p.Vehicle.Plate == nil || p.Vehicle.Plate.Number != "粤BD12345" || p.Vehicle.Plate.Color != "green" {
					t.Fatalf("plate = %+v", p.Vehicle.Plate)
				}
				if p.Vehicle.Type != "suv" || p.Vehicle.Color != "" {
					t.Fatalf("vehicle = %+v", p.Vehicle)
				}
				if len(p.Vehicle.Attributes) != 1 || p.Vehicle.Attributes["plate_type"] != 5 {
					t.Fatalf("attributes = %v, want only plate_type", p.Vehicle.Attributes)
				}
			},
		},
		{
			name: "vehicle without plate omits plate and empty attributes",
			delivery: Delivery{Event: record.AlarmEvent{
				ID: "01CAR2", EventType: record.EventTypePlateRecognition, CameraID: "cam_3",
				CapturedAt: captured, Vehicle: &record.VehicleEventData{VehicleType: "truck"},
			}},
			wantKeys: []string{
				"schema_version", "id", "seq", "event_type",
				"camera", "captured_at", "sent_at", "vehicle",
			},
			check: func(t *testing.T, p EventPayload, obj map[string]json.RawMessage) {
				if p.Vehicle.Plate != nil || p.Vehicle.Attributes != nil {
					t.Fatalf("vehicle = %+v, want nil plate and nil attributes", p.Vehicle)
				}
				veh := map[string]json.RawMessage{}
				if err := json.Unmarshal(obj["vehicle"], &veh); err != nil {
					t.Fatal(err)
				}
				assertKeysEqual(t, veh, "type", "score")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := buildEventPayload(tt.delivery, tt.deviceID, sentAt)
			obj := decodeKeys(t, p)
			assertKeysEqual(t, obj, tt.wantKeys...)
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

func TestBuildHeartbeatPayload(t *testing.T) {
	sentAt := time.Date(2026, 11, 24, 3, 23, 0, 4000000, time.UTC)
	snap := HeartbeatSnapshot{
		Status:  "degraded",
		Reasons: []string{"camera_offline"},
		Host:    HeartbeatHostPayload{Arch: "arm64", UptimeSeconds: 10},
	}
	p := buildHeartbeatPayload(snap, "site-a", 42, sentAt)
	obj := decodeKeys(t, p)
	assertKeysEqual(
		t,
		obj,
		"schema_version", "id", "seq", "event_type", "device_id", "sent_at",
		"status", "reasons", "host", "resources", "storage", "workload",
	)
	if p.EventType != EventTypeHeartbeat || p.Seq != 42 || p.SentAt != "2026-11-24T03:23:00.004Z" {
		t.Fatalf("heartbeat = %+v", p)
	}
	if p.Status != "degraded" || p.Host.Arch != "arm64" || p.ID == "" {
		t.Fatalf("heartbeat = %+v", p)
	}

	defaults := buildHeartbeatPayload(HeartbeatSnapshot{}, "", 1, sentAt)
	defaultObj := decodeKeys(t, defaults)
	assertKeysEqual(
		t,
		defaultObj,
		"schema_version", "id", "seq", "event_type", "sent_at",
		"status", "host", "resources", "storage", "workload",
	)
	if defaults.Status != "ready" || defaults.DeviceID != "" {
		t.Fatalf("defaults = %+v", defaults)
	}
}

func TestBuildOfflinePayload(t *testing.T) {
	will := buildOfflinePayload(OfflineReasonConnectionLost, "site-a", nil, nil)
	obj := decodeKeys(t, will)
	assertKeysEqual(t, obj, "schema_version", "id", "event_type", "device_id", "reason")
	if will.Reason != OfflineReasonConnectionLost || will.ID == "" {
		t.Fatalf("will = %+v", will)
	}

	seq := uint64(9)
	now := time.Date(2026, 11, 24, 4, 0, 0, 0, time.UTC)
	graceful := buildOfflinePayload(OfflineReasonGracefulShutdown, "site-a", &seq, &now)
	gracefulObj := decodeKeys(t, graceful)
	assertKeysEqual(t, gracefulObj, "schema_version", "id", "seq", "event_type", "device_id", "sent_at", "reason")
	if graceful.Seq == nil || *graceful.Seq != 9 || graceful.SentAt != "2026-11-24T04:00:00.000Z" {
		t.Fatalf("graceful = %+v", graceful)
	}
}
