package spatial

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler handles HTTP requests for the spatial domain.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates a new spatial Handler.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger,
	}
}

// RegisterRoutes registers endpoints that receive regular JSON/query requests.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/spatial")
	{
		group.GET("/maps", h.ListMaps)
		group.GET("/maps/:id", h.GetMap)
		group.PATCH("/maps/:id", h.UpdateMapName)
		group.DELETE("/maps/:id", h.DeleteMap)
		group.GET("/maps/:id/image", h.GetMapImage)
		group.HEAD("/maps/:id/image", h.GetMapImage)
		group.GET("/maps/:id/positions", h.GetPositions)
		group.PUT("/maps/:id/positions", h.SavePositions)
		group.GET("/trajectory", h.QueryTrajectory)
	}
}

// RegisterUploadRoutes registers large multipart file upload endpoints.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/spatial")
	{
		group.POST("/maps", h.UploadMap)
	}
}

type uploadRouteRegistrar struct {
	handler *Handler
}

func (r *uploadRouteRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	r.handler.RegisterUploadRoutes(rg)
}

// UploadRouteRegistrar returns an adapter for registering upload routes.
func (h *Handler) UploadRouteRegistrar() RouteRegistrar {
	return &uploadRouteRegistrar{handler: h}
}

// ListMaps godoc
// @Summary      List canvas maps
// @Tags         spatial
// @Produce      json
// @Success      200  {object}  httputil.Response{data=[]CanvasMapResponse}
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps [get]
//
// The payload is a bare array rather than the paginated envelope because an operator
// calibrates a handful of floor plans and the whole set is one canvas picker; use
// GET /spatial/maps/{id}/positions for the per-map list that can grow with the fleet.
func (h *Handler) ListMaps(c *gin.Context) {
	maps, err := h.service.ListMaps(c.Request.Context())
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, maps)
}

// GetMap godoc
// @Summary      Get canvas map metadata
// @Tags         spatial
// @Produce      json
// @Param        id path string true "Canvas map ID"
// @Success      200  {object}  httputil.Response{data=CanvasMapResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id} [get]
func (h *Handler) GetMap(c *gin.Context) {
	m, err := h.service.GetMap(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, m)
}

// UpdateMapName godoc
// @Summary      Rename a canvas map
// @Tags         spatial
// @Accept       json
// @Produce      json
// @Param        id      path string                  true "Canvas map ID"
// @Param        request body UpdateCanvasMapRequest  true "New map name"
// @Success      200  {object}  httputil.Response{data=CanvasMapResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id} [patch]
func (h *Handler) UpdateMapName(c *gin.Context) {
	var req UpdateCanvasMapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	updated, err := h.service.UpdateMapName(c.Request.Context(), c.Param("id"), req.Name)
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, updated)
}

// DeleteMap godoc
// @Summary      Delete a canvas map and its camera positions
// @Tags         spatial
// @Produce      json
// @Param        id path string true "Canvas map ID"
// @Success      200  {object}  httputil.Response{data=DeleteCanvasMapResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id} [delete]
func (h *Handler) DeleteMap(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.DeleteMap(c.Request.Context(), id); err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, DeleteCanvasMapResponse{ID: id})
}

