package app

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/algorunner"
	"github.com/nikonikowuw/Zhulong/internal/camera"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/storage"
	"github.com/nikonikowuw/Zhulong/internal/systeminfo"
)

type stubStatusIdentity string

func (s stubStatusIdentity) DeviceID() string { return string(s) }

type stubStatusHardware struct {
	resp *systeminfo.SystemHardwareResponse
}

func (s stubStatusHardware) GetHardwareStatus(_ context.Context) (*systeminfo.SystemHardwareResponse, error) {
	return s.resp, nil
}

type stubStatusStorage struct {
	status   *storage.StorageStatus
	writable bool
	err      error
}

func (s stubStatusStorage) GetStatus(_ context.Context) (*storage.StorageStatus, error) {
	return s.status, s.err
}

func (s stubStatusStorage) CanWrite() bool { return s.writable }

type stubStatusCameras camera.CameraHealthSummary

func (s stubStatusCameras) HealthSummary() camera.CameraHealthSummary {
	return camera.CameraHealthSummary(s)
}

type stubStatusRunners struct {
	active   int
	failed   int
	byAlgoID map[string]string
}

func (s stubStatusRunners) SummaryCounts() (int, int) { return s.active, s.failed }
func (s stubStatusRunners) GetStatus(id string) string {
	if s.byAlgoID == nil {
		return algorunner.StatusDisabled
	}
	return s.byAlgoID[id]
}

type stubStatusAlgorithms []algorithm.Algorithm

func (s stubStatusAlgorithms) ListAlgorithms(_ context.Context) ([]algorithm.Algorithm, error) {
	return s, nil
}

type stubStatusTasks int

func (s stubStatusTasks) CountEnabledTasks(_ context.Context) (int, error) {
	return int(s), nil
}

type stubStatusDecode struct {
	ready bool
	stats engine.DecodeStats
	err   error
}

func (s stubStatusDecode) Ready() bool                                 { return s.ready }
func (s stubStatusDecode) GetDecodeStats() (engine.DecodeStats, error) { return s.stats, s.err }

type stubStatusLog struct {
	err     error
	dropped uint64
}

func (s stubStatusLog) Health() (error, uint64) { return s.err, s.dropped }

