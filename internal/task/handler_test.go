package task_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/task"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *task.Handler, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc, _, cleanup := setupTestService(t)
	handler := task.NewHandler(svc, zap.NewNop())

	r := gin.New()
	rg := r.Group("/api/v1")
	handler.RegisterRoutes(rg)

	return r, handler, cleanup
}

func TestHandlerTaskEndpoints(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Create Task
	payload := []byte(`{
		"cameraId": "cam_http_1",
		"algorithmId": "algo_helmet",
		"sampleFps": 5,
		"configJson": "{\"threshold\": 0.6}"
	}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}

	var createResp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if createResp.Code != "OK" {
		t.Fatalf("expected code OK, got %s", createResp.Code)
	}

	dataMap, ok := createResp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map data, got %T", createResp.Data)
	}
	taskID := dataMap["id"].(string)

	// 2. Get Task
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// 3. List Tasks
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/tasks?camera_id=cam_http_1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	// 4. Update Task (PUT)
	updatePayload := []byte(`{
		"sampleFps": 10,
		"configJson": "{\"threshold\": 0.9}"
	}`)
	req, _ = http.NewRequest(http.MethodPut, "/api/v1/tasks/"+taskID, bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on PUT, got %d: %s", w.Code, w.Body.String())
	}

	// 5. Delete Task
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on DELETE, got %d", w.Code)
	}

	// 6. Delete again -> 404
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on deleted task, got %d", w.Code)
	}
}

func TestHandlerTaskValidationErrors(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// Missing cameraId -> 422
	payload := []byte(`{
		"algorithmId": "algo_helmet",
		"sampleFps": 5
	}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 UnprocessableEntity, got %d", w.Code)
	}

	// Malformed JSON -> 400
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader([]byte("{invalid-json")))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest, got %d", w.Code)
	}
}

func TestHandlerLegacyAlgorithmTaskEndpoints(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// 1. Create Task via /api/v1/algorithms/tasks
	payload := []byte(`{
		"cameraId": "cam_legacy_1",
		"algorithmId": "algo_helmet",
		"sampleFps": 5,
		"configJson": "{\"threshold\": 0.6}"
	}`)
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/algorithms/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created on legacy endpoint, got %d: %s", w.Code, w.Body.String())
	}

	var createResp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	dataMap := createResp.Data.(map[string]any)
	taskID := dataMap["id"].(string)

	// 2. List Tasks via /api/v1/algorithms/tasks
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/algorithms/tasks?camera_id=cam_legacy_1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on legacy list, got %d", w.Code)
	}

	// 3. Delete Task via /api/v1/algorithms/tasks/:id
	req, _ = http.NewRequest(http.MethodDelete, "/api/v1/algorithms/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on legacy delete, got %d", w.Code)
	}
}
