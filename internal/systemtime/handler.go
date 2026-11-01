package systemtime

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
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

// RegisterRoutes mounts the time endpoints. It satisfies the package's
// RouteRegistrar contract (module.go), which is what the Fx protected_routes
// value group consumes.
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
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /system/time [get]
func (h *Handler) GetTime(c *gin.Context) {
	status, err := h.service.GetStatus(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
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
// @Failure      422  {object}  httputil.Response  "VALIDATION_FAILED or INVALID_TIME_CONFIG"
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /system/time/config [put]
func (h *Handler) UpdateConfig(c *gin.Context) {
	audithttp.Tag(c, ActionTimeUpdateConfig, "system_time", "Update time configuration")

	var req UpdateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	status, err := h.service.UpdateConfig(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	// Whitelisted summary: the request fields are non-sensitive configuration.
	audithttp.TagDetail(c, fmt.Sprintf("mode=%s, servers=%v, interval=%ds, tz=%s",
		req.Mode, req.NTPServers, req.SyncIntervalSeconds, req.Timezone))
	httputil.Success(c, status)
}

// SyncNow godoc
// @Summary      Trigger immediate NTP synchronization
// @Description  Forces an immediate NTP query and clock alignment attempt against configured NTP server pool.
// @Tags         system
// @Produce      json
// @Success      200  {object}  httputil.Response{data=SystemTimeStatus}
// @Failure      409  {object}  httputil.Response  "SYNC_IN_PROGRESS"
// @Failure      412  {object}  httputil.Response  "MANUAL_MODE_ACTIVE"
// @Failure      500  {object}  httputil.Response  "DATABASE_ERROR"
// @Router       /system/time/sync [post]
func (h *Handler) SyncNow(c *gin.Context) {
	audithttp.Tag(c, ActionTimeNTPSync, "system_time", "Manual NTP sync")

	status, err := h.service.SyncNow(c.Request.Context())
	if err != nil {
		// The service returns apperr values, so the status code follows from the
		// domain Kind (conflict / precondition / internal) instead of a local
		// status table that can drift from the documented contract.
		httputil.WriteError(c, err)
		return
	}
	// The endpoint keeps its 200 contract even when the probe failed, so the
	// domain outcome is reported explicitly: without this the audit record would
	// claim success for a sync that never happened.
	if status != nil && status.SyncStatus.State == SyncStateFailed {
		audithttp.TagError(c, "NTP_SYNC_FAILED", "NTP synchronization failed")
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
// @Failure      422  {object}  httputil.Response  "VALIDATION_FAILED or INVALID_TIMESTAMP"
// @Failure      500  {object}  httputil.Response  "SET_TIME_FAILED"
// @Router       /system/time/manual [post]
func (h *Handler) ManualSet(c *gin.Context) {
	audithttp.Tag(c, ActionTimeManualSet, "system_time", "Set system time manually")

	var req ManualTimeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	targetTime, err := parseTimestamp(req.TargetTime)
	if err != nil {
		httputil.WriteError(c, apperr.Invalid("INVALID_TIMESTAMP",
			"Target timestamp must be a valid ISO-8601 or RFC3339 timestamp", err))
		return
	}

	status, err := h.service.SetManualTime(c.Request.Context(), targetTime)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	audithttp.TagDetail(c, "target="+targetTime.Format(time.RFC3339Nano))
	httputil.Success(c, status)
}

// timestampLayouts lists the accepted wire formats, most specific first.
// RFC3339Nano precedes RFC3339 because time.Parse rejects the latter once
// fractional seconds are present.
var timestampLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
}

func parseTimestamp(ts string) (time.Time, error) {
	for _, layout := range timestampLayouts {
		if t, err := time.Parse(layout, ts); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognized timestamp format %q", ts)
}
