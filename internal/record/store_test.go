package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestStoreRecognitionCRUDAndFilters(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t0 := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(1 * time.Second)
	t2 := t0.Add(2 * time.Second)

	r1 := &record.Recognition{
		ID:               idutil.New(),
		CameraID:         "cam_01",
		AlgorithmID:      strPtr("algo_01"),
		PTS:              1000,
		TrackID:          1,
		PersonID:         strPtr("person_alice"),
		PersonName:       "Alice",
		MatchedFaceID:    "face_alice_01",
		Matched:          true,
		Similarity:       0.95,
		CandidatesJSON:   `[{"person_id":"person_alice","person_name":"Alice","face_id":"face_alice_01","similarity":0.95}]`,
		FaceImagePath:    "snapshots/cam_01/2026-10-16/r1_face.jpg",
		GalleryImagePath: "faces/person_alice_face_alice_01.jpg",
		ImagePath:        "snapshots/cam_01/2026-10-16/r1.jpg",
		BoxJSON:          "[0.1, 0.2, 0.3, 0.4]",
		BodyBoxJSON:      "[0.1, 0.2, 0.5, 0.8]",
		AttributesJSON:   `{"gender":"female"}`,
		CapturedAt:       t0,
	}

	r2 := &record.Recognition{
		ID:             idutil.New(),
		CameraID:       "cam_01",
		AlgorithmID:    strPtr("algo_01"),
		PTS:            2000,
		TrackID:        2,
		PersonID:       nil,
		PersonName:     "",
		Matched:        false,
		Similarity:     0.25,
		CandidatesJSON: "[]",
		ImagePath:      "snapshots/cam_01/2026-10-16/r2.jpg",
		CapturedAt:     t1,
	}

	r3 := &record.Recognition{
		ID:             idutil.New(),
		CameraID:       "cam_02",
		AlgorithmID:    strPtr("algo_01"),
		PTS:            3000,
		TrackID:        3,
		PersonID:       strPtr("person_alice"),
		PersonName:     "Alice",
		MatchedFaceID:  "face_alice_01",
		Matched:        true,
		Similarity:     0.91,
		CandidatesJSON: `[{"person_id":"person_alice","person_name":"Alice","face_id":"face_alice_01","similarity":0.91}]`,
		CapturedAt:     t2,
	}

	// 1. Single create & get
	if err := store.CreateRecognition(ctx, r1); err != nil {
		t.Fatalf("CreateRecognition failed: %v", err)
	}

	got, err := store.GetRecognitionByID(ctx, r1.ID)
	if err != nil || got == nil {
		t.Fatalf("GetRecognitionByID failed: %v, got: %+v", err, got)
	}
	if got.PersonName != "Alice" || got.MatchedFaceID != "face_alice_01" || !got.Matched {
		t.Errorf("unexpected got recognition: %+v", got)
	}
	resp := got.ToResponse()
	if len(resp.Candidates) != 1 || resp.Candidates[0].FaceID != "face_alice_01" {
		t.Errorf("unexpected candidates in response: %+v", resp.Candidates)
	}
	if resp.Attributes["gender"] != "female" {
		t.Errorf("unexpected attributes in response: %+v", resp.Attributes)
	}

	// 2. Batch create
	if err := store.CreateRecognitionBatch(ctx, []*record.Recognition{r2, r3}); err != nil {
		t.Fatalf("CreateRecognitionBatch failed: %v", err)
	}

	// 3. Filter by CameraID
	listCam1, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || total != 2 || len(listCam1) != 2 {
		t.Fatalf("ListRecognitions by camera failed: err=%v, total=%d, len=%d", err, total, len(listCam1))
	}
	// Verify sort order: captured_at DESC (r2 is t1, r1 is t0)
	if listCam1[0].ID != r2.ID || listCam1[1].ID != r1.ID {
		t.Errorf("expected captured_at DESC order, got %s then %s", listCam1[0].ID, listCam1[1].ID)
	}

	// 4. Filter by Matched
	listMatched, totalMatched, err := store.ListRecognitions(ctx, record.RecognitionFilter{Matched: boolPtr(true)})
	if err != nil || totalMatched != 2 || len(listMatched) != 2 {
		t.Fatalf("ListRecognitions by matched failed: total=%d", totalMatched)
	}

	// 5. Filter by Time Window
	start := t0.Add(500 * time.Millisecond)
	end := t2.Add(500 * time.Millisecond)
	listTime, totalTime, err := store.ListRecognitions(ctx, record.RecognitionFilter{StartTime: &start, EndTime: &end})
	if err != nil || totalTime != 2 || len(listTime) != 2 {
		t.Fatalf("ListRecognitions by time window failed: total=%d, err=%v", totalTime, err)
	}

	// 6. Weak Foreign Key ON DELETE SET NULL test
	// Delete person_alice from persons table
	if err := dbStore.DB().Exec("DELETE FROM persons WHERE id = 'person_alice'").Error; err != nil {
		t.Fatalf("delete person_alice failed: %v", err)
	}
	afterDelete, err := store.GetRecognitionByID(ctx, r1.ID)
	if err != nil || afterDelete == nil {
		t.Fatalf("GetRecognitionByID after person delete failed: %v", err)
	}
	if afterDelete.PersonID != nil {
		t.Errorf("expected PersonID to be set to NULL after person deletion, got %v", *afterDelete.PersonID)
	}
	if afterDelete.PersonName != "Alice" {
		t.Errorf("expected snapshot PersonName 'Alice' to be preserved, got %q", afterDelete.PersonName)
	}
}

