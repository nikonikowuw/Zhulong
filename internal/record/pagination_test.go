package record_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/record"
)

// seedRecognitions 写入 n 条识别记录，PTS 递增代表时间先后（越大越新）。
// 分页断言用 PTS 定位行，比用 ID 更直观。
func seedRecognitions(t *testing.T, store *record.Store, n int) {
	t.Helper()
	base := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	recs := make([]*record.Recognition, 0, n)
	for i := 0; i < n; i++ {
		recs = append(recs, &record.Recognition{
			ID:             idutil.New(),
			CameraID:       "cam_01",
			AlgorithmID:    strPtr("algo_01"),
			PTS:            int64(i),
			CandidatesJSON: "[]",
			BoxJSON:        "[]",
			BodyBoxJSON:    "[]",
			AttributesJSON: "{}",
			CapturedAt:     base.Add(time.Duration(i) * time.Second),
		})
	}
	if err := store.CreateRecognitionBatch(context.Background(), recs); err != nil {
		t.Fatalf("seed recognitions: %v", err)
	}
}

// TestListPaginationHonoursUnalignedOffset 守住 legacy limit/offset 不被取整。
//
// 归一化曾把 `limit=20&offset=30` 反推成第 2 页再重算 offset=20，
// 客户端请求第 31–50 条却拿到第 21–40 条，且响应里看不出任何异常。
func TestListPaginationHonoursUnalignedOffset(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedRecognitions(t, store, 50)

	tests := []struct {
		name      string
		window    httputil.PageWindow
		wantTotal int64
		wantFirst int64 // 期望首行 PTS
		wantLast  int64 // 期望末行 PTS
	}{
		{
			name:      "legacy unaligned offset is not rounded down",
			window:    httputil.PageWindow{Page: 2, PageSize: 20, Offset: 30},
			wantTotal: 50,
			wantFirst: 19, // 第 31 行（captured_at DESC）
			wantLast:  0,
		},
		{
			name:      "page path still derives offset from the page number",
			window:    httputil.PageWindow{Page: 2, PageSize: 20, Offset: 20},
			wantTotal: 50,
			wantFirst: 29,
			wantLast:  10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{Window: tt.window})
			if err != nil {
				t.Fatalf("ListRecognitions: %v", err)
			}
			if total != tt.wantTotal {
				t.Fatalf("total = %d, want %d", total, tt.wantTotal)
			}
			if len(items) != tt.window.PageSize {
				t.Fatalf("len(items) = %d, want %d", len(items), tt.window.PageSize)
			}
			if items[0].PTS != tt.wantFirst {
				t.Errorf("first row PTS = %d, want %d", items[0].PTS, tt.wantFirst)
			}
			if items[len(items)-1].PTS != tt.wantLast {
				t.Errorf("last row PTS = %d, want %d", items[len(items)-1].PTS, tt.wantLast)
			}
		})
	}
}

// TestListPaginationDefendsAgainstZeroWindow 守住 GORM 的 Limit(0) 语义。
//
// GORM 的 Limit(0) 表示**不加 LIMIT**，一个漏填 window 的内部调用者会把整表
// 读进内存。store 的零值防御必须挡住它。
func TestListPaginationDefendsAgainstZeroWindow(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedRecognitions(t, store, 25)

	items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{})
	if err != nil {
		t.Fatalf("ListRecognitions: %v", err)
	}
	if total != 25 {
		t.Fatalf("total = %d, want 25", total)
	}
	if len(items) != httputil.DefaultPageSize {
		t.Fatalf("len(items) = %d, want the default page size %d", len(items), httputil.DefaultPageSize)
	}
}

// TestListPaginationKeepsCountAndPageConsistent 守住 count 与分页数据用同一组 WHERE。
//
// 两条语句在同一个只读事务里执行，且各自持有独立 Statement：条件漏带或重复
// 都会让 total 与本页内容对不上，而单看任何一边都是合法响应。
func TestListPaginationKeepsCountAndPageConsistent(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedRecognitions(t, store, 50)
	// 再混入 5 条另一台相机的记录，过滤条件必须把它们排除在 total 之外。
	base := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	other := make([]*record.Recognition, 0, 5)
	for i := 0; i < 5; i++ {
		other = append(other, &record.Recognition{
			ID:             idutil.New(),
			CameraID:       "cam_02",
			AlgorithmID:    strPtr("algo_01"),
			PTS:            int64(1000 + i),
			CandidatesJSON: "[]",
			BoxJSON:        "[]",
			BodyBoxJSON:    "[]",
			AttributesJSON: "{}",
			CapturedAt:     base.Add(time.Duration(i) * time.Second),
		})
	}
	if err := store.CreateRecognitionBatch(ctx, other); err != nil {
		t.Fatalf("seed other camera: %v", err)
	}

	items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{
		CameraID: "cam_01",
		Window:   httputil.PageWindow{Page: 2, PageSize: 20, Offset: 20},
	})
	if err != nil {
		t.Fatalf("ListRecognitions: %v", err)
	}
	if total != 50 {
		t.Fatalf("total = %d, want 50 (cam_02 rows must not count)", total)
	}
	if len(items) != 20 {
		t.Fatalf("len(items) = %d, want 20", len(items))
	}
	for _, it := range items {
		if it.CameraID != "cam_01" {
			t.Fatalf("page contains a row from %s, filter was dropped", it.CameraID)
		}
	}

	// 越界页必须是空页 + 正确的 total，而不是报错或回落到第一页。
	empty, emptyTotal, err := store.ListRecognitions(ctx, record.RecognitionFilter{
		CameraID: "cam_01",
		Window:   httputil.PageWindow{Page: 9, PageSize: 20, Offset: 160},
	})
	if err != nil {
		t.Fatalf("ListRecognitions out of range: %v", err)
	}
	if emptyTotal != 50 || len(empty) != 0 {
		t.Fatalf("out-of-range page = %d rows / total %d, want 0 / 50", len(empty), emptyTotal)
	}

	var count int64
	if err := dbStore.DB().Table("recognitions").Count(&count).Error; err != nil {
		t.Fatalf("count all: %v", err)
	}
	if count != 55 {
		t.Fatalf("expected 55 seeded rows, got %d", count)
	}
}

