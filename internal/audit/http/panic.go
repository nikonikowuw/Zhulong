package audithttp

import (
	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// panicObserver turns a recovered handler panic into a failed audit record.
//
// A panicking handler never advances gin's handler index, so the audit
// middleware's continuation after c.Next() is skipped and a deferred read would
// observe the default 200 status. Recovery notifies this observer after the
// sanitized 500 response is written, which is the only reliable point to
// capture the failure.
type panicObserver struct {
	service *audit.Service
}

// NewPanicObserver returns a httpmiddleware.PanicObserver that records a failed
// audit entry for any request that declared an audited action before panicking.
//
// The returned value satisfies httpmiddleware.PanicObserver without this
// package importing the middleware package, keeping the dependency direction
// one-way from the application wiring.
func NewPanicObserver(service *audit.Service) *panicObserver {
	return &panicObserver{service: service}
}

// OnPanic records the failed audit entry. It must not panic and must not write
// to the response, both of which are already handled by Recovery.
func (o *panicObserver) OnPanic(c *gin.Context) {
	if o == nil || o.service == nil || c == nil {
		return
	}

	state, ok := scopeFrom(c)
	if !ok {
		return
	}
	entry, ok := state.resolve(c)
	if !ok {
		return
	}

	// A panic is always a failure regardless of the status recorded so far.
	entry.status = audit.StatusFailed
	if safe, found := httputil.SafeErrorFromContext(c); found {
		entry.errorMsg = safeErrorSummary(safe.Code, safe.Message)
	} else {
		entry.errorMsg = "INTERNAL_ERROR"
	}

	actor := audit.Actor{Username: audit.UsernameUnknown}
	if found, present := audit.FromContext(requestContext(c)); present {
		actor = found
	}
	if actor.Username == "" {
		actor.Username = audit.UsernameUnknown
	}

	o.service.Record(audit.Entry{
		IP:       actor.IP,
		Username: actor.Username,
		Action:   entry.action,
		Target:   entry.target,
		Detail:   entry.detail,
		Status:   entry.status,
		ErrorMsg: entry.errorMsg,
	})
}
