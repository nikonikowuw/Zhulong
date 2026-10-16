package task

import (
	"net/http"

	"github.com/gin-gonic/gin"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for video analysis tasks.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler instantiates a new Task Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes registers task endpoints on the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/tasks")
	{
		group.GET("", h.ListTasks)
		group.GET("/:id", h.GetTask)
		group.POST("", h.CreateTask)
		group.PUT("/:id", h.UpdateTask)
		group.DELETE("/:id", h.DeleteTask)
	}

	legacyGroup := rg.Group("/algorithms/tasks")
	{
		legacyGroup.GET("", h.ListTasks)
		legacyGroup.POST("", h.CreateTask)
		legacyGroup.DELETE("/:id", h.DeleteTask)
	}
}

// ListTasks godoc
// @Summary      List analysis tasks
// @Description  Returns tasks filtered optionally by camera_id or algorithm_id.
// @Tags         tasks
// @Produce      json
// @Param        camera_id query string false "Filter by camera ID"
// @Param        algorithm_id query string false "Filter by algorithm ID"
// @Success      200  {object}  httputil.Response{data=[]Task}
// @Failure      500  {object}  httputil.Response
// @Router       /tasks [get]
func (h *Handler) ListTasks(c *gin.Context) {
	cameraID := c.Query("camera_id")
	if cameraID == "" {
		cameraID = c.Query("cameraId")
	}
	algorithmID := c.Query("algorithm_id")
	if algorithmID == "" {
		algorithmID = c.Query("algorithmId")
	}

	tasks, err := h.service.ListTasks(c.Request.Context(), cameraID, algorithmID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	if tasks == nil {
		tasks = make([]Task, 0)
	}
	httputil.Success(c, tasks)
}

// GetTask godoc
// @Summary      Get single analysis task
// @Tags         tasks
// @Produce      json
// @Param        id path string true "Task ID"
// @Success      200  {object}  httputil.Response{data=Task}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /tasks/{id} [get]
func (h *Handler) GetTask(c *gin.Context) {
	id := c.Param("id")
	task, err := h.service.GetTask(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, task)
}

// CreateTask godoc
// @Summary      Create an analysis task
// @Description  Binds a camera to an algorithm with a sampling rate and a config JSON document.
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Param        request body CreateTaskRequest true "Analysis task payload"
// @Success      201  {object}  httputil.Response{data=Task}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /tasks [post]
func (h *Handler) CreateTask(c *gin.Context) {
	audithttp.Tag(c, ActionTaskCreate, "task:", "Create analysis task")

	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_REQUEST", "Invalid analysis task payload", err))
		return
	}

	task := req.ToTask()
	if err := h.service.CreateTask(c.Request.Context(), task); err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusCreated, task)
}

// UpdateTask godoc
// @Summary      Update an analysis task
// @Description  Updates sampling rate, enabled state, or config JSON of an existing task.
// @Tags         tasks
// @Accept       json
// @Produce      json
// @Param        id path string true "Task ID"
// @Param        request body UpdateTaskRequest true "Update payload"
// @Success      200  {object}  httputil.Response{data=Task}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /tasks/{id} [put]
func (h *Handler) UpdateTask(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionTaskUpdate, "task:"+id, "Update analysis task "+id)

	var req UpdateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_REQUEST", "Invalid analysis task payload", err))
		return
	}

	task, err := h.service.UpdateTask(c.Request.Context(), id, &req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, task)
}

// DeleteTask godoc
// @Summary      Delete an analysis task
// @Tags         tasks
// @Produce      json
// @Param        id path string true "Analysis task ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /tasks/{id} [delete]
func (h *Handler) DeleteTask(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionTaskDelete, "task:"+id, "Delete analysis task "+id)

	if err := h.service.DeleteTask(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}