func TestStoreAlarmCRUDAndFilters(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t0 := time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC)
	t1 := t0.Add(5 * time.Second)

	a1 := &record.Alarm{
		ID:          idutil.New(),
		CameraID:    "cam_01",
		AlgorithmID: strPtr("algo_01"),
		AlarmType:   "no_helmet",
		Score:       0.89,
		BoxJSON:     "[0.2, 0.3, 0.4, 0.5]",
		ImagePath:   "snapshots/cam_01/2026-10-16/a1.jpg",
		CropPath:    "snapshots/cam_01/2026-10-16/a1_crop.jpg",
		ExtraJSON:   `{"helmet_color":"none"}`,
		PTS:         10000,
		CapturedAt:  t0,
	}

	a2 := &record.Alarm{
		ID:          idutil.New(),
		CameraID:    "cam_01",
		AlgorithmID: strPtr("algo_01"),
		AlarmType:   "smoke_detection",
		Score:       0.94,
		BoxJSON:     "[]",
		ImagePath:   "snapshots/cam_01/2026-10-16/a2.jpg",
		CropPath:    "", // scene-wide alarm allows empty crop
		ExtraJSON:   `{"density":"high"}`,
		PTS:         15000,
		CapturedAt:  t1,
	}

	if err := store.CreateAlarm(ctx, a1); err != nil {
		t.Fatalf("CreateAlarm failed: %v", err)
	}
	if err := store.CreateAlarmBatch(ctx, []*record.Alarm{a2}); err != nil {
		t.Fatalf("CreateAlarmBatch failed: %v", err)
	}

	got, err := store.GetAlarmByID(ctx, a1.ID)
	if err != nil || got == nil {
		t.Fatalf("GetAlarmByID failed: %v", err)
	}
	resp := got.ToResponse()
	if resp.AlarmType != "no_helmet" || resp.CropPath != "snapshots/cam_01/2026-10-16/a1_crop.jpg" {
		t.Errorf("unexpected alarm response: %+v", resp)
	}
	if resp.Extra["helmet_color"] != "none" {
		t.Errorf("unexpected extra in alarm response: %+v", resp.Extra)
	}

	// Filter by AlarmType
	list, total, err := store.ListAlarms(ctx, record.AlarmFilter{AlarmType: "smoke_detection"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListAlarms by type failed: total=%d, err=%v", total, err)
	}
	if list[0].CropPath != "" {
		t.Errorf("expected empty crop_path for smoke alarm, got %s", list[0].CropPath)
	}

	// Distinct Alarm Types
	alarmTypes, err := store.ListDistinctAlarmTypes(ctx)
	if err != nil {
		t.Fatalf("ListDistinctAlarmTypes failed: %v", err)
	}
	if len(alarmTypes) != 2 || alarmTypes[0] != "no_helmet" || alarmTypes[1] != "smoke_detection" {
		t.Errorf("unexpected alarm types: %v", alarmTypes)
	}
}

