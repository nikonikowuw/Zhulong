package systeminfo

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for system hardware monitoring.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates a new systeminfo Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes mounts the hardware monitoring endpoints onto the protected route group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/system/hardware")
	{
		group.GET("", h.GetHardware)
	}
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
