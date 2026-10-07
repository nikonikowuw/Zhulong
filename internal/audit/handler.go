package audit

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler provides HTTP endpoints for audit log inspection and maintenance.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler constructs an audit HTTP Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger.Named("audit.handler"),
	}
}

// RegisterRoutes registers audit routes onto the given router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/audit")
	{
		group.GET("/logs", h.ListLogs)
		group.DELETE("/logs", h.ClearLogs)
	}
}

// ListLogsQuery defines query parameters for listing audit logs.
type ListLogsQuery struct {
	httputil.PaginationQuery
	Action    string `form:"action"`
	Status    string `form:"status"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// AuditLogDTO represents the external API presentation for an audit log record.
type AuditLogDTO struct {
	ID        int64     `json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	IP        string    `json:"ip"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Target    string    `json:"target"`
	Detail    string    `json:"detail"`
	Status    string    `json:"status"`
	ErrorMsg  string    `json:"errorMsg"`
}

func toAuditLogDTO(l AuditLog) AuditLogDTO {
	return AuditLogDTO{
		ID:        l.ID,
		CreatedAt: l.CreatedAt.UTC(),
		IP:        l.IP,
		Username:  l.Username,
		Action:    l.Action,
		Target:    l.Target,
		Detail:    l.Detail,
		Status:    l.Status,
		ErrorMsg:  l.ErrorMsg,
	}
}

func toAuditLogDTOs(logs []AuditLog) []AuditLogDTO {
	if len(logs) == 0 {
		return []AuditLogDTO{}
	}
	dtos := make([]AuditLogDTO, len(logs))
	for i, l := range logs {
		dtos[i] = toAuditLogDTO(l)
	}
	return dtos
}

// ListLogs godoc
// @Summary      List audit logs with filtering and pagination
// @Tags         audit
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        action query string false "Filter by action"
// @Param        status query string false "Filter by status (success/failed)"
// @Param        startTime query string false "Start time (RFC3339)"
// @Param        endTime query string false "End time (RFC3339)"
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[AuditLogDTO]}
// @Router       /audit/logs [get]
func (h *Handler) ListLogs(c *gin.Context) {
	var q ListLogsQuery
	_ = c.ShouldBindQuery(&q)

	page, pageSize, offset, limit := q.Normalize()

	filter := Filter{
		Action: q.Action,
		Status: q.Status,
		Limit:  limit,
		Offset: offset,
	}

	if q.StartTime != "" {
		t, err := time.Parse(time.RFC3339, q.StartTime)
		if err != nil {
			httputil.WriteError(c, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: "startTime", Code: "INVALID_FORMAT", Message: "startTime must be in RFC3339 format"},
			}))
			return
		}
		filter.StartTime = &t
	}

	if q.EndTime != "" {
		t, err := time.Parse(time.RFC3339, q.EndTime)
		if err != nil {
			httputil.WriteError(c, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: "endTime", Code: "INVALID_FORMAT", Message: "endTime must be in RFC3339 format"},
			}))
			return
		}
		filter.EndTime = &t
	}

	items, total, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	dtos := toAuditLogDTOs(items)
	httputil.PaginatedSuccess(c, dtos, total, page, pageSize)
}

// ClearLogsQuery defines query parameters for deleting audit logs.
type ClearLogsQuery struct {
	Before string `form:"before"`
}

// ClearLogs godoc
// @Summary      Clear audit logs
// @Tags         audit
// @Produce      json
// @Param        before query string false "Clear logs older than this RFC3339 timestamp"
// @Success      200 {object} httputil.Response{data=object}
// @Router       /audit/logs [delete]
func (h *Handler) ClearLogs(c *gin.Context) {
	var q ClearLogsQuery
	_ = c.ShouldBindQuery(&q)

	var beforeTime []time.Time
	if q.Before != "" {
		t, err := time.Parse(time.RFC3339, q.Before)
		if err != nil {
			httputil.WriteError(c, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
				{Field: "before", Code: "INVALID_FORMAT", Message: "before must be in RFC3339 format"},
			}))
			return
		}
		beforeTime = append(beforeTime, t)
	}

	cleared, err := h.service.Clear(c.Request.Context(), beforeTime...)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, gin.H{"cleared": cleared})
}
