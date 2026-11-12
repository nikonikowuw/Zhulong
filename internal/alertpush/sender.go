package alertpush

import (
	"context"
	"io/fs"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
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

// MediaReader abstracts read access to media files with four-layer path protection.
type MediaReader interface {
	OpenContent(ctx context.Context, relPath string) (*os.File, fs.FileInfo, error)
}

// Sender defines the delivery mechanism for an alert destination.
type Sender interface {
	Send(ctx context.Context, ev record.AlarmEvent) error
	SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error
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

// HeartbeatSnapshotProvider supplies a point-in-time sanitized host health and
// workload summary without coupling alertpush to sibling domain packages.
type HeartbeatSnapshotProvider interface {
	Snapshot(ctx context.Context) HeartbeatSnapshot
}

// HeartbeatSnapshot holds the sanitized operational telemetry assembled by the host.
type HeartbeatSnapshot struct {
	ReportedAt time.Time
	Status     string
	Reasons    []string
	Host       HeartbeatHostPayload
	Resources  HeartbeatResourcesPayload
	Storage    HeartbeatStoragePayload
	Workload   HeartbeatWorkloadPayload
}

// HeartbeatHostPayload describes host architecture, uptime and optional reachable base URL.
type HeartbeatHostPayload struct {
	Arch          string `json:"arch"`
	UptimeSeconds uint64 `json:"uptime_seconds"`
	PublicBaseURL string `json:"public_base_url,omitempty"`
}

// HeartbeatResourcesPayload describes core compute and thermal watermarks.
type HeartbeatResourcesPayload struct {
	CPUUsagePercent   float64 `json:"cpu_usage_percent"`
	MemoryUsedPercent float64 `json:"memory_used_percent"`
	NPUSupported      bool    `json:"npu_supported"`
	NPULoadPercent    float64 `json:"npu_load_percent"`
	MaxTemperatureC   float64 `json:"max_temperature_c"`
}

// HeartbeatStoragePayload describes media disk health and write-gate state.
type HeartbeatStoragePayload struct {
	Status      string `json:"status"`
	Writable    bool   `json:"writable"`
	UsedPercent int    `json:"used_percent"`
	FreeMB      int64  `json:"free_mb"`
}

// HeartbeatCamerasPayload summarizes enabled camera health counts.
type HeartbeatCamerasPayload struct {
	Enabled  int `json:"enabled"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
	Degraded int `json:"degraded"`
}

// HeartbeatTasksPayload summarizes enabled channel analysis tasks.
type HeartbeatTasksPayload struct {
	Enabled int `json:"enabled"`
}

// HeartbeatRunnersPayload summarizes active and circuit-broken algorithm runner processes.
type HeartbeatRunnersPayload struct {
	Active int `json:"active"`
	Failed int `json:"failed"`
}

// HeartbeatDecodePayload summarizes native video decoder admission state.
type HeartbeatDecodePayload struct {
	Backend           string `json:"backend"`
	HardwareAvailable bool   `json:"hardware_available"`
	ActiveSessions    int    `json:"active_sessions"`
	MaxSessions       int    `json:"max_sessions"`
}

// HeartbeatWorkloadPayload groups camera, task, runner and decoder summaries.
type HeartbeatWorkloadPayload struct {
	Cameras HeartbeatCamerasPayload `json:"cameras"`
	Tasks   HeartbeatTasksPayload   `json:"tasks"`
	Runners HeartbeatRunnersPayload `json:"runners"`
	Decode  HeartbeatDecodePayload  `json:"decode"`
}

// HeartbeatPayload is the external snake_case contract for outbound liveness reports.
type HeartbeatPayload struct {
	ID         string                    `json:"id"`
	DeviceID   string                    `json:"device_id,omitempty"`
	EventType  string                    `json:"event_type"`
	Seq        uint64                    `json:"seq"`
	ReportedAt string                    `json:"reported_at"`
	Status     string                    `json:"status"`
	Reasons    []string                  `json:"reasons"`
	Host       HeartbeatHostPayload      `json:"host"`
	Resources  HeartbeatResourcesPayload `json:"resources"`
	Storage    HeartbeatStoragePayload   `json:"storage"`
	Workload   HeartbeatWorkloadPayload  `json:"workload"`
}

// mqttOfflineWillPayload is registered on the MQTT broker at connect time (LWT).
// It intentionally omits reported_at because static will bytes cannot stamp the
// future moment when power or network connectivity drops.
type mqttOfflineWillPayload struct {
	DeviceID  string `json:"device_id,omitempty"`
	EventType string `json:"event_type"`
	Reason    string `json:"reason"`
}

// mqttOfflineGracefulPayload is actively published before a clean MQTT disconnect.
type mqttOfflineGracefulPayload struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id,omitempty"`
	EventType  string `json:"event_type"`
	ReportedAt string `json:"reported_at"`
	Reason     string `json:"reason"`
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

// isReservedHeader returns true if the header name is managed internally and forbidden in custom headers.
func isReservedHeader(name string) bool {
	_, ok := reservedHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// resolveDeviceID tolerates a nil provider: payloads then omit device_id instead of
// panicking on a partially wired sender.
func resolveDeviceID(provider DeviceIDProvider) string {
	if provider == nil {
		return ""
	}
	return strings.TrimSpace(provider.DeviceID())
}

// resolveHeartbeatSnapshot fetches a live snapshot from provider or constructs a minimal
// ready snapshot when running in isolated unit tests without full host wiring.
func resolveHeartbeatSnapshot(
	ctx context.Context,
	provider HeartbeatSnapshotProvider,
	publicBaseURL string,
) HeartbeatSnapshot {
	if provider != nil {
		snap := provider.Snapshot(ctx)
		if snap.ReportedAt.IsZero() {
			snap.ReportedAt = time.Now().UTC()
		}
		if snap.Status == "" {
			snap.Status = "ready"
		}
		if snap.Reasons == nil {
			snap.Reasons = []string{}
		}
		if snap.Host.PublicBaseURL == "" && strings.TrimSpace(publicBaseURL) != "" {
			snap.Host.PublicBaseURL = strings.TrimSpace(publicBaseURL)
		}
		return snap
	}

	return HeartbeatSnapshot{
		ReportedAt: time.Now().UTC(),
		Status:     "ready",
		Reasons:    []string{},
		Host: HeartbeatHostPayload{
			Arch:          runtime.GOARCH,
			PublicBaseURL: strings.TrimSpace(publicBaseURL),
		},
		Storage: HeartbeatStoragePayload{
			Status:   "healthy",
			Writable: true,
		},
		Workload: HeartbeatWorkloadPayload{
			Decode: HeartbeatDecodePayload{
				Backend: "auto",
			},
		},
	}
}

func buildHeartbeatPayload(snap HeartbeatSnapshot, deviceID string, seq uint64) HeartbeatPayload {
	reportedAt := snap.ReportedAt
	if reportedAt.IsZero() {
		reportedAt = time.Now().UTC()
	}
	status := snap.Status
	if status == "" {
		status = "ready"
	}
	reasons := snap.Reasons
	if reasons == nil {
		reasons = []string{}
	}

	return HeartbeatPayload{
		ID:         idutil.New(),
		DeviceID:   deviceID,
		EventType:  EventTypeHeartbeat,
		Seq:        seq,
		ReportedAt: reportedAt.UTC().Format(time.RFC3339Nano),
		Status:     status,
		Reasons:    reasons,
		Host:       snap.Host,
		Resources:  snap.Resources,
		Storage:    snap.Storage,
		Workload:   snap.Workload,
	}
}

// sameOrigin reports whether two HTTP(S) URLs share the exact same scheme, hostname
// and effective port. Credentials bound to rawA may only be reused on rawB when true.
func sameOrigin(rawA, rawB string) bool {
	uA, errA := url.Parse(strings.TrimSpace(rawA))
	uB, errB := url.Parse(strings.TrimSpace(rawB))
	if errA != nil || errB != nil {
		return false
	}
	schemeA := strings.ToLower(uA.Scheme)
	schemeB := strings.ToLower(uB.Scheme)
	isHTTPScheme := schemeA == "http" || schemeA == "https"
	if !isHTTPScheme || schemeA != schemeB {
		return false
	}
	hostA := strings.ToLower(uA.Hostname())
	hostB := strings.ToLower(uB.Hostname())
	if hostA == "" || hostA != hostB {
		return false
	}
	return effectivePort(schemeA, uA.Port()) == effectivePort(schemeB, uB.Port())
}

func effectivePort(scheme, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// buildAlarmPayload creates the base payload from a perception event.
func buildAlarmPayload(ev record.AlarmEvent, deviceID string) AlarmPayload {
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

func populateSignedURLs(
	payload *AlarmPayload,
	ev record.AlarmEvent,
	publicBaseURL string,
	signer *URLSigner,
	ttl time.Duration,
) {
	if signer == nil || publicBaseURL == "" {
		return
	}
	if ev.ImageReady && ev.ImagePath != "" {
		if u, err := generateSignedURL(publicBaseURL, signer, ev.ImagePath, ttl); err == nil {
			payload.ImageURL = &u
			payload.ImageReady = true
		}
	}
	if ev.CropReady && ev.CropPath != "" {
		if u, err := generateSignedURL(publicBaseURL, signer, ev.CropPath, ttl); err == nil {
			payload.CropURL = &u
		}
	}
	hasFaceGallery := payload.Face != nil && ev.Face != nil && ev.Face.GalleryImagePath != ""
	if hasFaceGallery {
		if u, err := generateSignedURL(publicBaseURL, signer, ev.Face.GalleryImagePath, ttl); err == nil {
			payload.Face.GalleryURL = &u
		}
	}
}

// generateSignedURL generates an absolute signed URL using the public base URL and URLSigner.
func generateSignedURL(
	publicBaseURL string,
	signer *URLSigner,
	relPath string,
	ttl time.Duration,
) (string, error) {
	if signer == nil || relPath == "" || publicBaseURL == "" {
		return "", nil
	}
	exp := time.Now().Add(ttl).Unix()
	sig, err := signer.Sign(relPath, exp)
	if err != nil {
		return "", err
	}

	base := strings.TrimRight(publicBaseURL, "/")
	cleanRel := normalizeRelativePath(relPath)
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
