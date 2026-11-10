package asyncjob

import (
	"github.com/gin-gonic/gin"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Handler exposes RESTful HTTP endpoints for system background jobs.
type Handler struct {
	service *Service
}

// NewHandler constructs a Handler instance.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
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

// List godoc
// @Summary      List system asynchronous background jobs
// @Tags         system
// @Produce      json
// @Param        page query int false "Page number"
// @Param        pageSize query int false "Page size"
// @Param        type query string false "Job type filter"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[JobResponse]}
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/jobs [get]
func (h *Handler) List(c *gin.Context) {
	var query ListJobsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	window := query.Normalize()

	jobs, total, err := h.service.ListJobs(
		c.Request.Context(),
		query.Type,
		window.PageSize,
		window.Offset,
	)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(
		c,
		toJobResponses(jobs, requestLocale(c)),
		total,
		window,
	)
}

// Get godoc
// @Summary      Get system background job details
// @Tags         system
// @Produce      json
// @Param        id path string true "Job ID"
// @Success      200  {object}  httputil.Response{data=JobResponse}
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
	httputil.Success(c, toJobResponse(job, requestLocale(c)))
}

// Cancel godoc
// @Summary      Cancel a running or queued background job
// @Tags         system
// @Produce      json
// @Param        id path string true "Job ID"
// @Success      200  {object}  httputil.Response{data=CancelJobResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/jobs/{id}/cancel [post]
func (h *Handler) Cancel(c *gin.Context) {
	id := c.Param("id")
	// Declare the audit intent before the side effect so a failed cancellation
	// (unknown id, already finished) is audited together with a successful one.
	audithttp.Tag(
		c,
		ActionCancelJob,
		"job:"+id,
		"Cancel background job",
	)
	if err := h.service.CancelJob(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, CancelJobResponse{Cancelled: true})
}

func requestLocale(c *gin.Context) string {
	return httputil.ResolveLocale(c.GetHeader("Accept-Language"))
}
