package gb28181

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler exposes the protected GB28181 endpoints.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates the HTTP handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{service: service, logger: logger}
}

// RegisterRoutes mounts the GB28181 endpoints on the protected route group.
// Configuration is persisted here; nothing in this package applies it at runtime.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/gb28181")
	{
		group.GET("/config", h.GetConfig)
		group.PUT("/config", h.UpdateConfig)
		group.GET("/runtime", h.GetRuntime)
		group.GET("/devices", h.ListDevices)
		group.PUT("/devices/:deviceId", h.UpsertDevice)
	}
}

// GetConfig godoc
// @Summary      Get GB28181 configuration
// @Description  Returns the persisted GB28181 target configuration plus the configuration the running process actually loaded, its state and whether a restart is required.
// @Tags         gb28181
// @Produce      json
// @Success      200  {object}  httputil.Response{data=ConfigResponse}
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /gb28181/config [get]
func (h *Handler) GetConfig(c *gin.Context) {
	resp, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, resp)
}

// UpdateConfig godoc
// @Summary      Update GB28181 configuration
// @Description  Validates and atomically persists the GB28181 configuration. The running SIP listener is not reconfigured; the response reports restartRequired until the process is restarted. An empty mediaAdvertisedIp opts into single-address inference and assumes no NAT; NAT deployments must provide an explicit reachable address.
// @Tags         gb28181
// @Accept       json
// @Produce      json
// @Param        body  body      UpdateConfigRequest  true  "Complete target configuration"
// @Success      200   {object}  httputil.Response{data=ConfigResponse}
// @Failure      422   {object}  httputil.Response  "VALIDATION_FAILED, GB28181_CONFIG_INVALID, GB28181_PORT_RANGE_INVALID, GB28181_ADDRESS_UNRESOLVABLE, GB28181_PROFILE_UNSUPPORTED, GB28181_REGISTERED_DEVICE_REQUIRED"
// @Failure      500   {object}  httputil.Response  "DATABASE_ERROR, GB28181_ADDRESS_PROBE_FAILED"
// @Router       /gb28181/config [put]
func (h *Handler) UpdateConfig(c *gin.Context) {
	audithttp.Tag(c, ActionConfigUpdate, "gb28181", "Update GB28181 configuration")

	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	resp, err := h.service.UpdateConfig(c.Request.Context(), req.ToConfig())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	// Only the security-relevant summary is logged; the row never contains secrets,
	// but the point is to keep this endpoint's log footprint deliberate.
	h.logger.Info("gb28181 configuration saved",
		zap.Bool("enabled", resp.Config.Enabled),
		zap.String("sip_transport", resp.Config.SIPTransport),
		zap.Int("media_capacity", resp.Runtime.MediaCapacity),
		zap.Bool("restart_required", resp.Runtime.RestartRequired),
	)
	httputil.Success(c, resp)
}

// GetRuntime godoc
// @Summary      Get GB28181 runtime snapshot
// @Description  Reports live counters for operators: device totals, active sessions, media port pool usage and exhausted-port hits.
// @Tags         gb28181
// @Produce      json
// @Success      200  {object}  httputil.Response{data=Snapshot}
// @Router       /gb28181/runtime [get]
func (h *Handler) GetRuntime(c *gin.Context) {
	snapshot, err := h.service.Snapshot(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, snapshot)
}

// ListDevices godoc
// @Summary      List provisioned GB28181 devices
// @Description  Returns the device allowlist with online state. Digest passwords are never included.
// @Tags         gb28181
// @Produce      json
// @Success      200  {object}  httputil.Response{data=DeviceListResponse}
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /gb28181/devices [get]
func (h *Handler) ListDevices(c *gin.Context) {
	devices, err := h.service.ListDevices(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	items := make([]DeviceResponse, 0, len(devices))
	for _, device := range devices {
		items = append(items, toDeviceResponse(device))
	}
	httputil.Success(c, DeviceListResponse{Items: items})
}

// UpsertDevice godoc
// @Summary      Provision or update a GB28181 device
// @Description  Adds a device to the registration allowlist or updates its metadata and Digest password. Omitting password keeps the stored credential. The password is encrypted before storage and is never returned.
// @Tags         gb28181
// @Accept       json
// @Produce      json
// @Param        deviceId  path      string               true  "20-digit GB28181 device identity"
// @Param        body      body      UpsertDeviceRequest  true  "Device metadata and optional password"
// @Success      200       {object}  httputil.Response{data=DeviceResponse}
// @Failure      422       {object}  httputil.Response  "VALIDATION_FAILED, GB28181_DEVICE_ID_INVALID, GB28181_TRANSPORT_INVALID, GB28181_PASSWORD_REQUIRED"
// @Failure      500       {object}  httputil.Response  "DATABASE_ERROR, GB28181_RUNTIME_NOT_READY"
// @Router       /gb28181/devices/{deviceId} [put]
func (h *Handler) UpsertDevice(c *gin.Context) {
	// The device identity is in the path, so the audit target names the concrete
	// device: a forensics query for "who rotated this device's secret" would
	// otherwise only see a generic entity name.
	deviceID := strings.TrimSpace(c.Param("deviceId"))
	target := "gb28181_device"
	if deviceID != "" {
		target = "gb28181_device:" + deviceID
	}
	audithttp.Tag(c, ActionDeviceUpsert, target, "Provision or update GB28181 device")

	if deviceID == "" {
		httputil.WriteError(c, apperr.Invalid(CodeDeviceIDInvalid, "deviceId must not be empty", nil))
		return
	}

	var req UpsertDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	device, err := h.service.UpsertDevice(c.Request.Context(), req.ToUpsert(deviceID))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	// Deliberately no password, no ciphertext, and no request body in this line.
	h.logger.Info("gb28181 device provisioned",
		zap.String("device_id", device.DeviceID),
		zap.String("transport", device.Transport),
		zap.Bool("password_rotated", req.Password != nil),
	)
	httputil.Success(c, toDeviceResponse(*device))
}
