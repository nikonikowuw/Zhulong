package algorithm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *algorithm.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	comp := algorithm.NewAlgorithmComponents(dbStore, tempDir, "cpu", zap.NewNop())
	// Use sleep as stub runner in unit tests
	comp.Supervisor = algorithm.NewSupervisor(comp.Store, tempDir, "sleep", "", algorithm.DefaultRecoveryOptions(), zap.NewNop())
	comp.Verifier.RunnerPath = "" // bypass dynamic runner in router unit test

	r := gin.New()
	api := r.Group("/api/v1")
	comp.Handler.RegisterRoutes(api)
	comp.Handler.RegisterUploadRoutes(api)

	return r, comp.Service
}

func TestHandlerListAndInstall(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 1. Initial list should be empty
	req := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected response code OK, got: %s", resp.Code)
	}

	// 2. Install package via multipart upload
	manifest := algorithm.Manifest{
		ID:        "helmet_detector",
		Version:   "1.0.0",
		Name:      "helmet_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Arch: "x86_64", Target: "cpu"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 64},
	}
	manifestBytes, _ := json.Marshal(manifest)

	tarGzPath := createTestTarGz(t, map[string][]byte{
		"manifest.json": manifestBytes,
		"plugin.so":     []byte("elf_stub"),
		"sample.jpg":    []byte("jpg_stub"),
	})
	defer os.Remove(tarGzPath)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(tarGzPath))
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fileBytes, _ := os.ReadFile(tarGzPath)
	_, _ = part.Write(fileBytes)
	writer.Close()

	uploadReq := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/install", body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadW := httptest.NewRecorder()
	router.ServeHTTP(uploadW, uploadReq)

	if uploadW.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d, body: %s", uploadW.Code, uploadW.Body.String())
	}

	// 3. Get installed package
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/helmet_detector", nil)
	getW := httptest.NewRecorder()
	router.ServeHTTP(getW, getReq)
	if getW.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", getW.Code)
	}
}

func TestHandlerAnalysisTasks(t *testing.T) {
	router, svc := setupTestRouter(t)
	ctx := context.Background()

	// Pre-create algorithm
	_, _ = svc.InstallAlgorithm(ctx, createTestTarGz(t, map[string][]byte{
		"manifest.json": []byte(`{"id":"algo_cam","name":"cam_detector","version":"1.0.0","task_type":"detection","category":"safety","platform":{"arch":"x86_64","target":"cpu"},"resources":{"compute_cost":20,"memory_mb":64}}`),
		"plugin.so":     []byte("elf"),
		"sample.jpg":    []byte("jpg"),
	}))

	// 1. Create Analysis Task via /tasks with algorithm_id
	tBody := bytes.NewReader([]byte(`{"camera_id":"cam_01","algorithm_id":"algo_cam","sample_fps":5}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/tasks", tBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d, body: %s", w.Code, w.Body.String())
	}

	// 2. List tasks via /tasks
	req = httptest.NewRequest(http.MethodGet, "/api/v1/algorithms/tasks?camera_id=cam_01", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	// 3. Delete Task
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/algorithms/tasks/task_cam_01_algo_cam", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on delete task, got: %d", w.Code)
	}

	// 4. Delete Non-existent Task returns 404
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/algorithms/tasks/not_found_task_id", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found on deleting non-existent task, got: %d", w.Code)
	}
}
