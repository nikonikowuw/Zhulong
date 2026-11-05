package record_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

type recordAlarmCollector struct {
	mu     sync.Mutex
	alarms []record.AlarmEvent
}

func (c *recordAlarmCollector) OnAlarm(_ context.Context, ev record.AlarmEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alarms = append(c.alarms, ev)
}

func (c *recordAlarmCollector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.alarms)
}

func (c *recordAlarmCollector) get(index int) record.AlarmEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alarms[index]
}

func TestAlarmListenerSnapshotJobObserver(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	collector := &recordAlarmCollector{}
	svc.SetAlarmListener(collector)
	svc.Start(ctx)
	defer svc.Stop()

	root := t.TempDir()
	provider := &fakeSnapshotProvider{
		images: []engine.SnapshotImage{
			{BoxIndex: -1, JPEG: []byte("full-jpeg")},
			{BoxIndex: 0, JPEG: []byte("crop-jpeg")},
		},
	}
	snapshots := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	snapshots.Start()
	defer snapshots.Stop()

	svc.SetSnapshotStore(snapshots)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1000,
		Timestamp:   time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC),
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Label: "no_helmet_alarm", Score: 0.95},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult: %v", err)
	}

	// 等待抓拍 worker 执行与回调
	deadline := time.Now().Add(3 * time.Second)
	for collector.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if collector.count() != 1 {
		t.Fatalf("expected 1 alarm event, got %d", collector.count())
	}

	ev := collector.get(0)
	if !ev.ImageReady || !ev.CropReady {
		t.Fatalf("expected both images ready, got ImageReady=%v, CropReady=%v", ev.ImageReady, ev.CropReady)
	}
	if ev.ImagePath == "" || ev.CropPath == "" {
		t.Fatalf("expected non-empty paths, got ImagePath=%q, CropPath=%q", ev.ImagePath, ev.CropPath)
	}
	if ev.AlarmType != "no_helmet_alarm" || ev.CameraID != "cam_01" {
		t.Fatalf("unexpected alarm properties: %+v", ev)
	}
	// Extra/Attributes 必须是空 map 而不是 nil：下游（alertpush / SSE）不该
	// 为 null 与 {} 写两套分支，两者在 JSON 里是不同形状。
	if ev.Alarm == nil || ev.Alarm.Extra == nil {
		t.Fatalf("alarm extra must be a non-nil map, got %+v", ev.Alarm)
	}
}

func TestAlarmListenerPartialSnapshotFailure(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	collector := &recordAlarmCollector{}
	svc.SetAlarmListener(collector)
	svc.Start(ctx)
	defer svc.Stop()

	root := t.TempDir()
	// 只提供全幅图，缺少裁剪图 (boxIndex: 0)
	provider := &fakeSnapshotProvider{
		images: []engine.SnapshotImage{
			{BoxIndex: -1, JPEG: []byte("full-jpeg")},
		},
	}
	snapshots := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	snapshots.Start()
	defer snapshots.Stop()

	svc.SetSnapshotStore(snapshots)

	res := aitypes.InferenceResult{
		CameraID:    "cam_02",
		AlgorithmID: "algo_01",
		PTS:         2000,
		Timestamp:   time.Date(2026, 11, 3, 11, 0, 0, 0, time.UTC),
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Label: "no_helmet_alarm", Score: 0.95},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for collector.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if collector.count() != 1 {
		t.Fatalf("expected 1 alarm event, got %d", collector.count())
	}

	ev := collector.get(0)
	if !ev.ImageReady {
		t.Errorf("expected ImageReady=true, got false")
	}
	if ev.CropReady {
		t.Errorf("expected CropReady=false, got true")
	}
	if ev.CropPath != "" {
		t.Errorf("expected CropPath to be cleared on failure, got %q", ev.CropPath)
	}
}

func TestAlarmListenerSweeperTimeoutFallback(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	collector := &recordAlarmCollector{}
	svc.SetAlarmListener(collector)
	svc.Start(ctx)
	defer svc.Stop()

	// 模拟抓拍挂起（不启动 SnapshotStore 的 worker，导致任务永远停留在队列中）
	provider := &fakeSnapshotProvider{}
	snapshots := record.NewSnapshotStore(
		provider,
		&fakeMediaRoot{root: t.TempDir()},
		&fakeGate{allow: true},
		85,
		zap.NewNop(),
	)
	// 刻意不调 snapshots.Start()
	svc.SetSnapshotStore(snapshots)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         3000,
		Timestamp:   time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC),
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Label: "no_helmet_alarm", Score: 0.95},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult: %v", err)
	}

	// 5s 兜底 sweeper 触发，等待 6s
	deadline := time.Now().Add(7 * time.Second)
	for collector.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}

	if collector.count() != 1 {
		t.Fatalf("expected 1 alarm event from sweeper timeout, got %d", collector.count())
	}

	ev := collector.get(0)
	if ev.ImageReady || ev.CropReady {
		t.Fatalf("expected both ready to be false on sweeper timeout, got ImageReady=%v, CropReady=%v",
			ev.ImageReady, ev.CropReady)
	}
}
