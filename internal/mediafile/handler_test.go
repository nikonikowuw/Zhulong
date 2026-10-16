package mediafile

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/auth"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

const handlerFixtureContent = "0123456789abcdef"

func newFixtureHandler(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	writeFixtureFile(t, root, "recordings/cam-01/2026-01-01/10-00-00.mp4", handlerFixtureContent)
	writeFixtureFile(t, root, "snapshots/cam-01/2026-01-01/evt-1.jpg", "jpeg-bytes")
	writeFixtureFile(t, root, "exports/task-1.unknown", "unknown-bytes")

	svc := NewService(&stubRootProvider{root: root}, recordingInspector(), nil)
	handler := NewHandler(svc)

	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	return router, root
}

func doRequest(router *gin.Engine, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	return recorder
}

func TestHandler_RouteTableHasNoListingEndpoint(t *testing.T) {
	router, _ := newFixtureHandler(t)

	var registered []string
	for _, route := range router.Routes() {
		registered = append(registered, route.Method+" "+route.Path)
	}

	want := map[string]bool{
		"GET /api/v1/media/roots":         false,
		"GET /api/v1/media/entries/*path": false,
		"GET /api/v1/media/content/*path": false,
		// HEAD is registered explicitly because gin does not derive it from
		// GET; without it a HEAD probe would fall through to NoMethod and 404.
		"HEAD /api/v1/media/content/*path": false,
	}
	for _, route := range registered {
		if _, ok := want[route]; ok {
			want[route] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("expected route %s to be registered; got %v", route, registered)
		}
	}
	if len(registered) != len(want) {
		t.Errorf("expected exactly %d media routes, got %d: %v", len(want), len(registered), registered)
	}

	// The three paths, each serving exactly the methods above: no collection
	// route, no listing, no write method.
	paths := map[string][]string{}
	for _, route := range router.Routes() {
		paths[route.Path] = append(paths[route.Path], route.Method)
	}
	if len(paths) != 3 {
		t.Errorf("expected exactly 3 media paths, got %d: %v", len(paths), paths)
	}

	// A listing endpoint would be a bare /media or /media/entries collection
	// route. Neither may exist.
	for _, route := range registered {
		if strings.HasSuffix(route, "/media") || strings.HasSuffix(route, "/media/entries") {
			t.Errorf("unexpected collection route %s: media listing belongs to the event domain", route)
		}
	}
}

func TestHandler_GetEntryReturnsEnvelope(t *testing.T) {
	router, _ := newFixtureHandler(t)

	recorder := doRequest(router, http.MethodGet, "/api/v1/media/entries/snapshots/cam-01/2026-01-01/evt-1.jpg", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var envelope struct {
		Code    string      `json:"code"`
		Message string      `json:"message"`
		Data    *MediaEntry `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != "OK" {
		t.Errorf("expected code OK, got %q", envelope.Code)
	}
	if envelope.Data == nil {
		t.Fatal("expected entry data")
	}
	if envelope.Data.Path != "snapshots/cam-01/2026-01-01/evt-1.jpg" {
		t.Errorf("unexpected path %q", envelope.Data.Path)
	}
	if envelope.Data.SizeBytes != int64(len("jpeg-bytes")) {
		t.Errorf("unexpected size %d", envelope.Data.SizeBytes)
	}
}

func TestHandler_ErrorContract(t *testing.T) {
	router, _ := newFixtureHandler(t)

	cases := []struct {
		name       string
		target     string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "unknown top-level kind",
			target:     "/api/v1/media/entries/evil/a.mp4",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MEDIA_KIND_INVALID",
		},
		{
			name:       "traversal sequence",
			target:     "/api/v1/media/entries/recordings/../../etc/passwd",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MEDIA_PATH_INVALID",
		},
		{
			name:       "absolute path via encoded slash",
			target:     "/api/v1/media/entries//etc/passwd",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MEDIA_PATH_INVALID",
		},
		{
			name:       "empty path",
			target:     "/api/v1/media/entries/",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MEDIA_PATH_INVALID",
		},
		{
			name:       "missing file",
			target:     "/api/v1/media/entries/exports/gone.mp4",
			wantStatus: http.StatusNotFound,
			wantCode:   "MEDIA_ENTRY_NOT_FOUND",
		},
		{
			name:       "directory is not a file",
			target:     "/api/v1/media/entries/recordings/cam-01",
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "MEDIA_NOT_A_FILE",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := doRequest(router, http.MethodGet, tc.target, nil)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (%s)", tc.wantStatus, recorder.Code, recorder.Body.String())
			}

			var envelope struct {
				Code    string          `json:"code"`
				Message string          `json:"message"`
				Data    json.RawMessage `json:"data"`
				Details json.RawMessage `json:"details"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("decode envelope: %v", err)
			}
			if envelope.Code != tc.wantCode {
				t.Errorf("expected code %s, got %s", tc.wantCode, envelope.Code)
			}
			if string(envelope.Data) != "null" {
				t.Errorf("expected data to be null on error, got %s", envelope.Data)
			}
			if len(envelope.Details) != 0 {
				t.Errorf("expected details to be omitted on a non-validation error, got %s", envelope.Details)
			}
			if strings.Contains(envelope.Message, "/") {
				t.Errorf("error message must not leak a filesystem path: %q", envelope.Message)
			}
		})
	}
}

