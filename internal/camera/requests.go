package camera

// CreateStreamRequest represents stream configuration in creation or update requests.
type CreateStreamRequest struct {
	Role      string `json:"role"`     // "main" or "sub"
	Protocol  string `json:"protocol"` // "rtsp" (default)
	RTSPURL   string `json:"rtspUrl" binding:"required"`
	Transport string `json:"transport"` // "tcp" or "udp" (default "tcp")
}

// CreateCameraRequest represents parameters for adding a new camera.
type CreateCameraRequest struct {
	ID         string               `json:"id,omitempty"`
	Name       string               `json:"name" binding:"required,min=1,max=64"`
	Enabled    *bool                `json:"enabled,omitempty"`
	MainStream CreateStreamRequest  `json:"mainStream" binding:"required"`
	SubStream  *CreateStreamRequest `json:"subStream,omitempty"`
}

// UpdateCameraRequest represents parameters for updating an existing camera.
type UpdateCameraRequest struct {
	Revision   int64                `json:"revision" binding:"required"`
	Name       *string              `json:"name,omitempty" binding:"omitempty,min=1,max=64"`
	Enabled    *bool                `json:"enabled,omitempty"`
	MainStream *CreateStreamRequest `json:"mainStream,omitempty"`
	SubStream  *CreateStreamRequest `json:"subStream,omitempty"`
}
