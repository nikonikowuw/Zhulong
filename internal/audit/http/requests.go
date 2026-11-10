package audithttp

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// ListLogsQuery defines query parameters for listing audit logs.
type ListLogsQuery struct {
	httputil.PaginationQuery
	Action    string   `form:"action"`
	Actions   []string `form:"actions"`
	Status    string   `form:"status"`
	StartTime string   `form:"startTime"`
	EndTime   string   `form:"endTime"`
}

// ToFilter converts query parameters into a validated audit.Filter, returning the filter,
// the pagination window that was resolved, and any validation error.
func (q *ListLogsQuery) ToFilter() (audit.Filter, httputil.PageWindow, error) {
	window := q.Normalize()

	filter := audit.Filter{
		Action:  q.Action,
		Actions: q.Actions,
		Status:  q.Status,
		Limit:   window.PageSize,
		Offset:  window.Offset,
	}

	startTime, err := parseTimeParam("startTime", q.StartTime)
	if err != nil {
		return filter, window, err
	}
	filter.StartTime = startTime

	endTime, err := parseTimeParam("endTime", q.EndTime)
	if err != nil {
		return filter, window, err
	}
	filter.EndTime = endTime

	return filter, window, nil
}

// ClearLogsQuery defines query parameters for deleting audit logs.
type ClearLogsQuery struct {
	Before string `form:"before"`
}

// ParseBefore parses the optional before RFC3339 timestamp. A nil result means
// every prunable record matches.
func (q *ClearLogsQuery) ParseBefore() (*time.Time, error) {
	return parseTimeParam("before", q.Before)
}

// parseTimeParam parses an optional RFC3339 query parameter. It returns a nil
// time without an error for an absent value, which callers read as "no bound",
// and a validation error naming field when the value is malformed.
func parseTimeParam(field, value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
			{Field: field, Code: "INVALID_FORMAT", Message: field + " must be in RFC3339 format"},
		})
	}
	return &t, nil
}
