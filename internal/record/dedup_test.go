package record_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

func pageWindow(page, pageSize int) httputil.PageWindow {
	return httputil.PageWindow{Page: page, PageSize: pageSize, Offset: (page - 1) * pageSize}
}

// recognitionDedupFixture 创建一条识别记录；identity 为空表示未匹配。
func recognitionDedupFixture(
	id, cameraID, personID string,
	trackID int64,
	algorithmID *string,
	matched bool,
	capturedAt time.Time,
) *record.Recognition {
	rec := &record.Recognition{
		ID:             id,
		CameraID:       cameraID,
		AlgorithmID:    algorithmID,
		PTS:            capturedAt.UnixMilli(),
		TrackID:        trackID,
		Matched:        matched,
		CandidatesJSON: "[]",
		BoxJSON:        "[]",
		BodyBoxJSON:    "[]",
		AttributesJSON: "{}",
		CapturedAt:     capturedAt,
	}
	if personID != "" {
		rec.PersonID = strPtr(personID)
		rec.PersonName = "Alice"
		rec.MatchedFaceID = "face_alice_01"
		rec.Similarity = 0.95
	}
	return rec
}

func createDedupFixtures(t *testing.T, store *record.Store, recs ...*record.Recognition) {
	t.Helper()
	if err := store.CreateRecognitionBatch(context.Background(), recs); err != nil {
		t.Fatalf("seed recognitions: %v", err)
	}
}

func recognitionIDs(items []record.Recognition) []string {
	ids := make([]string, len(items))
	for i, item := range items {
		ids[i] = item.ID
	}
	return ids
}

// TestListRecognitionsDedupCollapsesAcrossTimeGaps 守住去重的核心语义：
// 当前筛选结果里同一个「相机 + 人员」只保留最新一条，即使中间隔了很久；
// 不同相机互不影响，普通列表完全不变。
func TestListRecognitionsDedupCollapsesAcrossTimeGaps(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	createDedupFixtures(t, store,
		recognitionDedupFixture("a1", "cam_01", "person_alice", 1, strPtr("algo_01"), true, base),
		recognitionDedupFixture("a2", "cam_01", "person_alice", 1, strPtr("algo_01"), true, base.Add(3*time.Second)),
		recognitionDedupFixture("a3", "cam_01", "person_alice", 2, strPtr("algo_01"), true, base.Add(6*time.Second)),
		recognitionDedupFixture("a4", "cam_01", "person_alice", 2, strPtr("algo_01"), true, base.Add(time.Hour)),
		recognitionDedupFixture("b1", "cam_02", "person_alice", 1, strPtr("algo_01"), true, base.Add(3*time.Second)),
	)

	all, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{})
	if err != nil {
		t.Fatalf("ListRecognitions: %v", err)
	}
	if total != 5 || len(all) != 5 {
		t.Fatalf("plain list total=%d len=%d, want 5/5", total, len(all))
	}

	deduped, dedupedTotal, err := store.ListRecognitions(ctx, record.RecognitionFilter{Dedup: true})
	if err != nil {
		t.Fatalf("ListRecognitions dedup: %v", err)
	}
	if dedupedTotal != 2 || len(deduped) != 2 {
		t.Fatalf("dedup total=%d len=%d, want 2/2", dedupedTotal, len(deduped))
	}
	wantIDs := []string{"a4", "b1"}
	if got := recognitionIDs(deduped); !slices.Equal(got, wantIDs) {
		t.Errorf("dedup ids = %v, want %v", got, wantIDs)
	}
}

// TestListRecognitionsDedupKeepsTrackNamespacesAndUntrackedRows 守住未匹配场景：
// track_id 只在「相机 + 算法」命名空间内相同才算同一人，且整个查询范围内只保留
// 最新一条；track_id 为 0 的记录没有可靠身份，必须各自保留，不能把不同陌生人
// 折叠掉。
func TestListRecognitionsDedupKeepsTrackNamespacesAndUntrackedRows(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Date(2026, 11, 8, 11, 0, 0, 0, time.UTC)
	createDedupFixtures(t, store,
		recognitionDedupFixture("u1", "cam_01", "", 7, strPtr("algo_01"), false, base),
		recognitionDedupFixture("u2", "cam_01", "", 7, strPtr("algo_01"), false, base.Add(3*time.Second)),
		recognitionDedupFixture("u3", "cam_01", "", 7, strPtr("algo_01"), false, base.Add(9*time.Second)),
		// NULL 算法是另一个 track 命名空间，不能与 algo_01 的 track 7 合并。
		recognitionDedupFixture("u4", "cam_01", "", 7, nil, false, base.Add(3*time.Second)),
		recognitionDedupFixture("u5", "cam_01", "", 0, strPtr("algo_01"), false, base.Add(2*time.Second)),
		recognitionDedupFixture("u6", "cam_01", "", 0, strPtr("algo_01"), false, base.Add(3*time.Second)),
	)

	items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{Dedup: true})
	if err != nil {
		t.Fatalf("ListRecognitions dedup: %v", err)
	}
	if total != 4 || len(items) != 4 {
		t.Fatalf("dedup total=%d len=%d, want 4/4 (ids=%v)", total, len(items), recognitionIDs(items))
	}

	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.ID] = true
	}
	for _, want := range []string{"u3", "u4", "u5", "u6"} {
		if !seen[want] {
			t.Errorf("dedup result missing representative %s (got %v)", want, recognitionIDs(items))
		}
	}
	for _, folded := range []string{"u1", "u2"} {
		if seen[folded] {
			t.Errorf("%s should be folded into u3", folded)
		}
	}
}

