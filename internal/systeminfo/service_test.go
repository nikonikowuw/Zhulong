package systeminfo

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// mockCollector serves snapshots whose counters advance by a fixed step per call, so
// rate assertions are deterministic without sleeping on a real ticker.
type mockCollector struct {
	mu        sync.Mutex
	snapCount int

	// err, when set, is returned instead of a snapshot.
	err error
}

func (m *mockCollector) CollectSnapshot(ctx context.Context) (*rawHardwareSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.err != nil {
		return nil, m.err
	}
	m.snapCount++
	return mockSnapshot(m.snapCount), nil
}

// calls reports how many snapshots were actually collected.
func (m *mockCollector) calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapCount
}

// mockSnapshot builds the nth synthetic sample. Every monotonic counter is
// proportional to n; the wall clock advances by exactly one sampleInterval so the
// derived per-second rates are exact integers.
func mockSnapshot(n int) *rawHardwareSnapshot {
	elapsed := time.Duration(n) * sampleInterval

	return &rawHardwareSnapshot{
		Timestamp: time.Unix(1_700_000_000, 0).UTC().Add(elapsed),
		Host: HostInfo{
			Hostname:      "mock-box",
			OS:            "linux",
			Arch:          "arm64",
			KernelVersion: "5.10.0",
			UptimeSeconds: 120,
		},
		CPUTotal: rawCPUTimes{
			User:   uint64(100 * n),
			System: uint64(50 * n),
			Idle:   uint64(850 * n),
		},
		CPUCores: []rawCPUTimes{
			{
				User:   uint64(50 * n),
				System: uint64(25 * n),
				Idle:   uint64(425 * n),
			},
		},
		CPUModel: "Mock Cortex-A55",
		Memory: MemoryStatus{
			TotalBytes:     4000000000,
			UsedBytes:      2000000000,
			AvailableBytes: 2000000000,
			UsedPercent:    50.0,
		},
		Storage: []StorageMountStatus{
			{
				MountPoint:  "/",
				Device:      "/dev/mmcblk0p1",
				FSType:      "ext4",
				TotalBytes:  32000000000,
				UsedBytes:   16000000000,
				FreeBytes:   16000000000,
				UsedPercent: 50.0,
			},
		},
		Network: map[string]rawNetworkInterface{
			"wlan0": {
				Interface:    "wlan0",
				RxTotalBytes: uint64(500000 * n),
				TxTotalBytes: uint64(250000 * n),
				IsUp:         true,
			},
			"eth0": {
				Interface:    "eth0",
				RxTotalBytes: uint64(1000000 * n),
				TxTotalBytes: uint64(500000 * n),
				IsUp:         true,
			},
			"can0": {
				Interface:    "can0",
				RxTotalBytes: uint64(10000 * n),
				TxTotalBytes: uint64(5000 * n),
				IsUp:         false,
			},
		},
		NPU: NPUStatus{
			Supported:   true,
			Name:        "Rockchip NPU",
			LoadPercent: 25.0,
			CoreLoads:   []float64{25.0},
			Temperature: 48.0,
		},
		Thermals: []TemperatureInfo{
			{Label: "CPU", Temperature: 50.0},
			{Label: "NPU", Temperature: 48.0},
		},
	}
}

func newTestService(t *testing.T, col collector) *Service {
	t.Helper()
	return NewService(col, zaptest.NewLogger(t))
}

func TestService_FirstSampleReportsZeroRates(t *testing.T) {
	svc := newTestService(t, &mockCollector{})

	status, err := svc.GetHardwareStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.Host.Hostname != "mock-box" {
		t.Fatalf("expected hostname mock-box, got %q", status.Host.Hostname)
	}
	if len(status.History) != 1 {
		t.Fatalf("expected 1 history point, got %d", len(status.History))
	}
	if status.CPU.UsagePercent != 0 {
		t.Fatalf("first sample has no previous counters, expected 0%% CPU, got %v", status.CPU.UsagePercent)
	}

	// The network table must still be populated on the first sample, just at 0 B/s.
	if len(status.Network) != 3 {
		t.Fatalf("expected 3 interfaces, got %d", len(status.Network))
	}
	for _, iface := range status.Network {
		if iface.RxBytesRate != 0 || iface.TxBytesRate != 0 {
			t.Fatalf("first sample must report zero rates, got %+v", iface)
		}
	}
}

func TestService_SecondSampleComputesRates(t *testing.T) {
	svc := newTestService(t, &mockCollector{})

	if _, err := svc.GetHardwareStatus(context.Background()); err != nil {
		t.Fatalf("first sample: %v", err)
	}
	if err := svc.sample(context.Background()); err != nil {
		t.Fatalf("second sample: %v", err)
	}

	status, err := svc.GetHardwareStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if status.CPU.UsagePercent != 15.0 {
		t.Fatalf("expected 15.0%% CPU usage, got %v", status.CPU.UsagePercent)
	}
	if len(status.History) != 2 {
		t.Fatalf("expected 2 history points, got %d", len(status.History))
	}

	// eth0 gains exactly 1 MB per sampleInterval.
	wantEthRxRate := uint64(1000000 / (sampleInterval / time.Second))
	for _, iface := range status.Network {
		if iface.Interface != "eth0" {
			continue
		}
		if iface.RxBytesRate != wantEthRxRate {
			t.Fatalf("expected eth0 rx rate %d B/s, got %d", wantEthRxRate, iface.RxBytesRate)
		}
	}

	if status.History[1].NetRxRate == 0 {
		t.Fatal("aggregate rx rate must be non-zero on the second sample")
	}
}

func TestService_SortsNetworkInterfaces(t *testing.T) {
	svc := newTestService(t, &mockCollector{})

	status, err := svc.GetHardwareStatus(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := []string{"can0", "eth0", "wlan0"}
	for i, name := range want {
		if status.Network[i].Interface != name {
			t.Fatalf("expected network interfaces sorted %v, got %+v", want, status.Network)
		}
	}
}

func TestService_ConcurrentReads(t *testing.T) {
	svc := newTestService(t, &mockCollector{})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Assert inside the goroutine would race the test's own failure reporting;
			// a non-nil error here is still caught by -race via the shared state.
			_, _ = svc.GetHardwareStatus(context.Background())
		}()
	}
	wg.Wait()
}

func TestService_ConcurrentFirstReadsShareOneSweep(t *testing.T) {
	col := &mockCollector{}
	svc := newTestService(t, col)

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.GetHardwareStatus(context.Background()); err != nil {
				t.Errorf("GetHardwareStatus: %v", err)
			}
		}()
	}
	wg.Wait()

	if calls := col.calls(); calls != 1 {
		t.Fatalf("concurrent first reads must collapse into one sweep, collector ran %d times", calls)
	}
}

func TestService_StopIsIdempotent(t *testing.T) {
	svc := newTestService(t, &mockCollector{})
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}

	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("expected second Stop to be idempotent, got: %v", err)
	}
}

func TestService_SampleFailureSurfacesAsUnavailable(t *testing.T) {
	col := &mockCollector{err: errTelemetryUnavailable}
	svc := newTestService(t, col)

	_, err := svc.GetHardwareStatus(context.Background())
	if err == nil {
		t.Fatal("expected an error when the collector never succeeds")
	}
}
