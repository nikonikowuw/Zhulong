package openapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

// Handler handles third-party OpenAPI provisioning requests.
type Handler struct {
	personService *person.Service
	limiter       *UploadLimiter
	logger        *zap.Logger
}

// NewHandler constructs a new OpenAPI handler.
func NewHandler(personService *person.Service, limiter *UploadLimiter, logger *zap.Logger) *Handler {
	if limiter == nil {
		limiter = NewUploadLimiter()
	}
	return &Handler{
		personService: personService,
		limiter:       limiter,
		logger:        logger,
	}
}

// RegisterRoutes mounts all OpenAPI endpoints onto the provided router group.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	jsonLimit := httpmiddleware.MaxBodyLimitWithLimit(1 * 1024 * 1024)

	// Person routes
	rg.POST("/persons/:personId", jsonLimit, h.UpsertPerson)
	rg.GET("/persons/:personId", h.GetPerson)
	rg.DELETE("/persons/:personId", h.DeletePerson)

	// Face routes
	rg.POST("/persons/:personId/faces", h.limiter.Middleware(), h.UploadFace)
	rg.DELETE("/persons/:personId/faces", h.ClearFaces)
	rg.DELETE("/persons/:personId/faces/:faceId", h.DeleteFace)
	rg.PUT("/persons/:personId/faces/:faceId/primary", jsonLimit, h.SetPrimaryFace)
}

// UpsertPerson creates or updates a person by external personId.
// @Summary      Create or update person
// @Description  Provisions a person by their external personId. Returns 201 on creation and 200 on update.
// @Tags         OpenAPI
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string               true  "External Person ID"
// @Param        body      body      UpsertPersonRequest  true  "Person payload"
// @Success      200       {object}  httputil.Response{data=PersonProvisionResponse}
// @Success      201       {object}  httputil.Response{data=PersonProvisionResponse}
// @Failure      400       {object}  httputil.Response
// @Failure      401       {object}  httputil.Response
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId} [post]
func (h *Handler) UpsertPerson(c *gin.Context) {
	personID := c.Param("personId")
	var req UpsertPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	tokenName := "API"
	if identity, ok := apitoken.GetIdentity(c); ok && identity.Name != "" {
		tokenName = identity.Name
	}

	p, created, err := h.personService.UpsertPersonByExternalID(c.Request.Context(), personID, req.Name, tokenName)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	// Fetch complete person with faces for the response
	fullPerson, err := h.personService.GetPersonByExternalID(c.Request.Context(), personID)
	if err != nil {
		fullPerson = p
	}

	resp := newPersonProvisionResponse(fullPerson)
	if created {
		httputil.SuccessWithStatus(c, http.StatusCreated, resp)
		return
	}
	httputil.Success(c, resp)
}

// GetPerson retrieves person details and their enrolled faces by external personId.
// @Summary      Get person by external ID
// @Description  Retrieves person metadata and enrolled faces.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true  "External Person ID"
// @Success      200       {object}  httputil.Response{data=PersonProvisionResponse}
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId} [get]
func (h *Handler) GetPerson(c *gin.Context) {
	personID := c.Param("personId")
	p, err := h.personService.GetPersonByExternalID(c.Request.Context(), personID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, newPersonProvisionResponse(p))
}

// DeletePerson deletes a person and all their faces by external personId.
// @Summary      Delete person
// @Description  Deletes a person, their enrolled faces, and algorithm gallery entries.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true  "External Person ID"
// @Success      200       {object}  httputil.Response{data=DeleteResponse}
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId} [delete]
func (h *Handler) DeletePerson(c *gin.Context) {
	personID := c.Param("personId")
	if err := h.personService.DeletePersonByExternalID(c.Request.Context(), personID); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, DeleteResponse{Deleted: true})
}

