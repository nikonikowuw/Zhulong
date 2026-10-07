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
		name         string
		input        PaginationQuery
		wantPage     int
		wantPageSize int
		wantOffset   int
		wantLimit    int
	}{
		{
			name:         "empty default",
			input:        PaginationQuery{},
			wantPage:     1,
			wantPageSize: 20,
			wantOffset:   0,
			wantLimit:    20,
		},
		{
			name: "negative inputs normalized to default",
			input: PaginationQuery{
				Page:     -3,
				PageSize: -10,
			},
			wantPage:     1,
			wantPageSize: 20,
			wantOffset:   0,
			wantLimit:    20,
		},
		{
			name: "clamp pageSize to max 100",
			input: PaginationQuery{
				Page:     2,
				PageSize: 500,
			},
			wantPage:     2,
			wantPageSize: 100,
			wantOffset:   100,
			wantLimit:    100,
		},
		{
			name: "valid page and pageSize",
			input: PaginationQuery{
				Page:     3,
				PageSize: 15,
			},
			wantPage:     3,
			wantPageSize: 15,
			wantOffset:   30,
			wantLimit:    15,
		},
		{
			name: "legacy limit and offset fallback",
			input: PaginationQuery{
				Limit:  10,
				Offset: 30,
			},
			wantPage:     4,
			wantPageSize: 10,
			wantOffset:   30,
			wantLimit:    10,
		},
		{
			name: "page and pageSize take precedence over limit and offset",
			input: PaginationQuery{
				Page:     2,
				PageSize: 25,
				Limit:    10,
				Offset:   50,
			},
			wantPage:     2,
			wantPageSize: 25,
			wantOffset:   25,
			wantLimit:    25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPage, gotPageSize, gotOffset, gotLimit := tt.input.Normalize()
			if gotPage != tt.wantPage {
				t.Errorf("Normalize() page = %v, want %v", gotPage, tt.wantPage)
			}
			if gotPageSize != tt.wantPageSize {
				t.Errorf("Normalize() pageSize = %v, want %v", gotPageSize, tt.wantPageSize)
			}
			if gotOffset != tt.wantOffset {
				t.Errorf("Normalize() offset = %v, want %v", gotOffset, tt.wantOffset)
			}
			if gotLimit != tt.wantLimit {
				t.Errorf("Normalize() limit = %v, want %v", gotLimit, tt.wantLimit)
			}
		})
	}
}

func TestPaginatedSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("writes valid envelope and never returns null items", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		var nilItems []string
		PaginatedSuccess(c, nilItems, 0, 1, 20)

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
				Limit    int      `json:"limit"`
				Offset   int      `json:"offset"`
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
		if resp.Data.Page != 1 || resp.Data.PageSize != 20 || resp.Data.Limit != 20 || resp.Data.Offset != 0 {
			t.Errorf("unexpected pagination metadata: %+v", resp.Data)
		}
	})

	t.Run("writes populated items correctly", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		items := []string{"cam-1", "cam-2"}
		PaginatedSuccess(c, items, 42, 2, 10)

		if w.Code != http.StatusOK {
			t.Fatalf("expected status 200, got %d", w.Code)
		}

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

		if resp.Data.Total != 42 || resp.Data.Page != 2 || resp.Data.PageSize != 10 || resp.Data.Offset != 10 {
			t.Errorf("unexpected pagination metadata: %+v", resp.Data)
		}
		if len(resp.Data.Items) != 2 || resp.Data.Items[0] != "cam-1" {
			t.Errorf("unexpected items content: %+v", resp.Data.Items)
		}
	})
}
