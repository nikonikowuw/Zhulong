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
// normalized page, normalized pageSize, and any validation error.
func (q *ListLogsQuery) ToFilter() (audit.Filter, int, int, error) {
	page, pageSize, offset, limit := q.Normalize()

	filter := audit.Filter{
		Action:  q.Action,
		Actions: q.Actions,
		Status:  q.Status,
		Limit:   limit,
		Offset:  offset,
	}

	if q.StartTime != "" {
		t, err := time.Parse(time.RFC3339, q.StartTime)
		if err != nil {
			return filter, page, pageSize, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: "startTime", Code: "INVALID_FORMAT", Message: "startTime must be in RFC3339 format"},
			})
		}
		filter.StartTime = &t
	}

	if q.EndTime != "" {
		t, err := time.Parse(time.RFC3339, q.EndTime)
		if err != nil {
			return filter, page, pageSize, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: "endTime", Code: "INVALID_FORMAT", Message: "endTime must be in RFC3339 format"},
			})
		}
		filter.EndTime = &t
	}

	return filter, page, pageSize, nil
}

// ClearLogsQuery defines query parameters for deleting audit logs.
type ClearLogsQuery struct {
	Before string `form:"before"`
}

// ParseBefore parses the optional before RFC3339 timestamp.
func (q *ClearLogsQuery) ParseBefore() ([]time.Time, error) {
	if q.Before == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, q.Before)
	if err != nil {
		return nil, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
			{Field: "before", Code: "INVALID_FORMAT", Message: "before must be in RFC3339 format"},
		})
	}
	return []time.Time{t}, nil
}
