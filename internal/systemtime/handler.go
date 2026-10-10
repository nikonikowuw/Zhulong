package systemtime

import (
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for system time management and hardware clock synchronization.
type Handler struct {
	service *TimeService
	logger  *zap.Logger
}

// NewHandler creates a new system time Handler.
func NewHandler(service *TimeService, logger *zap.Logger) *Handler {
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
	group := rg.Group("/system/time")
	{
		group.GET("", h.GetTime)
		group.PUT("/config", h.UpdateConfig)
		group.POST("/sync", h.SyncNow)
		group.POST("/manual", h.ManualSet)
	}
}

// GetTime godoc
// @Summary      Get system time status and clock health
// @Description  Retrieves current kernel time, active timezone, NTP candidate servers, synchronization state, and hardware RTC status.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=SystemTimeStatus}
// @Failure      500  {object}  httputil.Response
// @Router       /system/time [get]
func (h *Handler) GetTime(c *gin.Context) {
	status, err := h.service.GetStatus(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve system time status", err))
		return
	}
	httputil.Success(c, status)
}

// UpdateConfig godoc
// @Summary      Update system time configuration
// @Description  Updates synchronization mode (ntp/manual), NTP server pool, polling interval, and system IANA timezone.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      UpdateConfigRequest  true  "Time configuration parameters"
// @Success      200  {object}  httputil.Response{data=SystemTimeStatus}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/time/config [put]
func (h *Handler) UpdateConfig(c *gin.Context) {
	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	status, err := h.service.UpdateConfig(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_TIME_CONFIG", err.Error(), err))
		return
	}
	httputil.Success(c, status)
}

// SyncNow godoc
// @Summary      Trigger immediate NTP synchronization
// @Description  Forces an immediate NTP query and clock alignment attempt against configured NTP server pool.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=SystemTimeStatus}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/time/sync [post]
func (h *Handler) SyncNow(c *gin.Context) {
	status, err := h.service.SyncNow(c.Request.Context())
	if err != nil {
		if errors.Is(err, ErrSyncInProgress) {
			httputil.WriteError(c, httputil.NewError(http.StatusConflict, "SYNC_IN_PROGRESS", "NTP synchronization is already in progress", err))
			return
		}
		if errors.Is(err, ErrManualMode) {
			httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "MANUAL_MODE_ACTIVE", "Cannot perform NTP sync while in manual mode", err))
			return
		}
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "NTP_SYNC_FAILED", err.Error(), err))
		return
	}
	httputil.Success(c, status)
}

// ManualSet godoc
// @Summary      Manually set system clock or synchronize browser time
// @Description  Explicitly sets kernel CLOCK_REALTIME and commits new time to hardware RTC chip.
// @Tags         system
// @Accept       json
// @Produce      json
// @Param        body body      ManualTimeRequest  true  "Target ISO-8601 or RFC3339 timestamp"
// @Success      200  {object}  httputil.Response{data=SystemTimeStatus}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /system/time/manual [post]
func (h *Handler) ManualSet(c *gin.Context) {
	var req ManualTimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	targetTime, err := parseTimestamp(req.TargetTime)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_TIMESTAMP", "Target timestamp must be valid ISO-8601 or RFC3339 format", err))
		return
	}

	status, err := h.service.SetManualTime(c.Request.Context(), targetTime)
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "SET_TIME_FAILED", err.Error(), err))
		return
	}
	httputil.Success(c, status)
}

func parseTimestamp(ts string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}

	for _, layout := range formats {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("unrecognized timestamp format")
}
