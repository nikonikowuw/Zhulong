package person_test

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
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

var (
	testJPEGBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xdb\x00C\x00mock-jpeg-data")
	testPNGBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06mock-png-data")
	testWebPBytes = []byte("RIFF\x1a\x00\x00\x00WEBPVP8 \x0e\x00\x00\x00mock-webp-data")
)

// setupTestRouter wires the real audit middleware so handler tests assert the
// declarative audit path rather than a test double.
func setupTestRouter(t *testing.T) (*gin.Engine, *person.Service) {
	router, service, _ := setupTestRouterWithAudit(t)
	return router, service
}

func makeMultipartFaceRequest(url string, filename string, content []byte) *http.Request {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, _ := writer.CreateFormFile("file", filename)
	_, _ = part.Write(content)
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, url, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func setupTestRouterWithAudit(t *testing.T) (*gin.Engine, *person.Service, *audit.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Stop(context.Background()) })

	comp := person.NewPersonComponents(dbStore, tempDir, nil, zap.NewNop())

	r := gin.New()
	api := r.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	comp.Handler.RegisterRoutes(api)
	comp.Handler.RegisterUploadRoutes(api)

	return r, comp.Service, auditSvc
}

// drainAudit stops the service so queued entries are persisted, then lists them.
func drainAudit(t *testing.T, svc *audit.Service) []audit.AuditLog {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := svc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	items, _, err := svc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	return items
}

