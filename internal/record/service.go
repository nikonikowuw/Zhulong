package record

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

const (
	// pendingAlarmTimeout 是「记录已落库、抓拍尚未完成」事件的兜底超时。
	// 超过它仍没有快照回调，就降级为「图片未就绪」分发出去，避免事件无限悬挂。
	pendingAlarmTimeout = 5 * time.Second

	// pendingAlarmSweepInterval 是上面兜底扫描的周期。
	pendingAlarmSweepInterval = 500 * time.Millisecond

	// maxPendingAlarms 是在飞事件的硬上限：满了就退化为「先分发、不等图」，
	// 内存不得随写盘速度波动而增长。
	maxPendingAlarms = 256
)

type pendingAlarmEntry struct {
	alarm     AlarmEvent
	createdAt time.Time
}

// ingestFrame 是一次推理结果在写库期间共享的只读上下文。
//
// 打包 res / algo / capturedAt 的目的不是省参数，而是消灭**派生真值**：
// 目录日期与抓拍路径都从 capturedAt 现算（见 relPath），不再作为 dateStr 参数
// 沿调用链复制，避免某处传错日期导致图写进别的目录。
type ingestFrame struct {
	res        aitypes.InferenceResult
	algo       AlgorithmSnapshot
	capturedAt time.Time
}

// relPath 生成 snapshots/{camera}/{YYYY-MM-DD}/{id}{suffix} 形式的相对路径。
// 布局只由 snapshotRelDir 定义一次，与 mediafile / storage 的目录契约同源。
func (f ingestFrame) relPath(id, suffix string) string {
	return path.Join(snapshotRelDir(f.res.CameraID, f.capturedAt), id+suffix)
}

// Service coordinates perception records, alarms, cooldown deduplication, and persistence.
type Service struct {
	store      *Store
	cooldown   *CooldownManager
	tracker    *targetTracker
	algorithms *AlgorithmResolver
	logger     *zap.Logger

	// depsMu 保护两个装配期注入的外部依赖。
	//
	// 注入本应在 Start 之前完成，但「靠调用方时序保证无竞争」不是并发安全，
	// 只是没被 -race 撞到。读侧每次处理一条推理结果取一次快照，代价可忽略。
	depsMu        sync.RWMutex
	snapshots     *SnapshotStore
	alarmListener AlarmListener

	pendingMu     sync.Mutex
	pendingAlarms map[string]*pendingAlarmEntry
	sweeperStop   chan struct{}
	sweeperDone   chan struct{}
	accepting     bool
}

// NewService creates a new record Service.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:         store,
		cooldown:      NewCooldownManager(DefaultCooldown),
		tracker:       newTargetTracker(defaultTrackerConfig()),
		algorithms:    NewAlgorithmResolver(store, logger.Named("record.algorithms")),
		logger:        logger,
		pendingAlarms: make(map[string]*pendingAlarmEntry),
	}
}

// snapshotSink 返回已注入的抓拍落盘组件（未注入时为 nil）。
func (s *Service) snapshotSink() *SnapshotStore {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.snapshots
}

// eventListener 返回已注入的事件监听器（未注入时为 nil）。
func (s *Service) eventListener() AlarmListener {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.alarmListener
}

// TrackerStats returns read-only operational metrics of the target identity tracker.
func (s *Service) TrackerStats() TrackerStats {
	if s.tracker == nil {
		return TrackerStats{}
	}
	return s.tracker.Stats()
}

// SetCooldownWindow sets the deduplication window for tracking cooldowns.
func (s *Service) SetCooldownWindow(d time.Duration) {
	s.cooldown.SetCooldown(d)
}

// SetSnapshotStore 注入抓拍落盘组件。
//
// ⚠️ 用 setter 而不是构造参数，是为了守住领域内聚：record 域由
// NewRecordComponents 一次性装配，而抓拍需要 camera.StreamHub 与 storage 两个
// 外部依赖，把它们塞进构造函数会让 record 域反过来依赖相机与存储的生命周期顺序。
// 未注入时进程仍能正常落库，只是路径字段留空（与「抓拍失败」表现一致）。
func (s *Service) SetSnapshotStore(store *SnapshotStore) {
	s.depsMu.Lock()
	s.snapshots = store
	s.depsMu.Unlock()

	if store != nil {
		store.SetJobObserver(s)
	}
}

// SetAlarmListener 注入告警事件监听器。
func (s *Service) SetAlarmListener(l AlarmListener) {
	s.depsMu.Lock()
	s.alarmListener = l
	s.depsMu.Unlock()
}

// Start 启动兜底 sweeper 协程。
//
// ctx 是宿主生命周期：runtime 在 shutdown 时先 cancel 它再调 Stop，两条路径等价。
func (s *Service) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	s.pendingMu.Lock()
	if s.sweeperStop != nil {
		s.pendingMu.Unlock()
		return
	}
	stopCh := make(chan struct{})
	doneCh := make(chan struct{})
	s.sweeperStop = stopCh
	s.sweeperDone = doneCh
	s.accepting = true
	s.pendingMu.Unlock()

	go s.sweepPending(ctx, stopCh, doneCh)
}

// Stop 停止 sweeper 并丢弃未完成条目。
func (s *Service) Stop() {
	s.pendingMu.Lock()
	s.accepting = false
	stopCh := s.sweeperStop
	doneCh := s.sweeperDone
	s.sweeperStop = nil
	s.sweeperDone = nil
	clear(s.pendingAlarms)
	s.pendingMu.Unlock()

	if stopCh != nil {
		close(stopCh)
		<-doneCh
	}
}

