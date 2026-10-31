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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func setupTrialHandlerTest(t *testing.T) (*gin.Engine, *algorithm.Service, *algorithm.Store, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	algoDir := filepath.Join(tempDir, "algorithms")
	_ = os.MkdirAll(algoDir, 0o755)

	rt := algorunner.Runtime{AlgorithmsDir: algoDir}
	host := algorithm.HostPlatform{Arch: "x86_64", Target: "onnx"}
	comp := algorithm.NewAlgorithmComponents(dbStore, rt, host, zap.NewNop())

	r := gin.New()
	api := r.Group("/api/v1")
	comp.ProtectedRoutes.RegisterRoutes(api)
	comp.UploadRoutes.RegisterRoutes(api)

	return r, comp.Service, comp.Store, algoDir
}

func TestHandlerTrial_Endpoints(t *testing.T) {
	router, _, store, algoDir := setupTrialHandlerTest(t)
	ctx := context.Background()

	algoID := "plate_reader"
	version := "1.0.0"
	pkgDir := filepath.Join(algoDir, algoID, version)
	_ = os.MkdirAll(pkgDir, 0o755)

	scriptPath := filepath.Join(pkgDir, algoID)
	scriptContent := `#!/bin/sh
printf '%s\n' '{"status":"passed","sample":"mock.ppm","sample_decoded":true,"width":800,"height":600,"result_type":4,"count":1,"cost_ms":45,"result":{"stream_id":0,"pts":1000,"capture_time_ms":0,"cost_ms":45,"algorithm_id":"plate_reader","task_type":"vehicle_structure","detections":[],"classifications":[],"persons":[],"vehicles":[{"track_id":0,"vehicle_box":[0.1,0.2,0.8,0.9],"vehicle_score":0.88,"vehicle_type":"suv","vehicle_color":"black","plates":[{"box":[0.3,0.7,0.5,0.8],"score":0.99,"plate_number":"京A88888","plate_color":"蓝色","plate_type":0}]}]}}'
exit 0
`
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0o755); err != nil {
		t.Fatalf("write mock script: %v", err)
	}

	algo := &algorithm.Algorithm{
		ID:           algoID,
		Name:         "Plate Reader",
		Version:      version,
		TaskType:     algorithm.TaskTypeVehicleStructure,
		Category:     algorithm.CategoryTraffic,
		TargetArch:   "x86_64",
		TargetDevice: "onnx",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := store.CreateAlgorithm(ctx, algo); err != nil {
		t.Fatalf("save algo: %v", err)
	}

	// 1. Success case
	pngBytes := createTestPNGBytes(t, 800, 600)
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "test.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write(pngBytes)
	_ = writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+algoID+"/trial", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var envelope struct {
		Code    string                           `json:"code"`
		Message string                           `json:"message"`
		Data    algorithm.AlgorithmTrialResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if envelope.Code != "OK" {
		t.Errorf("expected code OK, got %s", envelope.Code)
	}
	if envelope.Data.CostMs != 45 {
		t.Errorf("expected costMs 45, got %d", envelope.Data.CostMs)
	}
	if len(envelope.Data.Vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %d", len(envelope.Data.Vehicles))
	}
	if len(envelope.Data.Vehicles[0].Plates) != 1 {
		t.Fatalf("expected 1 plate, got %d", len(envelope.Data.Vehicles[0].Plates))
	}
	if envelope.Data.Vehicles[0].Plates[0].PlateNumber != "京A88888" {
		t.Errorf("expected plate 京A88888, got %s", envelope.Data.Vehicles[0].Plates[0].PlateNumber)
	}

	// 2. Missing file part -> 400
	reqEmpty := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/algorithms/"+algoID+"/trial",
		bytes.NewReader([]byte{}),
	)
	reqEmpty.Header.Set("Content-Type", "multipart/form-data; boundary=invalid")
	wEmpty := httptest.NewRecorder()
	router.ServeHTTP(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty multipart, got %d", wEmpty.Code)
	}
}
