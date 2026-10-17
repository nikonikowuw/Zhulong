package record

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

// Service coordinates perception records, alarms, cooldown deduplication, and persistence.
type Service struct {
	store    *Store
	cooldown *CooldownManager
	logger   *zap.Logger
}

// NewService creates a new record Service.
func NewService(store *Store, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:    store,
		cooldown: NewCooldownManager(DefaultCooldown),
		logger:   logger,
	}
}

// SetCooldownWindow sets the deduplication window for tracking cooldowns.
func (s *Service) SetCooldownWindow(d time.Duration) {
	s.cooldown.SetCooldown(d)
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

	switch res.TaskType {
	case aitypes.TaskTypePersonStructure:
		return s.processPersonStructure(ctx, res, capturedAt, dateStr)
	case aitypes.TaskTypeDetection:
		return s.processDetection(ctx, res, capturedAt, dateStr)
	case aitypes.TaskTypeClassification:
		return s.processClassification(ctx, res, capturedAt, dateStr)
	default:
		return nil
	}
}

func (s *Service) processPersonStructure(ctx context.Context, res aitypes.InferenceResult, capturedAt time.Time, dateStr string) error {
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

					rec := &Recognition{
						ID:               recID,
						CameraID:         res.CameraID,
						AlgorithmID:      res.AlgorithmID,
						PTS:              res.PTS,
						TrackID:          p.TrackID,
						PersonID:         personIDPtr,
						PersonName:       face.PersonName,
						MatchedFaceID:    face.MatchedFaceID,
						Matched:          face.Matched,
						Similarity:       face.Similarity,
						CandidatesJSON:   candidatesJSON,
						FaceImagePath:    fmt.Sprintf("snapshots/%s/%s/%s_face.jpg", res.CameraID, dateStr, recID),
						GalleryImagePath: galleryImagePath,
						ImagePath:        fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, recID),
						BoxJSON:          string(boxBytes),
						BodyBoxJSON:      string(bodyBoxBytes),
						AttributesJSON:   attrsJSON,
						CapturedAt:       capturedAt,
						CreatedAt:        time.Now().UTC(),
					}
					recognitions = append(recognitions, rec)
				}
			}
		}

		// 2. Process Capture for Person
		capKey := fmt.Sprintf("cap:%s:person:%d", res.CameraID, p.TrackID)
		if s.cooldown.Allow(capKey, capturedAt) {
			capID := idutil.New()
			bodyBoxBytes, _ := json.Marshal(p.BodyBox)
			attrsJSON := marshalMapOrDefault(p.Attributes)

			cap := &Capture{
				ID:             capID,
				CameraID:       res.CameraID,
				TargetType:     "person",
				TrackID:        p.TrackID,
				Score:          p.BodyScore,
				BoxJSON:        string(bodyBoxBytes),
				CropPath:       fmt.Sprintf("snapshots/%s/%s/%s_crop.jpg", res.CameraID, dateStr, capID),
				ImagePath:      fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, capID),
				AttributesJSON: attrsJSON,
				PTS:            res.PTS,
				CapturedAt:     capturedAt,
				CreatedAt:      time.Now().UTC(),
			}
			captures = append(captures, cap)
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

func (s *Service) processDetection(ctx context.Context, res aitypes.InferenceResult, capturedAt time.Time, dateStr string) error {
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

				alarm := &Alarm{
					ID:          alarmID,
					CameraID:    res.CameraID,
					AlgorithmID: res.AlgorithmID,
					AlarmType:   d.Label,
					Score:       d.Score,
					BoxJSON:     string(boxBytes),
					ImagePath:   fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, alarmID),
					CropPath:    cropPath,
					ExtraJSON:   attrsJSON,
					PTS:         res.PTS,
					CapturedAt:  capturedAt,
					CreatedAt:   time.Now().UTC(),
				}
				alarms = append(alarms, alarm)
			}
		} else {
			// Regular object capture
			capKey := fmt.Sprintf("cap:%s:%s", res.CameraID, d.Label)
			if s.cooldown.Allow(capKey, capturedAt) {
				capID := idutil.New()
				boxBytes, _ := json.Marshal(d.Box)
				attrsJSON := marshalMapOrDefault(d.Attributes)

				cap := &Capture{
					ID:             capID,
					CameraID:       res.CameraID,
					TargetType:     d.Label,
					TrackID:        0,
					Score:          d.Score,
					BoxJSON:        string(boxBytes),
					CropPath:       fmt.Sprintf("snapshots/%s/%s/%s_crop.jpg", res.CameraID, dateStr, capID),
					ImagePath:      fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, capID),
					AttributesJSON: attrsJSON,
					PTS:            res.PTS,
					CapturedAt:     capturedAt,
					CreatedAt:      time.Now().UTC(),
				}
				captures = append(captures, cap)
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

func (s *Service) processClassification(ctx context.Context, res aitypes.InferenceResult, capturedAt time.Time, dateStr string) error {
	var alarms []*Alarm
	for _, c := range res.Classifications {
		if isAlarmLabel(c.Label) {
			alarmKey := fmt.Sprintf("alarm:%s:%s", res.CameraID, c.Label)
			if s.cooldown.Allow(alarmKey, capturedAt) {
				alarmID := idutil.New()
				alarm := &Alarm{
					ID:          alarmID,
					CameraID:    res.CameraID,
					AlgorithmID: res.AlgorithmID,
					AlarmType:   c.Label,
					Score:       c.Score,
					BoxJSON:     "[]",
					ImagePath:   fmt.Sprintf("snapshots/%s/%s/%s.jpg", res.CameraID, dateStr, alarmID),
					CropPath:    "",
					ExtraJSON:   "{}",
					PTS:         res.PTS,
					CapturedAt:  capturedAt,
					CreatedAt:   time.Now().UTC(),
				}
				alarms = append(alarms, alarm)
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
		s.logger.Error("failed to list recognitions", zap.Error(err))
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
		s.logger.Error("failed to get recognition", zap.String("id", id), zap.Error(err))
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
		s.logger.Error("failed to list alarms", zap.Error(err))
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
		s.logger.Error("failed to get alarm", zap.String("id", id), zap.Error(err))
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
		s.logger.Error("failed to list captures", zap.Error(err))
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
		s.logger.Error("failed to get capture", zap.String("id", id), zap.Error(err))
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
		s.logger.Error("failed to list alarm types", zap.Error(err))
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to list alarm types", err)
	}
	return types, nil
}

// ListTargetTypes returns all unique capture target types present in the record store.
func (s *Service) ListTargetTypes(ctx context.Context) ([]string, error) {
	types, err := s.store.ListDistinctTargetTypes(ctx)
	if err != nil {
		s.logger.Error("failed to list target types", zap.Error(err))
		return nil, apperr.Internal("INTERNAL_ERROR", "failed to list target types", err)
	}
	return types, nil
}
