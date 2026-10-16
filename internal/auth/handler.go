package auth

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Handler handles HTTP requests for authentication and identity.
type Handler struct {
	service AuthService
}

// NewHandler constructs an authentication HTTP Handler.
func NewHandler(service AuthService) *Handler {
	return &Handler{service: service}
}

// RegisterRoutes mounts auth endpoints onto the provided gin.RouterGroup.
//
// init and login declare their audit intent before HandleJSON binds the body, so
// a malformed or rejected payload still leaves a record. logout is tagged
// directly because it has no request body.
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	authGroup := rg.Group("/auth")
	authGroup.GET("/status", httputil.Handle(h.statusHandler))
	authGroup.POST("/init", audithttp.TagMiddleware(audit.ActionAuthInit, "system", "Initialize administrator"), httputil.HandleJSON(h.initHandler))
	authGroup.POST("/login", audithttp.TagMiddleware(audit.ActionAuthLogin, "user:", "Login"), httputil.HandleJSON(h.loginHandler))
	authGroup.POST("/logout", RequireAuth(h.service), h.logoutHandler)
	authGroup.GET("/me", RequireAuth(h.service), httputil.Handle(h.meHandler))
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, token, maxAge, "/", "", false, true)
}

func (h *Handler) sessionCookieMaxAge() int {
	if h.service != nil {
		ttl := h.service.SessionTTL()
		if ttl > 0 {
			return int(ttl / time.Second)
		}
	}
	return DefaultSessionCookieMaxAge
}

func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, "", -1, "/", "", false, true)
}

// statusHandler godoc
// @Summary      Get system initialization status
// @Tags         auth
// @Produce      json
// @Success      200  {object}  httputil.Response{data=AuthStatusResponse}
// @Router       /auth/status [get]
func (h *Handler) statusHandler(c *gin.Context) (AuthStatusResponse, error) {
	return h.service.GetStatus(c.Request.Context())
}

// initHandler godoc
// @Summary      Initialize administrator account
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body InitAdminRequest true "Admin credentials"
// @Success      200  {object}  httputil.Response{data=UserResponse}
// @Failure      403  {object}  httputil.Response
// @Failure      422  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Router       /auth/init [post]
func (h *Handler) initHandler(c *gin.Context, req InitAdminRequest) (UserResponse, error) {
	// The attempted username is only a target hint; the audit actor stays
	// anonymous until initialization actually succeeds.
	audithttp.TagTarget(c, "user:"+req.Username)

	user, token, err := h.service.InitAdmin(c.Request.Context(), req)
	if err != nil {
		return UserResponse{}, err
	}
	audithttp.TagActor(c, user.Username)
	audithttp.TagTarget(c, "user:"+user.Username)
	setSessionCookie(c, token, h.sessionCookieMaxAge())
	return user, nil
}

// loginHandler godoc
// @Summary      Login as administrator
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        request body LoginRequest true "Login credentials"
// @Success      200  {object}  httputil.Response{data=UserResponse}
// @Failure      401  {object}  httputil.Response
// @Failure      429  {object}  httputil.Response
// @Failure      413  {object}  httputil.Response
// @Router       /auth/login [post]
func (h *Handler) loginHandler(c *gin.Context, req LoginRequest) (UserResponse, error) {
	// A failed login keeps the anonymous actor; the attempted username is only
	// a target hint and never an authenticated identity.
	audithttp.TagTarget(c, "user:"+req.Username)

	user, token, err := h.service.Login(c.Request.Context(), c.ClientIP(), req)
	if err != nil {
		return UserResponse{}, err
	}
	audithttp.TagActor(c, user.Username)
	audithttp.TagTarget(c, "user:"+user.Username)
	setSessionCookie(c, token, h.sessionCookieMaxAge())
	return user, nil
}

// logoutHandler godoc
// @Summary      Logout administrator session
// @Tags         auth
// @Produce      json
// @Success      200  {object}  httputil.Response{data=object}
// @Failure      401  {object}  httputil.Response
// @Router       /auth/logout [post]
func (h *Handler) logoutHandler(c *gin.Context) {
	// Resolve the account name for the audit target and actor without ever
	// defaulting to a hardcoded admin.
	username := ""
	if u, ok := GetCurrentUser(c); ok && u.Username != "" {
		username = u.Username
	}
	token := GetSessionToken(c)
	if token == "" {
		token, _ = c.Cookie(SessionCookieName)
	}
	if token != "" {
		if session, ok := h.service.ValidateSession(token); ok && session.Username != "" {
			username = session.Username
		}
		h.service.Logout(token)
	}

	target := "user:"
	if username != "" {
		target = "user:" + username
	}
	audithttp.Tag(c, audit.ActionAuthLogout, target, "Logout")
	if username != "" {
		audithttp.TagActor(c, username)
	}

	clearSessionCookie(c)
	httputil.Success(c, gin.H{"loggedOut": true})
}

// meHandler godoc
// @Summary      Get currently authenticated user
// @Tags         auth
// @Produce      json
// @Success      200  {object}  httputil.Response{data=UserResponse}
// @Failure      401  {object}  httputil.Response
// @Router       /auth/me [get]
func (h *Handler) meHandler(c *gin.Context) (UserResponse, error) {
	sessionUser, ok := GetCurrentUser(c)
	if !ok {
		return UserResponse{}, apperr.Unauthenticated("UNAUTHORIZED", "Authentication required", nil)
	}
	user, err := h.service.GetCurrentUser(c.Request.Context(), sessionUser.ID)
	if errors.Is(err, ErrUserNotFound) {
		h.service.Logout(GetSessionToken(c))
		clearSessionCookie(c)
		return UserResponse{}, apperr.Unauthenticated("UNAUTHORIZED", "Authentication required", err)
	}
	if err != nil {
		return UserResponse{}, err
	}
	return user, nil
}
