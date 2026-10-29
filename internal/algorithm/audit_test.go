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
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// setupAuditRouter wires the real audit middleware so the assertions cover the
// declarative audit path rather than a test double.
func setupAuditRouter(t *testing.T) (*gin.Engine, *algorithm.Service, *audit.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Stop(context.Background()) })

	// enable/disable need a package process that actually starts; the archive's
	// entry is an idle stub, so the supervised status is deterministic.
	rt := algorunner.Runtime{AlgorithmsDir: dataDir}
	comp := algorithm.NewAlgorithmComponents(dbStore, rt, x86Host(), zap.NewNop())
	t.Cleanup(func() { comp.Supervisor.StopAll(time.Second) })

	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	comp.Handler.RegisterRoutes(api)
	comp.Handler.RegisterUploadRoutes(api)

	return router, comp.Service, auditSvc
}

// drainAlgorithmAudit stops the service so queued entries are persisted, then
// lists them newest first.
func drainAlgorithmAudit(t *testing.T, svc *audit.Service) []audit.AuditLog {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	items, _, err := svc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	return items
}

func installAuditArchive(t *testing.T, router *gin.Engine, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()

	manifest := algorithm.Manifest{
		ID:        "audit_detector",
		Version:   "1.0.0",
		Name:      "audit_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Target: "onnx"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json":  manifestBytes,
		"audit_detector": installableEntry(int(algorithm.ResultTypeDetection)),
		"sample.jpg":     []byte("jpg_stub"),
	})
	defer os.Remove(archivePath)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filepath.Base(archivePath))
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fileBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write archive part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/install", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.RemoteAddr = remoteAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// TestHandlerAuditCoverage exercises every audited algorithm write operation
// through the real router and asserts the resulting records, including the
// failure paths that must still leave a trace.
func TestHandlerAuditCoverage(t *testing.T) {
	router, _, auditSvc := setupAuditRouter(t)

	const clientAddr = "192.168.8.8:4321"
	const algoID = "audit_detector"

	do := func(method, path, body, remoteAddr string) *httptest.ResponseRecorder {
		t.Helper()
		var reader *bytes.Reader
		if body == "" {
			reader = bytes.NewReader(nil)
		} else {
			reader = bytes.NewReader([]byte(body))
		}
		req := httptest.NewRequest(method, path, reader)
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.RemoteAddr = remoteAddr
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 1. Install (upload route)
	if w := installAuditArchive(t, router, clientAddr); w.Code != http.StatusCreated {
		t.Fatalf("install failed: %d %s", w.Code, w.Body.String())
	}

	// 2. Enable
	if w := do(http.MethodPost, "/api/v1/algorithms/"+algoID+"/enable", "", clientAddr); w.Code != http.StatusOK {
		t.Fatalf("enable failed: %d %s", w.Code, w.Body.String())
	}

	// 3. Reset while RUNNING: the supervisor reports not-in-crash-loop.
	if w := do(http.MethodPost, "/api/v1/algorithms/"+algoID+"/reset", "", clientAddr); w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reset outside CRASH_LOOP, got %d %s", w.Code, w.Body.String())
	}

	// 4. Disable
	if w := do(http.MethodPost, "/api/v1/algorithms/"+algoID+"/disable", "", clientAddr); w.Code != http.StatusOK {
		t.Fatalf("disable failed: %d %s", w.Code, w.Body.String())
	}

	// 5. Delete algorithm
	if w := do(http.MethodDelete, "/api/v1/algorithms/"+algoID, "", clientAddr); w.Code != http.StatusOK {
		t.Fatalf("delete algorithm failed: %d %s", w.Code, w.Body.String())
	}

	// 6. Delete again: a failure path must still be recorded.
	if w := do(http.MethodDelete, "/api/v1/algorithms/"+algoID, "", clientAddr); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a second delete, got %d %s", w.Code, w.Body.String())
	}

	entries := drainAlgorithmAudit(t, auditSvc)
	if len(entries) != 6 {
		t.Fatalf("expected 6 audit entries, got %d: %+v", len(entries), entries)
	}

	wants := []auditWant{
		{algorithm.ActionAlgorithmInstall, audit.StatusSuccess},
		{algorithm.ActionAlgorithmEnable, audit.StatusSuccess},
		{algorithm.ActionAlgorithmReset, audit.StatusFailed},
		{algorithm.ActionAlgorithmDisable, audit.StatusSuccess},
		{algorithm.ActionAlgorithmDelete, audit.StatusSuccess},
		{algorithm.ActionAlgorithmDelete, audit.StatusFailed},
	}
	assertAuditEntriesMatch(t, entries, wants)
	assertAuditDetails(t, entries, algoID)
}

// auditWant 是期望出现的一条审计记录：动作 + 结果。
type auditWant struct {
	action string
	status string
}

// assertAuditEntriesMatch 按「动作 + 结果」而不是插入顺序配对：Service 倒序出队，
// 且有两条记录共享 delete 动作。
func assertAuditEntriesMatch(t *testing.T, entries []audit.AuditLog, wants []auditWant) {
	t.Helper()

	remaining := make([]auditWant, len(wants))
	copy(remaining, wants)
	for _, e := range entries {
		if e.IP != "192.168.8.8" {
			t.Errorf("expected the connection source IP on %s, got %q", e.Action, e.IP)
		}
		if e.Username != audit.UsernameAnonymous {
			t.Errorf("expected an unauthenticated actor on %s, got %q", e.Action, e.Username)
		}

		matched := -1
		for i, w := range remaining {
			if w.action == e.Action && w.status == e.Status {
				matched = i
				break
			}
		}
		if matched < 0 {
			t.Errorf("unexpected audit entry %s/%s: %+v", e.Action, e.Status, e)
			continue
		}
		remaining = append(remaining[:matched], remaining[matched+1:]...)
	}
	for _, w := range remaining {
		t.Errorf("missing audit entry for %s with status %s", w.action, w.status)
	}
}