// GetMapImage godoc
// @Summary      Serve the canvas map image bytes
// @Description  Returns the stored image as-is.
// @Description  The response carries sandboxing headers because the operator-supplied file may be an SVG,
// @Description  which can otherwise execute script on this origin.
// @Tags         spatial
// @Produce      image/png, image/jpeg, image/webp, image/svg+xml
// @Param        id path string true "Canvas map ID"
// @Success      200  {file}    file
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id}/image [get]
func (h *Handler) GetMapImage(c *gin.Context) {
	filePath, mimeType, err := h.service.GetMapFile(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	c.Header("Content-Type", mimeType)
	// The map belongs to the authenticated operator session, so a shared proxy must
	// not cache it; the file itself is immutable once uploaded.
	c.Header("Cache-Control", "private, max-age=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	// Neutralises script execution if the file is opened directly: an <img> tag
	// already refuses to run SVG script, navigating to the URL does not.
	c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	http.ServeFile(c.Writer, c.Request, filePath)
}

// UploadMap godoc
// @Summary      Upload a canvas map image
// @Tags         spatial
// @Accept       multipart/form-data
// @Produce      json
// @Param        name formData string true  "Canvas map name"
// @Param        file formData file   true  "Base map image (png, jpg, webp or svg)"
// @Success      201  {object}  httputil.Response{data=CanvasMapResponse}
// @Failure      413  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps [post]
func (h *Handler) UploadMap(c *gin.Context) {
	name := c.PostForm("name")

	fileHeader, err := c.FormFile("file")
	if err != nil {
		httputil.WriteErrorWithLogger(c, apperr.New(
			apperr.KindInvalid,
			"MAP_FILE_REQUIRED",
			"map image file is required",
			err,
		), h.logger)
		return
	}

	src, err := fileHeader.Open()
	if err != nil {
		httputil.WriteErrorWithLogger(c, apperr.New(
			apperr.KindInternal,
			"MAP_STORAGE_ERROR",
			"failed to read the uploaded file",
			err,
		), h.logger)
		return
	}
	defer func() {
		if closeErr := src.Close(); closeErr != nil {
			h.logger.Warn("failed to close uploaded canvas map file", zap.Error(closeErr))
		}
	}()

	// The name is validated by the service, which owns the rune limit: pre-checking
	// emptiness here would be a second copy of the same rule.
	res, err := h.service.CreateMap(c.Request.Context(), name, src, fileHeader.Filename)
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusCreated, res)
}

// GetPositions godoc
// @Summary      List calibrated camera positions on a canvas map
// @Tags         spatial
// @Produce      json
// @Param        id path string true "Canvas map ID"
// @Success      200  {object}  httputil.Response{data=[]CameraPositionInfo}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id}/positions [get]
//
// Bare array for the same reason as ListMaps: the set is bounded by the cameras an
// operator placed on one canvas, and the calibration UI always needs all of them.
func (h *Handler) GetPositions(c *gin.Context) {
	positions, err := h.service.GetPositions(c.Request.Context(), c.Param("id"))
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, positions)
}

// SavePositions godoc
// @Summary      Replace all camera positions on a canvas map
// @Tags         spatial
// @Accept       json
// @Produce      json
// @Param        id      path string                      true "Canvas map ID"
// @Param        request body BatchSavePositionsRequest   true "Positions to store; an empty array unbinds every camera"
// @Success      200  {object}  httputil.Response{data=SaveCameraPositionsResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/maps/{id}/positions [put]
func (h *Handler) SavePositions(c *gin.Context) {
	mapID := c.Param("id")
	var req BatchSavePositionsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	count, err := h.service.SavePositions(c.Request.Context(), mapID, req)
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, SaveCameraPositionsResponse{MapID: mapID, Count: count})
}

// QueryTrajectory godoc
// @Summary      Query a cross-camera spatio-temporal trajectory
// @Tags         spatial
// @Produce      json
// @Param        mapId      query string true  "Canvas map ID"
// @Param        targetType query string true  "Target type" Enums(vehicle, person)
// @Param        plate      query string false "Vehicle plate (required when targetType=vehicle)"
// @Param        personId   query string false "Person ID or external person key (required when targetType=person)"
// @Param        startTime  query string true  "Range start, RFC3339"
// @Param        endTime    query string true  "Range end, RFC3339"
// @Success      200  {object}  httputil.Response{data=TrajectoryResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /spatial/trajectory [get]
func (h *Handler) QueryTrajectory(c *gin.Context) {
	var q TrajectoryQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	res, err := h.service.QueryTrajectory(c.Request.Context(), q)
	if err != nil {
		httputil.WriteErrorWithLogger(c, err, h.logger)
		return
	}
	httputil.Success(c, res)
}
