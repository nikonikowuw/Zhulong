package storage

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
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
		group.POST("/test-connection", h.TestConnection)
		group.POST("/cleanup", h.TriggerCleanup)
		group.POST("/migrate", h.StartMigration)
		group.GET("/migrate/latest", h.GetLatestMigration)
	}
}

// currentActor resolves the identity to attribute a user-initiated storage
// operation to.
//
// The authenticated actor is read from the standard request context, where
// auth.RequireAuth stores it. Reading it here rather than importing internal/auth
// keeps this module free of a dependency on the auth package.
//
// The username must never fall back to a fixed "admin": the system has one
// configurable account, so a hardcoded name would fabricate an identity on every
// record. An unauthenticated caller is reported as anonymous instead, and the
// connection source IP remains the primary attribution field.
func (h *Handler) currentActor(c *gin.Context) (string, string) {
	ip := c.ClientIP()
	actor, ok := audit.FromContext(c.Request.Context())
	if !ok || actor.Username == "" {
		return audit.UsernameAnonymous, ip
	}
	return actor.Username, ip
}

// logClientError records the concrete reason behind a 4xx rejection. WriteError
// keeps that reason out of the response and only logs 5xx, but "which path was
// rejected and why" is the first thing an operator asks about a storage change.
func (h *Handler) logClientError(err error) {
	h.logClientErrorWithSecret(err, "")
}

func (h *Handler) logClientErrorWithSecret(err error, secret string) {
	var appErr *apperr.Error
	isServerFault := !errors.As(err, &appErr) ||
		appErr.Kind == apperr.KindInternal ||
		appErr.Kind == apperr.KindUnspecified
	if isServerFault {
		return
	}

	h.logger.Warn("storage request rejected",
		zap.String("code", appErr.Code),
		zap.String("error", sanitizeErrorText(err.Error(), secret)),
	)
}

// GetStatus godoc
// @Summary      Get media storage status and volume telemetry
// @Description  Retrieves disk capacity metrics, categorized usage breakdown, mount point, and current health status.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=StorageStatus}
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR, STORAGE_TELEMETRY_UNAVAILABLE"
// @Router       /system/storage/status [get]
func (h *Handler) GetStatus(c *gin.Context) {
	status, err := h.service.GetStatus(c.Request.Context())
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
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
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /system/storage/config [get]
func (h *Handler) GetConfig(c *gin.Context) {
	cfg, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, cfg)
}

// UpdateConfig godoc
// @Summary      Update media storage lifecycle configuration
// @Description  Validates and updates media root, retention policies, and watermarks with live hot switching.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      UpdateConfigRequest  true  "Storage configuration parameters"
// @Success      200  {object}  httputil.Response{data=StorageStatus}
// @Failure      422  {object}  httputil.Response  "INVALID_STORAGE_CONFIG or VALIDATION_FAILED"
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR, STORAGE_TELEMETRY_UNAVAILABLE"
// @Router       /system/storage/config [put]
func (h *Handler) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	localFallback := true
	if req.LocalFallback != nil {
		localFallback = *req.LocalFallback
	}
	suppliedSecret := ""
	if req.S3 != nil {
		suppliedSecret = req.S3.SecretAccessKey
	}

	cfg := StorageConfig{
		Backend:                 req.Backend,
		DeliveryMode:            req.DeliveryMode,
		LocalFallback:           localFallback,
		MediaDirectory:          req.MediaDirectory,
		SpoolDirectory:          req.SpoolDirectory,
		SpoolMaxMB:              req.SpoolMaxMB,
		RecordingsRetentionDays: req.RecordingsRetentionDays,
		SnapshotsRetentionDays:  req.SnapshotsRetentionDays,
		ExportsRetentionHours:   req.ExportsRetentionHours,
		HighWatermarkPercent:    req.HighWatermarkPercent,
		LowWatermarkPercent:     req.LowWatermarkPercent,
		EmergencyStopPercent:    req.EmergencyStopPercent,
		EmergencyStopMinMB:      req.EmergencyStopMinMB,
		S3:                      req.S3,
	}

	username, clientIP := h.currentActor(c)

	status, err := h.service.UpdateConfig(
		c.Request.Context(),
		cfg,
		username,
		clientIP,
	)
	if err != nil {
		h.logClientErrorWithSecret(err, suppliedSecret)
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, status)
}

