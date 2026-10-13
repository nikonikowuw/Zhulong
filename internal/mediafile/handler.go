package mediafile

import (
	"errors"
	"net/http"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Handler serves the media read endpoints.
type Handler struct {
	service *Service
}

// NewHandler creates the media file HTTP handler.
func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes mounts the media endpoints.
//
// There are exactly three, and none of them lists a directory. Listing,
// filtering, pagination and sorting are provided by the event domain through
// SQL, which is O(page) and returns a free total; the filesystem service only
// does the two things a filesystem can do in O(1): return metadata for one
// path, and stream bytes for one path.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	group := rg.Group("/media")
	{
		group.GET("/roots", h.GetRoots)
		group.GET("/entries/*path", h.GetEntry)
		// HEAD is registered explicitly. Gin does not derive HEAD from a GET
		// route: a HEAD request to a GET-only path falls through to NoMethod
		// and returns 404. Browsers and media clients issue HEAD to probe size
		// and range support before streaming, so without this route they would
		// conclude the file is missing. ServeContent already suppresses the
		// body for HEAD while keeping Content-Length and Accept-Ranges.
		group.GET("/content/*path", h.GetContent)
		group.HEAD("/content/*path", h.GetContent)
	}
}

// GetRoots godoc
// @Summary      Get media root status
// @Description  Reports media root availability, device identity, mount point and which media subdirectories exist.
// @Tags         media
// @Produce      json
// @Success      200  {object}  httputil.Response{data=MediaRootInfo}
// @Failure      500  {object}  httputil.Response
// @Failure      503  {object}  httputil.Response
// @Router       /media/roots [get]
func (h *Handler) GetRoots(c *gin.Context) {
	info, err := h.service.GetRootInfo(c.Request.Context())
	if err != nil {
		writeMediaError(c, err)
		return
	}
	httputil.Success(c, info)
}

// writeMediaError renders an error through the shared error pipeline, with a
// single correction: an unusable media root is a 503, not a 500.
//
// httputil.KindToHTTPStatus has no ServiceUnavailable branch, so an
// apperr.KindInternal error surfaces as 500. The distinction matters here: a
// media root that is temporarily unavailable (external disk dropped off, volume
// not mounted yet) is retryable, whereas a 500 tells the client the server is
// broken. The override is deliberately narrow and keyed on the one code whose
// status the shared mapper cannot express, so this is not a second, competing
// status table.
func writeMediaError(c *gin.Context, err error) {
	var appErr *apperr.Error
	if errors.As(err, &appErr) && appErr.Code == "MEDIA_ROOT_UNAVAILABLE" {
		httputil.WriteError(c, httputil.NewError(
			http.StatusServiceUnavailable, appErr.Code, appErr.Message, appErr.Err))
		return
	}
	httputil.WriteError(c, err)
}

// GetEntry godoc
// @Summary      Get media entry metadata
// @Description  Returns size and modification time for a single media file addressed by its path relative to the media root. The path must start with recordings, snapshots or exports. Absolute paths are rejected with 422 MEDIA_PATH_INVALID.
// @Tags         media
// @Produce      json
// @Param        path  path      string  true  "Media path relative to the media root, e.g. snapshots/cam-01/2026-10-13/evt-1.jpg"
// @Success      200  {object}  httputil.Response{data=MediaEntry}
// @Failure      404  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      503  {object}  httputil.Response
// @Router       /media/entries/{path} [get]
func (h *Handler) GetEntry(c *gin.Context) {
	entry, err := h.service.GetEntry(c.Request.Context(), relativeMediaPath(c.Param("path")))
	if err != nil {
		writeMediaError(c, err)
		return
	}
	httputil.Success(c, entry)
}

// GetContent godoc
// @Summary      Stream media file bytes
// @Description  Streams the raw bytes of a media file with full RFC 7233 range support. The server performs byte transfer only: no decoding, demuxing, remuxing, transcoding or slicing. The successful response body is a raw byte stream, not the JSON envelope; error responses follow the standard envelope except for 416, which net/http writes as plain text.
// @Tags         media
// @Produce      application/octet-stream
// @Param        path   path      string  true  "Media path relative to the media root"
// @Param        Range  header    string  false  "Byte range, e.g. bytes=0-1023"
// @Success      200  {file}    binary
// @Success      206  {file}    binary
// @Failure      401  {object}  httputil.Response
// @Failure      404  {object}  httputil.Response
// @Failure      416  {string}  string  "Invalid range"
// @Failure      422  {object}  httputil.Response
// @Failure      503  {object}  httputil.Response
// @Router       /media/content/{path} [get]
func (h *Handler) GetContent(c *gin.Context) {
	// Every validation happens before ServeContent is called. Once it starts
	// writing, it owns the status line and Content-Length, so an error written
	// afterwards would corrupt the response instead of replacing it.
	file, info, err := h.service.OpenContent(c.Request.Context(), relativeMediaPath(c.Param("path")))
	if err != nil {
		writeMediaError(c, err)
		return
	}
	defer file.Close()

	contentType := contentTypeForPath(info.Name())
	c.Header("Content-Type", contentType)
	// No Content-Disposition: attachment. It would force a download and defeat
	// inline preview, which is the entire purpose of this endpoint.
	//
	// No Cache-Control either: exports/ is pruned on a TTL, so a long max-age
	// would hand the browser a cached copy of a file that no longer exists.
	// ServeContent's Last-Modified plus If-Modified-Since is enough for files
	// that are immutable once written.
	http.ServeContent(c.Writer, c.Request, info.Name(), info.ModTime(), file)
}

// relativeMediaPath converts a Gin wildcard parameter into the relative media
// path this service publishes.
//
// Gin leaves a leading slash on "*path" parameters, so the raw value for
// /media/content/recordings/a.mp4 is "/recordings/a.mp4". The conversion lives
// here, at the URL boundary, rather than inside the resolver: the resolver's
// contract is that it accepts a path relative to the media root, and letting it
// tolerate a leading slash would make an absolute path such as //etc/passwd
// indistinguishable from a legitimate relative path.
//
// A bare /media/content/ yields "/", which becomes the empty string and is
// rejected by the resolver with MEDIA_PATH_INVALID.
func relativeMediaPath(wildcard string) string {
	return strings.TrimPrefix(wildcard, "/")
}

// contentTypeForPath maps a file extension to a deterministic content type.
//
// ServeContent would otherwise sniff the extension and fall back to
// http.DetectContentType, which can label arbitrary bytes as text/html and
// invite content-type confusion. Returning application/octet-stream for
// anything unknown keeps the browser from guessing.
func contentTypeForPath(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".mp4":
		return "video/mp4"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	default:
		return "application/octet-stream"
	}
}