// sweepPending 把超时未等到快照的事件降级分发出去。
func (s *Service) sweepPending(ctx context.Context, stopCh <-chan struct{}, doneCh chan<- struct{}) {
	defer close(doneCh)

	ticker := time.NewTicker(pendingAlarmSweepInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 宿主取消 recordCtx：不再分发新事件，剩余条目由 Stop 清空。
			return
		case <-stopCh:
			return
		case now := <-ticker.C:
			listener := s.eventListener()
			if listener == nil {
				continue
			}
			for _, ev := range s.expirePending(now) {
				listener.OnAlarm(context.Background(), ev)
			}
		}
	}
}

// expirePending 摘除并返回所有已超时的待关联事件。
func (s *Service) expirePending(now time.Time) []AlarmEvent {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	var expired []AlarmEvent
	for id, entry := range s.pendingAlarms {
		if now.Sub(entry.createdAt) <= pendingAlarmTimeout {
			continue
		}
		delete(s.pendingAlarms, id)
		expired = append(expired, entry.alarm.withoutEvidence())
	}
	return expired
}

// OnSnapshotJobDone 实现 SnapshotJobObserver 接口。
func (s *Service) OnSnapshotJobDone(targetTable, targetID string, result SnapshotJobResult) {
	switch targetTable {
	case tableAlarms, tableRecognitions, tableCaptures:
	default:
		return
	}

	s.pendingMu.Lock()
	if !s.accepting {
		s.pendingMu.Unlock()
		return
	}
	entry, ok := s.pendingAlarms[targetID]
	if !ok {
		s.pendingMu.Unlock()
		return
	}
	delete(s.pendingAlarms, targetID)
	s.pendingMu.Unlock()

	// 只记录真正写成的路径：调用方用 map 存在性判断「这张图可用」。
	written := make(map[string]struct{}, len(result.Outputs))
	for _, out := range result.Outputs {
		if out.Written {
			written[out.RelPath] = struct{}{}
		}
	}

	ev := entry.alarm
	_, imageWritten := written[ev.ImagePath]
	ev.ImageReady = ev.ImagePath != "" && imageWritten
	if !ev.ImageReady {
		ev.ImagePath = ""
	}
	_, cropWritten := written[ev.CropPath]
	ev.CropReady = ev.CropPath != "" && cropWritten
	if !ev.CropReady {
		ev.CropPath = ""
	}

	if listener := s.eventListener(); listener != nil {
		listener.OnAlarm(context.Background(), ev)
	}
}

// EnsureAlarmEvidenceLocks backfills .lock sidecars for historical alarm images.
// It is best-effort at startup: a failure is logged by the lifecycle, and the
// storage evidence guard still fails closed for any alarm IDs it can query.
func (s *Service) EnsureAlarmEvidenceLocks(ctx context.Context) error {
	if s == nil || s.store == nil {
		return nil
	}
	sink := s.snapshotSink()
	if sink == nil {
		return nil
	}

	paths, err := s.store.AlarmEvidencePaths(ctx)
	if err != nil {
		return fmt.Errorf("list alarm evidence paths: %w", err)
	}
	if len(paths) == 0 {
		return nil
	}

	written, err := sink.EnsureEvidenceLocks(ctx, paths)
	if err != nil {
		return fmt.Errorf("backfill alarm evidence locks: %w", err)
	}
	s.logger.Info("alarm evidence lock backfill completed",
		zap.Int("paths", len(paths)),
		zap.Int("written", written),
	)

	return nil
}

// ProcessInferenceResult consumes an inference result from StreamHub, applies
// tracking cooldowns, resolves enrolled face gallery images, and persists records.
//
// 返回的 error 只表示**持久化失败**（记录必须落库，抓拍图是增强项）；调用方
// StreamHub 会连同 camera_id 记录一条 Warn。抓拍落盘失败不会上抛 —— 它是后台
// worker 的事，与本函数能否消费这条结果无关。
func (s *Service) ProcessInferenceResult(ctx context.Context, res aitypes.InferenceResult) error {
	if res.CameraID == "" {
		return nil
	}

	capturedAt := res.Timestamp.UTC()
	if res.Timestamp.IsZero() {
		capturedAt = time.Now().UTC()
	}

	// 解析算法身份快照（幂等、带短 TTL 缓存）。一次结果的所有记录共用同一个
	// 算法 ID，因此只查一次库。
	f := ingestFrame{
		res:        res,
		algo:       s.resolveAlgorithm(ctx, res.AlgorithmID),
		capturedAt: capturedAt,
	}

	switch res.TaskType {
	case aitypes.TaskTypePersonStructure, aitypes.TaskTypeSceneStructure:
		return s.processPersonStructure(ctx, f)
	case aitypes.TaskTypeVehicleStructure:
		return s.processVehicleStructure(ctx, f)
	case aitypes.TaskTypeDetection:
		return s.processDetection(ctx, f)
	case aitypes.TaskTypeClassification:
		return s.processClassification(ctx, f)
	default:
		return nil
	}
}

