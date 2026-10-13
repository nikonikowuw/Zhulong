package audithttp

import (
	"context"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// scopeKey holds the per-request audit scope inside the Gin context.
const scopeKey = "zhulong.audit.scope"

// scope is the mutable audit intent for a single request. It is created by
// Middleware, optionally amended by the handler, and consumed once the request
// finishes.
//
// A request without a declared action is not audited at all: read-only
// endpoints and health checks pass through without producing records.
type scope struct {
	mu sync.Mutex

	declared bool
	action   string
	target   string
	detail   string

	// domainFailed records an explicit domain-level failure on an otherwise
	// successful HTTP response, such as an NTP sync that returns 200 with a
	// failed sync state.
	domainFailed  bool
	domainCode    string
	domainMessage string
}

// middlewareAudit records the resolved outcome of a request that declared an
// audit action.
type middlewareAudit struct {
	service *audit.Service
	actor   audit.Actor
}

// Middleware returns the declarative audit middleware. It initializes the
// request actor from the connection source, attaches it to the standard request
// context, and writes a record when the request declared an audited action.
//
// It must be mounted after the body-limit middleware (so a request rejected
// before the handler runs is not audited) and before authentication (so
// unauthenticated init/login attempts are covered).
func Middleware(service *audit.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		// The IP always comes from the TCP connection source. Forwarding
		// headers are ignored because this deployment has no reverse proxy.
		actor := audit.Actor{
			IP:       c.ClientIP(),
			Username: audit.UsernameAnonymous,
			Source:   audit.SourceWeb,
		}
		c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), actor))

		state := &scope{}
		c.Set(scopeKey, state)

		c.Next()

		entry, ok := state.resolve(c)
		if !ok || service == nil {
			return
		}
		// The handler may have upgraded the actor, so read it back from the
		// request context rather than using the initial value.
		finalActor := actor
		if updated, found := audit.FromContext(c.Request.Context()); found {
			finalActor = updated
		}
		if finalActor.Username == "" {
			finalActor.Username = audit.UsernameUnknown
		}

		service.Record(audit.Entry{
			IP:       finalActor.IP,
			Username: finalActor.Username,
			Action:   entry.action,
			Target:   entry.target,
			Detail:   entry.detail,
			Status:   entry.status,
			ErrorMsg: entry.errorMsg,
		})
	}
}

// resolvedEntry is the outcome derived from the request scope and response.
type resolvedEntry struct {
	action   string
	target   string
	detail   string
	status   string
	errorMsg string
}

// resolve converts the scope plus the finished response into a record, or
// reports false when the request never declared an audited action.
//
// Failure is detected from three sources, in priority order:
//  1. an explicit domain failure declared with TagError, which covers 2xx
//     responses that failed at the business level;
//  2. a failing HTTP status code;
//  3. otherwise success.
//
// Only sanitized error information is persisted: the code and outward message
// captured by httputil.WriteError. Raw internal errors and Gin's error list are
// never written.
func (s *scope) resolve(c *gin.Context) (resolvedEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.declared {
		return resolvedEntry{}, false
	}

	entry := resolvedEntry{
		action: s.action,
		target: s.target,
		detail: s.detail,
		status: audit.StatusSuccess,
	}

	if s.domainFailed {
		entry.status = audit.StatusFailed
		entry.errorMsg = safeErrorSummary(s.domainCode, s.domainMessage)
		return entry, true
	}

	status := c.Writer.Status()
	if !httpStatusFailure(status) {
		return entry, true
	}

	entry.status = audit.StatusFailed
	if safe, found := httputil.SafeErrorFromContext(c); found {
		entry.errorMsg = safeErrorSummary(safe.Code, safe.Message)
	} else {
		// No sanitized metadata is available, so record a generic summary
		// instead of leaking whatever raw error produced the status.
		entry.errorMsg = "REQUEST_FAILED"
	}
	return entry, true
}

// safeErrorSummary builds the persisted error summary from already-sanitized
// parts, preferring the stable code and appending the outward message when it
// adds information.
func safeErrorSummary(code, message string) string {
	switch {
	case code == "" && message == "":
		return "REQUEST_FAILED"
	case code == "":
		return message
	case message == "" || message == code:
		return code
	default:
		return code + ": " + message
	}
}

// scopeFrom returns the request scope, if the audit middleware is mounted.
func scopeFrom(c *gin.Context) (*scope, bool) {
	value, exists := c.Get(scopeKey)
	if !exists {
		return nil, false
	}
	state, ok := value.(*scope)
	return state, ok
}

// Tag declares the audited action for the current request. Handlers call it
// before binding or side effects so that validation and business failures still
// produce a record.
func Tag(c *gin.Context, action, target, detail string) {
	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.declared = true
	state.action = action
	state.target = target
	state.detail = detail
}

// TagMiddleware declares the audited action as middleware, so it can run before
// the JSON body is bound. This is what makes a bind failure auditable.
func TagMiddleware(action, target, detail string) gin.HandlerFunc {
	return func(c *gin.Context) {
		Tag(c, action, target, detail)
		c.Next()
	}
}

// TagAction replaces the action declared earlier. It exists for endpoints that
// share one route but distinguish operations by the request body, such as
// camera update versus toggle.
func TagAction(c *gin.Context, action string) {
	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.declared = true
	state.action = action
}

// TagTarget sets or replaces the audit target, typically once the business
// result produced an identifier.
func TagTarget(c *gin.Context, target string) {
	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.target = target
}

// TagDetail sets or replaces the audit detail. Callers must pass an explicitly
// whitelisted, non-sensitive summary; the audit core does not inspect it.
func TagDetail(c *gin.Context, detail string) {
	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.detail = detail
}

// TagActor records the authenticated account name once login or initialization
// succeeds, while preserving the request IP as the primary attribution field.
// It updates the standard request context so that later audit resolution
// observes the same actor.
func TagActor(c *gin.Context, username string) {
	actor, ok := audit.FromContext(c.Request.Context())
	if !ok {
		actor = audit.Actor{IP: c.ClientIP(), Source: audit.SourceWeb}
	}
	if username != "" {
		actor.Username = username
	}
	c.Request = c.Request.WithContext(audit.WithActor(c.Request.Context(), actor))
}

// TagError marks the request as a domain-level failure even when the HTTP
// response is successful, for example an NTP sync that returns 200 while its
// reported state is failed. Both arguments must already be safe to persist.
func TagError(c *gin.Context, code, safeMessage string) {
	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	state.declared = true
	state.domainFailed = true
	state.domainCode = code
	state.domainMessage = safeMessage
}

// requestContext returns the standard context used for audit lookups.
func requestContext(c *gin.Context) context.Context {
	if c == nil || c.Request == nil {
		return context.Background()
	}
	return c.Request.Context()
}

// httpStatusFailure reports whether a status code represents a failure.
func httpStatusFailure(status int) bool {
	return status >= http.StatusBadRequest
}
