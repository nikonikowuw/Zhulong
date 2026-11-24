package alertpush

import (
	"context"
	"io"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/filestore"
)

// Webhook envelope headers mirroring the payload's routing metadata.
const (
	headerZhulongEvent    = "X-Zhulong-Event"
	headerZhulongDelivery = "X-Zhulong-Delivery"
	headerZhulongSchema   = "X-Zhulong-Schema"
)

var reservedHeaders = map[string]struct{}{
	"accept":             {},
	"authorization":      {},
	"connection":         {},
	"content-length":     {},
	"content-type":       {},
	"host":               {},
	"transfer-encoding":  {},
	"user-agent":         {},
	"x-zhulong-event":    {},
	"x-zhulong-delivery": {},
	"x-zhulong-schema":   {},
}

// MediaReader abstracts read access to media files with four-layer path protection.
type MediaReader interface {
	OpenContent(ctx context.Context, relPath string) (io.ReadSeekCloser, filestore.ObjectInfo, error)
}

// MediaPresigner optionally resolves direct presigned URLs (e.g. S3/MinIO)
// when redirect delivery is active for a media key.
type MediaPresigner interface {
	PresignMediaURL(ctx context.Context, relPath string, ttl time.Duration) (string, bool, error)
}

// Sender defines the delivery mechanism for an alert destination.
type Sender interface {
	Send(ctx context.Context, d Delivery) error
	SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error
	Close() error
}

const defaultSendBudget = 10 * time.Second

// timeoutBudgets allows a Sender to declare the context budgets it needs so the
// worker does not silently truncate configured per-request timeouts or retries.
type timeoutBudgets interface {
	EventSendBudget() time.Duration
	HeartbeatSendBudget() time.Duration
}

// DeviceIDProvider supplies the stable business identity of this host. An
// aggregation platform needs it to tell which edge box pushed an event; the hostname
// is not usable because flashed images routinely share it.
//
// The interface is declared on the consumer side so this package never imports
// systeminfo; the host wires the implementation in.
type DeviceIDProvider interface {
	DeviceID() string
}

// HeartbeatSnapshotProvider supplies a point-in-time sanitized host health and
// workload summary without coupling alertpush to sibling domain packages.
type HeartbeatSnapshotProvider interface {
	Snapshot(ctx context.Context) HeartbeatSnapshot
}

// HeartbeatSnapshot holds the sanitized operational telemetry assembled by the host.
type HeartbeatSnapshot struct {
	ReportedAt time.Time
	Status     string
	Reasons    []string
	Host       HeartbeatHostPayload
	Resources  HeartbeatResourcesPayload
	Storage    HeartbeatStoragePayload
	Workload   HeartbeatWorkloadPayload
}

// HeartbeatHostPayload describes host architecture, uptime and optional reachable base URL.
type HeartbeatHostPayload struct {
	Arch          string `json:"arch"`
	UptimeSeconds uint64 `json:"uptime_seconds"`
	PublicBaseURL string `json:"public_base_url,omitempty"`
}

// HeartbeatResourcesPayload describes core compute and thermal watermarks.
type HeartbeatResourcesPayload struct {
	CPUUsagePercent   float64 `json:"cpu_usage_percent"`
	MemoryUsedPercent float64 `json:"memory_used_percent"`
	NPUSupported      bool    `json:"npu_supported"`
	NPULoadPercent    float64 `json:"npu_load_percent"`
	MaxTemperatureC   float64 `json:"max_temperature_c"`
}

// HeartbeatStoragePayload describes media disk health and write-gate state.
type HeartbeatStoragePayload struct {
	Status      string `json:"status"`
	Writable    bool   `json:"writable"`
	UsedPercent int    `json:"used_percent"`
	FreeMB      int64  `json:"free_mb"`
}