// TestListRecognitionsDedupPaginatesGroupsNotRows 守住分页：total 是去重后的
// 条数，offset 作用在代表行上，且列表查询仍然不携带 feature_vector。
func TestListRecognitionsDedupPaginatesGroupsNotRows(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	recs := make([]*record.Recognition, 0, 30)
	for i := 0; i < 10; i++ {
		for j := 0; j < 3; j++ {
			rec := recognitionDedupFixture(
				fmt.Sprintf("g%02d_%d", i, j),
				"cam_01",
				"",
				int64(100+i),
				strPtr("algo_01"),
				false,
				base.Add(time.Duration(i)*time.Minute+time.Duration(j)*2*time.Second),
			)
			if i == 9 && j == 2 {
				rec.FeatureVector = []byte{1, 2, 3}
			}
			recs = append(recs, rec)
		}
	}
	createDedupFixtures(t, store, recs...)

	page1, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{
		Dedup:  true,
		Window: pageWindow(1, 3),
	})
	if err != nil {
		t.Fatalf("ListRecognitions page 1: %v", err)
	}
	if total != 10 || len(page1) != 3 {
		t.Fatalf("page 1 total=%d len=%d, want 10/3", total, len(page1))
	}
	if got, want := recognitionIDs(page1), []string{"g09_2", "g08_2", "g07_2"}; !slices.Equal(got, want) {
		t.Errorf("page 1 ids = %v, want %v", got, want)
	}
	for _, item := range page1 {
		if len(item.FeatureVector) != 0 {
			t.Errorf("dedup list leaked feature_vector for %s", item.ID)
		}
	}

	page2, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{
		Dedup:  true,
		Window: pageWindow(3, 3),
	})
	if err != nil {
		t.Fatalf("ListRecognitions page 3: %v", err)
	}
	if total != 10 || len(page2) != 3 {
		t.Fatalf("page 3 total=%d len=%d, want 10/3", total, len(page2))
	}
	if got, want := recognitionIDs(page2), []string{"g03_2", "g02_2", "g01_2"}; !slices.Equal(got, want) {
		t.Errorf("page 3 ids = %v, want %v", got, want)
	}
}

// TestListRecognitionsDedupAppliesFiltersBeforeGrouping 守住「先筛选、后折叠」：
// 时间范围截断后，窗口内剩下的记录仍按同一套规则归组，total 与内容一致。
func TestListRecognitionsDedupAppliesFiltersBeforeGrouping(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	base := time.Date(2026, 11, 8, 13, 0, 0, 0, time.UTC)
	createDedupFixtures(t, store,
		recognitionDedupFixture("f1", "cam_01", "person_alice", 1, strPtr("algo_01"), true, base),
		recognitionDedupFixture("f2", "cam_01", "person_alice", 1, strPtr("algo_01"), true, base.Add(2*time.Second)),
		recognitionDedupFixture("f3", "cam_02", "person_alice", 1, strPtr("algo_01"), true, base.Add(2*time.Second)),
	)

	start := base.Add(1 * time.Second)
	end := base.Add(3 * time.Second)
	items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{
		CameraID:  "cam_01",
		StartTime: &start,
		EndTime:   &end,
		Dedup:     true,
	})
	if err != nil {
		t.Fatalf("ListRecognitions dedup with filters: %v", err)
	}
	// f1 落在时间范围外；f2 在范围内且只有这一条，单独成组。
	if total != 1 || len(items) != 1 || items[0].ID != "f2" {
		t.Fatalf("filtered dedup total=%d ids=%v, want 1/[f2]", total, recognitionIDs(items))
	}
}