// TestListQueryPlanUsesOrderedIndex 锁定 000021 的复合索引。
//
// 没有 (captured_at DESC, id DESC) 复合索引时，SQLite 会对每一页做
// "USE TEMP B-TREE FOR LAST TERM OF ORDER BY"；带 camera_id 过滤时更退化成
// 全表扫描 + 全量排序。深翻页在边缘设备上的成本就来自这里。
func TestListQueryPlanUsesOrderedIndex(t *testing.T) {
	_, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	db := dbStore.DB()

	cases := []struct{ name, sql string }{
		{
			name: "recognitions by captured_at",
			sql:  "SELECT * FROM recognitions ORDER BY captured_at DESC, id DESC LIMIT 20 OFFSET 40",
		},
		{
			name: "recognitions by camera",
			sql:  "SELECT * FROM recognitions WHERE camera_id = 'cam_01' ORDER BY captured_at DESC, id DESC LIMIT 20",
		},
		{
			name: "alarms by captured_at",
			sql:  "SELECT * FROM alarms ORDER BY captured_at DESC, id DESC LIMIT 20 OFFSET 40",
		},
		{
			name: "alarms by camera",
			sql:  "SELECT * FROM alarms WHERE camera_id = 'cam_01' ORDER BY captured_at DESC, id DESC LIMIT 20",
		},
		{
			name: "captures by captured_at",
			sql:  "SELECT * FROM captures ORDER BY captured_at DESC, id DESC LIMIT 20 OFFSET 40",
		},
		{
			name: "captures by camera",
			sql:  "SELECT * FROM captures WHERE camera_id = 'cam_01' ORDER BY captured_at DESC, id DESC LIMIT 20",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rows []struct {
				Detail string
			}
			if err := db.Raw("EXPLAIN QUERY PLAN " + tc.sql).Scan(&rows).Error; err != nil {
				t.Fatalf("explain query plan: %v", err)
			}
			if len(rows) == 0 {
				t.Fatal("empty query plan")
			}

			plan := make([]string, 0, len(rows))
			for _, r := range rows {
				plan = append(plan, r.Detail)
				if strings.Contains(strings.ToUpper(r.Detail), "TEMP B-TREE") {
					t.Fatalf("query plan sorts in a temporary b-tree: %v", plan)
				}
			}
			t.Logf("plan: %v", plan)
		})
	}
}

// TestMigration021IndexesExist 保证新索引真的建出来了（改坏迁移会静默退回慢计划）。
func TestMigration021IndexesExist(t *testing.T) {
	_, dbStore, cleanup := setupTestStore(t)
	defer cleanup()

	want := []string{
		"idx_recognitions_captured_at_id",
		"idx_recognitions_camera_captured_id",
		"idx_alarms_captured_at_id",
		"idx_alarms_camera_captured_id",
		"idx_captures_captured_at_id",
		"idx_captures_camera_captured_id",
	}
	for _, name := range want {
		var count int64
		if err := dbStore.DB().
			Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).
			Scan(&count).Error; err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if count != 1 {
			t.Errorf("index %s is missing", name)
		}
	}

	// 旧单列索引已被复合索引覆盖，继续存在只增加写入放大。
	gone := []string{
		"idx_recognitions_captured_at",
		"idx_recognitions_camera_captured",
		"idx_alarms_captured_at",
		"idx_alarms_camera_captured",
		"idx_captures_captured_at",
		"idx_captures_camera_captured",
	}
	for _, name := range gone {
		var count int64
		if err := dbStore.DB().
			Raw("SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).
			Scan(&count).Error; err != nil {
			t.Fatalf("query sqlite_master for %s: %v", name, err)
		}
		if count != 0 {
			t.Errorf("legacy index %s should have been dropped", name)
		}
	}
}
