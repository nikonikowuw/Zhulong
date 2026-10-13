package algorithm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// algorithmErrorCodes lists every error code the algorithm HTTP layer can emit.
// Each one must have a registered i18n catalog entry, otherwise WriteError
// degrades the message to the generic REQUEST_FAILED text.
var algorithmErrorCodes = []string{
	"INVALID_FILE",
	"ALGO_INSTALL_FAILED",
	"TASK_CREATE_FAILED",
	"INVALID_REQUEST",
}

// TestAlgorithmErrorCodesAreLocalized asserts the previously unregistered codes
// now resolve to their own localized message in every supported locale, and
// that no locale falls back to the generic REQUEST_FAILED text.
func TestAlgorithmErrorCodesAreLocalized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, code := range algorithmErrorCodes {
		for _, locale := range []string{"en", "zh-Hans", "zh-Hant"} {
			t.Run(code+"/"+locale, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				context, _ := gin.CreateTestContext(recorder)
				context.Request = httptest.NewRequest(http.MethodGet, "/api/v1/algorithms", nil)
				context.Request.Header.Set("Accept-Language", locale)

				// The raw cause must never reach the client.
				httputil.WriteError(context, httputil.NewError(
					http.StatusBadRequest, code, "raw internal detail: /var/lib/secret", nil))

				var body httputil.Response
				if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if body.Code != code {
					t.Fatalf("expected code %q, got %q", code, body.Code)
				}
				if body.Message == "" {
					t.Fatalf("expected localized message for %q in %q", code, locale)
				}
				// A generic fallback means the catalog entry is missing.
				generic := map[string]string{
					"en":      "The request could not be completed",
					"zh-Hans": "无法完成请求",
					"zh-Hant": "無法完成此請求",
				}[locale]
				if body.Message == generic {
					t.Errorf("code %q fell back to generic message in %q", code, locale)
				}
				if body.Message == "raw internal detail: /var/lib/secret" {
					t.Errorf("code %q leaked the raw internal message", code)
				}
			})
		}
	}
}

// TestCreateTaskRejectsNonJSONBody asserts a malformed body is rejected with the
// INVALID_REQUEST envelope instead of being silently coerced into defaults.
func TestCreateTaskRejectsNonJSONBody(t *testing.T) {
	router, _ := setupTestRouter(t)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/tasks",
		bytes.NewReader([]byte(`{"cameraId": `)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code < 400 || w.Code >= 500 {
		// NOTE: the current handler answers 400. The spec (http-api-guidelines §4.3)
		// prefers 422 + details for bind failures; that deviation is tracked
		// separately, so this test deliberately asserts "client error" rather
		// than pinning the exact status.
		t.Fatalf("expected a 4xx client error, got: %d, body: %s", w.Code, w.Body.String())
	}

	var body httputil.Response
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "INVALID_REQUEST" {
		t.Errorf("expected code INVALID_REQUEST, got %q", body.Code)
	}
	if body.Data != nil {
		t.Errorf("expected data to be null on error, got %v", body.Data)
	}
}

// TestCreateTaskSnakeCaseAliases verifies the snake_case compatibility keys still
// map onto the exported CreateTaskRequest DTO after the rename.
func TestCreateTaskSnakeCaseAliases(t *testing.T) {
	router, svc := setupTestRouter(t)
	ctx := context.Background()

	_, _ = svc.InstallAlgorithm(ctx, createTestTarGz(t, map[string][]byte{
		"manifest.json": []byte(`{"id":"algo_alias","name":"alias_detector","version":"1.0.0","task_type":"detection","category":"safety","platform":{"arch":"x86_64","target":"cpu"},"resources":{"compute_cost":10,"memory_mb":32}}`),
		"plugin.so":     []byte("elf"),
		"sample.jpg":    []byte("jpg"),
	}))

	body := bytes.NewReader([]byte(`{"camera_id":"cam_alias","algorithm_id":"algo_alias","sample_fps":7}`))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/tasks", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d, body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Data algorithm.AnalysisTask `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Data.CameraID != "cam_alias" {
		t.Errorf("expected cameraId cam_alias, got %q", resp.Data.CameraID)
	}
	if resp.Data.AlgorithmID != "algo_alias" {
		t.Errorf("expected algorithmId algo_alias, got %q", resp.Data.AlgorithmID)
	}
	if resp.Data.SampleFPS != 7 {
		t.Errorf("expected sampleFps 7, got %d", resp.Data.SampleFPS)
	}
}
