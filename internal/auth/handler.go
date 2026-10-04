package auth

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
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
func (h *Handler) RegisterRoutes(rg *gin.RouterGroup) {
	authGroup := rg.Group("/auth")
	authGroup.GET("/status", httputil.Handle(h.statusHandler))
	authGroup.POST("/init", httputil.HandleJSON(h.initHandler))
	authGroup.POST("/login", httputil.HandleJSON(h.loginHandler))
	authGroup.POST("/logout", RequireAuth(h.service), h.logoutHandler)
	authGroup.GET("/me", RequireAuth(h.service), httputil.Handle(h.meHandler))
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, token, maxAge, "/", "", false, true)
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
	user, token, err := h.service.InitAdmin(c.Request.Context(), req)
	if err != nil {
		return UserResponse{}, err
	}
	setSessionCookie(c, token, 7*24*3600)
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
	user, token, err := h.service.Login(c.Request.Context(), c.ClientIP(), req)
	if err != nil {
		return UserResponse{}, err
	}
	setSessionCookie(c, token, 7*24*3600)
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
	h.service.Logout(GetSessionToken(c))
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
