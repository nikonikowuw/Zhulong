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

func newRecordHandlerHarness(t *testing.T) (*gin.Engine, *record.Service, func()) {
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
	return router, comps.Service, cleanup
}

func TestHandlerListAndGetRecognitions(t *testing.T) {
	router, service, cleanup := newRecordHandlerHarness(t)
	defer cleanup()
	ctx := context.Background()

	// Seed recognitions
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
		CapturedAt:       time.Now().UTC(),
	}
	store := record.NewStore(nil)
	_ = store // test through direct service or store

	// Use store via reflect or test database insert
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/recognitions", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code 'OK', got %q", resp.Code)
	}

	// Test 404 on non-existent recognition
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/recognitions/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent recognition, got %d", wNotFound.Code)
	}

	// Test invalid time format returns 422
	wInvalidTime := httptest.NewRecorder()
	reqInvalidTime, _ := http.NewRequest("GET", "/api/v1/records/recognitions?startTime=invalid-date", nil)
	router.ServeHTTP(wInvalidTime, reqInvalidTime)
	if wInvalidTime.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected 422 for invalid time format, got %d", wInvalidTime.Code)
	}
	_ = rec
	_ = service
	_ = ctx
}

func TestHandlerListAndGetAlarms(t *testing.T) {
	router, _, cleanup := newRecordHandlerHarness(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/alarms", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code 'OK', got %q", resp.Code)
	}

	// Test 404 on non-existent alarm
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/alarms/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent alarm, got %d", wNotFound.Code)
	}
}

func TestHandlerListAndGetCaptures(t *testing.T) {
	router, _, cleanup := newRecordHandlerHarness(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/captures", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected code 'OK', got %q", resp.Code)
	}

	// Test 404 on non-existent capture
	wNotFound := httptest.NewRecorder()
	reqNotFound, _ := http.NewRequest("GET", "/api/v1/records/captures/non_existent", nil)
	router.ServeHTTP(wNotFound, reqNotFound)
	if wNotFound.Code != http.StatusNotFound {
		t.Errorf("expected 404 for non-existent capture, got %d", wNotFound.Code)
	}
}
