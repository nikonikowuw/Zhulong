package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httpmiddleware"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

// mockExtractor satisfies person.Extractor with a valid-dimension feature vector. The
// bytes are not decoded: the OpenAPI contract is about what the endpoint accepts and
// what it answers, not about face detection quality.
type mockExtractor struct{}

func (mockExtractor) ExtractFaceFeature(_ context.Context, _ []byte) ([]float32, error) {
	return make([]float32, 512), nil
}

type mockStatusProvider struct {
	resp DeviceStatusResponse
	err  error
}

func (m *mockStatusProvider) DeviceStatus(_ context.Context) (DeviceStatusResponse, error) {
	if m.err != nil {
		return DeviceStatusResponse{}, m.err
	}
	return m.resp, nil
}

// mockJPEGBytes is a JPEG header followed by filler: enough for content sniffing, and
// deliberately not a decodable image since the extractor is mocked.
var mockJPEGBytes = []byte(
	"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xdb\x00C\x00mock-jpeg-data",
)

// openAPITestEnv is one provisioned test host plus the wiring under test.
type openAPITestEnv struct {
	router   *gin.Engine
	service  *person.Service
	limiter  *UploadLimiter
	provider *mockStatusProvider
}

// newOpenAPITestEnv builds the production middleware shape: the group is sized for
// enrollment photos and every JSON endpoint nests its own tighter limit. Testing against
// a bare group would hide the body-limit behaviour these endpoints are documented for.
func newOpenAPITestEnv(t *testing.T) *openAPITestEnv {
	t.Helper()

	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	personSvc := person.NewService(
		person.NewStore(dbStore),
		tempDir,
		zap.NewNop(),
		person.Options{Extractor: mockExtractor{}},
	)
	limiter := NewUploadLimiter()
	cstZone := time.FixedZone("CST", 8*3600)
	reportedAt := time.Date(
		2026,
		11,
		11,
		10,
		0,
		0,
		0,
		cstZone,
	)
	provider := &mockStatusProvider{
		resp: DeviceStatusResponse{
			DeviceID:   "edge-box-test-01",
			ReportedAt: reportedAt,
			Status:     "ready",
			Reasons:    nil, // intentionally nil to verify handler normalizes to []
			Host: DeviceHostInfo{
				Arch:          "arm64",
				UptimeSeconds: 1200,
			},
			Storage: DeviceStorageInfo{
				Status:      "healthy",
				Writable:    true,
				UsedPercent: 35,
				FreeMB:      16384,
			},
			Workload: DeviceWorkloadInfo{
				Decode: DeviceDecodeSummaryInfo{
					Backend: "auto",
				},
			},
		},
	}
	handler := NewHandler(personSvc, limiter, WithStatusProvider(provider))

	router := gin.New()
	group := router.Group("/api/v1/openapi")
	group.Use(
		httpmiddleware.MaxBodyLimitWithLimit(11*1024*1024),
		func(c *gin.Context) {
			apitoken.SetIdentity(c, apitoken.Identity{ID: "tok_test", Name: "MockToken"})
			c.Next()
		},
	)
	handler.RegisterRoutes(group)

	return &openAPITestEnv{
		router:   router,
		service:  personSvc,
		limiter:  limiter,
		provider: provider,
	}
}

// envelope is the subset of the standard response body a test needs to inspect.
type envelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *openAPITestEnv) do(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	return rec
}

func (e *openAPITestEnv) doJSON(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return e.do(t, req)
}

// decodeEnvelope fails the test on a malformed envelope instead of silently leaving the
// zero value behind, which would turn a broken response into a confusing assertion miss.
func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) envelope {
	t.Helper()
	var env envelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode response envelope %q: %v", rec.Body.String(), err)
	}
	return env
}

// decodeData unmarshals the envelope's data payload into dst.
func decodeData(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	env := decodeEnvelope(t, rec)
	if err := json.Unmarshal(env.Data, dst); err != nil {
		t.Fatalf("decode response data %q: %v", env.Data, err)
	}
}

