package record

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/engine"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

// Service coordinates perception records, alarms, cooldown deduplication, and persistence.
type Service struct {
	store      *Store
	cooldown   *CooldownManager
	snapshots  *SnapshotStore
	algorithms *AlgorithmResolver
	logger     *zap.Logger
}

// NewService creates a new record Service.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:      store,
		cooldown:   NewCooldownManager(DefaultCooldown),
		algorithms: NewAlgorithmResolver(store),
		logger:     logger,
	}
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
	s.snapshots = store
}

// ProcessInferenceResult consumes an inference result from StreamHub, applies
// tracking cooldowns, resolves enrolled face gallery images, and persists records.
func (s *Service) ProcessInferenceResult(ctx context.Context, res aitypes.InferenceResult) error {
	if res.CameraID == "" {
		return nil
	}

	capturedAt := res.Timestamp.UTC()
	if res.Timestamp.IsZero() {
		capturedAt = time.Now().UTC()
	}
	dateStr := capturedAt.Format("2006-01-02")

	// 解析算法身份快照（幂等、带短 TTL 缓存）。一次结果的所有记录共用同一个
	// 算法 ID，因此只查一次库。
	algo := s.resolveAlgorithm(ctx, res.AlgorithmID)

	switch res.TaskType {
	case aitypes.TaskTypePersonStructure:
		return s.processPersonStructure(ctx, res, algo, capturedAt, dateStr)
	case aitypes.TaskTypeDetection:
		return s.processDetection(ctx, res, algo, capturedAt, dateStr)
	case aitypes.TaskTypeClassification:
		return s.processClassification(ctx, res, algo, capturedAt, dateStr)
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
	resolved := s.algorithms.resolveAlgorithmSnapshots(ctx, []string{algorithmID})
	return resolved[algorithmID]
}

func (s *Service) processPersonStructure(ctx context.Context, res aitypes.InferenceResult, algo AlgorithmSnapshot, capturedAt time.Time, dateStr string) error {
	var recognitions []*Recognition
	var captures []*Capture

	for _, p := range res.Persons {
		// 1. Process Face Recognition if faces exist
		if len(p.Faces) > 0 {
			for _, face := range p.Faces {
				// Cooldown key for recognition
				var recKey string
				if p.TrackID > 0 {
					recKey = fmt.Sprintf("rec:%s:%d", res.CameraID, p.TrackID)
				} else if face.PersonID != "" {
					recKey = fmt.Sprintf("rec:%s:%s", res.CameraID, face.PersonID)
				} else {
					recKey = fmt.Sprintf("rec:%s:unmatched:%.3f:%.3f", res.CameraID, face.Box[0], face.Box[1])
				}

				if s.cooldown.Allow(recKey, capturedAt) {
					recID := idutil.New()

					// Resolve gallery image path if matched
					var galleryImagePath string
					var personIDPtr *string
					if face.Matched && face.MatchedFaceID != "" {
						galleryImagePath = s.resolveGalleryImagePath(ctx, face.MatchedFaceID)
					}
					if face.PersonID != "" {
						pid := face.PersonID
						personIDPtr = &pid
					}

					candidatesJSON := marshalSliceOrDefault(face.Candidates)
					boxBytes, _ := json.Marshal(face.Box)
					bodyBoxBytes, _ := json.Marshal(p.BodyBox)
					attrsJSON := marshalMapOrDefault(p.Attributes)

					// 两条证据图：全幅（imagePath）+ 人脸裁剪（faceImagePath）。
					// 两者在一次抓拍里产出，避免为同一帧编码两次。
					fullPath := fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, recID)
					facePath := fmt.Sprintf("snapshots/%s/%s/%s_face.jpg", res.CameraID, dateStr, recID)

					rec := &Recognition{
						ID:               recID,
						CameraID:         res.CameraID,
						AlgorithmID:      optionalAlgorithmID(res.AlgorithmID),
						AlgorithmName:    algo.Name,
						AlgorithmVersion: algo.Version,
						PTS:              res.PTS,
						TrackID:          p.TrackID,
						PersonID:         personIDPtr,
						PersonName:       face.PersonName,
						MatchedFaceID:    face.MatchedFaceID,
						Matched:          face.Matched,
						Similarity:       face.Similarity,
						CandidatesJSON:   candidatesJSON,
						FaceImagePath:    facePath,
						GalleryImagePath: galleryImagePath,
						ImagePath:        fullPath,
						BoxJSON:          string(boxBytes),
						BodyBoxJSON:      string(bodyBoxBytes),
						AttributesJSON:   attrsJSON,
						CapturedAt:       capturedAt,
						CreatedAt:        time.Now().UTC(),
					}
					recognitions = append(recognitions, rec)

					s.enqueueSnapshot(res.CameraID, capturedAt, []engine.SnapshotBox{
						{face.Box[0], face.Box[1], face.Box[2], face.Box[3]},
					}, []snapshotOutput{
						{boxIndex: -1, relPath: fullPath},
						{boxIndex: 0, relPath: facePath},
					})
				}
			}
		}

		// 2. Process Capture for Person
		capKey := fmt.Sprintf("cap:%s:person:%d", res.CameraID, p.TrackID)
		if s.cooldown.Allow(capKey, capturedAt) {
			capID := idutil.New()
			bodyBoxBytes, _ := json.Marshal(p.BodyBox)
			attrsJSON := marshalMapOrDefault(p.Attributes)

			fullPath := fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, capID)
			cropPath := fmt.Sprintf("snapshots/%s/%s/%s_crop.jpg", res.CameraID, dateStr, capID)

			cap := &Capture{
				ID:               capID,
				CameraID:         res.CameraID,
				AlgorithmID:      optionalAlgorithmID(res.AlgorithmID),
				AlgorithmName:    algo.Name,
				AlgorithmVersion: algo.Version,
				TargetType:       "person",
				TrackID:          p.TrackID,
				Score:            p.BodyScore,
				BoxJSON:          string(bodyBoxBytes),
				CropPath:         cropPath,
				ImagePath:        fullPath,
				AttributesJSON:   attrsJSON,
				PTS:              res.PTS,
				CapturedAt:       capturedAt,
				CreatedAt:        time.Now().UTC(),
			}
			captures = append(captures, cap)

			s.enqueueSnapshot(res.CameraID, capturedAt, []engine.SnapshotBox{
				{p.BodyBox[0], p.BodyBox[1], p.BodyBox[2], p.BodyBox[3]},
			}, []snapshotOutput{
				{boxIndex: -1, relPath: fullPath},
				{boxIndex: 0, relPath: cropPath},
			})
		}
	}

	if len(recognitions) > 0 {
		if err := s.store.CreateRecognitionBatch(ctx, recognitions); err != nil {
			s.logger.Error("failed to batch insert recognitions", zap.Error(err))
		}
	}
	if len(captures) > 0 {
		if err := s.store.CreateCaptureBatch(ctx, captures); err != nil {
			s.logger.Error("failed to batch insert captures", zap.Error(err))
		}
	}

	return nil
}

