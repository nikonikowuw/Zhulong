package audithttp

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler provides HTTP endpoints for audit log inspection and maintenance.
type Handler struct {
	service *audit.Service
	logger  *zap.Logger
}

// NewHandler constructs an audit HTTP Handler.
func NewHandler(service *audit.Service, logger *zap.Logger) *Handler {
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

// ListLogs godoc
// @Summary      List audit logs with filtering and pagination
// @Tags         audit
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        action query string false "Filter by one action (legacy parameter)"
// @Param        actions query []string false "Filter by any matching action (repeat parameter for multiple values)" collectionFormat(multi)
// @Param        status query string false "Filter by status (success/failed)"
// @Param        startTime query string false "Start time (RFC3339)"
// @Param        endTime query string false "End time (RFC3339)"
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[AuditLogResponse]}
// @Router       /audit/logs [get]
func (h *Handler) ListLogs(c *gin.Context) {
	var q ListLogsQuery
	_ = c.ShouldBindQuery(&q)

	filter, page, pageSize, err := q.ToFilter()
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	items, total, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	responses := toAuditLogResponses(items)
	httputil.PaginatedSuccess(c, responses, total, page, pageSize)
}

// ClearLogs godoc
// @Summary      Clear audit logs
// @Tags         audit
// @Produce      json
// @Param        before query string false "Clear logs older than this RFC3339 timestamp"
// @Success      200 {object} httputil.Response{data=object}
// @Router       /audit/logs [delete]
//
// Clearing writes its own audit.clear record in the same transaction as the
// delete, so a successful response always has a matching record. The audit
// middleware is not involved: this endpoint must not depend on the droppable
// asynchronous queue for its own evidence.
func (h *Handler) ClearLogs(c *gin.Context) {
	var q ClearLogsQuery
	_ = c.ShouldBindQuery(&q)

	beforeTime, err := q.ParseBefore()
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	cleared, err := h.service.ClearWithAudit(c.Request.Context(), "audit_logs", beforeTime...)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, gin.H{"cleared": cleared})
}
