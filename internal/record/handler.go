package record

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler exposes RESTful HTTP endpoints for perception records and alarms.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler constructs a record Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger.Named("record.handler"),
	}
}

// RegisterRoutes registers record routes onto the protected router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/records")
	{
		group.GET("/recognitions", h.ListRecognitions)
		group.GET("/recognitions/:id", h.GetRecognition)
		group.GET("/alarms", h.ListAlarms)
		group.GET("/alarms/alarm-types", h.ListAlarmTypes)
		group.GET("/alarms/:id", h.GetAlarm)
		group.GET("/captures", h.ListCaptures)
		group.GET("/captures/target-types", h.ListCaptureTargetTypes)
		group.GET("/captures/:id", h.GetCapture)
	}
}

// ListRecognitions godoc
// @Summary      List face recognitions with filtering and pagination
// @Tags         records
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        cameraId query string false "Filter by camera ID"
// @Param        personId query string false "Filter by person ID"
// @Param        matched query bool false "Filter by match status"
// @Param        startTime query string false "Start time (RFC3339)"
// @Param        endTime query string false "End time (RFC3339)"
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[RecognitionResponse]}
// @Router       /records/recognitions [get]
func (h *Handler) ListRecognitions(c *gin.Context) {
	var q ListRecognitionsQuery
	_ = c.ShouldBindQuery(&q)

	filter, err := q.ToFilter()
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	items, total, err := h.service.ListRecognitions(c.Request.Context(), filter)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, items, total, filter.Page, filter.PageSize)
}

// GetRecognition godoc
// @Summary      Get a single recognition record by ID
// @Tags         records
// @Produce      json
// @Param        id path string true "Recognition record ID"
// @Success      200 {object} httputil.Response{data=RecognitionResponse}
// @Failure      404 {object} httputil.Response
// @Router       /records/recognitions/{id} [get]
func (h *Handler) GetRecognition(c *gin.Context) {
	item, err := h.service.GetRecognition(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, item)
}

// ListAlarms godoc
// @Summary      List perception alarms with filtering and pagination
// @Tags         records
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        cameraId query string false "Filter by camera ID"
// @Param        alarmType query string false "Filter by alarm type"
// @Param        startTime query string false "Start time (RFC3339)"
// @Param        endTime query string false "End time (RFC3339)"
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[AlarmResponse]}
// @Router       /records/alarms [get]
func (h *Handler) ListAlarms(c *gin.Context) {
	var q ListAlarmsQuery
	_ = c.ShouldBindQuery(&q)

	filter, err := q.ToFilter()
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	items, total, err := h.service.ListAlarms(c.Request.Context(), filter)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, items, total, filter.Page, filter.PageSize)
}

// GetAlarm godoc
// @Summary      Get a single alarm record by ID
// @Tags         records
// @Produce      json
// @Param        id path string true "Alarm record ID"
// @Success      200 {object} httputil.Response{data=AlarmResponse}
// @Failure      404 {object} httputil.Response
// @Router       /records/alarms/{id} [get]
func (h *Handler) GetAlarm(c *gin.Context) {
	item, err := h.service.GetAlarm(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, item)
}

// ListCaptures godoc
// @Summary      List captures with filtering and pagination
// @Tags         records
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        cameraId query string false "Filter by camera ID"
// @Param        targetType query string false "Filter by target type"
// @Param        startTime query string false "Start time (RFC3339)"
// @Param        endTime query string false "End time (RFC3339)"
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[CaptureResponse]}
// @Router       /records/captures [get]
func (h *Handler) ListCaptures(c *gin.Context) {
	var q ListCapturesQuery
	_ = c.ShouldBindQuery(&q)

	filter, err := q.ToFilter()
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	items, total, err := h.service.ListCaptures(c.Request.Context(), filter)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, items, total, filter.Page, filter.PageSize)
}

// GetCapture godoc
// @Summary      Get a single capture record by ID
// @Tags         records
// @Produce      json
// @Param        id path string true "Capture record ID"
// @Success      200 {object} httputil.Response{data=CaptureResponse}
// @Failure      404 {object} httputil.Response
// @Router       /records/captures/{id} [get]
func (h *Handler) GetCapture(c *gin.Context) {
	item, err := h.service.GetCapture(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, item)
}

// ListAlarmTypes godoc
// @Summary      List all distinct alarm types present in records
// @Tags         records
// @Produce      json
// @Success      200 {object} httputil.Response{data=[]string}
// @Router       /records/alarms/alarm-types [get]
func (h *Handler) ListAlarmTypes(c *gin.Context) {
	types, err := h.service.ListAlarmTypes(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, types)
}

// ListCaptureTargetTypes godoc
// @Summary      List all distinct capture target types present in records
// @Tags         records
// @Produce      json
// @Success      200 {object} httputil.Response{data=[]string}
// @Router       /records/captures/target-types [get]
func (h *Handler) ListCaptureTargetTypes(c *gin.Context) {
	types, err := h.service.ListTargetTypes(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, types)
}
