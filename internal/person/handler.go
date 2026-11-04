package person

import (
	"fmt"
	"io"
	"net/http"
	"strconv"

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
		logger:  logger.Named("person.handler"),
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
		// PATCH is registered alongside PUT because the personId back-fill contract
		// (task 10-15-decouple-external-person-id, AC-4) is specified as PATCH.
		group.PATCH("/:id", h.Update)
		group.PUT("/:id/primary-face", h.SetPrimaryFace)
		group.DELETE("/:id", h.Delete)

		// Face template sub-resources (一人多照)
		group.GET("/:id/faces", h.ListFaces)
		group.DELETE("/:id/faces/:face_id", h.DeleteFace)
		group.GET("/:id/faces/:face_id/image", h.GetFaceImage)

		// Face bulk import jobs
		group.GET("/import-face-jobs/:jobId", h.GetFaceImportJob)
		group.GET("/import-face-jobs/:jobId/failed-report", h.GetFaceImportFailedReport)
	}
}

// RegisterUploadRoutes registers large-body upload endpoints on the upload router group.
func (h *Handler) RegisterUploadRoutes(rg *gin.RouterGroup) {
	rg.POST("/persons/:id/faces", h.AddFace)
	rg.POST("/persons/import/preview", h.PreviewImport)
	rg.POST("/persons/import/commit", h.CommitImport)
	rg.POST("/persons/import-face-jobs", h.CreateFaceImportJob)
	rg.POST("/persons/search-by-image", h.SearchByImage)
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
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[PersonResponse]}
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons [get]
func (h *Handler) List(c *gin.Context) {
	var query listPersonsQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	window := query.Normalize()

	persons, total, err := h.service.ListPersons(c.Request.Context(), window.PageSize, window.Offset, query.Search)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, newPersonResponses(persons), total, window)
}

// Create godoc
// @Summary      Create a new person profile
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        request body CreatePersonRequest true "Person creation payload"
// @Success      201  {object}  httputil.Response{data=PersonResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
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
	httputil.SuccessWithStatus(c, http.StatusCreated, newPersonResponse(p))
}

// Get godoc
// @Summary      Get person profile by ID
// @Tags         person
// @Produce      json
// @Param        id path string true "Person ID"
// @Success      200  {object}  httputil.Response{data=PersonResponse}
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
	httputil.Success(c, newPersonResponse(p))
}

// Update godoc
// @Summary      Update person profile metadata
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        request body UpdatePersonRequest true "Person update payload"
// @Success      200  {object}  httputil.Response{data=PersonResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      409  {object}  httputil.Response
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
	httputil.Success(c, newPersonResponse(p))
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
// @Param        page query int false "Page number (1-based, default 1)"
// @Param        pageSize query int false "Page size (default 20, max 100)"
// @Param        limit query int false "Legacy pagination limit"
// @Param        offset query int false "Legacy pagination offset"
// @Success      200  {object}  httputil.Response{data=httputil.PaginatedData[FaceResponse]}
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/faces [get]
func (h *Handler) ListFaces(c *gin.Context) {
	personID := c.Param("id")

	var query httputil.PaginationQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		httputil.HandleBindError(c, err)
		return
	}
	window := query.Normalize()

	faces, total, err := h.service.ListFaces(c.Request.Context(), personID, window.PageSize, window.Offset)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.PaginatedSuccess(c, newFaceResponses(faces), total, window)
}

// AddFace godoc
// @Summary      Enroll a new face template for a person
// @Tags         person
// @Accept       multipart/form-data
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        file formData file true "Face photo image file (JPG, PNG, WebP)"
// @Success      201  {object}  httputil.Response{data=FaceResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
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
	httputil.SuccessWithStatus(c, http.StatusCreated, newFaceResponse(*face))
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

	file, info, err := h.service.OpenFaceImage(c.Request.Context(), personID, faceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	defer file.Close()

	// ServeContent owns the stat result and the Range handling: reopening the path the
	// service just resolved would reintroduce the traversal window it closed.
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), file)
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

