package alertpush

import (
	"context"
	"maps"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

// SchemaVersion is the outbound payload contract version. Additive fields keep it;
// any change to an existing field's meaning, type or value set must bump it.
const SchemaVersion = 1

// External v1 event types. Perception events carry a single matching sub-object
// (alarm / face / vehicle); heartbeat and offline use their own flat envelopes.
const (
	EventTypeAlarm   = "alarm"
	EventTypeFace    = "face"
	EventTypeVehicle = "vehicle"
)

// Image roles inside EventImages.
const (
	ImageRoleScene     = "scene"
	ImageRoleObject    = "object"
	ImageRoleReference = "reference"
)

const (
	imageMIMEJPEG = "image/jpeg"
	// payloadTimeLayout pins millisecond precision so every receiver parses the same shape.
	payloadTimeLayout = "2006-01-02T15:04:05.000Z"
)

// vehicleAttributeKeysPromoted are lifted into VehicleInfo and must not be duplicated
// inside its open attributes map.
var vehicleAttributeKeysPromoted = []string{"plate_number", "plate_color", "vehicle_type", "vehicle_color"}

// filterEventTypes is the closed set accepted by destination filters.
var filterEventTypes = []string{EventTypeAlarm, EventTypeFace, EventTypeVehicle}

// EventPayload is the v1 outbound envelope for perception events (alarm, face, vehicle).
// Optional fields omit empty values so payloads stay concise; exactly one business
// sub-object (named after EventType) is populated.
type EventPayload struct {
	SchemaVersion int                  `json:"schema_version"`
	ID            string               `json:"id"`
	Seq           uint64               `json:"seq"`
	EventType     string               `json:"event_type"`
	DeviceID      string               `json:"device_id,omitempty"`
	Camera        *CameraSourceInfo    `json:"camera,omitempty"`
	Algorithm     *AlgorithmSourceInfo `json:"algorithm,omitempty"`
	CapturedAt    string               `json:"captured_at"`
	SentAt        string               `json:"sent_at"`
	Images        *EventImages         `json:"images,omitempty"`
	Alarm         *AlarmInfo           `json:"alarm,omitempty"`
	Face          *FaceInfo            `json:"face,omitempty"`
	Vehicle       *VehicleInfo         `json:"vehicle,omitempty"`
}

// HeartbeatPayload is the v1 liveness summary envelope, kept separate from perception
// events so liveness traffic never carries unused source/image fields.
type HeartbeatPayload struct {
	SchemaVersion int                       `json:"schema_version"`
	ID            string                    `json:"id"`
	Seq           uint64                    `json:"seq"`
	EventType     string                    `json:"event_type"`
	DeviceID      string                    `json:"device_id,omitempty"`
	SentAt        string                    `json:"sent_at"`
	Status        string                    `json:"status"`
	Reasons       []string                  `json:"reasons,omitempty"`
	Host          HeartbeatHostPayload      `json:"host"`
	Resources     HeartbeatResourcesPayload `json:"resources"`
	Storage       HeartbeatStoragePayload   `json:"storage"`
	Workload      HeartbeatWorkloadPayload  `json:"workload"`
}

// OfflinePayload is the v1 host offline notice. MQTT last-will bytes are frozen at connect
// time, so they omit seq and sent_at; graceful shutdown populates both.
type OfflinePayload struct {
	SchemaVersion int     `json:"schema_version"`
	ID            string  `json:"id"`
	Seq           *uint64 `json:"seq,omitempty"`
	EventType     string  `json:"event_type"`
	DeviceID      string  `json:"device_id,omitempty"`
	SentAt        string  `json:"sent_at,omitempty"`
	Reason        string  `json:"reason"`
}

// SourceInfo bundles resolved camera and algorithm metadata returned by SourceResolver.
type SourceInfo struct {
	Camera    *CameraSourceInfo
	Algorithm *AlgorithmSourceInfo
}

// CameraSourceInfo carries the camera surrogate key plus its human-readable name.
type CameraSourceInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// AlgorithmSourceInfo carries the algorithm package identity. Version is the version
// installed at push time, not necessarily the one that produced the event.
type AlgorithmSourceInfo struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	Version string `json:"version,omitempty"`
}

// EventImages indexes deliverable images directly by role so receivers do not need to scan
// a list. Unavailable roles are omitted; when no role is deliverable, EventPayload.Images is nil.
type EventImages struct {
	Scene     *ImageInfo `json:"scene,omitempty"`
	Object    *ImageInfo `json:"object,omitempty"`
	Reference *ImageInfo `json:"reference,omitempty"`
}

