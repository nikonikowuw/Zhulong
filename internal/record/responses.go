package record

import (
	"encoding/json"
	"time"
)

// RecognitionCandidateInfo represents one Top-N face match candidate in a recognition response.
//
// PersonID carries the external business key emitted by the runner (may be empty if
// the person had none), whereas PersonRowID is resolved from the local person_faces
// table at query time so the UI can fetch /persons/{personRowId}/faces/{faceId}/image.
type RecognitionCandidateInfo struct {
	PersonID    string  `json:"person_id"`
	PersonRowID string  `json:"personRowId"`
	PersonName  string  `json:"person_name"`
	FaceID      string  `json:"face_id"`
	Similarity  float64 `json:"similarity"`
}

// RecognitionResponse represents the public API representation of a Recognition.
type RecognitionResponse struct {
	ID          string `json:"id"`
	CameraID    string `json:"cameraId"`
	AlgorithmID string `json:"algorithmId"`
	// AlgorithmName/Version 来自写入时快照：算法被卸载后 algorithmId 变空，
	// 但这两个字段仍能回答「这批记录是哪个算法产生的」。
	AlgorithmName    string  `json:"algorithmName"`
	AlgorithmVersion string  `json:"algorithmVersion"`
	PTS              int64   `json:"pts"`
	TrackID          int64   `json:"trackId"`
	PersonID         *string `json:"personId"`
	// PersonExternalID 是上报业务系统用的身份：写入时快照的 persons.person_id。
	// 空串表示该人员当时尚未绑定业务键 —— 该条记录不可上报，不要回退成 personId。
	PersonExternalID string                     `json:"personExternalId"`
	PersonName       string                     `json:"personName"`
	MatchedFaceID    string                     `json:"matchedFaceId"`
	Matched          bool                       `json:"matched"`
	Similarity       float64                    `json:"similarity"`
	Candidates       []RecognitionCandidateInfo `json:"candidates"`
	FaceImagePath    string                     `json:"faceImagePath"`
	GalleryImagePath string                     `json:"galleryImagePath"`
	ImagePath        string                     `json:"imagePath"`
	Box              [4]float64                 `json:"box"`
	BodyBox          [4]float64                 `json:"bodyBox"`
	Attributes       map[string]any             `json:"attributes"`
	CapturedAt       time.Time                  `json:"capturedAt"`
	CreatedAt        time.Time                  `json:"createdAt"`
}

// derefString 把可空列转换成对外 API 的空串表示。
//
// 对外契约始终是 string（前端 schema 与 Swagger 都按 string 声明），因此
// 「来源未标注」在 API 层表现为 ""，而不是 null。
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func unmarshalSliceOrDefault[T any](raw string) []T {
	fallback := make([]T, 0)
	if raw == "" {
		return fallback
	}
	var target []T
	if err := json.Unmarshal([]byte(raw), &target); err != nil || target == nil {
		return fallback
	}
	return target
}

func unmarshalMapOrDefault(raw string) map[string]any {
	fallback := make(map[string]any)
	if raw == "" {
		return fallback
	}
	var target map[string]any
	if err := json.Unmarshal([]byte(raw), &target); err != nil || target == nil {
		return fallback
	}
	return target
}

func unmarshalBox(raw string) [4]float64 {
	if raw == "" {
		return [4]float64{}
	}
	var box [4]float64
	if err := json.Unmarshal([]byte(raw), &box); err != nil {
		// 坏 JSON 可能已部分写入 box；对外必须是零框，不能把半个坐标泄漏出去。
		return [4]float64{}
	}
	return box
}

// ToResponse converts a Recognition GORM model to RecognitionResponse.
func (r *Recognition) ToResponse() RecognitionResponse {
	return RecognitionResponse{
		ID:               r.ID,
		CameraID:         r.CameraID,
		AlgorithmID:      derefString(r.AlgorithmID),
		AlgorithmName:    r.AlgorithmName,
		AlgorithmVersion: r.AlgorithmVersion,
		PTS:              r.PTS,
		TrackID:          r.TrackID,
		PersonID:         r.PersonID,
		PersonExternalID: r.PersonExternalID,
		PersonName:       r.PersonName,
		MatchedFaceID:    r.MatchedFaceID,
		Matched:          r.Matched,
		Similarity:       r.Similarity,
		Candidates:       unmarshalSliceOrDefault[RecognitionCandidateInfo](r.CandidatesJSON),
		FaceImagePath:    r.FaceImagePath,
		GalleryImagePath: r.GalleryImagePath,
		ImagePath:        r.ImagePath,
		Box:              unmarshalBox(r.BoxJSON),
		BodyBox:          unmarshalBox(r.BodyBoxJSON),
		Attributes:       unmarshalMapOrDefault(r.AttributesJSON),
		CapturedAt:       r.CapturedAt.UTC(),
		CreatedAt:        r.CreatedAt.UTC(),
	}
}

