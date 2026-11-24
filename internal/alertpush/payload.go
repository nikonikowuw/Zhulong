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

// External v1 event types. Each value equals the key of the single business
// sub-object carried by the payload (EventTypeHeartbeat / EventTypeOffline live in model.go).
const (
	EventTypeAlarm   = "alarm"
	EventTypeFace    = "face"
	EventTypeVehicle = "vehicle"
)

// Image roles inside EventPayload.Images, emitted in this fixed order.
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

// EventPayload is the v1 outbound envelope shared by perception events, heartbeats and
// offline notices. Optional scalars are always present and serialize as null; exactly one
// business sub-object (named after EventType) is non-nil.
type EventPayload struct {
	SchemaVersion int            `json:"schema_version"`
	ID            string         `json:"id"`
	Seq           *uint64        `json:"seq"`
	EventType     string         `json:"event_type"`
	DeviceID      *string        `json:"device_id"`
	Source        *SourceInfo    `json:"source"`
	CapturedAt    *string        `json:"captured_at"`
	SentAt        *string        `json:"sent_at"`
	Images        []ImageInfo    `json:"images"`
	Alarm         *AlarmInfo     `json:"alarm,omitempty"`
	Face          *FaceInfo      `json:"face,omitempty"`
	Vehicle       *VehicleInfo   `json:"vehicle,omitempty"`
	Heartbeat     *HeartbeatInfo `json:"heartbeat,omitempty"`
	Offline       *OfflineInfo   `json:"offline,omitempty"`
}

// SourceInfo identifies where a perception event came from.
type SourceInfo struct {
	Camera    *CameraSourceInfo    `json:"camera"`
	Algorithm *AlgorithmSourceInfo `json:"algorithm"`
}

// CameraSourceInfo carries the camera surrogate key plus its human-readable name.
type CameraSourceInfo struct {
	ID   string  `json:"id"`
	Name *string `json:"name"`
}

// AlgorithmSourceInfo carries the algorithm package identity. Version is the version
// installed at push time, not necessarily the one that produced the event.
type AlgorithmSourceInfo struct {
	ID      string  `json:"id"`
	Name    *string `json:"name"`
	Version *string `json:"version"`
}

// ImageInfo describes one image deliverable with this message. An image that cannot be
// delivered through any carrier is omitted from the list entirely.
type ImageInfo struct {
	Role         string  `json:"role"`
	MIME         string  `json:"mime"`
	SizeBytes    *int64  `json:"size_bytes"`
	URL          *string `json:"url"`
	URLExpiresAt *string `json:"url_expires_at"`
	Part         *string `json:"part"`
	Base64       *string `json:"base64"`
}

// AlarmInfo holds behavior and safety alarm details.
type AlarmInfo struct {
	AlarmType  *string        `json:"alarm_type"`
	Score      float64        `json:"score"`
	Box        *[4]float64    `json:"box"`
	Attributes map[string]any `json:"attributes"`
}

// FaceInfo holds face recognition results. Person and Similarity are null when unmatched.
type FaceInfo struct {
	Matched    bool           `json:"matched"`
	Person     *PersonInfo    `json:"person"`
	Similarity *float64       `json:"similarity"`
	Box        *[4]float64    `json:"box"`
	Attributes map[string]any `json:"attributes"`
}

// PersonInfo exposes the business key of a matched person, never the internal primary key.
type PersonInfo struct {
	ID   *string `json:"id"`
	Name *string `json:"name"`
}

// VehicleInfo holds vehicle and license plate recognition results.
type VehicleInfo struct {
	Plate        *PlateInfo     `json:"plate"`
	VehicleType  *string        `json:"vehicle_type"`
	VehicleColor *string        `json:"vehicle_color"`
	Score        float64        `json:"score"`
	Box          *[4]float64    `json:"box"`
	Attributes   map[string]any `json:"attributes"`
}

// PlateInfo holds the recognized license plate.
type PlateInfo struct {
	Number string  `json:"number"`
	Color  *string `json:"color"`
}

// HeartbeatInfo is the sanitized host health snapshot carried by heartbeat messages.
type HeartbeatInfo struct {
	Status    string                    `json:"status"`
	Reasons   []string                  `json:"reasons"`
	Host      HeartbeatHostPayload      `json:"host"`
	Resources HeartbeatResourcesPayload `json:"resources"`
	Storage   HeartbeatStoragePayload   `json:"storage"`
	Workload  HeartbeatWorkloadPayload  `json:"workload"`
}