func TestStatusSnapshotAdapter_DegradationReasonsAndSanitization(t *testing.T) {
	baseHealthy := func() *statusSnapshotAdapter {
		return &statusSnapshotAdapter{
			identity: stubStatusIdentity("edge-box-42"),
			hardware: stubStatusHardware{
				resp: &systeminfo.SystemHardwareResponse{
					Host:   systeminfo.HostInfo{Arch: "arm64", UptimeSeconds: 7200},
					CPU:    systeminfo.CPUStatus{UsagePercent: 25.44, Temperature: 52.1},
					Memory: systeminfo.MemoryStatus{UsedPercent: 48.88},
					Heterogeneous: systeminfo.HeterogeneousStatus{
						NPU:          systeminfo.NPUStatus{Supported: true, LoadPercent: 33.33, Temperature: 56.8},
						Temperatures: []systeminfo.TemperatureInfo{{Label: "soc", Temperature: 54.0}},
					},
				},
			},
			storage: stubStatusStorage{
				writable: true,
				status: &storage.StorageStatus{
					MediaDirectory: "/mnt/secret-nvme/zhulong/media",
					MountPoint:     "/mnt/secret-nvme",
					Status:         storage.StatusHealthy,
					CanWrite:       true,
					UsagePercent:   40,
					FreeBytes:      10 * 1024 * 1024 * 1024,
				},
			},
			cameras: stubStatusCameras{Enabled: 4, Online: 4, Offline: 0, Degraded: 1},
			runners: stubStatusRunners{active: 2, failed: 0},
			tasks:   stubStatusTasks(3),
			decode: stubStatusDecode{
				ready: true,
				stats: engine.DecodeStats{
					HardwareAvailable: true,
					ActiveSessions:    4,
					MaxSessions:       16,
				},
			},
			logSink:               stubStatusLog{},
			decodeBackend:         "mpp",
			allowSoftwareFallback: false,
			publicBaseURL:         "http://192.168.1.88:8000",
		}
	}

	tests := []struct {
		name        string
		mutate      func(*statusSnapshotAdapter)
		wantStatus  string
		wantReasons []string
	}{
		{
			name:        "all healthy (sub-stream degraded alone does not mark host degraded)",
			mutate:      func(*statusSnapshotAdapter) {},
			wantStatus:  "ready",
			wantReasons: []string{},
		},
		{
			name: "storage emergency stop",
			mutate: func(a *statusSnapshotAdapter) {
				a.storage = stubStatusStorage{
					writable: false,
					status: &storage.StorageStatus{
						Status:   storage.StatusEmergencyStopped,
						CanWrite: false,
					},
				}
			},
			wantStatus:  "degraded",
			wantReasons: []string{"storage_unavailable"},
		},
		{
			name: "persisted crash_loop algorithm without live watcher still reports runner_failed",
			mutate: func(a *statusSnapshotAdapter) {
				a.algorithms = stubStatusAlgorithms{
					{ID: "algo_helmet", Status: algorithm.StatusCrashLoop},
				}
			},
			wantStatus:  "degraded",
			wantReasons: []string{"runner_failed"},
		},
		{
			name: "all five degradation reasons preserve deterministic order",
			mutate: func(a *statusSnapshotAdapter) {
				a.storage = stubStatusStorage{err: errors.New("disk lost")}
				a.cameras = stubStatusCameras{Enabled: 4, Online: 2, Offline: 2}
				a.runners = stubStatusRunners{
					active:   1,
					failed:   1,
					byAlgoID: map[string]string{"algo_helmet": algorunner.StatusCrashLoop},
				}
				// Persisted CRASH_LOOP matching live watcher must NOT double-count!
				a.algorithms = stubStatusAlgorithms{
					{ID: "algo_helmet", Status: algorithm.StatusCrashLoop},
				}
				a.decode = stubStatusDecode{ready: true, stats: engine.DecodeStats{HardwareAvailable: false}}
				a.logSink = stubStatusLog{dropped: 5}
			},
			wantStatus: "degraded",
			wantReasons: []string{
				"storage_unavailable",
				"camera_offline",
				"runner_failed",
				"decode_degraded",
				"log_degraded",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter := baseHealthy()
			tc.mutate(adapter)

			pushSnap := adapter.Snapshot(context.Background())
			pullResp, err := adapter.DeviceStatus(context.Background())
			if err != nil {
				t.Fatalf("DeviceStatus error: %v", err)
			}

			if pushSnap.Status != tc.wantStatus || pullResp.Status != tc.wantStatus {
				t.Fatalf(
					"status mismatch: push=%q pull=%q want=%q",
					pushSnap.Status,
					pullResp.Status,
					tc.wantStatus,
				)
			}
			if !reflect.DeepEqual(pushSnap.Reasons, tc.wantReasons) {
				t.Fatalf("push reasons = %v, want %v", pushSnap.Reasons, tc.wantReasons)
			}
			if !reflect.DeepEqual(pullResp.Reasons, tc.wantReasons) {
				t.Fatalf("pull reasons = %v, want %v", pullResp.Reasons, tc.wantReasons)
			}

			// Verify non-double-counting when both live watcher and persisted row are CRASH_LOOP.
			isAllFiveCase := tc.name == "all five degradation reasons preserve deterministic order"
			if isAllFiveCase && pullResp.Workload.Runners.Failed != 1 {
				t.Fatalf("runners.failed = %d, want 1 (no double count)", pullResp.Workload.Runners.Failed)
			}

			// Verify sanitization: host filesystem paths must NEVER leak into either payload.
			pushJSON, err := json.Marshal(pushSnap)
			if err != nil {
				t.Fatalf("marshal pushSnap: %v", err)
			}
			pullJSON, err := json.Marshal(pullResp)
			if err != nil {
				t.Fatalf("marshal pullResp: %v", err)
			}
			leaked := strings.Contains(string(pushJSON), "/mnt/secret-nvme") ||
				strings.Contains(string(pullJSON), "/mnt/secret-nvme")
			if leaked {
				t.Fatalf("host filesystem path leaked into status payload: push=%s pull=%s", pushJSON, pullJSON)
			}
		})
	}
}

type blockingTaskSource struct{}

func (blockingTaskSource) CountEnabledTasks(ctx context.Context) (int, error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-time.After(5 * time.Second):
		return 99, nil
	}
}

func TestStatusSnapshotAdapter_Respects200msCollectionCeiling(t *testing.T) {
	adapter := &statusSnapshotAdapter{
		identity:              stubStatusIdentity("edge-box-timeout"),
		tasks:                 blockingTaskSource{},
		allowSoftwareFallback: true,
	}

	start := time.Now()
	snap := adapter.Snapshot(context.Background())
	elapsed := time.Since(start)

	if elapsed > 800*time.Millisecond {
		t.Fatalf("Snapshot exceeded 200ms collection ceiling, took %v", elapsed)
	}
	if snap.Workload.Tasks.Enabled != 0 {
		t.Fatalf("expected timed-out task count to fall back to 0, got %d", snap.Workload.Tasks.Enabled)
	}
}
