package httputil

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestPaginationQueryNormalize(t *testing.T) {
	tests := []struct {
		name   string
		input  PaginationQuery
		want   PageWindow
		reason string
	}{
		{
			name:  "empty default",
			input: PaginationQuery{},
			want:  PageWindow{Page: 1, PageSize: 20, Offset: 0},
		},
		{
			name:  "negative inputs normalized to default",
			input: PaginationQuery{Page: -3, PageSize: -10},
			want:  PageWindow{Page: 1, PageSize: 20, Offset: 0},
		},
		{
			name:  "clamp pageSize to max 100",
			input: PaginationQuery{Page: 2, PageSize: 500},
			want:  PageWindow{Page: 2, PageSize: 100, Offset: 100},
		},
		{
			name:  "valid page and pageSize",
			input: PaginationQuery{Page: 3, PageSize: 15},
			want:  PageWindow{Page: 3, PageSize: 15, Offset: 30},
		},
		{
			name:  "legacy limit and aligned offset",
			input: PaginationQuery{Limit: 10, Offset: 30},
			want:  PageWindow{Page: 4, PageSize: 10, Offset: 30},
		},
		{
			// 反推页码再重算会得到 offset=20，把客户端请求的第 31–50 条换成第 21–40 条。
			name:   "legacy unaligned offset is passed through verbatim",
			input:  PaginationQuery{Limit: 20, Offset: 30},
			want:   PageWindow{Page: 2, PageSize: 20, Offset: 30},
			reason: "offset 不是 pageSize 整数倍时必须原样使用",
		},
		{
			name:  "legacy offset without limit uses default pageSize",
			input: PaginationQuery{Offset: 45},
			want:  PageWindow{Page: 3, PageSize: 20, Offset: 45},
		},
		{
			name:  "page and pageSize take precedence over limit and offset",
			input: PaginationQuery{Page: 2, PageSize: 25, Limit: 10, Offset: 50},
			want:  PageWindow{Page: 2, PageSize: 25, Offset: 25},
		},
		{
			// 没有上限时 (page-1)*pageSize 会溢出成负数，SQLite 把负 OFFSET 当 0，
			// 越界请求会静默返回第一页。
			name:  "huge page is clamped instead of overflowing the offset",
			input: PaginationQuery{Page: 922337203685477581, PageSize: 100},
			want:  PageWindow{Page: MaxPage, PageSize: 100, Offset: (MaxPage - 1) * 100},
		},
		{
			name:  "zero page with zero offset is the first page",
			input: PaginationQuery{Page: 0, Offset: 0, Limit: 5},
			want:  PageWindow{Page: 1, PageSize: 5, Offset: 0},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.input.Normalize()
			if got != tt.want {
				reason := tt.reason
				if reason == "" {
					reason = "unexpected window"
				}
				t.Errorf("%s: Normalize() = %+v, want %+v", reason, got, tt.want)
			}
		})
	}
}

// TestNormalizeWindowStaysConsistent 守住「响应描述的窗口 = 实际查询的窗口」这条不变量。
func TestNormalizeWindowStaysConsistent(t *testing.T) {
	inputs := []PaginationQuery{
		{},
		{Page: 1, PageSize: 20},
		{Page: 7, PageSize: 50},
		{Limit: 20, Offset: 30},
		{Offset: 7},
		{Page: MaxPage + 1},
	}

	for _, in := range inputs {
		w := in.Normalize()
		if w.PageSize <= 0 || w.PageSize > MaxPageSize {
			t.Fatalf("%+v -> pageSize %d out of bounds", in, w.PageSize)
		}
		if w.Offset < 0 {
			t.Fatalf("%+v -> negative offset %d", in, w.Offset)
		}
		// 页码路径必须与 offset 严格互为逆运算；legacy offset 路径只在自洽时检查。
		if in.Page > 0 {
			if w.Offset != (w.Page-1)*w.PageSize {
				t.Fatalf("%+v -> page %d/offset %d inconsistent", in, w.Page, w.Offset)
			}
		}
		if in.Page <= 0 && in.Offset > 0 && w.Offset != in.Offset {
			t.Fatalf("%+v -> legacy offset rewritten to %d", in, w.Offset)
		}
	}
}

func TestPaginatedSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("writes valid envelope and never returns null items", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		var nilItems []string
		PaginatedSuccess(c, nilItems, 0, PageWindow{Page: 1, PageSize: 20, Offset: 0})

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

		var resp struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Data    struct {
				Items    []string `json:"items"`
				Total    int64    `json:"total"`
				Page     int      `json:"page"`
				PageSize int      `json:"pageSize"`
				Limit    *int     `json:"limit"`
				Offset   *int     `json:"offset"`
			} `json:"data"`
		}

		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Code != "OK" {
			t.Errorf("expected code OK, got %s", resp.Code)
		}
		if resp.Data.Items == nil {
			t.Errorf("expected items to be empty array, got nil")
		}
		if len(resp.Data.Items) != 0 {
			t.Errorf("expected 0 items, got %d", len(resp.Data.Items))
		}
		// offset 必须是恒出现的字段：第一页时若消失，客户端无法区分
		// 「offset=0」与「服务端没返回 offset」。
		if resp.Data.Limit == nil || resp.Data.Offset == nil {
			t.Fatalf("limit/offset must always be present, got %s", w.Body.String())
		}
		if resp.Data.Page != 1 || resp.Data.PageSize != 20 || *resp.Data.Limit != 20 || *resp.Data.Offset != 0 {
			t.Errorf("unexpected pagination metadata: %+v", resp.Data)
		}
	})

	t.Run("echoes the queried window verbatim", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		items := []string{"cam-1", "cam-2"}
		// legacy 非对齐 offset：回显必须与查询一致，而不是回显取整后的页码推导值。
		PaginatedSuccess(c, items, 42, PageWindow{Page: 2, PageSize: 20, Offset: 30})

		var resp struct {
			Code string `json:"code"`
			Data struct {
				Items    []string `json:"items"`
				Total    int64    `json:"total"`
				Page     int      `json:"page"`
				PageSize int      `json:"pageSize"`
				Limit    int      `json:"limit"`
				Offset   int      `json:"offset"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("failed to unmarshal response: %v", err)
		}

		if resp.Data.Total != 42 || resp.Data.Page != 2 || resp.Data.PageSize != 20 {
			t.Errorf("unexpected pagination metadata: %+v", resp.Data)
		}
		if resp.Data.Offset != 30 || resp.Data.Limit != 20 {
			t.Errorf("echoed window must match the queried window, got %+v", resp.Data)
		}
		if len(resp.Data.Items) != 2 || resp.Data.Items[0] != "cam-1" {
			t.Errorf("unexpected items content: %+v", resp.Data.Items)
		}
	})
}
