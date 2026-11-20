package record_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

// searchFixture 构造一条带人员快照的识别记录；快照不依赖 persons 行，
// 这样档案被删除后仍能按姓名/工号检索。
func searchFixture(id, personName, personExternalID string) *record.Recognition {
	rec := recognitionDedupFixture(
		id,
		"cam_01",
		"",
		0,
		strPtr("algo_01"),
		personName != "",
		time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC),
	)
	rec.PersonName = personName
	rec.PersonExternalID = personExternalID
	return rec
}

func TestListRecognitionsSearchBySnapshot(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	createDedupFixtures(t, store,
		searchFixture("rec_search_alice", "Alice", "EMP-001"),
		searchFixture("rec_search_percent", "100% Match", "EMP-002"),
		searchFixture("rec_search_underscore", "a_b", ""),
		searchFixture("rec_search_bob", "Bob", ""),
	)

	cases := []struct {
		name    string
		search  string
		wantIDs []string
	}{
		{
			name:    "name substring is case-insensitive for ASCII",
			search:  "ali",
			wantIDs: []string{"rec_search_alice"},
		},
		{
			name:    "employee id substring",
			search:  "emp-00",
			wantIDs: []string{"rec_search_alice", "rec_search_percent"},
		},
		{
			name:    "percent is literal, not a wildcard",
			search:  "%",
			wantIDs: []string{"rec_search_percent"},
		},
		{
			name:    "underscore is literal, not a wildcard",
			search:  "a_b",
			wantIDs: []string{"rec_search_underscore"},
		},
		{
			name:    "empty search returns all",
			search:  "",
			wantIDs: []string{"rec_search_alice", "rec_search_bob", "rec_search_percent", "rec_search_underscore"},
		},
		{
			name:    "no match",
			search:  "nobody",
			wantIDs: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := store.ListRecognitions(ctx, record.RecognitionFilter{
				Search: tc.search,
				Window: pageWindow(1, 20),
			})
			if err != nil {
				t.Fatalf("ListRecognitions: %v", err)
			}
			if total != int64(len(tc.wantIDs)) {
				t.Fatalf("total = %d, want %d (items %v)", total, len(tc.wantIDs), recognitionIDs(items))
			}
			got := recognitionIDs(items)
			want := slices.Clone(tc.wantIDs)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("ids = %v, want %v", got, want)
			}
		})
	}
}

func TestHandlerListRecognitionsAcceptsSearchParam(t *testing.T) {
	router, _, store, cleanup := newRecordHandlerHarness(t)
	defer cleanup()

	if err := store.CreateRecognition(context.Background(),
		searchFixture("rec_handler_search", "Alice", "EMP-001"),
	); err != nil {
		t.Fatalf("CreateRecognition: %v", err)
	}

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/v1/records/recognitions?search=EMP-001", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Code string `json:"code"`
		Data struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "OK" || resp.Data.Total != 1 {
		t.Fatalf("code=%q total=%d, want OK/1: %s", resp.Code, resp.Data.Total, w.Body.String())
	}
}

func TestListRecognitionsQueryTrimsSearch(t *testing.T) {
	filter, err := (&record.ListRecognitionsQuery{Search: "  ali  "}).ToFilter()
	if err != nil {
		t.Fatalf("ToFilter: %v", err)
	}
	if filter.Search != "ali" {
		t.Fatalf("Search = %q, want %q", filter.Search, "ali")
	}
}