// uploadFace posts a multipart enrollment photo.
func (e *openAPITestEnv) uploadFace(
	t *testing.T,
	personID, query, filename string,
	content []byte,
) *httptest.ResponseRecorder {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/"+personID+"/faces"+query, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return e.do(t, req)
}

// createPerson provisions a person and returns the decoded response.
func (e *openAPITestEnv) createPerson(t *testing.T, personID, name string) PersonProvisionResponse {
	t.Helper()
	rec := e.doJSON(t, http.MethodPost, "/api/v1/openapi/persons/"+personID, `{"name":"`+name+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create person %q: got %d, want 201: %s", personID, rec.Code, rec.Body.String())
	}
	var resp PersonProvisionResponse
	decodeData(t, rec, &resp)
	return resp
}

func TestOpenAPI_UpsertPerson(t *testing.T) {
	env := newOpenAPITestEnv(t)

	tests := []struct {
		name       string
		personID   string
		payload    string
		wantStatus int
		wantCode   string
		wantName   string
	}{
		{
			name:       "creates a new person",
			personID:   "EMP-001",
			payload:    `{"name":"Zhang San"}`,
			wantStatus: http.StatusCreated,
			wantCode:   "OK",
			wantName:   "Zhang San",
		},
		{
			name:       "repeats idempotently",
			personID:   "EMP-001",
			payload:    `{"name":"Zhang San"}`,
			wantStatus: http.StatusOK,
			wantCode:   "OK",
			wantName:   "Zhang San",
		},
		{
			name:       "renames an existing person",
			personID:   "EMP-001",
			payload:    `{"name":"Zhang San San"}`,
			wantStatus: http.StatusOK,
			wantCode:   "OK",
			wantName:   "Zhang San San",
		},
		{
			name:       "rejects an empty name",
			personID:   "EMP-001",
			payload:    `{"name":""}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "VALIDATION_FAILED",
		},
		{
			name:       "rejects a missing name",
			personID:   "EMP-001",
			payload:    `{}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "VALIDATION_FAILED",
		},
		{
			name:       "rejects a personId containing a control character",
			personID:   "EMP%00",
			payload:    `{"name":"Control"}`,
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "PERSON_ID_INVALID_CHARS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.doJSON(t, http.MethodPost, "/api/v1/openapi/persons/"+tt.personID, tt.payload)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			got := decodeEnvelope(t, rec)
			if got.Code != tt.wantCode {
				t.Fatalf("code = %q, want %q", got.Code, tt.wantCode)
			}
			if tt.wantName == "" {
				return
			}

			var resp PersonProvisionResponse
			decodeData(t, rec, &resp)
			if resp.PersonID != tt.personID {
				t.Errorf("personId = %q, want %q", resp.PersonID, tt.personID)
			}
			if resp.Name != tt.wantName {
				t.Errorf("name = %q, want %q", resp.Name, tt.wantName)
			}
		})
	}
}

// TestOpenAPI_UpsertPerson_AnswersFullProjection guards the create/update asymmetry: a
// repeated upsert takes the no-op update path, and answering from the pre-update copy
// there would return a person without its faces.
func TestOpenAPI_UpsertPerson_AnswersFullProjection(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-PROJ", "Projection")
	if rec := env.uploadFace(t, "EMP-PROJ", "", "a.jpg", mockJPEGBytes); rec.Code != http.StatusCreated {
		t.Fatalf("upload face: got %d: %s", rec.Code, rec.Body.String())
	}

	tests := []struct {
		name     string
		payload  string
		wantName string
	}{
		{name: "unchanged name", payload: `{"name":"Projection"}`, wantName: "Projection"},
		{name: "renamed", payload: `{"name":"Projection v2"}`, wantName: "Projection v2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := env.doJSON(t, http.MethodPost, "/api/v1/openapi/persons/EMP-PROJ", tt.payload)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			var resp PersonProvisionResponse
			decodeData(t, rec, &resp)
			if resp.Name != tt.wantName {
				t.Errorf("name = %q, want %q", resp.Name, tt.wantName)
			}
			if len(resp.Faces) != 1 {
				t.Fatalf("faces = %d, want the enrolled face in the projection", len(resp.Faces))
			}
			if resp.PrimaryFaceID == nil || *resp.PrimaryFaceID != resp.Faces[0].FaceID {
				t.Errorf("primaryFaceId = %v, want %q", resp.PrimaryFaceID, resp.Faces[0].FaceID)
			}
		})
	}
}