func TestHandlerPersonAndFaces(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 1. Initial list should be empty
	req := httptest.NewRequest(http.MethodGet, "/api/v1/persons", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}
	var emptyListResp struct {
		Data httputil.PaginatedData[person.Person] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &emptyListResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if emptyListResp.Data.Total != 0 || len(emptyListResp.Data.Items) != 0 {
		t.Fatalf("expected 0 total persons, got %d", emptyListResp.Data.Total)
	}

	// 2. Create Person
	personBody := bytes.NewReader([]byte(`{"personId":"EMP_9527","name":"张三"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons", personBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d, body: %s", w.Code, w.Body.String())
	}
	var createdPersonResp struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdPersonResp); err != nil {
		t.Fatalf("unmarshal created person: %v", err)
	}
	personRowID := createdPersonResp.Data.ID
	if personRowID == "" {
		t.Fatal("expected non-empty generated ID")
	}
	if strings.HasPrefix(personRowID, "person_") {
		t.Fatalf("expected ID without person_ prefix, got %q", personRowID)
	}

	// 3. Add Face 1
	req = makeMultipartFaceRequest("/api/v1/persons/"+personRowID+"/faces", "face_01.jpg", testJPEGBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for face 1, got: %d", w.Code)
	}
	var face1Resp struct {
		Data person.Face `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &face1Resp); err != nil {
		t.Fatalf("unmarshal face 1 response: %v", err)
	}
	if face1Resp.Data.ID == "" {
		t.Fatal("expected server-generated ID for face 1, got empty")
	}

	// 4. Add Face 2
	req = makeMultipartFaceRequest("/api/v1/persons/"+personRowID+"/faces", "face_02.png", testPNGBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for face 2, got: %d", w.Code)
	}

	// 5. Get Person
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons/"+personRowID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got: %d", w.Code)
	}

	var resp struct {
		Code string        `json:"code"`
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal person response: %v", err)
	}
	if resp.Data.Name != "张三" {
		t.Errorf("expected name '张三', got '%s'", resp.Data.Name)
	}
	if resp.Data.PersonID == nil || *resp.Data.PersonID != "EMP_9527" {
		t.Errorf("expected personId 'EMP_9527', got '%v'", resp.Data.PersonID)
	}
	if len(resp.Data.Faces) != 2 {
		t.Errorf("expected 2 faces registered, got %d", len(resp.Data.Faces))
	}

	// 5.1 Test Paginated List
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons?page=1&pageSize=10", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on list persons, got: %d", w.Code)
	}
	var pageResp struct {
		Data httputil.PaginatedData[person.Person] `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pageResp); err != nil {
		t.Fatalf("unmarshal list response: %v", err)
	}
	if pageResp.Data.Total != 1 || len(pageResp.Data.Items) != 1 {
		t.Fatalf("expected 1 person in paginated list, got total=%d items=%d", pageResp.Data.Total, len(pageResp.Data.Items))
	}

	// 6. Delete Face 1
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/"+personRowID+"/faces/"+face1Resp.Data.ID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on face deletion, got: %d", w.Code)
	}

	// 7. Delete Person
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/"+personRowID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on person deletion, got: %d", w.Code)
	}
}

func TestHandlerAutoGeneratedIDs(t *testing.T) {
	router, _ := setupTestRouter(t)

	// Create person 1 without a row ID ("Alice", len 5)
	p1Body := bytes.NewReader([]byte(`{"name":"Alice","personId":"EMP_ALICE"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", p1Body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for person 1, got: %d, body: %s", w.Code, w.Body.String())
	}
	var resp1 struct {
		Data person.Person `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp1)

	// Create person 2 without a row ID ("David", same length 5, must not collide)
	p2Body := bytes.NewReader([]byte(`{"name":"David","personId":"EMP_DAVID"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons", p2Body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for person 2, got: %d, body: %s", w.Code, w.Body.String())
	}
	var resp2 struct {
		Data person.Person `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp2)

	if resp1.Data.ID == resp2.Data.ID {
		t.Fatalf("expected distinct generated IDs, but got duplicate: %s", resp1.Data.ID)
	}
	if !idutil.IsValid(resp1.Data.ID) {
		t.Fatalf("expected person 1 ID %q to be a valid ULID", resp1.Data.ID)
	}
	if !idutil.IsValid(resp2.Data.ID) {
		t.Fatalf("expected person 2 ID %q to be a valid ULID", resp2.Data.ID)
	}

	// Add 2 faces for Alice without IDs, must not collide
	req = makeMultipartFaceRequest("/api/v1/persons/"+resp1.Data.ID+"/faces", "face1.jpg", testJPEGBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for face 1, got: %d", w.Code)
	}

	req = makeMultipartFaceRequest("/api/v1/persons/"+resp1.Data.ID+"/faces", "face2.webp", testWebPBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for face 2, got: %d, body: %s", w.Code, w.Body.String())
	}
}

func TestHandlerValidationErrors(t *testing.T) {
	router, _ := setupTestRouter(t)

	// Missing name should trigger 422 VALIDATION_FAILED via HandleBindError
	body := bytes.NewReader([]byte(`{"personId":"EMP_9527"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 Unprocessable Entity, got: %d, body: %s", w.Code, w.Body.String())
	}

	// Missing file when adding face should trigger 422
	var emptyMultipart bytes.Buffer
	mw := multipart.NewWriter(&emptyMultipart)
	_ = mw.WriteField("placeholder", "val")
	_ = mw.Close()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/emp_1/faces", &emptyMultipart)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing file, got: %d", w.Code)
	}

	// Malformed JSON should trigger 400 INVALID_FORMAT
	malformedBody := bytes.NewReader([]byte(`{"personId": invalid`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons", malformedBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got: %d, body: %s", w.Code, w.Body.String())
	}

	// personId is optional: omitting it must succeed and default personId to id.
	noPersonIDBody := bytes.NewReader([]byte(`{"name":"张三"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons", noPersonIDBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for optional personId, got: %d, body: %s", w.Code, w.Body.String())
	}
	var createdWithDefault struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdWithDefault); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if createdWithDefault.Data.ID == "" {
		t.Fatal("expected non-empty id")
	}
	if strings.HasPrefix(createdWithDefault.Data.ID, "person_") {
		t.Fatalf("id must not have person_ prefix: %q", createdWithDefault.Data.ID)
	}
	if createdWithDefault.Data.PersonID != nil {
		t.Fatalf("expected personId to be nil when omitted, got %v", createdWithDefault.Data.PersonID)
	}
}

func TestHandlerNotFoundErrors(t *testing.T) {
	router, _ := setupTestRouter(t)

	// Get non-existent person -> 404 PERSON_NOT_FOUND
	req := httptest.NewRequest(http.MethodGet, "/api/v1/persons/non_existent_id", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent person, got: %d", w.Code)
	}

	// Delete non-existent person -> 404 PERSON_NOT_FOUND
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/non_existent_id", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent person delete, got: %d", w.Code)
	}

	// Delete non-existent face -> 404 FACE_NOT_FOUND
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/any_id/faces/non_existent_face", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent face delete, got: %d", w.Code)
	}
}

// TestHandlerUpdatePerson covers the PUT path end to end, including the two rules that
// protect the external business key: it cannot be reassigned, and a blank name is
// rejected instead of being silently ignored.
func TestHandlerUpdatePerson(t *testing.T) {
	router, _ := setupTestRouter(t)

	createBody := bytes.NewReader([]byte(`{"personId":"EMP_1","name":"张三"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d, body: %s", w.Code, w.Body.String())
	}
	var createdResp struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdResp); err != nil {
		t.Fatalf("unmarshal created person: %v", err)
	}
	personRowID := createdResp.Data.ID

	put := func(body string) (*httptest.ResponseRecorder, map[string]any) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+personRowID, bytes.NewReader([]byte(body)))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var decoded map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("unmarshal update response: %v", err)
		}
		return w, decoded
	}

	// Renaming is allowed.
	w, decoded := put(`{"name":"李四"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on rename, got %d: %s", w.Code, w.Body.String())
	}
	data, _ := decoded["data"].(map[string]any)
	if data["name"] != "李四" {
		t.Errorf("expected updated name '李四', got %v", data["name"])
	}
	if data["personId"] != "EMP_1" {
		t.Errorf("expected personId to survive the rename, got %v", data["personId"])
	}

	// Reassigning the business key is refused with a dedicated code, not silently dropped.
	w, decoded = put(`{"personId":"EMP_OTHER"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 on personId change, got %d: %s", w.Code, w.Body.String())
	}
	if decoded["code"] != "PERSON_ID_IMMUTABLE" {
		t.Errorf("expected code PERSON_ID_IMMUTABLE, got %v", decoded["code"])
	}

	// Re-sending the current value is a no-op, not a conflict.
	if w, _ := put(`{"personId":"EMP_1"}`); w.Code != http.StatusOK {
		t.Fatalf("expected 200 when personId is unchanged, got %d: %s", w.Code, w.Body.String())
	}

	// A blank name must not be swallowed.
	w, decoded = put(`{"name":""}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 on blank name, got %d: %s", w.Code, w.Body.String())
	}
	if decoded["code"] != "NAME_REQUIRED" {
		t.Errorf("expected code NAME_REQUIRED, got %v", decoded["code"])
	}
}

// TestHandlerUpdatePersonNotFound pins the 404 for an unknown row id on the update path.
func TestHandlerUpdatePersonNotFound(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/persons/ghost", bytes.NewReader([]byte(`{"name":"李四"}`)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown person, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerCreateDuplicatePersonID pins the 409 for a business-key collision, which is
// distinct from a row-id collision and must name the field that collided.
func TestHandlerCreateDuplicatePersonID(t *testing.T) {
	router, _ := setupTestRouter(t)

	for i := 0; i < 2; i++ {
		body := bytes.NewReader([]byte(`{"personId":"EMP_1","name":"张三"}`))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if i == 0 {
			if w.Code != http.StatusCreated {
				t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
			}
			continue
		}
		if w.Code != http.StatusConflict {
			t.Fatalf("expected 409 for duplicate personId, got %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal conflict response: %v", err)
		}
		if resp.Code != "PERSON_ID_ALREADY_EXISTS" {
			t.Errorf("expected code PERSON_ID_ALREADY_EXISTS, got %q", resp.Code)
		}
	}
}

// TestHandlerListFacesMissingPerson pins the 404: an empty 200 would be indistinguishable
// from "this person has no enrolled photos".
func TestHandlerListFacesMissingPerson(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/persons/ghost/faces", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for a missing person, got %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Code != "PERSON_NOT_FOUND" {
		t.Errorf("expected code PERSON_NOT_FOUND, got %q", resp.Code)
	}
}

// TestHandlerAddFaceRejectsInvalidFileType pins the file extension validation rule on face enrollment.
func TestHandlerAddFaceRejectsInvalidFileType(t *testing.T) {
	router, _ := setupTestRouter(t)

	createBody := bytes.NewReader([]byte(`{"personId":"EMP_1","name":"张三"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got %d: %s", w.Code, w.Body.String())
	}
	var createdResp struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &createdResp); err != nil {
		t.Fatalf("unmarshal created person: %v", err)
	}
	personRowID := createdResp.Data.ID

	for _, filename := range []string{"script.sh", "file.txt", "archive.zip"} {
		req := makeMultipartFaceRequest("/api/v1/persons/"+personRowID+"/faces", filename, []byte("echo 1"))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("filename %q: expected 422, got %d: %s", filename, w.Code, w.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.Code != "INVALID_FILE_TYPE" {
			t.Errorf("filename %q: expected code INVALID_FILE_TYPE, got %q", filename, resp.Code)
		}
	}

	req = makeMultipartFaceRequest("/api/v1/persons/"+personRowID+"/faces", "face_01.jpg", testJPEGBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("a valid jpg must be accepted, got %d: %s", w.Code, w.Body.String())
	}
}

// TestHandlerAuditCoverage exercises every audited person write operation
// through the real router and asserts the resulting records.
func TestHandlerAuditCoverage(t *testing.T) {
	router, _, auditSvc := setupTestRouterWithAudit(t)

	createBody := bytes.NewReader([]byte(`{"personId":"EMP_AUDIT","name":"审计"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.9.9:1111"
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create failed: %d %s", w.Code, w.Body.String())
	}
	var created struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create response: %v", err)
	}
	id := created.Data.ID

	// Add a face, then delete it.
	req = makeMultipartFaceRequest("/api/v1/persons/"+id+"/faces", "enroll.jpg", testJPEGBytes)
	req.RemoteAddr = "192.168.9.9:1111"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add face failed: %d %s", w.Code, w.Body.String())
	}
	var faceResp struct {
		Data person.Face `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &faceResp); err != nil {
		t.Fatalf("unmarshal face response: %v", err)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/"+id+"/faces/"+faceResp.Data.ID, nil)
	req.RemoteAddr = "192.168.9.9:1111"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete face failed: %d %s", w.Code, w.Body.String())
	}

	// Update, then delete the person.
	updateBody := bytes.NewReader([]byte(`{"name":"审计-改"}`))
	req = httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+id, updateBody)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.9.9:1111"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update failed: %d %s", w.Code, w.Body.String())
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/"+id, nil)
	req.RemoteAddr = "192.168.9.9:1111"
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete failed: %d %s", w.Code, w.Body.String())
	}

	entries := drainAudit(t, auditSvc)
	want := []string{
		person.ActionPersonCreate,
		person.ActionPersonFaceAdd,
		person.ActionPersonFaceDelete,
		person.ActionPersonUpdate,
		person.ActionPersonDelete,
	}
	if len(entries) != len(want) {
		t.Fatalf("expected %d audit entries, got %d: %+v", len(want), len(entries), entries)
	}

	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		seen[e.Action] = true
		if e.Status != audit.StatusSuccess {
			t.Errorf("expected success for %s, got %q (%s)", e.Action, e.Status, e.ErrorMsg)
		}
		if e.IP != "192.168.9.9" {
			t.Errorf("expected the request IP on %s, got %q", e.Action, e.IP)
		}
	}
	for _, action := range want {
		if !seen[action] {
			t.Errorf("missing audit entry for %s", action)
		}
	}
}

