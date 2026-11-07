package asyncjob

import (
	"encoding/json"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// JobResponse represents the HTTP API response projection of a background job.
//
// The raw metadata payload is deliberately not part of this contract: it holds
// server-side staging paths that a client has no business reading. The result
// payload is exposed because it is the job's own summary.
type JobResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Status       JobStatus      `json:"status"`
	Progress     int            `json:"progress"`
	Total        int            `json:"total"`
	Processed    int            `json:"processed"`
	Succeeded    int            `json:"succeeded"`
	Failed       int            `json:"failed"`
	CurrentStage JobStage       `json:"currentStage,omitempty"`
	CurrentFile  string         `json:"currentFile,omitempty"`
	Result       map[string]any `json:"result"`
	ErrorCode    string         `json:"errorCode,omitempty"`
	ErrorMessage string         `json:"errorMessage,omitempty"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	FinishedAt   *time.Time     `json:"finishedAt,omitempty"`
}

// CancelJobResponse represents the acknowledgement payload for a cancellation request.
type CancelJobResponse struct {
	Cancelled bool `json:"cancelled"`
}

// toJobResponse projects a persisted job into the API contract.
//
// Every absolute timestamp is normalized to UTC field by field: SQLite stores
// UTC but the driver's Location is not part of the contract, so leaving it to
// chance would let a client parse an offset that contradicts the documented Z.
func toJobResponse(j *Job, locale string) JobResponse {
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
		CurrentStage: JobStage(j.CurrentStage),
		CurrentFile:  j.CurrentFile,
		Result:       resultPayload(j.ResultJSON),
		ErrorCode:    j.ErrorCode,
		ErrorMessage: jobErrorMessage(j, locale),
		CreatedAt:    j.CreatedAt.UTC(),
		UpdatedAt:    j.UpdatedAt.UTC(),
		FinishedAt:   finishedAt,
	}
}

func toJobResponses(jobs []Job, locale string) []JobResponse {
	res := make([]JobResponse, len(jobs))
	for i := range jobs {
		res[i] = toJobResponse(&jobs[i], locale)
	}
	return res
}

// jobErrorMessage renders the stored machine code through the catalog. Rows
// written before error codes existed only carry free text; that text is still
// returned rather than replaced with a guess.
func jobErrorMessage(j *Job, locale string) string {
	if j.ErrorCode != "" {
		if localized, ok := httputil.LookupMessage(j.ErrorCode, locale); ok && localized != "" {
			return localized
		}
	}
	return j.ErrorMessage
}

// resultPayload guarantees the field is a JSON object even for rows whose
// stored payload is empty or corrupt. Swagger also documents a real object
// schema, which json.RawMessage cannot express.
func resultPayload(raw string) map[string]any {
	payload := map[string]any{}
	if raw == "" {
		return payload
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload == nil {
		return map[string]any{}
	}
	return payload
}
