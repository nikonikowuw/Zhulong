package record

import (
	"encoding/json"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

// Recognition is the GORM model for the recognitions table.
type Recognition struct {
	ID               string    `gorm:"primaryKey;column:id" json:"id"`
	CameraID         string    `gorm:"column:camera_id;not null" json:"cameraId"`
	AlgorithmID      string    `gorm:"column:algorithm_id;not null" json:"algorithmId"`
	PTS              int64     `gorm:"column:pts;not null" json:"pts"`
	TrackID          int64     `gorm:"column:track_id;not null;default:0" json:"trackId"`
	PersonID         *string   `gorm:"column:person_id" json:"personId"`
	PersonName       string    `gorm:"column:person_name;not null;default:''" json:"personName"`
	MatchedFaceID    string    `gorm:"column:matched_face_id;not null;default:''" json:"matchedFaceId"`
	Matched          bool      `gorm:"column:matched;not null;default:0" json:"matched"`
	Similarity       float64   `gorm:"column:similarity;not null;default:0.0" json:"similarity"`
	CandidatesJSON   string    `gorm:"column:candidates_json;not null;default:'[]'" json:"-"`
	FaceImagePath    string    `gorm:"column:face_image_path;not null;default:''" json:"faceImagePath"`
	GalleryImagePath string    `gorm:"column:gallery_image_path;not null;default:''" json:"galleryImagePath"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'" json:"-"`
	BodyBoxJSON      string    `gorm:"column:body_box_json;not null;default:'[]'" json:"-"`
	AttributesJSON   string    `gorm:"column:attributes_json;not null;default:'{}'" json:"-"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null" json:"capturedAt"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (Recognition) TableName() string {
	return "recognitions"
}

// Alarm is the GORM model for the alarms table.
type Alarm struct {
	ID          string    `gorm:"primaryKey;column:id" json:"id"`
	CameraID    string    `gorm:"column:camera_id;not null" json:"cameraId"`
	AlgorithmID string    `gorm:"column:algorithm_id;not null" json:"algorithmId"`
	AlarmType   string    `gorm:"column:alarm_type;not null" json:"alarmType"`
	Score       float64   `gorm:"column:score;not null;default:0.0" json:"score"`
	BoxJSON     string    `gorm:"column:box_json;not null;default:'[]'" json:"-"`
	ImagePath   string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	CropPath    string    `gorm:"column:crop_path;not null;default:''" json:"cropPath"`
	ExtraJSON   string    `gorm:"column:extra_json;not null;default:'{}'" json:"-"`
	PTS         int64     `gorm:"column:pts;not null" json:"pts"`
	CapturedAt  time.Time `gorm:"column:captured_at;not null" json:"capturedAt"`
	CreatedAt   time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (Alarm) TableName() string {
	return "alarms"
}

// Capture is the GORM model for the captures table.
type Capture struct {
	ID             string    `gorm:"primaryKey;column:id" json:"id"`
	CameraID       string    `gorm:"column:camera_id;not null" json:"cameraId"`
	TargetType     string    `gorm:"column:target_type;not null" json:"targetType"`
	TrackID        int64     `gorm:"column:track_id;not null;default:0" json:"trackId"`
	Score          float64   `gorm:"column:score;not null;default:0.0" json:"score"`
	BoxJSON        string    `gorm:"column:box_json;not null;default:'[]'" json:"-"`
	CropPath       string    `gorm:"column:crop_path;not null;default:''" json:"cropPath"`
	ImagePath      string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	AttributesJSON string    `gorm:"column:attributes_json;not null;default:'{}'" json:"-"`
	PTS            int64     `gorm:"column:pts;not null" json:"pts"`
	CapturedAt     time.Time `gorm:"column:captured_at;not null" json:"capturedAt"`
	CreatedAt      time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (Capture) TableName() string {
	return "captures"
}

// RecognitionDTO represents the public API representation of a Recognition.
type RecognitionDTO struct {
	ID               string                    `json:"id"`
	CameraID         string                    `json:"cameraId"`
	AlgorithmID      string                    `json:"algorithmId"`
	PTS              int64                     `json:"pts"`
	TrackID          int64                     `json:"trackId"`
	PersonID         *string                   `json:"personId"`
	PersonName       string                    `json:"personName"`
	MatchedFaceID    string                    `json:"matchedFaceId"`
	Matched          bool                      `json:"matched"`
	Similarity       float64                   `json:"similarity"`
	Candidates       []algorithm.FaceCandidate `json:"candidates"`
	FaceImagePath    string                    `json:"faceImagePath"`
	GalleryImagePath string                    `json:"galleryImagePath"`
	ImagePath        string                    `json:"imagePath"`
	Box              [4]float64                `json:"box"`
	BodyBox          [4]float64                `json:"bodyBox"`
	Attributes       map[string]any            `json:"attributes"`
	CapturedAt       time.Time                 `json:"capturedAt"`
	CreatedAt        time.Time                 `json:"createdAt"`
}

// ToDTO converts a Recognition GORM model to RecognitionDTO.
func (r *Recognition) ToDTO() RecognitionDTO {
	candidates := make([]algorithm.FaceCandidate, 0)
	if r.CandidatesJSON != "" {
		_ = json.Unmarshal([]byte(r.CandidatesJSON), &candidates)
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

	return RecognitionDTO{
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

// AlarmDTO represents the public API representation of an Alarm.
type AlarmDTO struct {
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

// ToDTO converts an Alarm GORM model to AlarmDTO.
func (a *Alarm) ToDTO() AlarmDTO {
	var box [4]float64
	if a.BoxJSON != "" {
		_ = json.Unmarshal([]byte(a.BoxJSON), &box)
	}

	extra := make(map[string]any)
	if a.ExtraJSON != "" {
		_ = json.Unmarshal([]byte(a.ExtraJSON), &extra)
	}

	return AlarmDTO{
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

// CaptureDTO represents the public API representation of a Capture.
type CaptureDTO struct {
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

// ToDTO converts a Capture GORM model to CaptureDTO.
func (c *Capture) ToDTO() CaptureDTO {
	var box [4]float64
	if c.BoxJSON != "" {
		_ = json.Unmarshal([]byte(c.BoxJSON), &box)
	}

	attributes := make(map[string]any)
	if c.AttributesJSON != "" {
		_ = json.Unmarshal([]byte(c.AttributesJSON), &attributes)
	}

	return CaptureDTO{
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

// RecognitionFilter defines query filter criteria for Recognitions.
type RecognitionFilter struct {
	CameraID  string     `form:"cameraId"`
	PersonID  string     `form:"personId"`
	Matched   *bool      `form:"matched"`
	StartTime *time.Time `form:"startTime" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime   *time.Time `form:"endTime" time_format:"2006-01-02T15:04:05Z07:00"`
	Page      int        `form:"page"`
	PageSize  int        `form:"pageSize"`
}

// AlarmFilter defines query filter criteria for Alarms.
type AlarmFilter struct {
	CameraID  string     `form:"cameraId"`
	AlarmType string     `form:"alarmType"`
	StartTime *time.Time `form:"startTime" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime   *time.Time `form:"endTime" time_format:"2006-01-02T15:04:05Z07:00"`
	Page      int        `form:"page"`
	PageSize  int        `form:"pageSize"`
}

// CaptureFilter defines query filter criteria for Captures.
type CaptureFilter struct {
	CameraID   string     `form:"cameraId"`
	TargetType string     `form:"targetType"`
	StartTime  *time.Time `form:"startTime" time_format:"2006-01-02T15:04:05Z07:00"`
	EndTime    *time.Time `form:"endTime" time_format:"2006-01-02T15:04:05Z07:00"`
	Page       int        `form:"page"`
	PageSize   int        `form:"pageSize"`
}