// TestHandlerAuditDetailExcludesSensitiveFaceData asserts the enrollment photo
// path and the feature vector never reach the audit trail.
func TestHandlerAuditDetailExcludesSensitiveFaceData(t *testing.T) {
	router, _, auditSvc := setupTestRouterWithAudit(t)

	createBody := bytes.NewReader([]byte(`{"personId":"EMP_SEC","name":"机密"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var created struct {
		Data person.Person `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	req = makeMultipartFaceRequest("/api/v1/persons/"+created.Data.ID+"/faces", "secret-face.jpg", testJPEGBytes)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add face failed: %d %s", w.Code, w.Body.String())
	}

	entries := drainAudit(t, auditSvc)
	for _, e := range entries {
		if strings.Contains(e.Detail, "secret-face") {
			t.Errorf("enrollment photo path leaked into audit detail: %q", e.Detail)
		}
		if strings.Contains(e.Detail, "feature") {
			t.Errorf("feature data leaked into audit detail: %q", e.Detail)
		}
	}
}

// TestHandlerAuditRecordsRefusedUpdate asserts a rejected immutable-key change
// still produces a failed record instead of disappearing from the trail.
func TestHandlerAuditRecordsRefusedUpdate(t *testing.T) {
	router, _, auditSvc := setupTestRouterWithAudit(t)

	createBody := bytes.NewReader([]byte(`{"personId":"EMP_IMM","name":"不可变"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var created struct {
		Data person.Person `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)

	badUpdate := bytes.NewReader([]byte(`{"personId":"EMP_CHANGED"}`))
	req = httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+created.Data.ID, badUpdate)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	entries := drainAudit(t, auditSvc)
	var found bool
	for _, e := range entries {
		if e.Action == person.ActionPersonUpdate && e.Status == audit.StatusFailed {
			found = true
			if !strings.Contains(e.ErrorMsg, "PERSON_ID_IMMUTABLE") {
				t.Errorf("expected the stable error code, got %q", e.ErrorMsg)
			}
		}
	}
	if !found {
		t.Fatalf("expected a failed person.update record, got %+v", entries)
	}
}

func TestHandlerAddFaceMultipartAndGetImage(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 1. Create a person
	createBody := bytes.NewReader([]byte(`{"personId":"EMP_UPLOAD","name":"王五"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", createBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create person failed: %d", w.Code)
	}
	var created struct {
		Data person.Person `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	personID := created.Data.ID

	// 2. Prepare multipart upload with sample image content
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("CreateFormFile failed: %v", err)
	}
	sampleImageBytes := testPNGBytes
	if _, err := part.Write(sampleImageBytes); err != nil {
		t.Fatalf("write sample image failed: %v", err)
	}
	_ = writer.Close()

	// 3. Post multipart face enrollment
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+personID+"/faces", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("add face multipart failed: %d, body: %s", w.Code, w.Body.String())
	}

	var faceResp struct {
		Data person.Face `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &faceResp); err != nil {
		t.Fatalf("unmarshal face response failed: %v", err)
	}
	faceID := faceResp.Data.ID
	if faceID == "" {
		t.Fatalf("expected non-empty face ID")
	}

	// 4. Retrieve face image via GET /persons/:id/faces/:face_id/image
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons/"+personID+"/faces/"+faceID+"/image", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK fetching face image, got %d", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), sampleImageBytes) {
		t.Fatalf("fetched face image content mismatch")
	}

	// 5. Delete face and verify physical cleanup
	req = httptest.NewRequest(http.MethodDelete, "/api/v1/persons/"+personID+"/faces/"+faceID, nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("delete face failed: %d", w.Code)
	}

	// Verify image is gone
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons/"+personID+"/faces/"+faceID+"/image", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after face deletion, got %d", w.Code)
	}
}

func TestHandlerListPersonsSearch(t *testing.T) {
	router, _ := setupTestRouter(t)

	// Create 2 persons
	for _, p := range []struct {
		ID   string
		Name string
	}{
		{"EMP_SEARCH_1", "Alice Wonder"},
		{"EMP_SEARCH_2", "Bob Builder"},
	} {
		body := bytes.NewReader([]byte(`{"personId":"` + p.ID + `","name":"` + p.Name + `"}`))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusCreated {
			t.Fatalf("create %s failed: %d", p.Name, w.Code)
		}
	}

	// 1. Search for Alice
	req := httptest.NewRequest(http.MethodGet, "/api/v1/persons?search=Alice", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search Alice failed: %d", w.Code)
	}
	var respAlice struct {
		Data struct {
			Items []person.Person `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &respAlice); err != nil {
		t.Fatalf("unmarshal search Alice response: %v", err)
	}
	if respAlice.Data.Total != 1 || len(respAlice.Data.Items) != 1 || respAlice.Data.Items[0].Name != "Alice Wonder" {
		t.Fatalf("expected 1 result for Alice Wonder, got total=%d items=%+v", respAlice.Data.Total, respAlice.Data.Items)
	}

	// 2. Search for EMP_SEARCH_2 by personId
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons?search=SEARCH_2", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search SEARCH_2 failed: %d", w.Code)
	}
	var respBob struct {
		Data struct {
			Items []person.Person `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &respBob); err != nil {
		t.Fatalf("unmarshal search Bob response: %v", err)
	}
	if respBob.Data.Total != 1 || len(respBob.Data.Items) != 1 || respBob.Data.Items[0].PersonID == nil || *respBob.Data.Items[0].PersonID != "EMP_SEARCH_2" {
		t.Fatalf("expected 1 result for EMP_SEARCH_2, got total=%d items=%+v", respBob.Data.Total, respBob.Data.Items)
	}

	// 3. Search non-existent
	req = httptest.NewRequest(http.MethodGet, "/api/v1/persons?search=NonExistent", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("search non-existent failed: %d", w.Code)
	}
	var respNone struct {
		Data struct {
			Items []person.Person `json:"items"`
			Total int64           `json:"total"`
		} `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &respNone)
	if respNone.Data.Total != 0 || len(respNone.Data.Items) != 0 {
		t.Fatalf("expected 0 results, got total=%d", respNone.Data.Total)
	}
}