// TestOpenAPI_ReplaceFlag pins the append/replace fork. A value the endpoint cannot
// interpret must be rejected: guessing false would append a template the caller believed
// it was replacing, and nothing in a 201 response would say so.
func TestOpenAPI_ReplaceFlag(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantStatus   int
		wantFaces    int
		wantReplaced bool
	}{
		{name: "absent appends", query: "", wantStatus: http.StatusCreated, wantFaces: 3},
		{name: "false appends", query: "?replace=false", wantStatus: http.StatusCreated, wantFaces: 3},
		{
			name:         "upper case true replaces",
			query:        "?replace=TRUE",
			wantStatus:   http.StatusCreated,
			wantFaces:    1,
			wantReplaced: true,
		},
		{
			name:         "numeric true replaces",
			query:        "?replace=1",
			wantStatus:   http.StatusCreated,
			wantFaces:    1,
			wantReplaced: true,
		},
		{
			name:       "unparsable value is rejected",
			query:      "?replace=yes",
			wantStatus: http.StatusUnprocessableEntity,
			wantFaces:  2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newOpenAPITestEnv(t)
			env.createPerson(t, "EMP-REPLACE", "Replace")

			// Two faces first, so replace and append are distinguishable by count.
			for _, name := range []string{"a.jpg", "b.jpg"} {
				rec := env.uploadFace(t, "EMP-REPLACE", "", name, mockJPEGBytes)
				if rec.Code != http.StatusCreated {
					t.Fatalf("seed upload %q: got %d: %s", name, rec.Code, rec.Body.String())
				}
			}

			rec := env.uploadFace(t, "EMP-REPLACE", tt.query, "c.jpg", mockJPEGBytes)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantStatus != http.StatusCreated {
				if code := decodeEnvelope(t, rec).Code; code != "REPLACE_INVALID" {
					t.Fatalf("code = %q, want REPLACE_INVALID", code)
				}
			} else {
				var uploaded FaceUploadResponse
				decodeData(t, rec, &uploaded)
				if uploaded.Replaced != tt.wantReplaced {
					t.Errorf("replaced = %v, want %v", uploaded.Replaced, tt.wantReplaced)
				}
			}

			getRec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-REPLACE", nil))
			if getRec.Code != http.StatusOK {
				t.Fatalf("get person: got %d: %s", getRec.Code, getRec.Body.String())
			}
			var personResp PersonProvisionResponse
			decodeData(t, getRec, &personResp)
			if len(personResp.Faces) != tt.wantFaces {
				t.Fatalf("face count = %d, want %d", len(personResp.Faces), tt.wantFaces)
			}
		})
	}
}

