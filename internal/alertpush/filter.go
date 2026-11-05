package alertpush

import "slices"

// Matches evaluates whether a dispatched event matches the destination's filter rules.
func (f *DestinationFilter) Matches(eventType, algorithmID, cameraID string) bool {
	if f == nil {
		return true
	}

	// 1. Match event type
	if len(f.EventTypes) > 0 {
		if eventType == "" || !slices.Contains(f.EventTypes, eventType) {
			return false
		}
	}

	// 2. Match algorithm
	if len(f.AlgorithmIDs) > 0 {
		if algorithmID == "" || !slices.Contains(f.AlgorithmIDs, algorithmID) {
			return false
		}
	}

	// 3. Match camera
	if len(f.CameraIDs) > 0 {
		if cameraID == "" || !slices.Contains(f.CameraIDs, cameraID) {
			return false
		}
	}

	return true
}
