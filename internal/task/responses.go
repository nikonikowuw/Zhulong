package task

import "time"

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
	ID         string              `json:"id"`
	CameraID   string              `json:"cameraId"`
	Enabled    bool                `json:"enabled"`
	Name       string              `json:"name"`
	CreatedAt  time.Time           `json:"createdAt"`
	UpdatedAt  time.Time           `json:"updatedAt"`
	Algorithms []TaskAlgorithmInfo `json:"algorithms"`
}

// toTaskAlgorithmInfo converts a TaskAlgorithm entity to its public API projection.
func toTaskAlgorithmInfo(sub TaskAlgorithm) TaskAlgorithmInfo {
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

// ToResponse converts a Task entity to its public API response.
func (t *Task) ToResponse() TaskResponse {
	if t == nil {
		return TaskResponse{}
	}
	algorithms := make([]TaskAlgorithmInfo, 0, len(t.Algorithms))
	for _, sub := range t.Algorithms {
		algorithms = append(algorithms, toTaskAlgorithmInfo(sub))
	}
	return TaskResponse{
		ID:         t.ID,
		CameraID:   t.CameraID,
		Enabled:    t.Enabled,
		Name:       t.Name,
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
