package camera

import (
	"sync"
	"time"
)

const (
	EvidenceNone           = "none"
	EvidenceRTSPDescribe   = "rtsp_describe"
	EvidencePacketActivity = "packet_evidence"
	EvidenceError          = "error"

	DefaultStaleThreshold      = 240 * time.Second
	DefaultFailureOfflineCount = 3
	DefaultActivePacketTimeout = 4 * time.Second
)

// StreamStateInfo tracks the runtime health and session state of an individual stream.
type StreamStateInfo struct {
	Role                string     `json:"role"`
	Health              string     `json:"health"`
	Session             string     `json:"session"`
	EvidenceType        string     `json:"evidenceType"`
	Reason              string     `json:"reason,omitempty"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LastCheckedAt       *time.Time `json:"lastCheckedAt,omitempty"`
	LastSuccessAt       *time.Time `json:"lastSuccessAt,omitempty"`
	ErrorMessage        string     `json:"errorMessage,omitempty"`
}

// CameraStateInfo represents the full runtime status of a camera including aggregated state and per-stream details.
type CameraStateInfo struct {
	CameraID      string                      `json:"cameraId"`
	Enabled       bool                        `json:"enabled"`
	Revision      int64                       `json:"revision"`
	Health        string                      `json:"health"`
	Session       string                      `json:"session"`
	Degraded      bool                        `json:"degraded"`
	Stale         bool                        `json:"stale"`
	Reason        string                      `json:"reason,omitempty"`
	LastCheckedAt *time.Time                  `json:"lastCheckedAt,omitempty"`
	LastSuccessAt *time.Time                  `json:"lastSuccessAt,omitempty"`
	Streams       map[string]*StreamStateInfo `json:"streams"`
}

// StateRegistry provides a thread-safe in-memory cache and state manager for camera runtime states.
type StateRegistry struct {
	mu     sync.RWMutex
	states map[string]*CameraStateInfo
}

// NewStateRegistry constructs an empty StateRegistry.
func NewStateRegistry() *StateRegistry {
	return &StateRegistry{
		states: make(map[string]*CameraStateInfo),
	}
}

// InitCameraState initializes or resets a camera's state in the registry.
func (r *StateRegistry) InitCameraState(camera *Camera) *CameraStateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	streamsMap := make(map[string]*StreamStateInfo)
	for _, stream := range camera.Streams {
		streamsMap[stream.Role] = &StreamStateInfo{
			Role:         stream.Role,
			Health:       HealthStateUnknown,
			Session:      SessionStateIdle,
			EvidenceType: EvidenceNone,
			Reason:       "pending_check",
		}
	}

	state := &CameraStateInfo{
		CameraID: camera.ID,
		Enabled:  camera.Enabled,
		Revision: camera.Revision,
		Health:   HealthStateUnknown,
		Session:  SessionStateIdle,
		Degraded: false,
		Stale:    false,
		Reason:   "pending_check",
		Streams:  streamsMap,
	}

	if !camera.Enabled {
		state.Reason = "disabled"
		for _, s := range streamsMap {
			s.Reason = "disabled"
		}
	}

	r.states[camera.ID] = state
	return r.cloneState(state)
}

// GetState returns a snapshot of the camera state.
func (r *StateRegistry) GetState(cameraID string) (*CameraStateInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	state, ok := r.states[cameraID]
	if !ok {
		return nil, false
	}
	return r.cloneState(state), true
}

// GetAllStates returns snapshots of all registered cameras.
func (r *StateRegistry) GetAllStates() map[string]*CameraStateInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]*CameraStateInfo, len(r.states))
	for id, s := range r.states {
		result[id] = r.cloneState(s)
	}
	return result
}

// DeleteState removes a camera's state from the registry.
func (r *StateRegistry) DeleteState(cameraID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.states, cameraID)
}

func (r *StateRegistry) getOrCreateStreamLocked(state *CameraStateInfo, role string) *StreamStateInfo {
	stream, ok := state.Streams[role]
	if !ok {
		stream = &StreamStateInfo{Role: role}
		state.Streams[role] = stream
	}
	return stream
}

// RecordStreamSuccess records a successful health check or packet arrival for a stream and updates the aggregated camera state.
// If expectedRevision > 0 and the camera's current revision does not match, the stale probe result is ignored.
func (r *StateRegistry) RecordStreamSuccess(cameraID string, expectedRevision int64, role, evidenceType string) *CameraStateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.states[cameraID]
	if !ok {
		return nil
	}
	if expectedRevision > 0 && state.Revision != expectedRevision {
		return r.cloneState(state)
	}

	now := time.Now().UTC()
	stream := r.getOrCreateStreamLocked(state, role)

	stream.Health = HealthStateOnline
	stream.EvidenceType = evidenceType
	stream.Reason = ""
	stream.ErrorMessage = ""
	stream.ConsecutiveFailures = 0
	stream.LastCheckedAt = &now
	stream.LastSuccessAt = &now

	r.aggregateCameraStateLocked(state, now)
	return r.cloneState(state)
}

// RecordStreamFailure records a check or media failure for a stream.
// If expectedRevision > 0 and the camera's current revision does not match on non-media checks, the stale result is ignored.
func (r *StateRegistry) RecordStreamFailure(cameraID string, expectedRevision int64, role, reason, errMsg string, isMediaSession bool) *CameraStateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.states[cameraID]
	if !ok {
		return nil
	}
	if !isMediaSession && expectedRevision > 0 && state.Revision != expectedRevision {
		return r.cloneState(state)
	}

	now := time.Now().UTC()
	stream := r.getOrCreateStreamLocked(state, role)

	stream.ConsecutiveFailures++
	stream.LastCheckedAt = &now
	stream.Reason = reason
	stream.ErrorMessage = errMsg

	if isMediaSession {
		stream.Session = SessionStateReconnecting
		stream.Health = HealthStateError
		stream.EvidenceType = EvidenceError
	} else {
		// Control plane / Describe probe failure
		if stream.LastSuccessAt == nil || stream.ConsecutiveFailures >= DefaultFailureOfflineCount {
			stream.Health = HealthStateOffline
		} else {
			// Transient failure
			stream.Health = HealthStateError
		}
		stream.EvidenceType = EvidenceError
	}

	r.aggregateCameraStateLocked(state, now)
	return r.cloneState(state)
}

// UpdateSessionState updates the session dimension (e.g. idle -> starting -> running -> reconnecting).
func (r *StateRegistry) UpdateSessionState(cameraID, role, sessionState string) *CameraStateInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	state, ok := r.states[cameraID]
	if !ok {
		return nil
	}

	stream := r.getOrCreateStreamLocked(state, role)
	stream.Session = sessionState

	now := time.Now().UTC()
	r.aggregateCameraStateLocked(state, now)
	return r.cloneState(state)
}

func (r *StateRegistry) aggregateCameraStateLocked(state *CameraStateInfo, now time.Time) {
	if !state.Enabled {
		state.Health = HealthStateUnknown
		state.Session = SessionStateIdle
		state.Degraded = false
		state.Reason = "disabled"
		return
	}

	mainStream, hasMain := state.Streams[StreamRoleMain]
	subStream, hasSub := state.Streams[StreamRoleSub]

	if !hasMain {
		state.Health = HealthStateUnknown
		state.Session = SessionStateIdle
		state.Reason = "no_streams"
		return
	}

	state.LastCheckedAt = mainStream.LastCheckedAt
	state.LastSuccessAt = mainStream.LastSuccessAt

	// Check for staleness on idle camera
	if mainStream.Session == SessionStateIdle && mainStream.LastSuccessAt != nil {
		if now.Sub(*mainStream.LastSuccessAt) > DefaultStaleThreshold {
			state.Stale = true
			state.Health = HealthStateUnknown
			state.Reason = "stale_check"
			return
		}
	}
	state.Stale = false

	// Primary health is governed by main stream
	state.Health = mainStream.Health
	state.Session = mainStream.Session
	state.Reason = mainStream.Reason

	// Substream status influences degraded flag
	state.Degraded = hasSub && mainStream.Health == HealthStateOnline &&
		(subStream.Health == HealthStateOffline || subStream.Health == HealthStateError)
}

func (r *StateRegistry) cloneState(s *CameraStateInfo) *CameraStateInfo {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Streams = make(map[string]*StreamStateInfo, len(s.Streams))
	for k, v := range s.Streams {
		vClone := *v
		clone.Streams[k] = &vClone
	}
	return &clone
}
