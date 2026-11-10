package alertpush

import "slices"

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

func matchesFilterList(allowed []string, value string) bool {
	if len(allowed) == 0 {
		return true
	}
	return value != "" && slices.Contains(allowed, value)
}