// resolveAlgorithm 返回该结果对应的算法身份快照。
//
// 找不到（算法已卸载 / 载荷未带 ID / 查询失败）时返回零值快照，调用方会写入
// 空名称与 NULL 外键 —— 记录本身必须落库，来源标注的完整性是次级信息。
func (s *Service) resolveAlgorithm(ctx context.Context, algorithmID string) AlgorithmSnapshot {
	if algorithmID == "" || s.algorithms == nil {
		return AlgorithmSnapshot{}
	}
	return s.algorithms.resolveAlgorithmSnapshots(ctx, []string{algorithmID})[algorithmID]
}

// resolveIdentities maps target bounding boxes to stable host-exclusive tracking identities.
func (s *Service) resolveIdentities(b bucketKey, obs []observation, at time.Time) []int64 {
	if s.tracker == nil {
		return make([]int64, len(obs))
	}
	return s.tracker.resolveFrame(b, obs, at, 2*s.cooldown.Window())
}

// =========================================================================
// person_structure / scene_structure
// =========================================================================

// personStructurePlan 是推理结果展平后的中间形态。
//
// 「展平」指把挂在人体上的人脸提到同一层：下游只面对两种对象 —— 带身份的人体
// 和孤立人脸，不必在每处都处理两种载荷形态。
type personStructurePlan struct {
	persons []aitypes.PersonItem
	// faces 是全量人脸（含挂在人体上的）；isolated 只有 PersonIndex == -1 的孤立人脸。
	faces    []aitypes.FaceStructureItem
	isolated []aitypes.FaceStructureItem
}

// planPersonStructure 防御性展平载荷。
//
// 兼容两种输入：已经归一化的（res.Faces 非空）与直接构造结构体的（人脸只挂在
// Persons 下）。近景人体降级为孤立人脸，其余人体保留并接管其 Faces。
func planPersonStructure(res aitypes.InferenceResult) personStructurePlan {
	faces := make([]aitypes.FaceStructureItem, 0, len(res.Faces))
	faces = append(faces, res.Faces...)

	if len(res.Faces) > 0 || len(res.Persons) == 0 {
		return personStructurePlan{persons: res.Persons, faces: faces, isolated: isolatedFaces(faces)}
	}

	persons := make([]aitypes.PersonItem, 0, len(res.Persons))
	for _, p := range res.Persons {
		if p.IsFaceCloseup() {
			if len(p.Faces) == 0 {
				continue
			}
			f := p.Faces[0]
			faces = append(faces, aitypes.FaceStructureItem{
				Box:           f.Box,
				Quality:       f.Quality,
				Landmarks:     f.Landmarks,
				Matched:       f.Matched,
				Similarity:    f.Similarity,
				PersonIndex:   -1,
				PersonID:      f.PersonID,
				PersonName:    f.PersonName,
				MatchedFaceID: f.MatchedFaceID,
				IsFused:       f.IsFused,
				Candidates:    f.Candidates,
			})
			continue
		}

		personIdx := int32(len(persons))
		for _, f := range p.Faces {
			faces = append(faces, aitypes.FaceStructureItem{
				Box:           f.Box,
				Quality:       f.Quality,
				Landmarks:     f.Landmarks,
				Matched:       f.Matched,
				Similarity:    f.Similarity,
				PersonIndex:   personIdx,
				PersonID:      f.PersonID,
				PersonName:    f.PersonName,
				MatchedFaceID: f.MatchedFaceID,
				IsFused:       f.IsFused,
				Candidates:    f.Candidates,
			})
		}
		p.Faces = nil
		persons = append(persons, p)
	}

	return personStructurePlan{persons: persons, faces: faces, isolated: isolatedFaces(faces)}
}

func isolatedFaces(faces []aitypes.FaceStructureItem) []aitypes.FaceStructureItem {
	isolated := make([]aitypes.FaceStructureItem, 0, len(faces))
	for _, f := range faces {
		if f.PersonIndex == -1 {
			isolated = append(isolated, f)
		}
	}
	return isolated
}

func personObservations(persons []aitypes.PersonItem) []observation {
	obs := make([]observation, len(persons))
	for i, p := range persons {
		obs[i] = observation{box: p.BodyBox, score: p.BodyScore}
	}
	return obs
}

func faceObservations(faces []aitypes.FaceStructureItem) []observation {
	obs := make([]observation, len(faces))
	for i, f := range faces {
		obs[i] = observation{box: f.Box, score: f.Quality}
	}
	return obs
}

// trackIDAt 越界时返回 0：关联器与载荷偶发不同步时退化，而不是 panic。
func trackIDAt(ids []int64, i int) int64 {
	if i < 0 || i >= len(ids) {
		return 0
	}
	return ids[i]
}

func (s *Service) processPersonStructure(ctx context.Context, f ingestFrame) error {
	plan := planPersonStructure(f.res)

	personIDs := s.resolveIdentities(
		bucketKey{camera: f.res.CameraID, algorithm: f.res.AlgorithmID, kind: "person"},
		personObservations(plan.persons),
		f.capturedAt,
	)
	for _, p := range plan.persons {
		s.debugIgnoredTrackID(f.res.CameraID, p.TrackID)
	}

	faceIDs := s.resolveIdentities(
		bucketKey{camera: f.res.CameraID, algorithm: f.res.AlgorithmID, kind: "face"},
		faceObservations(plan.isolated),
		f.capturedAt,
	)

	captures, captureJobs := s.planPersonCaptures(f, plan, personIDs, faceIDs)
	recognitions, recognitionJobs := s.planRecognitions(ctx, f, plan, personIDs, faceIDs)

	var errs []error
	if len(recognitions) > 0 {
		if err := s.store.CreateRecognitionBatch(ctx, recognitions); err != nil {
			errs = append(errs, fmt.Errorf("insert recognitions: %w", err))
		} else {
			dispatchCreated(s, ctx, recognitions, recognitionJobs, eventFromRecognition)
		}
	}
	// 人体/人脸抓拍只落库，不对外发事件（识别事件已是同一帧的下游表示）。
	if len(captures) > 0 {
		if err := s.store.CreateCaptureBatch(ctx, captures); err != nil {
			errs = append(errs, fmt.Errorf("insert captures: %w", err))
		} else {
			s.enqueueSnapshots(ctx, captureJobs)
		}
	}

	return errors.Join(errs...)
}