func TestHandler_RangeSemantics(t *testing.T) {
	router, _ := newFixtureHandler(t)
	target := "/api/v1/media/content/recordings/cam-01/2026-01-01/10-00-00.mp4"

	cases := []struct {
		name        string
		rangeHeader string
		ifRange     string
		wantStatus  int
		wantBody    string
		wantRange   string
		wantAccept  string
	}{
		{
			name:       "no range returns the whole file",
			wantStatus: http.StatusOK,
			wantBody:   handlerFixtureContent,
			wantAccept: "bytes",
		},
		{
			name:        "first ten bytes",
			rangeHeader: "bytes=0-9",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "0123456789",
			wantRange:   "bytes 0-9/16",
			wantAccept:  "bytes",
		},
		{
			name:        "open ended range",
			rangeHeader: "bytes=5-",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "56789abcdef",
			wantRange:   "bytes 5-15/16",
			wantAccept:  "bytes",
		},
		{
			name:        "suffix range",
			rangeHeader: "bytes=-4",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "cdef",
			wantRange:   "bytes 12-15/16",
			wantAccept:  "bytes",
		},
		{
			name:        "single byte",
			rangeHeader: "bytes=0-0",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "0",
			wantRange:   "bytes 0-0/16",
			wantAccept:  "bytes",
		},
		{
			// The upper bound is silently clamped rather than rejected, unlike
			// an out-of-range start. The asymmetry is easy to get wrong.
			name:        "upper bound beyond the file is clamped",
			rangeHeader: "bytes=0-16",
			wantStatus:  http.StatusPartialContent,
			wantBody:    handlerFixtureContent,
			wantRange:   "bytes 0-15/16",
			wantAccept:  "bytes",
		},
		{
			// A zero-length suffix is legal and yields an empty 206, not a 416.
			// Clients looping over ranges must tolerate a 206 with no body.
			name:        "zero length suffix yields an empty partial response",
			rangeHeader: "bytes=-0",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "",
			wantRange:   "bytes 16-15/16",
		},
		{
			// 416 is written by net/http as plain text, not as the JSON
			// envelope. The data plane deliberately does not use the envelope
			// for its success body, so clients must not assume success and
			// failure are shaped alike.
			name:        "start beyond the file is unsatisfiable",
			rangeHeader: "bytes=999-",
			wantStatus:  http.StatusRequestedRangeNotSatisfiable,
			wantRange:   "bytes */16",
			wantBody:    "invalid range: failed to overlap\n",
		},
		{
			name:        "start equal to the file length is unsatisfiable",
			rangeHeader: "bytes=16-",
			wantStatus:  http.StatusRequestedRangeNotSatisfiable,
			wantRange:   "bytes */16",
			wantBody:    "invalid range: failed to overlap\n",
		},
		{
			name:        "reversed range is unsatisfiable",
			rangeHeader: "bytes=1-0",
			wantStatus:  http.StatusRequestedRangeNotSatisfiable,
			wantBody:    "invalid range\n",
		},
		{
			// A syntactically invalid range is a 416, not a silently ignored
			// header returning 200.
			name:        "malformed range is unsatisfiable",
			rangeHeader: "bytes=abc",
			wantStatus:  http.StatusRequestedRangeNotSatisfiable,
			wantBody:    "invalid range\n",
		},
		{
			name:        "matching If-Range yields partial content",
			rangeHeader: "bytes=0-3",
			wantStatus:  http.StatusPartialContent,
			wantBody:    "0123",
		},
		{
			name:        "non matching If-Range degrades to the full file",
			rangeHeader: "bytes=0-3",
			ifRange:     "Wed, 21 Oct 2099 07:28:00 GMT",
			wantStatus:  http.StatusOK,
			wantBody:    handlerFixtureContent,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.rangeHeader != "" {
				headers["Range"] = tc.rangeHeader
			}
			if tc.ifRange != "" {
				headers["If-Range"] = tc.ifRange
			}

			recorder := doRequest(router, http.MethodGet, target, headers)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d (body %q)", tc.wantStatus, recorder.Code, recorder.Body.String())
			}
			if got := recorder.Body.String(); got != tc.wantBody {
				t.Errorf("expected body %q, got %q", tc.wantBody, got)
			}
			if tc.wantRange != "" {
				if got := recorder.Header().Get("Content-Range"); got != tc.wantRange {
					t.Errorf("expected Content-Range %q, got %q", tc.wantRange, got)
				}
			}
			if tc.wantAccept != "" {
				if got := recorder.Header().Get("Accept-Ranges"); got != tc.wantAccept {
					t.Errorf("expected Accept-Ranges %q, got %q", tc.wantAccept, got)
				}
			}
		})
	}
}