// TestPath godoc
// @Summary      Test candidate storage directory or backend connection
// @Description  Runs a local path write probe and telemetry check, or delegates to S3 connectivity test when backend is set.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      PathTestRequest  true  "Target directory path to test (or ConnectionTestRequest when backend is set)"
// @Success      200  {object}  httputil.Response{data=PathTestResponse}  "Path test result (or ConnectionTestResponse when backend is set)"
// @Failure      422  {object}  httputil.Response  "VALIDATION_FAILED or INVALID_STORAGE_CONFIG"
// @Failure      500  {object}  httputil.Response  "STORAGE_TELEMETRY_UNAVAILABLE"
// @Router       /system/storage/test [post]
func (h *Handler) TestPath(c *gin.Context) {
	var peek struct {
		Backend string `json:"backend"`
	}
	if err := c.ShouldBindBodyWith(&peek, binding.JSON); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	if strings.TrimSpace(peek.Backend) != "" {
		var connReq ConnectionTestRequest
		if err := c.ShouldBindBodyWith(&connReq, binding.JSON); err != nil {
			httputil.HandleBindError(c, err)
			return
		}
		h.runConnectionTest(c, connReq)
		return
	}

	var req PathTestRequest
	if err := c.ShouldBindBodyWith(&req, binding.JSON); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	username, clientIP := h.currentActor(c)

	res, err := h.service.TestPath(
		c.Request.Context(),
		req.Path,
		username,
		clientIP,
	)
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, res)
}

// TestConnection godoc
// @Summary      Test local or remote S3 storage connection
// @Description  Validates write connectivity against a candidate local directory or S3/MinIO bucket configuration without persisting settings.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      ConnectionTestRequest  true  "Connection probe parameters"
// @Success      200  {object}  httputil.Response{data=ConnectionTestResponse}
// @Failure      422  {object}  httputil.Response  "INVALID_STORAGE_CONFIG or VALIDATION_FAILED"
// @Failure      500  {object}  httputil.Response  "STORAGE_TELEMETRY_UNAVAILABLE"
// @Router       /system/storage/test-connection [post]
func (h *Handler) TestConnection(c *gin.Context) {
	var req ConnectionTestRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	h.runConnectionTest(c, req)
}

func (h *Handler) runConnectionTest(c *gin.Context, req ConnectionTestRequest) {
	suppliedSecret := ""
	if req.S3 != nil {
		suppliedSecret = req.S3.SecretAccessKey
	}
	username, clientIP := h.currentActor(c)

	res, err := h.service.TestConnection(
		c.Request.Context(),
		req,
		username,
		clientIP,
	)
	if err != nil {
		h.logClientErrorWithSecret(err, suppliedSecret)
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, res)
}

// TriggerCleanup godoc
// @Summary      Manually trigger storage prune cycle
// @Description  Forces an immediate pruning cycle: all expired exports/recordings/snapshots first, then capacity eviction of unexpired recordings and unlocked snapshots until the target watermark.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=CleanupSummary}
// @Failure      409  {object}  httputil.Response  "STORAGE_CLEANUP_IN_PROGRESS"
// @Failure      500  {object}  httputil.Response  "STORAGE_CLEANUP_FAILED, DATABASE_ERROR"
// @Router       /system/storage/cleanup [post]
func (h *Handler) TriggerCleanup(c *gin.Context) {
	username, clientIP := h.currentActor(c)

	summary, err := h.service.TriggerManualCleanup(c.Request.Context(), username, clientIP)
	if err != nil {
		h.logClientError(err)
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, summary)
}

// StartMigration godoc
// @Summary      Start local-to-remote media asset migration job
// @Description  Queues an asynchronous background job migrating historical local snapshots, recordings, faces, and spatial maps to the active remote S3 store.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      StartMigrationRequest  false  "Migration options"
// @Success      202  {object}  httputil.Response{data=MigrationJobResponse}
// @Failure      409  {object}  httputil.Response  "STORAGE_MIGRATION_IN_PROGRESS"
// @Failure      422  {object}  httputil.Response  "STORAGE_MIGRATION_REQUIRES_S3 or VALIDATION_FAILED"
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Failure      503  {object}  httputil.Response  "STORAGE_MIGRATION_UNAVAILABLE"
// @Router       /system/storage/migrate [post]
func (h *Handler) StartMigration(c *gin.Context) {
	var req StartMigrationRequest
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
			httputil.HandleBindError(c, err)
			return
		}
	}

	username, clientIP := h.currentActor(c)
	job, err := h.service.StartMigration(
		c.Request.Context(),
		req,
		username,
		clientIP,
	)
	if err != nil {
		h.logClientError(err)
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.SuccessWithStatus(
		c,
		http.StatusAccepted,
		newMigrationJobResponse(job, httputil.ResolveLocale(c.GetHeader("Accept-Language"))),
	)
}

// GetLatestMigration godoc
// @Summary      Get latest local-to-remote storage migration job
// @Description  Returns the most recent local-to-remote asset migration job status, or null when no job has been submitted yet.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=MigrationJobResponse}
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Failure      503  {object}  httputil.Response  "STORAGE_MIGRATION_UNAVAILABLE"
// @Router       /system/storage/migrate/latest [get]
func (h *Handler) GetLatestMigration(c *gin.Context) {
	job, err := h.service.GetLatestMigration(c.Request.Context())
	if err != nil {
		h.logClientError(err)
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	if job == nil {
		httputil.Success(c, (*MigrationJobResponse)(nil))
		return
	}
	httputil.Success(c, newMigrationJobResponse(job, httputil.ResolveLocale(c.GetHeader("Accept-Language"))))
}
