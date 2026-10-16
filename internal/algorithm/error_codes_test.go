package algorithm_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// algorithmErrorCodes lists every error code the algorithm HTTP layer can emit.
// Each one must have a registered i18n catalog entry, otherwise WriteError
// degrades the message to the generic REQUEST_FAILED text.
var algorithmErrorCodes = []string{
	"INVALID_FILE",
	"ALGO_INSTALL_FAILED",
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
