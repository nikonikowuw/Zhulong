package record

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ClipEventKind identifies which event table a clip trigger belongs to.
type ClipEventKind string

// Clip event kinds. Captures never trigger clips (R1.1).
const (
	ClipEventAlarm       ClipEventKind = "alarm"
	ClipEventRecognition ClipEventKind = "recognition"
)

// 识别事件录像过滤（与 task.ClipFilter* 取值一致）。
const (
	ClipFilterAll      = "all"
	ClipFilterStranger = "stranger"
	ClipFilterKnown    = "known"
)

// ClipPolicy is the per-(camera, algorithm) clip policy record needs to decide a trigger.
type ClipPolicy struct {
	PostSeconds int
	// RecognitionFilter is all|stranger|known; "" means all.
	RecognitionFilter string
}

// allowsRecognition reports whether a recognition passes the policy filter.
func (p ClipPolicy) allowsRecognition(matched bool) bool {
	switch p.RecognitionFilter {
	case ClipFilterStranger:
		return !matched
	case ClipFilterKnown:
		return matched
	default:
		return true
	}
}

// ClipPolicyProvider resolves the enabled clip policy for a camera/algorithm pair.
// ok=false means recording is disabled. Implemented by an app adapter over task.ClipPolicyCache.
type ClipPolicyProvider interface {
	ClipPolicyFor(cameraID, algorithmID string) (ClipPolicy, bool)
}

// ClipTriggerRequest describes one persisted event that should be covered by a clip.
type ClipTriggerRequest struct {
	CameraID    string
	AlgorithmID string
	Kind        ClipEventKind
	EventID     string
	PTSUS       *int64
	CapturedAt  time.Time
	PostSeconds int
}

// ClipTrigger starts or extends an event clip and attaches it to the event row.
// Implemented by an app adapter over recording.Service.Trigger.
type ClipTrigger interface {
	TriggerClip(ctx context.Context, req ClipTriggerRequest) (clipID string, offsetMs int64, err error)
}

// SetClipTrigger injects the clip trigger. nil disables clip recording.
func (s *Service) SetClipTrigger(t ClipTrigger) {
	s.depsMu.Lock()
	s.clipTrigger = t
	s.depsMu.Unlock()
}

// SetClipPolicyProvider injects the clip policy source. nil disables clip recording.
func (s *Service) SetClipPolicyProvider(p ClipPolicyProvider) {
	s.depsMu.Lock()
	s.clipPolicies = p
	s.depsMu.Unlock()
}

func (s *Service) clipDeps() (ClipTrigger, ClipPolicyProvider) {
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.clipTrigger, s.clipPolicies
}

// triggerAlarmClips triggers clips for freshly persisted alarms (after dispatch).
//
// R4.3：失败只记 Warn，绝不影响返回值、事件分发或已落库的行。
func (s *Service) triggerAlarmClips(ctx context.Context, f ingestFrame, alarms []*Alarm) {
	trigger, policies := s.clipDeps()
	if trigger == nil || policies == nil {
		return
	}
	for _, a := range alarms {
		if a == nil || a.AlgorithmID == nil {
			continue
		}
		policy, ok := policies.ClipPolicyFor(a.CameraID, *a.AlgorithmID)
		if !ok {
			continue
		}
		clipID, offset, ok := s.triggerClip(ctx, trigger, ClipTriggerRequest{
			CameraID:    a.CameraID,
			AlgorithmID: *a.AlgorithmID,
			Kind:        ClipEventAlarm,
			EventID:     a.ID,
			PTSUS:       f.res.PTSUS,
			CapturedAt:  a.CapturedAt,
			PostSeconds: policy.PostSeconds,
		})
		if ok {
			a.ClipID, a.ClipOffsetMS = &clipID, &offset
		}
	}
}