// TestOpenAPI_UploadErrors pins the documented failure contract of the enrollment
// endpoint, including the case the multipart parser reports before any file exists.
func TestOpenAPI_UploadErrors(t *testing.T) {
	tests := []struct {
		name       string
		personID   string
		build      func(t *testing.T, env *openAPITestEnv) *http.Request
		wantStatus int
		wantCode   string
	}{
		{
			name:     "missing image field",
			personID: "EMP-ERR",
			build: func(t *testing.T, env *openAPITestEnv) *http.Request {
				t.Helper()
				body := &bytes.Buffer{}
				writer := multipart.NewWriter(body)
				if err := writer.WriteField("note", "no file"); err != nil {
					t.Fatalf("write field: %v", err)
				}
				if err := writer.Close(); err != nil {
					t.Fatalf("close writer: %v", err)
				}
				req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-ERR/faces", body)
				req.Header.Set("Content-Type", writer.FormDataContentType())
				return req
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "FILE_REQUIRED",
		},
		{
			name:     "request body above the group ceiling",
			personID: "EMP-ERR",
			build: func(t *testing.T, env *openAPITestEnv) *http.Request {
				t.Helper()
				body, contentType := oversizedMultipart(t, 12*1024*1024)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-ERR/faces", body)
				req.Header.Set("Content-Type", contentType)
				return req
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "PAYLOAD_TOO_LARGE",
		},
		{
			// With no declared length the group limiter cannot pre-check, so the abort
			// surfaces from the multipart parser. The endpoint reports the photo limit:
			// past that point the two ceilings are indistinguishable, and the file limit
			// is the one this endpoint is documented to answer.
			name:     "unknown length above the group ceiling",
			personID: "EMP-ERR",
			build: func(t *testing.T, env *openAPITestEnv) *http.Request {
				t.Helper()
				body, contentType := oversizedMultipart(t, 12*1024*1024)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-ERR/faces", body)
				req.ContentLength = -1
				req.Header.Set("Content-Type", contentType)
				return req
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "FILE_TOO_LARGE",
		},
		{
			// Under the group ceiling but over the enrollment limit: the endpoint's own
			// check answers, and it must name the file limit rather than the body limit.
			name:     "photo above the enrollment limit",
			personID: "EMP-ERR",
			build: func(t *testing.T, env *openAPITestEnv) *http.Request {
				t.Helper()
				body, contentType := oversizedMultipart(t, 10*1024*1024+512*1024)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-ERR/faces", body)
				req.Header.Set("Content-Type", contentType)
				return req
			},
			wantStatus: http.StatusRequestEntityTooLarge,
			wantCode:   "FILE_TOO_LARGE",
		},
		{
			name:     "unsupported image content",
			personID: "EMP-ERR",
			build: func(t *testing.T, env *openAPITestEnv) *http.Request {
				t.Helper()
				return newUploadRequest(t, "EMP-ERR", "", "notes.txt", []byte("plain text, not an image"))
			},
			wantStatus: http.StatusUnprocessableEntity,
			wantCode:   "INVALID_FILE_TYPE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := newOpenAPITestEnv(t)
			env.createPerson(t, tt.personID, "Errors")

			rec := env.do(t, tt.build(t, env))
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if code := decodeEnvelope(t, rec).Code; code != tt.wantCode {
				t.Fatalf("code = %q, want %q", code, tt.wantCode)
			}
		})
	}
}

// TestOpenAPI_UploadMissingPerson keeps the 404 distinct from the enrollment errors: a
// caller must not read "person not found" as "the photo was rejected".
func TestOpenAPI_UploadMissingPerson(t *testing.T) {
	env := newOpenAPITestEnv(t)
	rec := env.uploadFace(t, "EMP-ABSENT", "", "a.jpg", mockJPEGBytes)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if code := decodeEnvelope(t, rec).Code; code != "PERSON_NOT_FOUND" {
		t.Fatalf("code = %q, want PERSON_NOT_FOUND", code)
	}
}

// newUploadRequest builds a multipart enrollment request without going through the env,
// so table entries can vary the parts and the content type.
func newUploadRequest(
	t *testing.T,
	personID, query, filename string,
	content []byte,
) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/"+personID+"/faces"+query, body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// oversizedMultipart returns a multipart body carrying an image part of size bytes.
func oversizedMultipart(t *testing.T, size int) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("image", "huge.jpg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	jpegHeader := []byte("\xff\xd8\xff\xe0")
	if _, err := part.Write(jpegHeader); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := part.Write(bytes.Repeat([]byte("A"), size-len(jpegHeader))); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return body, writer.FormDataContentType()
}

// TestOpenAPI_PersonLifecycle walks the resource as a client would: enroll, promote,
// clear, delete, and confirm the record is gone.
func TestOpenAPI_PersonLifecycle(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-FACE", "Face Tester")

	// 1. First upload becomes primary by default.
	firstRec := env.uploadFace(t, "EMP-FACE", "", "photo1.jpg", mockJPEGBytes)
	if firstRec.Code != http.StatusCreated {
		t.Fatalf("upload face 1: got %d: %s", firstRec.Code, firstRec.Body.String())
	}
	var first FaceUploadResponse
	decodeData(t, firstRec, &first)
	if !first.IsPrimary || first.Replaced {
		t.Errorf("first upload = %+v, want isPrimary=true replaced=false", first)
	}

	// 2. Second upload appends and is not promoted.
	secondRec := env.uploadFace(t, "EMP-FACE", "", "photo2.jpg", mockJPEGBytes)
	if secondRec.Code != http.StatusCreated {
		t.Fatalf("upload face 2: got %d: %s", secondRec.Code, secondRec.Body.String())
	}
	var second FaceUploadResponse
	decodeData(t, secondRec, &second)
	if second.IsPrimary {
		t.Errorf("second upload = %+v, want isPrimary=false", second)
	}

	// 3. Promote the second face.
	primaryRec := env.do(t, httptest.NewRequest(
		http.MethodPut,
		"/api/v1/openapi/persons/EMP-FACE/faces/"+second.FaceID+"/primary",
		nil,
	))
	if primaryRec.Code != http.StatusOK {
		t.Fatalf("set primary: got %d: %s", primaryRec.Code, primaryRec.Body.String())
	}
	var promoted PersonProvisionResponse
	decodeData(t, primaryRec, &promoted)
	if promoted.PrimaryFaceID == nil || *promoted.PrimaryFaceID != second.FaceID {
		t.Fatalf("primaryFaceId = %v, want %q", promoted.PrimaryFaceID, second.FaceID)
	}
	if len(promoted.Faces) != 2 {
		t.Errorf("faces = %d, want the full projection", len(promoted.Faces))
	}

	// 4. Replace both faces with one.
	replaceRec := env.uploadFace(t, "EMP-FACE", "?replace=true", "photo3.jpg", mockJPEGBytes)
	if replaceRec.Code != http.StatusCreated {
		t.Fatalf("replace faces: got %d: %s", replaceRec.Code, replaceRec.Body.String())
	}
	var replaced FaceUploadResponse
	decodeData(t, replaceRec, &replaced)
	if !replaced.Replaced || !replaced.IsPrimary {
		t.Errorf("replace upload = %+v, want replaced=true isPrimary=true", replaced)
	}

	getRec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	var afterReplace PersonProvisionResponse
	decodeData(t, getRec, &afterReplace)
	if len(afterReplace.Faces) != 1 || afterReplace.Faces[0].FaceID != replaced.FaceID {
		t.Fatalf("faces after replace = %+v, want exactly %q", afterReplace.Faces, replaced.FaceID)
	}

	// 5. Clear the remaining face.
	clearRec := env.do(t, httptest.NewRequest(http.MethodDelete, "/api/v1/openapi/persons/EMP-FACE/faces", nil))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear faces: got %d: %s", clearRec.Code, clearRec.Body.String())
	}
	var cleared FaceClearResponse
	decodeData(t, clearRec, &cleared)
	if cleared.Cleared != 1 {
		t.Errorf("cleared = %d, want 1", cleared.Cleared)
	}

	getClearedRec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	var afterClear PersonProvisionResponse
	decodeData(t, getClearedRec, &afterClear)
	if len(afterClear.Faces) != 0 || afterClear.PrimaryFaceID != nil {
		t.Errorf("after clear = %+v, want no faces and no primary", afterClear)
	}

	// 6. Delete the person and confirm it is gone.
	deleteRec := env.do(t, httptest.NewRequest(http.MethodDelete, "/api/v1/openapi/persons/EMP-FACE", nil))
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete person: got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
	ghostRec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	if ghostRec.Code != http.StatusNotFound {
		t.Fatalf("status after delete = %d, want 404", ghostRec.Code)
	}
}

// TestOpenAPI_TimestampsAreUTC guards the serialization contract: an offset-bearing
// timestamp silently breaks clients that parse the documented UTC form.
func TestOpenAPI_TimestampsAreUTC(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-TZ", "Timezone")

	rec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-TZ", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("get person: got %d: %s", rec.Code, rec.Body.String())
	}
	raw := rec.Body.String()
	for _, field := range []string{`"createdAt"`, `"updatedAt"`} {
		if !strings.Contains(raw, field) {
			t.Fatalf("response is missing %s: %s", field, raw)
		}
	}
	if strings.Contains(raw, "+0") {
		t.Fatalf("timestamp carries a UTC offset instead of Z: %s", raw)
	}

	var resp PersonProvisionResponse
	decodeData(t, rec, &resp)
	if resp.CreatedAt.Location().String() != "UTC" || resp.UpdatedAt.Location().String() != "UTC" {
		t.Fatalf("locations = %v/%v, want UTC", resp.CreatedAt.Location(), resp.UpdatedAt.Location())
	}
}

