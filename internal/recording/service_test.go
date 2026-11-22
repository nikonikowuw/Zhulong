package recording_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/filestore"
	"github.com/nikonikowuw/Zhulong/internal/recording"
	"go.uber.org/zap"
)

const insertCameraSQL = `
INSERT INTO cameras (id, name, enabled, revision, created_at, updated_at)
VALUES (?, ?, 1, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`

// fakeHub simulates the native recorder: one active clip per camera; a trigger while a
// clip is active extends it (Created=false, same seq).
type fakeHub struct {
	mu        sync.Mutex
	nextSeq   uint64
	active    map[string]uint64 // camera → seq
	offsets   map[uint64]int64
	requests  []recording.ClipTriggerRequest
	events    []recording.ClipEvent
	triggerFn func(cameraID string, req recording.ClipTriggerRequest) (recording.ClipTriggerResult, error)
	pollErr   error
}

func newFakeHub() *fakeHub {
	return &fakeHub{active: map[string]uint64{}, offsets: map[uint64]int64{}}
}

func (h *fakeHub) TriggerClip(
	_ context.Context,
	cameraID string,
	req recording.ClipTriggerRequest,
) (recording.ClipTriggerResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, req)
	if h.triggerFn != nil {
		return h.triggerFn(cameraID, req)
	}
	if seq, ok := h.active[cameraID]; ok {
		h.offsets[seq] += 1000
		return recording.ClipTriggerResult{Seq: seq, Created: false, Codec: "h264", OffsetMs: h.offsets[seq]}, nil
	}
	h.nextSeq++
	seq := h.nextSeq
	h.active[cameraID] = seq
	h.offsets[seq] = 5000
	return recording.ClipTriggerResult{Seq: seq, Created: true, Codec: "h264", OffsetMs: 5000}, nil
}

func (h *fakeHub) PollClipEvents(_ context.Context, max int) ([]recording.ClipEvent, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pollErr != nil {
		return nil, h.pollErr
	}
	n := min(max, len(h.events))
	out := append([]recording.ClipEvent(nil), h.events[:n]...)
	h.events = h.events[n:]
	return out, nil
}

// finish ends the camera's active clip and queues a completion event.
func (h *fakeHub) finish(cameraID string, ev recording.ClipEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ev.Seq = h.active[cameraID]
	delete(h.active, cameraID)
	h.events = append(h.events, ev)
}

func (h *fakeHub) lastRequest(t *testing.T) recording.ClipTriggerRequest {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.requests) == 0 {
		t.Fatal("no trigger requests recorded")
	}
	return h.requests[len(h.requests)-1]
}

type staticRoot struct {
	root string
	err  error
}

func (r staticRoot) MediaRoot(context.Context) (string, error) { return r.root, r.err }

type gate struct{ open bool }

func (g *gate) CanWrite() bool { return g.open }

type env struct {
	t     *testing.T
	db    *database.Store
	store *recording.Store
	hub   *fakeHub
	root  string
	gate  *gate
	svc   *recording.Service
	now   time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	for _, cam := range []string{"cam_01", "cam_02"} {
		if err := dbStore.DB().Exec(insertCameraSQL, cam, cam).Error; err != nil {
			t.Fatalf("insert camera: %v", err)
		}
	}
	e := &env{
		t:     t,
		db:    dbStore,
		store: recording.NewStore(dbStore),
		hub:   newFakeHub(),
		root:  t.TempDir(),
		gate:  &gate{open: true},
		now:   time.Date(2026, 11, 17, 8, 0, 0, 0, time.UTC),
	}
	e.svc = recording.NewService(e.store, e.hub, staticRoot{root: e.root}, e.gate, zap.NewNop(), recording.Options{
		PollInterval:        10 * time.Millisecond,
		CleanupInitialDelay: time.Hour,
		Now:                 func() time.Time { return e.now },
	})
	return e
}

