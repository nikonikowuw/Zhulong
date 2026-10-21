package record_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// jpegStub 返回一段结构上像 JPEG 的字节（SOI + 任意负载 + EOI）。
// 写盘测试只关心「字节被原样落地」，不需要真实可解码的图像。
func jpegStub(tag byte) []byte {
	return []byte{0xFF, 0xD8, tag, 0x11, 0x22, 0xFF, 0xD9}
}

// fakeSnapshotProvider 记录收到的请求并返回预置图片。
type fakeSnapshotProvider struct {
	mu       sync.Mutex
	calls    int
	cameraID string
	options  engine.SnapshotOptions
	// images 为空时按请求的 boxes 自动生成：boxIndex -1 一张 + 每个框一张。
	images []engine.SnapshotImage
	err    error
	// delay 用于验证「Stop 会等在飞任务完成」。
	delay time.Duration
}

func (p *fakeSnapshotProvider) Snapshot(_ context.Context, cameraID string, options engine.SnapshotOptions) ([]engine.SnapshotImage, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	p.mu.Lock()
	p.calls++
	p.cameraID = cameraID
	p.options = options
	imgs := p.images
	err := p.err
	p.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if imgs != nil {
		return imgs, nil
	}
	out := []engine.SnapshotImage{{BoxIndex: -1, Width: 64, Height: 48, JPEG: jpegStub(0xA0)}}
	for i := range options.Boxes {
		out = append(out, engine.SnapshotImage{
			BoxIndex: i, Width: 16, Height: 16, JPEG: jpegStub(byte(0xB0 + i)),
		})
	}
	return out, nil
}

func (p *fakeSnapshotProvider) snapshotCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// fakeMediaRoot 返回固定媒体根。
type fakeMediaRoot struct {
	root string
	err  error
}

func (m *fakeMediaRoot) MediaRoot(context.Context) (string, error) {
	return m.root, m.err
}

// fakeGate 可切换写入门闸。
type fakeGate struct {
	allow bool
}

func (g *fakeGate) CanWrite() bool { return g.allow }

// waitFor 轮询直到条件成立或超时，避免用固定 sleep 猜时序。
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSnapshotStoreWritesFullAndCrop(t *testing.T) {
	root := t.TempDir()
	provider := &fakeSnapshotProvider{}
	store := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	store.Start()
	defer store.Stop()

	fullRel := "snapshots/cam-01/2026-10-20/rec-1.jpg"
	cropRel := "snapshots/cam-01/2026-10-20/rec-1_face.jpg"

	if ok := store.Enqueue(record.NewSnapshotJobForTest(
		"cam-01",
		[]engine.SnapshotBox{{0.1, 0.2, 0.4, 0.5}},
		[]record.SnapshotOutputForTest{
			{BoxIndex: -1, RelPath: fullRel},
			{BoxIndex: 0, RelPath: cropRel},
		},
	)); !ok {
		t.Fatal("enqueue rejected")
	}

	waitFor(t, "both files written", func() bool {
		_, errFull := os.Stat(filepath.Join(root, fullRel))
		_, errCrop := os.Stat(filepath.Join(root, cropRel))
		return errFull == nil && errCrop == nil
	})

	// 全幅图与裁剪图的内容必须各自独立：不能因为索引错位而写反。
	fullBytes, err := os.ReadFile(filepath.Join(root, fullRel))
	if err != nil {
		t.Fatalf("read full: %v", err)
	}
	cropBytes, err := os.ReadFile(filepath.Join(root, cropRel))
	if err != nil {
		t.Fatalf("read crop: %v", err)
	}
	if !bytes.Equal(fullBytes, jpegStub(0xA0)) {
		t.Errorf("full image content mismatch: %x", fullBytes)
	}
	if !bytes.Equal(cropBytes, jpegStub(0xB0)) {
		t.Errorf("crop image content mismatch: %x", cropBytes)
	}

	// 请求必须带上质量与裁剪框，否则原生层只会出全幅图。
	if provider.options.Quality != 85 {
		t.Errorf("expected quality 85, got %d", provider.options.Quality)
	}
	if len(provider.options.Boxes) != 1 {
		t.Errorf("expected 1 box, got %d", len(provider.options.Boxes))
	}
}

