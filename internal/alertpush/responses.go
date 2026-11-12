package alertpush

// AlertDestinationResponse represents external API projection of an alert destination.
type AlertDestinationResponse struct {
	ID               string                     `json:"id"`
	Name             string                     `json:"name"`
	ChannelType      string                     `json:"channelType"`
	Enabled          bool                       `json:"enabled"`
	EventsEnabled    bool                       `json:"eventsEnabled"`
	HeartbeatEnabled bool                       `json:"heartbeatEnabled"`
	ImageMode        string                     `json:"imageMode"`
	Config           AlertDestinationConfigInfo `json:"config"`
	Filter           AlertDestinationFilterInfo `json:"filter"`
	SecretSet        bool                       `json:"secretSet"`
	HeaderNames      []string                   `json:"headerNames,omitempty"`
	Status           AlertDestinationStatus     `json:"status"`
}

// AlertDestinationListResponse represents the list envelope.
type AlertDestinationListResponse struct {
	Items []AlertDestinationResponse `json:"items"`
}

// AlertDestinationConfigInfo exposes non-sensitive configuration plus decrypted URLs for edit.
//
// HeartbeatURL and HeartbeatTopic use pointer fields so update requests can distinguish
// "omitted (keep existing)" from "explicitly empty string (clear override)".
type AlertDestinationConfigInfo struct {
	URL                      string  `json:"url,omitempty"`
	HeartbeatURL             *string `json:"heartbeatUrl,omitempty"`
	AuthType                 string  `json:"authType,omitempty"`
	TimeoutSeconds           *int    `json:"timeoutSeconds,omitempty"`
	Broker                   string  `json:"broker,omitempty"`
	ClientID                 string  `json:"clientId,omitempty"`
	Username                 string  `json:"username,omitempty"`
	Topic                    string  `json:"topic,omitempty"`
	HeartbeatTopic           *string `json:"heartbeatTopic,omitempty"`
	LWTEnabled               *bool   `json:"lwtEnabled,omitempty"`
	QoS                      *uint8  `json:"qos,omitempty"`
	KeepAliveSeconds         *int    `json:"keepAliveSeconds,omitempty"`
	TLSSkipVerify            bool    `json:"tlsSkipVerify,omitempty"`
	HeartbeatIntervalSeconds *int    `json:"heartbeatIntervalSeconds,omitempty"`
}

// AlertDestinationFilterInfo exposes event type, algorithm and camera filtering.
type AlertDestinationFilterInfo struct {
	EventTypes   []string `json:"eventTypes"`
	AlgorithmIDs []string `json:"algorithmIds"`
	CameraIDs    []string `json:"cameraIds"`
}

// TestAlertDestinationResponse reports connectivity test results.
type TestAlertDestinationResponse struct {
	OK        bool   `json:"ok"`
	LatencyMs int64  `json:"latencyMs"`
	Reason    string `json:"reason,omitempty"`
}