func (e *env) insertAlarm(id, cameraID string) {
	e.t.Helper()
	err := e.db.DB().Exec(`INSERT INTO alarms (id, camera_id, alarm_type, pts, captured_at)
		VALUES (?, ?, 'fire', 1, ?)`, id, cameraID, e.now).Error
	if err != nil {
		e.t.Fatalf("insert alarm: %v", err)
	}
}

func (e *env) insertRecognition(id, cameraID string) {
	e.t.Helper()
	err := e.db.DB().Exec(`INSERT INTO recognitions (id, camera_id, pts, captured_at)
		VALUES (?, ?, 1, ?)`, id, cameraID, e.now).Error
	if err != nil {
		e.t.Fatalf("insert recognition: %v", err)
	}
}

type eventClipRef struct {
	ClipID   *string
	OffsetMS *int64
}

func (e *env) eventClip(table, id string) eventClipRef {
	e.t.Helper()
	var ref eventClipRef
	row := e.db.DB().Raw("SELECT clip_id, clip_offset_ms FROM "+table+" WHERE id = ?", id).Row()
	if err := row.Scan(&ref.ClipID, &ref.OffsetMS); err != nil {
		e.t.Fatalf("scan %s clip: %v", table, err)
	}
	return ref
}

func (e *env) clip(id string) *recording.EventClip {
	e.t.Helper()
	clip, err := e.store.GetClip(context.Background(), id)
	if err != nil {
		e.t.Fatalf("get clip: %v", err)
	}
	return clip
}

func (e *env) abs(rel string) string { return filepath.Join(e.root, filepath.FromSlash(rel)) }

func writeFile(t *testing.T, p string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o640); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }

func alarmTrigger(eventID string) recording.TriggerRequest {
	return recording.TriggerRequest{
		CameraID:    "cam_01",
		AlgorithmID: "algo",
		EventKind:   recording.EventKindAlarm,
		EventID:     eventID,
		PTSUS:       ptr(int64(123_456)),
		CapturedAt:  time.Date(2026, 11, 16, 23, 59, 0, 0, time.FixedZone("CST", 8*3600)),
		Policy:      recording.ClipPolicy{PostSeconds: 10},
	}
}

func TestClipRelPath(t *testing.T) {
	at := time.Date(2026, 11, 17, 1, 0, 0, 0, time.FixedZone("CST", 8*3600))
	got := recording.ClipRelPath("cam_01", at, "01ABC")
	want := "recordings/cam_01/2026-11-16/evt-01ABC.mp4"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if strings.Contains(filepath.Base(got), "_") {
		t.Fatalf("clip file name must not contain '_': %q", got)
	}
}

func TestTriggerCreatesClipAndLocksAlarm(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertAlarm("alarm_1", "cam_01")

	clipID, offset, err := e.svc.Trigger(ctx, alarmTrigger("alarm_1"))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if clipID == "" || offset != 5000 {
		t.Fatalf("unexpected result id=%q offset=%d", clipID, offset)
	}

	req := e.hub.lastRequest(t)
	if req.EventPTSUS != 123_456 || req.PostMs != 10_000 || req.MaxMs != recording.DefaultMaxClipMs {
		t.Fatalf("unexpected trigger request %+v", req)
	}
	// 日期目录按 capturedAt 的 UTC。
	wantRel := "recordings/cam_01/2026-11-16/evt-" + clipID + ".mp4"
	if req.Path != e.abs(wantRel) {
		t.Fatalf("path = %q, want %q", req.Path, e.abs(wantRel))
	}
	if info, err := os.Stat(filepath.Dir(req.Path)); err != nil || !info.IsDir() {
		t.Fatalf("clip directory not created: %v", err)
	}
	if _, err := os.Stat(req.Path + ".lock"); err != nil {
		t.Fatalf("alarm clip must have .lock: %v", err)
	}

	clip := e.clip(clipID)
	if clip == nil || clip.Status != recording.ClipStatusRecording || clip.Path != wantRel ||
		clip.CameraID != "cam_01" || clip.Codec != "h264" {
		t.Fatalf("unexpected clip row %+v", clip)
	}
	ref := e.eventClip("alarms", "alarm_1")
	if ref.ClipID == nil || *ref.ClipID != clipID || ref.OffsetMS == nil || *ref.OffsetMS != 5000 {
		t.Fatalf("alarm not attached: %+v", ref)
	}
	if e.svc.ActiveClips() != 1 {
		t.Fatalf("expected one active clip")
	}
}

