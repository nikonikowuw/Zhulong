package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

const (
	// SessionCookieName is the standard cookie name for the session token.
	SessionCookieName = "zhulong_session"

	currentUserKey  = "zhulong.auth.current_user"
	sessionTokenKey = "zhulong.auth.session_token"
)

// SetCurrentUser stores the authenticated user into Gin context.
func SetCurrentUser(c *gin.Context, user UserResponse) {
	c.Set(currentUserKey, user)
}

// GetCurrentUser retrieves the authenticated user from Gin context.
func GetCurrentUser(c *gin.Context) (UserResponse, bool) {
	if val, ok := c.Get(currentUserKey); ok {
		if user, ok := val.(UserResponse); ok {
			return user, true
		}
	}
	return UserResponse{}, false
}

// SetSessionToken stores the active token string into Gin context.
func SetSessionToken(c *gin.Context, token string) {
	c.Set(sessionTokenKey, token)
}

// GetSessionToken retrieves the active token from Gin context.
func GetSessionToken(c *gin.Context) string {
	if val, ok := c.Get(sessionTokenKey); ok {
		if token, ok := val.(string); ok {
			return token
		}
	}
	return ""
}

func writeUnauthorized(c *gin.Context) {
	httputil.WriteError(c, httputil.NewError(http.StatusUnauthorized, "UNAUTHORIZED", "Authentication required", nil))
}

// RequireAuth middleware verifies the session cookie and injects CurrentUser into context.
func RequireAuth(service AuthService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			writeUnauthorized(c)
			return
		}

		sessionItem, ok := service.ValidateSession(token)
		if !ok {
			writeUnauthorized(c)
			return
		}

		SetSessionToken(c, token)
		SetCurrentUser(c, UserResponse{
			ID:       sessionItem.UserID,
			Username: sessionItem.Username,
		})

		c.Next()
	}
}
