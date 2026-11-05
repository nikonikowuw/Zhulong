package systeminfo

// UpdateSystemIdentityRequest is the payload for editing this host's device identity.
type UpdateSystemIdentityRequest struct {
	DeviceID string `json:"deviceId" binding:"required"`
}
