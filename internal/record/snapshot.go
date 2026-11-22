package record

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
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

// SnapshotPathCleaner 负责在抓拍生成或落盘失败时，将记录中预填的虚假图片路径清空。
type SnapshotPathCleaner interface {
	ClearRecordSnapshotPaths(ctx context.Context, table, id string) error
}

// snapshotOutput 描述一张输出图的目标位置。
type snapshotOutput struct {
	// boxIndex 为 -1 表示全幅图，>=0 表示 boxes[boxIndex] 的裁剪图。
	boxIndex int
	// relPath 是相对媒体根的路径，例如
	// snapshots/cam-01/2026-10-20/01J8XYZ.jpg。必须满足 mediafile 的路径契约
	// （首段为 snapshots，无前导斜杠，无 .. 段）。
	relPath   string
	protected bool
}

// snapshotJob 是一次待执行的抓拍写盘任务。
type snapshotJob struct {
	cameraID string
	pts      int64
	// captureTimeMs 是推理结果携带的帧采集时钟（毫秒）。> 0 时它是断言
	// 「证据图与检测结果同帧」的首选键：它比 pts 可靠 —— 插件可以不回传 pts，
	// RTSP 重连还会重置时基。
	captureTimeMs int64
	targetTable   string
	targetID      string
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

	// depsMu 保护装配期注入的清理器与观察者：worker goroutine 读、接线期写。
	depsMu    sync.RWMutex
	fileStore filestore.Store
	cleaner   SnapshotPathCleaner
	observer  SnapshotJobObserver

	localMu   sync.RWMutex
	localRoot string
	localMgr  *filestore.Manager

	queue     chan snapshotJob
	stop      chan struct{}
	startOnce sync.Once
	wg        sync.WaitGroup

	// enqMu 让「关闭」与「入队」互斥。
	//
	// 只靠 select 里的 <-s.stop 不够：队列有空间时两个 case 同时就绪，Go 会随机
	// 选一个，于是 Stop 之后仍可能入队成功，而 worker 已经退出 —— 记录里就留下
	// 一条永不落盘的路径。
	enqMu   sync.RWMutex
	stopped bool

	quality int

	// dropMu 保护抓拍统计（dropped / mismatched）：写侧分别在调用方 goroutine
	// 与 worker goroutine，读侧是运维接口。
	dropMu     sync.Mutex
	dropped    int64
	mismatched int64
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

// Start 拉起写盘 worker。（幂等：重复调用只启动一个 goroutine）
func (s *SnapshotStore) Start() {
	if s == nil {
		return
	}
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go s.worker()
	})
}

// Stop 关闭队列，并把已接收的任务做完后返回。（幂等）
func (s *SnapshotStore) Stop() {
	if s == nil {
		return
	}
	s.enqMu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.stop)
	}
	s.enqMu.Unlock()

	s.wg.Wait()

	s.localMu.Lock()
	localMgr := s.localMgr
	s.localMgr = nil
	s.localMu.Unlock()
	if localMgr != nil {
		if err := localMgr.Close(); err != nil {
			s.logger.Warn("close snapshot fallback filestore manager failed", zap.Error(err))
		}
	}
}

// SetFileStore configures the unified filestore backend for snapshot writes and lock backfills.
func (s *SnapshotStore) SetFileStore(store filestore.Store) {
	s.depsMu.Lock()
	s.fileStore = store
	s.depsMu.Unlock()
}

// Backend returns the active storage backend identifier ("local" or "s3").
func (s *SnapshotStore) Backend() string {
	if s == nil {
		return filestore.BackendLocal
	}
	s.depsMu.RLock()
	store := s.fileStore
	s.depsMu.RUnlock()
	if store == nil {
		return filestore.BackendLocal
	}
	return store.Backend()
}

