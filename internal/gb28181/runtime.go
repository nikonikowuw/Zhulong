package gb28181

import (
	"sync"
	"time"
)

// Runtime detail codes. The backend stores a stable machine code (not localized
// text) so the UI owns the wording and the API does not ship single-language
// strings; unknown codes are rendered verbatim, which keeps forward compatibility.
const (
	DetailConfigUnreadable         = "config_unreadable"
	DetailMediaAddressUnresolvable = "media_address_unresolvable"
	DetailConfigInvalid            = "config_invalid"
	DetailIngressUnavailable       = "ingress_unavailable"
	DetailIngressStartFailed       = "ingress_start_failed"
	DetailIngressStopFailed        = "ingress_stop_failed"
	// DetailIngressServeFailed reports that a bound listener stopped serving after
	// the ingress had started. It is distinct from a bind failure: the socket existed
	// and then died, which is what an operator needs to know before trusting the state.
	DetailIngressServeFailed = "ingress_serve_failed"
)

// RuntimeStatus is what GET /gb28181/config reports about the *current process*.
// Desired and Startup are both returned so the UI can show "saved, restart
// required" instead of pretending the running listener already changed.
type RuntimeStatus struct {
	Desired         Config
	Startup         Config
	AdvertisedIP    string
	State           string
	Detail          string
	RestartRequired bool
	Counters        Counters
}

// Counters is the observable surface for the runtime snapshot. P0 fills the device,
// catalog and port-pool parts; session/media counters stay zero until S3/S4 wire the
// media path, which keeps the API shape stable across the phase boundary.
type Counters struct {
	DevicesTotal   int64
	DevicesOnline  int64
	SessionsActive int64
	PortPairsTotal int
	PortPairsUsed  int64
	PortExhausted  uint64
	Catalog        CatalogCounts
}

// CatalogCounts is the catalog-sync side of the snapshot. Without it an operator
// cannot tell "the device never answered" from "we never asked", which are two very
// different field problems.
type CatalogCounts struct {
	Attempts     int64      `json:"attempts"`
	Failures     int64      `json:"failures"`
	Timeouts     int64      `json:"timeouts"`
	LastChannels int64      `json:"lastChannels"`
	LastSyncAt   *time.Time `json:"lastSyncAt"`
}

// Snapshot is the JSON-facing shape served by GET /gb28181/runtime.
type Snapshot struct {
	State           string         `json:"state"`
	RestartRequired bool           `json:"restartRequired"`
	Devices         DeviceCounters `json:"devices"`
	Catalog         CatalogCounts  `json:"catalog"`
	Sessions        SessionCounts  `json:"sessions"`
	PortPool        PortPoolCounts `json:"portPool"`
	GeneratedAt     time.Time      `json:"generatedAt"`
}

// DeviceCounters mirrors the allowlist side of the snapshot.
type DeviceCounters struct {
	Total  int64 `json:"total"`
	Online int64 `json:"online"`
}

// SessionCounts stays zero in S0; the fields exist so dashboards do not need a
// schema change when playback lands.
type SessionCounts struct {
	Active int64 `json:"active"`
}

// PortPoolCounts exposes the hard constraint that decides how many sessions can run.
type PortPoolCounts struct {
	PairsTotal    int    `json:"pairsTotal"`
	PairsUsed     int64  `json:"pairsUsed"`
	ExhaustedHits uint64 `json:"exhaustedHits"`
}

// Runtime holds the process-local view of the GB28181 subsystem. It is safe for
// concurrent use because HTTP handlers read it while the lifecycle writes it.
type Runtime struct {
	mu sync.RWMutex

	desired     Config
	startup     Config
	advertised  string
	startupSet  bool
	state       string
	detail      string
	activePairs int64
	exhausted   uint64
	devices     DeviceCounters
	catalog     CatalogCounts
}

// NewRuntime creates the runtime in the disabled state with default values, so a
// read before the lifecycle runs still returns a coherent object.
func NewRuntime() *Runtime {
	def := DefaultConfig()
	return &Runtime{
		desired: def,
		startup: def,
		state:   RuntimeStateDisabled,
	}
}

