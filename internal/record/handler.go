package record

import (
	"time"

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
		group.GET("/alarms/:id", h.GetAlarm)
		group.GET("/captures", h.ListCaptures)
		group.GET("/captures/:id", h.GetCapture)
	}
}

// ListRecognitionsQuery defines query parameters for listing recognitions.
type ListRecognitionsQuery struct {
	httputil.PaginationQuery
	CameraID  string `form:"cameraId"`
	PersonID  string `form:"personId"`
	Matched   *bool  `form:"matched"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// ToFilter converts query parameters into a validated RecognitionFilter.
func (q *ListRecognitionsQuery) ToFilter() (RecognitionFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return RecognitionFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return RecognitionFilter{}, err
	}
	return RecognitionFilter{
		CameraID:  q.CameraID,
		PersonID:  q.PersonID,
		Matched:   q.Matched,
		StartTime: start,
		EndTime:   end,
		Page:      page,
		PageSize:  pageSize,
	}, nil
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
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[RecognitionDTO]}
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
// @Success      200 {object} httputil.Response{data=RecognitionDTO}
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

// ListAlarmsQuery defines query parameters for listing alarms.
type ListAlarmsQuery struct {
	httputil.PaginationQuery
	CameraID  string `form:"cameraId"`
	AlarmType string `form:"alarmType"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// ToFilter converts query parameters into a validated AlarmFilter.
func (q *ListAlarmsQuery) ToFilter() (AlarmFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return AlarmFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return AlarmFilter{}, err
	}
	return AlarmFilter{
		CameraID:  q.CameraID,
		AlarmType: q.AlarmType,
		StartTime: start,
		EndTime:   end,
		Page:      page,
		PageSize:  pageSize,
	}, nil
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
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[AlarmDTO]}
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
// @Success      200 {object} httputil.Response{data=AlarmDTO}
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

// ListCapturesQuery defines query parameters for listing captures.
type ListCapturesQuery struct {
	httputil.PaginationQuery
	CameraID   string `form:"cameraId"`
	TargetType string `form:"targetType"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// ToFilter converts query parameters into a validated CaptureFilter.
func (q *ListCapturesQuery) ToFilter() (CaptureFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return CaptureFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return CaptureFilter{}, err
	}
	return CaptureFilter{
		CameraID:   q.CameraID,
		TargetType: q.TargetType,
		StartTime:  start,
		EndTime:    end,
		Page:       page,
		PageSize:   pageSize,
	}, nil
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
// @Success      200 {object} httputil.Response{data=httputil.PaginatedData[CaptureDTO]}
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
// @Success      200 {object} httputil.Response{data=CaptureDTO}
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

func parseTime(raw, fieldName string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
			{Field: fieldName, Code: "INVALID_TIME_FORMAT", Message: fieldName + " must be in RFC3339 format"},
		})
	}
	utc := t.UTC()
	return &utc, nil
}