func (s *SnapshotStore) resolveStore(ctx context.Context) (filestore.Store, error) {
	s.depsMu.RLock()
	store := s.fileStore
	s.depsMu.RUnlock()
	if store != nil {
		return store, nil
	}

	if s.media == nil {
		return nil, errors.New("snapshot media root is unavailable")
	}
	rootPath, err := s.media.MediaRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve media root: %w", err)
	}
	if rootPath == "" {
		return nil, errors.New("snapshot media root is empty")
	}

	s.localMu.RLock()
	if s.localMgr != nil && s.localRoot == rootPath {
		mgr := s.localMgr
		s.localMu.RUnlock()
		return mgr, nil
	}
	s.localMu.RUnlock()

	s.localMu.Lock()
	defer s.localMu.Unlock()
	if s.localMgr != nil && s.localRoot == rootPath {
		return s.localMgr, nil
	}
	localCfg := filestore.LocalConfig{
		RootDir: rootPath,
		Logger:  s.logger,
	}
	if s.localMgr == nil {
		s.localMgr = filestore.NewManager(filestore.ManagerConfig{
			Local:  localCfg,
			Logger: s.logger,
		})
	} else if err := s.localMgr.ConfigureLocal(localCfg); err != nil {
		return nil, err
	}
	s.localRoot = rootPath
	return s.localMgr, nil
}

// SetPathCleaner 注入落盘失败时的路径清理器。
func (s *SnapshotStore) SetPathCleaner(cleaner SnapshotPathCleaner) {
	s.depsMu.Lock()
	s.cleaner = cleaner
	s.depsMu.Unlock()
}

// SetJobObserver 注入写盘完成观察者，用于异步告警事件分发。
func (s *SnapshotStore) SetJobObserver(observer SnapshotJobObserver) {
	s.depsMu.Lock()
	s.observer = observer
	s.depsMu.Unlock()
}

func (s *SnapshotStore) pathCleaner() SnapshotPathCleaner {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.cleaner
}

