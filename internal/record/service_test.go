package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

func TestServiceProcessPersonStructureAndCooldown(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Insert dummy person face photo in person_faces for gallery resolution
	if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		VALUES ('face_alice_01', 'person_alice', 'faces/person_alice_face_alice_01.jpg', X'00', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert dummy person_face: %v", err)
	}

	svc := record.NewService(store, dbStore, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)

	captureTime := time.Date(2026, 10, 16, 15, 30, 0, 123000000, time.UTC)

	res := algorithm.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         123456,
		Timestamp:   captureTime,
		TaskType:    algorithm.TaskTypePersonStructure,
		CostMs:      45,
		Persons: []algorithm.PersonItem{
			{
				TrackID:   1001,
				BodyBox:   [4]float64{0.1, 0.2, 0.4, 0.9},
				BodyScore: 0.95,
				Faces: []algorithm.FaceSubItem{
					{
						Box:           [4]float64{0.15, 0.22, 0.35, 0.45},
						Quality:       0.91,
						Matched:       true,
						PersonID:      "person_alice",
						PersonName:    "Alice",
						MatchedFaceID: "face_alice_01",
						Similarity:    0.96,
						Candidates: []algorithm.FaceCandidate{
							{
								PersonID:   "person_alice",
								PersonName: "Alice",
								FaceID:     "face_alice_01",
								Similarity: 0.96,
							},
						},
					},
				},
				Attributes: map[string]any{"gender": "female"},
			},
		},
	}

	// 1. First process: should persist 1 recognition and 1 capture
	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	recs, totalRecs, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalRecs != 1 || len(recs) != 1 {
		t.Fatalf("expected 1 recognition, got %d, err=%v", totalRecs, err)
	}
	r := recs[0]
	// Verify exact captured_at anchor
	if !r.CapturedAt.Equal(captureTime) {
		t.Errorf("expected CapturedAt %v, got %v", captureTime, r.CapturedAt)
	}
	// Verify Option B gallery resolution
	if r.GalleryImagePath != "faces/person_alice_face_alice_01.jpg" {
		t.Errorf("expected galleryImagePath 'faces/person_alice_face_alice_01.jpg', got %q", r.GalleryImagePath)
	}
	if r.MatchedFaceID != "face_alice_01" {
		t.Errorf("expected matchedFaceID 'face_alice_01', got %q", r.MatchedFaceID)
	}
	if len(r.Candidates) != 1 || r.Candidates[0].FaceID != "face_alice_01" {
		t.Errorf("unexpected candidates: %+v", r.Candidates)
	}

	// Verify person capture
	caps, totalCaps, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || totalCaps != 1 || len(caps) != 1 {
		t.Fatalf("expected 1 capture, got %d, err=%v", totalCaps, err)
	}
	if caps[0].TrackID != 1001 || !caps[0].CapturedAt.Equal(captureTime) {
		t.Errorf("unexpected capture: %+v", caps[0])
	}

	// 2. High-FPS repeated calls within cooldown window: should be debounced and NOT insert new records
	for i := 0; i < 5; i++ {
		resRapid := res
		resRapid.Timestamp = captureTime.Add(time.Duration(i*100) * time.Millisecond)
		if err := svc.ProcessInferenceResult(ctx, resRapid); err != nil {
			t.Fatalf("rapid ProcessInferenceResult failed: %v", err)
		}
	}

	_, totalRecsAfterRapid, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalRecsAfterRapid != 1 {
		t.Fatalf("expected still 1 recognition after rapid burst, got %d", totalRecsAfterRapid)
	}

	// 3. Call after cooldown window expires (e.g. 4 seconds later): should insert new record
	resAfterCooldown := res
	resAfterCooldown.Timestamp = captureTime.Add(4 * time.Second)
	if err := svc.ProcessInferenceResult(ctx, resAfterCooldown); err != nil {
		t.Fatalf("ProcessInferenceResult after cooldown failed: %v", err)
	}

	_, totalRecsAfterCooldown, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalRecsAfterCooldown != 2 {
		t.Fatalf("expected 2 recognitions after cooldown passed, got %d", totalRecsAfterCooldown)
	}
}

func TestServiceProcessDetectionsAlarmsAndCaptures(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, dbStore, zap.NewNop())
	t0 := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)

	res := algorithm.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         55555,
		Timestamp:   t0,
		TaskType:    algorithm.TaskTypeDetection,
		Detections: []algorithm.DetectionItem{
			{
				Box:   [4]float64{0.2, 0.2, 0.4, 0.4},
				Label: "no_helmet_alarm",
				Score: 0.92,
			},
			{
				Box:   [4]float64{0.0, 0.0, 1.0, 1.0},
				Label: "smoke_alarm",
				Score: 0.88,
			},
			{
				Box:   [4]float64{0.5, 0.5, 0.8, 0.9},
				Label: "car",
				Score: 0.95,
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult detection failed: %v", err)
	}

	// Alarms
	alarms, totalAlarms, err := svc.ListAlarms(ctx, record.AlarmFilter{CameraID: "cam_01"})
	if err != nil || totalAlarms != 2 || len(alarms) != 2 {
		t.Fatalf("expected 2 alarms, got %d, err=%v", totalAlarms, err)
	}

	// Smoke alarm should have empty crop_path, while no_helmet_alarm has crop_path
	for _, a := range alarms {
		if a.AlarmType == "smoke_alarm" {
			if a.CropPath != "" {
				t.Errorf("expected empty crop_path for smoke alarm, got %q", a.CropPath)
			}
		} else if a.AlarmType == "no_helmet_alarm" {
			if a.CropPath == "" {
				t.Errorf("expected non-empty crop_path for no_helmet_alarm")
			}
		}
	}

	// Captures
	caps, totalCaps, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || totalCaps != 1 || len(caps) != 1 {
		t.Fatalf("expected 1 capture for car, got %d, err=%v", totalCaps, err)
	}
	if caps[0].TargetType != "car" {
		t.Errorf("expected targetType 'car', got %q", caps[0].TargetType)
	}
}
