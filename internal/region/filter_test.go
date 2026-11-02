package region

import (
	"reflect"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

func TestConfig_Apply(t *testing.T) {
	// Base mock result
	now := time.Now().UTC()
	makeBaseResult := func() aitypes.InferenceResult {
		return aitypes.InferenceResult{
			CameraID:    "cam-1",
			AlgorithmID: "algo-1",
			PTS:         1000,
			Timestamp:   now,
			TaskType:    aitypes.TaskTypeDetection,
			Detections: []aitypes.DetectionItem{
				{
					// box: [0.1, 0.1, 0.3, 0.3] -> bottom center: (0.2, 0.3)
					Box:   [4]float64{0.1, 0.1, 0.3, 0.3},
					Label: "inside_roi",
					Score: 0.9,
				},
				{
					// box: [0.7, 0.7, 0.9, 0.9] -> bottom center: (0.8, 0.9)
					Box:   [4]float64{0.7, 0.7, 0.9, 0.9},
					Label: "outside_roi",
					Score: 0.8,
				},
				{
					// box: [0.35, 0.35, 0.45, 0.45] -> bottom center: (0.4, 0.45)
					Box:   [4]float64{0.35, 0.35, 0.45, 0.45},
					Label: "inside_both_roi_and_mask",
					Score: 0.85,
				},
			},
			Persons: []aitypes.PersonItem{
				{
					TrackID:   1,
					BodyBox:   [4]float64{0.1, 0.1, 0.3, 0.3}, // bottom center: (0.2, 0.3)
					BodyScore: 0.95,
					Faces: []aitypes.FaceSubItem{
						{Quality: 0.99, Matched: true, PersonName: "Alice"},
					},
				},
				{
					TrackID:   2,
					BodyBox:   [4]float64{0.7, 0.7, 0.9, 0.9}, // bottom center: (0.8, 0.9)
					BodyScore: 0.85,
				},
			},
			Vehicles: []aitypes.VehicleItem{
				{
					TrackID:      101,
					VehicleBox:   [4]float64{0.1, 0.1, 0.3, 0.3}, // bottom center: (0.2, 0.3)
					VehicleScore: 0.93,
					Plates:       []aitypes.PlateSubItem{{Box: [4]float64{0.15, 0.2, 0.25, 0.28}, Score: 0.97, PlateNumber: "\u4eacA12345"}},
				},
				{
					TrackID:      102,
					VehicleBox:   [4]float64{0.7, 0.7, 0.9, 0.9}, // bottom center: (0.8, 0.9)
					VehicleScore: 0.88,
					Plates:       []aitypes.PlateSubItem{{Box: [4]float64{0.75, 0.8, 0.85, 0.88}, Score: 0.91, PlateNumber: "\u6caaB54321"}},
				},
				{
					TrackID:      103,
					VehicleBox:   [4]float64{0.35, 0.35, 0.45, 0.45}, // bottom center: (0.4, 0.45)
					VehicleScore: 0.80,
				},
			},
			Classifications: []aitypes.ClassificationItem{
				{Label: "indoor", Score: 0.99},
			},
		}
	}

	t.Run("empty config returns unmodified (fast path)", func(t *testing.T) {
		cfg := Config{}
		res := makeBaseResult()
		out := cfg.Filter(res)
		if !reflect.DeepEqual(res, out) {
			t.Fatalf("expected unmodified result, got differences")
		}
	})

	t.Run("empty result beacons preserved as empty slices", func(t *testing.T) {
		cfg := Config{
			ROI: []Rect{{0.0, 0.0, 0.5, 0.5}},
		}
		beacon := aitypes.InferenceResult{
			CameraID:   "cam-1",
			Detections: []aitypes.DetectionItem{},
			Persons:    []aitypes.PersonItem{},
			Vehicles:   []aitypes.VehicleItem{},
		}
		out := cfg.Filter(beacon)
		if out.Detections == nil || len(out.Detections) != 0 {
			t.Fatalf("expected empty non-nil detections slice")
		}
		if out.Persons == nil || len(out.Persons) != 0 {
			t.Fatalf("expected empty non-nil persons slice")
		}
		if out.Vehicles == nil || len(out.Vehicles) != 0 {
			t.Fatalf("expected empty non-nil vehicles slice")
		}
	})

	t.Run("ROI only filtering", func(t *testing.T) {
		// ROI covers [0.0, 0.0, 0.5, 0.5], including (0.2, 0.3) and (0.4, 0.45)
		cfg := Config{
			ROI: []Rect{{0.0, 0.0, 0.5, 0.5}},
		}
		out := cfg.Filter(makeBaseResult())
		if len(out.Detections) != 2 {
			t.Fatalf("expected 2 detections, got %d", len(out.Detections))
		}
		if out.Detections[0].Label != "inside_roi" {
			t.Errorf("expected inside_roi, got %s", out.Detections[0].Label)
		}
		if out.Detections[1].Label != "inside_both_roi_and_mask" {
			t.Errorf("expected inside_both_roi_and_mask, got %s", out.Detections[1].Label)
		}

		if len(out.Persons) != 1 {
			t.Fatalf("expected 1 person, got %d", len(out.Persons))
		}
		if out.Persons[0].TrackID != 1 {
			t.Errorf("expected track id 1, got %d", out.Persons[0].TrackID)
		}
		if len(out.Persons[0].Faces) != 1 {
			t.Errorf("expected face kept intact, got %d", len(out.Persons[0].Faces))
		}

		// Vehicles obey the same policy (vehicle_structure / plate recognition package)
		if len(out.Vehicles) != 2 {
			t.Fatalf("expected 2 vehicles, got %d", len(out.Vehicles))
		}
		if out.Vehicles[0].TrackID != 101 || out.Vehicles[1].TrackID != 103 {
			t.Errorf("expected tracks 101,103, got %d,%d", out.Vehicles[0].TrackID, out.Vehicles[1].TrackID)
		}
		if len(out.Vehicles[0].Plates) != 1 {
			t.Errorf("expected plates kept intact, got %d", len(out.Vehicles[0].Plates))
		}

		// Classification untouched
		if len(out.Classifications) != 1 {
			t.Errorf("expected 1 classification, got %d", len(out.Classifications))
		}
	})

	t.Run("MASK only filtering", func(t *testing.T) {
		// MASK covers [0.3, 0.3, 0.5, 0.5], excluding (0.4, 0.45)
		cfg := Config{
			MASK: []Rect{{0.3, 0.3, 0.5, 0.5}},
		}
		out := cfg.Filter(makeBaseResult())
		if len(out.Detections) != 2 {
			t.Fatalf("expected 2 detections, got %d", len(out.Detections))
		}
		if out.Detections[0].Label != "inside_roi" {
			t.Errorf("expected inside_roi, got %s", out.Detections[0].Label)
		}
		if out.Detections[1].Label != "outside_roi" {
			t.Errorf("expected outside_roi, got %s", out.Detections[1].Label)
		}

		if len(out.Vehicles) != 2 {
			t.Fatalf("expected 2 vehicles, got %d", len(out.Vehicles))
		}
		if out.Vehicles[0].TrackID != 101 || out.Vehicles[1].TrackID != 102 {
			t.Errorf("expected tracks 101,102, got %d,%d", out.Vehicles[0].TrackID, out.Vehicles[1].TrackID)
		}
	})

	t.Run("Both ROI and MASK: MASK takes priority over ROI", func(t *testing.T) {
		// ROI: [0.0, 0.0, 0.5, 0.5]
		// MASK: [0.3, 0.3, 0.5, 0.5]
		// (0.2, 0.3) -> in ROI, not in MASK -> KEEP
		// (0.4, 0.45) -> in ROI, in MASK -> DROP
		// (0.8, 0.9) -> not in ROI -> DROP
		cfg := Config{
			ROI:  []Rect{{0.0, 0.0, 0.5, 0.5}},
			MASK: []Rect{{0.3, 0.3, 0.5, 0.5}},
		}
		out := cfg.Filter(makeBaseResult())
		if len(out.Detections) != 1 {
			t.Fatalf("expected 1 detection, got %d", len(out.Detections))
		}
		if out.Detections[0].Label != "inside_roi" {
			t.Errorf("expected inside_roi, got %s", out.Detections[0].Label)
		}

		if len(out.Persons) != 1 {
			t.Fatalf("expected 1 person, got %d", len(out.Persons))
		}
		if out.Persons[0].TrackID != 1 {
			t.Errorf("expected track id 1, got %d", out.Persons[0].TrackID)
		}

		if len(out.Vehicles) != 1 {
			t.Fatalf("expected 1 vehicle, got %d", len(out.Vehicles))
		}
		if out.Vehicles[0].TrackID != 101 {
			t.Errorf("expected vehicle track 101, got %d", out.Vehicles[0].TrackID)
		}
		if len(out.Vehicles[0].Plates) != 1 || out.Vehicles[0].Plates[0].PlateNumber != "\u4eacA12345" {
			t.Errorf("expected vehicle plate kept intact, got %+v", out.Vehicles[0].Plates)
		}
	})

	t.Run("scene structure faces filtering, index remapping, and fail-closed guard", func(t *testing.T) {
		// ROI covers [0.0, 0.0, 0.5, 0.5]
		cfg := Config{
			ROI: []Rect{{0.0, 0.0, 0.5, 0.5}},
		}

		res := aitypes.InferenceResult{
			CameraID: "cam-1",
			TaskType: aitypes.TaskTypeSceneStructure,
			Persons: []aitypes.PersonItem{
				{
					TrackID:   1,
					BodyBox:   [4]float64{0.7, 0.7, 0.9, 0.9}, // (0.8, 0.9) Outside ROI -> will be dropped
					BodyScore: 0.9,
				},
				{
					TrackID:   2,
					BodyBox:   [4]float64{0.1, 0.1, 0.3, 0.3}, // (0.2, 0.3) Inside ROI -> kept, newIdx = 0
					BodyScore: 0.95,
				},
			},
			Faces: []aitypes.FaceStructureItem{
				{
					// Associated to Person 0 (Outside ROI) -> should be dropped
					Box:         [4]float64{0.1, 0.1, 0.2, 0.2}, // Even if face box is inside ROI!
					PersonIndex: 0,
					PersonName:  "FaceOfPerson0",
				},
				{
					// Associated to Person 1 (Inside ROI) -> kept, PersonIndex remapped 1 -> 0
					Box:         [4]float64{0.15, 0.12, 0.25, 0.22},
					PersonIndex: 1,
					PersonName:  "FaceOfPerson1",
				},
				{
					// Isolated face inside ROI -> kept, PersonIndex stays -1
					Box:         [4]float64{0.2, 0.2, 0.3, 0.3}, // bottom center: (0.25, 0.3)
					PersonIndex: -1,
					PersonName:  "IsolatedInside",
				},
				{
					// Isolated face outside ROI -> dropped
					Box:         [4]float64{0.6, 0.6, 0.8, 0.8}, // bottom center: (0.7, 0.8)
					PersonIndex: -1,
					PersonName:  "IsolatedOutside",
				},
				{
					// Malformed index out of bounds (person_index = 99) -> fail-closed dropped
					Box:         [4]float64{0.2, 0.2, 0.3, 0.3},
					PersonIndex: 99,
					PersonName:  "MalformedOutOfBounds",
				},
				{
					// Malformed negative index (person_index = -5) -> fail-closed dropped
					Box:         [4]float64{0.2, 0.2, 0.3, 0.3},
					PersonIndex: -5,
					PersonName:  "MalformedNegative",
				},
			},
		}

		out := cfg.Filter(res)

		// 1. Person verification
		if len(out.Persons) != 1 {
			t.Fatalf("expected 1 person kept, got %d", len(out.Persons))
		}
		if out.Persons[0].TrackID != 2 {
			t.Errorf("expected Person 2 kept, got trackID %d", out.Persons[0].TrackID)
		}

		// 2. Face verification
		if len(out.Faces) != 2 {
			t.Fatalf("expected exactly 2 faces kept, got %d: %+v", len(out.Faces), out.Faces)
		}

		// Face 1: FaceOfPerson1 remapped to PersonIndex 0
		if out.Faces[0].PersonName != "FaceOfPerson1" || out.Faces[0].PersonIndex != 0 {
			t.Errorf("expected FaceOfPerson1 remapped to index 0, got %+v", out.Faces[0])
		}

		// Face 2: IsolatedInside with PersonIndex -1
		if out.Faces[1].PersonName != "IsolatedInside" || out.Faces[1].PersonIndex != -1 {
			t.Errorf("expected IsolatedInside kept with index -1, got %+v", out.Faces[1])
		}
	})
}
