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

	// sessionCookieDeleteMaxAge is the MaxAge that tells the browser to drop the
	// cookie immediately instead of storing it.
	sessionCookieDeleteMaxAge = -1
)

// sessionCookieMaxAge derives the browser-visible cookie lifetime from the
// session TTL, so the cookie disappears exactly when an idle session expires.
func sessionCookieMaxAge(service AuthService) int {
	if service == nil {
		return defaultSessionCookieMaxAge
	}
	ttl := service.SessionTTL()
	if ttl <= 0 {
		return defaultSessionCookieMaxAge
	}
	seconds := int(ttl / time.Second)
	if seconds == 0 {
		// Clamp sub-second TTLs to 1s so net/http does not omit Max-Age and turn
		// the cookie into a browser-session cookie.
		return 1
	}
	return seconds
}

// writeSessionCookie is the single place that knows the cookie's attribute set,
// so set and clear can never disagree on Path, Domain or SameSite.
func writeSessionCookie(c *gin.Context, token string, maxAge int) {
	// SameSite must be set before SetCookie, otherwise the header would not match
	// the stored cookie.
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

// clearSessionCookie expires the cookie so the browser drops it immediately.
func clearSessionCookie(c *gin.Context) {
	writeSessionCookie(c, "", sessionCookieDeleteMaxAge)
}