func (s *SnapshotStore) jobObserver() SnapshotJobObserver {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.observer
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

// Mismatched 返回「抓拍取到的帧与检测结果不是同一帧」的累计次数。
//
// 非零说明 Native 的历史窗口已经覆盖不住算法回传延迟：证据图上的框会画在别的
// 画面上。这个计数是判断「需不需要调大副本环预算」的唯一现场依据。
func (s *SnapshotStore) Mismatched() int64 {
	s.dropMu.Lock()
	defer s.dropMu.Unlock()
	return s.mismatched
}

// Enqueue 提交一次抓拍写盘任务，非阻塞。
//
// 返回 false 表示任务被丢弃（不可用、已停机或队列已满），调用方应让路径字段留空。
func (s *SnapshotStore) Enqueue(job snapshotJob) bool {
	if s == nil || !s.Available() || len(job.outputs) == 0 {
		return false
	}

	var enqueued bool
	s.enqMu.RLock()
	if !s.stopped {
		select {
		case s.queue <- job:
			enqueued = true
		default:
		}
	}
	s.enqMu.RUnlock()

	if enqueued {
		return true
	}

	s.dropMu.Lock()
	s.dropped++
	s.dropMu.Unlock()
	s.logger.Warn("snapshot queue full or store stopped, dropping snapshot",
		zap.String("camera_id", job.cameraID))

	return false
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
	store, images, ok := s.capture(job)
	if !ok {
		s.clearRecordPaths(job)
		s.notifyObserver(job, nil)
		return
	}

	written := s.writeOutputs(job, store, images)
	if len(written) == 0 {
		s.clearRecordPaths(job)
	}
	s.notifyObserver(job, written)
}

// capture 依次做写入门闸、存储解析与原生抓拍。返回 false 表示本次什么都没写。
func (s *SnapshotStore) capture(job snapshotJob) (filestore.Store, map[int]engine.SnapshotImage, bool) {
	// 存储熔断时不写：写满的盘会让整机录像停摆，抓拍没有资格抢占。
	if s.gate != nil && !s.gate.CanWrite() {
		s.logger.Warn("storage gate is closed, skipping snapshot",
			zap.String("camera_id", job.cameraID))
		return nil, nil, false
	}

	ctx := context.Background()
	store, err := s.resolveStore(ctx)
	if err != nil {
		s.logger.Warn("resolve filestore for snapshot failed",
			zap.String("camera_id", job.cameraID), zap.Error(err))
		return nil, nil, false
	}

	images, err := s.provider.Snapshot(ctx, job.cameraID, engine.SnapshotOptions{
		Quality:       s.quality,
		PTS:           job.pts,
		CaptureTimeMs: job.captureTimeMs,
		Boxes:         job.boxes,
	})
	if err != nil {
		s.logger.Warn("snapshot capture failed",
			zap.String("camera_id", job.cameraID), zap.Error(err))
		return nil, nil, false
	}
	s.verifySource(job, images)

	// 按 boxIndex 建索引：原生层可能跳过退化框，不能按位置对应。
	byIndex := make(map[int]engine.SnapshotImage, len(images))
	for _, img := range images {
		byIndex[img.BoxIndex] = img
	}

	return store, byIndex, true
}

// verifySource 断言本次抓拍取到的确实是检测结果对应的那一帧。
//
// 证据图是追溯凭据：帧一旦错位，框就画在别的画面上，而这在界面上与「检测不准」
// 看起来完全一样。Native 侧已经把实际帧的身份（pts / capture_time_ms / exact）
// 回传上来，这里把它变成可检索的告警与计数，而不是静默落盘。
//
// ⚠️ 这里只告警、不改写记录：告警图仍然要落盘（有图总比没图强），但调用方与运维
// 能通过 mismatched 计数和这条 Warn 立刻看出「这批图不能当证据用」。
func (s *SnapshotStore) verifySource(job snapshotJob, images []engine.SnapshotImage) {
	if len(images) == 0 {
		return
	}
	source := images[0].Source
	mismatch := !source.Exact
	switch {
	case job.captureTimeMs > 0:
		mismatch = mismatch || source.CaptureTimeMs != job.captureTimeMs
	case job.pts > 0:
		mismatch = mismatch || source.PTS != job.pts
	default:
		// 结果既无 capture_time_ms 也无 pts：没有可断言的帧身份，不算错。
		return
	}
	if !mismatch {
		return
	}

	s.dropMu.Lock()
	s.mismatched++
	s.dropMu.Unlock()
	s.logger.Warn("snapshot frame does not match the inference result",
		zap.String("camera_id", job.cameraID),
		zap.String("table", job.targetTable),
		zap.String("record_id", job.targetID),
		zap.Int64("expected_pts", job.pts),
		zap.Int64("expected_capture_time_ms", job.captureTimeMs),
		zap.Int64("actual_pts", source.PTS),
		zap.Int64("actual_capture_time_ms", source.CaptureTimeMs),
		zap.Bool("actual_exact", source.Exact),
	)
}

// writeOutputs 把抓拍结果写到各自的目标路径，返回「哪些相对路径确实写成了」。
func (s *SnapshotStore) writeOutputs(
	job snapshotJob,
	store filestore.Store,
	byIndex map[int]engine.SnapshotImage,
) map[string]struct{} {
	written := make(map[string]struct{}, len(job.outputs))

	for _, out := range job.outputs {
		img, ok := byIndex[out.boxIndex]
		if !ok {
			s.logger.Warn(
				"snapshot image for output not found in result",
				zap.Int("box_index", out.boxIndex),
				zap.String("rel_path", out.relPath),
				zap.Int("total_images", len(byIndex)),
			)
			continue
		}
		if s.writeOutput(
			job,
			store,
			out,
			img,
		) {
			written[out.relPath] = struct{}{}
		}
	}

	return written
}

// writeOutput 写一张图。返回 false 表示该图未落盘（调用方据此清空对应路径）。
func (s *SnapshotStore) writeOutput(
	job snapshotJob,
	store filestore.Store,
	out snapshotOutput,
	img engine.SnapshotImage,
) bool {
	relKey := filepath.ToSlash(out.relPath)
	err := store.Put(
		context.Background(),
		relKey,
		bytes.NewReader(img.JPEG),
		filestore.PutOptions{
			Size:        int64(len(img.JPEG)),
			ContentType: "image/jpeg",
			Mode:        0o640,
			Protected:   out.protected,
		},
	)
	if err != nil {
		s.logger.Warn(
			"write snapshot file failed",
			zap.String("camera_id", job.cameraID),
			zap.String("key", relKey),
			zap.Error(err),
		)
		return false
	}

	s.logger.Debug(
		"snapshot file written successfully",
		zap.String("camera_id", job.cameraID),
		zap.String("key", relKey),
		zap.Int("bytes", len(img.JPEG)),
	)

	return true
}

// clearRecordPaths 尽力清空记录里预填的快照路径。失败只记 Warn。
func (s *SnapshotStore) clearRecordPaths(job snapshotJob) {
	cleaner := s.pathCleaner()
	if cleaner == nil || job.targetTable == "" || job.targetID == "" {
		return
	}
	if err := cleaner.ClearRecordSnapshotPaths(context.Background(), job.targetTable, job.targetID); err != nil {
		s.logger.Warn(
			"clear snapshot paths on failure failed",
			zap.String("table", job.targetTable),
			zap.String("id", job.targetID),
			zap.Error(err),
		)
	}
}

// notifyObserver 把每个输出路径的写入结果回报给观察者（用于异步告警事件分发）。
func (s *SnapshotStore) notifyObserver(job snapshotJob, written map[string]struct{}) {
	observer := s.jobObserver()
	if observer == nil || job.targetTable == "" || job.targetID == "" {
		return
	}

	outs := make([]SnapshotJobOutput, len(job.outputs))
	for i, out := range job.outputs {
		_, ok := written[out.relPath]
		outs[i] = SnapshotJobOutput{
			RelPath: out.relPath,
			Written: ok,
		}
	}
	observer.OnSnapshotJobDone(job.targetTable, job.targetID, SnapshotJobResult{Outputs: outs})
}

// EnsureEvidenceLocks writes missing .lock sidecars for existing local evidence
// images. Remote backends enforce protection via SQLite EvidenceGuard and return 0.
func (s *SnapshotStore) EnsureEvidenceLocks(ctx context.Context, relPaths []string) (int, error) {
	if s == nil {
		return 0, errors.New("snapshot store is nil")
	}
	if len(relPaths) == 0 {
		return 0, nil
	}

	store, err := s.resolveStore(ctx)
	if err != nil {
		return 0, err
	}
	if store.Backend() != filestore.BackendLocal {
		return 0, nil
	}
	markers, ok := store.(filestore.ProtectionMarkerStore)
	if !ok {
		return 0, nil
	}

	var written int
	for _, rel := range relPaths {
		locked, ensureErr := ensureEvidenceLock(
			ctx,
			store,
			markers,
			rel,
		)
		if ensureErr != nil {
			return written, ensureErr
		}
		if locked {
			written++
		}
	}

	return written, nil
}

func ensureEvidenceLock(
	ctx context.Context,
	store filestore.Store,
	markers filestore.ProtectionMarkerStore,
	rel string,
) (bool, error) {
	if rel == "" {
		return false, nil
	}
	cleanKey, err := filestore.ValidateObjectKeyForWrite(filepath.ToSlash(rel))
	if err != nil {
		return false, fmt.Errorf("validate evidence key %q: %w", rel, err)
	}

	exists, err := store.Exists(ctx, cleanKey)
	if err != nil {
		if errors.Is(err, filestore.ErrNotAFile) {
			return false, nil
		}
		return false, fmt.Errorf("stat evidence %q: %w", cleanKey, err)
	}
	if !exists {
		return false, nil
	}

	hasMarker, err := markers.HasProtectionMarker(ctx, cleanKey)
	if err != nil {
		return false, fmt.Errorf("check evidence lock %q: %w", cleanKey, err)
	}
	if hasMarker {
		return false, nil
	}
	if err := markers.EnsureProtectionMarker(ctx, cleanKey); err != nil {
		return false, fmt.Errorf("create evidence lock %q: %w", cleanKey, err)
	}
	return true, nil
}

// snapshotRelDir 生成 snapshots/{cameraID}/{YYYY-MM-DD} 形式的相对目录，
// 与 mediafile 的路径契约以及 storage-guidelines 的目录布局保持一致。
// 抓拍相对路径的唯一构造点：service 侧一律经 ingestFrame.relPath 调用它。
func snapshotRelDir(cameraID string, capturedAt time.Time) string {
	return fmt.Sprintf("snapshots/%s/%s", cameraID, capturedAt.UTC().Format("2006-01-02"))
}
