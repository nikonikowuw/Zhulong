package record_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

type clipPolicyMap map[string]record.ClipPolicy // key: camera|algorithm

func (m clipPolicyMap) ClipPolicyFor(cameraID, algorithmID string) (record.ClipPolicy, bool) {
	p, ok := m[cameraID+"|"+algorithmID]
	return p, ok
}

// fakeClipTrigger emulates recording.Service: one clip per camera, extended by later
// events; it inserts the event_clips row and attaches it to the event row.
type fakeClipTrigger struct {
	t     *testing.T
	db    *database.Store
	err   error
	mu    sync.Mutex
	calls []record.ClipTriggerRequest
	clips map[string]string // camera → clip id
	next  int64
}

func (f *fakeClipTrigger) TriggerClip(_ context.Context, req record.ClipTriggerRequest) (string, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	if f.err != nil {
		return "", 0, f.err
	}
	if f.clips == nil {
		f.clips = map[string]string{}
	}
	clipID, ok := f.clips[req.CameraID]
	if !ok {
		clipID = "CLIP" + req.CameraID
		f.clips[req.CameraID] = clipID
		if err := f.db.DB().Exec(`INSERT INTO event_clips (id, camera_id, path, codec, status, started_at)
			VALUES (?, ?, ?, 'h264', 'recording', CURRENT_TIMESTAMP)`,
			clipID, req.CameraID, "recordings/"+req.CameraID+"/2026-10-16/evt-"+clipID+".mp4").Error; err != nil {
			f.t.Errorf("insert clip: %v", err)
		}
	}
	f.next += 1000
	table := "alarms"
	if req.Kind == record.ClipEventRecognition {
		table = "recognitions"
	}
	if err := f.db.DB().Exec("UPDATE "+table+" SET clip_id = ?, clip_offset_ms = ? WHERE id = ?",
		clipID, f.next, req.EventID).Error; err != nil {
		f.t.Errorf("attach clip: %v", err)
	}
	return clipID, f.next, nil
}

func (f *fakeClipTrigger) snapshot() []record.ClipTriggerRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]record.ClipTriggerRequest(nil), f.calls...)
}

type countingListener struct {
	mu     sync.Mutex
	events []record.AlarmEvent
}

func (l *countingListener) OnAlarm(_ context.Context, ev record.AlarmEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, ev)
}

func (l *countingListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

func detectionResult(at time.Time) aitypes.InferenceResult {
	pts := int64(777_000)
	return aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         77,
		PTSUS:       &pts,
		Timestamp:   at,
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0.2, 0.2, 0.4, 0.4}, Label: "no_helmet_alarm", Score: 0.9},
			{Box: [4]float64{0.0, 0.0, 1.0, 1.0}, Label: "smoke_alarm", Score: 0.8},
			// 普通目标 → capture，绝不触发录像。
			{Box: [4]float64{0.5, 0.5, 0.8, 0.9}, Label: "car", Score: 0.95},
		},
	}
}

func personResult(at time.Time, matched bool, trackID int64) aitypes.InferenceResult {
	face := aitypes.FaceSubItem{Box: [4]float64{0.15, 0.22, 0.35, 0.45}, Quality: 0.9}
	if matched {
		face.Matched = true
		face.PersonID = "person_alice"
		face.PersonName = "Alice"
		face.Similarity = 0.95
	}
	return aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1,
		Timestamp:   at,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{{
			TrackID:   trackID,
			BodyBox:   [4]float64{0.1, 0.2, 0.4, 0.9},
			BodyScore: 0.9,
			Faces:     []aitypes.FaceSubItem{face},
		}},
	}
}