// planPersonCaptures 决策本轮要落库的人体抓拍与孤立人脸抓拍。
func (s *Service) planPersonCaptures(
	f ingestFrame,
	plan personStructurePlan,
	personIDs []int64,
	faceIDs []int64,
) ([]*Capture, []snapshotJob) {
	captures := make([]*Capture, 0, len(plan.persons)+len(plan.isolated))
	jobs := make([]snapshotJob, 0, cap(captures))

	for i, p := range plan.persons {
		trackID := trackIDAt(personIDs, i)
		if !s.cooldown.Allow(personCaptureCooldownKey(f.res.CameraID, trackID, p.BodyBox), f.capturedAt) {
			continue
		}
		capture, job := buildCaptureRecordAndJob(f, captureSpec{
			targetType: "person",
			trackID:    trackID,
			score:      p.BodyScore,
			box:        p.BodyBox,
			attrsJSON:  marshalMapOrDefault(p.Attributes),
		})
		captures = append(captures, capture)
		jobs = append(jobs, job)
	}

	for i, face := range plan.isolated {
		trackID := trackIDAt(faceIDs, i)
		if !s.cooldown.Allow(faceCaptureCooldownKey(f.res.CameraID, trackID, face.Box), f.capturedAt) {
			continue
		}
		capture, job := buildCaptureRecordAndJob(f, captureSpec{
			targetType: "face",
			trackID:    trackID,
			score:      face.Quality,
			box:        face.Box,
			attrsJSON:  "{}",
		})
		captures = append(captures, capture)
		jobs = append(jobs, job)
	}

	return captures, jobs
}

// recognitionDraft 是一条通过冷却判定的待落库识别。
//
// 冷却先于底库解析：默认窗口 3s，而推理结果按帧到达，先解析再判冷却会让每一帧
// 都付一次 person_faces JOIN —— 绝大多数帧的记录随后都会被丢弃。
type recognitionDraft struct {
	face        aitypes.FaceStructureItem
	trackID     int64
	bodyBox     [4]float64
	personAttrs map[string]any
}

// planRecognitions 决策本轮要落库的识别记录。
//
// 两级抑制：宿主身份键（trk）与已登记人员键（person）必须同时通过才落一条，
// 否则同一张脸在「换人/换框」时会被当成两个目标重复上报。
func (s *Service) planRecognitions(
	ctx context.Context,
	f ingestFrame,
	plan personStructurePlan,
	personIDs []int64,
	faceIDs []int64,
) ([]*Recognition, []snapshotJob) {
	drafts := s.filterRecognitions(f, plan, personIDs, faceIDs)

	// 只为通过冷却的记录回查底库：被抑制的帧不产生记录，也不需要身份归属。
	gallery := s.resolveGalleryFaces(ctx, draftFaceIDs(drafts))

	recognitions := make([]*Recognition, 0, len(drafts))
	jobs := make([]snapshotJob, 0, len(drafts))
	for _, d := range drafts {
		rec, job := buildRecognitionRecordAndJob(f, d.face, d.bodyBox, d.personAttrs, d.trackID, gallery[d.face.MatchedFaceID])
		recognitions = append(recognitions, rec)
		jobs = append(jobs, job)
	}

	return recognitions, jobs
}

// filterRecognitions 用两级抑制键筛出本帧要落库的识别。判定是原子的（AllowAll），
// 半通过（trk 命中、person 未命中）不会留下任何时间戳。
func (s *Service) filterRecognitions(
	f ingestFrame,
	plan personStructurePlan,
	personIDs []int64,
	faceIDs []int64,
) []recognitionDraft {
	drafts := make([]recognitionDraft, 0, len(plan.faces))

	isolatedIdx := 0
	for _, face := range plan.faces {
		var trackID int64
		var bodyBox [4]float64
		var personAttrs map[string]any

		if face.PersonIndex >= 0 && int(face.PersonIndex) < len(plan.persons) {
			trackID = trackIDAt(personIDs, int(face.PersonIndex))
			bodyBox = plan.persons[face.PersonIndex].BodyBox
			personAttrs = plan.persons[face.PersonIndex].Attributes
		} else {
			trackID = trackIDAt(faceIDs, isolatedIdx)
			isolatedIdx++
		}

		keyFace := aitypes.FaceSubItem{
			Box:           face.Box,
			Matched:       face.Matched,
			PersonID:      face.PersonID,
			MatchedFaceID: face.MatchedFaceID,
			Similarity:    face.Similarity,
		}
		if !s.cooldown.AllowAll(recognitionCooldownKeys(f.res.CameraID, trackID, keyFace), f.capturedAt) {
			continue
		}

		drafts = append(drafts, recognitionDraft{
			face:        face,
			trackID:     trackID,
			bodyBox:     bodyBox,
			personAttrs: personAttrs,
		})
	}

	return drafts
}

