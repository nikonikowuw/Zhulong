package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

func TestServiceProcessPersonStructureAndCooldown(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Insert dummy person face photo in person_faces for gallery resolution
	if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		VALUES ('face_alice_01', 'person_alice',
		        'faces/person_alice_face_alice_01.jpg', X'00', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert dummy person_face: %v", err)
	}

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)

	captureTime := time.Date(2026, 10, 16, 15, 30, 0, 123000000, time.UTC)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         123456,
		Timestamp:   captureTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		CostMs:      45,
		Persons: []aitypes.PersonItem{
			{
				TrackID:   1001,
				BodyBox:   [4]float64{0.1, 0.2, 0.4, 0.9},
				BodyScore: 0.95,
				Faces: []aitypes.FaceSubItem{
					{
						Box:           [4]float64{0.15, 0.22, 0.35, 0.45},
						Quality:       0.91,
						Matched:       true,
						PersonID:      "person_alice",
						PersonName:    "Alice",
						MatchedFaceID: "face_alice_01",
						Similarity:    0.96,
						Candidates: []aitypes.FaceCandidate{
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
	// The owner is resolved by matched_face_id, so the row id is internal and the
	// business-key snapshot is empty simply because Alice has no key yet.
	if r.PersonID == nil || *r.PersonID != "person_alice" {
		t.Errorf("expected personID 'person_alice', got %v", r.PersonID)
	}
	if r.PersonExternalID != "" {
		t.Errorf("expected empty personExternalId for a person without one, got %q", r.PersonExternalID)
	}

	// Verify person capture
	caps, totalCaps, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || totalCaps != 1 || len(caps) != 1 {
		t.Fatalf("expected 1 capture, got %d, err=%v", totalCaps, err)
	}
	// D3 契约：track_id 由宿主关联器生产并独占，载荷 1001 不进库，落库为宿主正数身份 ID
	if caps[0].TrackID <= 0 || !caps[0].CapturedAt.Equal(captureTime) {
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
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	t0 := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         55555,
		Timestamp:   t0,
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
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

func TestServiceAlarmLabelDiscrimination(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	t0 := time.Date(2026, 10, 16, 17, 0, 0, 0, time.UTC)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         66666,
		Timestamp:   t0,
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.1, 0.1, 0.2, 0.2}, Label: "helmet", Score: 0.9},
			{Box: [4]float64{0.2, 0.2, 0.3, 0.3}, Label: "fire_hydrant", Score: 0.85},
			{Box: [4]float64{0.3, 0.3, 0.4, 0.4}, Label: "fire_extinguisher", Score: 0.88},
			{Box: [4]float64{0.4, 0.4, 0.5, 0.5}, Label: "no_helmet", Score: 0.95},
			{Box: [4]float64{0.5, 0.5, 0.6, 0.6}, Label: "real_fire", Score: 0.99},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	alarms, totalAlarms, err := svc.ListAlarms(ctx, record.AlarmFilter{CameraID: "cam_01"})
	if err != nil {
		t.Fatalf("ListAlarms failed: %v", err)
	}
	if totalAlarms != 2 {
		t.Fatalf("expected 2 alarms (no_helmet, real_fire), got %d: %+v", totalAlarms, alarms)
	}
	alarmTypes := map[string]struct{}{}
	for _, a := range alarms {
		alarmTypes[a.AlarmType] = struct{}{}
	}
	_, hasNoHelmet := alarmTypes["no_helmet"]
	_, hasRealFire := alarmTypes["real_fire"]
	if !hasNoHelmet || !hasRealFire {
		t.Errorf("unexpected alarm types: %+v", alarmTypes)
	}

	// helmet, fire_hydrant, fire_extinguisher should be classified as captures
	caps, totalCaps, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil {
		t.Fatalf("ListCaptures failed: %v", err)
	}
	if totalCaps != 3 {
		t.Fatalf("expected 3 captures (helmet, fire_hydrant, fire_extinguisher), got %d: %+v", totalCaps, caps)
	}
}

// TestProcessPersonStructureResolvesIdentityThroughMatchedFace pins the cross-layer
// identity contract:
//
//   - the runner reports persons.person_id (the external business key),
//   - recognitions.person_id is a foreign key onto the internal persons.id.
//
// Writing the payload straight into the foreign key is what used to make every
// recognition of a keyed person fail the batch insert and vanish silently. The owner is
// therefore resolved from matched_face_id, and the business key is snapshotted.
func TestProcessPersonStructureResolvesIdentityThroughMatchedFace(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const (
		internalID  = "01J8ZK3M9P4T7Q2R5S8V0X1Y2Z"
		businessKey = "EMP-9527"
	)
	if err := dbStore.DB().Exec(
		`INSERT INTO persons (id, person_id, name, created_at, updated_at)
		 VALUES (?, ?, 'Bob', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		internalID, businessKey).Error; err != nil {
		t.Fatalf("insert keyed person: %v", err)
	}
	if err := dbStore.DB().Exec(
		`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		 VALUES ('face_bob_01', ?, 'faces/bob.jpg', X'00', CURRENT_TIMESTAMP)`,
		internalID).Error; err != nil {
		t.Fatalf("insert keyed person face: %v", err)
	}

	svc := record.NewService(store, zap.NewNop())
	captureTime := time.Date(2026, 10, 16, 18, 0, 0, 0, time.UTC)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         90001,
		Timestamp:   captureTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				TrackID: 2001,
				BodyBox: [4]float64{0.1, 0.2, 0.4, 0.9},
				Faces: []aitypes.FaceSubItem{
					{
						Box:           [4]float64{0.15, 0.22, 0.35, 0.45},
						Matched:       true,
						PersonID:      businessKey,
						PersonName:    "Stale Payload Name",
						MatchedFaceID: "face_bob_01",
						Similarity:    0.97,
					},
				},
			},
			{
				// A separate track: the cooldown is per track, so a second face of the
				// same person would be debounced instead of stored.
				TrackID: 2002,
				BodyBox: [4]float64{0.5, 0.5, 0.8, 0.95},
				Faces: []aitypes.FaceSubItem{
					{
						Box:     [4]float64{0.6, 0.6, 0.7, 0.7},
						Matched: false,
					},
				},
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	recs, total, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil {
		t.Fatalf("ListRecognitions failed: %v", err)
	}
	if total != 2 {
		t.Fatalf("expected 2 recognitions (matched + unmatched), got %d: %+v", total, recs)
	}

	var matched, unmatched *record.RecognitionResponse
	for i := range recs {
		if recs[i].Matched {
			matched = &recs[i]
			continue
		}
		unmatched = &recs[i]
	}
	if matched == nil || unmatched == nil {
		t.Fatalf("expected one matched and one unmatched recognition: %+v", recs)
	}

	if matched.PersonID == nil || *matched.PersonID != internalID {
		t.Errorf("expected personID %q (internal row id), got %v", internalID, matched.PersonID)
	}
	if matched.PersonExternalID != businessKey {
		t.Errorf("expected personExternalId %q, got %q", businessKey, matched.PersonExternalID)
	}
	// The local person row is authoritative; a runner holding a stale gallery must not
	// be able to write its own name into the evidence.
	if matched.PersonName != "Bob" {
		t.Errorf("expected personName from the person row, got %q", matched.PersonName)
	}
	if matched.GalleryImagePath != "faces/bob.jpg" {
		t.Errorf("expected galleryImagePath 'faces/bob.jpg', got %q", matched.GalleryImagePath)
	}

	if unmatched.PersonID != nil {
		t.Errorf("expected nil personID for an unmatched face, got %v", *unmatched.PersonID)
	}
	if unmatched.PersonExternalID != "" {
		t.Errorf("expected empty personExternalId for an unmatched face, got %q", unmatched.PersonExternalID)
	}

	// The business key is the reporting identity, so it must be queryable directly.
	byKey, keyTotal, err := svc.ListRecognitions(ctx, record.RecognitionFilter{PersonExternalID: businessKey})
	if err != nil {
		t.Fatalf("ListRecognitions by business key failed: %v", err)
	}
	if keyTotal != 1 || len(byKey) != 1 || byKey[0].ID != matched.ID {
		t.Fatalf("expected the matched recognition by business key, got %d rows: %+v", keyTotal, byKey)
	}

	// Proof the foreign key is satisfied: deleting the person nulls the FK and keeps
	// the business-key snapshot, which is what makes old evidence still reportable.
	if err := dbStore.DB().Exec(`DELETE FROM persons WHERE id = ?`, internalID).Error; err != nil {
		t.Fatalf("delete person: %v", err)
	}
	after, err := svc.GetRecognition(ctx, matched.ID)
	if err != nil {
		t.Fatalf("GetRecognition failed: %v", err)
	}
	if after.PersonID != nil {
		t.Errorf("expected personID to be nulled by ON DELETE SET NULL, got %v", *after.PersonID)
	}
	if after.PersonExternalID != businessKey {
		t.Errorf("expected personExternalId to survive the person deletion, got %q", after.PersonExternalID)
	}
}
