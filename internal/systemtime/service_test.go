package systemtime

import (
	"context"
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
	// Simulate cold boot: system time in 2020 (< BuildEpoch 2026-01-01)
	driver.offset = time.Until(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

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