// draftFaceIDs 收集需要通过底库回查的人脸 ID。
func draftFaceIDs(drafts []recognitionDraft) []string {
	ids := make([]string, 0, len(drafts))
	for _, d := range drafts {
		if d.face.MatchedFaceID != "" {
			ids = append(ids, d.face.MatchedFaceID)
		}
	}
	return ids
}

// =========================================================================
// vehicle_structure
// =========================================================================

func (s *Service) processVehicleStructure(ctx context.Context, f ingestFrame) error {
	observations := make([]observation, len(f.res.Vehicles))
	for i, v := range f.res.Vehicles {
		observations[i] = observation{box: v.VehicleBox, score: v.VehicleScore}
		s.debugIgnoredTrackID(f.res.CameraID, v.TrackID)
	}

	identities := s.resolveIdentities(
		bucketKey{camera: f.res.CameraID, algorithm: f.res.AlgorithmID, kind: "vehicle"},
		observations,
		f.capturedAt,
	)

	captures, jobs := s.planVehicleCaptures(f, identities)
	if len(captures) == 0 {
		return nil
	}
	if err := s.store.CreateCaptureBatch(ctx, captures); err != nil {
		return fmt.Errorf("insert vehicle captures: %w", err)
	}
	dispatchCreated(s, ctx, captures, jobs, eventFromVehicleCapture)

	return nil
}

// planVehicleCaptures 决策本轮要落库的车辆抓拍。
func (s *Service) planVehicleCaptures(f ingestFrame, identities []int64) ([]*Capture, []snapshotJob) {
	captures := make([]*Capture, 0, len(f.res.Vehicles))
	jobs := make([]snapshotJob, 0, cap(captures))

	for i, v := range f.res.Vehicles {
		trackID := trackIDAt(identities, i)
		plateNo := firstPlateNumber(v)

		if !s.cooldown.AllowAll(vehicleCaptureCooldownKeys(f.res.CameraID, trackID, plateNo, v.VehicleBox), f.capturedAt) {
			continue
		}

		capture, job := buildCaptureRecordAndJob(f, captureSpec{
			targetType: "vehicle",
			trackID:    trackID,
			score:      v.VehicleScore,
			box:        v.VehicleBox,
			attrsJSON:  vehicleAttributesJSON(v, plateNo),
		})
		captures = append(captures, capture)
		jobs = append(jobs, job)
	}

	return captures, jobs
}

// =========================================================================
// detection / classification
// =========================================================================

func (s *Service) processDetection(ctx context.Context, f ingestFrame) error {
	alarms, alarmJobs := s.planDetectionAlarms(f)
	captures, captureJobs := s.planDetectionCaptures(f)

	var errs []error
	if len(alarms) > 0 {
		if err := s.store.CreateAlarmBatch(ctx, alarms); err != nil {
			errs = append(errs, fmt.Errorf("insert alarms: %w", err))
		} else {
			dispatchCreated(s, ctx, alarms, alarmJobs, eventFromAlarm)
		}
	}
	if len(captures) > 0 {
		if err := s.store.CreateCaptureBatch(ctx, captures); err != nil {
			errs = append(errs, fmt.Errorf("insert captures: %w", err))
		} else {
			s.enqueueSnapshots(ctx, captureJobs)
		}
	}

	return errors.Join(errs...)
}

// planDetectionAlarms 决策本轮要落库的告警行。
func (s *Service) planDetectionAlarms(f ingestFrame) ([]*Alarm, []snapshotJob) {
	alarms := make([]*Alarm, 0, len(f.res.Detections))
	jobs := make([]snapshotJob, 0, cap(alarms))

	for _, d := range f.res.Detections {
		if !isAlarmLabel(d.Label) {
			continue
		}
		if !s.cooldown.Allow(alarmCooldownKey(f.res.CameraID, d.Label), f.capturedAt) {
			continue
		}
		// 场景级告警（烟火）没有目标裁剪框，只出全幅图。
		var cropBox *[4]float64
		if !isSceneWideAlarm(d.Label) {
			box := d.Box
			cropBox = &box
		}
		alarm, job := buildAlarmRecordAndJob(f, alarmSpec{
			label:     d.Label,
			score:     d.Score,
			boxJSON:   marshalBox(d.Box),
			attrsJSON: marshalMapOrDefault(d.Attributes),
			cropBox:   cropBox,
		})
		alarms = append(alarms, alarm)
		jobs = append(jobs, job)
	}

	return alarms, jobs
}

// planDetectionCaptures 决策本轮要落库的普通目标抓拍。
//
// 按 label 分组是为了让同一标签的目标共享一个身份命名空间：跨标签的身份没有
// 可比性，混在一个 bucket 里会互相抢占匹配。
func (s *Service) planDetectionCaptures(f ingestFrame) ([]*Capture, []snapshotJob) {
	groups := make(map[string][]aitypes.DetectionItem, len(f.res.Detections))
	for _, d := range f.res.Detections {
		if !isAlarmLabel(d.Label) {
			groups[d.Label] = append(groups[d.Label], d)
		}
	}

	var captures []*Capture
	var jobs []snapshotJob
	for label, items := range groups {
		observations := make([]observation, len(items))
		for i, it := range items {
			observations[i] = observation{box: it.Box, score: it.Score}
		}

		identities := s.resolveIdentities(
			bucketKey{camera: f.res.CameraID, algorithm: f.res.AlgorithmID, kind: "detection:" + label},
			observations,
			f.capturedAt,
		)

		for i, it := range items {
			trackID := trackIDAt(identities, i)
			if !s.cooldown.Allow(detectionCaptureCooldownKey(f.res.CameraID, label, trackID), f.capturedAt) {
				continue
			}
			capture, job := buildCaptureRecordAndJob(f, captureSpec{
				targetType: it.Label,
				trackID:    trackID,
				score:      it.Score,
				box:        it.Box,
				attrsJSON:  marshalMapOrDefault(it.Attributes),
			})
			captures = append(captures, capture)
			jobs = append(jobs, job)
		}
	}

	return captures, jobs
}