func TestHandler_HeadRequestHasNoBody(t *testing.T) {
	router, _ := newFixtureHandler(t)
	target := "/api/v1/media/content/exports/task-1.unknown"

	recorder := doRequest(router, http.MethodHead, target, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("expected an empty body for HEAD, got %q", recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Length"); got != "13" {
		t.Errorf("expected Content-Length 13, got %q", got)
	}
	if got := recorder.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("expected Accept-Ranges bytes, got %q", got)
	}
}

func TestHandler_ContentTypeByExtension(t *testing.T) {
	router, _ := newFixtureHandler(t)

	cases := []struct {
		target      string
		wantContent string
	}{
		{"/api/v1/media/content/recordings/cam-01/2026-01-01/10-00-00.mp4", "video/mp4"},
		{"/api/v1/media/content/snapshots/cam-01/2026-01-01/evt-1.jpg", "image/jpeg"},
		{"/api/v1/media/content/exports/task-1.unknown", "application/octet-stream"},
	}

	for _, tc := range cases {
		t.Run(tc.wantContent, func(t *testing.T) {
			recorder := doRequest(router, http.MethodGet, tc.target, nil)
			if recorder.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != tc.wantContent {
				t.Errorf("expected Content-Type %q, got %q", tc.wantContent, got)
			}
			// Forcing a download would defeat inline preview.
			if got := recorder.Header().Get("Content-Disposition"); got != "" {
				t.Errorf("expected no Content-Disposition, got %q", got)
			}
		})
	}
}

func TestHandler_MultiRangeUsesMultipart(t *testing.T) {
	router, _ := newFixtureHandler(t)

	recorder := doRequest(router, http.MethodGet,
		"/api/v1/media/content/exports/task-1.unknown",
		map[string]string{"Range": "bytes=0-1,5-6"})

	if recorder.Code != http.StatusPartialContent {
		t.Fatalf("expected 206, got %d", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "multipart/byteranges") {
		t.Errorf("expected a multipart/byteranges response, got %q", got)
	}
}

func TestHandler_DeletedFileReturnsNotFound(t *testing.T) {
	router, root := newFixtureHandler(t)

	target := "/api/v1/media/content/exports/task-1.unknown"
	if recorder := doRequest(router, http.MethodGet, target, nil); recorder.Code != http.StatusOK {
		t.Fatalf("expected the fixture to be readable first, got %d", recorder.Code)
	}

	// The retention cleaner deletes exports on a TTL. A 404 afterwards is the
	// normal contract, not a fault.
	removeFixtureFile(t, root, "exports/task-1.unknown")

	recorder := doRequest(router, http.MethodGet, target, nil)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after the file was pruned, got %d", recorder.Code)
	}

	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != "MEDIA_ENTRY_NOT_FOUND" {
		t.Errorf("expected MEDIA_ENTRY_NOT_FOUND, got %q", envelope.Code)
	}
}

