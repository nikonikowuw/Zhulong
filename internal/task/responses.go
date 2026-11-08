package task

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/region"
)

// TaskAlgorithmInfo represents algorithm configuration details nested in task responses.
type TaskAlgorithmInfo struct {
	ID               string    `json:"id"`
	TaskID           string    `json:"taskId"`
	AlgorithmID      string    `json:"algorithmId"`
	Enabled          bool      `json:"enabled"`
	SampleFPS        int       `json:"sampleFps"`
	ConfigJSON       string    `json:"configJson"`
	RegionConfigJSON string    `json:"regionConfigJson"`
	ScheduleJSON     string    `json:"scheduleJson"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

// TaskResponse represents an analysis task in API responses.
type TaskResponse struct {
	ID       string `json:"id"`
	CameraID string `json:"cameraId"`
	Enabled  bool   `json:"enabled"`
	Name     string `json:"name"`
	// Crop 是任务级前置裁剪窗口；null 表示不裁剪。
	Crop       *region.Rect        `json:"crop"`
	CreatedAt  time.Time           `json:"createdAt"`
	UpdatedAt  time.Time           `json:"updatedAt"`
	Algorithms []TaskAlgorithmInfo `json:"algorithms"`
}

// DeleteTaskResponse represents the confirmation payload returned when a task is deleted.
type DeleteTaskResponse struct {
	Deleted bool `json:"deleted"`
}

// ToResponse converts a Task entity to its public API response.
func (t *Task) ToResponse() TaskResponse {
	if t == nil {
		return TaskResponse{}
	}
	algorithms := make([]TaskAlgorithmInfo, 0, len(t.Algorithms))
	for i := range t.Algorithms {
		algorithms = append(algorithms, toTaskAlgorithmInfo(&t.Algorithms[i]))
	}
	return TaskResponse{
		ID:         t.ID,
		CameraID:   t.CameraID,
		Enabled:    t.Enabled,
		Name:       t.Name,
		Crop:       responseCrop(t.CropJSON),
		CreatedAt:  t.CreatedAt.UTC(),
		UpdatedAt:  t.UpdatedAt.UTC(),
		Algorithms: algorithms,
	}
}

// ToTaskResponses converts a slice of Task entities to public API responses.
func ToTaskResponses(tasks []Task) []TaskResponse {
	responses := make([]TaskResponse, 0, len(tasks))
	for i := range tasks {
		responses = append(responses, tasks[i].ToResponse())
	}
	return responses
}

// toTaskAlgorithmInfo converts a TaskAlgorithm entity to its public API projection.
func toTaskAlgorithmInfo(sub *TaskAlgorithm) TaskAlgorithmInfo {
	return TaskAlgorithmInfo{
		ID:               sub.ID,
		TaskID:           sub.TaskID,
		AlgorithmID:      sub.AlgorithmID,
		Enabled:          sub.Enabled,
		SampleFPS:        sub.SampleFPS,
		ConfigJSON:       sub.ConfigJSON,
		RegionConfigJSON: sub.RegionConfigJSON,
		ScheduleJSON:     sub.ScheduleJSON,
		CreatedAt:        sub.CreatedAt.UTC(),
		UpdatedAt:        sub.UpdatedAt.UTC(),
	}
}

// responseCrop 把存储形态解析回响应字段；解析失败按 null 返回，不阻断读取。
//
// 非法配置的可见性由 Service 的读路径告警负责（warnInvalidCrop），投影层保持纯函数。
func responseCrop(raw string) *region.Rect {
	crop, err := region.ParseCrop(raw)
	if err != nil {
		return nil
	}
	return crop
}