// Empty reports whether no image role is populated.
func (e *EventImages) Empty() bool {
	return e == nil || (e.Scene == nil && e.Object == nil && e.Reference == nil)
}

func (e *EventImages) set(role string, info *ImageInfo) {
	switch role {
	case ImageRoleScene:
		e.Scene = info
	case ImageRoleObject:
		e.Object = info
	case ImageRoleReference:
		e.Reference = info
	}
}

// ImageInfo describes one deliverable image. Only fields relevant to the chosen delivery
// carrier (url, part, or base64) are populated.
type ImageInfo struct {
	MIME      string `json:"mime"`
	SizeBytes *int64 `json:"size_bytes,omitempty"`
	URL       string `json:"url,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Part      string `json:"part,omitempty"`
	Base64    string `json:"base64,omitempty"`
}

// AlarmInfo holds behavior and safety alarm details.
type AlarmInfo struct {
	Type       string         `json:"type,omitempty"`
	Score      float64        `json:"score"`
	Box        *[4]float64    `json:"box,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// FaceInfo holds face recognition results. Person and Similarity are omitted when unmatched.
type FaceInfo struct {
	Matched    bool           `json:"matched"`
	Person     *PersonInfo    `json:"person,omitempty"`
	Similarity *float64       `json:"similarity,omitempty"`
	Box        *[4]float64    `json:"box,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// PersonInfo exposes the business key of a matched person, never the internal primary key.
type PersonInfo struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

// VehicleInfo holds vehicle and license plate recognition results.
type VehicleInfo struct {
	Plate      *PlateInfo     `json:"plate,omitempty"`
	Type       string         `json:"type,omitempty"`
	Color      string         `json:"color,omitempty"`
	Score      float64        `json:"score"`
	Box        *[4]float64    `json:"box,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// PlateInfo holds the recognized license plate.
type PlateInfo struct {
	Number string `json:"number"`
	Color  string `json:"color,omitempty"`
}

// Delivery is one queued perception event bound for a single destination. Seq is assigned
// at enqueue time so events dropped on queue overflow leave visible gaps.
type Delivery struct {
	Event  record.AlarmEvent
	Seq    uint64
	Source SourceInfo
}

// SourceResolver looks up human-readable source metadata. It is declared on the consumer
// side so alertpush never imports the camera or algorithm packages. Implementations must
// not fail: lookup errors degrade to omitted names so an event is never lost over metadata.
type SourceResolver interface {
	ResolveSource(ctx context.Context, cameraID, algorithmID string) SourceInfo
}

// formatPayloadTime renders t in UTC with fixed millisecond precision.
func formatPayloadTime(t time.Time) string {
	return t.UTC().Format(payloadTimeLayout)
}

// externalEventType maps the internal record event type onto the v1 short name.
// Unknown or empty values fall back to alarm, matching how record treats legacy events.
func externalEventType(t record.EventType) string {
	switch t {
	case record.EventTypeFaceRecognition:
		return EventTypeFace
	case record.EventTypePlateRecognition:
		return EventTypeVehicle
	default:
		return EventTypeAlarm
	}
}

// buildEventPayload projects a perception delivery onto the lightweight v1 envelope.
// Images start nil; each sender populates them according to its image mode.
func buildEventPayload(d Delivery, deviceID string, sentAt time.Time) EventPayload {
	ev := d.Event
	camera, algo := normalizeSource(d.Source, ev.CameraID, ev.AlgorithmID)

	payload := EventPayload{
		SchemaVersion: SchemaVersion,
		ID:            ev.ID,
		Seq:           d.Seq,
		EventType:     externalEventType(ev.EventType),
		DeviceID:      strings.TrimSpace(deviceID),
		Camera:        camera,
		Algorithm:     algo,
		CapturedAt:    formatPayloadTime(ev.CapturedAt),
		SentAt:        formatPayloadTime(sentAt),
	}

	switch payload.EventType {
	case EventTypeFace:
		payload.Face = buildFaceInfo(ev.Face)
	case EventTypeVehicle:
		payload.Vehicle = buildVehicleInfo(ev.Vehicle)
	default:
		payload.Alarm = buildAlarmInfo(ev)
	}
	return payload
}

// normalizeSource guarantees camera.id always mirrors the event and omits unresolved optional names.
func normalizeSource(
	src SourceInfo,
	cameraID, algorithmID string,
) (*CameraSourceInfo, *AlgorithmSourceInfo) {
	var cam *CameraSourceInfo
	if camID := strings.TrimSpace(cameraID); camID != "" {
		cam = &CameraSourceInfo{ID: camID}
		if src.Camera != nil {
			cam.Name = strings.TrimSpace(src.Camera.Name)
		}
	}

	algoID := strings.TrimSpace(algorithmID)
	if algoID == "" {
		return cam, nil
	}
	algo := &AlgorithmSourceInfo{ID: algoID}
	if src.Algorithm != nil {
		algo.Name = strings.TrimSpace(src.Algorithm.Name)
		algo.Version = strings.TrimSpace(src.Algorithm.Version)
	}
	return cam, algo
}

func buildAlarmInfo(ev record.AlarmEvent) *AlarmInfo {
	if ev.Alarm != nil {
		return &AlarmInfo{
			Type:       strings.TrimSpace(ev.Alarm.AlarmType),
			Score:      ev.Alarm.Score,
			Box:        optionalBox(ev.Alarm.Box),
			Attributes: optionalAttributes(ev.Alarm.Extra),
		}
	}
	// Legacy flat fields are still populated by record for behavior alarms.
	return &AlarmInfo{
		Type:       strings.TrimSpace(ev.AlarmType),
		Score:      ev.Score,
		Box:        optionalBox(ev.Box),
		Attributes: optionalAttributes(ev.Extra),
	}
}

func buildFaceInfo(face *record.FaceEventData) *FaceInfo {
	if face == nil {
		return &FaceInfo{}
	}
	info := &FaceInfo{
		Matched:    face.Matched,
		Box:        optionalBox(face.Box),
		Attributes: optionalAttributes(face.Attributes),
	}
	if !face.Matched {
		return info
	}
	similarity := face.Similarity
	info.Similarity = &similarity
	personID := strings.TrimSpace(face.PersonID)
	personName := strings.TrimSpace(face.PersonName)
	if personID != "" || personName != "" {
		info.Person = &PersonInfo{
			ID:   personID,
			Name: personName,
		}
	}
	return info
}

func buildVehicleInfo(vehicle *record.VehicleEventData) *VehicleInfo {
	if vehicle == nil {
		return &VehicleInfo{}
	}

	// The event is a value copy but its attribute map is shared across destinations,
	// so promoted keys are removed from a clone, never from the original.
	attrs := maps.Clone(vehicle.Attributes)
	for _, key := range vehicleAttributeKeysPromoted {
		delete(attrs, key)
	}

	info := &VehicleInfo{
		Type:       strings.TrimSpace(vehicle.VehicleType),
		Color:      strings.TrimSpace(vehicle.VehicleColor),
		Score:      vehicle.Score,
		Box:        optionalBox(vehicle.Box),
		Attributes: optionalAttributes(attrs),
	}
	if number := strings.TrimSpace(vehicle.PlateNumber); number != "" {
		info.Plate = &PlateInfo{
			Number: number,
			Color:  strings.TrimSpace(vehicle.PlateColor),
		}
	}
	return info
}

// buildHeartbeatPayload wraps a liveness snapshot in the standalone v1 heartbeat envelope.
func buildHeartbeatPayload(
	snap HeartbeatSnapshot,
	deviceID string,
	seq uint64,
	sentAt time.Time,
) HeartbeatPayload {
	status := snap.Status
	if status == "" {
		status = "ready"
	}

	return HeartbeatPayload{
		SchemaVersion: SchemaVersion,
		ID:            idutil.New(),
		Seq:           seq,
		EventType:     EventTypeHeartbeat,
		DeviceID:      strings.TrimSpace(deviceID),
		SentAt:        formatPayloadTime(sentAt),
		Status:        status,
		Reasons:       snap.Reasons,
		Host:          snap.Host,
		Resources:     snap.Resources,
		Storage:       snap.Storage,
		Workload:      snap.Workload,
	}
}

// buildOfflinePayload builds a standalone offline notice. The MQTT last-will is registered
// at connect time as static bytes, so it passes nil seq and sentAt.
func buildOfflinePayload(
	reason, deviceID string,
	seq *uint64,
	sentAt *time.Time,
) OfflinePayload {
	payload := OfflinePayload{
		SchemaVersion: SchemaVersion,
		ID:            idutil.New(),
		EventType:     EventTypeOffline,
		DeviceID:      strings.TrimSpace(deviceID),
		Reason:        reason,
	}
	if seq != nil {
		value := *seq
		payload.Seq = &value
	}
	if sentAt != nil {
		payload.SentAt = formatPayloadTime(*sentAt)
	}
	return payload
}

// optionalBox treats an all-zero box as "no box": classification results carry none.
func optionalBox(box [4]float64) *[4]float64 {
	if box == [4]float64{} {
		return nil
	}
	out := box
	return &out
}

func optionalAttributes(attrs map[string]any) map[string]any {
	if len(attrs) == 0 {
		return nil
	}
	return attrs
}
