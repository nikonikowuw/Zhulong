package camera

import (
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler exposes REST API and SSE endpoints for camera management.
type Handler struct {
	service    *CameraService
	hub        *EventHub
	sessionVal func(token string) bool
	logger     *zap.Logger
}

// NewHandler creates a new Handler.
func NewHandler(service *CameraService, hub *EventHub, logger *zap.Logger, sessionVal ...func(token string) bool) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	var valFn func(token string) bool
	if len(sessionVal) > 0 {
		valFn = sessionVal[0]
	}
	return &Handler{
		service:    service,
		hub:        hub,
		sessionVal: valFn,
		logger:     logger.Named("camera.handler"),
	}
}

// RegisterRoutes registers all camera endpoints on the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/cameras")
	{
		// SSE endpoint must be registered before param routes to avoid routing collisions
		group.GET("/events", h.Events)

		group.POST("", h.Create)
		group.GET("", h.List)
		group.GET("/:id", h.Get)
		group.GET("/:id/credentials", h.GetCredentials)
		group.PUT("/:id", h.Update)
		group.DELETE("/:id", h.Delete)
		group.POST("/:id/diagnose", h.Diagnose)
	}
}

// Create godoc
// @Summary      Create a new camera
// @Tags         camera
// @Accept       json
// @Produce      json
// @Param        request body CreateCameraRequest true "Camera parameters"
// @Success      201  {object}  httputil.Response{data=CameraResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Router       /cameras [post]
func (h *Handler) Create(c *gin.Context) {
	var req CreateCameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}
	res, err := h.service.Create(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.SuccessWithStatus(c, http.StatusCreated, res)
}

// List godoc
// @Summary      List cameras
// @Tags         camera
// @Produce      json
// @Param        limit query int false "Pagination limit"
// @Param        offset query int false "Pagination offset"
// @Success      200  {object}  httputil.Response{data=object}
// @Router       /cameras [get]
func (h *Handler) List(c *gin.Context) {
	limit := 20
	offset := 0

	if lStr := c.Query("limit"); lStr != "" {
		if l, err := strconv.Atoi(lStr); err == nil && l > 0 {
			limit = l
		}
	}
	if oStr := c.Query("offset"); oStr != "" {
		if o, err := strconv.Atoi(oStr); err == nil && o >= 0 {
			offset = o
		}
	}

	cameras, total, err := h.service.List(c.Request.Context(), limit, offset)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, gin.H{
		"items":  cameras,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// Get godoc
// @Summary      Get camera by ID
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=CameraResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httputil.Success(c, res)
}

// GetCredentials godoc
// @Summary      Get camera plaintext credentials
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=CameraCredentialsResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id}/credentials [get]
func (h *Handler) GetCredentials(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.GetCredentials(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	httputil.Success(c, res)
}

// Update godoc
// @Summary      Update camera configuration
// @Tags         camera
// @Accept       json
// @Produce      json
// @Param        id path string true "Camera ID"
// @Param        request body UpdateCameraRequest true "Updated parameters"
// @Success      200  {object}  httputil.Response{data=CameraResponse}
// @Failure      409  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Router       /cameras/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	id := c.Param("id")
	var req UpdateCameraRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, nil)
		return
	}

	res, err := h.service.Update(c.Request.Context(), id, req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, res)
}

// Delete godoc
// @Summary      Delete camera
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"id": id, "deleted": true})
}

// Diagnose godoc
// @Summary      Manually diagnose camera connection
// @Tags         camera
// @Produce      json
// @Param        id path string true "Camera ID"
// @Success      200  {object}  httputil.Response{data=DiagnoseResponse}
// @Failure      404  {object}  httputil.Response
// @Router       /cameras/{id}/diagnose [post]
func (h *Handler) Diagnose(c *gin.Context) {
	id := c.Param("id")
	res, err := h.service.Diagnose(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, res)
}

// Events godoc
// @Summary      Subscribe to camera state events via SSE
// @Tags         camera
// @Produce      text/event-stream
// @Success      200  {string}  string "Server-Sent Events stream"
// @Router       /cameras/events [get]
func (h *Handler) Events(c *gin.Context) {
	sub, snapshotMsg, err := h.hub.Subscribe()
	if err != nil {
		if errors.Is(err, ErrSSEMaxClientsReached) {
			httputil.WriteError(c, apperr.New(apperr.KindRateLimited, "TOO_MANY_CONNECTIONS", "Too many active SSE connections", err))
			return
		}
		httputil.WriteError(c, apperr.New(apperr.KindInternal, "SSE_ERROR", "Failed to initiate SSE session", err))
		return
	}
	defer h.hub.Unsubscribe(sub)

	// Set SSE HTTP response headers
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		httputil.WriteError(c, apperr.New(apperr.KindInternal, "STREAMING_UNSUPPORTED", "Streaming unsupported", nil))
		return
	}

	// 1. Send initial snapshot
	snapshotBytes, err := snapshotMsg.FormatSSE()
	if err == nil {
		_, _ = c.Writer.Write(snapshotBytes)
		flusher.Flush()
	}

	clientGone := c.Request.Context().Done()
	authTicker := time.NewTicker(30 * time.Second)
	defer authTicker.Stop()

	for {
		select {
		case <-clientGone:
			return
		case <-authTicker.C:
			if h.sessionVal != nil {
				token := ""
				if cookie, err := c.Request.Cookie("zhulong_session"); err == nil {
					token = cookie.Value
				}
				if token == "" || !h.sessionVal(token) {
					return // Session expired or revoked
				}
			}
		case msg, ok := <-sub.Channel():
			if !ok {
				return // Channel closed (e.g. server shutdown or slow client eviction)
			}
			msgBytes, err := msg.FormatSSE()
			if err != nil {
				continue
			}
			if _, err := c.Writer.Write(msgBytes); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// Ensure interface compliance
var _ io.Closer = (*Handler)(nil)

func (h *Handler) Close() error {
	return h.hub.Close()
}