// AlarmResponse represents the public API representation of an Alarm.
type AlarmResponse struct {
	ID          string `json:"id"`
	CameraID    string `json:"cameraId"`
	AlgorithmID string `json:"algorithmId"`
	// 同 RecognitionResponse：算法卸载后的来源凭证。
	AlgorithmName    string         `json:"algorithmName"`
	AlgorithmVersion string         `json:"algorithmVersion"`
	AlarmType        string         `json:"alarmType"`
	Score            float64        `json:"score"`
	Box              [4]float64     `json:"box"`
	ImagePath        string         `json:"imagePath"`
	CropPath         string         `json:"cropPath"`
	Extra            map[string]any `json:"extra"`
	PTS              int64          `json:"pts"`
	CapturedAt       time.Time      `json:"capturedAt"`
	CreatedAt        time.Time      `json:"createdAt"`
}

// ToResponse converts an Alarm GORM model to AlarmResponse.
func (a *Alarm) ToResponse() AlarmResponse {
	return AlarmResponse{
		ID:               a.ID,
		CameraID:         a.CameraID,
		AlgorithmID:      derefString(a.AlgorithmID),
		AlgorithmName:    a.AlgorithmName,
		AlgorithmVersion: a.AlgorithmVersion,
		AlarmType:        a.AlarmType,
		Score:            a.Score,
		Box:              unmarshalBox(a.BoxJSON),
		ImagePath:        a.ImagePath,
		CropPath:         a.CropPath,
		Extra:            unmarshalMapOrDefault(a.ExtraJSON),
		PTS:              a.PTS,
		CapturedAt:       a.CapturedAt.UTC(),
		CreatedAt:        a.CreatedAt.UTC(),
	}
}

// CaptureResponse represents the public API representation of a Capture.
type CaptureResponse struct {
	ID       string `json:"id"`
	CameraID string `json:"cameraId"`
	// 与 RecognitionResponse / AlarmResponse 同构：算法卸载后 ID 变空，
	// 名称与版本快照仍能回答「这张抓拍是哪个算法产生的」。
	AlgorithmID      string         `json:"algorithmId"`
	AlgorithmName    string         `json:"algorithmName"`
	AlgorithmVersion string         `json:"algorithmVersion"`
	TargetType       string         `json:"targetType"`
	TrackID          int64          `json:"trackId"`
	Score            float64        `json:"score"`
	Box              [4]float64     `json:"box"`
	CropPath         string         `json:"cropPath"`
	ImagePath        string         `json:"imagePath"`
	Attributes       map[string]any `json:"attributes"`
	PTS              int64          `json:"pts"`
	CapturedAt       time.Time      `json:"capturedAt"`
	CreatedAt        time.Time      `json:"createdAt"`
}

// ToResponse converts a Capture GORM model to CaptureResponse.
func (c *Capture) ToResponse() CaptureResponse {
	return CaptureResponse{
		ID:               c.ID,
		CameraID:         c.CameraID,
		AlgorithmID:      derefString(c.AlgorithmID),
		AlgorithmName:    c.AlgorithmName,
		AlgorithmVersion: c.AlgorithmVersion,
		TargetType:       c.TargetType,
		TrackID:          c.TrackID,
		Score:            c.Score,
		Box:              unmarshalBox(c.BoxJSON),
		CropPath:         c.CropPath,
		ImagePath:        c.ImagePath,
		Attributes:       unmarshalMapOrDefault(c.AttributesJSON),
		PTS:              c.PTS,
		CapturedAt:       c.CapturedAt.UTC(),
		CreatedAt:        c.CreatedAt.UTC(),
	}
}
