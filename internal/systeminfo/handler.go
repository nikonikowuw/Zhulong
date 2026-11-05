package systeminfo

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for system hardware monitoring and host identity.
type Handler struct {
	service  *Service
	identity *IdentityService
	logger   *zap.Logger
}

// NewHandler creates a new systeminfo Handler.
func NewHandler(service *Service, identity *IdentityService, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service:  service,
		identity: identity,
		logger:   logger,
	}
}

// RegisterRoutes mounts the telemetry and host identity endpoints onto the protected route group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	hardware := rg.Group("/system/hardware")
	{
		hardware.GET("", h.GetHardware)
	}

	identity := rg.Group("/system/identity")
	{
		identity.GET("", h.GetIdentity)
		identity.PUT("", h.UpdateIdentity)
	}
}

// GetIdentity godoc
// @Summary      Get this host's device identity
// @Description  Returns the stable device_id stamped onto every dispatched webhook / MQTT
// @Description  payload, plus whether the current value is the auto-generated fallback.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=SystemIdentityResponse}
// @Router       /system/identity [get]
func (h *Handler) GetIdentity(c *gin.Context) {
	// identity is always wired by NewSystemInfoComponents; the nil branch only keeps
	// a partially constructed handler (tests, future callers) from panicking.
	if h.identity == nil {
		httputil.Success(c, SystemIdentityResponse{})
		return
	}
	httputil.Success(c, h.identity.Snapshot())
}

// UpdateIdentity godoc
// @Summary      Update this host's device identity
// @Description  Persists a new device_id and publishes it to in-flight dispatchers without a restart.
// @Description  The value is stamped onto every subsequent webhook / MQTT payload.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        request  body      UpdateSystemIdentityRequest  true  "Device identity"
// @Success      200      {object}  httputil.Response{data=SystemIdentityResponse}
// @Failure      422      {object}  httputil.Response  "SYSTEM_IDENTITY_INVALID"
// @Router       /system/identity [put]
func (h *Handler) UpdateIdentity(c *gin.Context) {
	audithttp.Tag(c, ActionIdentityUpdate, "system", "Update host device identity")

	if h.identity == nil {
		httputil.WriteError(c, apperr.New(apperr.KindUnavailable, CodeIdentityInvalid, "identity service unavailable", nil))
		return
	}

	var req UpdateSystemIdentityRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	resp, err := h.identity.Update(c.Request.Context(), req.DeviceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	h.logger.Info("device identity saved", zap.String("device_id", resp.DeviceID))
	httputil.Success(c, resp)
}

// GetHardware godoc
// @Summary      Get system hardware telemetry and trend history
// @Description  Retrieves current CPU, memory, storage, network rates, NPU load,
// @Description  temperatures, and recent time-series history points.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=SystemHardwareResponse}
// @Failure      500  {object}  httputil.Response  "INTERNAL_ERROR"
// @Router       /system/hardware [get]
func (h *Handler) GetHardware(c *gin.Context) {
	status, err := h.service.GetHardwareStatus(c.Request.Context())
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, status)
}
