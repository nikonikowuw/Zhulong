package systemtime

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// recordingSNTPClient scripts a sequence of probe outcomes. Each call consumes
// the next entry; the last entry repeats once the script is exhausted. This lets
// the tests drive multi-round transitions (failure → retry → recovery) without
// running the real worker timer.
type recordingSNTPClient struct {
	mu      sync.Mutex
	results []*SNTPResult
	errs    []error
	calls   int
}

func (r *recordingSNTPClient) next() (*SNTPResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := r.calls
	r.calls++
	if idx >= len(r.results) {
		idx = len(r.results) - 1
	}
	var err error
	if idx < len(r.errs) {
		err = r.errs[idx]
	}
	return r.results[idx], err
}

func (r *recordingSNTPClient) Query(context.Context, string, time.Duration) (*SNTPResult, error) {
	return r.next()
}

func (r *recordingSNTPClient) QueryPool(context.Context, []string, time.Duration) (*SNTPResult, error) {
	return r.next()
}

func (r *recordingSNTPClient) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// probeHarness wires a TimeService with a live audit service so the assertions
// cover the real persistence path rather than a test double.
type probeHarness struct {
	svc    *TimeService
	driver *StubClockDriver
	sntp   *recordingSNTPClient
	audit  *audit.Service
	logs   *observer.ObservedLogs
	stop   func()

	repo Repository
}

func newProbeHarness(t *testing.T, sntp *recordingSNTPClient) *probeHarness {
	t.Helper()

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

	// Capture Debug output too: the steady-state probe path must still be
	// observable as a run log even though it produces no audit record.
	core, logs := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)

	driver := NewStubClockDriver()
	repo := NewRepository(dbStore)
	svc := NewTimeService(repo, driver, sntp, logger)
	svc.SetSystemEventRecorder(auditSvc)

	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = auditSvc.Stop(context.Background())
		_ = dbStore.Close()
	}
	t.Cleanup(stop)

	return &probeHarness{
		svc:    svc,
		driver: driver,
		sntp:   sntp,
		audit:  auditSvc,
		logs:   logs,
		stop:   stop,
		repo:   repo,
	}
}

// drain stops the audit worker so queued entries hit disk, then lists them.
// Without the drain, assertions race the async batch writer.
func (h *probeHarness) drain(t *testing.T) []audit.AuditLog {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.audit.Stop(ctx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	items, _, err := h.audit.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	return items
}

// probe runs one explicit background probe round. probeOnce is unexported but
// lives in this package, so the test drives the exact code path the worker loop
// uses instead of reimplementing it.
func (h *probeHarness) probe() bool {
	return h.svc.probeOnce(context.Background())
}

func TestProbeSteadyStateSlewRecordsNoAudit(t *testing.T) {
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{{
			Server:  "ntp.aliyun.com",
			Offset:  10 * time.Millisecond, // below SlewThreshold
			RTT:     12 * time.Millisecond,
			Stratum: 2,
		}},
		errs: []error{nil},
	}
	h := newProbeHarness(t, sntp)

	// Several rounds, because the defect being pinned is per-round accumulation.
	for i := 0; i < 3; i++ {
		if !h.probe() {
			t.Fatalf("round %d: expected a healthy probe", i)
		}
	}

	entries := h.drain(t)
	if len(entries) != 0 {
		t.Fatalf("a steady-state slew must not reach the audit trail, got %d entries: %+v",
			len(entries), entries)
	}

	// The telemetry still has to be observable, just not as an audit record.
	var debugCount int
	for _, entry := range h.logs.All() {
		if entry.Message == "NTP probe ok" {
			debugCount++
			if entry.Level != zapcore.DebugLevel {
				t.Errorf("expected the probe telemetry at Debug level, got %s", entry.Level)
			}
			if got := entry.ContextMap()["action"]; got != ActionNone.String() {
				t.Errorf("expected action=%q, got %v", ActionNone.String(), got)
			}
		}
	}
	if debugCount != 3 {
		t.Fatalf("expected 3 debug telemetry records, got %d", debugCount)
	}
}

func TestProbeStepRecordsClockStep(t *testing.T) {
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{{
			Server:  "ntp.aliyun.com",
			Offset:  5 * time.Second, // >= SlewThreshold, < PanicThreshold
			RTT:     18 * time.Millisecond,
			Stratum: 2,
		}},
		errs: []error{nil},
	}
	h := newProbeHarness(t, sntp)

	if !h.probe() {
		t.Fatal("expected a healthy probe")
	}

	entries := h.drain(t)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 audit entry for a clock step, got %d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != ActionTimeClockStep {
		t.Errorf("expected %q, got %q", ActionTimeClockStep, entry.Action)
	}
	if entry.Status != audit.StatusSuccess {
		t.Errorf("expected success, got %q", entry.Status)
	}
	if entry.Username != audit.UsernameSystem || entry.IP != "" {
		t.Errorf("expected a system attribution with no IP, got user=%q ip=%q",
			entry.Username, entry.IP)
	}
	if !strings.Contains(entry.Detail, "step=5s") || !strings.Contains(entry.Detail, "ntp.aliyun.com") {
		t.Errorf("expected the applied step and server in detail, got %q", entry.Detail)
	}
	if len(h.driver.GetStepCalls()) != 1 {
		t.Errorf("expected the clock to be stepped once, got %d", len(h.driver.GetStepCalls()))
	}
}

