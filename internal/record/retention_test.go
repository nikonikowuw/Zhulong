package record_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// stubRetentionDays 是 RetentionDaysProvider 的可控替身。
type stubRetentionDays struct {
	days  int
	err   error
	calls atomic.Int64
}

func (s *stubRetentionDays) SnapshotRetentionDays(context.Context) (int, error) {
	s.calls.Add(1)
	return s.days, s.err
}

type recordIDs struct {
	recID            string
	alarmID          string
	capID            string
	recImagePath     string
	recFacePath      string
	captureImagePath string
}

// seedRecordRows inserts one expired-capable row into each record table.
func seedRecordRows(t *testing.T, store *record.Store, cameraID string, capturedAt time.Time) recordIDs {
	t.Helper()
	ctx := context.Background()

	ids := recordIDs{
		recID:            idutil.New(),
		alarmID:          idutil.New(),
		capID:            idutil.New(),
		recImagePath:     "snapshots/" + cameraID + "/" + idutil.New() + ".jpg",
		recFacePath:      "snapshots/" + cameraID + "/" + idutil.New() + "_face.jpg",
		captureImagePath: "snapshots/" + cameraID + "/" + idutil.New() + ".jpg",
	}
	if err := store.CreateRecognition(ctx, &record.Recognition{
		ID:            ids.recID,
		CameraID:      cameraID,
		PTS:           1000,
		ImagePath:     ids.recImagePath,
		FaceImagePath: ids.recFacePath,
		CapturedAt:    capturedAt,
	}); err != nil {
		t.Fatalf("seed recognition: %v", err)
	}
	if err := store.CreateAlarm(ctx, &record.Alarm{
		ID:         ids.alarmID,
		CameraID:   cameraID,
		AlarmType:  "smoke",
		ImagePath:  "snapshots/" + cameraID + "/alarm-" + ids.alarmID + ".jpg",
		CapturedAt: capturedAt,
	}); err != nil {
		t.Fatalf("seed alarm: %v", err)
	}
	if err := store.CreateCapture(ctx, &record.Capture{
		ID:         ids.capID,
		CameraID:   cameraID,
		TargetType: "person",
		ImagePath:  ids.captureImagePath,
		CapturedAt: capturedAt,
	}); err != nil {
		t.Fatalf("seed capture: %v", err)
	}
	return ids
}

type snapshotFileChecker struct {
	files map[string]bool
	err   error
}

func (c *snapshotFileChecker) SnapshotFileExists(_ context.Context, path string) (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	return c.files[path], nil
}

func assertRecordRows(t *testing.T, store *record.Store, ids recordIDs, wantRec, wantAlarm, wantCapture bool) {
	t.Helper()
	ctx := context.Background()

	rec, err := store.GetRecognitionByID(ctx, ids.recID)
	if err != nil {
		t.Fatalf("get recognition: %v", err)
	}
	alarm, err := store.GetAlarmByID(ctx, ids.alarmID)
	if err != nil {
		t.Fatalf("get alarm: %v", err)
	}
	cap, err := store.GetCaptureByID(ctx, ids.capID)
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}

	for name, tc := range map[string]struct{ got, want bool }{
		"recognition": {rec != nil, wantRec},
		"alarm":       {alarm != nil, wantAlarm},
		"capture":     {cap != nil, wantCapture},
	} {
		if tc.got != tc.want {
			t.Errorf("%s row existence = %v, want %v", name, tc.got, tc.want)
		}
	}
}

func ordinaryRowsGone(t *testing.T, store *record.Store, ids recordIDs) bool {
	t.Helper()
	ctx := context.Background()

	rec, err := store.GetRecognitionByID(ctx, ids.recID)
	if err != nil {
		t.Fatalf("get recognition: %v", err)
	}
	cap, err := store.GetCaptureByID(ctx, ids.capID)
	if err != nil {
		t.Fatalf("get capture: %v", err)
	}
	return rec == nil && cap == nil
}