func (s *Service) processClassification(ctx context.Context, f ingestFrame) error {
	alarms := make([]*Alarm, 0, len(f.res.Classifications))
	jobs := make([]snapshotJob, 0, cap(alarms))

	for _, c := range f.res.Classifications {
		if !isAlarmLabel(c.Label) {
			continue
		}
		if !s.cooldown.Allow(alarmCooldownKey(f.res.CameraID, c.Label), f.capturedAt) {
			continue
		}
		// 分类结果没有检测框，只能出全幅图；BoxJSON 保持与列默认值一致的 "[]"。
		alarm, job := buildAlarmRecordAndJob(f, alarmSpec{
			label:     c.Label,
			score:     c.Score,
			boxJSON:   "[]",
			attrsJSON: "{}",
		})
		alarms = append(alarms, alarm)
		jobs = append(jobs, job)
	}

	if len(alarms) == 0 {
		return nil
	}
	if err := s.store.CreateAlarmBatch(ctx, alarms); err != nil {
		return fmt.Errorf("insert classification alarms: %w", err)
	}
	dispatchCreated(s, ctx, alarms, jobs, eventFromAlarm)

	return nil
}

// =========================================================================
// 事件分发
// =========================================================================

// eventFromAlarm 把告警行投影成对外事件。图片可用性由快照回调补齐。
func eventFromAlarm(alarm *Alarm) AlarmEvent {
	extra := unmarshalMapOrDefault(alarm.ExtraJSON)
	box := unmarshalBox(alarm.BoxJSON)

	return AlarmEvent{
		ID:          alarm.ID,
		EventType:   EventTypeAlarm,
		CameraID:    alarm.CameraID,
		AlgorithmID: derefString(alarm.AlgorithmID),
		CapturedAt:  alarm.CapturedAt,
		ImagePath:   alarm.ImagePath,
		CropPath:    alarm.CropPath,
		Alarm: &AlarmEventData{
			AlarmType: alarm.AlarmType,
			Score:     alarm.Score,
			Box:       box,
			Extra:     extra,
		},
		// 兼容 alertpush 仍在读取的扁平字段。
		AlarmType: alarm.AlarmType,
		Score:     alarm.Score,
		Box:       box,
		Extra:     extra,
	}
}

func eventFromRecognition(rec *Recognition) AlarmEvent {
	return AlarmEvent{
		ID:          rec.ID,
		EventType:   EventTypeFaceRecognition,
		CameraID:    rec.CameraID,
		AlgorithmID: derefString(rec.AlgorithmID),
		CapturedAt:  rec.CapturedAt,
		ImagePath:   rec.ImagePath,
		CropPath:    rec.FaceImagePath,
		Face: &FaceEventData{
			Matched:    rec.Matched,
			PersonName: rec.PersonName,
			// 对外只暴露业务键：内部 persons.id 是代理主键，跨系统无意义。
			PersonID:         rec.PersonExternalID,
			Similarity:       rec.Similarity,
			Box:              unmarshalBox(rec.BoxJSON),
			GalleryImagePath: rec.GalleryImagePath,
			Attributes:       unmarshalMapOrDefault(rec.AttributesJSON),
		},
	}
}

func eventFromVehicleCapture(cap *Capture) AlarmEvent {
	attrs := unmarshalMapOrDefault(cap.AttributesJSON)

	return AlarmEvent{
		ID:          cap.ID,
		EventType:   EventTypePlateRecognition,
		CameraID:    cap.CameraID,
		AlgorithmID: derefString(cap.AlgorithmID),
		CapturedAt:  cap.CapturedAt,
		ImagePath:   cap.ImagePath,
		CropPath:    cap.CropPath,
		Vehicle: &VehicleEventData{
			PlateNumber:  stringFromMap(attrs, "plate_number"),
			PlateColor:   stringFromMap(attrs, "plate_color"),
			VehicleType:  stringFromMap(attrs, "vehicle_type"),
			VehicleColor: stringFromMap(attrs, "vehicle_color"),
			Score:        cap.Score,
			Box:          unmarshalBox(cap.BoxJSON),
			Attributes:   attrs,
		},
	}
}

// dispatchCreated 把已落库的记录投影成事件并交给监听器。
//
// 三种记录类型（alarm / recognition / vehicle capture）共用同一套骨架：
// 建 targetID→job 索引 → 逐条投影 → 关联快照或直接入队。列表器缺失时退回
// 「只入队、不发事件」，与 record 域未接入 alertpush 时的行为一致。
func dispatchCreated[T any](
	s *Service,
	ctx context.Context,
	records []T,
	jobs []snapshotJob,
	toEvent func(T) AlarmEvent,
) {
	listener := s.eventListener()
	if listener == nil {
		s.enqueueSnapshots(ctx, jobs)
		return
	}

	sink := s.snapshotSink()
	jobMap := make(map[string]snapshotJob, len(jobs))
	for _, job := range jobs {
		jobMap[job.targetID] = job
	}

	for _, rec := range records {
		ev := toEvent(rec)
		job, hasJob := jobMap[ev.ID]
		s.dispatchOrEnqueueEvent(ctx, listener, sink, pendingEvent{event: ev, job: job, hasJob: hasJob})
	}
}