func TestHandlerBindPersonIDWhenEmpty(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 1. Create person without personId
	noPersonIDBody := bytes.NewReader([]byte(`{"name":"未绑定员工"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/persons", noPersonIDBody)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d", w.Code)
	}
	var created struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created response: %v", err)
	}
	if created.Data.PersonID != nil {
		t.Fatalf("expected nil personId, got %v", created.Data.PersonID)
	}

	// 2. Bind personId via PATCH
	bindBody := bytes.NewReader([]byte(`{"personId":"EMP_NEWLY_BOUND"}`))
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/persons/"+created.Data.ID, bindBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 OK on binding personId, got %d: %s", w.Code, w.Body.String())
	}
	var updated struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated response: %v", err)
	}
	if updated.Data.PersonID == nil || *updated.Data.PersonID != "EMP_NEWLY_BOUND" {
		t.Fatalf("expected personId 'EMP_NEWLY_BOUND', got %v", updated.Data.PersonID)
	}

	// 3. Modifying it again must fail with 409 PERSON_ID_IMMUTABLE
	rebindBody := bytes.NewReader([]byte(`{"personId":"EMP_CHANGED"}`))
	req = httptest.NewRequest(http.MethodPatch, "/api/v1/persons/"+created.Data.ID, rebindBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict on personId change, got %d: %s", w.Code, w.Body.String())
	}
}

func TestHandler_SetPrimaryFace(t *testing.T) {
	router, service, auditSvc := setupTestRouterWithAudit(t)
	ctx := context.Background()

	p, err := service.CreatePerson(ctx, person.CreatePersonRequest{Name: "Primary Tester"})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}

	// Upload face 1 (auto becomes primary)
	req1 := makeMultipartFaceRequest("/api/v1/persons/"+p.ID+"/faces", "face1.jpg", testJPEGBytes)
	w1 := httptest.NewRecorder()
	router.ServeHTTP(w1, req1)
	if w1.Code != http.StatusCreated {
		t.Fatalf("add face 1 failed: %d", w1.Code)
	}
	var resp1 struct {
		Data person.Face `json:"data"`
	}
	_ = json.Unmarshal(w1.Body.Bytes(), &resp1)
	face1ID := resp1.Data.ID

	// Upload face 2
	req2 := makeMultipartFaceRequest("/api/v1/persons/"+p.ID+"/faces", "face2.png", testPNGBytes)
	w2 := httptest.NewRecorder()
	router.ServeHTTP(w2, req2)
	if w2.Code != http.StatusCreated {
		t.Fatalf("add face 2 failed: %d", w2.Code)
	}
	var resp2 struct {
		Data person.Face `json:"data"`
	}
	_ = json.Unmarshal(w2.Body.Bytes(), &resp2)
	face2ID := resp2.Data.ID

	// 1. Success: switch primary face to face 2
	bodyJSON, _ := json.Marshal(person.SetPrimaryFaceRequest{FaceID: face2ID})
	req := httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+p.ID+"/primary-face", bytes.NewReader(bodyJSON))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("set primary face failed with %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data person.Person `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.Data.PrimaryFaceID == nil || *resp.Data.PrimaryFaceID != face2ID {
		t.Fatalf("expected PrimaryFaceID %q, got %v", face2ID, resp.Data.PrimaryFaceID)
	}

	// 2. Failure: non-existent face returns 404
	fakeBody, _ := json.Marshal(person.SetPrimaryFaceRequest{FaceID: "non_existent"})
	reqFake := httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+p.ID+"/primary-face", bytes.NewReader(fakeBody))
	reqFake.Header.Set("Content-Type", "application/json")
	wFake := httptest.NewRecorder()
	router.ServeHTTP(wFake, reqFake)
	if wFake.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for non-existent face, got %d", wFake.Code)
	}

	// 3. Validation failure: missing faceId returns 422
	emptyBody := []byte(`{}`)
	reqEmpty := httptest.NewRequest(http.MethodPut, "/api/v1/persons/"+p.ID+"/primary-face", bytes.NewReader(emptyBody))
	reqEmpty.Header.Set("Content-Type", "application/json")
	wEmpty := httptest.NewRecorder()
	router.ServeHTTP(wEmpty, reqEmpty)
	if wEmpty.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing faceId, got %d", wEmpty.Code)
	}

	// 4. Verify audit log entry
	entries := drainAudit(t, auditSvc)
	var found bool
	for _, e := range entries {
		if e.Action == person.ActionPersonPrimaryFaceSet {
			found = true
			if e.Target != "person:"+p.ID {
				t.Errorf("expected target 'person:%s', got %q", p.ID, e.Target)
			}
			break
		}
	}
	if !found {
		t.Fatalf("expected audit log for %s, got none in %+v", person.ActionPersonPrimaryFaceSet, entries)
	}
	_ = face1ID
}