// HeartbeatCamerasPayload summarizes enabled camera health counts.
type HeartbeatCamerasPayload struct {
	Enabled  int `json:"enabled"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
	Degraded int `json:"degraded"`
}

// HeartbeatTasksPayload summarizes enabled channel analysis tasks.
type HeartbeatTasksPayload struct {
	Enabled int `json:"enabled"`
}

// HeartbeatRunnersPayload summarizes active and circuit-broken algorithm runner processes.
type HeartbeatRunnersPayload struct {
	Active int `json:"active"`
	Failed int `json:"failed"`
}

// HeartbeatDecodePayload summarizes native video decoder admission state.
type HeartbeatDecodePayload struct {
	Backend           string `json:"backend"`
	HardwareAvailable bool   `json:"hardware_available"`
	ActiveSessions    int    `json:"active_sessions"`
	MaxSessions       int    `json:"max_sessions"`
}

// HeartbeatWorkloadPayload groups camera, task, runner and decoder summaries.
type HeartbeatWorkloadPayload struct {
	Cameras HeartbeatCamerasPayload `json:"cameras"`
	Tasks   HeartbeatTasksPayload   `json:"tasks"`
	Runners HeartbeatRunnersPayload `json:"runners"`
	Decode  HeartbeatDecodePayload  `json:"decode"`
}

// isReservedHeader returns true if the header name is managed internally
// and forbidden in custom headers.
func isReservedHeader(name string) bool {
	_, ok := reservedHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// resolveDeviceID tolerates a nil provider: payloads then omit device_id instead of
// panicking on a partially wired sender.
func resolveDeviceID(provider DeviceIDProvider) string {
	if provider == nil {
		return ""
	}
	return strings.TrimSpace(provider.DeviceID())
}

// resolveHeartbeatSnapshot fetches a live snapshot from provider or constructs a minimal
// ready snapshot when running in isolated unit tests without full host wiring.
func resolveHeartbeatSnapshot(
	ctx context.Context,
	provider HeartbeatSnapshotProvider,
	publicBaseURL string,
) HeartbeatSnapshot {
	if provider != nil {
		snap := provider.Snapshot(ctx)
		if snap.ReportedAt.IsZero() {
			snap.ReportedAt = time.Now().UTC()
		}
		if snap.Status == "" {
			snap.Status = "ready"
		}
		if snap.Reasons == nil {
			snap.Reasons = []string{}
		}
		if snap.Host.PublicBaseURL == "" && strings.TrimSpace(publicBaseURL) != "" {
			snap.Host.PublicBaseURL = strings.TrimSpace(publicBaseURL)
		}
		return snap
	}

	return HeartbeatSnapshot{
		ReportedAt: time.Now().UTC(),
		Status:     "ready",
		Reasons:    []string{},
		Host: HeartbeatHostPayload{
			Arch:          runtime.GOARCH,
			PublicBaseURL: strings.TrimSpace(publicBaseURL),
		},
		Storage: HeartbeatStoragePayload{
			Status:   "healthy",
			Writable: true,
		},
		Workload: HeartbeatWorkloadPayload{
			Decode: HeartbeatDecodePayload{
				Backend: "auto",
			},
		},
	}
}

// sameOrigin reports whether two HTTP(S) URLs share the exact same scheme, hostname
// and effective port. Credentials bound to rawA may only be reused on rawB when true.
func sameOrigin(rawA, rawB string) bool {
	uA, errA := url.Parse(strings.TrimSpace(rawA))
	uB, errB := url.Parse(strings.TrimSpace(rawB))
	if errA != nil || errB != nil {
		return false
	}
	schemeA := strings.ToLower(uA.Scheme)
	schemeB := strings.ToLower(uB.Scheme)
	isHTTPScheme := schemeA == "http" || schemeA == "https"
	if !isHTTPScheme || schemeA != schemeB {
		return false
	}
	hostA := strings.ToLower(uA.Hostname())
	hostB := strings.ToLower(uB.Hostname())
	if hostA == "" || hostA != hostB {
		return false
	}
	return effectivePort(schemeA, uA.Port()) == effectivePort(schemeB, uB.Port())
}

func effectivePort(scheme, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if scheme == "https" {
		return "443"
	}
	return "80"
}

// generateSignedURL generates an absolute signed URL using the public base URL and URLSigner.
func generateSignedURL(
	publicBaseURL string,
	signer *URLSigner,
	relPath string,
	ttl time.Duration,
) (string, error) {
	if signer == nil || relPath == "" || publicBaseURL == "" {
		return "", nil
	}
	exp := time.Now().Add(ttl).Unix()
	sig, err := signer.Sign(relPath, exp)
	if err != nil {
		return "", err
	}

	base := strings.TrimRight(publicBaseURL, "/")
	cleanRel := normalizeRelativePath(relPath)
	u, err := url.Parse(base + "/api/v1/media/signed/" + cleanRel)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", sig)
	u.RawQuery = q.Encode()
	return u.String(), nil
}
