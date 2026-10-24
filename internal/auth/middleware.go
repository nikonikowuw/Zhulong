package auth

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

const (
	currentUserKey       = "zhulong.auth.current_user"
	sessionTokenKey      = "zhulong.auth.session_token"
	skipCookieRenewalKey = "zhulong.auth.skip_cookie_renewal"
)

// errUnauthorized is the single definition of the unauthenticated response, shared
// by the middleware and by handlers that reject a dangling session.
var errUnauthorized = apperr.Unauthenticated("UNAUTHORIZED", "Authentication required", nil)

// SessionUser is the identity proven by a valid session cookie. It is deliberately
// narrower than UserResponse: a session carries no authoritative profile data, so a
// handler that needs createdAt must query the store instead of reading a plausible
// but zero value off the Gin context.
type SessionUser struct {
	ID       int64
	Username string
}

// SetCurrentUser stores the authenticated session identity into Gin context.
func SetCurrentUser(c *gin.Context, user SessionUser) {
	c.Set(currentUserKey, user)
}

// GetCurrentUser retrieves the authenticated session identity from Gin context.
func GetCurrentUser(c *gin.Context) (SessionUser, bool) {
	value, ok := c.Get(currentUserKey)
	if !ok {
		return SessionUser{}, false
	}
	user, ok := value.(SessionUser)
	return user, ok
}

// SetSessionToken stores the active token string into Gin context.
func SetSessionToken(c *gin.Context, token string) {
	c.Set(sessionTokenKey, token)
}

// GetSessionToken retrieves the active token from Gin context.
func GetSessionToken(c *gin.Context) string {
	value, ok := c.Get(sessionTokenKey)
	if !ok {
		return ""
	}
	token, _ := value.(string)
	return token
}

func writeUnauthorized(c *gin.Context) {
	httputil.WriteError(c, errUnauthorized)
}

// withoutCookieRenewal marks a route whose handler clears the session cookie
// itself. Without the marker RequireAuth would re-issue the cookie the handler is
// about to delete, answering logout with two conflicting Set-Cookie headers.
// Routing states the intent so the middleware never has to inspect URL paths.
func withoutCookieRenewal() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(skipCookieRenewalKey, true)
		c.Next()
	}
}

func renewsSessionCookie(c *gin.Context) bool {
	value, ok := c.Get(skipCookieRenewalKey)
	if !ok {
		return true
	}
	skipped, _ := value.(bool)
	return !skipped
}

// RequireAuth middleware verifies the session cookie and injects CurrentUser into context.
//
// It also attaches the authenticated actor to the standard request context, so
// downstream audit records resolve the configured account name. The request IP
// set by the audit middleware is preserved: authentication refines the
// identity but never replaces the connection source.
func RequireAuth(service AuthService) gin.HandlerFunc {
	if service == nil {
		// Fail closed: a router wired without an auth service must expose no
		// protected route, not panic on the first request.
		return func(c *gin.Context) {
			writeUnauthorized(c)
			c.Abort()
		}
	}

	maxAge := sessionCookieMaxAge(service)
	return func(c *gin.Context) {
		token, err := c.Cookie(SessionCookieName)
		if err != nil || token == "" {
			writeUnauthorized(c)
			c.Abort()
			return
		}

		sessionItem, ok := service.ValidateSession(token)
		if !ok {
			writeUnauthorized(c)
			c.Abort()
			return
		}

		SetSessionToken(c, token)
		SetCurrentUser(c, SessionUser{
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

		// Valid sessions slide on access, so keep the cookie's browser lifetime in
		// step with the server-side expiry.
		if renewsSessionCookie(c) {
			setSessionCookie(c, token, maxAge)
		}

		c.Next()
	}
}
