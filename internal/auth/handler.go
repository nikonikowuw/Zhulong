package auth

import (
	"errors"
	"net/http"
	"reflect"

	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
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
	authGroup.GET("/status", h.statusHandler)
	authGroup.POST("/init", h.initHandler)
	authGroup.POST("/login", h.loginHandler)
	authGroup.POST("/logout", RequireAuth(h.service), h.logoutHandler)
	authGroup.GET("/me", RequireAuth(h.service), h.meHandler)
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, token, maxAge, "/", "", false, true)
}

func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(SessionCookieName, "", -1, "/", "", false, true)
}

func handleBindError(c *gin.Context, err error) {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		httputil.WritePayloadTooLarge(c)
		return
	}

	var validationErrors validator.ValidationErrors
	if errors.As(err, &validationErrors) {
		details := make([]httputil.FieldDetail, 0, len(validationErrors))
		for _, fieldError := range validationErrors {
			details = append(details, httputil.FieldDetail{
				Field: jsonFieldPath(fieldError.StructField()),
				Code:  validationCode(fieldError),
			})
		}
		httputil.WriteError(c, httputil.NewValidationError("Validation failed", details))
		return
	}
	httputil.WriteError(c, httputil.NewError(http.StatusBadRequest, "INVALID_FORMAT", "Invalid request format", err))
}

func jsonFieldPath(fieldName string) string {
	switch fieldName {
	case "Username":
		return "username"
	case "Password":
		return "password"
	case "ConfirmPassword":
		return "confirmPassword"
	default:
		return fieldName
	}
}

func validationCode(fieldError validator.FieldError) string {
	if fieldError.StructField() == "Password" && fieldError.Tag() == "min" {
		return "PASSWORD_TOO_SHORT"
	}
	switch fieldError.Tag() {
	case "required":
		return "REQUIRED"
	case "min":
		if fieldError.Kind() == reflect.String {
			return "MIN_LENGTH"
		}
		return "MIN_VALUE"
	case "max":
		if fieldError.Kind() == reflect.String {
			return "MAX_LENGTH"
		}
		return "MAX_VALUE"
	default:
		return "INVALID_VALUE"
	}
}

// statusHandler godoc
// @Summary      Get system initialization status
// @Tags         auth
// @Produce      json
// @Success      200  {object}  httputil.Response{data=AuthStatusResponse}
// @Router       /auth/status [get]
func (h *Handler) statusHandler(c *gin.Context) {
	status, err := h.service.GetStatus(c.Request.Context())
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve status", err))
		return
	}
	httputil.Success(c, status)
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
func (h *Handler) initHandler(c *gin.Context) {
	var req InitAdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleBindError(c, err)
		return
	}

	user, token, err := h.service.InitAdmin(c.Request.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyInitialized):
			httputil.WriteError(c, httputil.NewError(http.StatusForbidden, "SYSTEM_ALREADY_INITIALIZED", "System is already initialized", err))
		case errors.Is(err, ErrPasswordMismatch):
			httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "PASSWORD_MISMATCH", "Passwords do not match", err))
		case errors.Is(err, ErrPasswordTooShort):
			httputil.WriteError(c, httputil.NewError(http.StatusUnprocessableEntity, "PASSWORD_TOO_SHORT", "Password is too short", err))
		default:
			httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to initialize admin", err))
		}
		return
	}

	setSessionCookie(c, token, 7*24*3600)
	httputil.Success(c, user)
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
func (h *Handler) loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		handleBindError(c, err)
		return
	}

	user, token, err := h.service.Login(c.Request.Context(), c.ClientIP(), req)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooManyAttempts):
			httputil.WriteError(c, httputil.NewError(http.StatusTooManyRequests, "TOO_MANY_ATTEMPTS", "Too many attempts", err))
		case errors.Is(err, ErrNotInitialized):
			httputil.WriteError(c, httputil.NewError(http.StatusPreconditionFailed, "SYSTEM_NOT_INITIALIZED", "System is not initialized", err))
		case errors.Is(err, ErrInvalidCredentials):
			httputil.WriteError(c, httputil.NewError(http.StatusUnauthorized, "INVALID_CREDENTIALS", "Invalid credentials", err))
		default:
			httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Login failed", err))
		}
		return
	}

	setSessionCookie(c, token, 7*24*3600)
	httputil.Success(c, user)
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
func (h *Handler) meHandler(c *gin.Context) {
	sessionUser, ok := GetCurrentUser(c)
	if !ok {
		writeUnauthorized(c)
		return
	}
	user, err := h.service.GetCurrentUser(c.Request.Context(), sessionUser.ID)
	if errors.Is(err, ErrUserNotFound) {
		h.service.Logout(GetSessionToken(c))
		clearSessionCookie(c)
		writeUnauthorized(c)
		return
	}
	if err != nil {
		httputil.WriteError(c, httputil.NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to retrieve current user", err))
		return
	}
	httputil.Success(c, user)
}
