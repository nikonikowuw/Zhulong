package auth

import (
	"errors"
	"fmt"

	"github.com/gin-gonic/gin"
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
	authGroup.POST(
		"/init",
		audithttp.TagMiddleware(audit.ActionAuthInit, "system", "Initialize administrator"),
		httputil.HandleJSON(h.initHandler),
	)
	authGroup.POST(
		"/login",
		audithttp.TagMiddleware(audit.ActionAuthLogin, "user:", "Login"),
		httputil.HandleJSON(h.loginHandler),
	)
	// logout clears the cookie itself, so RequireAuth must not re-issue it first.
	authGroup.POST("/logout", withoutCookieRenewal(), RequireAuth(h.service), h.logoutHandler)
	authGroup.GET("/me", RequireAuth(h.service), httputil.Handle(h.meHandler))
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
	setSessionCookie(c, token, sessionCookieMaxAge(h.service))
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
	setSessionCookie(c, token, sessionCookieMaxAge(h.service))
	return user, nil
}

// logoutHandler godoc
// @Summary      Logout administrator session
// @Tags         auth
// @Produce      json
// @Success      200  {object}  httputil.Response{data=LogoutResponse}
// @Failure      401  {object}  httputil.Response
// @Router       /auth/logout [post]
func (h *Handler) logoutHandler(c *gin.Context) {
	username, token := h.sessionIdentity(c)
	h.service.Logout(token)

	target := "user:" + username
	audithttp.Tag(
		c,
		audit.ActionAuthLogout,
		target,
		"Logout",
	)
	if username != "" {
		audithttp.TagActor(c, username)
	}

	clearSessionCookie(c)
	httputil.Success(c, LogoutResponse{LoggedOut: true})
}

// sessionIdentity resolves the account name and token for the audit record. The
// session is the only authority: it never defaults to a hardcoded admin, and a
// missing or expired session leaves the actor anonymous.
func (h *Handler) sessionIdentity(c *gin.Context) (username, token string) {
	if user, ok := GetCurrentUser(c); ok {
		username = user.Username
	}

	token = GetSessionToken(c)
	if token == "" {
		return username, ""
	}
	if session, ok := h.service.ValidateSession(token); ok && session.Username != "" {
		username = session.Username
	}
	return username, token
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
		return UserResponse{}, errUnauthorized
	}

	user, err := h.service.GetCurrentUser(c.Request.Context(), sessionUser.ID)
	if errors.Is(err, ErrUserNotFound) {
		// The account vanished between the session lookup and this query: drop the
		// dangling session instead of leaving the client half authenticated.
		h.service.Logout(GetSessionToken(c))
		clearSessionCookie(c)
		return UserResponse{}, errUnauthorized
	}
	if err != nil {
		return UserResponse{}, fmt.Errorf("load current user: %w", err)
	}
	return user, nil
}
