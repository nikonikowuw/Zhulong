package record

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// Recognition is the GORM model for the recognitions table.
type Recognition struct {
	ID       string `gorm:"primaryKey;column:id"`
	CameraID string `gorm:"column:camera_id;not null"`
	// AlgorithmID 为 nil 表示「来源未标注」：算法已被卸载，或宿主无法确定是哪个
	// 算法产生的（AI 流按相机共享，算法归属由 runner 在载荷里打戳，可能缺失）。
	// ⚠️ 必须用 NULL 而不是空串：SQLite 的外键对空串同样要求父表存在匹配行，
	// 只有 NULL 才跳过校验。对外 API 仍以 string("") 呈现，见 ToResponse。
	AlgorithmID *string `gorm:"column:algorithm_id"`
	// AlgorithmName / AlgorithmVersion 是**写入时快照**，与 PersonName 同构。
	// 算法包卸载后 algorithms 行消失，id 被置 NULL；若没有这两个冗余字段，
	// 历史证据将永久失去「是哪个算法产生的」这一信息。
	// ⚠️ 严禁改成读取时 join 回 algorithms 表补全 —— 那正是依赖被删数据的反模式。
	AlgorithmName    string `gorm:"column:algorithm_name;not null;default:''"`
	AlgorithmVersion string `gorm:"column:algorithm_version;not null;default:''"`
	PTS              int64  `gorm:"column:pts;not null"`
	TrackID          int64  `gorm:"column:track_id;not null;default:0"`
	// PersonID 是内部 persons.id 的弱外键；PersonExternalID 是同一时刻对外业务键
	// （persons.person_id）的写入时快照。
	// ⚠️ 两者不可互换：前者负责库内关联与 ON DELETE SET NULL，后者负责跨系统上报。
	// 底库 IPC 载荷下发的 person_id 是业务键原值，绝不能直接写进 PersonID ——
	// 那会让业务键不等于内部主键的人员，每次命中都撞外键、整批记录静默丢失。
	PersonID         *string   `gorm:"column:person_id"`
	PersonExternalID string    `gorm:"column:person_external_id;not null;default:''"`
	PersonName       string    `gorm:"column:person_name;not null;default:''"`
	MatchedFaceID    string    `gorm:"column:matched_face_id;not null;default:''"`
	Matched          bool      `gorm:"column:matched;not null;default:0"`
	Similarity       float64   `gorm:"column:similarity;not null;default:0.0"`
	CandidatesJSON   string    `gorm:"column:candidates_json;not null;default:'[]'"`
	FaceImagePath    string    `gorm:"column:face_image_path;not null;default:''"`
	GalleryImagePath string    `gorm:"column:gallery_image_path;not null;default:''"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'"`
	BodyBoxJSON      string    `gorm:"column:body_box_json;not null;default:'[]'"`
	AttributesJSON   string    `gorm:"column:attributes_json;not null;default:'{}'"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
}

func (Recognition) TableName() string {
	return "recognitions"
}

// Alarm is the GORM model for the alarms table.
type Alarm struct {
	ID          string  `gorm:"primaryKey;column:id"`
	CameraID    string  `gorm:"column:camera_id;not null"`
	AlgorithmID *string `gorm:"column:algorithm_id"`
	// 同 Recognition.AlgorithmName：算法卸载后的唯一来源凭证。
	AlgorithmName    string    `gorm:"column:algorithm_name;not null;default:''"`
	AlgorithmVersion string    `gorm:"column:algorithm_version;not null;default:''"`
	AlarmType        string    `gorm:"column:alarm_type;not null"`
	Score            float64   `gorm:"column:score;not null;default:0.0"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''"`
	CropPath         string    `gorm:"column:crop_path;not null;default:''"`
	ExtraJSON        string    `gorm:"column:extra_json;not null;default:'{}'"`
	PTS              int64     `gorm:"column:pts;not null"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
}

func (Alarm) TableName() string {
	return "alarms"
}

// Capture is the GORM model for the captures table.
type Capture struct {
	ID       string `gorm:"primaryKey;column:id"`
	CameraID string `gorm:"column:camera_id;not null"`
	// 算法来源归属，与 Recognition / Alarm 完全同构：
	//   NULL = 来源未标注（算法已卸载，或宿主无法归属）
	//   AlgorithmName / AlgorithmVersion 是写入时快照，算法卸载后的唯一凭证
	AlgorithmID      *string   `gorm:"column:algorithm_id"`
	AlgorithmName    string    `gorm:"column:algorithm_name;not null;default:''"`
	AlgorithmVersion string    `gorm:"column:algorithm_version;not null;default:''"`
	TargetType       string    `gorm:"column:target_type;not null"`
	TrackID          int64     `gorm:"column:track_id;not null;default:0"`
	Score            float64   `gorm:"column:score;not null;default:0.0"`
	BoxJSON          string    `gorm:"column:box_json;not null;default:'[]'"`
	CropPath         string    `gorm:"column:crop_path;not null;default:''"`
	ImagePath        string    `gorm:"column:image_path;not null;default:''"`
	AttributesJSON   string    `gorm:"column:attributes_json;not null;default:'{}'"`
	PTS              int64     `gorm:"column:pts;not null"`
	CapturedAt       time.Time `gorm:"column:captured_at;not null"`
	CreatedAt        time.Time `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
}

func (Capture) TableName() string {
	return "captures"
}

// RecognitionFilter defines query filter criteria for Recognitions.
type RecognitionFilter struct {
	CameraID string
	// PersonID filters on the internal persons.id; PersonExternalID filters on the
	// external business-key snapshot. They are separate parameters on purpose: the
	// business key is what an outside system correlates with, and it never equals
	// the internal row id once a person has been assigned one.
	PersonID         string
	PersonExternalID string
	Matched          *bool
	StartTime        *time.Time
	EndTime          *time.Time
	// Window 是已解析的分页窗口。store 直接用它，不再自行从 page/pageSize
	// 推导 offset —— 二段推导正是 legacy limit/offset 被静默取整的原因。
	Window httputil.PageWindow
}

// AlarmFilter defines query filter criteria for Alarms.
type AlarmFilter struct {
	CameraID  string
	AlarmType string
	StartTime *time.Time
	EndTime   *time.Time
	// Window 同 RecognitionFilter.Window。
	Window httputil.PageWindow
}

// CaptureFilter defines query filter criteria for Captures.
type CaptureFilter struct {
	CameraID   string
	TargetType string
	StartTime  *time.Time
	EndTime    *time.Time
	// Window 同 RecognitionFilter.Window。
	Window httputil.PageWindow
}
