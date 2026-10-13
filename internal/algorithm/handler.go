package algorithm

import (
	"errors"
	"io"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Auditor defines the interface for recording audit events.
type Auditor interface {
	Record(entry audit.Entry)
}

// Handler provides HTTP endpoints for algorithm packages, analysis tasks, and face galleries.
type Handler struct {
	service        *Service
	auditor        Auditor
	logger         *zap.Logger
	maxUploadLimit int64
}

// NewHandler creates a new Handler instance.
func NewHandler(service *Service, auditor Auditor, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service:        service,
		auditor:        auditor,
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
	reader, err := c.Request.MultipartReader()
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Missing upload file", err))
		return
	}

	uploadLimit := h.maxUploadLimit
	if uploadLimit <= 0 {
		uploadLimit = MaxUploadSize
	}

	var tmpPath string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				httputil.WriteError(c, httputil.NewError(http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Uploaded file exceeds size limit", err))
				return
			}
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Failed to read multipart stream", err))
			return
		}
		if part.FormName() != "file" {
			if closeErr := part.Close(); closeErr != nil {
				h.logger.Debug("failed to close unused multipart part", zap.Error(closeErr))
			}
			continue
		}

		tmpPath, err = SaveUploadStreamWithLimit(part, "", uploadLimit)
		if closeErr := part.Close(); closeErr != nil {
			h.logger.Debug("failed to close multipart part", zap.Error(closeErr))
		}
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.Is(err, ErrFileTooLarge) || errors.As(err, &maxBytesErr) {
				httputil.WriteError(c, httputil.NewError(http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", "Uploaded file exceeds 500MB", err))
				return
			}
			httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to save upload", err))
			return
		}
		break
	}

	if tmpPath == "" {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_FILE", "Missing upload file", nil))
		return
	}
	defer func() {
		if rmErr := os.Remove(tmpPath); rmErr != nil && !os.IsNotExist(rmErr) {
			h.logger.Warn("failed to remove temporary upload file", zap.String("path", tmpPath), zap.Error(rmErr))
		}
	}()

	algo, err := h.service.InstallAlgorithm(c.Request.Context(), tmpPath)
	if err != nil {
		switch {
		case errors.Is(err, ErrPlatformMismatch):
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "ALGO_PLATFORM_MISMATCH", "Platform mismatch", err))
		case errors.Is(err, ErrUnsupportedArchive):
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "UNSUPPORTED_ARCHIVE_FORMAT", "Unsupported archive format", err))
		case errors.Is(err, ErrZipSlip):
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "ZIP_SLIP_ATTACK_DETECTED", "Zip-slip attack detected", err))
		case errors.Is(err, ErrDecompressionBombSize), errors.Is(err, ErrDecompressionBombFile):
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "DECOMPRESSION_BOMB_DETECTED", "Decompression bomb detected", err))
		case errors.Is(err, ErrVerificationFailed), errors.Is(err, ErrResultTypeMismatch):
			httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "ALGO_VERIFICATION_FAILED", "Verification failed", err))
		case errors.Is(err, ErrInvalidManifestToken):
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_MANIFEST_TOKEN", err.Error(), err))
		case errors.Is(err, ErrVersionAlreadyInstalled):
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "ALGORITHM_VERSION_EXISTS", "Algorithm version is already installed", err))
		case errors.Is(err, ErrInvalidConfigSchema):
			httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "ALGO_VERIFICATION_FAILED", "Invalid algorithm configuration schema", err))
		default:
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "ALGO_INSTALL_FAILED", err.Error(), err))
		}
		return
	}

	if h.auditor != nil {
		h.auditor.Record(audit.Entry{
			Action: "algorithm.install",
			Target: algo.ID,
			Detail: algo.Name,
			Status: audit.StatusSuccess,
		})
	}

	c.JSON(http.StatusCreated, httputil.Response{
		Code:    "OK",
		Message: "Algorithm installed successfully",
		Data:    algo,
	})
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
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list algorithms", err))
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
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "ALGORITHM_NOT_FOUND", "Algorithm not found", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to get algorithm", err))
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
	if err := h.service.EnableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to enable algorithm", err))
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
	if err := h.service.DisableAlgorithm(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to disable algorithm", err))
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
	if err := h.service.ResetAlgorithm(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrAlgorithmNotInCrashLoop) {
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "ALGORITHM_NOT_IN_CRASH_LOOP", "Algorithm is not in CRASH_LOOP", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to reset algorithm", err))
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
	if err := h.service.DeleteAlgorithm(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrAlgorithmActive) {
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "ALGORITHM_IS_ACTIVE", "Algorithm is active", err))
			return
		}
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "ALGORITHM_NOT_FOUND", "Algorithm not found", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete algorithm", err))
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
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to list analysis tasks", err))
		return
	}
	if tasks == nil {
		tasks = make([]AnalysisTask, 0)
	}
	httputil.Success(c, tasks)
}