// assertAuditDetails 断言目标/详情收敛，以及失败记录只带稳定机器码、
// 不泄露底层数据库错误原文。
func assertAuditDetails(t *testing.T, entries []audit.AuditLog, algoID string) {
	t.Helper()

	var installEntry, resetEntry, failedDelete *audit.AuditLog
	for i := range entries {
		switch {
		case entries[i].Action == algorithm.ActionAlgorithmInstall:
			installEntry = &entries[i]
		case entries[i].Action == algorithm.ActionAlgorithmReset:
			resetEntry = &entries[i]
		case entries[i].Action == algorithm.ActionAlgorithmDelete && entries[i].Status == audit.StatusFailed:
			failedDelete = &entries[i]
		}
	}

	if installEntry == nil {
		t.Fatal("missing install audit entry")
	}
	if installEntry.Target != "algorithm:"+algoID {
		t.Errorf("expected the refined install target, got %q", installEntry.Target)
	}
	if installEntry.Detail != algoID {
		t.Errorf("expected the algorithm name as detail, got %q", installEntry.Detail)
	}

	if resetEntry == nil {
		t.Fatal("missing reset audit entry")
	}
	if !strings.Contains(resetEntry.ErrorMsg, "ALGORITHM_NOT_IN_CRASH_LOOP") {
		t.Errorf("expected the stable error code on reset, got %q", resetEntry.ErrorMsg)
	}
	if failedDelete == nil {
		t.Fatal("missing the failed delete audit entry")
	}
	if !strings.Contains(failedDelete.ErrorMsg, "ALGORITHM_NOT_FOUND") {
		t.Errorf("expected the stable error code on the failed delete, got %q", failedDelete.ErrorMsg)
	}
	if strings.Contains(failedDelete.ErrorMsg, "record not found") {
		t.Errorf("raw database error leaked into error_msg: %q", failedDelete.ErrorMsg)
	}
}

// TestInstallAuditRecordsRejectedArchive covers the early failure paths on the
// dedicated upload route: the audit intent is declared before the multipart
// stream is read, so a rejected archive must still leave a failed record.
func TestInstallAuditRecordsRejectedArchive(t *testing.T) {
	router, _, auditSvc := setupAuditRouter(t)

	const clientAddr = "192.168.8.9:5555"

	// Missing file part: the handler rejects before any algorithm is known.
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/install", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.RemoteAddr = clientAddr
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a missing upload file, got %d %s", w.Code, w.Body.String())
	}

	// An archive whose manifest does not match the host platform is rejected by
	// the verifier, which is a second, later failure path.
	manifest := algorithm.Manifest{
		ID:        "rejected_detector",
		Version:   "1.0.0",
		Name:      "rejected_detector",
		TaskType:  algorithm.TaskTypeDetection,
		Category:  algorithm.CategorySafety,
		Platform:  algorithm.ManifestPlatform{Target: "npu-not-this-host"},
		Resources: algorithm.ManifestResources{ComputeCost: 30, MemoryMB: 64},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	archivePath := createTestTarGz(t, map[string][]byte{
		"manifest.json":     manifestBytes,
		"rejected_detector": installableEntry(int(algorithm.ResultTypeDetection)),
		"sample.jpg":        []byte("jpg_stub"),
	})
	defer os.Remove(archivePath)

	archiveBody := &bytes.Buffer{}
	archiveWriter := multipart.NewWriter(archiveBody)
	part, err := archiveWriter.CreateFormFile("file", filepath.Base(archivePath))
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	fileBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write archive part: %v", err)
	}
	if err := archiveWriter.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/install", archiveBody)
	req.Header.Set("Content-Type", archiveWriter.FormDataContentType())
	req.RemoteAddr = clientAddr
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a platform mismatch, got %d %s", w.Code, w.Body.String())
	}

	entries := drainAlgorithmAudit(t, auditSvc)
	if len(entries) != 2 {
		t.Fatalf("expected 2 failed install records, got %d: %+v", len(entries), entries)
	}
	for _, e := range entries {
		if e.Action != algorithm.ActionAlgorithmInstall {
			t.Errorf("expected an install action, got %q", e.Action)
		}
		if e.Status != audit.StatusFailed {
			t.Errorf("expected a failed status, got %q", e.Status)
		}
		if e.IP != "192.168.8.9" {
			t.Errorf("expected the connection source IP, got %q", e.IP)
		}
		if e.ErrorMsg == "" {
			t.Error("expected a safe error summary")
		}
	}

	// The verifier failure carries the stable outward code; the missing-file
	// path uses INVALID_FILE. Neither may leak internal detail.
	codes := make(map[string]bool, len(entries))
	for _, e := range entries {
		codes[e.ErrorMsg] = true
	}
	if !strings.Contains(strings.Join(mapKeys(codes), "|"), "INVALID_FILE") {
		t.Errorf("expected INVALID_FILE among the failure summaries, got %v", mapKeys(codes))
	}
}

// mapKeys returns the keys of a string-keyed set for readable failure output.
func mapKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