func TestSnapshotStoreSkipsWhenGateClosed(t *testing.T) {
	root := t.TempDir()
	provider := &fakeSnapshotProvider{}
	gate := &fakeGate{allow: false}
	store := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, gate, 85, zap.NewNop())
	store.Start()
	defer store.Stop()

	rel := "snapshots/cam-01/2026-10-20/blocked.jpg"
	store.Enqueue(record.NewSnapshotJobForTest("cam-01", nil,
		[]record.SnapshotOutputForTest{{BoxIndex: -1, RelPath: rel}}))

	// 门闸关闭时根本不应该发起抓拍（连编码都不做），这才是真正的省资源。
	time.Sleep(150 * time.Millisecond)
	if provider.snapshotCalls() != 0 {
		t.Errorf("expected no snapshot calls when gate is closed, got %d", provider.snapshotCalls())
	}
	if _, err := os.Stat(filepath.Join(root, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no file, got err=%v", err)
	}
}

func TestSnapshotStoreSurvivesProviderFailure(t *testing.T) {
	root := t.TempDir()
	provider := &fakeSnapshotProvider{err: errors.New("no active AI stream")}
	store := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	store.Start()
	defer store.Stop()

	rel := "snapshots/cam-01/2026-10-20/missing.jpg"
	store.Enqueue(record.NewSnapshotJobForTest("cam-01", nil,
		[]record.SnapshotOutputForTest{{BoxIndex: -1, RelPath: rel}}))

	waitFor(t, "provider called once", func() bool { return provider.snapshotCalls() == 1 })
	if _, err := os.Stat(filepath.Join(root, rel)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected no file after provider failure, got err=%v", err)
	}
}

func TestSnapshotStoreStopDrainsQueue(t *testing.T) {
	root := t.TempDir()
	provider := &fakeSnapshotProvider{delay: 30 * time.Millisecond}
	store := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	store.Start()

	rel := "snapshots/cam-01/2026-10-20/drained.jpg"
	if !store.Enqueue(record.NewSnapshotJobForTest("cam-01", nil,
		[]record.SnapshotOutputForTest{{BoxIndex: -1, RelPath: rel}})) {
		t.Fatal("enqueue rejected")
	}

	// Stop 必须等在飞任务写完，否则 DB 里会留下指向不存在文件的路径。
	store.Stop()

	if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
		t.Fatalf("expected file to be written before Stop returns: %v", err)
	}
}

func TestSnapshotStoreUnavailableWhenDependenciesMissing(t *testing.T) {
	store := record.NewSnapshotStore(nil, nil, nil, 85, zap.NewNop())
	if store.Available() {
		t.Error("store with nil provider/media must report unavailable")
	}
	if store.Enqueue(record.NewSnapshotJobForTest("cam-01", nil,
		[]record.SnapshotOutputForTest{{BoxIndex: -1, RelPath: "snapshots/x.jpg"}})) {
		t.Error("enqueue must be rejected when dependencies are missing")
	}
}

// TestServiceEnqueuesSnapshotOnRecord 验证落库路径确实会登记抓拍任务，
// 且记录的 imagePath 与写入的文件路径一致。
func TestServiceEnqueuesSnapshotOnRecord(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	root := t.TempDir()
	provider := &fakeSnapshotProvider{}
	snapshots := record.NewSnapshotStore(provider, &fakeMediaRoot{root: root}, &fakeGate{allow: true}, 85, zap.NewNop())
	snapshots.Start()
	defer snapshots.Stop()

	svc := record.NewService(store, zap.NewNop())
	svc.SetSnapshotStore(snapshots)

	captureTime := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1000,
		Timestamp:   captureTime,
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.2, 0.3, 0.6, 0.8}, Label: "fire", Score: 0.91},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult: %v", err)
	}

	// 记录必须已经落库（抓拍是异步的，但落库是同步的）。
	var alarms []record.Alarm
	if err := dbStore.DB().Find(&alarms).Error; err != nil {
		t.Fatalf("query alarms: %v", err)
	}
	if len(alarms) != 1 {
		t.Fatalf("expected 1 alarm, got %d", len(alarms))
	}
	alarm := alarms[0]
	if alarm.ImagePath == "" {
		t.Fatal("alarm.ImagePath must not be empty")
	}

	// 抓拍任务应当被登记，最终文件出现在记录声明的路径上。
	waitFor(t, "alarm image written", func() bool {
		_, err := os.Stat(filepath.Join(root, alarm.ImagePath))
		return err == nil
	})

	// 烟火类告警无目标裁剪框：只应出全幅图，不应请求任何裁剪。
	if len(provider.options.Boxes) != 0 {
		t.Errorf("scene-wide alarm must not request crops, got %d boxes", len(provider.options.Boxes))
	}
}
