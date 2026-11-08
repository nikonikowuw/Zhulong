package alertpush

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

var reservedHeaders = map[string]struct{}{
	"authorization":     {},
	"content-type":      {},
	"content-length":    {},
	"transfer-encoding": {},
	"connection":        {},
	"host":              {},
}

// IsReservedHeader returns true if the header name is managed internally and forbidden in custom headers.
func IsReservedHeader(name string) bool {
	_, ok := reservedHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// MediaReader abstracts read access to media files with four-layer path protection.
type MediaReader interface {
	OpenContent(ctx context.Context, relPath string) (*os.File, fs.FileInfo, error)
}

// Sender defines the delivery mechanism for an alert destination.
type Sender interface {
	Send(ctx context.Context, ev record.AlarmEvent) error
	Close() error
}

// DeviceIDProvider supplies the stable business identity of this host. An
// aggregation platform needs it to tell which edge box pushed an event; the hostname
// is not usable because flashed images routinely share it.
//
// The interface is declared on the consumer side so this package never imports
// systeminfo; the host wires the implementation in.
type DeviceIDProvider interface {
	DeviceID() string
}

// resolveDeviceID tolerates a nil provider: payloads then omit device_id instead of
// panicking on a partially wired sender.
func resolveDeviceID(provider DeviceIDProvider) string {
	if provider == nil {
		return ""
	}
	return strings.TrimSpace(provider.DeviceID())
}

// AlarmDataPayload holds behavior & safety alarm details.
type AlarmDataPayload struct {
	AlarmType string         `json:"alarm_type"`
	Score     float64        `json:"score"`
	Box       [4]float64     `json:"box"`
	Extra     map[string]any `json:"extra"`
}

// FaceDataPayload holds face recognition identity and dynamic attributes.
type FaceDataPayload struct {
	Matched    bool           `json:"matched"`
	PersonName string         `json:"person_name"`
	PersonID   string         `json:"person_id"`
	Similarity float64        `json:"similarity"`
	Box        [4]float64     `json:"box"`
	GalleryURL *string        `json:"gallery_url,omitempty"`
	Attributes map[string]any `json:"attributes"`
}

// VehicleDataPayload holds vehicle & license plate details and dynamic attributes.
type VehicleDataPayload struct {
	PlateNumber  string         `json:"plate_number"`
	PlateColor   string         `json:"plate_color"`
	VehicleType  string         `json:"vehicle_type"`
	VehicleColor string         `json:"vehicle_color"`
	Score        float64        `json:"score"`
	Box          [4]float64     `json:"box"`
	Attributes   map[string]any `json:"attributes"`
}

// AlarmPayload is the external contract representation for perception event dispatch (snake_case).
type AlarmPayload struct {
	ID string `json:"id"`
	// DeviceID is this host's business identity, configured under settings → system.
	// It is what lets one aggregation platform tell two edge boxes apart.
	DeviceID    string              `json:"device_id,omitempty"`
	EventType   string              `json:"event_type"` // "alarm" | "face_recognition" | "plate_recognition"
	CameraID    string              `json:"camera_id"`
	CapturedAt  string              `json:"captured_at"`
	ImageReady  bool                `json:"image_ready"`
	ImageURL    *string             `json:"image_url"`
	CropURL     *string             `json:"crop_url"`
	ImageBase64 *string             `json:"image_base64"`
	Alarm       *AlarmDataPayload   `json:"alarm,omitempty"`
	Face        *FaceDataPayload    `json:"face,omitempty"`
	Vehicle     *VehicleDataPayload `json:"vehicle,omitempty"`
}

// BuildAlarmPayload creates the base payload from a perception event.
func BuildAlarmPayload(ev record.AlarmEvent, deviceID string) AlarmPayload {
	eventType := string(ev.EventType)
	if eventType == "" {
		eventType = string(record.EventTypeAlarm)
	}

	payload := AlarmPayload{
		ID:         ev.ID,
		DeviceID:   deviceID,
		EventType:  eventType,
		CameraID:   ev.CameraID,
		CapturedAt: ev.CapturedAt.UTC().Format(time.RFC3339Nano),
		ImageReady: false,
	}

	switch ev.EventType {
	case record.EventTypeFaceRecognition:
		if ev.Face != nil {
			attrs := ev.Face.Attributes
			if attrs == nil {
				attrs = make(map[string]any)
			}
			payload.Face = &FaceDataPayload{
				Matched:    ev.Face.Matched,
				PersonName: ev.Face.PersonName,
				PersonID:   ev.Face.PersonID,
				Similarity: ev.Face.Similarity,
				Box:        ev.Face.Box,
				Attributes: attrs,
			}
		}
	case record.EventTypePlateRecognition:
		if ev.Vehicle != nil {
			attrs := ev.Vehicle.Attributes
			if attrs == nil {
				attrs = make(map[string]any)
			}
			payload.Vehicle = &VehicleDataPayload{
				PlateNumber:  ev.Vehicle.PlateNumber,
				PlateColor:   ev.Vehicle.PlateColor,
				VehicleType:  ev.Vehicle.VehicleType,
				VehicleColor: ev.Vehicle.VehicleColor,
				Score:        ev.Vehicle.Score,
				Box:          ev.Vehicle.Box,
				Attributes:   attrs,
			}
		}
	default:
		// Default to behavior / safety alarm
		var alarmData *AlarmDataPayload
		if ev.Alarm != nil {
			extra := ev.Alarm.Extra
			if extra == nil {
				extra = make(map[string]any)
			}
			alarmData = &AlarmDataPayload{
				AlarmType: ev.Alarm.AlarmType,
				Score:     ev.Alarm.Score,
				Box:       ev.Alarm.Box,
				Extra:     extra,
			}
		} else {
			extra := ev.Extra
			if extra == nil {
				extra = make(map[string]any)
			}
			alarmData = &AlarmDataPayload{
				AlarmType: ev.AlarmType,
				Score:     ev.Score,
				Box:       ev.Box,
				Extra:     extra,
			}
		}
		payload.Alarm = alarmData
	}

	return payload
}

// GenerateSignedURL generates an absolute signed URL using the public base URL and URLSigner.
func GenerateSignedURL(publicBaseURL string, signer *URLSigner, relPath string, ttl time.Duration) (string, error) {
	if signer == nil || relPath == "" || publicBaseURL == "" {
		return "", nil
	}
	exp := time.Now().Add(ttl).Unix()
	sig, err := signer.Sign(relPath, exp)
	if err != nil {
		return "", err
	}

	base := strings.TrimRight(publicBaseURL, "/")
	cleanRel := NormalizeRelativePath(relPath)
	u, err := url.Parse(base + "/api/v1/media/signed/" + cleanRel)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", sig)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