func TestRetentionPrunerDeletesRowsOnlyAfterSnapshotFilesAreRemoved(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	now := time.Now().UTC()
	expiredAt := now.Add(-40 * 24 * time.Hour)
	freshAt := now.Add(-1 * time.Hour)

	expired := seedRecordRows(t, store, "cam_01", expiredAt)
	fresh := seedRecordRows(t, store, "cam_02", freshAt)
	checker := &snapshotFileChecker{files: map[string]bool{
		expired.recImagePath:     true,
		expired.recFacePath:      true,
		expired.captureImagePath: true,
		fresh.recImagePath:       true,
		fresh.captureImagePath:   true,
	}}

	pruner := record.NewRetentionPruner(store, zap.NewNop())
	pruner.SetRetentionDaysProvider(&stubRetentionDays{days: 30})
	pruner.SetSnapshotFileChecker(checker)

	first, err := pruner.PruneNow(ctx)
	if err != nil {
		t.Fatalf("PruneNow with existing images: %v", err)
	}
	if first.Total() != 0 || first.Alarms != 0 {
		t.Fatalf("existing image references must be retained and alarms untouched: %+v", first)
	}
	assertRecordRows(t, store, expired, true, true, true)

	// Once storage cleanup removes both regular images, the next reconciliation
	// removes their rows but keeps alarm metadata as high-value evidence.
	checker.files[expired.recImagePath] = false
	checker.files[expired.captureImagePath] = false
	partial, err := pruner.PruneNow(ctx)
	if err != nil {
		t.Fatalf("PruneNow after partial image cleanup: %v", err)
	}
	if partial.Recognitions != 0 || partial.Captures != 1 || partial.Alarms != 0 {
		t.Fatalf("recognition must survive while its face image exists: %+v", partial)
	}
	assertRecordRows(t, store, expired, true, true, false)

	checker.files[expired.recFacePath] = false
	result, err := pruner.PruneNow(ctx)
	if err != nil {
		t.Fatalf("PruneNow after all image cleanup: %v", err)
	}
	if result.Recognitions != 1 || result.Alarms != 0 || result.Captures != 0 || result.Total() != 1 {
		t.Fatalf("unexpected prune counts: %+v", result)
	}
	if result.Cutoff.Before(expiredAt) || result.Cutoff.After(freshAt) {
		t.Fatalf("cutoff %v not between %v and %v", result.Cutoff, expiredAt, freshAt)
	}

	assertRecordRows(t, store, expired, false, true, false)
	assertRecordRows(t, store, fresh, true, true, true)

	second, err := pruner.PruneNow(ctx)
	if err != nil {
		t.Fatalf("second PruneNow: %v", err)
	}
	if second.Total() != 0 {
		t.Fatalf("expected idempotent second sweep, deleted %d rows", second.Total())
	}
}

func TestRetentionPrunerNoOpWithoutPolicy(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	old := seedRecordRows(t, store, "cam_01", time.Now().UTC().Add(-1000*24*time.Hour))

	cases := []struct {
		name     string
		provider record.RetentionDaysProvider
	}{
		{name: "provider not injected", provider: nil},
		{name: "retention disabled (zero days)", provider: &stubRetentionDays{days: 0}},
		{name: "retention disabled (negative days)", provider: &stubRetentionDays{days: -5}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pruner := record.NewRetentionPruner(store, zap.NewNop())
			pruner.SetRetentionDaysProvider(tc.provider)

			result, err := pruner.PruneNow(ctx)
			if err != nil {
				t.Fatalf("PruneNow: %v", err)
			}
			if result.Total() != 0 {
				t.Fatalf("expected no deletion, got %+v", result)
			}
		})
	}

	// 任何一轮「未启用」都不能删证据行。
	rec, err := store.GetRecognitionByID(ctx, old.recID)
	if err != nil {
		t.Fatalf("get recognition: %v", err)
	}
	if rec == nil {
		t.Fatal("recognition row must survive when retention is not enabled")
	}
}