func TestTriggerResolvesRelativeMediaRootToAbsolutePath(t *testing.T) {
	e := newEnv(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	relRoot, err := filepath.Rel(cwd, e.root)
	if err != nil {
		t.Fatalf("relative root: %v", err)
	}
	if filepath.IsAbs(relRoot) {
		t.Fatalf("test setup: expected relative root, got %q", relRoot)
	}

	e.svc = recording.NewService(e.store, e.hub, staticRoot{root: relRoot}, e.gate, zap.NewNop(), recording.Options{
		PollInterval:        10 * time.Millisecond,
		CleanupInitialDelay: time.Hour,
		Now:                 func() time.Time { return e.now },
	})
	e.insertAlarm("alarm_rel", "cam_01")

	if _, _, err := e.svc.Trigger(context.Background(), alarmTrigger("alarm_rel")); err != nil {
		t.Fatalf("trigger: %v", err)
	}
	req := e.hub.lastRequest(t)
	if !filepath.IsAbs(req.Path) {
		t.Fatalf("trigger path must be absolute for native ABI, got %q", req.Path)
	}
	if !strings.HasPrefix(req.Path, e.root+string(os.PathSeparator)) {
		t.Fatalf("trigger path %q is outside media root %q", req.Path, e.root)
	}
}

// AC3（Go 侧）：同一相机连续 3 个事件只生成 1 个片段，偏移单调递增；识别事件不加锁。
func TestTriggerExtendsActiveClip(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, id := range []string{"rec_1", "rec_2", "rec_3"} {
		e.insertRecognition(id, "cam_01")
	}

	var clipIDs []string
	var offsets []int64
	for _, id := range []string{"rec_1", "rec_2", "rec_3"} {
		req := alarmTrigger(id)
		req.EventKind = recording.EventKindRecognition
		clipID, offset, err := e.svc.Trigger(ctx, req)
		if err != nil {
			t.Fatalf("trigger %s: %v", id, err)
		}
		clipIDs = append(clipIDs, clipID)
		offsets = append(offsets, offset)
	}
	if clipIDs[0] != clipIDs[1] || clipIDs[1] != clipIDs[2] {
		t.Fatalf("events must share one clip: %v", clipIDs)
	}
	if !(offsets[0] < offsets[1] && offsets[1] < offsets[2]) {
		t.Fatalf("offsets must increase: %v", offsets)
	}
	for i, id := range []string{"rec_1", "rec_2", "rec_3"} {
		ref := e.eventClip("recognitions", id)
		if ref.ClipID == nil || *ref.ClipID != clipIDs[0] || *ref.OffsetMS != offsets[i] {
			t.Fatalf("%s attachment wrong: %+v", id, ref)
		}
	}
	var count int64
	if err := e.db.DB().Raw("SELECT COUNT(*) FROM event_clips").Scan(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected exactly one clip row, got %d", count)
	}
	clip := e.clip(clipIDs[0])
	if _, err := os.Stat(e.abs(clip.Path) + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("recognition-only clip must not be locked: %v", err)
	}

	// 识别建的片段再被告警延长：告警引用后补锁（按原片段路径）。
	e.insertAlarm("alarm_x", "cam_01")
	clipID, _, err := e.svc.Trigger(ctx, alarmTrigger("alarm_x"))
	if err != nil || clipID != clipIDs[0] {
		t.Fatalf("alarm extension: id=%q err=%v", clipID, err)
	}
	if _, err := os.Stat(e.abs(clip.Path) + ".lock"); err != nil {
		t.Fatalf("clip referenced by an alarm must be locked: %v", err)
	}

	// 片段结束后再来事件：新片段（120s 切分由 native 判定，这里体现为 Created=true）。
	e.hub.finish("cam_01", recording.ClipEvent{Status: recording.ClipStatusCompleted})
	if err := e.svc.PollOnce(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	e.insertRecognition("rec_4", "cam_01")
	req := alarmTrigger("rec_4")
	req.EventKind = recording.EventKindRecognition
	next, _, err := e.svc.Trigger(ctx, req)
	if err != nil || next == clipIDs[0] {
		t.Fatalf("expected a new clip, got %q err=%v", next, err)
	}
}

func TestTriggerFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("gate closed", func(t *testing.T) {
		e := newEnv(t)
		e.insertAlarm("a", "cam_01")
		e.gate.open = false
		if _, _, err := e.svc.Trigger(ctx, alarmTrigger("a")); !errors.Is(err, recording.ErrClipSkipped) {
			t.Fatalf("want ErrClipSkipped, got %v", err)
		}
		if len(e.hub.requests) != 0 {
			t.Fatalf("hub must not be called when the gate is closed")
		}
		if ref := e.eventClip("alarms", "a"); ref.ClipID != nil {
			t.Fatalf("clip_id must stay NULL")
		}
	})

	t.Run("native error", func(t *testing.T) {
		e := newEnv(t)
		e.insertAlarm("a", "cam_01")
		e.hub.triggerFn = func(string, recording.ClipTriggerRequest) (recording.ClipTriggerResult, error) {
			return recording.ClipTriggerResult{}, errors.New("not ready")
		}
		if _, _, err := e.svc.Trigger(ctx, alarmTrigger("a")); err == nil {
			t.Fatal("expected error")
		}
		if ref := e.eventClip("alarms", "a"); ref.ClipID != nil {
			t.Fatalf("clip_id must stay NULL")
		}
	})

	t.Run("state lost", func(t *testing.T) {
		e := newEnv(t)
		e.insertAlarm("a", "cam_01")
		e.hub.triggerFn = func(string, recording.ClipTriggerRequest) (recording.ClipTriggerResult, error) {
			return recording.ClipTriggerResult{Seq: 99, Created: false}, nil
		}
		if _, _, err := e.svc.Trigger(ctx, alarmTrigger("a")); !errors.Is(err, recording.ErrClipStateLost) {
			t.Fatalf("want ErrClipStateLost, got %v", err)
		}
	})

	t.Run("missing event row", func(t *testing.T) {
		e := newEnv(t)
		if _, _, err := e.svc.Trigger(ctx, alarmTrigger("ghost")); !errors.Is(err, recording.ErrEventNotFound) {
			t.Fatalf("want ErrEventNotFound, got %v", err)
		}
	})

	t.Run("media root error", func(t *testing.T) {
		e := newEnv(t)
		svc := recording.NewService(e.store, e.hub, staticRoot{err: errors.New("unmounted")}, nil, nil, recording.Options{})
		if _, _, err := svc.Trigger(ctx, alarmTrigger("a")); err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		e := newEnv(t)
		svc := recording.NewService(e.store, nil, staticRoot{root: e.root}, nil, nil, recording.Options{})
		if _, _, err := svc.Trigger(ctx, alarmTrigger("a")); !errors.Is(err, recording.ErrClipUnavailable) {
			t.Fatalf("want ErrClipUnavailable, got %v", err)
		}
	})

	invalid := []struct {
		name   string
		mutate func(*recording.TriggerRequest)
	}{
		{name: "camera traversal", mutate: func(r *recording.TriggerRequest) { r.CameraID = "../x" }},
		{name: "camera empty", mutate: func(r *recording.TriggerRequest) { r.CameraID = "" }},
		{name: "event id empty", mutate: func(r *recording.TriggerRequest) { r.EventID = "" }},
		{name: "capture kind", mutate: func(r *recording.TriggerRequest) { r.EventKind = "capture" }},
		{name: "post zero", mutate: func(r *recording.TriggerRequest) { r.Policy.PostSeconds = 0 }},
	}
	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			req := alarmTrigger("a")
			tt.mutate(&req)
			if _, _, err := e.svc.Trigger(ctx, req); !errors.Is(err, recording.ErrInvalidTrigger) {
				t.Fatalf("want ErrInvalidTrigger, got %v", err)
			}
		})
	}
}