// OfflineInfo explains why the host went offline.
type OfflineInfo struct {
	Reason string `json:"reason"`
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
// not fail: lookup errors degrade to null fields so an event is never lost over metadata.
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

// buildEventPayload projects a perception delivery onto the v1 envelope. Images start empty;
// each sender fills them according to its image mode.
func buildEventPayload(d Delivery, deviceID string, sentAt time.Time) EventPayload {
	ev := d.Event
	seq := d.Seq
	capturedAt := formatPayloadTime(ev.CapturedAt)
	sent := formatPayloadTime(sentAt)

	payload := EventPayload{
		SchemaVersion: SchemaVersion,
		ID:            ev.ID,
		Seq:           &seq,
		EventType:     externalEventType(ev.EventType),
		DeviceID:      optionalString(deviceID),
		Source:        normalizeSource(d.Source, ev.CameraID, ev.AlgorithmID),
		CapturedAt:    &capturedAt,
		SentAt:        &sent,
		Images:        []ImageInfo{},
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

// normalizeSource guarantees camera.id always mirrors the event and fills defaults when the
// resolver is absent or returned nothing.
func normalizeSource(src SourceInfo, cameraID, algorithmID string) *SourceInfo {
	out := SourceInfo{
		Camera:    &CameraSourceInfo{ID: cameraID},
		Algorithm: nil,
	}
	if src.Camera != nil {
		out.Camera.Name = optionalStringPtr(src.Camera.Name)
	}

	algoID := strings.TrimSpace(algorithmID)
	if algoID == "" {
		return &out
	}
	out.Algorithm = &AlgorithmSourceInfo{ID: algoID}
	if src.Algorithm != nil {
		out.Algorithm.Name = optionalStringPtr(src.Algorithm.Name)
		out.Algorithm.Version = optionalStringPtr(src.Algorithm.Version)
	}
	return &out
}

func buildAlarmInfo(ev record.AlarmEvent) *AlarmInfo {
	if ev.Alarm != nil {
		return &AlarmInfo{
			AlarmType:  optionalString(ev.Alarm.AlarmType),
			Score:      ev.Alarm.Score,
			Box:        optionalBox(ev.Alarm.Box),
			Attributes: nonNilAttributes(ev.Alarm.Extra),
		}
	}
	// Legacy flat fields are still populated by record for behavior alarms.
	return &AlarmInfo{
		AlarmType:  optionalString(ev.AlarmType),
		Score:      ev.Score,
		Box:        optionalBox(ev.Box),
		Attributes: nonNilAttributes(ev.Extra),
	}
}

func buildFaceInfo(face *record.FaceEventData) *FaceInfo {
	if face == nil {
		return &FaceInfo{Attributes: map[string]any{}}
	}
	info := &FaceInfo{
		Matched:    face.Matched,
		Box:        optionalBox(face.Box),
		Attributes: nonNilAttributes(face.Attributes),
	}
	if !face.Matched {
		return info
	}
	similarity := face.Similarity
	info.Similarity = &similarity
	info.Person = &PersonInfo{
		ID:   optionalString(face.PersonID),
		Name: optionalString(face.PersonName),
	}
	return info
}

func buildVehicleInfo(vehicle *record.VehicleEventData) *VehicleInfo {
	if vehicle == nil {
		return &VehicleInfo{Attributes: map[string]any{}}
	}

	// The event is a value copy but its attribute map is shared across destinations,
	// so promoted keys are removed from a clone, never from the original.
	attrs := maps.Clone(vehicle.Attributes)
	if attrs == nil {
		attrs = map[string]any{}
	}
	for _, key := range vehicleAttributeKeysPromoted {
		delete(attrs, key)
	}

	info := &VehicleInfo{
		VehicleType:  optionalString(vehicle.VehicleType),
		VehicleColor: optionalString(vehicle.VehicleColor),
		Score:        vehicle.Score,
		Box:          optionalBox(vehicle.Box),
		Attributes:   attrs,
	}
	if number := strings.TrimSpace(vehicle.PlateNumber); number != "" {
		info.Plate = &PlateInfo{
			Number: number,
			Color:  optionalString(vehicle.PlateColor),
		}
	}
	return info
}

// buildHeartbeatEnvelope wraps a liveness snapshot in the v1 envelope.
func buildHeartbeatEnvelope(
	snap HeartbeatSnapshot,
	deviceID string,
	seq uint64,
	sentAt time.Time,
) EventPayload {
	status := snap.Status
	if status == "" {
		status = "ready"
	}
	reasons := snap.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	sent := formatPayloadTime(sentAt)

	return EventPayload{
		SchemaVersion: SchemaVersion,
		ID:            idutil.New(),
		Seq:           &seq,
		EventType:     EventTypeHeartbeat,
		DeviceID:      optionalString(deviceID),
		SentAt:        &sent,
		Images:        []ImageInfo{},
		Heartbeat: &HeartbeatInfo{
			Status:    status,
			Reasons:   reasons,
			Host:      snap.Host,
			Resources: snap.Resources,
			Storage:   snap.Storage,
			Workload:  snap.Workload,
		},
	}
}

// buildOfflineEnvelope builds an offline notice. The MQTT last-will is registered at connect
// time as static bytes, so it passes nil seq and sentAt: it cannot know when the link drops.
func buildOfflineEnvelope(reason, deviceID string, seq *uint64, sentAt *time.Time) EventPayload {
	payload := EventPayload{
		SchemaVersion: SchemaVersion,
		ID:            idutil.New(),
		EventType:     EventTypeOffline,
		DeviceID:      optionalString(deviceID),
		Images:        []ImageInfo{},
		Offline:       &OfflineInfo{Reason: reason},
	}
	if seq != nil {
		value := *seq
		payload.Seq = &value
	}
	if sentAt != nil {
		sent := formatPayloadTime(*sentAt)
		payload.SentAt = &sent
	}
	return payload
}

// optionalString maps blank strings to nil so they serialize as null.
func optionalString(s string) *string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func optionalStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	return optionalString(*s)
}

// optionalBox treats an all-zero box as "no box": classification results carry none.
func optionalBox(box [4]float64) *[4]float64 {
	if box == [4]float64{} {
		return nil
	}
	out := box
	return &out
}

func nonNilAttributes(attrs map[string]any) map[string]any {
	if attrs == nil {
		return map[string]any{}
	}
	return attrs
}
