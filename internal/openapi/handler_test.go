package openapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apitoken"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

type mockExtractor struct{}

func (mockExtractor) ExtractFaceFeature(ctx context.Context, imgBytes []byte) ([]float32, error) {
	return make([]float32, 512), nil
}

var mockJPEGBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xdb\x00C\x00mock-jpeg-data")

func setupTestOpenAPIEnv(t *testing.T) (*gin.Engine, *person.Service, func()) {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	personStore := person.NewStore(dbStore)
	personSvc := person.NewService(personStore, tempDir, zap.NewNop(), person.Options{Extractor: mockExtractor{}})
	limiter := NewUploadLimiter()
	handler := NewHandler(personSvc, limiter, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api/v1/openapi")
	// Simulate Bearer Auth middleware populating caller identity
	rg.Use(func(c *gin.Context) {
		apitoken.SetIdentity(c, apitoken.Identity{ID: "tok_test", Name: "MockToken"})
		c.Next()
	})
	handler.RegisterRoutes(rg)

	cleanup := func() {
		_ = dbStore.Close()
	}
	return router, personSvc, cleanup
}

func TestOpenAPI_UpsertPerson(t *testing.T) {
	router, _, cleanup := setupTestOpenAPIEnv(t)
	defer cleanup()

	// 1. Initial creation (201 Created)
	body := `{"name":"Zhang San"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-001", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string                  `json:"code"`
		Data PersonProvisionResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.PersonID != "EMP-001" || resp.Data.Name != "Zhang San" {
		t.Errorf("unexpected provisioned person: %+v", resp.Data)
	}

	// 2. Idempotent upsert with same name (200 OK)
	reqSame := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-001", bytes.NewBufferString(body))
	reqSame.Header.Set("Content-Type", "application/json")
	wSame := httptest.NewRecorder()
	router.ServeHTTP(wSame, reqSame)

	if wSame.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for idempotent upsert, got %d: %s", wSame.Code, wSame.Body.String())
	}

	// 3. Upsert with updated name (200 OK)
	bodyUpdate := `{"name":"Zhang San San"}`
	reqUpdate := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-001", bytes.NewBufferString(bodyUpdate))
	reqUpdate.Header.Set("Content-Type", "application/json")
	wUpdate := httptest.NewRecorder()
	router.ServeHTTP(wUpdate, reqUpdate)

	if wUpdate.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for rename, got %d: %s", wUpdate.Code, wUpdate.Body.String())
	}
	_ = json.Unmarshal(wUpdate.Body.Bytes(), &resp)
	if resp.Data.Name != "Zhang San San" {
		t.Errorf("expected name to be updated, got %q", resp.Data.Name)
	}

	// 4. Invalid input (empty name)
	reqBad := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-001", bytes.NewBufferString(`{"name":""}`))
	reqBad.Header.Set("Content-Type", "application/json")
	wBad := httptest.NewRecorder()
	router.ServeHTTP(wBad, reqBad)
	if wBad.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for empty name, got %d", wBad.Code)
	}
}

func TestOpenAPI_FaceLifecycle(t *testing.T) {
	router, _, cleanup := setupTestOpenAPIEnv(t)
	defer cleanup()

	// 1. Create person first
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/openapi/persons/EMP-FACE", bytes.NewBufferString(`{"name":"Face Tester"}`))
	createReq.Header.Set("Content-Type", "application/json")
	wCreate := httptest.NewRecorder()
	router.ServeHTTP(wCreate, createReq)
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create person failed: %d", wCreate.Code)
	}

	// Helper for multipart face upload
	uploadFace := func(url string, filename string, content []byte) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		part, _ := writer.CreateFormFile("image", filename)
		_, _ = part.Write(content)
		_ = writer.Close()

		req := httptest.NewRequest(http.MethodPost, url, body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}

	// 2. Upload first face
	wUp1 := uploadFace("/api/v1/openapi/persons/EMP-FACE/faces", "photo1.jpg", mockJPEGBytes)
	if wUp1.Code != http.StatusCreated {
		t.Fatalf("upload face 1 failed: %d: %s", wUp1.Code, wUp1.Body.String())
	}
	var upResp1 struct {
		Data FaceUploadResponse `json:"data"`
	}
	_ = json.Unmarshal(wUp1.Body.Bytes(), &upResp1)
	if !upResp1.Data.IsPrimary || upResp1.Data.Replaced {
		t.Errorf("unexpected upload response: %+v", upResp1.Data)
	}

	// 3. Upload second face
	wUp2 := uploadFace("/api/v1/openapi/persons/EMP-FACE/faces", "photo2.jpg", mockJPEGBytes)
	if wUp2.Code != http.StatusCreated {
		t.Fatalf("upload face 2 failed: %d", wUp2.Code)
	}
	var upResp2 struct {
		Data FaceUploadResponse `json:"data"`
	}
	_ = json.Unmarshal(wUp2.Body.Bytes(), &upResp2)
	if upResp2.Data.IsPrimary {
		t.Errorf("expected second face not to be primary by default")
	}

	// 4. Set face 2 as primary
	reqPrimary := httptest.NewRequest(http.MethodPut, "/api/v1/openapi/persons/EMP-FACE/faces/"+upResp2.Data.FaceID+"/primary", nil)
	wPrimary := httptest.NewRecorder()
	router.ServeHTTP(wPrimary, reqPrimary)
	if wPrimary.Code != http.StatusOK {
		t.Fatalf("set primary failed: %d: %s", wPrimary.Code, wPrimary.Body.String())
	}
	var primResp struct {
		Data PersonProvisionResponse `json:"data"`
	}
	_ = json.Unmarshal(wPrimary.Body.Bytes(), &primResp)
	if primResp.Data.PrimaryFaceID == nil || *primResp.Data.PrimaryFaceID != upResp2.Data.FaceID {
		t.Errorf("expected primaryFaceId to be %s, got %v", upResp2.Data.FaceID, primResp.Data.PrimaryFaceID)
	}

	// 5. Replace with a single face
	wReplace := uploadFace("/api/v1/openapi/persons/EMP-FACE/faces?replace=true", "photo_single.jpg", mockJPEGBytes)
	if wReplace.Code != http.StatusCreated {
		t.Fatalf("replace faces failed: %d: %s", wReplace.Code, wReplace.Body.String())
	}
	var repResp struct {
		Data FaceUploadResponse `json:"data"`
	}
	_ = json.Unmarshal(wReplace.Body.Bytes(), &repResp)
	if !repResp.Data.Replaced || !repResp.Data.IsPrimary {
		t.Errorf("expected replaced=true and isPrimary=true, got %+v", repResp.Data)
	}

	// Verify only 1 face remains
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	_ = json.Unmarshal(wGet.Body.Bytes(), &primResp)
	if len(primResp.Data.Faces) != 1 || primResp.Data.Faces[0].FaceID != repResp.Data.FaceID {
		t.Fatalf("expected 1 replacement face, got %+v", primResp.Data.Faces)
	}

	// 6. Clear faces
	wClear := httptest.NewRecorder()
	router.ServeHTTP(wClear, httptest.NewRequest(http.MethodDelete, "/api/v1/openapi/persons/EMP-FACE/faces", nil))
	if wClear.Code != http.StatusOK {
		t.Fatalf("clear faces failed: %d: %s", wClear.Code, wClear.Body.String())
	}

	// Verify 0 faces remain
	wGetAfter := httptest.NewRecorder()
	router.ServeHTTP(wGetAfter, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	_ = json.Unmarshal(wGetAfter.Body.Bytes(), &primResp)
	if len(primResp.Data.Faces) != 0 || primResp.Data.PrimaryFaceID != nil {
		t.Errorf("expected 0 faces and nil primaryFaceId, got %+v", primResp.Data)
	}

	// 7. Delete person
	wDel := httptest.NewRecorder()
	router.ServeHTTP(wDel, httptest.NewRequest(http.MethodDelete, "/api/v1/openapi/persons/EMP-FACE", nil))
	if wDel.Code != http.StatusOK {
		t.Fatalf("delete person failed: %d", wDel.Code)
	}

	wGetGhost := httptest.NewRecorder()
	router.ServeHTTP(wGetGhost, httptest.NewRequest(http.MethodGet, "/api/v1/openapi/persons/EMP-FACE", nil))
	if wGetGhost.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after person deletion, got %d", wGetGhost.Code)
	}
}
