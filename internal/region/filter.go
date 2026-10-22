package region

import (
	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// Filter applies the ROI and MASK policy to an InferenceResult.
//
//  1. If Config is empty, result is returned directly (no-op fast path).
//  2. Classifications have no bounding box and are not affected.
//  3. For each detection / person / vehicle, the decision is strictly based on the target
//     box's bottom-center point: drop when it falls outside every configured ROI (ROI is an
//     allowlist, empty means pass-all), and drop when it falls inside any MASK. MASK wins
//     over ROI.
//  4. If a Person is kept its associated faces remain intact; if a Vehicle is kept its
//     plates remain intact. Sub-targets are never re-evaluated: the policy anchors on the
//     entity that owns the track, which is what DeepStream / Frigate / Axis all do.
//  5. Empty result beacons (vanishing target indicator) are preserved as empty slices.
//
// ⚠️ Every task type that carries a movable target must be handled here. A missing
// branch fails silently: the policy simply stops applying to that family of records, and
// users report it as "configured ROI but vehicles still show up". Detection, person and
// vehicle are the three families the runner can emit today (see internal/aitypes TaskType*).
func (c Config) Filter(res aitypes.InferenceResult) aitypes.InferenceResult {
	if c.IsEmpty() {
		return res
	}

	res.Detections = filterByRegion(res.Detections, func(det aitypes.DetectionItem) [4]float64 {
		return det.Box
	}, c)
	res.Persons = filterByRegion(res.Persons, func(person aitypes.PersonItem) [4]float64 {
		return person.BodyBox
	}, c)
	// 判定用车辆框而非车牌框：车牌不跨区域，车辆才跨区域。
	res.Vehicles = filterByRegion(res.Vehicles, func(vehicle aitypes.VehicleItem) [4]float64 {
		return vehicle.VehicleBox
	}, c)

	// Classifications do not have bounding boxes and are unaffected
	return res
}

func filterByRegion[T any](items []T, boxOf func(T) [4]float64, config Config) []T {
	if len(items) == 0 {
		return items
	}
	filtered := make([]T, 0, len(items))
	for _, item := range items {
		px, py := Rect(boxOf(item)).BottomCenter()
		if config.isPointAllowed(px, py) {
			filtered = append(filtered, item)
		}
	}
	return filtered
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
