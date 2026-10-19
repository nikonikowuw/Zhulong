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
		}
		out := cfg.Filter(beacon)
		if out.Detections == nil || len(out.Detections) != 0 {
			t.Fatalf("expected empty non-nil detections slice")
		}
		if out.Persons == nil || len(out.Persons) != 0 {
			t.Fatalf("expected empty non-nil persons slice")
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
	})
}
