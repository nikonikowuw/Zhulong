package openapi

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/person"
)

// PersonFaceInfo is one enrolled face as the machine API describes it.
//
// It deliberately omits the stored image path: the on-disk name encodes the internal
// person row id, and this API exposes no endpoint that would let a caller read the
// photo back. Publishing it would leak an internal identifier for no consumer's benefit.
type PersonFaceInfo struct {
	FaceID    string    `json:"faceId"`
	IsPrimary bool      `json:"isPrimary"`
	CreatedAt time.Time `json:"createdAt"`
}

// PersonProvisionResponse describes the complete state of a provisioned person and their enrolled faces.
type PersonProvisionResponse struct {
	PersonID      string           `json:"personId"`
	Name          string           `json:"name"`
	PrimaryFaceID *string          `json:"primaryFaceId"`
	Faces         []PersonFaceInfo `json:"faces"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// FaceUploadResponse represents the result of a single face upload or replacement.
type FaceUploadResponse struct {
	FaceID    string    `json:"faceId"`
	Replaced  bool      `json:"replaced"`
	IsPrimary bool      `json:"isPrimary"`
	CreatedAt time.Time `json:"createdAt"`
}

// FaceClearResponse represents the result of clearing all faces for a person.
type FaceClearResponse struct {
	Cleared int `json:"cleared"`
}

// DeleteResponse represents the standard response for deletion operations.
type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}

// DeviceStatusResponse is the camelCase read-only operational snapshot returned by
// GET /api/v1/openapi/status.
type DeviceStatusResponse struct {
	DeviceID   string              `json:"deviceId"`
	ReportedAt time.Time           `json:"reportedAt"`
	Status     string              `json:"status"`
	Reasons    []string            `json:"reasons"`
	Host       DeviceHostInfo      `json:"host"`
	Resources  DeviceResourcesInfo `json:"resources"`
	Storage    DeviceStorageInfo   `json:"storage"`
	Workload   DeviceWorkloadInfo  `json:"workload"`
}

// DeviceHostInfo describes host architecture, uptime and optional reachable base URL.
type DeviceHostInfo struct {
	Arch          string `json:"arch"`
	UptimeSeconds uint64 `json:"uptimeSeconds"`
	PublicBaseURL string `json:"publicBaseUrl,omitempty"`
}

// DeviceResourcesInfo describes compute utilization and thermal watermarks.
type DeviceResourcesInfo struct {
	CPUUsagePercent   float64 `json:"cpuUsagePercent"`
	MemoryUsedPercent float64 `json:"memoryUsedPercent"`
	NPUSupported      bool    `json:"npuSupported"`
	NPULoadPercent    float64 `json:"npuLoadPercent"`
	MaxTemperatureC   float64 `json:"maxTemperatureC"`
}

// DeviceStorageInfo describes media storage health and write-gate state.
type DeviceStorageInfo struct {
	Status      string `json:"status"`
	Writable    bool   `json:"writable"`
	UsedPercent int    `json:"usedPercent"`
	FreeMB      int64  `json:"freeMb"`
}

// DeviceCamerasSummaryInfo summarizes enabled camera health counts.
type DeviceCamerasSummaryInfo struct {
	Enabled  int `json:"enabled"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
	Degraded int `json:"degraded"`
}

// DeviceTasksSummaryInfo summarizes enabled channel analysis tasks.
type DeviceTasksSummaryInfo struct {
	Enabled int `json:"enabled"`
}

// DeviceRunnersSummaryInfo summarizes active and circuit-broken algorithm runners.
type DeviceRunnersSummaryInfo struct {
	Active int `json:"active"`
	Failed int `json:"failed"`
}

// DeviceDecodeSummaryInfo summarizes native video decoder admission state.
type DeviceDecodeSummaryInfo struct {
	Backend           string `json:"backend"`
	HardwareAvailable bool   `json:"hardwareAvailable"`
	ActiveSessions    int    `json:"activeSessions"`
	MaxSessions       int    `json:"maxSessions"`
}

// DeviceWorkloadInfo groups camera, task, runner and decoder summaries.
type DeviceWorkloadInfo struct {
	Cameras DeviceCamerasSummaryInfo `json:"cameras"`
	Tasks   DeviceTasksSummaryInfo   `json:"tasks"`
	Runners DeviceRunnersSummaryInfo `json:"runners"`
	Decode  DeviceDecodeSummaryInfo  `json:"decode"`
}

// newPersonProvisionResponse projects a persisted person onto the machine API contract.
//
// Timestamps are normalized to UTC so they always serialize with a trailing Z: the
// stored location is whatever the driver hands back, and an offset-bearing timestamp
// would silently break clients parsing the documented RFC3339 UTC form.
func newPersonProvisionResponse(p *person.Person) PersonProvisionResponse {
	personKey := ""
	if p.PersonID != nil {
		personKey = *p.PersonID
	}
	faces := make([]PersonFaceInfo, 0, len(p.Faces))
	for _, f := range p.Faces {
		isPrimary := p.PrimaryFaceID != nil && *p.PrimaryFaceID == f.ID
		faces = append(faces, PersonFaceInfo{
			FaceID:    f.ID,
			IsPrimary: isPrimary,
			CreatedAt: f.CreatedAt.UTC(),
		})
	}
	return PersonProvisionResponse{
		PersonID:      personKey,
		Name:          p.Name,
		PrimaryFaceID: p.PrimaryFaceID,
		Faces:         faces,
		CreatedAt:     p.CreatedAt.UTC(),
		UpdatedAt:     p.UpdatedAt.UTC(),
	}
}
