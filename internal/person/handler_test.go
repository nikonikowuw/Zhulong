package person_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, *person.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	comp := person.NewPersonComponents(dbStore, nil, zap.NewNop())

	r := gin.New()
	api := r.Group("/api/v1")
	comp.Handler.RegisterRoutes(api)

	return r, comp.Service
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
	face1Body := bytes.NewReader([]byte(`{"imagePath":"/data/faces/face_01.jpg"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+personRowID+"/faces", face1Body)
	req.Header.Set("Content-Type", "application/json")
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
	face2Body := bytes.NewReader([]byte(`{"imagePath":"/data/faces/face_02.jpg"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+personRowID+"/faces", face2Body)
	req.Header.Set("Content-Type", "application/json")
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
	if resp.Data.PersonID != "EMP_9527" {
		t.Errorf("expected personId 'EMP_9527', got '%s'", resp.Data.PersonID)
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

	// Add 2 faces for Alice without IDs, must not collide
	f1Body := bytes.NewReader([]byte(`{"imagePath":"/face1.jpg"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+resp1.Data.ID+"/faces", f1Body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created for face 1, got: %d", w.Code)
	}

	f2Body := bytes.NewReader([]byte(`{"imagePath":"/face2.jpg"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+resp1.Data.ID+"/faces", f2Body)
	req.Header.Set("Content-Type", "application/json")
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

	// Missing imagePath when adding face should trigger 422
	faceBody := bytes.NewReader([]byte(`{}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/emp_1/faces", faceBody)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for missing imagePath, got: %d", w.Code)
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
	if createdWithDefault.Data.PersonID != createdWithDefault.Data.ID {
		t.Fatalf("expected personId %q to match id %q", createdWithDefault.Data.PersonID, createdWithDefault.Data.ID)
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

// TestHandlerAddFaceRejectsUncleanImagePath pins the containment rule on the client-supplied
// enrollment-photo reference end to end.
func TestHandlerAddFaceRejectsUncleanImagePath(t *testing.T) {
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

	for _, path := range []string{"../../etc/passwd", "faces/relative.jpg", "/data/faces/"} {
		body := bytes.NewReader([]byte(`{"imagePath":"` + path + `"}`))
		req := httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+personRowID+"/faces", body)
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("imagePath %q: expected 422, got %d: %s", path, w.Code, w.Body.String())
		}
		var resp struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if resp.Code != "INVALID_IMAGE_PATH" {
			t.Errorf("imagePath %q: expected code INVALID_IMAGE_PATH, got %q", path, resp.Code)
		}
	}

	body := bytes.NewReader([]byte(`{"imagePath":"/data/faces/face_01.jpg"}`))
	req = httptest.NewRequest(http.MethodPost, "/api/v1/persons/"+personRowID+"/faces", body)
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("a clean absolute imagePath must be accepted, got %d: %s", w.Code, w.Body.String())
	}
}
