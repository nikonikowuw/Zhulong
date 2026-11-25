package alertpush

import (
	"slices"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

// Matches evaluates whether a dispatched event matches the destination's filter rules.
func (f *destinationFilter) Matches(eventType, algorithmID, cameraID string) bool {
	if f == nil {
		return true
	}
	if !matchesFilterList(f.EventTypes, eventType) {
		return false
	}
	if !matchesFilterList(f.AlgorithmIDs, algorithmID) {
		return false
	}
	return matchesFilterList(f.CameraIDs, cameraID)
}

// normalize trims and deduplicates persisted filter criteria and migrates any legacy
// event type names (face_recognition / plate_recognition) to v1 short names in memory
// so legacy database rows never silently drop events. Unknown entries are preserved
// (never dropped into an empty wildcard) and reported so callers can log a warning.
func (f *destinationFilter) normalize() (mappedLegacy bool, unknownEventTypes []string) {
	if f == nil {
		return false, nil
	}
	if len(f.EventTypes) > 0 {
		out := make([]string, 0, len(f.EventTypes))
		for _, raw := range f.EventTypes {
			v := strings.TrimSpace(raw)
			switch v {
			case string(record.EventTypeFaceRecognition):
				v = EventTypeFace
				mappedLegacy = true
			case string(record.EventTypePlateRecognition):
				v = EventTypeVehicle
				mappedLegacy = true
			}
			if !slices.Contains(filterEventTypes, v) {
				unknownEventTypes = append(unknownEventTypes, raw)
			}
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		}
		f.EventTypes = out
	}
	f.AlgorithmIDs = dedupeTrimmed(f.AlgorithmIDs)
	f.CameraIDs = dedupeTrimmed(f.CameraIDs)
	return mappedLegacy, unknownEventTypes
}

func dedupeTrimmed(items []string) []string {
	if len(items) == 0 {
		return items
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		v := strings.TrimSpace(raw)
		if v == "" || slices.Contains(out, v) {
			continue
		}
		out = append(out, v)
	}
	return out
}

func matchesFilterList(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	return value != "" && slices.Contains(allowed, value)
}
