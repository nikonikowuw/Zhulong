package httputil

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// PaginationQuery provides standardized pagination query parameter binding and normalization.
type PaginationQuery struct {
	Page     int `form:"page"`
	PageSize int `form:"pageSize"`
	Limit    int `form:"limit"`
	Offset   int `form:"offset"`
}

// Normalize sanitizes and resolves pagination parameters, ensuring safe defaults and bounds.
// - page: 1-based current page index (minimum 1)
// - pageSize: number of items per page (default 20, max 100)
// - offset: 0-based database query offset ((page - 1) * pageSize)
// - limit: database query limit (equal to pageSize)
func (q *PaginationQuery) Normalize() (page, pageSize, offset, limit int) {
	pageSize = q.PageSize
	if pageSize <= 0 {
		if q.Limit > 0 {
			pageSize = q.Limit
		} else {
			pageSize = 20
		}
	}
	if pageSize > 100 {
		pageSize = 100
	}

	page = q.Page
	if page <= 0 {
		if q.Limit > 0 && q.Offset >= 0 {
			page = (q.Offset / pageSize) + 1
		} else {
			page = 1
		}
	}

	offset = (page - 1) * pageSize
	limit = pageSize
	return page, pageSize, offset, limit
}

// PaginatedData represents standard paginated response payload.
type PaginatedData[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	// Backwards compatibility fields for legacy clients expecting limit/offset
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// PaginatedSuccess writes a standard HTTP 200 paginated response envelope.
// Ensures items is never null (falls back to empty slice) and contains pagination metadata.
func PaginatedSuccess[T any](c *gin.Context, items []T, total int64, page, pageSize int) {
	if items == nil {
		items = []T{}
	}
	offset := (page - 1) * pageSize
	data := PaginatedData[T]{
		Items:    items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Limit:    pageSize,
		Offset:   offset,
	}
	SuccessWithStatus(c, http.StatusOK, data)
}