// pendingEvent 是一条待分发事件及其抓拍任务。
//
// hasJob 为 false 表示该记录没有对应任务（队列里没排上 / 没生成），此时必须
// 立即分发并清空路径，而不是等一个永远不会来的回调。
type pendingEvent struct {
	event  AlarmEvent
	job    snapshotJob
	hasJob bool
}

// dispatchOrEnqueueEvent 决定一条事件是「等图再发」还是「现在就发」。
//
// 只有「抓拍可用 + 有对应任务 + 未超在飞上限」才登记挂起，其余全部降级为
// 立即分发（图片字段清空），保证事件不会因为图慢而消失。
func (s *Service) dispatchOrEnqueueEvent(
	ctx context.Context,
	listener AlarmListener,
	sink *SnapshotStore,
	ev pendingEvent,
) {
	if sink == nil || !sink.Available() || !ev.hasJob {
		if ev.hasJob {
			s.clearJobPath(ctx, ev.job)
		}
		listener.OnAlarm(ctx, ev.event.withoutEvidence())
		return
	}

	s.pendingMu.Lock()
	if !s.accepting || len(s.pendingAlarms) >= maxPendingAlarms {
		s.pendingMu.Unlock()
		sink.Enqueue(ev.job)
		listener.OnAlarm(ctx, ev.event.withoutEvidence())
		return
	}
	s.pendingAlarms[ev.event.ID] = &pendingAlarmEntry{alarm: ev.event, createdAt: time.Now()}
	s.pendingMu.Unlock()

	if sink.Enqueue(ev.job) {
		return
	}

	s.pendingMu.Lock()
	delete(s.pendingAlarms, ev.event.ID)
	s.pendingMu.Unlock()

	s.clearJobPath(ctx, ev.job)
	listener.OnAlarm(ctx, ev.event.withoutEvidence())
}

// withoutEvidence 返回「图片未就绪」的事件副本。
// 三条降级路径（无抓拍组件 / 队列拒绝 / 队列满）必须产出完全一致的事件。
func (ev AlarmEvent) withoutEvidence() AlarmEvent {
	ev.ImagePath = ""
	ev.CropPath = ""
	ev.ImageReady = false
	ev.CropReady = false
	return ev
}

