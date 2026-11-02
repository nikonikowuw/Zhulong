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
	token, ok := value.(string)
	if !ok {
		return ""
	}
	return token
}

// abortUnauthorized is the single exit for every unauthenticated request: it
// writes the error envelope and stops the chain in one step, so no call site can
// answer 401 and then keep running handlers.
func abortUnauthorized(c *gin.Context) {
	httputil.WriteError(c, errUnauthorized)
	c.Abort()
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
	skipped, ok := value.(bool)
	if !ok {
		return true
	}
	return !skipped
}

// RequireAuth middleware verifies the session cookie, injects CurrentUser into
// the context, attaches the authenticated audit actor and keeps the sliding
// cookie expiry in step with the session.
func RequireAuth(service AuthService) gin.HandlerFunc {
	if service == nil {
		// Fail closed: a router wired without an auth service must expose no
		// protected route, not panic on the first request.
		return abortUnauthorized
	}

	maxAge := sessionCookieMaxAge(service)
	return func(c *gin.Context) {
		token, sessionItem, ok := authenticate(c, service)
		if !ok {
			abortUnauthorized(c)
			return
		}

		SetSessionToken(c, token)
		SetCurrentUser(c, SessionUser{
			ID:       sessionItem.UserID,
			Username: sessionItem.Username,
		})
		injectAuditActor(c, sessionItem)

		// Valid sessions slide on access, so keep the cookie's browser lifetime in
		// step with the server-side expiry.
		if renewsSessionCookie(c) {
			writeSessionCookie(c, token, maxAge)
		}

		c.Next()
	}
}

// authenticate resolves the session cookie into a validated session. A missing
// cookie and an unknown or expired token are reported identically, because the
// client must not learn which of the two it holds.
func authenticate(c *gin.Context, service AuthService) (string, SessionItem, bool) {
	token, err := c.Cookie(SessionCookieName)
	if err != nil || token == "" {
		return "", SessionItem{}, false
	}

	sessionItem, ok := service.ValidateSession(token)
	if !ok {
		return "", SessionItem{}, false
	}
	return token, sessionItem, true
}

// injectAuditActor attaches the authenticated actor to the standard request
// context, so downstream audit records resolve the configured account name. The
// request IP set by the audit middleware is preserved: authentication refines the
// identity but never replaces the connection source.
func injectAuditActor(c *gin.Context, sessionItem SessionItem) {
	ctx := c.Request.Context()

	actor, found := audit.FromContext(ctx)
	if !found {
		actor = audit.Actor{IP: c.ClientIP(), Source: audit.SourceWeb}
	}
	if sessionItem.Username != "" {
		actor.Username = sessionItem.Username
	}
	c.Request = c.Request.WithContext(audit.WithActor(ctx, actor))
}
