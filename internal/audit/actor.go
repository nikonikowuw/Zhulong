package audit

import "context"

// Actor describes who initiated an audited operation.
//
// The system has exactly one configurable login account, so Username is only
// auxiliary: it records the configured account name for diagnostics. The
// primary attribution field for human HTTP operations is IP, taken from the
// TCP connection source. IP is a network-origin clue, not a personal identity;
// NAT, dynamic addressing, or a shared proxy can make several users share one IP.
type Actor struct {
	// IP is the connection source address. Empty means "no client origin",
	// which is expected for system events and must not be faked as 127.0.0.1.
	IP string
	// Username is the configured account name when authenticated. It never
	// substitutes for IP and must not be hardcoded to "admin".
	Username string
	// Source is request-scoped context (for example "web" or "system").
	// It is not persisted and does not appear in the API DTO.
	Source string
}

// Identity values used when no authenticated human is attributable.
const (
	// UsernameAnonymous marks a request that reached the HTTP layer without
	// completing authentication, such as a failed login attempt.
	UsernameAnonymous = "anonymous"
	// UsernameUnknown marks a generic event recorded without an Actor. It must
	// never be used to imply a system event.
	UsernameUnknown = "unknown"
	// UsernameSystem marks an explicit system event recorded via RecordSystem.
	UsernameSystem = "system"
	// SourceWeb marks an HTTP-originated actor.
	SourceWeb = "web"
	// SourceSystem marks an explicitly recorded system event.
	SourceSystem = "system"
)

type actorContextKey struct{}

// WithActor returns a copy of ctx carrying the given Actor.
func WithActor(ctx context.Context, actor Actor) context.Context {
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// FromContext extracts the Actor stored by WithActor. The boolean is false when
// no Actor was attached, so callers can distinguish "unknown" from a real actor.
func FromContext(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok {
		return Actor{}, false
	}
	return actor, true
}
