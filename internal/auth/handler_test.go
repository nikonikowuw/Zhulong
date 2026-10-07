package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, AuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore.DB)
	sessionStore := NewMemorySessionStore(1 * time.Hour)
	svc := NewAuthService(userStore, sessionStore, nil, zap.NewNop())
	handler := NewHandler(svc)

	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	// Add a protected test endpoint
	api.GET("/protected", RequireAuth(svc), func(c *gin.Context) {
		user, _ := GetCurrentUser(c)
		httputil.Success(c, gin.H{"hello": user.Username})
	})

	return router, svc
}

func TestAuthHandler_Status(t *testing.T) {
	router, _ := setupTestRouter(t)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp struct {
		Code string             `json:"code"`
		Data AuthStatusResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Data.Initialized {
		t.Fatal("expected initialized = false")
	}
}

func TestAuthHandler_ValidationDetailsUseJSONFieldsAndLocalizedCodes(t *testing.T) {
	router, _ := setupTestRouter(t)
	tests := []struct {
		name     string
		request  InitAdminRequest
		language string
		field    string
		code     string
		message  string
	}{
		{
			name:     "username minimum length",
			request:  InitAdminRequest{Username: "ab", Password: "password123", ConfirmPassword: "password123"},
			language: "zh-Hans",
			field:    "username",
			code:     "MIN_LENGTH",
			message:  "长度小于允许的最小值",
		},
		{
			name:     "password minimum length",
			request:  InitAdminRequest{Username: "admin", Password: "short", ConfirmPassword: "short"},
			language: "zh-Hant",
			field:    "password",
			code:     "PASSWORD_TOO_SHORT",
			message:  "密碼長度不能少於 8 位",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := json.Marshal(test.request)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Accept-Language", test.language)
			router.ServeHTTP(recorder, request)

			var response struct {
				Code    string                 `json:"code"`
				Details []httputil.FieldDetail `json:"details"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if recorder.Code != http.StatusUnprocessableEntity || response.Code != "VALIDATION_FAILED" {
				t.Fatalf("unexpected response status/code: %d %+v", recorder.Code, response)
			}
			if len(response.Details) != 1 || response.Details[0].Field != test.field || response.Details[0].Code != test.code || response.Details[0].Message != test.message {
				t.Fatalf("unexpected validation details: %+v", response.Details)
			}
		})
	}
}

func TestAuthHandler_InitAndLoginFlow(t *testing.T) {
	router, _ := setupTestRouter(t)

	// 1. Protected endpoint returns 401 without cookie
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}

	// Logout requires an active session too.
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 when logging out without a session, got %d", w.Code)
	}

	// 2. Init with password mismatch returns 422
	initBody, _ := json.Marshal(InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "otherPassword",
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 for mismatch, got %d", w.Code)
	}

	// 3. Init with valid input returns 200 and sets cookie
	initBody, _ = json.Marshal(InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	cookies := w.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil || sessionCookie.Value == "" {
		t.Fatal("expected session cookie to be set")
	}

	// 4. Second init returns 403
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for duplicate init, got %d", w.Code)
	}

	// 5. Test protected endpoint with session cookie
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	req.AddCookie(sessionCookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on protected endpoint, got %d", w.Code)
	}

	// 6. Test /me endpoint with session cookie
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	req.AddCookie(sessionCookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on /me, got %d", w.Code)
	}
	var meResponse struct {
		Data UserResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &meResponse); err != nil {
		t.Fatalf("decode /me response: %v", err)
	}
	if meResponse.Data.CreatedAt.IsZero() {
		t.Fatalf("expected /me createdAt to be populated, got %+v", meResponse.Data)
	}

	// 7. Test logout
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	req.AddCookie(sessionCookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on logout, got %d", w.Code)
	}

	// 8. Protected endpoint fails again with the logged-out cookie
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodGet, "/api/v1/protected", nil)
	req.AddCookie(sessionCookie)
	router.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 after logout, got %d", w.Code)
	}

	// 9. Login with correct credentials
	loginBody, _ := json.Marshal(LoginRequest{
		Username: "admin",
		Password: "password123",
	})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 on login, got %d: %s", w.Code, w.Body.String())
	}
}

type testAuditor struct {
	entries []audit.Entry
}

func (m *testAuditor) Record(entry audit.Entry) {
	m.entries = append(m.entries, entry)
}

func TestAuthHandler_AuditLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dbStore := setupTestDB(t)
	userStore := NewUserStore(dbStore.DB)
	sessionStore := NewMemorySessionStore(1 * time.Hour)
	svc := NewAuthService(userStore, sessionStore, nil, zap.NewNop())
	auditor := &testAuditor{}
	handler := NewHandler(svc, auditor)

	router := gin.New()
	api := router.Group("/api/v1")
	handler.RegisterRoutes(api)

	// 1. InitAdmin
	initBody, _ := json.Marshal(InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/auth/init", bytes.NewReader(initBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	// 2. Failed Login
	badLogin, _ := json.Marshal(LoginRequest{Username: "admin", Password: "wrong"})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(badLogin))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	// 3. Successful Login
	goodLogin, _ := json.Marshal(LoginRequest{Username: "admin", Password: "password123"})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/login", bytes.NewReader(goodLogin))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	cookies := w.Result().Cookies()

	// 4. Logout
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	router.ServeHTTP(w, req)

	// Verify entries
	if len(auditor.entries) != 4 {
		t.Fatalf("expected 4 audit entries, got %d", len(auditor.entries))
	}
	if auditor.entries[0].Action != audit.ActionAuthInit || auditor.entries[0].Status != audit.StatusSuccess {
		t.Errorf("entry 0 mismatch: %+v", auditor.entries[0])
	}
	if auditor.entries[1].Action != audit.ActionAuthLogin || auditor.entries[1].Status != audit.StatusFailed {
		t.Errorf("entry 1 mismatch: %+v", auditor.entries[1])
	}
	if auditor.entries[2].Action != audit.ActionAuthLogin || auditor.entries[2].Status != audit.StatusSuccess {
		t.Errorf("entry 2 mismatch: %+v", auditor.entries[2])
	}
	if auditor.entries[3].Action != audit.ActionAuthLogout || auditor.entries[3].Status != audit.StatusSuccess {
		t.Errorf("entry 3 mismatch: %+v", auditor.entries[3])
	}
}
