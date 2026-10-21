package record

import (
	"time"
)

// Recognition is the GORM model for the recognitions table.
type Recognition struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`
	CameraID string `gorm:"column:camera_id;not null" json:"cameraId"`
	// AlgorithmID 为 nil 表示「来源未标注」：算法已被卸载，或宿主无法确定是哪个
	// 算法产生的（AI 流按相机共享，算法归属由 runner 在载荷里打戳，可能缺失）。
	// ⚠️ 必须用 NULL 而不是空串：SQLite 的外键对空串同样要求父表存在匹配行，
	// 只有 NULL 才跳过校验。对外 API 仍以 string("") 呈现，见 ToResponse。
	AlgorithmID *string `gorm:"column:algorithm_id" json:"-"`
	// AlgorithmName / AlgorithmVersion 是**写入时快照**，与 PersonName 同构。
	// 算法包卸载后 algorithms 行消失，id 被置 NULL；若没有这两个冗余字段，
	// 历史证据将永久失去「是哪个算法产生的」这一信息。
	// ⚠️ 严禁改成读取时 join 回 algorithms 表补全 —— 那正是依赖被删数据的反模式。
	AlgorithmName    string    `gorm:"column:algorithm_name;not null;default:''" json:"-"`
	AlgorithmVersion string    `gorm:"column:algorithm_version;not null;default:''" json:"-"`
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
	ID          string  `gorm:"primaryKey;column:id" json:"id"`
	CameraID    string  `gorm:"column:camera_id;not null" json:"cameraId"`
	AlgorithmID *string `gorm:"column:algorithm_id" json:"-"`
	// 同 Recognition.AlgorithmName：算法卸载后的唯一来源凭证。
	AlgorithmName    string    `gorm:"column:algorithm_name;not null;default:''" json:"-"`
	AlgorithmVersion string    `gorm:"column:algorithm_version;not null;default:''" json:"-"`
	AlarmType        string    `gorm:"column:alarm_type;not null" json:"alarmType"`
	Score            float64   `gorm:"column:score;not null;default:0.0" json:"score"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'" json:"-"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	CropPath         string    `gorm:"column:crop_path;not null;default:''" json:"cropPath"`
	ExtraJSON        string    `gorm:"column:extra_json;not null;default:'{}'" json:"-"`
	PTS              int64     `gorm:"column:pts;not null" json:"pts"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null" json:"capturedAt"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (Alarm) TableName() string {
	return "alarms"
}

// Capture is the GORM model for the captures table.
type Capture struct {
	ID       string `gorm:"primaryKey;column:id" json:"id"`
	CameraID string `gorm:"column:camera_id;not null" json:"cameraId"`
	// 算法来源归属，与 Recognition / Alarm 完全同构：
	//   NULL = 来源未标注（算法已卸载，或宿主无法归属）
	//   AlgorithmName / AlgorithmVersion 是写入时快照，算法卸载后的唯一凭证
	AlgorithmID      *string   `gorm:"column:algorithm_id" json:"-"`
	AlgorithmName    string    `gorm:"column:algorithm_name;not null;default:''" json:"-"`
	AlgorithmVersion string    `gorm:"column:algorithm_version;not null;default:''" json:"-"`
	TargetType       string    `gorm:"column:target_type;not null" json:"targetType"`
	TrackID          int64     `gorm:"column:track_id;not null;default:0" json:"trackId"`
	Score            float64   `gorm:"column:score;not null;default:0.0" json:"score"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'" json:"-"`
	CropPath         string    `gorm:"column:crop_path;not null;default:''" json:"cropPath"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	AttributesJSON   string    `gorm:"column:attributes_json;not null;default:'{}'" json:"-"`
	PTS              int64     `gorm:"column:pts;not null" json:"pts"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null" json:"capturedAt"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP" json:"createdAt"`
}

func (Capture) TableName() string {
	return "captures"
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
