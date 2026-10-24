package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// SessionCookieName is the standard cookie name for the session token.
	SessionCookieName = "zhulong_session"

	sessionCookiePath   = "/"
	sessionCookieDomain = ""
	// sessionCookieSecure is false because Zhulong serves its SPA and API over
	// plain HTTP on the local network, where browsers drop a Secure cookie and
	// login would never stick. A deployment that terminates TLS in front of the
	// host must flip this to true.
	sessionCookieSecure   = false
	sessionCookieHTTPOnly = true
)

// sessionCookieMaxAge derives the browser-visible cookie lifetime from the
// session TTL, so the cookie disappears exactly when an idle session expires.
func sessionCookieMaxAge(service AuthService) int {
	if service == nil {
		return DefaultSessionCookieMaxAge
	}
	ttl := service.SessionTTL()
	if ttl <= 0 {
		return DefaultSessionCookieMaxAge
	}
	return int(ttl / time.Second)
}

func setSessionCookie(c *gin.Context, token string, maxAge int) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		SessionCookieName,
		token,
		maxAge,
		sessionCookiePath,
		sessionCookieDomain,
		sessionCookieSecure,
		sessionCookieHTTPOnly,
	)
}

// clearSessionCookie expires the cookie. SameSite must be set before SetCookie,
// otherwise the deletion header would not match the stored cookie.
func clearSessionCookie(c *gin.Context) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(
		SessionCookieName,
		"",
		-1,
		sessionCookiePath,
		sessionCookieDomain,
		sessionCookieSecure,
		sessionCookieHTTPOnly,
	)
}