// AC3 风格关联 + 列表/详情 clip 投影 + captures 永不触发。
func TestAlarmClipTriggerAndProjection(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	trigger := &fakeClipTrigger{t: t, db: dbStore}
	svc.SetClipTrigger(trigger)
	svc.SetClipPolicyProvider(clipPolicyMap{"cam_01|algo_01": {PostSeconds: 12}})

	at := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	if err := svc.ProcessInferenceResult(ctx, detectionResult(at)); err != nil {
		t.Fatalf("process: %v", err)
	}

	calls := trigger.snapshot()
	if len(calls) != 2 {
		t.Fatalf("expected 2 alarm triggers (captures never trigger), got %d: %+v", len(calls), calls)
	}
	for _, c := range calls {
		if c.Kind != record.ClipEventAlarm || c.PostSeconds != 12 || c.CameraID != "cam_01" ||
			c.AlgorithmID != "algo_01" || c.PTSUS == nil || *c.PTSUS != 777_000 || !c.CapturedAt.Equal(at) {
			t.Fatalf("unexpected trigger request %+v", c)
		}
	}

	alarms, total, err := svc.ListAlarms(ctx, record.AlarmFilter{CameraID: "cam_01"})
	if err != nil || total != 2 {
		t.Fatalf("list alarms: total=%d err=%v", total, err)
	}
	offsets := map[int64]bool{}
	for _, a := range alarms {
		if a.Clip == nil {
			t.Fatalf("alarm %s missing clip projection", a.ID)
		}
		if a.Clip.ID != "CLIPcam_01" || a.Clip.Status != "recording" || a.Clip.Codec != "h264" ||
			a.Clip.MediaPath != "recordings/cam_01/2026-10-16/evt-CLIPcam_01.mp4" {
			t.Fatalf("unexpected clip projection %+v", a.Clip)
		}
		offsets[a.Clip.OffsetMS] = true
	}
	if len(offsets) != 2 {
		t.Fatalf("each event must carry its own offset: %v", offsets)
	}

	detail, err := svc.GetAlarm(ctx, alarms[0].ID)
	if err != nil || detail.Clip == nil || detail.Clip.OffsetMS != alarms[0].Clip.OffsetMS {
		t.Fatalf("detail clip projection: %+v err=%v", detail, err)
	}

	caps, _, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || len(caps) != 1 {
		t.Fatalf("expected the car capture to be persisted: %d err=%v", len(caps), err)
	}
}

func TestClassificationAlarmTriggersClip(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	svc := record.NewService(store, zap.NewNop())
	trigger := &fakeClipTrigger{t: t, db: dbStore}
	svc.SetClipTrigger(trigger)
	svc.SetClipPolicyProvider(clipPolicyMap{"cam_01|algo_01": {PostSeconds: 5}})

	res := aitypes.InferenceResult{
		CameraID:        "cam_01",
		AlgorithmID:     "algo_01",
		PTS:             1,
		Timestamp:       time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC),
		TaskType:        aitypes.TaskTypeClassification,
		Classifications: []aitypes.ClassificationItem{{Label: "fire_alarm", Score: 0.9}},
	}
	if err := svc.ProcessInferenceResult(context.Background(), res); err != nil {
		t.Fatalf("process: %v", err)
	}
	calls := trigger.snapshot()
	if len(calls) != 1 || calls[0].Kind != record.ClipEventAlarm {
		t.Fatalf("expected one alarm trigger, got %+v", calls)
	}
}

// AC2：识别过滤 stranger / known 生效；人体抓拍永不触发。
func TestRecognitionClipFilter(t *testing.T) {
	at := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		filter      string
		matched     bool
		wantTrigger bool
	}{
		{filter: "", matched: true, wantTrigger: true},
		{filter: "all", matched: false, wantTrigger: true},
		{filter: "stranger", matched: false, wantTrigger: true},
		{filter: "stranger", matched: true, wantTrigger: false},
		{filter: "known", matched: true, wantTrigger: true},
		{filter: "known", matched: false, wantTrigger: false},
	}
	for _, tt := range tests {
		name := tt.filter
		if tt.matched {
			name += "/matched"
		} else {
			name += "/unmatched"
		}
		t.Run(name, func(t *testing.T) {
			store, dbStore, cleanup := setupTestStore(t)
			defer cleanup()
			ctx := context.Background()
			svc := record.NewService(store, zap.NewNop())
			trigger := &fakeClipTrigger{t: t, db: dbStore}
			svc.SetClipTrigger(trigger)
			svc.SetClipPolicyProvider(clipPolicyMap{"cam_01|algo_01": {PostSeconds: 10, RecognitionFilter: tt.filter}})

			if err := svc.ProcessInferenceResult(ctx, personResult(at, tt.matched, 7)); err != nil {
				t.Fatalf("process: %v", err)
			}
			recs, total, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
			if err != nil || total != 1 {
				t.Fatalf("expected one recognition: total=%d err=%v", total, err)
			}
			calls := trigger.snapshot()
			for _, c := range calls {
				if c.Kind != record.ClipEventRecognition {
					t.Fatalf("only recognitions may trigger here, got %+v", c)
				}
			}
			if got := len(calls) == 1; got != tt.wantTrigger {
				t.Fatalf("trigger calls = %d, want trigger=%v", len(calls), tt.wantTrigger)
			}
			if tt.wantTrigger {
				if recs[0].Clip == nil || recs[0].Clip.ID != "CLIPcam_01" {
					t.Fatalf("recognition clip projection missing: %+v", recs[0].Clip)
				}
				detail, err := svc.GetRecognition(ctx, recs[0].ID)
				if err != nil || detail.Clip == nil {
					t.Fatalf("detail clip projection missing: err=%v", err)
				}
			} else if recs[0].Clip != nil {
				t.Fatalf("filtered recognition must have no clip")
			}
		})
	}
}

