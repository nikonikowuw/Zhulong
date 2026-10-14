package person

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
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
		group.DELETE("/:id/faces/:face_id", h.DeleteFace)
		group.GET("/:id/faces/:face_id/image", h.GetFaceImage)
	}
}

// RegisterUploadRoutes registers large-body upload endpoints on the upload router group.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
	rg.POST("/persons/:id/faces", h.AddFace)
}

type uploadRouteRegistrar struct {
	handler *Handler
}

func (r *uploadRouteRegistrar) RegisterRoutes(rg *gin.RouterGroup) {
	r.handler.RegisterUploadRoutes(rg)
}

// UploadRouteRegistrar returns a RouteRegistrar adapter for upload endpoints.
func (h *Handler) UploadRouteRegistrar() RouteRegistrar {
	return &uploadRouteRegistrar{handler: h}
}

type listPersonsQuery struct {
	httputil.PaginationQuery
	Search string `form:"search"`
}

// List godoc
// @Summary      List enrolled persons
// @Tags         person
// @Produce      json
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        limit query int false "Legacy pagination limit"
// @Param        offset query int false "Legacy pagination offset"
// @Param        search query string false "Search keyword for name or personId"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[Person]}
// @Failure      500  {object}  httputil.Response
// @Router       /persons [get]
func (h *Handler) List(c *gin.Context) {
	var query listPersonsQuery
	_ = c.ShouldBindQuery(&query)
	page, pageSize, offset, limit := query.Normalize()

	persons, total, err := h.service.ListPersons(c.Request.Context(), limit, offset, query.Search)
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
	// Declared before binding so a rejected payload is still attributable.
	audithttp.Tag(c, ActionPersonCreate, "person:", "Create person")

	var req CreatePersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	p, err := h.service.CreatePerson(c.Request.Context(), req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.TagTarget(c, "person:"+p.ID)
	audithttp.TagDetail(c, "Create person "+p.Name)
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
	audithttp.Tag(c, ActionPersonUpdate, "person:"+id, "Update person "+id)

	var req UpdatePersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	p, err := h.service.UpdatePerson(c.Request.Context(), id, req)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	audithttp.TagDetail(c, "Update person "+p.Name)
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
	audithttp.Tag(c, ActionPersonDelete, "person:"+id, "Delete person "+id)

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
// @Accept       multipart/form-data
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        file formData file true "Face photo image file (JPG, PNG, WebP)"
// @Success      201  {object}  httputil.Response{data=Face}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces [post]
func (h *Handler) AddFace(c *gin.Context) {
	personID := c.Param("id")
	// The enrollment photo path and features are deliberately excluded from the
	// detail: only the owning person is named until the face row exists.
	audithttp.Tag(c, ActionPersonFaceAdd, "person:"+personID, "Add face for person "+personID)

	var req AddFaceRequest
	if err := c.ShouldBind(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	face, err := h.service.AddFace(c.Request.Context(), personID, req.File)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.TagTarget(c, "face:"+face.ID)
	httputil.SuccessWithStatus(c, http.StatusCreated, face)
}

// GetFaceImage godoc
// @Summary      Get face photo image file
// @Tags         person
// @Produce      image/jpeg,image/png,image/webp
// @Param        id path string true "Person ID"
// @Param        face_id path string true "Face template ID"
// @Success      200  {file}    binary
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces/{face_id}/image [get]
func (h *Handler) GetFaceImage(c *gin.Context) {
	personID := c.Param("id")
	faceID := c.Param("face_id")

	imgPath, err := h.service.GetFaceImagePath(c.Request.Context(), personID, faceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	if _, err := os.Stat(imgPath); err != nil {
		httputil.WriteError(c, apperr.NotFound("IMAGE_NOT_FOUND", "image file not found on disk", err))
		return
	}

	c.File(imgPath)
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
	audithttp.Tag(c, ActionPersonFaceDelete, "face:"+faceID, "Delete face "+faceID)

	if err := h.service.DeleteFace(c.Request.Context(), personID, faceID); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, gin.H{"deleted": true})
}
