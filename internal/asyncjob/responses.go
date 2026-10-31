package asyncjob

import "time"

// JobResponse represents the HTTP API response projection of a background job.
type JobResponse struct {
	ID           string     `json:"id"`
	Type         string     `json:"type"`
	Status       JobStatus  `json:"status"`
	Progress     int        `json:"progress"`
	Total        int        `json:"total"`
	Processed    int        `json:"processed"`
	Succeeded    int        `json:"succeeded"`
	Failed       int        `json:"failed"`
	CurrentStage string     `json:"currentStage,omitempty"`
	CurrentFile  string     `json:"currentFile,omitempty"`
	MetadataJSON string     `json:"metadata"`
	ResultJSON   string     `json:"result"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	FinishedAt   *time.Time `json:"finishedAt,omitempty"`
}

// CancelJobResponse represents the acknowledgement payload for a cancellation request.
type CancelJobResponse struct {
	Cancelled bool `json:"cancelled"`
}

func toJobResponse(j *Job) JobResponse {
	if j == nil {
		return JobResponse{}
	}
	var finishedAt *time.Time
	if j.FinishedAt != nil {
		utc := j.FinishedAt.UTC()
		finishedAt = &utc
	}
	return JobResponse{
		ID:           j.ID,
		Type:         j.Type,
		Status:       j.Status,
		Progress:     j.Progress,
		Total:        j.Total,
		Processed:    j.Processed,
		Succeeded:    j.Succeeded,
		Failed:       j.Failed,
		CurrentStage: j.CurrentStage,
		CurrentFile:  j.CurrentFile,
		MetadataJSON: j.MetadataJSON,
		ResultJSON:   j.ResultJSON,
		ErrorMessage: j.ErrorMessage,
		CreatedAt:    j.CreatedAt.UTC(),
		UpdatedAt:    j.UpdatedAt.UTC(),
		FinishedAt:   finishedAt,
	}
}

func toJobResponses(jobs []Job) []JobResponse {
	if len(jobs) == 0 {
		return []JobResponse{}
	}
	res := make([]JobResponse, len(jobs))
	for i := range jobs {
		res[i] = toJobResponse(&jobs[i])
	}
	return res
}