// TestOpenAPI_ResponsesOmitInternalPaths keeps the internal file naming out of the
// external contract: the stored name encodes the person row id and no endpoint serves
// the photo back, so it has no consumer.
func TestOpenAPI_ResponsesOmitInternalPaths(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-PATH", "Path")
	if rec := env.uploadFace(t, "EMP-PATH", "", "a.jpg", mockJPEGBytes); rec.Code != http.StatusCreated {
		t.Fatalf("upload: got %d: %s", rec.Code, rec.Body.String())
	}

	rec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-PATH", nil))
	if strings.Contains(rec.Body.String(), "imagePath") {
		t.Fatalf("response leaks the internal image path: %s", rec.Body.String())
	}
}

func TestOpenAPI_GetStatus_ReadyDegradedAndLimiterIsolation(t *testing.T) {
	env := newOpenAPITestEnv(t)
	env.createPerson(t, "EMP-LIM", "Limiter Check")

	// Saturate both global and per-token upload limiter slots so face upload answers 429,
	// while GET /openapi/status still answers 200 OK.
	slot, ok := env.limiter.acquireToken("tok_test")
	if !ok {
		t.Fatal("failed to acquire per-token slot")
	}
	defer env.limiter.releaseToken("tok_test", slot)
	for range GlobalMaxFaceUploads {
		env.limiter.globalSem <- struct{}{}
	}
	defer func() {
		for range GlobalMaxFaceUploads {
			<-env.limiter.globalSem
		}
	}()

	faceRec := env.uploadFace(
		t,
		"EMP-LIM",
		"",
		"a.jpg",
		mockJPEGBytes,
	)
	if faceRec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected face upload to be rate-limited (429), got %d", faceRec.Code)
	}

	rec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /openapi/status while upload limiter full: got %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if strings.Contains(rec.Body.String(), "+08:00") {
		t.Fatalf("reportedAt must be normalized to UTC Z, got: %s", rec.Body.String())
	}

	var ready DeviceStatusResponse
	decodeData(t, rec, &ready)
	readyOK := ready.DeviceID == "edge-box-test-01" && ready.Status == "ready" && len(ready.Reasons) == 0
	if !readyOK {
		t.Fatalf("unexpected ready status payload: %+v", ready)
	}

	// Degraded state still answers HTTP 200 with machine-readable reasons.
	env.provider.resp.Status = "degraded"
	env.provider.resp.Reasons = []string{"storage_unavailable", "camera_offline"}
	degRec := env.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/status", nil))
	if degRec.Code != http.StatusOK {
		t.Fatalf("degraded status must still return HTTP 200, got %d", degRec.Code)
	}
	var deg DeviceStatusResponse
	decodeData(t, degRec, &deg)
	if deg.Status != "degraded" || len(deg.Reasons) != 2 {
		t.Fatalf("unexpected degraded payload: %+v", deg)
	}
}

func TestOpenAPI_GetStatus_NilProviderReturns503(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	group := router.Group("/api/v1/openapi")
	NewHandler(nil, NewUploadLimiter()).RegisterRoutes(group)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/status", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when status provider is nil, got %d: %s", rec.Code, rec.Body.String())
	}
	if code := decodeEnvelope(t, rec).Code; code != "SERVICE_UNAVAILABLE" {
		t.Fatalf("code = %q, want SERVICE_UNAVAILABLE", code)
	}
}
