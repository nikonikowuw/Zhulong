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
	"time"

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

// TestHandler_UserAuditUsesAuthenticatedActor is the regression guard for the
// hardcoded "admin" attribution: the audit record for a user-initiated storage
// operation must carry the authenticated account name, never a fixed one.
func TestHandler_UserAuditUsesAuthenticatedActor(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer dbStore.Close()

	repo := NewRepository(dbStore)
	mediaDir := filepath.Join(tempDir, "media")
	if err := os.MkdirAll(mediaDir, 0o750); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	if _, err := repo.UpdateConfig(context.Background(), initCfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	defer func() { _ = auditSvc.Stop(context.Background()) }()

	svc := NewService(repo, NewDefaultPathInspector(), NewEmergencyGate(zap.NewNop()),
		NewCleanerEngine(NewDefaultPathInspector(), zap.NewNop()), zap.NewNop())
	svc.SetAuditor(auditSvc)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start storage service: %v", err)
	}
	defer func() { _ = svc.Stop(context.Background()) }()

	handler := NewHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api/v1")
	// Stand in for auth.RequireAuth: attach the authenticated actor to the
	// standard request context exactly as the real middleware does.
	api.Use(func(c *gin.Context) {
		actor := audit.Actor{IP: c.ClientIP(), Username: "operator", Source: audit.SourceWeb}
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), actor))
		c.Next()
	})
	handler.RegisterRoutes(api)

	payload := UpdateConfigRequest{
		MediaDirectory:          mediaDir,
		RecordingsRetentionDays: 10,
		SnapshotsRetentionDays:  5,
		ExportsRetentionHours:   12,
		HighWatermarkPercent:    80,
		LowWatermarkPercent:     60,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      100,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.4.4:6666"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	entries, total, err := auditSvc.List(context.Background(), audit.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 audit entry, got %d", total)
	}

	entry := entries[0]
	if entry.Username != "operator" {
		t.Errorf("expected the authenticated account name, got %q", entry.Username)
	}
	if entry.Username == "admin" {
		t.Error("the audit record must not be attributed to a hardcoded admin")
	}
	if entry.IP != "192.168.4.4" {
		t.Errorf("expected the request IP, got %q", entry.IP)
	}
}

// TestHandler_UserAuditWithoutActorIsAnonymous pins the fallback: an
// unauthenticated caller must be recorded as anonymous, not as a fabricated
// account name.
func TestHandler_UserAuditWithoutActorIsAnonymous(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer dbStore.Close()

	repo := NewRepository(dbStore)
	mediaDir := filepath.Join(tempDir, "media")
	if err := os.MkdirAll(mediaDir, 0o750); err != nil {
		t.Fatalf("mkdir media: %v", err)
	}
	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	if _, err := repo.UpdateConfig(context.Background(), initCfg); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	defer func() { _ = auditSvc.Stop(context.Background()) }()

	svc := NewService(repo, NewDefaultPathInspector(), NewEmergencyGate(zap.NewNop()),
		NewCleanerEngine(NewDefaultPathInspector(), zap.NewNop()), zap.NewNop())
	svc.SetAuditor(auditSvc)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start storage service: %v", err)
	}
	defer func() { _ = svc.Stop(context.Background()) }()

	handler := NewHandler(svc, zap.NewNop())

	gin.SetMode(gin.TestMode)
	router := gin.New()
	api := router.Group("/api/v1")
	// No actor middleware: this request is unattributed.
	handler.RegisterRoutes(api)

	payload := UpdateConfigRequest{
		MediaDirectory:          mediaDir,
		RecordingsRetentionDays: 10,
		SnapshotsRetentionDays:  5,
		ExportsRetentionHours:   12,
		HighWatermarkPercent:    80,
		LowWatermarkPercent:     60,
		EmergencyStopPercent:    95,
		EmergencyStopMinMB:      100,
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	entries, _, err := auditSvc.List(context.Background(), audit.Filter{Limit: 10})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry, got %d", len(entries))
	}
	if entries[0].Username != audit.UsernameAnonymous {
		t.Errorf("expected %q, got %q", audit.UsernameAnonymous, entries[0].Username)
	}
	if entries[0].Username == "admin" {
		t.Error("an unattributed request must not be recorded as admin")
	}
}