func TestPollUpdatesClipStatus(t *testing.T) {
	tests := []struct {
		name     string
		event    recording.ClipEvent
		fileSize int
		want     recording.ClipStatus
		wantSize int64
	}{
		{
			name:     "completed",
			event:    recording.ClipEvent{Status: recording.ClipStatusCompleted, Codec: "hevc", DurationMs: 15_000, SizeBytes: 4096},
			want:     recording.ClipStatusCompleted,
			wantSize: 4096,
		},
		{
			name:     "truncated size from file",
			event:    recording.ClipEvent{Status: recording.ClipStatusTruncated, Codec: "h264", DurationMs: 3_000},
			fileSize: 77,
			want:     recording.ClipStatusTruncated,
			wantSize: 77,
		},
		{
			name:  "failed",
			event: recording.ClipEvent{Status: recording.ClipStatusFailed},
			want:  recording.ClipStatusFailed,
		},
		{
			name:  "unknown treated as failed",
			event: recording.ClipEvent{Status: "weird"},
			want:  recording.ClipStatusFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			ctx := context.Background()
			e.insertAlarm("a", "cam_01")
			clipID, _, err := e.svc.Trigger(ctx, alarmTrigger("a"))
			if err != nil {
				t.Fatalf("trigger: %v", err)
			}
			if tt.fileSize > 0 {
				writeFile(t, e.hub.lastRequest(t).Path, tt.fileSize)
			}
			e.hub.finish("cam_01", tt.event)
			if err := e.svc.PollOnce(ctx); err != nil {
				t.Fatalf("poll: %v", err)
			}
			clip := e.clip(clipID)
			if clip.Status != tt.want || clip.SizeBytes != tt.wantSize || clip.EndedAt == nil {
				t.Fatalf("unexpected clip %+v", clip)
			}
			if clip.DurationMS != tt.event.DurationMs && tt.want != recording.ClipStatusFailed {
				t.Fatalf("duration = %d", clip.DurationMS)
			}
			if tt.event.Codec != "" && clip.Codec != tt.event.Codec {
				t.Fatalf("codec = %q", clip.Codec)
			}
			if e.svc.ActiveClips() != 0 {
				t.Fatalf("finished clip must leave the active map")
			}
			// failed 行保留，供前端显示「录像失败」。
			if ref := e.eventClip("alarms", "a"); ref.ClipID == nil {
				t.Fatalf("event must keep its clip reference")
			}
		})
	}
}

