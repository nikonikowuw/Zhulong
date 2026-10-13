package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/mediafile"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"go.uber.org/zap"
)

// TestMediaRoutesProduceNoAuditRecords pins the audit boundary of the media
// transfer endpoint.
//
// Media reads declare no audit action, so they produce no records. That is a
// deliberate decision rather than an oversight: the audit table keeps a rolling
// 5000-entry window, and a single video preview issues tens to hundreds of range
// requests. Auditing them would evict the write operations the audit log exists
// to preserve.
//
// The test runs against the real router and the real audit middleware rather
// than a stand-in, and it asserts a positive control in the same run: without
// one, "no records" cannot be distinguished from "the middleware was never
// mounted".
func TestMediaRoutesProduceNoAuditRecords(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("database migration failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Stop(context.Background()) })

	mediaRoot := t.TempDir()
	mediaFile := filepath.Join(mediaRoot, "exports", "task-1.mp4")
	if err := os.MkdirAll(filepath.Dir(mediaFile), 0o755); err != nil {
		t.Fatalf("create media dir: %v", err)
	}
	if err := os.WriteFile(mediaFile, []byte("0123456789abcdef"), 0o644); err != nil {
		t.Fatalf("write media fixture: %v", err)
	}

	storageSvc := storage.NewService(
		storage.NewRepository(dbStore),
		storage.NewDefaultPathInspector(),
		nil,
		nil,
		zap.NewNop(),
	)
	// Point the single source of truth for the media root at the fixture.
	cfg := storage.DefaultStorageConfig()
	cfg.MediaDirectory = mediaRoot
	if _, err := storageSvc.UpdateConfig(context.Background(), cfg, "tester", "127.0.0.1"); err != nil {
		t.Fatalf("configure media root: %v", err)
	}

	mediaComp := mediafile.NewMediaFileComponents(
		mediafile.NewMediaRootProvider(storageSvc),
		mediafile.NewDeviceInspector(storage.NewDefaultPathInspector()),
		zap.NewNop(),
	)

	// The storage handler is the positive control: it declares audited actions,
	// so it proves the middleware chain is live in this router.
	storageComp := storage.NewStorageComponents(dbStore, auditSvc, zap.NewNop())

	assets := http.FS(fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("shell")},
	})

	router := newRouter(RouterParams{
		Logger:          zap.NewNop(),
		DatabaseReady:   readinessStub(true),
		NativeReady:     readinessStub(true),
		AuditService:    auditSvc,
		ProtectedRoutes: []RouteRegistrar{mediaComp.ProtectedRoutes, storageComp.ProtectedRoutes},
		Assets:          assets,
	})

	// A range request, the shape a real player issues repeatedly.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/media/content/exports/task-1.mp4", nil)
	req.Header.Set("Range", "bytes=0-3")
	req.RemoteAddr = "192.168.12.12:9999"
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("expected 206 for the range request, got %d: %s", recorder.Code, recorder.Body.String())
	}

	// Also exercise the metadata endpoint and the root probe: all three must
	// stay silent.
	for _, target := range []string{
		"/api/v1/media/entries/exports/task-1.mp4",
		"/api/v1/media/roots",
	} {
		probe := httptest.NewRequest(http.MethodGet, target, nil)
		probe.RemoteAddr = "192.168.12.12:9999"
		probeRecorder := httptest.NewRecorder()
		router.ServeHTTP(probeRecorder, probe)
		if probeRecorder.Code != http.StatusOK {
			t.Fatalf("expected 200 for %s, got %d: %s", target, probeRecorder.Code, probeRecorder.Body.String())
		}
	}

	// Positive control: an audited write must still land, proving the chain is
	// mounted and the empty result above is meaningful.
	controlBody, err := json.Marshal(map[string]any{
		"mediaDirectory":          mediaRoot,
		"recordingsRetentionDays": 15,
		"snapshotsRetentionDays":  90,
		"exportsRetentionHours":   48,
		"highWatermarkPercent":    90,
		"lowWatermarkPercent":     80,
		"emergencyStopPercent":    95,
		"emergencyStopMinMB":      2048,
	})
	if err != nil {
		t.Fatalf("encode control body: %v", err)
	}
	control := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(controlBody))
	control.Header.Set("Content-Type", "application/json")
	control.RemoteAddr = "192.168.12.12:9999"
	controlRecorder := httptest.NewRecorder()
	router.ServeHTTP(controlRecorder, control)

	entries := drainAuditRouter(t, auditSvc)

	for _, entry := range entries {
		if len(entry.Action) >= len("media") && entry.Action[:len("media")] == "media" {
			t.Errorf("media read produced an audit record: %+v", entry)
		}
	}

	var sawControl bool
	for _, entry := range entries {
		if entry.Action == storage.ActionStorageUpdateConfig {
			sawControl = true
		}
	}
	if !sawControl {
		t.Fatalf("positive control failed: no storage update record, so the audit chain was not mounted (entries: %+v)", entries)
	}
}