func (s *Service) processDetection(ctx context.Context, res aitypes.InferenceResult, algo AlgorithmSnapshot, capturedAt time.Time, dateStr string) error {
	var alarms []*Alarm
	var captures []*Capture

	for _, d := range res.Detections {
		// Detect whether this is an alarm category (e.g. helmet, smoke, fire, intrusion, etc.)
		isAlarm := isAlarmLabel(d.Label)
		if isAlarm {
			alarmKey := fmt.Sprintf("alarm:%s:%s", res.CameraID, d.Label)
			if s.cooldown.Allow(alarmKey, capturedAt) {
				alarmID := idutil.New()
				boxBytes, _ := json.Marshal(d.Box)
				attrsJSON := marshalMapOrDefault(d.Attributes)

				var cropPath string
				// Scene-wide alarms like smoke or fire do not have small target crops
				if !isSceneWideAlarm(d.Label) {
					cropPath = fmt.Sprintf("snapshots/%s/%s/%s_crop.jpg", res.CameraID, dateStr, alarmID)
				}

				fullPath := fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, alarmID)

				alarm := &Alarm{
					ID:               alarmID,
					CameraID:         res.CameraID,
					AlgorithmID:      optionalAlgorithmID(res.AlgorithmID),
					AlgorithmName:    algo.Name,
					AlgorithmVersion: algo.Version,
					AlarmType:        d.Label,
					Score:            d.Score,
					BoxJSON:          string(boxBytes),
					ImagePath:        fullPath,
					CropPath:         cropPath,
					ExtraJSON:        attrsJSON,
					PTS:              res.PTS,
					CapturedAt:       capturedAt,
					CreatedAt:        time.Now().UTC(),
				}
				alarms = append(alarms, alarm)

				// 场景级告警（烟火）没有目标裁剪框，只出全幅图。
				outs := []snapshotOutput{{boxIndex: -1, relPath: fullPath}}
				boxList := []engine.SnapshotBox{}
				if cropPath != "" {
					boxList = append(boxList, engine.SnapshotBox{d.Box[0], d.Box[1], d.Box[2], d.Box[3]})
					outs = append(outs, snapshotOutput{boxIndex: 0, relPath: cropPath})
				}
				s.enqueueSnapshot(res.CameraID, capturedAt, boxList, outs)
			}
		} else {
			// Regular object capture
			capKey := fmt.Sprintf("cap:%s:%s", res.CameraID, d.Label)
			if s.cooldown.Allow(capKey, capturedAt) {
				capID := idutil.New()
				boxBytes, _ := json.Marshal(d.Box)
				attrsJSON := marshalMapOrDefault(d.Attributes)

				fullPath := fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, capID)
				cropPath := fmt.Sprintf("snapshots/%s/%s/%s_crop.jpg", res.CameraID, dateStr, capID)

				cap := &Capture{
					ID:               capID,
					CameraID:         res.CameraID,
					AlgorithmID:      optionalAlgorithmID(res.AlgorithmID),
					AlgorithmName:    algo.Name,
					AlgorithmVersion: algo.Version,
					TargetType:       d.Label,
					TrackID:          0,
					Score:            d.Score,
					BoxJSON:          string(boxBytes),
					CropPath:         cropPath,
					ImagePath:        fullPath,
					AttributesJSON:   attrsJSON,
					PTS:              res.PTS,
					CapturedAt:       capturedAt,
					CreatedAt:        time.Now().UTC(),
				}
				captures = append(captures, cap)

				s.enqueueSnapshot(res.CameraID, capturedAt, []engine.SnapshotBox{
					{d.Box[0], d.Box[1], d.Box[2], d.Box[3]},
				}, []snapshotOutput{
					{boxIndex: -1, relPath: fullPath},
					{boxIndex: 0, relPath: cropPath},
				})
			}
		}
	}

	if len(alarms) > 0 {
		if err := s.store.CreateAlarmBatch(ctx, alarms); err != nil {
			s.logger.Error("failed to batch insert alarms", zap.Error(err))
		}
	}
	if len(captures) > 0 {
		if err := s.store.CreateCaptureBatch(ctx, captures); err != nil {
			s.logger.Error("failed to batch insert captures", zap.Error(err))
		}
	}

	return nil
}

