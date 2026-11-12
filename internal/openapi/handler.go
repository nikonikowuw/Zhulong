package openapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/person"
)

// defaultCallerName is the audit actor recorded when the authenticated token carries no
// name. Every provisioning call is attributed to the token, so the fallback has to be a
// stable, recognizable value rather than an empty source.
const defaultCallerName = "API"

// StatusProvider supplies a sanitized point-in-time host health and workload snapshot.
type StatusProvider interface {
	DeviceStatus(ctx context.Context) (DeviceStatusResponse, error)
}

// HandlerOption configures optional dependencies on Handler.
type HandlerOption func(*Handler)

// WithStatusProvider wires the read-only device status provider into Handler.
func WithStatusProvider(provider StatusProvider) HandlerOption {
	return func(h *Handler) {
		h.statusProvider = provider
	}
}

// Handler handles third-party OpenAPI provisioning and liveness requests.
type Handler struct {
	personService  *person.Service
	limiter        *UploadLimiter
	statusProvider StatusProvider
}

// NewHandler constructs a new OpenAPI handler.
func NewHandler(
	personService *person.Service,
	limiter *UploadLimiter,
	opts ...HandlerOption,
) *Handler {
	if limiter == nil {
		limiter = NewUploadLimiter()
	}
	h := &Handler{
		personService: personService,
		limiter:       limiter,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// RegisterRoutes mounts all OpenAPI endpoints onto the provided router group.
//
// The group is sized for enrollment photos by internal/app/router.go. The JSON
// endpoints are nested under their own 1 MiB ceiling so an oversized body is rejected
// before it is buffered, and so the answer stays PAYLOAD_TOO_LARGE rather than the
// photo-sized limit.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	jsonEndpoints := rg.Group("")
	jsonEndpoints.Use(httpmiddleware.MaxBodyLimit())

	// Liveness and operational status (read-only; never touches the upload limiter or audit log).
	rg.GET("/status", h.GetStatus)

	// Person routes
	jsonEndpoints.POST("/persons/:personId", h.UpsertPerson)
	rg.GET("/persons/:personId", h.GetPerson)
	rg.DELETE("/persons/:personId", h.DeletePerson)

	// Face routes. The limiter decorates the enrollment endpoint alone: mounting it on
	// the whole group would answer 429 for the clear/delete calls that drain a saturated
	// upload pipeline.
	rg.POST("/persons/:personId/faces", h.limiter.Middleware(), h.UploadFace)
	rg.DELETE("/persons/:personId/faces", h.ClearFaces)
	rg.DELETE("/persons/:personId/faces/:faceId", h.DeleteFace)
	jsonEndpoints.PUT("/persons/:personId/faces/:faceId/primary", h.SetPrimaryFace)
}

// GetStatus returns a sanitized point-in-time liveness and workload snapshot.
// @Summary      Get device health and workload status
// @Description  Returns a sanitized point-in-time liveness, resource, storage, and workload snapshot for this host.
// @Description  Degraded subsystems still return HTTP 200 with status="degraded" and machine-readable reasons.
// @Tags         OpenAPI
// @Produce      json
// @Security     BearerAuth
// @Success      200  {object}  httputil.Response{data=DeviceStatusResponse}
// @Failure      401  {object}  httputil.Response
// @Failure      503  {object}  httputil.Response
// @Router       /openapi/status [get]
func (h *Handler) GetStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if h.statusProvider == nil {
		httputil.WriteError(
			c,
			apperr.Unavailable("SERVICE_UNAVAILABLE", "Device status service is unavailable", nil),
		)
		return
	}

	resp, err := h.statusProvider.DeviceStatus(c.Request.Context())
	if err != nil {
		var appErr *apperr.Error
		if errors.As(err, &appErr) {
			httputil.WriteError(c, appErr)
			return
		}
		httputil.WriteError(
			c,
			apperr.Unavailable("SERVICE_UNAVAILABLE", "Device status snapshot is unavailable", err),
		)
		return
	}

	resp.ReportedAt = resp.ReportedAt.UTC()
	if resp.Reasons == nil {
		resp.Reasons = []string{}
	}
	if resp.Status == "" {
		resp.Status = "ready"
	}
	httputil.Success(c, resp)
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
// @Failure      413       {object}  httputil.Response
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId} [post]
func (h *Handler) UpsertPerson(c *gin.Context) {
	personID := c.Param("personId")
	var req UpsertPersonRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	p, created, err := h.personService.UpsertPersonByExternalID(
		c.Request.Context(),
		personID,
		req.Name,
		callerName(c),
	)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	resp := newPersonProvisionResponse(p)
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
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId} [get]
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
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId} [delete]
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
// @Description  Uploads an enrolled face photo. With replace=true the person's existing
// @Description  faces are atomically replaced; otherwise the photo is appended.
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
// @Router       /openapi/persons/{personId}/faces [post]
func (h *Handler) UploadFace(c *gin.Context) {
	personID := c.Param("personId")

	// Parsed before the body is touched: an unreadable flag must be answered without
	// buffering the upload it would have applied to.
	replace, err := parseReplaceFlag(c.Query("replace"))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	file, err := c.FormFile("image")
	if err != nil {
		writeUploadFormError(c, err)
		return
	}

	// The service answers 413 for the same limit; this early check only avoids reading
	// an obviously oversized file into the enrollment buffer.
	if file.Size > person.MaxFaceImageBytes {
		httputil.WriteError(c, apperr.PayloadTooLarge(
			"FILE_TOO_LARGE",
			"File size exceeds the 10MB limit",
			nil,
		))
		return
	}

	src, err := file.Open()
	if err != nil {
		httputil.WriteError(c, apperr.Invalid("FILE_READ_FAILED", "Failed to open uploaded file", err))
		return
	}
	defer src.Close()

	enrollment, err := h.personService.AddFaceByExternalID(c.Request.Context(), personID, person.FaceUpload{
		Filename: file.Filename,
		Content:  src,
		Replace:  replace,
	})
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusCreated, FaceUploadResponse{
		FaceID:    enrollment.Face.ID,
		Replaced:  enrollment.Replaced,
		IsPrimary: enrollment.IsPrimary,
		CreatedAt: enrollment.Face.CreatedAt,
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
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId}/faces [delete]
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
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId}/faces/{faceId} [delete]
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
// @Failure      422       {object}  httputil.Response
// @Failure      500       {object}  httputil.Response
// @Router       /openapi/persons/{personId}/faces/{faceId}/primary [put]
func (h *Handler) SetPrimaryFace(c *gin.Context) {
	personID := c.Param("personId")
	faceID := c.Param("faceId")

	// The service reloads the person with its face summary, so the response is the same
	// projection GET answers without a second read here.
	p, err := h.personService.SetPrimaryFaceByExternalID(c.Request.Context(), personID, faceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, newPersonProvisionResponse(p))
}

