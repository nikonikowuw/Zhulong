package gb28181

// UpdateConfigRequest is the full-replacement PUT body. Every field is provided so
// the client always sends a complete target configuration; partial updates would
// make "restartRequired" depend on merge order.
//
// The RTP port pair is the deliberate exception expressed with omitempty: a zero
// value means "not provided" and Normalize fills the documented defaults, which is
// the same rule the domain model documents. Only a half-zero pair is an error.
type UpdateConfigRequest struct {
	Enabled                 bool   `json:"enabled"`
	SIPID                   string `json:"sipId" binding:"max=64"`
	SIPRealm                string `json:"sipRealm" binding:"max=64"`
	SIPListen               string `json:"sipListen" binding:"max=64"`
	SIPTransport            string `json:"sipTransport" binding:"omitempty,oneof=udp tcp both"`
	SecurityProfile         string `json:"securityProfile" binding:"omitempty,oneof=standard"`
	MediaBindAddress        string `json:"mediaBindAddress" binding:"max=64"`
	MediaAdvertisedIP       string `json:"mediaAdvertisedIp" binding:"max=64"`
	RTPPortMin              int    `json:"rtpPortMin" binding:"omitempty,min=1,max=65535"`
	RTPPortMax              int    `json:"rtpPortMax" binding:"omitempty,min=1,max=65535"`
	RequireRegisteredDevice *bool  `json:"requireRegisteredDevice" binding:"required"`
}

// ToConfig converts the request into the domain model. Validation is left to the
// service so both the API and direct callers share one rule set.
func (r UpdateConfigRequest) ToConfig() Config {
	return Config{
		ID:                      ConfigSingletonID,
		Enabled:                 r.Enabled,
		SIPID:                   r.SIPID,
		SIPRealm:                r.SIPRealm,
		SIPListen:               r.SIPListen,
		SIPTransport:            r.SIPTransport,
		SecurityProfile:         r.SecurityProfile,
		MediaBindAddress:        r.MediaBindAddress,
		MediaAdvertisedIP:       r.MediaAdvertisedIP,
		RTPPortMin:              r.RTPPortMin,
		RTPPortMax:              r.RTPPortMax,
		RequireRegisteredDevice: r.RequireRegisteredDevice != nil && *r.RequireRegisteredDevice,
	}
}

// UpsertDeviceRequest pre-provisions one device.
//
// Name and Password are pointers on purpose: omitting them keeps the stored value,
// so renaming a device does not require retyping (or transmitting) its Digest
// secret, and rotating the secret does not silently erase the name.
//
// ExpiresSeconds is the *default* lease: it is granted when the device omits Expires
// from its REGISTER. A device that asks for its own lease gets that one instead, and
// the stored value becomes the record of what was granted.
type UpsertDeviceRequest struct {
	Name           *string `json:"name" binding:"omitempty,max=128"`
	Password       *string `json:"password" binding:"omitempty,max=128"`
	Transport      *string `json:"transport" binding:"omitempty,oneof=udp tcp"`
	ExpiresSeconds *int    `json:"expiresSeconds" binding:"omitempty,min=60,max=86400"`
}

// ToUpsert converts the request into the store's write shape.
func (r UpsertDeviceRequest) ToUpsert(deviceID string) DeviceUpsert {
	return DeviceUpsert{
		DeviceID:       deviceID,
		Name:           r.Name,
		Password:       r.Password,
		Transport:      r.Transport,
		ExpiresSeconds: r.ExpiresSeconds,
	}
}