func TestStartRepairsLeftoversAndStopPollsFinal(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	mk := func(id string) *recording.EventClip {
		clip := &recording.EventClip{
			ID:        id,
			CameraID:  "cam_01",
			Path:      recording.ClipRelPath("cam_01", e.now, id),
			Status:    recording.ClipStatusRecording,
			StartedAt: e.now,
		}
		if err := e.store.CreateClip(ctx, clip); err != nil {
			t.Fatalf("create clip: %v", err)
		}
		return clip
	}
	withFile := mk("CLIPFILE")
	writeFile(t, e.abs(withFile.Path), 100)
	withPart := mk("CLIPPART")
	writeFile(t, e.abs(withPart.Path)+".part", 50)
	mk("CLIPGONE")
	emptyPart := mk("CLIPEMPTY")
	writeFile(t, e.abs(emptyPart.Path)+".part", 0)

	if err := e.svc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	if c := e.clip("CLIPFILE"); c.Status != recording.ClipStatusCompleted || !c.Partial || c.SizeBytes != 100 {
		t.Fatalf("file clip: %+v", c)
	}
	if c := e.clip("CLIPPART"); c.Status != recording.ClipStatusCompleted || !c.Partial || c.SizeBytes != 50 {
		t.Fatalf("part clip: %+v", c)
	}
	if _, err := os.Stat(e.abs(withPart.Path)); err != nil {
		t.Fatalf(".part must be renamed to the final path: %v", err)
	}
	if c := e.clip("CLIPGONE"); c.Status != recording.ClipStatusFailed || c.Partial {
		t.Fatalf("missing clip: %+v", c)
	}
	if c := e.clip("CLIPEMPTY"); c.Status != recording.ClipStatusFailed {
		t.Fatalf("empty part clip: %+v", c)
	}

	// Start 后触发一个片段；native 在相机停止时收尾，Stop 的最后一次 poll 写入终态。
	e.insertAlarm("a", "cam_01")
	clipID, _, err := e.svc.Trigger(ctx, alarmTrigger("a"))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if err := e.svc.Start(ctx); err != nil {
		t.Fatalf("second start must be idempotent: %v", err)
	}
	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// 完成事件可能被后台 loop 或 Stop 的最后一次 poll 处理，两者结果一致。
	e.hub.finish("cam_01", recording.ClipEvent{Status: recording.ClipStatusCompleted, DurationMs: 9000, SizeBytes: 1})
	if err := e.svc.Stop(stopCtx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if c := e.clip(clipID); c.Status != recording.ClipStatusCompleted {
		t.Fatalf("clip should be finalized by stop, got %+v", c)
	}
	if err := e.svc.Stop(stopCtx); err != nil {
		t.Fatalf("second stop must be a no-op: %v", err)
	}
}

func TestPollLoopRunsInBackground(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.svc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() {
		if err := e.svc.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	e.insertAlarm("a", "cam_01")
	clipID, _, err := e.svc.Trigger(ctx, alarmTrigger("a"))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	e.hub.finish("cam_01", recording.ClipEvent{Status: recording.ClipStatusTruncated})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c := e.clip(clipID); c.Status == recording.ClipStatusTruncated {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("poll loop did not finalize the clip")
}

// AC7（行侧）：文件被 storage 清理后，片段行随之删除，事件 clip_id 由 FK 置空；
// failed 行超过保留期后删除；recording 行绝不被清理。
func TestCleanupOnce(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.insertRecognition("rec_gone", "cam_01")
	e.insertRecognition("rec_keep", "cam_01")

	mk := func(id string, status recording.ClipStatus, startedAt time.Time) *recording.EventClip {
		clip := &recording.EventClip{
			ID:        id,
			CameraID:  "cam_01",
			Path:      recording.ClipRelPath("cam_01", startedAt, id),
			Status:    status,
			StartedAt: startedAt,
		}
		if err := e.store.CreateClip(ctx, clip); err != nil {
			t.Fatalf("create clip: %v", err)
		}
		return clip
	}
	gone := mk("C1GONE", recording.ClipStatusCompleted, e.now)
	kept := mk("C2KEEP", recording.ClipStatusTruncated, e.now)
	writeFile(t, e.abs(kept.Path), 10)
	mk("C3REC", recording.ClipStatusRecording, e.now)
	mk("C4FAILOLD", recording.ClipStatusFailed, e.now.Add(-40*24*time.Hour))
	mk("C5FAILNEW", recording.ClipStatusFailed, e.now.Add(-time.Hour))

	if err := e.store.AttachClip(ctx, recording.EventKindRecognition, "rec_gone", gone.ID, 100); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := e.store.AttachClip(ctx, recording.EventKindRecognition, "rec_keep", kept.ID, 200); err != nil {
		t.Fatalf("attach: %v", err)
	}

	// 无保留期 provider：failed 行一律保留。
	if err := e.svc.CleanupOnce(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if e.clip("C1GONE") != nil {
		t.Fatal("row whose file is gone must be deleted")
	}
	if ref := e.eventClip("recognitions", "rec_gone"); ref.ClipID != nil {
		t.Fatalf("FK must null the event's clip_id, got %v", *ref.ClipID)
	}
	if ref := e.eventClip("recognitions", "rec_keep"); ref.ClipID == nil || *ref.ClipID != kept.ID {
		t.Fatal("clip with an existing file must stay attached")
	}
	for _, id := range []string{"C2KEEP", "C3REC", "C4FAILOLD", "C5FAILNEW"} {
		if e.clip(id) == nil {
			t.Fatalf("%s must survive", id)
		}
	}

	e.svc.SetRetentionDaysProvider(recording.RetentionDaysFunc(func(context.Context) (int, error) { return 30, nil }))
	if err := e.svc.CleanupOnce(ctx); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if e.clip("C4FAILOLD") != nil {
		t.Fatal("expired failed row must be deleted")
	}
	if e.clip("C5FAILNEW") == nil || e.clip("C3REC") == nil {
		t.Fatal("fresh failed / recording rows must survive")
	}

	// 媒体根不可用时绝不能把「全都不存在」当真。
	svc := recording.NewService(e.store, e.hub, staticRoot{root: filepath.Join(e.root, "unmounted")}, nil, nil, recording.Options{})
	if err := svc.CleanupOnce(ctx); err == nil {
		t.Fatal("expected error for an unavailable media root")
	}
	if e.clip("C2KEEP") == nil {
		t.Fatal("rows must survive an unavailable media root")
	}
}

// Stop 的最后一次 poll 必须独立于后台 loop：loop 永不 tick 时也要写入终态（R4.5）。
func TestStopFinalPoll(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	svc := recording.NewService(e.store, e.hub, staticRoot{root: e.root}, nil, nil, recording.Options{
		PollInterval:        time.Hour,
		CleanupInitialDelay: time.Hour,
	})
	if err := svc.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	e.insertAlarm("a", "cam_01")
	clipID, _, err := svc.Trigger(ctx, alarmTrigger("a"))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	e.hub.finish("cam_01", recording.ClipEvent{Status: recording.ClipStatusCompleted, DurationMs: 4200, SizeBytes: 9})
	if c := e.clip(clipID); c.Status != recording.ClipStatusRecording {
		t.Fatalf("loop must not have run yet: %+v", c)
	}
	if err := svc.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if c := e.clip(clipID); c.Status != recording.ClipStatusCompleted || c.DurationMS != 4200 {
		t.Fatalf("final poll did not finalize the clip: %+v", c)
	}
}

type stubClipFileChecker struct {
	fn func(ctx context.Context, relPath string) (bool, error)
}

func (s stubClipFileChecker) ClipFileExists(ctx context.Context, relPath string) (bool, error) {
	return s.fn(ctx, relPath)
}

func TestFallthroughAbortAndOrphanLockCleanup(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	// 1. 告警片段最终失败时必须顺手清除 .lock，避免永久残留空锁文件。
	e.insertAlarm("a_fail", "cam_01")
	clipID, _, err := e.svc.Trigger(ctx, alarmTrigger("a_fail"))
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	lockPath := e.abs(e.clip(clipID).Path) + ".lock"
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("expected .lock on alarm clip: %v", err)
	}
	e.hub.finish("cam_01", recording.ClipEvent{Status: recording.ClipStatusFailed})
	if err := e.svc.PollOnce(ctx); err != nil {
		t.Fatalf("poll: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("orphan .lock must be removed when clip fails, err=%v", err)
	}
	// 失败后原 codec 不得被空串覆盖。
	if got := e.clip(clipID).Codec; got != "h264" {
		t.Fatalf("codec overwritten on failed event: got %q, want h264", got)
	}

	// 2. 外置盘卸载（挂载点目录仍在但 ClipFileChecker 报不可用）时，清理与修复都必须中止。
	kept := &recording.EventClip{
		ID:        "C_FALLTHROUGH",
		CameraID:  "cam_01",
		Path:      recording.ClipRelPath("cam_01", e.now, "C_FALLTHROUGH"),
		Status:    recording.ClipStatusCompleted,
		StartedAt: e.now,
	}
	if err := e.store.CreateClip(ctx, kept); err != nil {
		t.Fatalf("create kept: %v", err)
	}
	rec := &recording.EventClip{
		ID:        "C_REC_FALLTHROUGH",
		CameraID:  "cam_01",
		Path:      recording.ClipRelPath("cam_01", e.now, "C_REC_FALLTHROUGH"),
		Status:    recording.ClipStatusRecording,
		StartedAt: e.now,
	}
	if err := e.store.CreateClip(ctx, rec); err != nil {
		t.Fatalf("create rec: %v", err)
	}

	e.svc.SetFileChecker(stubClipFileChecker{
		fn: func(context.Context, string) (bool, error) {
			return false, errors.New("media root fallen through")
		},
	})
	if err := e.svc.CleanupOnce(ctx); err == nil {
		t.Fatal("expected CleanupOnce error on fallthrough")
	}
	if e.clip(kept.ID) == nil {
		t.Fatal("CleanupOnce must not delete rows when media root falls through")
	}
	if err := e.svc.RepairLeftovers(ctx); err == nil {
		t.Fatal("expected RepairLeftovers error on fallthrough")
	}
	if got := e.clip(rec.ID).Status; got != recording.ClipStatusRecording {
		t.Fatalf("RepairLeftovers must not fail rows when media root falls through, got %q", got)
	}
}

func TestRepairLeftovers_MixedS3AndLocalStillChecksFallthrough(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	s3Rec := &recording.EventClip{
		ID:             "C_MIX_S3",
		CameraID:       "cam_01",
		Path:           recording.ClipRelPath("cam_01", e.now, "C_MIX_S3"),
		Status:         recording.ClipStatusRecording,
		StorageBackend: filestore.BackendS3,
		StartedAt:      e.now,
	}
	localRec := &recording.EventClip{
		ID:             "C_MIX_LOCAL",
		CameraID:       "cam_01",
		Path:           recording.ClipRelPath("cam_01", e.now, "C_MIX_LOCAL"),
		Status:         recording.ClipStatusRecording,
		StorageBackend: filestore.BackendLocal,
		StartedAt:      e.now.Add(time.Second),
	}
	for _, row := range []*recording.EventClip{s3Rec, localRec} {
		if err := e.store.CreateClip(ctx, row); err != nil {
			t.Fatalf("create %s: %v", row.ID, err)
		}
	}

	var checkedPath string
	e.svc.SetFileChecker(stubClipFileChecker{
		fn: func(_ context.Context, relPath string) (bool, error) {
			checkedPath = relPath
			return false, errors.New("media root fallen through")
		},
	})
	if err := e.svc.RepairLeftovers(ctx); err == nil {
		t.Fatal("expected RepairLeftovers error when any row is local and root falls through")
	}
	if checkedPath != localRec.Path {
		t.Fatalf("expected fallthrough check against local row %q, got %q", localRec.Path, checkedPath)
	}
	for _, id := range []string{s3Rec.ID, localRec.ID} {
		if got := e.clip(id).Status; got != recording.ClipStatusRecording {
			t.Fatalf("expected %s to remain recording on fallthrough, got %q", id, got)
		}
	}
}
