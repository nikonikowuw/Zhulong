package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestHandler(t *testing.T) (*gin.Engine, *Service, string, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	repo := NewRepository(dbStore)
	mediaDir := filepath.Join(tempDir, "media")
	_ = os.MkdirAll(mediaDir, 0o750)

	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	_, _ = repo.UpdateConfig(context.Background(), initCfg)

	inspector := NewDefaultPathInspector()
	gate := NewEmergencyGate(zap.NewNop())
	cleaner := NewCleanerEngine(inspector, zap.NewNop())

	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, zap.NewNop())

	svc := NewService(repo, inspector, gate, cleaner, zap.NewNop())
	svc.SetAuditor(auditSvc)
	_ = svc.Start(context.Background())

	handler := NewHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	cleanup := func() {
		_ = svc.Stop(context.Background())
		_ = dbStore.Close()
	}

	return router, svc, mediaDir, cleanup
}

func TestHandler_GetStatus(t *testing.T) {
	router, _, mediaDir, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/storage/status", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Code != "OK" {
		t.Errorf("expected OK, got %s", resp.Code)
	}

	dataMap, ok := resp.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map in data, got %T", resp.Data)
	}
	if dataMap["mediaDirectory"] != mediaDir {
		t.Errorf("expected mediaDirectory %q, got %v", mediaDir, dataMap["mediaDirectory"])
	}
}

func TestHandler_GetConfig(t *testing.T) {
	router, _, mediaDir, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/storage/config", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	dataMap := resp.Data.(map[string]any)
	if dataMap["mediaDirectory"] != mediaDir {
		t.Errorf("expected mediaDirectory %q, got %v", mediaDir, dataMap["mediaDirectory"])
	}
}

func TestHandler_UpdateConfig(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tempDir := t.TempDir()
	newMedia := filepath.Join(tempDir, "updated_media")

	payload := UpdateConfigRequest{
		MediaDirectory:          newMedia,
		RecordingsRetentionDays: 20,
		SnapshotsRetentionDays:  100,
		ExportsRetentionHours:   36,
		HighWatermarkPercent:    85,
		LowWatermarkPercent:     70,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      2048,
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	dataMap := resp.Data.(map[string]any)
	if dataMap["mediaDirectory"] != newMedia {
		t.Errorf("expected updated media dir %q, got %v", newMedia, dataMap["mediaDirectory"])
	}

	// Test invalid payload (low watermark >= high watermark)
	invalidPayload := payload
	invalidPayload.LowWatermarkPercent = 90
	invalidPayload.HighWatermarkPercent = 80
	invBody, _ := json.Marshal(invalidPayload)

	wInv := httptest.NewRecorder()
	reqInv := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(invBody))
	reqInv.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wInv, reqInv)

	if wInv.Code != http.StatusBadRequest && wInv.Code != http.StatusUnprocessableEntity {
		t.Errorf("expected error status for invalid config, got %d", wInv.Code)
	}
}

func TestHandler_TestPath(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	tempDir := t.TempDir()
	payload := PathTestRequest{
		Path: tempDir,
	}
	body, _ := json.Marshal(payload)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	dataMap := resp.Data.(map[string]any)
	if dataMap["writable"] != true {
		t.Errorf("expected writable = true, got %v", dataMap["writable"])
	}
}

func TestHandler_TriggerCleanup(t *testing.T) {
	router, _, _, cleanup := setupTestHandler(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/cleanup", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d: %s", w.Code, w.Body.String())
	}

	var resp httputil.Response
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Code != "OK" {
		t.Errorf("expected OK, got %s", resp.Code)
	}
}
