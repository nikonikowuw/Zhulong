package systemtime

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

type mockSNTPClient struct {
	result *SNTPResult
	err    error
}

func (m *mockSNTPClient) Query(ctx context.Context, server string, timeout time.Duration) (*SNTPResult, error) {
	return m.result, m.err
}

func (m *mockSNTPClient) QueryPool(ctx context.Context, servers []string, timeout time.Duration) (*SNTPResult, error) {
	return m.result, m.err
}

func setupTestRepository(t *testing.T) (Repository, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repo := NewRepository(dbStore)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return repo, cleanup
}

func TestTimeServiceBootCheckRTCHeal(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	// Simulate cold boot: system time in 2020 (< buildEpoch 2026-01-01)
	driver.SetSystemTime(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

	// Hardware RTC holds valid 2026 time
	validRTC := time.Date(2026, 10, 7, 18, 0, 0, 0, time.UTC)
	driver.SetRTCTime(validRTC)

	sntpMock := &mockSNTPClient{}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	ctx := context.Background()
	if err := svc.OnBootCheck(ctx); err != nil {
		t.Fatalf("OnBootCheck failed: %v", err)
	}

	stepCalls := driver.GetStepCalls()
	if len(stepCalls) != 1 || !stepCalls[0].Equal(validRTC) {
		t.Fatalf("expected clock to be healed with RTC time %v, got calls: %v", validRTC, stepCalls)
	}
}

func TestTimeServiceGetStatusAndUpdateConfig(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	ctx := context.Background()
	status, err := svc.GetStatus(ctx)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.Mode != ModeNTP {
		t.Errorf("expected default mode %s, got %s", ModeNTP, status.Mode)
	}
	if status.Timezone != "Asia/Shanghai" {
		t.Errorf("expected default timezone Asia/Shanghai, got %s", status.Timezone)
	}

	// Update configuration
	newStatus, err := svc.UpdateConfig(ctx, UpdateConfigRequest{
		Mode:                ModeManual,
		NTPServers:          []string{"time.google.com"},
		SyncIntervalSeconds: 1800,
		Timezone:            "UTC",
	})
	if err != nil {
		t.Fatalf("UpdateConfig failed: %v", err)
	}

	if newStatus.Mode != ModeManual {
		t.Errorf("expected mode %s, got %s", ModeManual, newStatus.Mode)
	}
	if newStatus.Timezone != "UTC" {
		t.Errorf("expected timezone UTC, got %s", newStatus.Timezone)
	}
	if newStatus.SyncIntervalSeconds != 1800 {
		t.Errorf("expected interval 1800, got %d", newStatus.SyncIntervalSeconds)
	}
}

func TestTimeServiceSyncNow(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	mockResult := &SNTPResult{
		Server:  "ntp.aliyun.com",
		Offset:  150 * time.Millisecond,
		RTT:     20 * time.Millisecond,
		Stratum: 2,
	}
	sntpMock := &mockSNTPClient{result: mockResult}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	ctx := context.Background()
	status, err := svc.SyncNow(ctx)
	if err != nil {
		t.Fatalf("SyncNow failed: %v", err)
	}

	if status.SyncStatus.State != SyncStateSynchronized {
		t.Errorf("expected state synchronized, got %s", status.SyncStatus.State)
	}
	if status.SyncStatus.LastSyncServer != "ntp.aliyun.com" {
		t.Errorf("expected last server ntp.aliyun.com, got %s", status.SyncStatus.LastSyncServer)
	}
}

func TestTimeServiceSetManualTime(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	target := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	status, err := svc.SetManualTime(ctx, target)
	if err != nil {
		t.Fatalf("SetManualTime failed: %v", err)
	}

	if status.SyncStatus.State != SyncStateSynchronized {
		t.Errorf("expected state synchronized, got %s", status.SyncStatus.State)
	}
	stepCalls := driver.GetStepCalls()
	if len(stepCalls) != 1 || !stepCalls[0].Equal(target) {
		t.Fatalf("expected step call %v, got %v", target, stepCalls)
	}
}

func TestTimeServiceWorkerLifecycle(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{
		result: &SNTPResult{Server: "test.ntp", Offset: 10 * time.Millisecond, Stratum: 2},
	}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	ctx := context.Background()
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Double start is a no-op
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("Second Start failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

// TestTimeServiceGetStatusSurvivesUnreadableClock pins the fallback that the
// linux driver's error-swallowing used to make dead code: the endpoint reports
// the process's own time instead of failing outright.
func TestTimeServiceGetStatusSurvivesUnreadableClock(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	driver.SetSystemTimeError(errors.New("clock_gettime: operation not permitted"))
	svc := NewTimeService(repo, driver, &mockSNTPClient{}, zap.NewNop())

	status, err := svc.GetStatus(context.Background())
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.CurrentTime.IsZero() {
		t.Fatal("expected the process clock as a fallback, got the zero time")
	}
	if delta := time.Since(status.CurrentTime); delta < 0 || delta > time.Minute {
		t.Errorf("expected a fallback close to now, got %v off", delta)
	}
}

// TestTimeServiceRestartAfterStop guards the worker lifecycle: the goroutine must
// close the channel it was started with, so a later Start/Stop pair still joins
// instead of waiting for the context deadline.
func TestTimeServiceRestartAfterStop(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	sntpMock := &mockSNTPClient{
		result: &SNTPResult{Server: "test.ntp", Offset: 10 * time.Millisecond, Stratum: 2},
	}
	svc := NewTimeService(repo, driver, sntpMock, zap.NewNop())

	stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for cycle := 0; cycle < 3; cycle++ {
		if err := svc.Start(context.Background()); err != nil {
			t.Fatalf("cycle %d: Start failed: %v", cycle, err)
		}
		if err := svc.Stop(stopCtx); err != nil {
			t.Fatalf("cycle %d: Stop failed: %v", cycle, err)
		}
	}
}

// TestProbeSkippedWhenSyncInFlight pins that a probe preempted by an explicit
// sync is not reported as a failure: the worker must not back off for a reason
// it did not cause.
func TestProbeSkippedWhenSyncInFlight(t *testing.T) {
	repo, cleanup := setupTestRepository(t)
	defer cleanup()

	driver := NewStubClockDriver()
	svc := NewTimeService(repo, driver, &mockSNTPClient{}, zap.NewNop())

	svc.syncMu.Lock()
	defer svc.syncMu.Unlock()

	if !svc.probeOnce(context.Background()) {
		t.Fatal("a skipped probe must not be reported as a failure")
	}
}