// SetPrimaryFace godoc
// @Summary      Set primary face for a person
// @Tags         person
// @Accept       json
// @Produce      json
// @Param        id path string true "Person ID"
// @Param        request body SetPrimaryFaceRequest true "Primary face payload"
// @Success      200  {object}  httputil.Response{data=PersonResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/{id}/primary-face [put]
func (h *Handler) SetPrimaryFace(c *gin.Context) {
	personID := c.Param("id")
	audithttp.Tag(c, ActionPersonPrimaryFaceSet, "person:"+personID, "Set primary face for person "+personID)

	var req SetPrimaryFaceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httputil.HandleBindError(c, err)
		return
	}

	p, err := h.service.SetPrimaryFace(c.Request.Context(), personID, req.FaceID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.Success(c, newPersonResponse(p))
}

// PreviewImport previews a CSV without creating person rows.
// @Summary      Preview a person CSV import
// @Tags         person
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "UTF-8 CSV file"
// @Success      200  {object}  httputil.Response{data=PersonImportPreviewResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/import/preview [post]
func (h *Handler) PreviewImport(c *gin.Context) {
	audithttp.Tag(c, ActionPersonImportPreview, "persons", "Preview person import")
	file, err := c.FormFile("file")
	if err != nil {
		httpImportRequiredError(c, err)
		return
	}
	reader, err := file.Open()
	if err != nil {
		httpImportReadError(c, err)
		return
	}
	defer reader.Close()

	result, err := h.service.PreviewPersonImport(c.Request.Context(), reader)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, result)
}

// CommitImport imports valid rows from the uploaded CSV and reports row-level failures.
// @Summary      Commit a person CSV import
// @Tags         person
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "UTF-8 CSV file"
// @Success      200  {object}  httputil.Response{data=PersonImportCommitResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/import/commit [post]
func (h *Handler) CommitImport(c *gin.Context) {
	audithttp.Tag(c, ActionPersonImportCommit, "persons", "Commit person import")
	file, err := c.FormFile("file")
	if err != nil {
		httpImportRequiredError(c, err)
		return
	}
	reader, err := file.Open()
	if err != nil {
		httpImportReadError(c, err)
		return
	}
	defer reader.Close()

	result, err := h.service.CommitPersonImport(c.Request.Context(), reader)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, result)
}

func httpImportRequiredError(c *gin.Context, err error) {
	httputil.WriteError(c, apperr.Invalid("FILE_REQUIRED", "CSV file is required", err))
}

func httpImportReadError(c *gin.Context, err error) {
	httputil.WriteError(c, apperr.Internal("FILE_READ_FAILED", "Failed to read uploaded file", err))
}

// CreateFaceImportJob godoc
// @Summary      Create an asynchronous face bulk import job
// @Tags         person
// @Accept       multipart/form-data
// @Produce      json
// @Param        file formData file true "ZIP archive containing images and optional manifest.csv"
// @Success      202  {object}  httputil.Response{data=FaceImportJobResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/import-face-jobs [post]
func (h *Handler) CreateFaceImportJob(c *gin.Context) {
	audithttp.Tag(c, ActionPersonFaceImportCreate, "job:", "Create face bulk import job")

	file, err := c.FormFile("file")
	if err != nil {
		httputil.WriteError(c, apperr.Invalid("FILE_REQUIRED", "ZIP file is required", err))
		return
	}

	job, err := h.service.StartFaceImportJob(c.Request.Context(), file)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	httputil.SuccessWithStatus(c, http.StatusAccepted, newFaceImportJobResponse(job))
}

// GetFaceImportJob godoc
// @Summary      Get face import job progress and status
// @Tags         person
// @Produce      json
// @Param        jobId path string true "Job ID"
// @Success      200  {object}  httputil.Response{data=FaceImportJobResponse}
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/import-face-jobs/{jobId} [get]
func (h *Handler) GetFaceImportJob(c *gin.Context) {
	jobID := c.Param("jobId")
	job, err := h.service.GetFaceImportJob(c.Request.Context(), jobID)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	httputil.Success(c, newFaceImportJobResponse(job))
}

