package record_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

func newRecordHandlerHarness(t *testing.T) (*gin.Engine, *record.Service, *record.Store, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	db := dbStore.DB()
	if err := db.Exec(`INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at) 
		VALUES ('cam_01', 'Cam 1', 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert camera: %v", err)
	}
	if err := db.Exec(`INSERT INTO algorithms (id, name, version, task_type, category, target_arch, target_device, compute_cost, memory_mb, description, manifest_json, config_schema_json, status, previous_version, created_at, updated_at)
		VALUES ('algo_01', 'TestAlgo', '1.0.0', 'person_structure', 'face', 'x86_64', 'cpu', 10, 64, 'test', '{}', '{}', 'ENABLED', '', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert algorithm: %v", err)
	}

	comps := record.NewRecordComponents(dbStore, zap.NewNop())
	router := gin.New()
	api := router.Group("/api/v1")
	comps.ProtectedRoutes.RegisterRoutes(api)

	cleanup := func() {
		_ = dbStore.Close()
	}
	return router, comps.Service, comps.Store, cleanup
}

func TestHandlerListAndGetRecognitions(t *testing.T) {
	router, _, store, cleanup := newRecordHandlerHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Seed recognition record
	recID := idutil.New()
	rec := &record.Recognition{
		ID:               recID,
		CameraID:         "cam_01",
		AlgorithmID:      "algo_01",
		PTS:              1000,
		TrackID:          1,
		PersonName:       "Alice",
		MatchedFaceID:    "face_01",
		Matched:          true,
		Similarity:       0.95,
		CandidatesJSON:   `[{"person_id":"p1","person_name":"Alice","face_id":"face_01","similarity":0.95}]`,
		FaceImagePath:    "snapshots/cam_01/2026-10-16/rec_face.jpg",
		GalleryImagePath: "faces/alice.jpg",
		ImagePath:        "snapshots/cam_01/2026-10-16/rec.jpg",
		BoxJSON:          `[0.1,0.2,0.3,0.4]`,
		BodyBoxJSON:      `[0.1,0.2,0.5,0.8]`,
		AttributesJSON:   `{"gender":"female"}`,
		CapturedAt:       time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC),
	}
	if err := store.CreateRecognition(ctx, rec); err != nil {
		t.Fatalf("CreateRecognition failed: %v", err)
	}

	// 1. List recognitions
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/recognitions?cameraId=cam_01", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code 'OK', got %q", resp.Code)
	}

	// 2. Get single recognition by ID
	wGet := httptest.NewRecorder()
	reqGet, _ := http.NewRequest("GET", "/api/v1/records/recognitions/"+recID, nil)
	router.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GetRecognition, got %d: %s", wGet.Code, wGet.Body.String())
	}

	var getResp struct {
		Code string                     `json:"code"`
		Data record.RecognitionResponse `json:"data"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal get response: %v", err)
	}
	if getResp.Data.ID != recID || getResp.Data.PersonName != "Alice" {
		t.Errorf("unexpected record data: %+v", getResp.Data)
	}
	if len(getResp.Data.Candidates) != 1 || getResp.Data.Candidates[0].FaceID != "face_01" {
		t.Errorf("unexpected candidates: %+v", getResp.Data.Candidates)
	}

	// 3. Test 404 on non-existent recognition
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/recognitions/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent recognition, got %d", wNotFound.Code)
	}

	// 4. Test invalid time format returns 422
	wInvalidTime := httptest.NewRecorder()
	reqInvalidTime, _ := http.NewRequest("GET", "/api/v1/records/recognitions?startTime=invalid-date", nil)
	router.ServeHTTP(wInvalidTime, reqInvalidTime)
	if wInvalidTime.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for invalid time format, got %d", wInvalidTime.Code)
	}

	// 5. Test invalid query param binding returns 400
	wInvalidQuery := httptest.NewRecorder()
	reqInvalidQuery, _ := http.NewRequest("GET", "/api/v1/records/recognitions?page=abc", nil)
	router.ServeHTTP(wInvalidQuery, reqInvalidQuery)
	if wInvalidQuery.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid query int binding, got %d: %s", wInvalidQuery.Code, wInvalidQuery.Body.String())
	}
}

func TestHandlerListAndGetAlarms(t *testing.T) {
	router, _, store, cleanup := newRecordHandlerHarness(t)
	defer cleanup()
	ctx := context.Background()

	alarmID := idutil.New()
	alarm := &record.Alarm{
		ID:          alarmID,
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		AlarmType:   "smoke_detection",
		Score:       0.94,
		BoxJSON:     `[0,0,1,1]`,
		ImagePath:   "snapshots/cam_01/2026-10-16/a1.jpg",
		CropPath:    "",
		ExtraJSON:   `{"area":0.8}`,
		PTS:         2000,
		CapturedAt:  time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC),
	}
	if err := store.CreateAlarm(ctx, alarm); err != nil {
		t.Fatalf("CreateAlarm failed: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/alarms?alarmType=smoke_detection", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	wGet := httptest.NewRecorder()
	reqGet, _ := http.NewRequest("GET", "/api/v1/records/alarms/"+alarmID, nil)
	router.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GetAlarm, got %d: %s", wGet.Code, wGet.Body.String())
	}

	var getResp struct {
		Code string               `json:"code"`
		Data record.AlarmResponse `json:"data"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal alarm response: %v", err)
	}
	if getResp.Data.ID != alarmID || getResp.Data.AlarmType != "smoke_detection" {
		t.Errorf("unexpected alarm data: %+v", getResp.Data)
	}

	// Test 404 on non-existent alarm
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/alarms/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent alarm, got %d", wNotFound.Code)
	}

	// Test alarm-types endpoint
	wAlarmTypes := httptest.NewRecorder()
	reqAlarmTypes, _ := http.NewRequest("GET", "/api/v1/records/alarms/alarm-types", nil)
	router.ServeHTTP(wAlarmTypes, reqAlarmTypes)
	if wAlarmTypes.Code != http.StatusOK {
		t.Errorf("expected 200 for alarm-types, got %d", wAlarmTypes.Code)
	}
}

