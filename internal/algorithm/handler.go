package algorithm

import (
	"net/http"

	"github.com/gin-gonic/gin"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler provides HTTP endpoints for algorithm packages, analysis tasks, and face galleries.
type Handler struct {
	service        *Service
	logger         *zap.Logger
	maxUploadLimit int64
}

// NewHandler creates a new Handler instance.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service:        service,
		logger:         logger.Named("algorithm_handler"),
		maxUploadLimit: MaxUploadSize,
	}
}

// SetMaxUploadLimitForTest overrides the max upload limit for testing.
func (h *Handler) SetMaxUploadLimitForTest(limit int64) {
	h.maxUploadLimit = limit
}

// RegisterRoutes registers control-plane algorithm endpoints on the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/algorithms")
	{
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.POST("/:id/enable", h.Enable)
		group.POST("/:id/disable", h.Disable)
		group.POST("/:id/reset", h.Reset)
		group.DELETE("/:id", h.Delete)

		// Analysis Tasks
		group.GET("/tasks", h.ListTasks)
		group.POST("/tasks", h.CreateTask)
		group.DELETE("/tasks/:id", h.DeleteTask)
	}
}

// RegisterUploadRoutes registers large-body upload endpoints on the upload router group.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
	rg.POST("/algorithms/install", h.Install)
}

type uploadRouteRegistrar struct {
	handler *Handler
}

func (r *uploadRouteRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	r.handler.RegisterUploadRoutes(rg)
}

// UploadRouteRegistrar returns a RouteRegistrar adapter for upload endpoints.
func (h *Handler) UploadRouteRegistrar() RouteRegistrar {
	return &uploadRouteRegistrar{handler: h}
}

// Install godoc
// @Summary      Install an algorithm package
// @Description  Streams a multipart archive to a temporary file, then verifies platform target, resource budget, sample inference and config schema before registering the algorithm. Rejects platform mismatch, unsupported archive formats, zip-slip paths, decompression bombs, invalid manifest tokens and already installed versions.
// @Tags         algorithm
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "Algorithm package archive (at most 500 MB)"
// @Success      201  {object}  httputil.Response{data=Algorithm}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/install [post]
func (h *Handler) Install(c *gin.Context) {
	// Declared before the multipart stream is read so a rejected archive is
	// still attributable. The target is refined once the algorithm is known.
	audithttp.Tag(c, ActionAlgorithmInstall, "algorithm:", "Install algorithm")

	filePart, cleanup, err := ExtractMultipartFile(c.Request, "file")
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	defer cleanup()

	algo, err := h.service.InstallAlgorithmFromStream(c.Request.Context(), filePart, h.maxUploadLimit)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.TagTarget(c, "algorithm:"+algo.ID)
	audithttp.TagDetail(c, algo.Name)

	httputil.SuccessWithStatus(c, http.StatusCreated, algo)
}

// List godoc
// @Summary      List registered algorithms
// @Tags         algorithm
// @Produce      json
// @Success      200  {object}  httputil.Response{data=[]Algorithm}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms [get]
func (h *Handler) List(c *gin.Context) {
	algos, err := h.service.ListAlgorithms(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	if algos == nil {
		algos = make([]Algorithm, 0)
	}
	httputil.Success(c, algos)
}

// Get godoc
// @Summary      Get algorithm details by ID
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=Algorithm}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	algo, err := h.service.GetAlgorithm(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, algo)
}

// Enable godoc
// @Summary      Enable an algorithm and start its runner process
// @Description  Starts the supervised runner subprocess and sets the algorithm status to ACTIVE. The runner is monitored and restarted according to the recovery policy until the crash-loop threshold is reached.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/enable [post]
func (h *Handler) Enable(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmEnable, "algorithm:"+id, "Enable algorithm "+id)

	if err := h.service.EnableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusActive})
}

// Disable godoc
// @Summary      Disable an algorithm and stop its runner process
// @Description  Stops the supervised runner subprocess and sets the algorithm status to DISABLED.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/disable [post]
func (h *Handler) Disable(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmDisable, "algorithm:"+id, "Disable algorithm "+id)

	if err := h.service.DisableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusDisabled})
}

// Reset godoc
// @Summary      Reset an algorithm stuck in CRASH_LOOP
// @Description  Clears the crash-loop circuit breaker and returns the algorithm to DISABLED so it can be enabled again. Returns 409 ALGORITHM_NOT_IN_CRASH_LOOP when the algorithm is not currently in CRASH_LOOP.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id}/reset [post]
func (h *Handler) Reset(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmReset, "algorithm:"+id, "Reset algorithm "+id)

	if err := h.service.ResetAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"status": StatusDisabled})
}

// Delete godoc
// @Summary      Delete an algorithm
// @Description  Removes the algorithm registration and its installed files. Deleting an algorithm that is currently ACTIVE returns 409 ALGORITHM_IS_ACTIVE; disable it first.
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Algorithm ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmDelete, "algorithm:"+id, "Delete algorithm "+id)

	if err := h.service.DeleteAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}

// --- Analysis Tasks Handlers ---

// ListTasks godoc
// @Summary      List analysis tasks
// @Tags         algorithm
// @Produce      json
// @Param        camera_id query string false "Filter by camera ID"
// @Success      200  {object}  httputil.Response{data=[]AnalysisTask}
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/tasks [get]
func (h *Handler) ListTasks(c *gin.Context) {
	cameraID := c.Query("camera_id")
	tasks, err := h.service.ListTasks(c.Request.Context(), cameraID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	if tasks == nil {
		tasks = make([]AnalysisTask, 0)
	}
	httputil.Success(c, tasks)
}

// CreateTask godoc
// @Summary      Create an analysis task
// @Description  Binds a camera to an algorithm with a sampling rate and a config JSON document. Both camelCase and snake_case request keys are accepted; the snake_case alias is used only when the camelCase key is absent. Missing id, cameraId or algorithmId is rejected with 422 VALIDATION_FAILED. Returns 409 COMPUTE_BUDGET_EXCEEDED when the accumulated compute cost of enabled tasks exceeds the host budget.
// @Tags         algorithm
// @Accept       json
// @Produce      json
// @Param        request body CreateTaskRequest true "Analysis task payload"
// @Success      201  {object}  httputil.Response{data=AnalysisTask}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/tasks [post]
func (h *Handler) CreateTask(c *gin.Context) {
	audithttp.Tag(c, ActionAlgorithmTaskCreate, "algorithm.task:", "Create analysis task")

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

// DeleteTask godoc
// @Summary      Delete an analysis task
// @Tags         algorithm
// @Produce      json
// @Param        id path string true "Analysis task ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /algorithms/tasks/{id} [delete]
func (h *Handler) DeleteTask(c *gin.Context) {
	id := c.Param("id")
	audithttp.Tag(c, ActionAlgorithmTaskDelete, "algorithm.task:"+id, "Delete analysis task "+id)

	if err := h.service.DeleteTask(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}