func (s *Service) processClassification(ctx context.Context, res aitypes.InferenceResult, algo AlgorithmSnapshot, capturedAt time.Time, dateStr string) error {
	var alarms []*Alarm
	for _, c := range res.Classifications {
		if isAlarmLabel(c.Label) {
			alarmKey := fmt.Sprintf("alarm:%s:%s", res.CameraID, c.Label)
			if s.cooldown.Allow(alarmKey, capturedAt) {
				alarmID := idutil.New()
				fullPath := fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, alarmID)

				alarm := &Alarm{
					ID:               alarmID,
					CameraID:         res.CameraID,
					AlgorithmID:      optionalAlgorithmID(res.AlgorithmID),
					AlgorithmName:    algo.Name,
					AlgorithmVersion: algo.Version,
					AlarmType:        c.Label,
					Score:            c.Score,
					BoxJSON:          "[]",
					ImagePath:        fullPath,
					CropPath:         "",
					ExtraJSON:        "{}",
					PTS:              res.PTS,
					CapturedAt:       capturedAt,
					CreatedAt:        time.Now().UTC(),
				}
				alarms = append(alarms, alarm)

				// 分类结果没有检测框，只能出全幅图。
				s.enqueueSnapshot(res.CameraID, capturedAt, nil,
					[]snapshotOutput{{boxIndex: -1, relPath: fullPath}})
			}
		}
	}
	if len(alarms) > 0 {
		if err := s.store.CreateAlarmBatch(ctx, alarms); err != nil {
			s.logger.Error("failed to batch insert classification alarms", zap.Error(err))
		}
	}
	return nil
}

