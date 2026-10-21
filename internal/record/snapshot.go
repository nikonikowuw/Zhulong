package record

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"go.uber.org/zap"
)

// ErrSnapshotUnsupported 表示当前构建或平台不支持抓拍；调用方不应重试。
var ErrSnapshotUnsupported = errors.New("snapshot capture is not supported")

// SnapshotProvider 对指定相机执行抓拍，返回全幅图与各裁剪框的 JPEG 字节流。
//
// 实现方是 camera.StreamHub。抽成接口而不是直接依赖 *camera.StreamHub，是为了让
// record 域的单元测试可以在无引擎、无解码会话的条件下验证写盘逻辑本身。
type SnapshotProvider interface {
	Snapshot(ctx context.Context, cameraID string, options engine.SnapshotOptions) ([]engine.SnapshotImage, error)
}

// MediaRootProvider 返回当前生效的媒体根目录。
//
// ⚠️ 必须每次现取、不可缓存：storage.Service.UpdateConfig 会热切目录，
// 缓存会让抓拍写进旧盘，而这些路径最终由 mediafile 服务以当前根解析。
type MediaRootProvider interface {
	MediaRoot(ctx context.Context) (string, error)
}

// WriteGate 判断存储当前是否允许创建新文件（空间熔断 / 只读挂载）。
type WriteGate interface {
	CanWrite() bool
}

// snapshotOutput 描述一张输出图的目标位置。
type snapshotOutput struct {
	// boxIndex 为 -1 表示全幅图，>=0 表示 boxes[boxIndex] 的裁剪图。
	boxIndex int
	// relPath 是相对媒体根的路径，例如
	// snapshots/cam-01/2026-10-20/01J8XYZ.jpg。必须满足 mediafile 的路径契约
	// （首段为 snapshots，无前导斜杠，无 .. 段）。
	relPath string
}

// snapshotJob 是一次待执行的抓拍写盘任务。
type snapshotJob struct {
	cameraID string
	// boxes 是归一化裁剪框，索引 i 对应 boxIndex == i 的输出。
	boxes   []engine.SnapshotBox
	outputs []snapshotOutput
}

// SnapshotStore 把 JPEG 落盘到媒体根下的 snapshots 目录。
//
// 三个设计约束（都是「写错就出事」级别）：
//  1. **失败不阻断落库**：抓拍图是证据的增强项，不是记录的前置条件。任何写盘失败
//     都只让对应路径字段留空，记录照常落库，前端显示占位图。
//  2. **不阻塞 AI 消费路径**：JPEG 编码是几十毫秒级的同步操作，绝不能在
//     consumeAIResults 的 goroutine 里执行。ProcessInferenceResult 只登记任务，
//     由本结构的后台 worker 消费。
//  3. **有界队列 + 满则丢**：磁盘慢于推理时不堆积内存。宁可少一张图，也不能让
//     推理结果消费路径因为磁盘变慢而积压。
type SnapshotStore struct {
	provider SnapshotProvider
	media    MediaRootProvider
	gate     WriteGate
	logger   *zap.Logger

	queue    chan snapshotJob
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup

	quality int
	dropped int64
	dropMu  sync.Mutex
}

// NewSnapshotStore 构造抓拍落盘组件，quality 为 JPEG 质量（0 表示使用默认值）。
//
// ⚠️ 纯构造函数：不创建目录、不启动 goroutine。worker 由 Start 拉起，
// 与 Fx「构造函数必须纯粹」的约定一致。
func NewSnapshotStore(
	provider SnapshotProvider,
	media MediaRootProvider,
	gate WriteGate,
	quality int,
	logger *zap.Logger,
) *SnapshotStore {
	if logger == nil {
		logger = zap.NewNop()
	}
	if quality <= 0 || quality > 100 {
		quality = engine.DefaultSnapshotQuality
	}
	return &SnapshotStore{
		provider: provider,
		media:    media,
		gate:     gate,
		logger:   logger.Named("record.snapshot"),
		// 每个相机的抓拍受 3s 冷却约束，同时在飞的任务量很小，32 已是宽裕上界。
		queue:   make(chan snapshotJob, 32),
		stop:    make(chan struct{}),
		quality: quality,
	}
}

// Start 拉起写盘 worker。
func (s *SnapshotStore) Start() {
	s.wg.Add(1)
	go s.worker()
}

// Stop 关闭队列，并把已接收的任务做完后返回。
func (s *SnapshotStore) Stop() {
	s.stopOnce.Do(func() { close(s.stop) })
	s.wg.Wait()
}

// Available 报告抓拍能力是否可用（依赖注入是否齐备）。
func (s *SnapshotStore) Available() bool {
	return s != nil && s.provider != nil && s.media != nil
}