func TestProbePanicReviewRecordsFailure(t *testing.T) {
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{{
			Server:  "ntp.aliyun.com",
			Offset:  15 * time.Minute, // >= PanicThreshold
			RTT:     30 * time.Millisecond,
			Stratum: 2,
		}},
		errs: []error{nil},
	}
	h := newProbeHarness(t, sntp)

	// The first two probes only count toward the verification; the wall clock
	// must not move yet.
	if !h.probe() {
		t.Fatal("expected a panic-review round to still be a healthy probe")
	}
	if steps := h.driver.GetStepCalls(); len(steps) != 0 {
		t.Fatalf("a panic-review round must not step the clock, got %d steps", len(steps))
	}

	entries := h.drain(t)
	if len(entries) != 1 {
		t.Fatalf("expected 1 audit entry for the panic review, got %d: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Action != ActionTimeClockStep {
		t.Errorf("expected %q, got %q", ActionTimeClockStep, entry.Action)
	}
	// This is the regression that mattered most: a 15-minute offset under
	// verification used to be persisted as a successful sync.
	if entry.Status != audit.StatusFailed {
		t.Fatalf("a panic-threshold offset must be audited as failed, got %q", entry.Status)
	}
	if entry.ErrorMsg == "" {
		t.Error("expected a failure summary on the panic review record")
	}
	if !strings.Contains(entry.Detail, "review=1/") {
		t.Errorf("expected the review progress in detail, got %q", entry.Detail)
	}
}

func TestProbeFailureTransitionRecordedOnce(t *testing.T) {
	probeErr := errors.New("all ntp servers failed, last error: i/o timeout")
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{
			nil, nil, nil, // three failing rounds
			{Server: "ntp.aliyun.com", Offset: 8 * time.Millisecond, RTT: 15 * time.Millisecond, Stratum: 2},
		},
		errs: []error{probeErr, probeErr, probeErr, nil},
	}
	h := newProbeHarness(t, sntp)

	for i := 0; i < 3; i++ {
		if h.probe() {
			t.Fatalf("round %d: expected the probe to report failure", i)
		}
	}
	if h.probe() != true {
		t.Fatal("expected the recovery round to report success")
	}

	entries := h.drain(t)
	if len(entries) != 2 {
		t.Fatalf("expected exactly 1 failure + 1 recovery record, got %d: %+v", len(entries), entries)
	}

	byAction := make(map[string]audit.AuditLog, len(entries))
	for _, e := range entries {
		byAction[e.Action] = e
	}

	failed, ok := byAction[ActionTimeNTPSyncFailed]
	if !ok {
		t.Fatalf("missing the %q record: %+v", ActionTimeNTPSyncFailed, entries)
	}
	if failed.Status != audit.StatusFailed {
		t.Errorf("expected the outage record to be failed, got %q", failed.Status)
	}

	recovered, ok := byAction[ActionTimeNTPRecovered]
	if !ok {
		t.Fatalf("missing the %q record: %+v", ActionTimeNTPRecovered, entries)
	}
	if recovered.Status != audit.StatusSuccess {
		t.Errorf("expected the recovery record to be success, got %q", recovered.Status)
	}

	// 3 failing rounds produced 1 record, and the healthy steady state after
	// recovery produced none: the bounded audit table stays reserved for events.
	if sntp.CallCount() != 4 {
		t.Fatalf("expected 4 probe calls, got %d", sntp.CallCount())
	}
}

func TestProbeNilRecorderDoesNotPanic(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{{
			Server: "ntp.aliyun.com", Offset: 5 * time.Second, RTT: 20 * time.Millisecond, Stratum: 2,
		}},
		errs: []error{nil},
	}
	// No SetSystemEventRecorder call: the recorder is an optional dependency.
	svc := NewTimeService(repo, driver, sntp, zap.NewNop())

	if !svc.probeOnce(context.Background()) {
		t.Fatal("expected a healthy probe")
	}
	if len(driver.GetStepCalls()) != 1 {
		t.Errorf("expected the clock to be stepped, got %d", len(driver.GetStepCalls()))
	}
}

func TestProbeDriverFailureIsDebouncedAsOutage(t *testing.T) {
	sntp := &recordingSNTPClient{
		results: []*SNTPResult{{
			Server: "ntp.aliyun.com", Offset: 5 * time.Second, RTT: 20 * time.Millisecond, Stratum: 2,
		}},
		errs: []error{nil},
	}
	h := newProbeHarness(t, sntp)

	// The probe succeeds but the clock driver refuses the step (no CAP_SYS_TIME).
	// That is an outage, not a healthy steady state: without the shared edge
	// detector it would silently clear the failure state and audit nothing.
	h.driver.SetPermission(false)
	for i := 0; i < 3; i++ {
		if h.probe() {
			t.Fatalf("round %d: a refused clock step must report failure", i)
		}
	}

	entries := h.drain(t)
	if len(entries) != 1 {
		t.Fatalf("expected a single debounced outage record, got %d: %+v", len(entries), entries)
	}
	if entries[0].Action != ActionTimeNTPSyncFailed {
		t.Errorf("expected %q, got %q", ActionTimeNTPSyncFailed, entries[0].Action)
	}
	if entries[0].Status != audit.StatusFailed {
		t.Errorf("expected failed status, got %q", entries[0].Status)
	}
}