// LoadStartup records the configuration this process actually read and applied.
// It also seeds Desired: until an operator saves something, both views are equal
// and restartRequired is false.
func (r *Runtime) LoadStartup(cfg Config, advertisedIP string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.startup = cfg
	r.desired = cfg
	r.advertised = advertisedIP
	r.startupSet = true
}

// SetDesired records a newly persisted configuration without touching the running
// listener. This is the whole point of the restart-required contract.
func (r *Runtime) SetDesired(cfg Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.desired = cfg
}

// SetState records the current process state and an operator-facing (already
// sanitized) detail string.
func (r *Runtime) SetState(state, detail string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = state
	r.detail = detail
}

// SetPortPoolUsage feeds the snapshot. Callers update it on lease/release.
func (r *Runtime) SetPortPoolUsage(used int64, exhausted uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activePairs = used
	r.exhausted = exhausted
}

// SetDeviceCounters feeds the snapshot from the store.
func (r *Runtime) SetDeviceCounters(total, online int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.devices = DeviceCounters{Total: total, Online: online}
}

// RecordCatalogAttempt counts one operator-triggered catalog query.
func (r *Runtime) RecordCatalogAttempt() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.catalog.Attempts++
}

// RecordCatalogSuccess records a completed sync and how many channels it imported.
func (r *Runtime) RecordCatalogSuccess(channels int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now().UTC()
	r.catalog.LastChannels = int64(channels)
	r.catalog.LastSyncAt = &now
}

// RecordCatalogFailure records a sync that produced no catalog. timeout separates
// "the device stayed silent" from "the exchange itself failed", which is the first
// thing to check on site.
func (r *Runtime) RecordCatalogFailure(timeout bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.catalog.Failures++
	if timeout {
		r.catalog.Timeouts++
	}
}

// Status returns a consistent copy for the config endpoint.
func (r *Runtime) Status() RuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusLocked(r.desired)
}

// StatusAgainst answers the same question for a caller-supplied desired
// configuration. The repository stays the source of truth for what is persisted:
// reading the row back and comparing it here means an out-of-band database change
// is reported as "restart required" instead of being masked by a stale cache.
func (r *Runtime) StatusAgainst(desired Config) RuntimeStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.statusLocked(desired)
}

func (r *Runtime) statusLocked(desired Config) RuntimeStatus {
	return RuntimeStatus{
		Desired:         desired,
		Startup:         r.startup,
		AdvertisedIP:    r.advertised,
		State:           r.state,
		Detail:          r.detail,
		RestartRequired: r.startupSet && !desired.Equal(r.startup),
		Counters: Counters{
			DevicesTotal:   r.devices.Total,
			DevicesOnline:  r.devices.Online,
			SessionsActive: 0,
			PortPairsTotal: r.startup.MediaCapacity(),
			PortPairsUsed:  r.activePairs,
			PortExhausted:  r.exhausted,
			Catalog:        r.catalog,
		},
	}
}

// Snapshot returns the runtime snapshot served to operators. It is derived from
// Status so the two endpoints cannot disagree.
func (r *Runtime) Snapshot() Snapshot {
	status := r.Status()
	return Snapshot{
		State:           status.State,
		RestartRequired: status.RestartRequired,
		Devices: DeviceCounters{
			Total:  status.Counters.DevicesTotal,
			Online: status.Counters.DevicesOnline,
		},
		Catalog:  status.Counters.Catalog,
		Sessions: SessionCounts{Active: status.Counters.SessionsActive},
		PortPool: PortPoolCounts{
			PairsTotal:    status.Counters.PortPairsTotal,
			PairsUsed:     status.Counters.PortPairsUsed,
			ExhaustedHits: status.Counters.PortExhausted,
		},
		GeneratedAt: time.Now().UTC(),
	}
}

// Desired returns the currently persisted configuration.
func (r *Runtime) Desired() Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.desired
}