// Dropped 返回因队列满而被丢弃的任务数，供运维观测。
func (s *SnapshotStore) Dropped() int64 {
	s.dropMu.Lock()
	defer s.dropMu.Unlock()
	return s.dropped
}

// Enqueue 提交一次抓拍写盘任务，非阻塞。
//
// 返回 false 表示任务被丢弃（不可用或队列已满），调用方应让路径字段留空。
func (s *SnapshotStore) Enqueue(job snapshotJob) bool {
	if !s.Available() || len(job.outputs) == 0 {
		return false
	}
	select {
	case s.queue <- job:
		return true
	case <-s.stop:
		return false
	default:
		s.dropMu.Lock()
		s.dropped++
		s.dropMu.Unlock()
		s.logger.Warn("snapshot queue full, dropping snapshot",
			zap.String("camera_id", job.cameraID))
		return false
	}
}

func (s *SnapshotStore) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.stop:
			// 停机时把队列里已接收的任务做完：调用方已经把这些路径回填进记录，
			// 直接丢弃会留下指向不存在文件的 DB 行。
			for {
				select {
				case job := <-s.queue:
					s.run(job)
				default:
					return
				}
			}
		case job := <-s.queue:
			s.run(job)
		}
	}
}

// run 执行一次抓拍 + 写盘。所有错误只记日志，不向上传播。
func (s *SnapshotStore) run(job snapshotJob) {
	// 存储熔断时不写：写满的盘会让整机录像停摆，抓拍没有资格抢占。
	if s.gate != nil && !s.gate.CanWrite() {
		s.logger.Debug("storage gate is closed, skipping snapshot",
			zap.String("camera_id", job.cameraID))
		return
	}

	ctx := context.Background()
	root, err := s.media.MediaRoot(ctx)
	if err != nil {
		s.logger.Warn("resolve media root for snapshot failed",
			zap.String("camera_id", job.cameraID), zap.Error(err))
		return
	}

	images, err := s.provider.Snapshot(ctx, job.cameraID, engine.SnapshotOptions{
		Quality: s.quality,
		Boxes:   job.boxes,
	})
	if err != nil {
		s.logger.Debug("snapshot capture failed",
			zap.String("camera_id", job.cameraID), zap.Error(err))
		return
	}

	// 按 boxIndex 建索引：原生层可能跳过退化框，不能按位置对应。
	byIndex := make(map[int]engine.SnapshotImage, len(images))
	for _, img := range images {
		byIndex[img.BoxIndex] = img
	}

	for _, out := range job.outputs {
		img, ok := byIndex[out.boxIndex]
		if !ok {
			continue
		}
		abs := filepath.Join(root, out.relPath)
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			s.logger.Warn("create snapshot directory failed",
				zap.String("camera_id", job.cameraID), zap.String("dir", filepath.Dir(abs)), zap.Error(err))
			return
		}
		// 0o640：证据图需要被服务进程之外的只读消费者（巡检脚本、导出工具）读取，
		// 因此保留组读权限；但不给其他用户，避免多租户宿主上的越权读取。
		if err := os.WriteFile(abs, img.JPEG, 0o640); err != nil {
			s.logger.Warn("write snapshot file failed",
				zap.String("camera_id", job.cameraID), zap.String("path", abs), zap.Error(err))
		}
	}
}

// snapshotRelDir 生成 snapshots/{cameraID}/{YYYY-MM-DD} 形式的相对目录，
// 与 mediafile 的路径契约以及 storage-guidelines 的目录布局保持一致。
func snapshotRelDir(cameraID string, capturedAt time.Time) string {
	return fmt.Sprintf("snapshots/%s/%s", cameraID, capturedAt.UTC().Format("2006-01-02"))
}

// SnapshotOutputForTest 是 snapshotOutput 的对外镜像，仅供同目录测试构造用例。
//
// ⚠️ 生产代码不应引用本类型：它存在只是因为 Go 的 external test package
// （record_test）无法访问未导出字段，而 Enqueue 需要一个可构造的任务描述。
type SnapshotOutputForTest struct {
	BoxIndex int
	RelPath  string
}

// NewSnapshotJobForTest 构造一个等价于内部 snapshotJob 的任务，仅供测试使用。
func NewSnapshotJobForTest(cameraID string, boxes []engine.SnapshotBox, outputs []SnapshotOutputForTest) snapshotJob {
	converted := make([]snapshotOutput, 0, len(outputs))
	for _, out := range outputs {
		converted = append(converted, snapshotOutput{boxIndex: out.BoxIndex, relPath: out.RelPath})
	}
	return snapshotJob{cameraID: cameraID, boxes: boxes, outputs: converted}
}