// UploadFace uploads or replaces a face photo for a person.
// @Summary      Upload or replace face
// @Description  Uploads an enrolled face photo. If replace=true query param is set, existing faces are atomically replaced.
// @Tags         OpenAPI
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true   "External Person ID"
// @Param        replace   query     bool    false  "Replace all existing faces (default false)"
// @Param        image     formData  file    true   "Face photo file (JPG, PNG, WebP)"
// @Success      201       {object}  httputil.Response{data=FaceUploadResponse}
// @Failure      400       {object}  httputil.Response
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      409       {object}  httputil.Response
// @Failure      413       {object}  httputil.Response
// @Failure      422       {object}  httputil.Response
// @Failure      429       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId}/faces [post]
func (h *Handler) UploadFace(c *gin.Context) {
	personID := c.Param("personId")
	file, err := c.FormFile("image")
	if err != nil {
		httputil.WriteError(c, apperr.Invalid("FILE_REQUIRED", "Face image file 'image' is required", err))
		return
	}

	if file.Size > person.MaxFaceImageBytes {
		httputil.WriteError(c, apperr.Invalid("FILE_TOO_LARGE", "File size exceeds 10MB limit", nil))
		return
	}

	src, err := file.Open()
	if err != nil {
		httputil.WriteError(c, apperr.Invalid("FILE_READ_FAILED", "Failed to open uploaded file", err))
		return
	}
	defer src.Close()

	replace := c.Query("replace") == "true"
	face, wasReplaced, err := h.personService.AddFaceByExternalID(c.Request.Context(), personID, file.Filename, src, replace)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	// Determine if this face became the primary face
	p, err := h.personService.GetPersonByExternalID(c.Request.Context(), personID)
	isPrimary := err == nil && p.PrimaryFaceID != nil && *p.PrimaryFaceID == face.ID

	httputil.SuccessWithStatus(c, http.StatusCreated, FaceUploadResponse{
		FaceID:    face.ID,
		Replaced:  wasReplaced,
		IsPrimary: isPrimary,
		CreatedAt: face.CreatedAt,
	})
}

// ClearFaces deletes all enrolled faces for a person.
// @Summary      Clear all faces
// @Description  Deletes all enrolled faces for a person without deleting the person record itself.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true  "External Person ID"
// @Success      200       {object}  httputil.Response{data=FaceClearResponse}
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId}/faces [delete]
func (h *Handler) ClearFaces(c *gin.Context) {
	personID := c.Param("personId")
	cleared, err := h.personService.ClearFacesByExternalID(c.Request.Context(), personID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, FaceClearResponse{Cleared: cleared})
}

// DeleteFace deletes a specific face template of a person.
// @Summary      Delete specific face
// @Description  Deletes a single face template by its ID.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true  "External Person ID"
// @Param        faceId    path      string  true  "Face template ID"
// @Success      200       {object}  httputil.Response{data=DeleteResponse}
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId}/faces/{faceId} [delete]
func (h *Handler) DeleteFace(c *gin.Context) {
	personID := c.Param("personId")
	faceID := c.Param("faceId")
	if err := h.personService.DeleteFaceByExternalID(c.Request.Context(), personID, faceID); err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, DeleteResponse{Deleted: true})
}

// SetPrimaryFace designates a face template as the primary face of a person.
// @Summary      Set primary face
// @Description  Sets an enrolled face template as the primary face for the person.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Param        personId  path      string  true  "External Person ID"
// @Param        faceId    path      string  true  "Face template ID"
// @Success      200       {object}  httputil.Response{data=PersonProvisionResponse}
// @Failure      401       {object}  httputil.Response
// @Failure      404       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /api/v1/openapi/persons/{personId}/faces/{faceId}/primary [put]
func (h *Handler) SetPrimaryFace(c *gin.Context) {
	personID := c.Param("personId")
	faceID := c.Param("faceId")
	p, err := h.personService.SetPrimaryFaceByExternalID(c.Request.Context(), personID, faceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	// Fetch complete person with faces for the response
	fullPerson, err := h.personService.GetPersonByExternalID(c.Request.Context(), personID)
	if err != nil {
		fullPerson = p
	}
	httputil.Success(c, newPersonProvisionResponse(fullPerson))
}