func stringFromMap(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// debugIgnoredTrackID 记录「载荷里的 track_id 被宿主忽略」。
//
// 用 Check 而不是直接 Debug：这条日志在每帧每个目标上触发，即使等级被关闭，
// zap 也会先把字段结构体构造出来，在边缘设备上是白白的分配。
func (s *Service) debugIgnoredTrackID(cameraID string, payloadTrackID int64) {
	if payloadTrackID == 0 {
		return
	}
	ce := s.logger.Check(zapcore.DebugLevel, "payload track_id ignored; host maintains exclusive tracking identities")
	if ce == nil {
		return
	}
	ce.Write(zap.String("camera_id", cameraID), zap.Int64("payload_track_id", payloadTrackID))
}

// enqueueSnapshots 把成功落库记录对应的抓拍任务登记到后台队列。
//
// 写库失败的任务不得落盘，否则会产生没有数据库引用的孤儿图片；
// 若抓拍队列不可用或丢弃任务，则清理已预填的虚假图片路径，避免死链。
func (s *Service) enqueueSnapshots(ctx context.Context, jobs []snapshotJob) {
	sink := s.snapshotSink()
	if sink == nil {
		return
	}
	for _, job := range jobs {
		if !sink.Enqueue(job) {
			s.clearJobPath(ctx, job)
		}
	}
}

// clearJobPath 尽力清空一条记录里预填的快照路径。失败只记 Warn：
// 清理是尽力而为，调用方（mediafile）必须容忍路径指向缺失文件。
func (s *Service) clearJobPath(ctx context.Context, job snapshotJob) {
	if s.store == nil || job.targetTable == "" || job.targetID == "" {
		return
	}
	if err := s.store.ClearRecordSnapshotPaths(ctx, job.targetTable, job.targetID); err != nil {
		s.logger.Warn("failed to clear record snapshot paths",
			zap.String("table", job.targetTable),
			zap.String("id", job.targetID),
			zap.Error(err),
		)
	}
}

// resolveGalleryFaces 把命中的人脸 ID 解析为本地人员身份（内部行 ID + 业务键快照）。
//
// 解析失败只降级不中断：记录本身是已发生事实，归属标注的完整性是次级信息。
func (s *Service) resolveGalleryFaces(ctx context.Context, faceIDs []string) map[string]GalleryFaceRef {
	if s.store == nil || len(faceIDs) == 0 {
		return nil
	}
	refs, err := s.store.ResolveGalleryFaces(ctx, faceIDs)
	if err != nil {
		s.logger.Error("failed to resolve gallery faces", zap.Int("faces", len(faceIDs)), zap.Error(err))
		return nil
	}
	return refs
}

// 标签判定表：定长数组而不是可变 slice，包级状态不会被 append 悄悄改写。
var (
	// nonAlarmSubstrings 是消防设备的反向关键词：它们是正常设备，不是告警。
	nonAlarmSubstrings = [...]string{"hydrant", "extinguisher"}
	// nonAlarmExactLabels 是正常佩戴安全帽的检测标签；只有违规（no_helmet）才告警。
	nonAlarmExactLabels = [...]string{"helmet", "safety_helmet", "wearing_helmet"}
	// alarmKeywords 是告警动作关键词。
	alarmKeywords = [...]string{
		"alarm",
		"no_helmet",
		"without_helmet",
		"smoke",
		"fire",
		"flame",
		"intrusion",
		"fall",
		"danger",
	}
	// sceneWideAlarmKeywords 是无目标裁剪框、只能出全幅图的场景级告警。
	sceneWideAlarmKeywords = [...]string{"smoke", "fire", "flame"}
)

func isAlarmLabel(label string) bool {
	l := strings.ToLower(label)
	if matchesNonAlarmMarker(l) || slices.Contains(nonAlarmExactLabels[:], l) {
		return false
	}
	return slices.ContainsFunc(alarmKeywords[:], func(kw string) bool {
		return strings.Contains(l, kw)
	})
}

func isSceneWideAlarm(label string) bool {
	l := strings.ToLower(label)
	if matchesNonAlarmMarker(l) {
		return false
	}
	return slices.ContainsFunc(sceneWideAlarmKeywords[:], func(kw string) bool {
		return strings.Contains(l, kw)
	})
}

// matchesNonAlarmMarker 报告已小写的标签是否命中「正常设备」反向关键词。
func matchesNonAlarmMarker(l string) bool {
	return slices.ContainsFunc(nonAlarmSubstrings[:], func(sub string) bool {
		return strings.Contains(l, sub)
	})
}

// =========================================================================
// Query APIs
// =========================================================================

// ListRecognitions returns filtered and paginated RecognitionResponse items.
func (s *Service) ListRecognitions(
	ctx context.Context,
	filter RecognitionFilter,
) ([]RecognitionResponse, int64, error) {
	items, total, err := s.store.ListRecognitions(ctx, filter)
	if err != nil {
		return nil, 0, apperr.Internal("INTERNAL_ERROR", "failed to list recognitions", err)
	}
	responses := make([]RecognitionResponse, len(items))
	for i := range items {
		responses[i] = items[i].ToResponse()
	}
	return responses, total, nil
}

// GetRecognition returns a single RecognitionResponse by ID.
func (s *Service) GetRecognition(ctx context.Context, id string) (*RecognitionResponse, error) {
	item, err := s.store.GetRecognitionByID(ctx, id)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to get recognition", err)
	}
	if item == nil {
		return nil, apperr.NotFound("RECOGNITION_NOT_FOUND", "recognition not found", nil)
	}
	resp := item.ToResponse()
	return &resp, nil
}

// ListAlarms returns filtered and paginated AlarmResponse items.
func (s *Service) ListAlarms(ctx context.Context, filter AlarmFilter) ([]AlarmResponse, int64, error) {
	items, total, err := s.store.ListAlarms(ctx, filter)
	if err != nil {
		return nil, 0, apperr.Internal("INTERNAL_ERROR", "failed to list alarms", err)
	}
	responses := make([]AlarmResponse, len(items))
	for i := range items {
		responses[i] = items[i].ToResponse()
	}
	return responses, total, nil
}

// GetAlarm returns a single AlarmResponse by ID.
func (s *Service) GetAlarm(ctx context.Context, id string) (*AlarmResponse, error) {
	item, err := s.store.GetAlarmByID(ctx, id)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to get alarm", err)
	}
	if item == nil {
		return nil, apperr.NotFound("ALARM_NOT_FOUND", "alarm not found", nil)
	}
	resp := item.ToResponse()
	return &resp, nil
}

// ListCaptures returns filtered and paginated CaptureResponse items.
func (s *Service) ListCaptures(ctx context.Context, filter CaptureFilter) ([]CaptureResponse, int64, error) {
	items, total, err := s.store.ListCaptures(ctx, filter)
	if err != nil {
		return nil, 0, apperr.Internal("INTERNAL_ERROR", "failed to list captures", err)
	}
	responses := make([]CaptureResponse, len(items))
	for i := range items {
		responses[i] = items[i].ToResponse()
	}
	return responses, total, nil
}

// GetCapture returns a single CaptureResponse by ID.
func (s *Service) GetCapture(ctx context.Context, id string) (*CaptureResponse, error) {
	item, err := s.store.GetCaptureByID(ctx, id)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to get capture", err)
	}
	if item == nil {
		return nil, apperr.NotFound("CAPTURE_NOT_FOUND", "capture not found", nil)
	}
	resp := item.ToResponse()
	return &resp, nil
}

// ListAlarmTypes returns all unique alarm types present in the record store.
func (s *Service) ListAlarmTypes(ctx context.Context) ([]string, error) {
	types, err := s.store.ListDistinctAlarmTypes(ctx)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to list alarm types", err)
	}
	return types, nil
}

// ListTargetTypes returns all unique capture target types present in the record store.
func (s *Service) ListTargetTypes(ctx context.Context) ([]string, error) {
	types, err := s.store.ListDistinctTargetTypes(ctx)
	if err != nil {
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to list target types", err)
	}
	return types, nil
}
