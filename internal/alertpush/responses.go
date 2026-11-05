package alertpush

// AlertDestinationResponse represents external API projection of an alert destination.
type AlertDestinationResponse struct {
	ID          string                     `json:"id"`
	Name        string                     `json:"name"`
	ChannelType string                     `json:"channelType"`
	Enabled     bool                       `json:"enabled"`
	ImageMode   string                     `json:"imageMode"`
	Config      AlertDestinationConfigInfo `json:"config"`
	Filter      AlertDestinationFilterInfo `json:"filter"`
	SecretSet   bool                       `json:"secretSet"`
	HeaderNames []string                   `json:"headerNames,omitempty"`
	Status      AlertDestinationStatus     `json:"status"`
}

// AlertDestinationListResponse represents the list envelope.
type AlertDestinationListResponse struct {
	Items []AlertDestinationResponse `json:"items"`
}

// AlertDestinationConfigInfo exposes non-sensitive configuration plus decrypted URL for edit.
type AlertDestinationConfigInfo struct {
	URL              string `json:"url,omitempty"`
	AuthType         string `json:"authType,omitempty"`
	TimeoutSeconds   *int   `json:"timeoutSeconds,omitempty"`
	Broker           string `json:"broker,omitempty"`
	ClientID         string `json:"clientId,omitempty"`
	Username         string `json:"username,omitempty"`
	Topic            string `json:"topic,omitempty"`
	QoS              *uint8 `json:"qos,omitempty"`
	KeepAliveSeconds *int   `json:"keepAliveSeconds,omitempty"`
	TLSSkipVerify    bool   `json:"tlsSkipVerify,omitempty"`
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
