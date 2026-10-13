package person

import (
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

// Handler exposes RESTful API endpoints for the person and face gallery domain.
type Handler struct {
	service *Service
	logger  *zap.Logger
}

// NewHandler creates a new Handler instance.
func NewHandler(service *Service, logger *zap.Logger) *Handler {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Handler{
		service: service,
		logger:  logger.Named("person_handler"),
	}
}

// RegisterRoutes registers endpoints to the Gin router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/persons")
	{
		group.GET("", h.List)
		group.POST("", h.Create)
		group.GET("/:id", h.Get)
		group.PUT("/:id", h.Update)
		group.DELETE("/:id", h.Delete)

		// Face template sub-resources (一人多照)
		group.GET("/:id/faces", h.ListFaces)
		group.POST("/:id/faces", h.AddFace)
		group.DELETE("/:id/faces/:face_id", h.DeleteFace)
	}
}

// List godoc
// @Summary      List enrolled persons
// @Tags         person
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        limit query int false "Legacy pagination limit"
// @Param        offset query int false "Legacy pagination offset"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[Person]}
// @Failure      500  {object}  httputil.Response
// @Router       /persons [get]
func (h *Handler) List(c *gin.Context) {
	var query httputil.PaginationQuery
	_ = c.ShouldBindQuery(&query)
	page, pageSize, offset, limit := query.Normalize()

	persons, total, err := h.service.ListPersons(c.Request.Context(), limit, offset)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, persons, total, page, pageSize)
}

// Create godoc
// @Summary      Create a new person profile
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        request body CreatePersonRequest true "Person creation payload"
// @Success      201  {object}  httputil.Response{data=Person}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons [post]
func (h *Handler) Create(c *gin.Context) {
	var req CreatePersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	p, err := h.service.CreatePerson(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusCreated, p)
}

// Get godoc
// @Summary      Get person profile by ID
// @Tags         person
// @Produce      json
// @Param        id path string true "Person ID"
// @Success      200  {object}  httputil.Response{data=Person}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id} [get]
func (h *Handler) Get(c *gin.Context) {
	id := c.Param("id")
	p, err := h.service.GetPerson(c.Request.Context(), id)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, p)
}

// Update godoc
// @Summary      Update person profile metadata
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        request body UpdatePersonRequest true "Person update payload"
// @Success      200  {object}  httputil.Response{data=Person}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id} [put]
func (h *Handler) Update(c *gin.Context) {
	id := c.Param("id")
	var req UpdatePersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	p, err := h.service.UpdatePerson(c.Request.Context(), id, req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, p)
}

// Delete godoc
// @Summary      Delete person profile and face templates
// @Tags         person
// @Produce      json
// @Param        id path string true "Person ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id} [delete]
func (h *Handler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := h.service.DeletePerson(c.Request.Context(), id); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}

// ListFaces godoc
// @Summary      List face templates for a person
// @Tags         person
// @Produce      json
// @Param        id path string true "Person ID"
// @Success      200  {object}  httputil.Response{data=[]Face}
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces [get]
func (h *Handler) ListFaces(c *gin.Context) {
	personID := c.Param("id")
	faces, err := h.service.ListFaces(c.Request.Context(), personID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	if faces == nil {
		faces = make([]Face, 0)
	}
	httputil.Success(c, faces)
}

// AddFace godoc
// @Summary      Enroll a new face template for a person
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        request body AddFaceRequest true "Face enrollment payload"
// @Success      201  {object}  httputil.Response{data=Face}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces [post]
//
// TODO(api/storage): 待重构为 multipart/form-data 文件流上传，解耦浏览器与服务端文件路径。
func (h *Handler) AddFace(c *gin.Context) {
	personID := c.Param("id")
	var req AddFaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err, reflect.TypeOf(req))
		return
	}

	face, err := h.service.AddFace(c.Request.Context(), personID, req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusCreated, face)
}

// DeleteFace godoc
// @Summary      Delete a face template
// @Tags         person
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        face_id path string true "Face template ID"
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces/{face_id} [delete]
func (h *Handler) DeleteFace(c *gin.Context) {
	personID := c.Param("id")
	faceID := c.Param("face_id")
	if err := h.service.DeleteFace(c.Request.Context(), personID, faceID); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}
