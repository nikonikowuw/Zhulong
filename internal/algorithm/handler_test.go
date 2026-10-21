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
	"gorm.io/gorm"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *algorithm.Service) {
	t.Helper()
	router, svc, _ := newAlgorithmTestApp(t)
	return router, svc
}

// newAlgorithmTestApp 是测试装配的内核：除 router 与 service 外还回传 *gorm.DB，
// 让需要直接播种数据的用例（如影响面统计）不必穿透 Service 的内部字段。
func newAlgorithmTestApp(t *testing.T) (*gin.Engine, *algorithm.Service, *gorm.DB) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	comp := algorithm.NewAlgorithmComponents(dbStore, tempDir, "cpu", zap.NewNop())
	comp.Verifier.HostArch = "x86_64"
	// Use sleep as stub runner in unit tests
	comp.Supervisor = algorithm.NewSupervisor(comp.Store, tempDir, "sleep", algorithm.DefaultRecoveryOptions(), zap.NewNop())
	comp.Verifier.RunnerPath = "" // bypass dynamic runner in router unit test

	r := gin.New()
	api := r.Group("/api/v1")
	comp.Handler.RegisterRoutes(api)
	comp.Handler.RegisterUploadRoutes(api)

	return r, comp.Service, dbStore.DB()
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
