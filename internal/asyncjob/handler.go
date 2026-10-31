package asyncjob

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler exposes RESTful HTTP endpoints for system background jobs.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler constructs a Handler instance.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger.Named("asyncjob.handler"),
	}
}

// RegisterRoutes registers the system job management routes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/jobs")
	{
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.POST("/:id/cancel", h.Cancel)
	}
}

type listJobsQuery struct {
	httputil.PaginationQuery
	Type string `form:"type"`
}

// List godoc
// @Summary      List system asynchronous background jobs
// @Tags         system
// @Produce      json
// @Param        page query int false "Page number"
// @Param        pageSize query int false "Page size"
// @Param        type query string false "Job type filter"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[Job]}
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/jobs [get]
func (h *Handler) List(c *gin.Context) {
	var query listJobsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	window := query.Normalize()

	jobs, total, err := h.service.ListJobs(c.Request.Context(), query.Type, window.PageSize, window.Offset)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, jobs, total, window)
}

// Get godoc
// @Summary      Get system background job details
// @Tags         system
// @Produce      json
// @Param        id path string true "Job ID"
// @Success      200  {object}  httputil.Response{data=Job}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/jobs/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	job, err := h.service.GetJob(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, job)
}

// Cancel godoc
// @Summary      Cancel a running background job
// @Tags         system
// @Produce      json
// @Param        id path string true "Job ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      500  {object}  httputil.Response
// @Router       /system/jobs/{id}/cancel [post]
func (h *Handler) Cancel(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.CancelJob(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"cancelled": true})
}
