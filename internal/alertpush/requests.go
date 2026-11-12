package alertpush

// CreateAlertDestinationRequest payload for creating a new alert destination.
type CreateAlertDestinationRequest struct {
	Name             string                      `json:"name" binding:"required"`
	ChannelType      string                      `json:"channelType" binding:"required"`
	EventsEnabled    *bool                       `json:"eventsEnabled,omitempty"`
	HeartbeatEnabled *bool                       `json:"heartbeatEnabled,omitempty"`
	ImageMode        string                      `json:"imageMode,omitempty"`
	Config           AlertDestinationConfigInfo  `json:"config" binding:"required"`
	Filter           *AlertDestinationFilterInfo `json:"filter,omitempty"`
	Secret           *AlertDestinationSecretInfo `json:"secret,omitempty"`
}

// UpdateAlertDestinationRequest payload for updating an alert destination.
type UpdateAlertDestinationRequest struct {
	Name             *string                     `json:"name,omitempty"`
	EventsEnabled    *bool                       `json:"eventsEnabled,omitempty"`
	HeartbeatEnabled *bool                       `json:"heartbeatEnabled,omitempty"`
	ImageMode        *string                     `json:"imageMode,omitempty"`
	Config           *AlertDestinationConfigInfo `json:"config,omitempty"`
	Filter           *AlertDestinationFilterInfo `json:"filter,omitempty"`
	Secret           *AlertDestinationSecretInfo `json:"secret,omitempty"`
}

// TestAlertDestinationRequest payload for testing destination connectivity.
type TestAlertDestinationRequest struct {
	ID               string                      `json:"id,omitempty"`
	ChannelType      string                      `json:"channelType"`
	EventsEnabled    *bool                       `json:"eventsEnabled,omitempty"`
	HeartbeatEnabled *bool                       `json:"heartbeatEnabled,omitempty"`
	ImageMode        string                      `json:"imageMode,omitempty"`
	Config           AlertDestinationConfigInfo  `json:"config"`
	Secret           *AlertDestinationSecretInfo `json:"secret,omitempty"`
}

// AlertDestinationSecretInfo holds sensitive inbound credentials. Never returned in responses.
type AlertDestinationSecretInfo struct {
	Token    *string           `json:"token,omitempty"`
	Username *string           `json:"username,omitempty"`
	Password *string           `json:"password,omitempty"`
	Headers  map[string]string `json:"headers,omitempty"`
}