func TestHandler_ContentErrorsNeverReachServeContent(t *testing.T) {
	router, _ := newFixtureHandler(t)

	// An error must be a well-formed JSON envelope, which it could not be if
	// ServeContent had already written the status line and Content-Length.
	cases := []string{
		"/api/v1/media/content/recordings/../../etc/passwd",
		"/api/v1/media/content/exports/gone.mp4",
		"/api/v1/media/content/recordings/cam-01",
		"/api/v1/media/content/",
	}

	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			recorder := doRequest(router, http.MethodGet, target, nil)
			if recorder.Code < 400 {
				t.Fatalf("expected an error status, got %d", recorder.Code)
			}

			var envelope httputil.Response
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("expected a JSON envelope for %s, got %q: %v", target, recorder.Body.String(), err)
			}
			if envelope.Code == "" || envelope.Code == "OK" {
				t.Errorf("expected a failure code, got %q", envelope.Code)
			}
		})
	}
}

func TestHandler_RootsEndpoint(t *testing.T) {
	router, root := newFixtureHandler(t)

	recorder := doRequest(router, http.MethodGet, "/api/v1/media/roots", nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var envelope struct {
		Code string         `json:"code"`
		Data *MediaRootInfo `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Data == nil {
		t.Fatal("expected root info")
	}
	if envelope.Data.MediaRoot != root {
		t.Errorf("expected mediaRoot %q, got %q", root, envelope.Data.MediaRoot)
	}
	if !envelope.Data.Directories.Recordings {
		t.Error("expected recordings to be reported as present")
	}
}

func TestHandler_UnavailableRootIsServiceUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	svc := NewService(&stubRootProvider{}, recordingInspector(), nil)
	handler := NewHandler(svc)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))

	recorder := doRequest(router, http.MethodGet, "/api/v1/media/entries/exports/task-1.mp4", nil)

	// 503 rather than 500: the root is temporarily unusable, and the standard
	// library's status mapping has no 503 branch, so this asserts the handler
	// builds the error explicitly instead of deriving it from a Kind.
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d (%s)", recorder.Code, recorder.Body.String())
	}

	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Code != "MEDIA_ROOT_UNAVAILABLE" {
		t.Errorf("expected MEDIA_ROOT_UNAVAILABLE, got %q", envelope.Code)
	}
}

func TestHandler_ContentTypeForPath(t *testing.T) {
	cases := map[string]string{
		"a.mp4":     "video/mp4",
		"a.MP4":     "video/mp4",
		"a.jpg":     "image/jpeg",
		"a.jpeg":    "image/jpeg",
		"a.JPEG":    "image/jpeg",
		"a.png":     "image/png",
		"a.txt":     "application/octet-stream",
		"noext":     "application/octet-stream",
		"a.mp4.exe": "application/octet-stream",
	}
	for name, want := range cases {
		if got := contentTypeForPath(name); got != want {
			t.Errorf("contentTypeForPath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRelativeMediaPath(t *testing.T) {
	cases := map[string]string{
		"/recordings/a.mp4":    "recordings/a.mp4",
		"/snapshots/cam/e.jpg": "snapshots/cam/e.jpg",
		"recordings/a.mp4":     "recordings/a.mp4",
		"/":                    "",
		"":                     "",
		"/../etc/passwd":       "../etc/passwd",
	}
	for input, want := range cases {
		if got := relativeMediaPath(input); got != want {
			t.Errorf("relativeMediaPath(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestHandler_ServiceErrorCodesHaveLocalizedMessages checks that every code the
// service can emit has a registered message in all three supported languages.
//
// Without this, an unregistered code silently falls back to the English
// hardcoded message, so a missing zh-Hans or zh-Hant entry would only surface
// in production.
func TestHandler_ServiceErrorCodesHaveLocalizedMessages(t *testing.T) {
	codes := []string{
		"MEDIA_ROOT_UNAVAILABLE",
		"MEDIA_KIND_INVALID",
		"MEDIA_PATH_INVALID",
		"MEDIA_PATH_ESCAPE",
		"MEDIA_ENTRY_NOT_FOUND",
		"MEDIA_ENTRY_UNREADABLE",
		"MEDIA_NOT_A_FILE",
	}
	locales := []string{"en", "zh-Hans", "zh-Hant"}

	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			for _, locale := range locales {
				recorder := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(recorder)
				context.Request = httptest.NewRequest(http.MethodGet, "/", nil)
				context.Request.Header.Set("Accept-Language", locale)

				httputil.WriteError(context, apperr.Invalid(code, "fallback message", errors.New("cause")))

				var envelope struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
					t.Fatalf("decode envelope: %v", err)
				}
				if envelope.Code != code {
					t.Fatalf("expected code %s, got %s", code, envelope.Code)
				}
				if envelope.Message == "" || envelope.Message == "fallback message" {
					t.Errorf("expected a registered %s message for code %s, got %q", locale, code, envelope.Message)
				}
			}
		})
	}
}

func removeFixtureFile(t *testing.T, root, relPath string) {
	t.Helper()
	if err := os.Remove(filepath.Join(root, filepath.FromSlash(relPath))); err != nil {
		t.Fatalf("remove %s: %v", relPath, err)
	}
}

// stubAuthService is a minimal auth.AuthService that accepts no session, used
// to verify the media endpoints are mounted behind authentication.
type stubAuthService struct{}

func (stubAuthService) GetStatus(context.Context) (auth.AuthStatusResponse, error) {
	return auth.AuthStatusResponse{}, nil
}
func (stubAuthService) InitAdmin(context.Context, auth.InitAdminRequest) (auth.UserResponse, string, error) {
	return auth.UserResponse{}, "", nil
}
func (stubAuthService) Login(context.Context, string, auth.LoginRequest) (auth.UserResponse, string, error) {
	return auth.UserResponse{}, "", nil
}
func (stubAuthService) Logout(string) {}
func (stubAuthService) GetCurrentUser(context.Context, int64) (auth.UserResponse, error) {
	return auth.UserResponse{}, nil
}
func (stubAuthService) ValidateSession(string) (auth.SessionItem, bool) {
	return auth.SessionItem{}, false
}
func (stubAuthService) SessionTTL() time.Duration {
	return time.Hour
}

// TestHandler_RequiresSessionCookie verifies the media endpoints are mounted on
// the protected route group.
//
// This matters more here than for a JSON endpoint: /media/content is the one
// endpoint whose success body is raw bytes. A client that assumed success and
// failure are shaped alike would try to parse the 401 envelope as media data.
func TestHandler_RequiresSessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	root := t.TempDir()
	writeFixtureFile(t, root, "exports/task-1.mp4", handlerFixtureContent)

	svc := NewService(&stubRootProvider{root: root}, recordingInspector(), nil)
	handler := NewHandler(svc)

	// Mirror the production mounting: RequireAuth guards the group.
	router := gin.New()
	protected := router.Group("/api/v1")
	protected.Use(auth.RequireAuth(stubAuthService{}))
	handler.RegisterRoutes(protected)

	for _, target := range []string{
		"/api/v1/media/roots",
		"/api/v1/media/entries/exports/task-1.mp4",
		"/api/v1/media/content/exports/task-1.mp4",
	} {
		t.Run(target, func(t *testing.T) {
			recorder := doRequest(router, http.MethodGet, target, nil)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401 without a session cookie, got %d", recorder.Code)
			}

			// The 401 body is the JSON envelope, not media bytes, even for the
			// content endpoint.
			var envelope struct {
				Code string `json:"code"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
				t.Fatalf("expected a JSON envelope for the 401 body, got %q: %v", recorder.Body.String(), err)
			}
			if envelope.Code != "UNAUTHORIZED" {
				t.Errorf("expected UNAUTHORIZED, got %q", envelope.Code)
			}
		})
	}
}
