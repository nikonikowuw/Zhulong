package gb28181

import "time"

// Inbound protection defaults. Every value here is a P0 delivery item (see the
// task's implement.md): they bound the work a single peer can create before the
// device-side behaviour has been verified against real hardware.
const (
	defaultMaxSIPMessageBytes = 16 * 1024

	// defaultMaxSignallingConns bounds concurrent TCP signalling connections. A
	// device holds one connection for as long as it stays registered, so the cap
	// must cover the documented device budget (100 devices) with headroom for the
	// overlap window of a re-registering device instead of being a small constant:
	// a cap below the device count turns the 65th device into a silent lockout.
	defaultMaxSignallingConns = 256

	// defaultMaxConnsPerSource bounds one peer address. It is deliberately far
	// above "one device, one flow": devices behind the same NAT share a source
	// address, so a per-address cap is a coarse flood guard, not a per-device
	// quota (a device identity is not known at accept time).
	defaultMaxConnsPerSource = 8

	// defaultSignallingIdleTimeout closes a signalling connection that has been
	// silent for this long. Three missed keepalives (3 x 180 s) is a dead link, and
	// without the deadline a device that loses power holds its slot until the
	// kernel gives up on the half-open socket — which, with nothing written to it,
	// can be forever.
	defaultSignallingIdleTimeout = 9 * time.Minute

	defaultSIPRateBurst  = 10
	defaultSIPRateRefill = time.Second

	// Outbound limits bound what the platform can do to one device. A catalog
	// query is a MESSAGE the device must answer with a 200 OK, so an unbounded
	// trigger (an operator script, a retry loop) is a self-inflicted flood that the
	// device absorbs.
	defaultOutboundBurst  = 5
	defaultOutboundRefill = time.Second

	// defaultLogThrottleWindow is how long one source may produce at most one
	// diagnostic line per message. Without it a flood turns into a log flood: the
	// limiter rejects per packet, and one 10 kpps source would otherwise write
	// 10 k lines per second.
	defaultLogThrottleWindow = 5 * time.Second
	defaultLogThrottleKeys   = 1024

	minRegistrationSeconds = 60
	maxRegistrationSeconds = 86400
)

// ingressLimits configures inbound protection and device timing. Zero values fall
// back to the documented defaults.
type ingressLimits struct {
	MaxSIPMessageBytes int
	MaxXMLBytes        int
	MaxSignallingConns int
	MaxConnsPerSource  int
	ConnIdleTimeout    time.Duration
	RateBurst          int
	RateRefill         time.Duration
	OutboundBurst      int
	OutboundRefill     time.Duration
	LogThrottleWindow  time.Duration
	NonceTTL           time.Duration
	KeepaliveTimeout   time.Duration
	RegistrationGrace  time.Duration
	SweepInterval      time.Duration
}

// defaultIngressLimits returns the production defaults.
func defaultIngressLimits() ingressLimits {
	return ingressLimits{
		MaxSIPMessageBytes: defaultMaxSIPMessageBytes,
		MaxXMLBytes:        maxInboundXMLBytes,
		MaxSignallingConns: defaultMaxSignallingConns,
		MaxConnsPerSource:  defaultMaxConnsPerSource,
		ConnIdleTimeout:    defaultSignallingIdleTimeout,
		RateBurst:          defaultSIPRateBurst,
		RateRefill:         defaultSIPRateRefill,
		OutboundBurst:      defaultOutboundBurst,
		OutboundRefill:     defaultOutboundRefill,
		LogThrottleWindow:  defaultLogThrottleWindow,
		NonceTTL:           defaultNonceTTL,
		KeepaliveTimeout:   defaultKeepaliveTimeout,
		RegistrationGrace:  defaultRegistrationGrace,
		SweepInterval:      defaultDeviceSweepInterval,
	}
}

// withDefaults fills the fields the caller left at zero.
//
// MaxXMLBytes is capped rather than only defaulted: a caller may tighten it, never
// raise it, because the value bounds what the parser is allowed to allocate.
func (l ingressLimits) withDefaults() ingressLimits {
	defaults := defaultIngressLimits()
	if l.MaxSIPMessageBytes <= 0 {
		l.MaxSIPMessageBytes = defaults.MaxSIPMessageBytes
	}
	if l.MaxXMLBytes <= 0 || l.MaxXMLBytes > defaults.MaxXMLBytes {
		l.MaxXMLBytes = defaults.MaxXMLBytes
	}
	if l.MaxSignallingConns <= 0 {
		l.MaxSignallingConns = defaults.MaxSignallingConns
	}
	if l.MaxConnsPerSource <= 0 {
		l.MaxConnsPerSource = defaults.MaxConnsPerSource
	}
	if l.ConnIdleTimeout <= 0 {
		l.ConnIdleTimeout = defaults.ConnIdleTimeout
	}
	if l.RateBurst <= 0 {
		l.RateBurst = defaults.RateBurst
	}
	if l.RateRefill <= 0 {
		l.RateRefill = defaults.RateRefill
	}
	if l.OutboundBurst <= 0 {
		l.OutboundBurst = defaults.OutboundBurst
	}
	if l.OutboundRefill <= 0 {
		l.OutboundRefill = defaults.OutboundRefill
	}
	if l.LogThrottleWindow <= 0 {
		l.LogThrottleWindow = defaults.LogThrottleWindow
	}
	if l.NonceTTL <= 0 {
		l.NonceTTL = defaults.NonceTTL
	}
	if l.KeepaliveTimeout <= 0 {
		l.KeepaliveTimeout = defaults.KeepaliveTimeout
	}
	if l.RegistrationGrace <= 0 {
		l.RegistrationGrace = defaults.RegistrationGrace
	}
	if l.SweepInterval <= 0 {
		l.SweepInterval = defaults.SweepInterval
	}
	return l
}