// CreateTaskRequest is the request payload for creating an analysis task.
//
// Both camelCase and snake_case keys are accepted: the snake_case alias is used
// only when the camelCase key is absent, so legacy clients keep working without
// silently overriding the canonical field.
type CreateTaskRequest struct {
	ID             string `json:"id"`
	CameraID       string `json:"cameraId"`
	CameraIDAlt    string `json:"camera_id"`
	AlgorithmID    string `json:"algorithmId"`
	AlgorithmIDAlt string `json:"algorithm_id"`
	SampleFPS      int    `json:"sampleFps"`
	SampleFPSAlt   int    `json:"sample_fps"`
	ConfigJSON     string `json:"configJson"`
	ConfigJSONAlt  string `json:"config_json"`
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
	var req CreateTaskRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_REQUEST", "Invalid analysis task payload", err))
		return
	}

	camID := req.CameraID
	if camID == "" {
		camID = req.CameraIDAlt
	}
	algoID := req.AlgorithmID
	if algoID == "" {
		algoID = req.AlgorithmIDAlt
	}

	if camID == "" || algoID == "" {
		httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "CameraID and AlgorithmID are required", nil))
		return
	}

	fps := req.SampleFPS
	if fps <= 0 {
		fps = req.SampleFPSAlt
	}
	if fps <= 0 {
		fps = 5
	}

	cfgJSON := req.ConfigJSON
	if cfgJSON == "" {
		cfgJSON = req.ConfigJSONAlt
	}
	if cfgJSON == "" {
		cfgJSON = "{}"
	}

	task := AnalysisTask{
		ID:          req.ID,
		CameraID:    camID,
		AlgorithmID: algoID,
		Enabled:     true,
		SampleFPS:   fps,
		ConfigJSON:  cfgJSON,
	}
	if task.ID == "" {
		task.ID = "task_" + task.CameraID + "_" + task.AlgorithmID
	}

	if err := h.service.CreateTask(c.Request.Context(), &task); err != nil {
		if errors.Is(err, ErrComputeBudgetExceeded) {
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "COMPUTE_BUDGET_EXCEEDED", err.Error(), err))
			return
		}
		if errors.Is(err, ErrInvalidTaskConfig) {
			httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Analysis task config is invalid", err))
			return
		}
		if errors.Is(err, ErrInvalidConfigSchema) {
			httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Algorithm config schema is invalid", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "TASK_CREATE_FAILED", err.Error(), err))
		return
	}
	c.JSON(http.StatusCreated, httputil.Response{
		Code:    "OK",
		Message: "Analysis task created successfully",
		Data:    task,
	})
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
	if err := h.service.DeleteTask(c.Request.Context(), id); err != nil {
		if errors.Is(err, ErrNotFound) {
			httputil.WriteError(c, httputil.NewError(http.StatusNotFound, "TASK_NOT_FOUND", "Analysis task not found", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to delete analysis task", err))
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}
