package task_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/task"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *task.Handler, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	svc, _, cleanup := setupTestService(t)
	handler := task.NewHandler(svc)

	r := gin.New()
	rg := r.Group("/api/v1")
	handler.RegisterRoutes(rg)

	return r, handler, cleanup
}

func TestHandlerTaskEndpoints(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	payload := []byte(`{
		"cameraId": "cam_http_1",
		"algorithmId": "algo_helmet",
		"sampleFps": 5,
		"configJson": "{\"threshold\": 0.6}"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
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

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tasks?camera_id=cam_http_1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", w.Code)
	}

	updatePayload := []byte(`{
		"sampleFps": 10,
		"configJson": "{\"threshold\": 0.9}"
	}`)
	req = httptest.NewRequest(http.MethodPut, "/api/v1/tasks/"+taskID, bytes.NewReader(updatePayload))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on PUT, got %d: %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on DELETE, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/tasks/"+taskID, nil)
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 UnprocessableEntity, got %d", w.Code)
	}

	// Malformed JSON -> 400
	req = httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader([]byte("{invalid-json")))
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

	payload := []byte(`{
		"cameraId": "cam_legacy_1",
		"algorithmId": "algo_helmet",
		"sampleFps": 5,
		"configJson": "{\"threshold\": 0.6}"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/tasks", bytes.NewReader(payload))
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

	req = httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/tasks?camera_id=cam_legacy_1", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on legacy list, got %d", w.Code)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/algorithms/tasks/"+taskID, nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on legacy delete, got %d", w.Code)
	}
}

func TestHandlerSaveTaskByCameraUpsert(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	payload := []byte(`{
		"algorithms": [{"algorithmId": "algo_helmet", "sampleFps": 5}]
	}`)
	put := func(body []byte) *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPut,
			"/api/v1/tasks/camera/cam_http_upsert",
			bytes.NewReader(body),
		)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	if w := put(payload); w.Code != http.StatusOK {
		t.Fatalf("expected 200 on first upsert, got %d: %s", w.Code, w.Body.String())
	}

	updatedPayload := []byte(`{
		"algorithms": [{"algorithmId": "algo_helmet", "sampleFps": 12}]
	}`)
	w := put(updatedPayload)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on second upsert, got %d: %s", w.Code, w.Body.String())
	}

	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	dataMap, ok := body.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected object data, got %T", body.Data)
	}
	algorithms, ok := dataMap["algorithms"].([]any)
	if !ok || len(algorithms) != 1 {
		t.Fatalf("expected one algorithm in response, got %v", dataMap["algorithms"])
	}
	first, ok := algorithms[0].(map[string]any)
	if !ok {
		t.Fatalf("expected algorithm map, got %T", algorithms[0])
	}
	fps, ok := first["sampleFps"].(float64)
	if !ok || fps != 12 {
		t.Fatalf("expected sampleFps 12 after upsert, got %v", first["sampleFps"])
	}

	// GET /tasks/camera/:cameraId 必须命中同一份数据。
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks/camera/cam_http_upsert", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on GetTaskByCamera, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandlerSaveTaskByCameraRejectsMalformedJSON(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/tasks/camera/cam_http_bad",
		bytes.NewReader([]byte("{not-json")),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest, got %d", w.Code)
	}
	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "INVALID_REQUEST" {
		t.Fatalf("expected INVALID_REQUEST, got %q", body.Code)
	}
}

func TestHandlerUpdateTaskRejectsMalformedJSON(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/tasks/task_bad_body",
		bytes.NewReader([]byte("{not-json")),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 BadRequest, got %d", w.Code)
	}
}

func TestHandlerTaskNotFound(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	tests := []struct {
		name   string
		method string
		path   string
		body   []byte
	}{
		{name: "get by unknown id", method: http.MethodGet, path: "/api/v1/tasks/task_missing"},
		{name: "get by unknown camera", method: http.MethodGet, path: "/api/v1/tasks/camera/cam_missing"},
		{
			name:   "update unknown id",
			method: http.MethodPut,
			path:   "/api/v1/tasks/task_missing",
			body:   []byte(`{"name": "x"}`),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var bodyReader io.Reader
			if test.body != nil {
				bodyReader = bytes.NewReader(test.body)
			}
			req := httptest.NewRequest(test.method, test.path, bodyReader)
			if test.body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			if w.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
			}
			var body httputil.Response
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Code != "TASK_NOT_FOUND" {
				t.Fatalf("expected TASK_NOT_FOUND, got %q", body.Code)
			}
			if body.Data != nil {
				t.Fatalf("expected null data on error, got %v", body.Data)
			}
		})
	}
}

func TestHandlerListTasksAcceptsCamelCaseFilter(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	payload := []byte(`{"cameraId": "cam_alias", "algorithmId": "algo_helmet", "sampleFps": 5}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed task failed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/tasks?cameraId=cam_alias", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	list, ok := body.Data.([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("expected 1 task for camelCase filter, got %v", body.Data)
	}
}

func TestHandlerSaveTaskByCameraRejectsBudgetOverflow(t *testing.T) {
	r, _, cleanup := setupTestRouter(t)
	defer cleanup()

	// algo_helmet 单点算力 20，100 fps = 2,000 点，超预算池。
	payload := []byte(`{"algorithms": [{"algorithmId": "algo_helmet", "sampleFps": 100}]}`)
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/tasks/camera/cam_http_budget",
		bytes.NewReader(payload),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d: %s", w.Code, w.Body.String())
	}
	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "COMPUTE_BUDGET_EXCEEDED" {
		t.Fatalf("expected COMPUTE_BUDGET_EXCEEDED, got %q", body.Code)
	}
}
