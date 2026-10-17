package record

import (
	"encoding/json"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// RecognitionResponse represents the public API representation of a Recognition.
type RecognitionResponse struct {
	ID               string                  `json:"id"`
	CameraID         string                  `json:"cameraId"`
	AlgorithmID      string                  `json:"algorithmId"`
	PTS              int64                   `json:"pts"`
	TrackID          int64                   `json:"trackId"`
	PersonID         *string                 `json:"personId"`
	PersonName       string                  `json:"personName"`
	MatchedFaceID    string                  `json:"matchedFaceId"`
	Matched          bool                    `json:"matched"`
	Similarity       float64                 `json:"similarity"`
	Candidates       []aitypes.FaceCandidate `json:"candidates"`
	FaceImagePath    string                  `json:"faceImagePath"`
	GalleryImagePath string                  `json:"galleryImagePath"`
	ImagePath        string                  `json:"imagePath"`
	Box              [4]float64              `json:"box"`
	BodyBox          [4]float64              `json:"bodyBox"`
	Attributes       map[string]any          `json:"attributes"`
	CapturedAt       time.Time               `json:"capturedAt"`
	CreatedAt        time.Time               `json:"createdAt"`
}

// ToResponse converts a Recognition GORM model to RecognitionResponse.
func (r *Recognition) ToResponse() RecognitionResponse {
	candidates := make([]aitypes.FaceCandidate, 0)
	if r.CandidatesJSON != "" {
		_ = json.Unmarshal([]byte(r.CandidatesJSON), &candidates)
	}
	if candidates == nil {
		candidates = make([]aitypes.FaceCandidate, 0)
	}

	var box [4]float64
	if r.BoxJSON != "" {
		_ = json.Unmarshal([]byte(r.BoxJSON), &box)
	}

	var bodyBox [4]float64
	if r.BodyBoxJSON != "" {
		_ = json.Unmarshal([]byte(r.BodyBoxJSON), &bodyBox)
	}

	attributes := make(map[string]any)
	if r.AttributesJSON != "" {
		_ = json.Unmarshal([]byte(r.AttributesJSON), &attributes)
	}
	if attributes == nil {
		attributes = make(map[string]any)
	}

	return RecognitionResponse{
		ID:               r.ID,
		CameraID:         r.CameraID,
		AlgorithmID:      r.AlgorithmID,
		PTS:              r.PTS,
		TrackID:          r.TrackID,
		PersonID:         r.PersonID,
		PersonName:       r.PersonName,
		MatchedFaceID:    r.MatchedFaceID,
		Matched:          r.Matched,
		Similarity:       r.Similarity,
		Candidates:       candidates,
		FaceImagePath:    r.FaceImagePath,
		GalleryImagePath: r.GalleryImagePath,
		ImagePath:        r.ImagePath,
		Box:              box,
		BodyBox:          bodyBox,
		Attributes:       attributes,
		CapturedAt:       r.CapturedAt.UTC(),
		CreatedAt:        r.CreatedAt.UTC(),
	}
}

// AlarmResponse represents the public API representation of an Alarm.
type AlarmResponse struct {
	ID          string         `json:"id"`
	CameraID    string         `json:"cameraId"`
	AlgorithmID string         `json:"algorithmId"`
	AlarmType   string         `json:"alarmType"`
	Score       float64        `json:"score"`
	Box         [4]float64     `json:"box"`
	ImagePath   string         `json:"imagePath"`
	CropPath    string         `json:"cropPath"`
	Extra       map[string]any `json:"extra"`
	PTS         int64          `json:"pts"`
	CapturedAt  time.Time      `json:"capturedAt"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// ToResponse converts an Alarm GORM model to AlarmResponse.
func (a *Alarm) ToResponse() AlarmResponse {
	var box [4]float64
	if a.BoxJSON != "" {
		_ = json.Unmarshal([]byte(a.BoxJSON), &box)
	}

	extra := make(map[string]any)
	if a.ExtraJSON != "" {
		_ = json.Unmarshal([]byte(a.ExtraJSON), &extra)
	}
	if extra == nil {
		extra = make(map[string]any)
	}

	return AlarmResponse{
		ID:          a.ID,
		CameraID:    a.CameraID,
		AlgorithmID: a.AlgorithmID,
		AlarmType:   a.AlarmType,
		Score:       a.Score,
		Box:         box,
		ImagePath:   a.ImagePath,
		CropPath:    a.CropPath,
		Extra:       extra,
		PTS:         a.PTS,
		CapturedAt:  a.CapturedAt.UTC(),
		CreatedAt:   a.CreatedAt.UTC(),
	}
}

// CaptureResponse represents the public API representation of a Capture.
type CaptureResponse struct {
	ID         string         `json:"id"`
	CameraID   string         `json:"cameraId"`
	TargetType string         `json:"targetType"`
	TrackID    int64          `json:"trackId"`
	Score      float64        `json:"score"`
	Box        [4]float64     `json:"box"`
	CropPath   string         `json:"cropPath"`
	ImagePath  string         `json:"imagePath"`
	Attributes map[string]any `json:"attributes"`
	PTS        int64          `json:"pts"`
	CapturedAt time.Time      `json:"capturedAt"`
	CreatedAt  time.Time      `json:"createdAt"`
}

// ToResponse converts a Capture GORM model to CaptureResponse.
func (c *Capture) ToResponse() CaptureResponse {
	var box [4]float64
	if c.BoxJSON != "" {
		_ = json.Unmarshal([]byte(c.BoxJSON), &box)
	}

	attributes := make(map[string]any)
	if c.AttributesJSON != "" {
		_ = json.Unmarshal([]byte(c.AttributesJSON), &attributes)
	}
	if attributes == nil {
		attributes = make(map[string]any)
	}

	return CaptureResponse{
		ID:         c.ID,
		CameraID:   c.CameraID,
		TargetType: c.TargetType,
		TrackID:    c.TrackID,
		Score:      c.Score,
		Box:        box,
		CropPath:   c.CropPath,
		ImagePath:  c.ImagePath,
		Attributes: attributes,
		PTS:        c.PTS,
		CapturedAt: c.CapturedAt.UTC(),
		CreatedAt:  c.CreatedAt.UTC(),
	}
}