// GetFaceImportFailedReport godoc
// @Summary      Download failed report CSV for a face import job
// @Tags         person
// @Produce      text/csv
// @Param        jobId path string true "Job ID"
// @Success      200  {file}    binary
// @Failure      404  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/import-face-jobs/{jobId}/failed-report [get]
func (h *Handler) GetFaceImportFailedReport(c *gin.Context) {
	jobID := c.Param("jobId")
	csvBytes, err := h.service.GetFaceImportFailedReportCSV(c.Request.Context(), jobID, c.GetHeader("Accept-Language"))
	if err != nil {
		httputil.WriteError(c, err)
		return
	}
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="failed_report_%s.csv"`, jobID))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", csvBytes)
}

// Search tuning defaults and bounds. They live here because the handler is what
// documents (and clamps) the public form contract.
const (
	defaultSearchThreshold = 0.70
	defaultSearchTopK      = 10
	maxSearchTopK          = 50
)

// searchParam reads an optional search parameter from the multipart form, falling back
// to the query string: gin's multipart binder only sees form values, and this endpoint
// accepts both.
func searchParam(c *gin.Context, field string) string {
	if raw := c.Request.FormValue(field); raw != "" {
		return raw
	}
	return c.Query(field)
}

// invalidSearchParam builds the 422 field detail for a bad search parameter. Field
// validation is the one case that cannot go through apperr: only httputil.AppError
// carries the details list.
func invalidSearchParam(field, code string) *httputil.AppError {
	return httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
		{Field: field, Code: code},
	})
}

func parseSearchThreshold(c *gin.Context) (float32, *httputil.AppError) {
	raw := searchParam(c, "threshold")
	if raw == "" {
		return defaultSearchThreshold, nil
	}
	value, err := strconv.ParseFloat(raw, 32)
	if err != nil {
		return 0, invalidSearchParam("threshold", "INVALID_FORMAT")
	}
	if value < 0 || value > 1 {
		return 0, invalidSearchParam("threshold", "INVALID_VALUE")
	}
	return float32(value), nil
}

func parseSearchTopK(c *gin.Context) (int, *httputil.AppError) {
	raw := searchParam(c, "topK")
	if raw == "" {
		return defaultSearchTopK, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, invalidSearchParam("topK", "INVALID_FORMAT")
	}
	if value < 1 || value > maxSearchTopK {
		return 0, invalidSearchParam("topK", "INVALID_VALUE")
	}
	return value, nil
}

// SearchByImage godoc
// @Summary      Search enrolled persons by facial photo
// @Tags         person
// @Accept       multipart/form-data
// @Produce      json
// @Param        image formData file true "Face photo image file (JPG, PNG, WebP)"
// @Param        threshold formData number false "Similarity threshold (0.0 to 1.0, default 0.70)"
// @Param        topK formData int false "Maximum candidate count (1 to 50, default 10)"
// @Success      200  {object}  httputil.Response{data=ImageSearchResponse}
// @Failure      400  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      500  {object}  httputil.Response
// @Router       /persons/search-by-image [post]
func (h *Handler) SearchByImage(c *gin.Context) {
	audithttp.Tag(c, ActionPersonSearchByImage, "search_by_image", "Search persons by image")

	threshold, valErr := parseSearchThreshold(c)
	if valErr != nil {
		httputil.WriteError(c, valErr)
		return
	}

	topK, valErr := parseSearchTopK(c)
	if valErr != nil {
		httputil.WriteError(c, valErr)
		return
	}

	file, _, err := c.Request.FormFile("image")
	if err != nil {
		httputil.WriteError(c, invalidSearchParam("image", "REQUIRED"))
		return
	}
	defer file.Close()

	// Neither the size limit nor the image validation is re-done here: the service
	// already answers 413/400 for them, and a second copy in the handler is how the two
	// answers drift apart. The limit only bounds what this read buffers.
	imgBytes, err := io.ReadAll(io.LimitReader(file, MaxFaceImageBytes+1))
	if err != nil {
		httputil.WriteError(c, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Failed to read image content", err))
		return
	}

	resp, err := h.service.SearchByImage(c.Request.Context(), imgBytes, threshold, topK)
	if err != nil {
		httputil.WriteError(c, err)
		return
	}

	audithttp.TagDetail(c, fmt.Sprintf("Matches: %d", len(resp.Matches)))
	httputil.Success(c, resp)
}
