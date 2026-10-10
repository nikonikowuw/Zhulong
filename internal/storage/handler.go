package storage

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for media storage configuration and lifecycle management.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates a new system storage Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes implements the app.RouteRegistrar interface for protected routes.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/storage")
	{
		group.GET("/status", h.GetStatus)
		group.GET("/config", h.GetConfig)
		group.PUT("/config", h.UpdateConfig)
		group.POST("/test", h.TestPath)
		group.POST("/cleanup", h.TriggerCleanup)
	}
}

// GetStatus godoc
// @Summary      Get media storage status and volume telemetry
// @Description  Retrieves disk capacity metrics, categorized usage breakdown, mount point, and current health status.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=StorageStatus}
// @Failure      500  {object}  httputil.Response
// @Router       /system/storage/status [get]
func (h *Handler) GetStatus(c *gin.Context) {
	status, err := h.service.GetStatus(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve storage status", err))
		return
	}
	httputil.Success(c, status)
}

// GetConfig godoc
// @Summary      Get media storage lifecycle configuration
// @Description  Retrieves current media root directory, retention periods, and watermark thresholds.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=StorageConfig}
// @Failure      500  {object}  httputil.Response
// @Router       /system/storage/config [get]
func (h *Handler) GetConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve storage config", err))
		return
	}
	httputil.Success(c, cfg)
}

// UpdateConfig godoc
// @Summary      Update media storage lifecycle configuration
// @Description  Validates and updates media root directory, retention policies, and high/low watermarks with live hot switching.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      UpdateConfigRequest  true  "Storage configuration parameters"
// @Success      200  {object}  httputil.Response{data=StorageStatus}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/storage/config [put]
func (h *Handler) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	cfg := StorageConfig{
		MediaDirectory:          req.MediaDirectory,
		RecordingsRetentionDays: req.RecordingsRetentionDays,
		SnapshotsRetentionDays:  req.SnapshotsRetentionDays,
		ExportsRetentionHours:   req.ExportsRetentionHours,
		HighWatermarkPercent:    req.HighWatermarkPercent,
		LowWatermarkPercent:     req.LowWatermarkPercent,
		EmergencyStopPercent:    req.EmergencyStopPercent,
		EmergencyStopMinMB:      req.EmergencyStopMinMB,
	}

	username := c.GetString("username")
	if username == "" {
		username = "admin"
	}
	clientIP := c.ClientIP()

	status, err := h.service.UpdateConfig(c.Request.Context(), cfg, username, clientIP)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_STORAGE_CONFIG", err.Error(), err))
		return
	}
	httputil.Success(c, status)
}

// TestPath godoc
// @Summary      Test candidate storage directory
// @Description  Performs write probe, filesystem detection, and external volume identity verification on candidate path.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      PathTestRequest  true  "Target directory path to test"
// @Success      200  {object}  httputil.Response{data=PathTestResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/storage/test [post]
func (h *Handler) TestPath(c *gin.Context) {
	var req PathTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	username := c.GetString("username")
	if username == "" {
		username = "admin"
	}
	clientIP := c.ClientIP()

	res, err := h.service.TestPath(c.Request.Context(), req.Path, username, clientIP)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "STORAGE_PATH_INVALID", err.Error(), err))
		return
	}
	httputil.Success(c, res)
}

// TriggerCleanup godoc
// @Summary      Manually trigger storage prune cycle
// @Description  Forces an immediate pruning cycle across expired exports and recordings according to lifecycle policy.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=CleanupSummary}
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/storage/cleanup [post]
func (h *Handler) TriggerCleanup(c *gin.Context) {
	username := c.GetString("username")
	if username == "" {
		username = "admin"
	}
	clientIP := c.ClientIP()

	summary, err := h.service.TriggerManualCleanup(c.Request.Context(), username, clientIP)
	if err != nil {
		if errors.Is(err, ErrCleanupInProgress) {
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "STORAGE_CLEANUP_IN_PROGRESS", "Cleanup cycle is already in progress", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to execute cleanup", err))
		return
	}
	httputil.Success(c, summary)
}
