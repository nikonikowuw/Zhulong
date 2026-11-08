package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

func setupTestRouter(t *testing.T) (*gin.Engine, AuthService) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, _, _ := newTestService(t)
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil)
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
			if len(response.Details) != 1 {
				t.Fatalf("expected exactly one detail, got %+v", response.Details)
			}

			detail := response.Details[0]
			matchesExpected := detail.Field == test.field &&
				detail.Code == test.code &&
				detail.Message == test.message
			if !matchesExpected {
				t.Fatalf("unexpected validation detail: %+v", detail)
			}
		})
	}
}

func findSessionCookie(cookies []*http.Cookie) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == SessionCookieName {
			return cookie
		}
	}
	return nil
}

// jsonPost describes a JSON body request. Language and RemoteAddr are optional:
// the former drives error localization, the latter is the connection source the
// audit middleware records instead of a header.
type jsonPost struct {
	path       string
	payload    any
	language   string
	remoteAddr string
}

func postJSON(t *testing.T, router *gin.Engine, post jsonPost) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(post.payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, post.path, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if post.language != "" {
		request.Header.Set("Accept-Language", post.language)
	}
	if post.remoteAddr != "" {
		request.RemoteAddr = post.remoteAddr
	}

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestAuthHandler_InitAndLoginFlow(t *testing.T) {
	router, svc := setupTestRouter(t)
	credentials := InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	}
	var sessionCookie *http.Cookie

	t.Run("protected endpoint rejects a missing session", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	t.Run("logout rejects a missing session", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 when logging out without a session, got %d", w.Code)
		}
	})

	t.Run("init rejects a password mismatch", func(t *testing.T) {
		mismatch := credentials
		mismatch.ConfirmPassword = "otherPassword"
		w := postJSON(t, router, jsonPost{path: "/api/v1/auth/init", payload: mismatch})
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 for mismatch, got %d", w.Code)
		}
	})

	t.Run("init succeeds and issues a hardened cookie", func(t *testing.T) {
		w := postJSON(t, router, jsonPost{path: "/api/v1/auth/init", payload: credentials})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
		}

		cookie := findSessionCookie(w.Result().Cookies())
		if cookie == nil || cookie.Value == "" {
			t.Fatal("expected session cookie to be set")
		}
		if !cookie.HttpOnly {
			t.Error("session cookie must be HttpOnly")
		}
		if cookie.SameSite != http.SameSiteLaxMode {
			t.Errorf("expected SameSite=Lax, got %v", cookie.SameSite)
		}
		if cookie.Path != "/" {
			t.Errorf("expected Path=/, got %q", cookie.Path)
		}
		if cookie.MaxAge != int(svc.SessionTTL().Seconds()) {
			t.Errorf("expected MaxAge=%d, got %d", int(svc.SessionTTL().Seconds()), cookie.MaxAge)
		}
		sessionCookie = cookie
	})

	t.Run("second init is rejected", func(t *testing.T) {
		w := postJSON(t, router, jsonPost{path: "/api/v1/auth/init", payload: credentials})
		if w.Code != http.StatusForbidden {
			t.Fatalf("expected 403 for duplicate init, got %d", w.Code)
		}
	})

	t.Run("protected endpoint accepts the session and renews the cookie", func(t *testing.T) {
		if sessionCookie == nil {
			t.Fatal("no session cookie from the init step")
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
		req.AddCookie(sessionCookie)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on protected endpoint, got %d", w.Code)
		}

		renewed := findSessionCookie(w.Result().Cookies())
		if renewed == nil || renewed.Value != sessionCookie.Value {
			t.Fatalf("expected the session cookie to be renewed in place: %+v", renewed)
		}
		if renewed.MaxAge != int(svc.SessionTTL().Seconds()) {
			t.Errorf("expected renewed MaxAge=%d, got %d", int(svc.SessionTTL().Seconds()), renewed.MaxAge)
		}
	})

	t.Run("me returns the stored account", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
		req.AddCookie(sessionCookie)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on /me, got %d", w.Code)
		}

		var response struct {
			Data UserResponse `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatalf("decode /me response: %v", err)
		}
		if response.Data.CreatedAt.IsZero() {
			t.Fatalf("expected /me createdAt to be populated, got %+v", response.Data)
		}
	})

	t.Run("logout clears the cookie without re-issuing it", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
		req.AddCookie(sessionCookie)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on logout, got %d", w.Code)
		}

		// RequireAuth must not renew the cookie it is about to delete, otherwise the
		// browser receives two conflicting Set-Cookie headers.
		issued := []*http.Cookie{}
		for _, cookie := range w.Result().Cookies() {
			if cookie.Name == SessionCookieName {
				issued = append(issued, cookie)
			}
		}
		if len(issued) != 1 {
			t.Fatalf("expected exactly one session Set-Cookie on logout, got %d", len(issued))
		}
		if issued[0].Value != "" || issued[0].MaxAge >= 0 {
			t.Fatalf("expected the session cookie to be expired, got %+v", issued[0])
		}
	})

	t.Run("protected endpoint rejects the logged-out session", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/protected", nil)
		req.AddCookie(sessionCookie)
		router.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401 after logout, got %d", w.Code)
		}
	})

	t.Run("login succeeds with the configured credentials", func(t *testing.T) {
		w := postJSON(t, router, jsonPost{
			path: "/api/v1/auth/login",
			payload: LoginRequest{
				Username: credentials.Username,
				Password: credentials.Password,
			},
		})
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200 on login, got %d: %s", w.Code, w.Body.String())
		}
		if findSessionCookie(w.Result().Cookies()) == nil {
			t.Fatal("expected a session cookie on login")
		}
	})
}

// A password that satisfies the 64-rune binding contract can still exceed the
// 72 bytes bcrypt accepts. That must be a client error, never a 500.
func TestAuthHandler_InitRejectsPasswordBeyondBcryptLimit(t *testing.T) {
	router, _ := setupTestRouter(t)
	password := strings.Repeat("密", 64) // 64 runes, 192 bytes

	w := postJSON(t, router, jsonPost{
		path: "/api/v1/auth/init",
		payload: InitAdminRequest{
			Username:        "admin",
			Password:        password,
			ConfirmPassword: password,
		},
		language: "zh-Hans",
	})

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}

	var response struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != "PASSWORD_TOO_LONG" {
		t.Fatalf("expected PASSWORD_TOO_LONG, got %+v", response)
	}
	if !strings.Contains(response.Message, "密码过长") {
		t.Fatalf("expected a localized message, got %q", response.Message)
	}
}

func TestAuthHandler_AuditLogging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc, dbStore, _ := newTestService(t)
	handler := NewHandler(svc)

	// Wire the real audit middleware so the assertions cover the actual
	// declarative path rather than a test double.
	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize:    100,
		BatchSize:     10,
		FlushInterval: 5 * time.Millisecond,
		MaxEntries:    1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Stop(context.Background()); err != nil {
			t.Errorf("stop audit service: %v", err)
		}
	})

	router := gin.New()
	if err := router.SetTrustedProxies(nil); err != nil {
		t.Fatalf("SetTrustedProxies: %v", err)
	}
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	handler.RegisterRoutes(api)

	const clientAddr = "192.168.7.7:5555"
	credentials := InitAdminRequest{
		Username:        "admin",
		Password:        "password123",
		ConfirmPassword: "password123",
	}

	initResponse := postJSON(t, router, jsonPost{
		path:       "/api/v1/auth/init",
		payload:    credentials,
		remoteAddr: clientAddr,
	})
	if initResponse.Code != http.StatusOK {
		t.Fatalf("expected 200 on init, got %d", initResponse.Code)
	}

	postJSON(t, router, jsonPost{
		path:       "/api/v1/auth/login",
		payload:    LoginRequest{Username: "admin", Password: "wrong"},
		remoteAddr: clientAddr,
	})
	loginResponse := postJSON(t, router, jsonPost{
		path:       "/api/v1/auth/login",
		payload:    LoginRequest{Username: "admin", Password: "password123"},
		remoteAddr: clientAddr,
	})

	logoutRequest := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	logoutRequest.RemoteAddr = clientAddr
	for _, cookie := range loginResponse.Result().Cookies() {
		logoutRequest.AddCookie(cookie)
	}
	router.ServeHTTP(httptest.NewRecorder(), logoutRequest)

	// Drain the asynchronous queue before asserting.
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}

	entries, total, err := auditSvc.List(context.Background(), audit.Filter{Limit: 20})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}
	if total != 4 {
		t.Fatalf("expected 4 audit entries, got %d", total)
	}

	assertAuditEntries(t, entries)
}

// assertAuditEntries checks the four entries the auth routes must produce. The
// failed login shares its action with the successful one, so both are located by
// status instead of by a map key.
func assertAuditEntries(t *testing.T, entries []audit.AuditLog) {
	t.Helper()

	byAction := make(map[string]audit.AuditLog, len(entries))
	for _, entry := range entries {
		byAction[entry.Action] = entry
	}

	initEntry, ok := byAction[audit.ActionAuthInit]
	if !ok || initEntry.Status != audit.StatusSuccess {
		t.Errorf("auth.init mismatch: %+v", initEntry)
	}
	if initEntry.Username != "admin" {
		t.Errorf("expected the configured account name, got %q", initEntry.Username)
	}
	if initEntry.IP != "192.168.7.7" {
		t.Errorf("expected the connection source IP, got %q", initEntry.IP)
	}

	var failedLogin, okLogin bool
	for _, entry := range entries {
		if entry.Action != audit.ActionAuthLogin {
			continue
		}
		switch entry.Status {
		case audit.StatusFailed:
			failedLogin = true
			if entry.Username != audit.UsernameAnonymous {
				t.Errorf("failed login must stay anonymous, got %q", entry.Username)
			}
		case audit.StatusSuccess:
			okLogin = true
		}
	}
	if !failedLogin {
		t.Error("expected a failed auth.login entry")
	}
	if !okLogin {
		t.Error("expected a successful auth.login entry")
	}

	logoutEntry, ok := byAction[audit.ActionAuthLogout]
	if !ok || logoutEntry.Status != audit.StatusSuccess {
		t.Errorf("auth.logout mismatch: %+v", logoutEntry)
	}
}
