package gb28181

import "time"

// ConfigInfo is the API projection of the singleton configuration.
//
// It is a whitelist, not a mirror: a field reaches the API only when it is added
// here, so adding a column to the persisted model cannot silently expose it. The
// delta against Config is deliberate — the row ID stays private (the row is a
// singleton and exposing "1" invites clients to treat it as a collection) and the
// audit timestamp is not part of the target state.
type ConfigInfo struct {
	Enabled                 bool   `json:"enabled"`
	SIPID                   string `json:"sipId"`
	SIPRealm                string `json:"sipRealm"`
	SIPListen               string `json:"sipListen"`
	SIPTransport            string `json:"sipTransport"`
	SecurityProfile         string `json:"securityProfile"`
	MediaBindAddress        string `json:"mediaBindAddress"`
	MediaAdvertisedIP       string `json:"mediaAdvertisedIp"`
	RTPPortMin              int    `json:"rtpPortMin"`
	RTPPortMax              int    `json:"rtpPortMax"`
	RequireRegisteredDevice bool   `json:"requireRegisteredDevice"`
}

// RuntimeInfo describes the process that is currently running, not the saved
// configuration. `restartRequired` compares the two and is never persisted.
type RuntimeInfo struct {
	StartupConfig   ConfigInfo `json:"startupConfig"`
	State           string     `json:"state"`
	Detail          string     `json:"detail"`
	AdvertisedIP    string     `json:"advertisedIp"`
	RestartRequired bool       `json:"restartRequired"`
	MediaCapacity   int        `json:"mediaCapacity"`
}

// ConfigResponse is the payload of both GET and PUT /gb28181/config: the saved
// target plus the running view.
type ConfigResponse struct {
	Config  ConfigInfo  `json:"config"`
	Runtime RuntimeInfo `json:"runtime"`
}

// DeviceResponse is the allowlist entry projection. The Digest password never
// appears here, and its absence is enforced by construction rather than by a
// `json:"-"` tag someone could drop when they touch the model.
type DeviceResponse struct {
	DeviceID        string     `json:"deviceId"`
	Name            string     `json:"name"`
	Manufacturer    *string    `json:"manufacturer"`
	Model           *string    `json:"model"`
	Firmware        *string    `json:"firmware"`
	Transport       string     `json:"transport"`
	RemoteAddr      *string    `json:"remoteAddr"`
	ExpiresSeconds  int        `json:"expiresSeconds"`
	State           string     `json:"state"`
	ChannelCount    int        `json:"channelCount"`
	LastKeepaliveAt *time.Time `json:"lastKeepaliveAt"`
	RegisteredAt    *time.Time `json:"registeredAt"`
	CreatedAt       time.Time  `json:"createdAt"`
	UpdatedAt       time.Time  `json:"updatedAt"`
}

// DeviceListResponse wraps the allowlist. Devices are physically bounded (hundreds
// at most on a single box), so this endpoint is explicitly not paginated — the
// spec allows that only when the upper bound is small, which is why the field is
// `items` rather than a bare array.
type DeviceListResponse struct {
	Items []DeviceResponse `json:"items"`
}

// ChannelResponse is the API projection for a device video channel.
type ChannelResponse struct {
	DeviceID         string    `json:"deviceId"`
	ChannelID        string    `json:"channelId"`
	Name             string    `json:"name"`
	Manufacturer     *string   `json:"manufacturer"`
	Model            *string   `json:"model"`
	Owner            *string   `json:"owner"`
	CivilCode        *string   `json:"civilCode"`
	Address          *string   `json:"address"`
	Parental         int       `json:"parental"`
	ParentID         *string   `json:"parentId"`
	SafetyWay        *int      `json:"safetyWay"`
	RegisterWay      *int      `json:"registerWay"`
	PTZType          *int      `json:"ptzType"`
	ResolutionWidth  *int      `json:"resolutionWidth"`
	ResolutionHeight *int      `json:"resolutionHeight"`
	CameraID         *string   `json:"cameraId"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// ChannelListResponse wraps a channel list.
type ChannelListResponse struct {
	Items []ChannelResponse `json:"items"`
}

// SyncCatalogResponse reports the result of a catalog synchronization.
type SyncCatalogResponse struct {
	DeviceID string `json:"deviceId"`
	Total    int    `json:"total"`
}

// toConfigInfo projects the persisted model onto the API shape.
func toConfigInfo(cfg Config) ConfigInfo {
	return ConfigInfo{
		Enabled:                 cfg.Enabled,
		SIPID:                   cfg.SIPID,
		SIPRealm:                cfg.SIPRealm,
		SIPListen:               cfg.SIPListen,
		SIPTransport:            cfg.SIPTransport,
		SecurityProfile:         cfg.SecurityProfile,
		MediaBindAddress:        cfg.MediaBindAddress,
		MediaAdvertisedIP:       cfg.MediaAdvertisedIP,
		RTPPortMin:              cfg.RTPPortMin,
		RTPPortMax:              cfg.RTPPortMax,
		RequireRegisteredDevice: cfg.RequireRegisteredDevice,
	}
}

// toRuntimeInfo projects the runtime status, deriving media capacity from the
// startup configuration so the UI can show "50 sessions" without recomputing it.
func toRuntimeInfo(status RuntimeStatus) RuntimeInfo {
	return RuntimeInfo{
		StartupConfig:   toConfigInfo(status.Startup),
		State:           status.State,
		Detail:          status.Detail,
		AdvertisedIP:    status.AdvertisedIP,
		RestartRequired: status.RestartRequired,
		MediaCapacity:   status.Startup.MediaCapacity(),
	}
}

// toDeviceResponse projects a device record without its ciphertext.
func toDeviceResponse(device Device) DeviceResponse {
	return DeviceResponse{
		DeviceID:        device.DeviceID,
		Name:            device.Name,
		Manufacturer:    device.Manufacturer,
		Model:           device.Model,
		Firmware:        device.Firmware,
		Transport:       device.Transport,
		RemoteAddr:      device.RemoteAddr,
		ExpiresSeconds:  device.ExpiresSeconds,
		State:           device.State,
		ChannelCount:    device.ChannelCount,
		LastKeepaliveAt: utcOrNil(device.LastKeepaliveAt),
		RegisteredAt:    utcOrNil(device.RegisteredAt),
		CreatedAt:       device.CreatedAt.UTC(),
		UpdatedAt:       device.UpdatedAt.UTC(),
	}
}

// toChannelResponse projects a channel record onto the API shape.
func toChannelResponse(ch Channel) ChannelResponse {
	return ChannelResponse{
		DeviceID:         ch.DeviceID,
		ChannelID:        ch.ChannelID,
		Name:             ch.Name,
		Manufacturer:     ch.Manufacturer,
		Model:            ch.Model,
		Owner:            ch.Owner,
		CivilCode:        ch.CivilCode,
		Address:          ch.Address,
		Parental:         ch.Parental,
		ParentID:         ch.ParentID,
		SafetyWay:        ch.SafetyWay,
		RegisterWay:      ch.RegisterWay,
		PTZType:          ch.PTZType,
		ResolutionWidth:  ch.ResolutionWidth,
		ResolutionHeight: ch.ResolutionHeight,
		CameraID:         ch.CameraID,
		CreatedAt:        ch.CreatedAt.UTC(),
		UpdatedAt:        ch.UpdatedAt.UTC(),
	}
}

// utcOrNil normalizes optional timestamps to UTC without inventing zero values.
func utcOrNil(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
}
