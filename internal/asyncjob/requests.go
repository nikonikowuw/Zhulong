package asyncjob

import "github.com/nikonikowuw/Zhulong/internal/httputil"

// ListJobsQuery represents the query parameters for filtering and paginating background jobs.
type ListJobsQuery struct {
	httputil.PaginationQuery
	Type string `form:"type"`
}