// callerName is the audit actor for a provisioning call: the token's own name, or the
// stable fallback when the token has none.
func callerName(c *gin.Context) string {
	if identity, ok := apitoken.GetIdentity(c); ok && identity.Name != "" {
		return identity.Name
	}
	return defaultCallerName
}

// parseReplaceFlag decodes the replace query parameter.
//
// The flag switches the endpoint between appending a template and atomically dropping
// the person's existing ones, so an unparsable value must not be guessed: silently
// treating "TRUE" or "yes" as false would append a face the caller believed it was
// replacing.
func parseReplaceFlag(raw string) (bool, error) {
	if raw == "" {
		return false, nil
	}
	replace, err := strconv.ParseBool(raw)
	if err != nil {
		return false, apperr.Invalid(
			"REPLACE_INVALID",
			"replace must be a boolean (true or false)",
			err,
		)
	}
	return replace, nil
}

// writeUploadFormError maps a multipart parse failure onto the documented contract.
//
// The group-level body limit aborts the parse for an oversized request, and that
// surfaces here as *http.MaxBytesError with no file at all. Reporting it as
// FILE_REQUIRED would tell the caller to attach a photo it had already sent, so the
// size answer wins whenever the parser says the body was cut short.
func writeUploadFormError(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		httputil.WriteError(c, apperr.PayloadTooLarge(
			"FILE_TOO_LARGE",
			"File size exceeds the 10MB limit",
			err,
		))
		return
	}
	httputil.WriteError(c, apperr.Invalid(
		"FILE_REQUIRED",
		"Face image file 'image' is required",
		err,
	))
}
