package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type remoteHandlerEnv struct {
	router      *gin.Engine
	service     *Service
	repo        Repository
	auditStore  *audit.Store
	asyncSvc    *asyncjob.Service
	logs        *observer.ObservedLogs
	remoteStore *filestore.MemoryDriver
	mediaDir    string
	spoolDir    string
}

func setupRemoteHandlerEnv(t *testing.T) (*remoteHandlerEnv, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	cipher := newTestAESGCMCipher(t, true)
	repo := NewRepositoryWithCipher(dbStore, cipher, zap.NewNop())
	mediaDir := filepath.Join(tempDir, "media")
	spoolDir := filepath.Join(tempDir, "spool")
	mustMkdirAll(t, mediaDir)
	mustMkdirAll(t, spoolDir)

	initCfg := DefaultStorageConfig()
	initCfg.MediaDirectory = mediaDir
	initCfg.SpoolDirectory = spoolDir
	if _, err := repo.UpdateConfig(context.Background(), initCfg); err != nil {
		t.Fatalf("init storage config: %v", err)
	}

	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	inspector := NewDefaultPathInspector()
	gate := NewEmergencyGate(logger)
	cleaner := NewCleanerEngine(inspector, logger)
	auditStore := audit.NewStore(dbStore)
	auditSvc := audit.NewService(auditStore, logger, audit.Config{
		BufferSize:    100,
		BatchSize:     1,
		FlushInterval: 5 * time.Millisecond,
		MaxEntries:    1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}

	svc := NewService(ServiceDeps{
		Repo:      repo,
		Inspector: inspector,
		Gate:      gate,
		Cleaner:   cleaner,
		Cipher:    cipher,
		Logger:    logger,
	})
	svc.SetAuditor(auditSvc)
	svc.s3Probe = func(_ context.Context, _ filestore.S3Config) error { return nil }
	svc.s3Health = func(_ context.Context, _ filestore.S3Config) (filestore.HealthStatus, error) {
		return filestore.HealthStatus{Backend: filestore.BackendS3, Available: true}, nil
	}

	asyncComp := asyncjob.NewAsyncJobComponents(dbStore, logger)
	svc.SetAsyncJobs(asyncComp.Service)
	remoteStore := filestore.NewMemoryDriver()
	svc.SetRemoteStore(remoteStore)

	handler := NewHandler(svc, logger)
	router := gin.New()
	v1 := router.Group("/api/v1")
	handler.RegisterRoutes(v1)

	cleanup := func() {
		if err := auditSvc.Stop(context.Background()); err != nil {
			t.Logf("stop auditSvc: %v", err)
		}
		if err := dbStore.Close(); err != nil {
			t.Logf("close dbStore: %v", err)
		}
	}

	return &remoteHandlerEnv{
		router:      router,
		service:     svc,
		repo:        repo,
		auditStore:  auditStore,
		asyncSvc:    asyncComp.Service,
		logs:        logs,
		remoteStore: remoteStore,
		mediaDir:    mediaDir,
		spoolDir:    spoolDir,
	}, cleanup
}

func assertNoSecretLeak(t *testing.T, env *remoteHandlerEnv, secret string, bodies ...string) {
	t.Helper()
	for i, body := range bodies {
		if strings.Contains(body, secret) {
			t.Fatalf(
				"HTTP response #%d leaked secret key %q: %s",
				i,
				secret,
				body,
			)
		}
		if strings.Contains(body, "X-Amz-Signature=super-secret-sig") {
			t.Fatalf("HTTP response #%d leaked presigned signature: %s", i, body)
		}
	}
	for _, entry := range env.logs.All() {
		line := entry.Message
		for _, f := range entry.Context {
			if strings.Contains(f.String, secret) || strings.Contains(f.String, "X-Amz-Signature=super-secret-sig") {
				t.Fatalf("zap log field %s leaked sensitive string: %q", f.Key, f.String)
			}
			line += " " + f.String
		}
		if strings.Contains(line, secret) {
			t.Fatalf("zap log message leaked secret key: %s", line)
		}
	}
	deadline := time.Now().Add(time.Second)
	var entries []audit.AuditLog
	for {
		var qErr error
		entries, _, qErr = env.auditStore.List(context.Background(), audit.Filter{Limit: 50, Offset: 0})
		if qErr != nil {
			t.Fatalf("list audit logs: %v", qErr)
		}
		if len(entries) > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, e := range entries {
		if strings.Contains(e.Detail, secret) {
			t.Fatalf("audit detail leaked secret key: %s", e.Detail)
		}
	}
}

func TestHandler_RemoteS3ConfigMaskingAndSecretPreservation(t *testing.T) {
	env, cleanup := setupRemoteHandlerEnv(t)
	defer cleanup()

	const rawSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
	putPayload := map[string]any{
		"backend":                 BackendS3,
		"deliveryMode":            DeliveryModeRedirect,
		"localFallback":           true,
		"mediaDirectory":          env.mediaDir,
		"spoolDirectory":          env.spoolDir,
		"spoolMaxMb":              2048,
		"recordingsRetentionDays": 15,
		"snapshotsRetentionDays":  90,
		"exportsRetentionHours":   48,
		"highWatermarkPercent":    90,
		"lowWatermarkPercent":     80,
		"emergencyStopPercent":    95,
		"emergencyStopMinMb":      100,
		"s3": map[string]any{
			"endpoint":        "http://minio.local:9000",
			"region":          "us-east-1",
			"bucket":          "zhulong-media",
			"prefix":          "edge-01",
			"usePathStyle":    true,
			"useTls":          false,
			"accessKeyId":     "minioadmin",
			"secretAccessKey": rawSecret,
			"quotaGb":         100,
		},
	}

	body, err := json.Marshal(putPayload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	putRec := httptest.NewRecorder()
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(body))
	putReq.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK updating S3 config, got %d: %s", putRec.Code, putRec.Body.String())
	}

	// GET /config must mask SecretAccessKey and set HasSecretAccessKey=true.
	getRec := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/storage/config", nil)
	env.router.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK getting config, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var cfgResp struct {
		Code string        `json:"code"`
		Data StorageConfig `json:"data"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &cfgResp); err != nil {
		t.Fatalf("unmarshal config response: %v", err)
	}
	if cfgResp.Data.Backend != BackendS3 || cfgResp.Data.DeliveryMode != DeliveryModeRedirect {
		t.Fatalf("unexpected backend/delivery config: %+v", cfgResp.Data)
	}
	if cfgResp.Data.S3 == nil || !cfgResp.Data.S3.HasSecretAccessKey || cfgResp.Data.S3.SecretAccessKey != "" {
		t.Fatalf("expected masked S3 secret with HasSecretAccessKey=true, got %+v", cfgResp.Data.S3)
	}

	// Subsequent PUT with empty secretAccessKey must preserve the encrypted secret.
	putPayload["s3"].(map[string]any)["secretAccessKey"] = ""
	putPayload["s3"].(map[string]any)["prefix"] = "edge-02"
	body2, _ := json.Marshal(putPayload)

	putRec2 := httptest.NewRecorder()
	putReq2 := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(body2))
	putReq2.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(putRec2, putReq2)
	if putRec2.Code != http.StatusOK {
		t.Fatalf("expected 200 OK preserving secret on blank update, got %d: %s", putRec2.Code, putRec2.Body.String())
	}

	assertNoSecretLeak(
		t,
		env,
		rawSecret,
		putRec.Body.String(),
		getRec.Body.String(),
		putRec2.Body.String(),
	)
}

func TestHandler_TestConnectionAndCompatTestRoute(t *testing.T) {
	env, cleanup := setupRemoteHandlerEnv(t)
	defer cleanup()

	const rawSecret = "secret-must-never-leak-on-error"
	env.service.s3Probe = func(_ context.Context, cfg filestore.S3Config) error {
		if cfg.Bucket == "bad-bucket" {
			return errors.New("put failed: https://s3.local/bad-bucket?X-Amz-Signature=super-secret-sig with " + cfg.SecretAccessKey)
		}
		return nil
	}

	// 1. POST /test-connection for healthy S3.
	okBody, _ := json.Marshal(ConnectionTestRequest{
		Backend: BackendS3,
		S3: &S3Config{
			Endpoint:        "http://minio.local:9000",
			Bucket:          "good-bucket",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: rawSecret,
		},
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/test-connection", bytes.NewReader(okBody))
	req.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /test-connection, got %d: %s", rec.Code, rec.Body.String())
	}
	var connResp struct {
		Data ConnectionTestResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &connResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !connResp.Data.Reachable || !connResp.Data.Writable || connResp.Data.Backend != BackendS3 {
		t.Fatalf("unexpected ConnectionTestResponse: %+v", connResp.Data)
	}

	// 2. POST /test with backend="s3" (compat dual payload) when probe fails -> secret & signature redacted!
	failBody, _ := json.Marshal(ConnectionTestRequest{
		Backend: BackendS3,
		S3: &S3Config{
			Endpoint:        "http://minio.local:9000",
			Bucket:          "bad-bucket",
			AccessKeyID:     "minioadmin",
			SecretAccessKey: rawSecret,
		},
	})
	failRec := httptest.NewRecorder()
	failReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/test", bytes.NewReader(failBody))
	failReq.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(failRec, failReq)
	if failRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK with reachable=false, got %d: %s", failRec.Code, failRec.Body.String())
	}
	var failResp struct {
		Data ConnectionTestResponse `json:"data"`
	}
	if err := json.Unmarshal(failRec.Body.Bytes(), &failResp); err != nil {
		t.Fatalf("decode fail response: %v", err)
	}
	if failResp.Data.Reachable || !strings.Contains(failResp.Data.ErrorReason, "[REDACTED]") {
		t.Fatalf("expected unreachable redacted error reason, got %+v", failResp.Data)
	}

	// 3. POST /test with empty JSON {} must still enforce legacy PathTestRequest validation error.
	invalidRec := httptest.NewRecorder()
	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/test", strings.NewReader(`{}`))
	invalidReq.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(invalidRec, invalidReq)
	if invalidRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 VALIDATION_FAILED on empty /test payload, got %d: %s",
			invalidRec.Code, invalidRec.Body.String())
	}

	// 4. Failed PUT /config must also scrub supplied secret from client error logs.
	badPut, _ := json.Marshal(map[string]any{
		"backend":                 BackendS3,
		"mediaDirectory":          env.mediaDir,
		"spoolDirectory":          env.spoolDir,
		"spoolMaxMb":              2048,
		"recordingsRetentionDays": 15,
		"snapshotsRetentionDays":  90,
		"exportsRetentionHours":   48,
		"highWatermarkPercent":    90,
		"lowWatermarkPercent":     80,
		"emergencyStopPercent":    95,
		"emergencyStopMinMb":      100,
		"s3": map[string]any{
			"endpoint":        "http://minio.local:9000",
			"bucket":          "bad-bucket",
			"accessKeyId":     "minioadmin",
			"secretAccessKey": rawSecret,
		},
	})
	badPutRec := httptest.NewRecorder()
	badPutReq := httptest.NewRequest(http.MethodPut, "/api/v1/system/storage/config", bytes.NewReader(badPut))
	badPutReq.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(badPutRec, badPutReq)
	if badPutRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on bad S3 bucket probe, got %d: %s", badPutRec.Code, badPutRec.Body.String())
	}

	assertNoSecretLeak(
		t,
		env,
		rawSecret,
		rec.Body.String(),
		failRec.Body.String(),
		badPutRec.Body.String(),
	)
}

func TestHandler_MigrationEndpointsLifecycle(t *testing.T) {
	env, cleanup := setupRemoteHandlerEnv(t)
	defer cleanup()

	src := &stubMigrationSource{}
	env.service.SetMigrationSource(src)
	env.asyncSvc.RegisterHandler(NewMigrationJobHandler(env.service))

	// GET latest before any job exists -> data: null.
	latestRec := httptest.NewRecorder()
	latestReq := httptest.NewRequest(http.MethodGet, "/api/v1/system/storage/migrate/latest", nil)
	env.router.ServeHTTP(latestRec, latestReq)
	if latestRec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /migrate/latest, got %d: %s", latestRec.Code, latestRec.Body.String())
	}
	var nullResp httputil.Response
	if err := json.Unmarshal(latestRec.Body.Bytes(), &nullResp); err != nil {
		t.Fatalf("decode latest response: %v", err)
	}

	// POST /migrate while backend is local -> 422 STORAGE_MIGRATION_REQUIRES_S3.
	migFailRec := httptest.NewRecorder()
	migFailReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/migrate", nil)
	env.router.ServeHTTP(migFailRec, migFailReq)
	if migFailRec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 while backend is local, got %d: %s", migFailRec.Code, migFailRec.Body.String())
	}
	if !strings.Contains(migFailRec.Body.String(), codeMigrationRequiresS3) {
		t.Fatalf("expected %s, got %s", codeMigrationRequiresS3, migFailRec.Body.String())
	}

	// Switch persisted config to S3.
	s3Cfg := DefaultStorageConfig()
	s3Cfg.Backend = BackendS3
	s3Cfg.MediaDirectory = env.mediaDir
	s3Cfg.SpoolDirectory = env.spoolDir
	s3Cfg.EmergencyStopMinMB = 100
	s3Cfg.S3 = &S3Config{
		Endpoint:        "http://minio.local:9000",
		Bucket:          "zhulong-media",
		AccessKeyID:     "minioadmin",
		SecretAccessKey: "secret-key-val",
	}
	if _, err := env.repo.UpdateConfig(context.Background(), s3Cfg); err != nil {
		t.Fatalf("update S3 config: %v", err)
	}

	// Submit migration while worker is not started yet so status remains PENDING -> test conflict guard!
	migRec := httptest.NewRecorder()
	migReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/migrate", strings.NewReader(`{"deleteLocal":true}`))
	migReq.Header.Set("Content-Type", "application/json")
	env.router.ServeHTTP(migRec, migReq)
	if migRec.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted starting migration, got %d: %s", migRec.Code, migRec.Body.String())
	}

	var migResp struct {
		Data MigrationJobResponse `json:"data"`
	}
	if err := json.Unmarshal(migRec.Body.Bytes(), &migResp); err != nil {
		t.Fatalf("decode migration response: %v", err)
	}
	if !migResp.Data.DeleteLocal || migResp.Data.JobID == "" {
		t.Fatalf("unexpected migration response: %+v", migResp.Data)
	}

	// Second POST /migrate while PENDING -> 409 STORAGE_MIGRATION_IN_PROGRESS.
	dupRec := httptest.NewRecorder()
	dupReq := httptest.NewRequest(http.MethodPost, "/api/v1/system/storage/migrate", nil)
	env.router.ServeHTTP(dupRec, dupReq)
	if dupRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict for concurrent migration, got %d: %s", dupRec.Code, dupRec.Body.String())
	}

	// GET /migrate/latest returns the pending job.
	latestRec2 := httptest.NewRecorder()
	latestReq2 := httptest.NewRequest(http.MethodGet, "/api/v1/system/storage/migrate/latest", nil)
	env.router.ServeHTTP(latestRec2, latestReq2)
	if latestRec2.Code != http.StatusOK || !strings.Contains(latestRec2.Body.String(), migResp.Data.JobID) {
		t.Fatalf(
			"expected latest job %s, got %d: %s",
			migResp.Data.JobID,
			latestRec2.Code,
			latestRec2.Body.String(),
		)
	}
}