// triggerRecognitionClips triggers clips for freshly persisted recognitions that pass
// the policy's stranger/known filter.
func (s *Service) triggerRecognitionClips(ctx context.Context, f ingestFrame, recs []*Recognition) {
	trigger, policies := s.clipDeps()
	if trigger == nil || policies == nil {
		return
	}
	for _, r := range recs {
		if r == nil || r.AlgorithmID == nil {
			continue
		}
		policy, ok := policies.ClipPolicyFor(r.CameraID, *r.AlgorithmID)
		if !ok || !policy.allowsRecognition(r.Matched) {
			continue
		}
		clipID, offset, ok := s.triggerClip(ctx, trigger, ClipTriggerRequest{
			CameraID:    r.CameraID,
			AlgorithmID: *r.AlgorithmID,
			Kind:        ClipEventRecognition,
			EventID:     r.ID,
			PTSUS:       f.res.PTSUS,
			CapturedAt:  r.CapturedAt,
			PostSeconds: policy.PostSeconds,
		})
		if ok {
			r.ClipID, r.ClipOffsetMS = &clipID, &offset
		}
	}
}

func (s *Service) triggerClip(ctx context.Context, trigger ClipTrigger, req ClipTriggerRequest) (string, int64, bool) {
	clipID, offset, err := trigger.TriggerClip(ctx, req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", 0, false
		}
		s.logger.Warn("event clip trigger failed",
			zap.String("camera_id", req.CameraID),
			zap.String("algorithm_id", req.AlgorithmID),
			zap.String("event_kind", string(req.Kind)),
			zap.String("event_id", req.EventID),
			zap.Error(err),
		)
		return "", 0, false
	}
	return clipID, offset, clipID != ""
}

// eventClipRow is record's read-only projection of event_clips (owned by internal/recording).
// record reads it directly instead of importing recording, keeping the dependency one-way.
type eventClipRow struct {
	ID         string `gorm:"column:id"`
	Path       string `gorm:"column:path"`
	Codec      string `gorm:"column:codec"`
	Status     string `gorm:"column:status"`
	Partial    bool   `gorm:"column:partial"`
	DurationMS int64  `gorm:"column:duration_ms"`
}

func (eventClipRow) TableName() string { return "event_clips" }

// clipRowsByIDs batch-loads clip rows for the given IDs (deduplicated, empty IDs skipped).
func (s *Store) clipRowsByIDs(ctx context.Context, ids []string) (map[string]eventClipRow, error) {
	out := make(map[string]eventClipRow, len(ids))
	unique := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return out, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := gorm.G[eventClipRow](db).Where("id IN ?", unique).Find(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row
	}
	return out, nil
}

// clipRef is an event row's clip reference.
type clipRef struct {
	clipID   *string
	offsetMS *int64
}

// attachClipInfos fills resp clip projections with one secondary query.
// set(i, info) assigns the projection for item i. A query failure only drops the
// projections (logged): the list itself must not fail because of clip metadata.
func (s *Service) attachClipInfos(ctx context.Context, refs []clipRef, set func(i int, info *ClipInfo)) {
	ids := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.clipID != nil {
			ids = append(ids, *ref.clipID)
		}
	}
	if len(ids) == 0 {
		return
	}
	rows, err := s.store.clipRowsByIDs(ctx, ids)
	if err != nil {
		s.logger.Warn("load event clip projections failed", zap.Error(err))
		return
	}
	for i, ref := range refs {
		if ref.clipID == nil {
			continue
		}
		row, ok := rows[*ref.clipID]
		if !ok {
			continue
		}
		var offset int64
		if ref.offsetMS != nil {
			offset = max(*ref.offsetMS, 0)
		}
		set(i, &ClipInfo{
			ID:         row.ID,
			Status:     row.Status,
			Partial:    row.Partial,
			DurationMS: row.DurationMS,
			OffsetMS:   offset,
			MediaPath:  row.Path,
			Codec:      row.Codec,
		})
	}
}

func (s *Service) attachRecognitionClips(ctx context.Context, items []Recognition, responses []RecognitionResponse) {
	refs := make([]clipRef, len(items))
	for i := range items {
		refs[i] = clipRef{clipID: items[i].ClipID, offsetMS: items[i].ClipOffsetMS}
	}
	s.attachClipInfos(ctx, refs, func(i int, info *ClipInfo) { responses[i].Clip = info })
}

func (s *Service) attachAlarmClips(ctx context.Context, items []Alarm, responses []AlarmResponse) {
	refs := make([]clipRef, len(items))
	for i := range items {
		refs[i] = clipRef{clipID: items[i].ClipID, offsetMS: items[i].ClipOffsetMS}
	}
	s.attachClipInfos(ctx, refs, func(i int, info *ClipInfo) { responses[i].Clip = info })
}