func TestRetentionPrunerPropagatesProviderError(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	old := seedRecordRows(t, store, "cam_01", time.Now().UTC().Add(-1000*24*time.Hour))

	boom := errors.New("storage config unavailable")
	pruner := record.NewRetentionPruner(store, zap.NewNop())
	pruner.SetRetentionDaysProvider(&stubRetentionDays{days: 30, err: boom})

	if _, err := pruner.PruneNow(ctx); !errors.Is(err, boom) {
		t.Fatalf("expected wrapped provider error, got %v", err)
	}

	// 读不到保留期时必须一条都不删（宁可不删，也不能猜天数删证据）。
	rec, err := store.GetRecognitionByID(ctx, old.recID)
	if err != nil {
		t.Fatalf("get recognition: %v", err)
	}
	if rec == nil {
		t.Fatal("recognition row must survive when the retention policy cannot be read")
	}
}

func TestRetentionPrunerDoesNotDeleteRowsWhenSnapshotCheckFails(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	old := seedRecordRows(t, store, "cam_01", time.Now().UTC().Add(-1000*24*time.Hour))

	boom := errors.New("media root unavailable")
	pruner := record.NewRetentionPruner(store, zap.NewNop())
	pruner.SetRetentionDaysProvider(&stubRetentionDays{days: 30})
	pruner.SetSnapshotFileChecker(&snapshotFileChecker{err: boom})
	if _, err := pruner.PruneNow(ctx); !errors.Is(err, boom) {
		t.Fatalf("expected wrapped snapshot-check error, got %v", err)
	}
	assertRecordRows(t, store, old, true, true, true)
}

func TestRetentionPrunerDeletesInBatches(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	const expiredCount = 7
	expiredAt := time.Now().UTC().Add(-48 * time.Hour)
	ids := make([]recordIDs, 0, expiredCount)
	checker := &snapshotFileChecker{files: make(map[string]bool)}
	for i := 0; i < expiredCount; i++ {
		rowIDs := seedRecordRows(t, store, "cam_01", expiredAt.Add(time.Duration(i)*time.Second))
		ids = append(ids, rowIDs)
	}

	// 批大小 2 小于待删行数：必须循环多轮才能删完，验证「分批」真的在分批。
	pruner := record.NewRetentionPruner(store, zap.NewNop(), record.RetentionOptions{BatchSize: 2})
	pruner.SetRetentionDaysProvider(&stubRetentionDays{days: 1})
	pruner.SetSnapshotFileChecker(checker)

	result, err := pruner.PruneNow(ctx)
	if err != nil {
		t.Fatalf("PruneNow: %v", err)
	}
	if result.Recognitions != expiredCount || result.Captures != expiredCount {
		t.Fatalf("expected %d recognitions and captures deleted, got %+v", expiredCount, result)
	}

	for _, rowIDs := range ids {
		assertRecordRows(t, store, rowIDs, false, true, false)
	}
}

func TestRetentionPrunerBackgroundLoopStops(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	expired := seedRecordRows(t, store, "cam_01", time.Now().UTC().Add(-48*time.Hour))

	provider := &stubRetentionDays{days: 1}
	pruner := record.NewRetentionPruner(store, zap.NewNop(), record.RetentionOptions{
		Interval:     5 * time.Millisecond,
		InitialDelay: time.Nanosecond,
		BatchSize:    100,
	})
	pruner.SetRetentionDaysProvider(provider)
	pruner.SetSnapshotFileChecker(&snapshotFileChecker{files: make(map[string]bool)})

	pruner.Start(context.Background())

	// 过期图片已不存在时，后台巡检回收普通记录，但不碰告警元数据。
	deadline := time.Now().Add(3 * time.Second)
	for {
		if ordinaryRowsGone(t, store, expired) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background retention loop did not prune expired rows in time")
		}
		time.Sleep(5 * time.Millisecond)
	}

	pruner.Stop()
	callsAfterStop := provider.calls.Load()
	pruner.Stop()
	time.Sleep(20 * time.Millisecond)
	if got := provider.calls.Load(); got != callsAfterStop {
		t.Fatalf("provider called %d more times after Stop", got-callsAfterStop)
	}

	assertRecordRows(t, store, expired, false, true, false)
}

func TestRetentionPrunerStopIsSafeWithoutStart(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()

	pruner := record.NewRetentionPruner(store, zap.NewNop())
	// 不 Start 直接 Stop 不得 panic / 死锁（Fx 启动失败回滚路径会走到这里）。
	pruner.Stop()
	pruner.Stop()
}