// optionalAlgorithmID 把推理结果里的算法标识转换成可空列值。
//
// ⚠️ 空串必须转成 nil：recognitions.algorithm_id / alarms.algorithm_id 上是
// REFERENCES algorithms(id)，SQLite 对空串同样要求父表存在匹配行，只有 NULL 才
// 跳过外键校验。直接写空串会让整条记录 insert 失败，而 service 只记日志不返回
// 错误 —— 表现就是「记录莫名其妙消失」。
//
// 空串的来源是历史设计：AI 流按相机共享，Hub 无法归属算法，若 runner 未在载荷里
// 打戳就只剩空串。
func optionalAlgorithmID(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}

// enqueueSnapshot 把一次抓拍写盘任务登记到后台队列。
//
// ⚠️ 这里是「非阻塞登记」而不是「同步抓拍」：JPEG 编码在 RK3568 上 35~60ms/帧，
// 若在本 goroutine 里执行，consumeAIResults 会被磁盘与编码拖住，进而让 AI 结果
// 订阅队列丢帧。因此只登记，由 SnapshotStore 的 worker 实际执行。
//
// 未注入 SnapshotStore 或队列已满时静默跳过：路径字段已在记录里生成，前端拿不到
// 文件时显示占位图，不影响记录本身的可检索性。
func (s *Service) enqueueSnapshot(
	cameraID string,
	capturedAt time.Time,
	boxes []engine.SnapshotBox,
	outputs []snapshotOutput,
) {
	if s.snapshots == nil {
		return
	}
	s.snapshots.Enqueue(snapshotJob{
		cameraID: cameraID,
		boxes:    boxes,
		outputs:  outputs,
	})
}

// resolveGalleryImagePath queries person_faces via Store for the image_path associated with the matched face ID.
func (s *Service) resolveGalleryImagePath(ctx context.Context, faceID string) string {
	if s.store == nil || faceID == "" {
		return ""
	}
	path, err := s.store.GetFaceImagePath(ctx, faceID)
	if err != nil {
		s.logger.Debug("failed to resolve gallery image path", zap.String("face_id", faceID), zap.Error(err))
		return ""
	}
	return path
}

var nonAlarmSubstrings = []string{
	"hydrant",
	"extinguisher",
}

func isAlarmLabel(label string) bool {
	l := strings.ToLower(label)
	for _, sub := range nonAlarmSubstrings {
		if strings.Contains(l, sub) {
			return false
		}
	}
	// Normal helmet detection should not trigger an alarm; only violations (e.g. no_helmet) or explicit alarms
	if l == "helmet" || l == "safety_helmet" || l == "wearing_helmet" {
		return false
	}
	return strings.Contains(l, "alarm") ||
		strings.Contains(l, "no_helmet") ||
		strings.Contains(l, "without_helmet") ||
		strings.Contains(l, "smoke") ||
		strings.Contains(l, "fire") ||
		strings.Contains(l, "flame") ||
		strings.Contains(l, "intrusion") ||
		strings.Contains(l, "fall") ||
		strings.Contains(l, "danger")
}

func isSceneWideAlarm(label string) bool {
	l := strings.ToLower(label)
	for _, sub := range nonAlarmSubstrings {
		if strings.Contains(l, sub) {
			return false
		}
	}
	return strings.Contains(l, "smoke") || strings.Contains(l, "fire") || strings.Contains(l, "flame")
}

func marshalSliceOrDefault[T any](items []T) string {
	if len(items) == 0 {
		return "[]"
	}
	b, err := json.Marshal(items)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func marshalMapOrDefault(m map[string]any) string {
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// =========================================================================
// Query APIs
// =========================================================================

// ListRecognitions returns filtered and paginated RecognitionResponse items.
func (s *Service) ListRecognitions(ctx context.Context, filter RecognitionFilter) ([]RecognitionResponse, int64, error) {
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