func TestStoreCaptureCRUDAndFilters(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t0 := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(3 * time.Second)

	c1 := &record.Capture{
		ID:             idutil.New(),
		CameraID:       "cam_01",
		TargetType:     "person",
		TrackID:        1001,
		Score:          0.91,
		BoxJSON:        "[0.1, 0.1, 0.4, 0.9]",
		CropPath:       "snapshots/cam_01/2026-10-16/c1_crop.jpg",
		ImagePath:      "snapshots/cam_01/2026-10-16/c1.jpg",
		AttributesJSON: `{"upper_color":"black","lower_color":"blue"}`,
		PTS:            50000,
		CapturedAt:     t0,
	}

	c2 := &record.Capture{
		ID:             idutil.New(),
		CameraID:       "cam_01",
		TargetType:     "vehicle",
		TrackID:        1002,
		Score:          0.85,
		BoxJSON:        "[0.3, 0.3, 0.7, 0.8]",
		CropPath:       "snapshots/cam_01/2026-10-16/c2_crop.jpg",
		ImagePath:      "snapshots/cam_01/2026-10-16/c2.jpg",
		AttributesJSON: `{"license_plate":"京A88888","color":"white"}`,
		PTS:            53000,
		CapturedAt:     t1,
	}

	if err := store.CreateCapture(ctx, c1); err != nil {
		t.Fatalf("CreateCapture failed: %v", err)
	}
	if err := store.CreateCaptureBatch(ctx, []*record.Capture{c2}); err != nil {
		t.Fatalf("CreateCaptureBatch failed: %v", err)
	}

	got, err := store.GetCaptureByID(ctx, c1.ID)
	if err != nil || got == nil {
		t.Fatalf("GetCaptureByID failed: %v", err)
	}
	resp := got.ToResponse()
	if resp.TargetType != "person" || resp.TrackID != 1001 {
		t.Errorf("unexpected capture response: %+v", resp)
	}
	if resp.Attributes["upper_color"] != "black" {
		t.Errorf("unexpected capture attributes: %+v", resp.Attributes)
	}

	// Filter by TargetType
	list, total, err := store.ListCaptures(ctx, record.CaptureFilter{TargetType: "vehicle"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListCaptures by target type failed: total=%d, err=%v", total, err)
	}
	if list[0].TrackID != 1002 {
		t.Errorf("unexpected vehicle track ID: %d", list[0].TrackID)
	}

	// Distinct Target Types
	targetTypes, err := store.ListDistinctTargetTypes(ctx)
	if err != nil {
		t.Fatalf("ListDistinctTargetTypes failed: %v", err)
	}
	if len(targetTypes) != 2 || targetTypes[0] != "person" || targetTypes[1] != "vehicle" {
		t.Errorf("unexpected target types: %v", targetTypes)
	}
}

// TestStoreResolveGalleryFaces pins the mapping the ingest path relies on: a face id
// answers both "which internal person row" and "which business key", in one query.
func TestStoreResolveGalleryFaces(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := dbStore.DB().Exec(
		`INSERT INTO persons (id, person_id, name, created_at, updated_at)
		 VALUES ('person_keyed', 'EMP-1', 'Keyed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert keyed person: %v", err)
	}
	for _, face := range []struct{ id, personID, path string }{
		{"face_01", "person_alice", "faces/alice.jpg"},
		{"face_02", "person_keyed", "faces/keyed.jpg"},
	} {
		if err := dbStore.DB().Exec(
			`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
			 VALUES (?, ?, ?, X'00', CURRENT_TIMESTAMP)`, face.id, face.personID, face.path).Error; err != nil {
			t.Fatalf("insert face %s: %v", face.id, err)
		}
	}

	refs, err := store.ResolveGalleryFaces(ctx, []string{"face_01", "face_01", "face_02", "face_missing", ""})
	if err != nil {
		t.Fatalf("ResolveGalleryFaces failed: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 resolved faces (duplicates collapsed, unknown skipped), got %d: %+v", len(refs), refs)
	}

	got := refs["face_01"]
	if got.PersonRowID != "person_alice" || got.PersonExternalID != "" ||
		got.ImagePath != "faces/alice.jpg" || got.PersonName != "Alice" {
		t.Errorf("unexpected ref for an unkeyed person: %+v", got)
	}
	got = refs["face_02"]
	if got.PersonRowID != "person_keyed" || got.PersonExternalID != "EMP-1" ||
		got.ImagePath != "faces/keyed.jpg" || got.PersonName != "Keyed" {
		t.Errorf("unexpected ref for a keyed person: %+v", got)
	}
}

// TestStoreBackfillPersonExternalID pins that binding a business key retro-fills only the
// empty snapshots, and stays a no-op afterwards.
func TestStoreBackfillPersonExternalID(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	capturedAt := time.Date(2026, 10, 16, 19, 0, 0, 0, time.UTC)
	alice := "person_alice"
	rows := []*record.Recognition{
		{ID: idutil.New(), CameraID: "cam_01", PTS: 1, PersonID: &alice, CapturedAt: capturedAt},
		{ID: idutil.New(), CameraID: "cam_01", PTS: 2, PersonID: &alice, PersonExternalID: "KEEP-ME", CapturedAt: capturedAt},
		{ID: idutil.New(), CameraID: "cam_01", PTS: 3, PersonExternalID: "OTHER", CapturedAt: capturedAt},
	}
	if err := store.CreateRecognitionBatch(ctx, rows); err != nil {
		t.Fatalf("CreateRecognitionBatch failed: %v", err)
	}
	n, err := store.BackfillPersonExternalID(ctx, alice, "EMP-777")
	if err != nil {
		t.Fatalf("BackfillPersonExternalID failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 backfilled row (the empty snapshot), got %d", n)
	}

	for _, tc := range []struct {
		id   string
		want string
	}{
		{rows[0].ID, "EMP-777"},
		{rows[1].ID, "KEEP-ME"},
		{rows[2].ID, "OTHER"},
	} {
		got, err := store.GetRecognitionByID(ctx, tc.id)
		if err != nil {
			t.Fatalf("GetRecognitionByID(%s) failed: %v", tc.id, err)
		}
		if got.PersonExternalID != tc.want {
			t.Errorf("row %s: expected personExternalId %q, got %q", tc.id, tc.want, got.PersonExternalID)
		}
	}

	again, err := store.BackfillPersonExternalID(ctx, alice, "EMP-777")
	if err != nil {
		t.Fatalf("second BackfillPersonExternalID failed: %v", err)
	}
	if again != 0 {
		t.Errorf("expected the backfill to be idempotent, touched %d rows on the second pass", again)
	}

	// An unknown person row is a silent no-op: there is nothing to attribute.
	if n, err := store.BackfillPersonExternalID(ctx, "person_ghost", "EMP-1"); err != nil || n != 0 {
		t.Errorf("expected no-op for a missing person, got n=%d err=%v", n, err)
	}
}

// TestClearRecordSnapshotPathsRejectsUnknownTable 守住「未知表不得静默成功」。
//
// 该函数曾经对未知表直接返回 nil：调用方会认为预填的路径已经清空，而记录里
// 仍留着指向不存在文件的引用。表名来自 snapshotJob，写错一次就只会表现为死链。
func TestClearRecordSnapshotPathsRejectsUnknownTable(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	if err := store.ClearRecordSnapshotPaths(context.Background(), "not_a_records_table", "row_1"); err == nil {
		t.Fatal("expected an error for an unsupported table, got nil")
	}
}