func TestHandlerListAndGetCaptures(t *testing.T) {
	router, _, store, cleanup := newRecordHandlerHarness(t)
	defer cleanup()
	ctx := context.Background()

	capID := idutil.New()
	capRecord := &record.Capture{
		ID:             capID,
		CameraID:       "cam_01",
		TargetType:     "person",
		TrackID:        123,
		Score:          0.91,
		BoxJSON:        `[0.1,0.2,0.3,0.4]`,
		CropPath:       "snapshots/cam_01/2026-10-16/c1_crop.jpg",
		ImagePath:      "snapshots/cam_01/2026-10-16/c1.jpg",
		AttributesJSON: `{"color":"blue"}`,
		PTS:            3000,
		CapturedAt:     time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC),
	}
	if err := store.CreateCapture(ctx, capRecord); err != nil {
		t.Fatalf("CreateCapture failed: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/captures?targetType=person", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	wGet := httptest.NewRecorder()
	reqGet, _ := http.NewRequest("GET", "/api/v1/records/captures/"+capID, nil)
	router.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 for GetCapture, got %d: %s", wGet.Code, wGet.Body.String())
	}

	var getResp struct {
		Code string                 `json:"code"`
		Data record.CaptureResponse `json:"data"`
	}
	if err := json.Unmarshal(wGet.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("unmarshal capture response: %v", err)
	}
	if getResp.Data.ID != capID || getResp.Data.TargetType != "person" {
		t.Errorf("unexpected capture data: %+v", getResp.Data)
	}

	// Test 404 on non-existent capture
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/captures/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent capture, got %d", wNotFound.Code)
	}

	// Test target-types endpoint
	wTargetTypes := httptest.NewRecorder()
	reqTargetTypes, _ := http.NewRequest("GET", "/api/v1/records/captures/target-types", nil)
	router.ServeHTTP(wTargetTypes, reqTargetTypes)
	if wTargetTypes.Code != http.StatusOK {
		t.Errorf("expected 200 for target-types, got %d", wTargetTypes.Code)
	}
}
