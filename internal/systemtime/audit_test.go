package systemtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// setupAuditHandler wires the real audit middleware and a live audit service so
// the assertions cover the declarative audit path rather than a test double.
//
// sntpErr controls the mocked NTP probe: a non-nil error drives SyncNow into the
// failed domain state that must be reported as failed even though the HTTP
// response stays 200.
func setupAuditHandler(t *testing.T, sntpErr error) (*gin.Engine, *audit.Service, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}

	repo := NewRepository(dbStore)
	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{err: sntpErr}
	if sntpErr == nil {
		sntpMock.result = &SNTPResult{Server: "ntp.aliyun.com", Offset: 50 * time.Millisecond, Stratum: 2}
	}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())
	handler := NewHandler(svc, zap.NewNop())

	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	handler.RegisterRoutes(api)

	cleanup := func() {
		_ = auditSvc.Stop(context.Background())
		_ = dbStore.Close()
	}
	return router, auditSvc, cleanup
}

// drainAudit stops the service so queued entries are persisted, then lists them.
func drainAudit(t *testing.T, svc *audit.Service) []audit.AuditLog {
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

// httpCall is one scripted HTTP call. It is a struct rather than a parameter
// list because every field is a string: a swapped pair would still compile.
type httpCall struct {
	method         string
	path           string
	body           string
	remoteAddr     string
	acceptLanguage string
}

// send runs the call through the real router. Headers are only set when the call
// asks for them, so a body-less call keeps the "no JSON payload" shape the
// binding path relies on.
func (c httpCall) send(router *gin.Engine) *httptest.ResponseRecorder {
	req := httptest.NewRequest(c.method, c.path, bytes.NewReader([]byte(c.body)))
	if c.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.acceptLanguage != "" {
		req.Header.Set("Accept-Language", c.acceptLanguage)
	}
	if c.remoteAddr != "" {
		req.RemoteAddr = c.remoteAddr
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// testConfigBody builds a config-update payload from its two varying fields, so
// the accepted and rejected cases cannot drift apart.
func testConfigBody(mode, timezone string) string {
	return `{"mode":"` + mode + `","ntpServers":["ntp.aliyun.com"],` +
		`"syncIntervalSeconds":600,"timezone":"` + timezone + `"}`
}

// TestHandlerAuditCoverage exercises every audited system time write operation
// through the real router and asserts the resulting records.
func TestHandlerAuditCoverage(t *testing.T) {
	router, auditSvc, cleanup := setupAuditHandler(t, nil)
	defer cleanup()

	const clientAddr = "192.168.11.11:6666"

	// 1. Update config
	updateConfig := httpCall{
		method:     http.MethodPut,
		path:       "/api/v1/system/time/config",
		body:       testConfigBody(ModeNTP, "Asia/Shanghai"),
		remoteAddr: clientAddr,
	}
	if w := updateConfig.send(router); w.Code != http.StatusOK {
		t.Fatalf("update config failed: %d %s", w.Code, w.Body.String())
	}

	// 2. Manual set
	manualSet := httpCall{
		method:     http.MethodPost,
		path:       "/api/v1/system/time/manual",
		body:       `{"targetTime":"2026-10-07T16:45:00.000Z"}`,
		remoteAddr: clientAddr,
	}
	if w := manualSet.send(router); w.Code != http.StatusOK {
		t.Fatalf("manual set failed: %d %s", w.Code, w.Body.String())
	}

	// 3. NTP sync with a healthy probe
	syncNow := httpCall{
		method:     http.MethodPost,
		path:       "/api/v1/system/time/sync",
		remoteAddr: clientAddr,
	}
	if w := syncNow.send(router); w.Code != http.StatusOK {
		t.Fatalf("sync failed: %d %s", w.Code, w.Body.String())
	}

	// 4. Validation failure on the config update must still be recorded.
	invalidConfig := httpCall{
		method:     http.MethodPut,
		path:       "/api/v1/system/time/config",
		body:       `{}`,
		remoteAddr: clientAddr,
	}
	if w := invalidConfig.send(router); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for an empty config body, got %d %s", w.Code, w.Body.String())
	}

	entries := drainAudit(t, auditSvc)
	if len(entries) != 4 {
		t.Fatalf("expected 4 audit entries, got %d: %+v", len(entries), entries)
	}

	type want struct {
		action string
		status string
	}
	wants := []want{
		{ActionTimeUpdateConfig, audit.StatusSuccess},
		{ActionTimeManualSet, audit.StatusSuccess},
		{ActionTimeNTPSync, audit.StatusSuccess},
		{ActionTimeUpdateConfig, audit.StatusFailed},
	}

	remaining := make([]want, len(wants))
	copy(remaining, wants)
	for _, e := range entries {
		if e.IP != "192.168.11.11" {
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

	// The whitelisted config summary must describe the accepted fields only.
	for _, e := range entries {
		if e.Action != ActionTimeUpdateConfig || e.Status != audit.StatusSuccess {
			continue
		}
		if !strings.Contains(e.Detail, "mode=ntp") || !strings.Contains(e.Detail, "Asia/Shanghai") {
			t.Errorf("expected the whitelisted config summary, got %q", e.Detail)
		}
	}
}

// TestSyncNowDomainFailureIsAuditedAsFailed pins the 2xx domain-failure rule:
// the HTTP response stays 200 while the sync state is failed, so the record must
// come from the explicit TagError channel rather than the status code.
func TestSyncNowDomainFailureIsAuditedAsFailed(t *testing.T) {
	router, auditSvc, cleanup := setupAuditHandler(t, errors.New("all ntp servers unreachable"))
	defer cleanup()

	const clientAddr = "192.168.11.12:7777"
	syncNow := httpCall{method: http.MethodPost, path: "/api/v1/system/time/sync", remoteAddr: clientAddr}
	w := syncNow.send(router)
	if w.Code != http.StatusOK {
		t.Fatalf("expected the 200 response contract to be preserved, got %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data struct {
			SyncStatus struct {
				State string `json:"state"`
			} `json:"syncStatus"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal sync response: %v", err)
	}
	if resp.Data.SyncStatus.State != SyncStateFailed {
		t.Fatalf("expected the domain sync state to be failed, got %q", resp.Data.SyncStatus.State)
	}

	entries := drainAudit(t, auditSvc)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 record for the sync, got %d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != ActionTimeNTPSync {
		t.Errorf("expected the NTP sync action, got %q", entry.Action)
	}
	if entry.Status != audit.StatusFailed {
		t.Errorf("expected a failed status despite the 200 response, got %q", entry.Status)
	}
	if entry.IP != "192.168.11.12" {
		t.Errorf("expected the connection source IP, got %q", entry.IP)
	}
	if !strings.Contains(entry.ErrorMsg, "NTP_SYNC_FAILED") {
		t.Errorf("expected the stable error code, got %q", entry.ErrorMsg)
	}
	// The raw probe error text is an internal cause and must not be persisted.
	if strings.Contains(entry.ErrorMsg, "unreachable") {
		t.Errorf("raw probe error leaked into error_msg: %q", entry.ErrorMsg)
	}
}

// TestBackgroundSystemEventsAreNotUserAudited pins the Service boundary: the
// service keeps a narrow system-event recorder for background NTP/RTC work, and
// a user-requested config update no longer produces a duplicate service-side
// record.
func TestBackgroundSystemEventsAreNotUserAudited(t *testing.T) {
	router, auditSvc, cleanup := setupAuditHandler(t, nil)
	defer cleanup()

	const clientAddr = "192.168.11.13:8888"
	configUpdate := httpCall{
		method:     http.MethodPut,
		path:       "/api/v1/system/time/config",
		body:       testConfigBody(ModeNTP, "UTC"),
		remoteAddr: clientAddr,
	}
	if w := configUpdate.send(router); w.Code != http.StatusOK {
		t.Fatalf("update config failed: %d %s", w.Code, w.Body.String())
	}

	entries := drainAudit(t, auditSvc)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 record (no service-side duplicate), got %d: %+v", len(entries), entries)
	}
	if entries[0].Action != ActionTimeUpdateConfig {
		t.Errorf("expected the config update action, got %q", entries[0].Action)
	}
}
