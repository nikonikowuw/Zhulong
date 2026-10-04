package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

type sampleRequest struct {
	DisplayName string `json:"displayName" binding:"required,min=3"`
	Age         int    `json:"age" binding:"required,min=18"`
}

type sampleResponse struct {
	Greeting string `json:"greeting"`
}

func TestHandleJSON_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	router.POST("/test", HandleJSON(func(c *gin.Context, req sampleRequest) (sampleResponse, error) {
		return sampleResponse{Greeting: "Hello " + req.DisplayName}, nil
	}))

	body, _ := json.Marshal(sampleRequest{DisplayName: "Alice", Age: 25})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var resp struct {
		Code string         `json:"code"`
		Data sampleResponse `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != "OK" || resp.Data.Greeting != "Hello Alice" {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestHandleJSON_ValidationFailureWithJSONTags(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	router.POST("/test", HandleJSON(func(c *gin.Context, req sampleRequest) (sampleResponse, error) {
		return sampleResponse{}, nil
	}))

	body, _ := json.Marshal(sampleRequest{DisplayName: "Al", Age: 12})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", recorder.Code)
	}

	var resp struct {
		Code    string        `json:"code"`
		Details []FieldDetail `json:"details"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != "VALIDATION_FAILED" {
		t.Fatalf("expected VALIDATION_FAILED, got %s", resp.Code)
	}
	if len(resp.Details) != 2 {
		t.Fatalf("expected 2 field details, got %d", len(resp.Details))
	}

	fields := map[string]string{}
	for _, d := range resp.Details {
		fields[d.Field] = d.Code
	}
	if fields["displayName"] != "MIN_LENGTH" {
		t.Fatalf("expected displayName: MIN_LENGTH, got %s", fields["displayName"])
	}
	if fields["age"] != "MIN_VALUE" {
		t.Fatalf("expected age: MIN_VALUE, got %s", fields["age"])
	}
}

func TestHandleJSON_DomainErrorUnauthenticated(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	router.POST("/test", HandleJSON(func(c *gin.Context, req sampleRequest) (sampleResponse, error) {
		return sampleResponse{}, apperr.Unauthenticated("INVALID_CREDENTIALS", "invalid credentials", nil)
	}))

	body, _ := json.Marshal(sampleRequest{DisplayName: "Alice", Age: 25})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", recorder.Code)
	}

	var resp struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Code != "INVALID_CREDENTIALS" {
		t.Fatalf("expected INVALID_CREDENTIALS, got %s", resp.Code)
	}
}

func TestHandle_SuccessAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	router.GET("/ok", Handle(func(c *gin.Context) (string, error) {
		return "hello world", nil
	}))

	router.GET("/forbidden", Handle(func(c *gin.Context) (string, error) {
		return "", apperr.PermissionDenied("FORBIDDEN", "forbidden", nil)
	}))

	// Test Success
	w1 := httptest.NewRecorder()
	r1 := httptest.NewRequest(http.MethodGet, "/ok", nil)
	router.ServeHTTP(w1, r1)
	if w1.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w1.Code)
	}

	// Test Error
	w2 := httptest.NewRecorder()
	r2 := httptest.NewRequest(http.MethodGet, "/forbidden", nil)
	router.ServeHTTP(w2, r2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", w2.Code)
	}
}

func TestHandleJSON_MaxBodyLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()

	router.POST("/limited", func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 10)
		c.Next()
	}, HandleJSON(func(c *gin.Context, req sampleRequest) (sampleResponse, error) {
		return sampleResponse{}, nil
	}))

	bigJSON, _ := json.Marshal(map[string]string{
		"displayName": "Alice",
		"padding":     strings.Repeat("a", 100),
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/limited", bytes.NewReader(bigJSON))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413, got %d", recorder.Code)
	}
}

func TestWriteError_WithApperrAndInternalCause(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	underlying := errors.New("sql: no rows")
	err := apperr.NotFound("USER_NOT_FOUND", "user not found", underlying)

	WriteError(c, err)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", recorder.Code)
	}
}

func TestWriteError_UnregisteredDomainCodePreservesFallbackMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/test", nil)

	customMsg := "Camera resolution not supported by hardware"
	err := apperr.Invalid("CAMERA_RES_UNSUPPORTED", customMsg, nil)

	WriteError(c, err)

	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", recorder.Code)
	}

	var resp Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.Code != "CAMERA_RES_UNSUPPORTED" {
		t.Fatalf("expected code CAMERA_RES_UNSUPPORTED, got %s", resp.Code)
	}
	if resp.Message != customMsg {
		t.Fatalf("expected message %q, got %q", customMsg, resp.Message)
	}
}
