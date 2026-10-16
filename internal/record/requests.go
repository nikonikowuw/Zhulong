package record

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// ListRecognitionsQuery defines query parameters for listing recognitions.
type ListRecognitionsQuery struct {
	httputil.PaginationQuery
	CameraID  string `form:"cameraId"`
	PersonID  string `form:"personId"`
	Matched   *bool  `form:"matched"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// ToFilter converts query parameters into a validated RecognitionFilter.
func (q *ListRecognitionsQuery) ToFilter() (RecognitionFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return RecognitionFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return RecognitionFilter{}, err
	}
	return RecognitionFilter{
		CameraID:  q.CameraID,
		PersonID:  q.PersonID,
		Matched:   q.Matched,
		StartTime: start,
		EndTime:   end,
		Page:      page,
		PageSize:  pageSize,
	}, nil
}

// ListAlarmsQuery defines query parameters for listing alarms.
type ListAlarmsQuery struct {
	httputil.PaginationQuery
	CameraID  string `form:"cameraId"`
	AlarmType string `form:"alarmType"`
	StartTime string `form:"startTime"`
	EndTime   string `form:"endTime"`
}

// ToFilter converts query parameters into a validated AlarmFilter.
func (q *ListAlarmsQuery) ToFilter() (AlarmFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return AlarmFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return AlarmFilter{}, err
	}
	return AlarmFilter{
		CameraID:  q.CameraID,
		AlarmType: q.AlarmType,
		StartTime: start,
		EndTime:   end,
		Page:      page,
		PageSize:  pageSize,
	}, nil
}

// ListCapturesQuery defines query parameters for listing captures.
type ListCapturesQuery struct {
	httputil.PaginationQuery
	CameraID   string `form:"cameraId"`
	TargetType string `form:"targetType"`
	StartTime  string `form:"startTime"`
	EndTime    string `form:"endTime"`
}

// ToFilter converts query parameters into a validated CaptureFilter.
func (q *ListCapturesQuery) ToFilter() (CaptureFilter, error) {
	page, pageSize, _, _ := q.Normalize()
	start, err := parseTime(q.StartTime, "startTime")
	if err != nil {
		return CaptureFilter{}, err
	}
	end, err := parseTime(q.EndTime, "endTime")
	if err != nil {
		return CaptureFilter{}, err
	}
	return CaptureFilter{
		CameraID:   q.CameraID,
		TargetType: q.TargetType,
		StartTime:  start,
		EndTime:    end,
		Page:       page,
		PageSize:   pageSize,
	}, nil
}

func parseTime(raw, fieldName string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, httputil.NewValidationError("Validation failed", []httputil.FieldDetail{
			{Field: fieldName, Code: "INVALID_TIME_FORMAT", Message: fieldName + " must be in RFC3339 format"},
		})
	}
	utc := t.UTC()
	return &utc, nil
}
