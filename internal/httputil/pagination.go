package httputil

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// 分页参数边界。它们属于 HTTP 契约的一部分（超限被 clamp 而不是报错），
// 所以集中从这里导出，各领域不再各自派生一份。
const (
	// DefaultPageSize 是未指定每页行数时的取值。
	DefaultPageSize = 20
	// MaxPageSize 是每页行数上限。
	MaxPageSize = 100
	// MaxPage 是页码上限。没有它，(page-1)*pageSize 会在 int64 上溢出成负数，
	// 而 SQLite 把负 OFFSET 当 0 处理 —— 一次越界请求会静默返回第一页而不是空页。
	MaxPage = 100000
)

// PaginationQuery provides standardized pagination query parameter binding and normalization.
type PaginationQuery struct {
	Page     int `form:"page"`
	PageSize int `form:"pageSize"`
	Limit    int `form:"limit"`
	Offset   int `form:"offset"`
}

// PageWindow is the resolved pagination window: what to query, and what to echo back.
//
// Page 与 Offset 由 Normalize 一并算出并始终自洽，因此响应不可能描述一个
// 与实际读取的行区间不同的窗口。
type PageWindow struct {
	Page     int
	PageSize int
	Offset   int
}

// Normalize resolves the pagination parameters into a query window.
//
// page/pageSize 是主契约；limit/offset 仍被接受，作为 legacy 客户端的兼容写法。
// 两个分支的 offset 语义不同，这一点是刻意的：
//
//   - 给了 page：offset = (page-1)*pageSize，窗口完全由页码决定。
//   - 只给了 offset：offset 原样采用，绝不反推页码再重算 ——
//     `limit=20&offset=30` 反推会得到第 2 页、offset=20，客户端拿到的就是
//     第 21–40 条而不是它请求的第 31–50 条，且响应里看起来一切正常。
func (q *PaginationQuery) Normalize() PageWindow {
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = q.Limit
	}
	if pageSize <= 0 {
		pageSize = DefaultPageSize
	}
	if pageSize > MaxPageSize {
		pageSize = MaxPageSize
	}

	if q.Page <= 0 && q.Offset > 0 {
		return PageWindow{
			// Page 只是该偏移落点的描述性取值，权威字段是 Offset。
			Page:     q.Offset/pageSize + 1,
			PageSize: pageSize,
			Offset:   q.Offset,
		}
	}

	page := q.Page
	if page < 1 {
		page = 1
	}
	if page > MaxPage {
		page = MaxPage
	}
	return PageWindow{Page: page, PageSize: pageSize, Offset: (page - 1) * pageSize}
}

// PaginatedData represents standard paginated response payload.
type PaginatedData[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
	// Legacy 客户端按 limit/offset 读取。两个字段恒出现（不做 omitempty）：
	// offset=0 时字段消失会让「第一页」与「客户端没传 offset」不可区分。
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// PaginatedSuccess writes a standard HTTP 200 paginated response envelope.
// Ensures items is never null and echoes back the exact window that was queried.
func PaginatedSuccess[T any](c *gin.Context, items []T, total int64, window PageWindow) {
	if items == nil {
		items = []T{}
	}
	data := PaginatedData[T]{
		Items:    items,
		Total:    total,
		Page:     window.Page,
		PageSize: window.PageSize,
		Limit:    window.PageSize,
		Offset:   window.Offset,
	}
	SuccessWithStatus(c, http.StatusOK, data)
}