// AC5：触发失败（写门关闭 / native 报错）时事件照常入库与分发，clip_id 为 NULL。
func TestClipTriggerFailureDoesNotAffectEvents(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	listener := &countingListener{}
	svc.SetAlarmListener(listener)
	trigger := &fakeClipTrigger{t: t, db: dbStore, err: errors.New("native: not ready")}
	svc.SetClipTrigger(trigger)
	svc.SetClipPolicyProvider(clipPolicyMap{"cam_01|algo_01": {PostSeconds: 10}})

	at := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	if err := svc.ProcessInferenceResult(ctx, detectionResult(at)); err != nil {
		t.Fatalf("trigger failure must not surface: %v", err)
	}
	if err := svc.ProcessInferenceResult(ctx, personResult(at, false, 9)); err != nil {
		t.Fatalf("trigger failure must not surface: %v", err)
	}
	if len(trigger.snapshot()) != 3 {
		t.Fatalf("expected 3 trigger attempts, got %d", len(trigger.snapshot()))
	}
	if listener.count() != 3 {
		t.Fatalf("events must still be dispatched, got %d", listener.count())
	}

	alarms, total, err := svc.ListAlarms(ctx, record.AlarmFilter{})
	if err != nil || total != 2 {
		t.Fatalf("alarms must persist: total=%d err=%v", total, err)
	}
	for _, a := range alarms {
		if a.Clip != nil {
			t.Fatalf("alarm must have no clip")
		}
	}
	var nonNull int64
	if err := dbStore.DB().Raw(`SELECT
		(SELECT COUNT(*) FROM alarms WHERE clip_id IS NOT NULL) +
		(SELECT COUNT(*) FROM recognitions WHERE clip_id IS NOT NULL)`).Scan(&nonNull).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if nonNull != 0 {
		t.Fatalf("clip_id must stay NULL, got %d rows", nonNull)
	}
}

func TestClipTriggerSkippedWithoutPolicyOrDeps(t *testing.T) {
	at := time.Date(2026, 10, 16, 16, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		policies record.ClipPolicyProvider
		trigger  bool
	}{
		{name: "no policy provider", policies: nil, trigger: true},
		{name: "policy disabled for pair", policies: clipPolicyMap{"cam_02|algo_01": {PostSeconds: 5}}, trigger: true},
		{name: "no trigger", policies: clipPolicyMap{"cam_01|algo_01": {PostSeconds: 5}}, trigger: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, dbStore, cleanup := setupTestStore(t)
			defer cleanup()
			svc := record.NewService(store, zap.NewNop())
			trigger := &fakeClipTrigger{t: t, db: dbStore}
			if tt.trigger {
				svc.SetClipTrigger(trigger)
			}
			if tt.policies != nil {
				svc.SetClipPolicyProvider(tt.policies)
			}
			if err := svc.ProcessInferenceResult(context.Background(), detectionResult(at)); err != nil {
				t.Fatalf("process: %v", err)
			}
			if n := len(trigger.snapshot()); n != 0 {
				t.Fatalf("expected no triggers, got %d", n)
			}
		})
	}
}
