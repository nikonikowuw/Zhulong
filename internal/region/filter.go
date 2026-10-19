package region

import (
	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// Filter applies the ROI and MASK policy to an InferenceResult:
// 1. If Config is empty, result is returned directly (no-op fast path).
// 2. Classifications have no bounding box and are not affected.
// 3. For each detection / person, determination is strictly based on target box's bottom-center point:
//   - If len(ROI) > 0 and point is not in any ROI: drop
//   - If point is in any MASK: drop (MASK takes priority over ROI)
//   - Otherwise keep
//
// 4. If a Person is kept, its associated faces remain intact.
// 5. Empty result beacons (vanishing target indicator) are preserved as empty slices.
func (c Config) Filter(res aitypes.InferenceResult) aitypes.InferenceResult {
	if c.IsEmpty() {
		return res
	}

	// Filter detections
	if len(res.Detections) > 0 {
		filtered := make([]aitypes.DetectionItem, 0, len(res.Detections))
		for _, det := range res.Detections {
			rect := Rect(det.Box)
			px, py := rect.BottomCenter()
			if c.isPointAllowed(px, py) {
				filtered = append(filtered, det)
			}
		}
		res.Detections = filtered
	}

	// Filter persons
	if len(res.Persons) > 0 {
		filtered := make([]aitypes.PersonItem, 0, len(res.Persons))
		for _, p := range res.Persons {
			rect := Rect(p.BodyBox)
			px, py := rect.BottomCenter()
			if c.isPointAllowed(px, py) {
				filtered = append(filtered, p)
			}
		}
		res.Persons = filtered
	}

	// Classifications do not have bounding boxes and are unaffected
	return res
}

func (c Config) isPointAllowed(px, py float64) bool {
	// 1. If ROI is configured, point must be inside at least one ROI
	if len(c.ROI) > 0 {
		inROI := false
		for _, roi := range c.ROI {
			if roi.Contains(px, py) {
				inROI = true
				break
			}
		}
		if !inROI {
			return false
		}
	}

	// 2. If point is inside any MASK, it is dropped (MASK takes precedence)
	for _, mask := range c.MASK {
		if mask.Contains(px, py) {
			return false
		}
	}

	return true
}
