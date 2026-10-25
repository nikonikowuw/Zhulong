package gb28181

import "time"

// Inbound protection defaults. Every value here is a P0 delivery item (see the
// task's implement.md): they bound the work a single peer can create before the
// device-side behaviour has been verified against real hardware.
const (
	defaultMaxSIPMessageBytes = 16 * 1024
	defaultMaxSignallingConns = 64
	defaultSIPRateBurst       = 10
	defaultSIPRateRefill      = time.Second
	minRegistrationSeconds    = 60
	maxRegistrationSeconds    = 86400
)

// ingressLimits configures inbound protection and device timing. Zero values fall
// back to the documented defaults.
type ingressLimits struct {
	MaxSIPMessageBytes int
	MaxXMLBytes        int
	MaxSignallingConns int
	RateBurst          int
	RateRefill         time.Duration
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
		RateBurst:          defaultSIPRateBurst,
		RateRefill:         defaultSIPRateRefill,
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
	if l.RateBurst <= 0 {
		l.RateBurst = defaults.RateBurst
	}
	if l.RateRefill <= 0 {
		l.RateRefill = defaults.RateRefill
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
