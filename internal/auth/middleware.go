package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
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
	httputil.WriteError(c, apperr.Unauthenticated("UNAUTHORIZED", "Authentication required", nil))
}

// RequireAuth middleware verifies the session cookie and injects CurrentUser into context.
//
// It also attaches the authenticated actor to the standard request context, so
// downstream audit records resolve the configured account name. The request IP
// set by the audit middleware is preserved: authentication refines the
// identity but never replaces the connection source.
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

		actor, found := audit.FromContext(c.Request.Context())
		if !found {
			actor = audit.Actor{IP: c.ClientIP(), Source: audit.SourceWeb}
		}
		if sessionItem.Username != "" {
			actor.Username = sessionItem.Username
		}
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), actor))

		c.Next()
	}
}
