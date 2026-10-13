package mediafile

import "time"

// MediaEntry describes a single media file addressed by its relative path.
//
// Only fields that a single stat call can produce are exposed. Duration,
// resolution and codec are intentionally absent: obtaining them requires
// parsing the container, which the byte-transfer boundary forbids.
type MediaEntry struct {
	// Path is the relative path inside the media root, using forward slashes
	// and no leading slash. This is the same value persisted by the event
	// domain, so it can round-trip back into the content endpoint.
	Path string `json:"path"`
	// Name is the final path element.
	Name string `json:"name"`
	// SizeBytes is the file length in bytes.
	SizeBytes int64 `json:"sizeBytes"`
	// ModifiedAt is the file modification time in UTC (RFC3339Nano).
	ModifiedAt time.Time `json:"modifiedAt"`
}

// MediaDirectories reports which of the three media subdirectories exist under
// the current media root.
type MediaDirectories struct {
	Recordings bool `json:"recordings"`
	Snapshots  bool `json:"snapshots"`
	Exports    bool `json:"exports"`
}

// MediaRootInfo describes the availability and identity of the media root.
//
// It serves two audiences: the frontend, which uses it to decide whether media
// can be fetched at all, and operators, who use the device identity to confirm
// that an external disk is actually mounted instead of silently fallen through
// to the system root filesystem.
type MediaRootInfo struct {
	Available   bool             `json:"available"`
	MediaRoot   string           `json:"mediaRoot"`
	DeviceID    uint64           `json:"deviceId"`
	RootDevice  uint64           `json:"rootDeviceId"`
	MountPoint  string           `json:"mountPoint"`
	FSType      string           `json:"fsType"`
	IsExternal  bool             `json:"isExternal"`
	IsReadOnly  bool             `json:"isReadOnly"`
	Directories MediaDirectories `json:"directories"`
}
