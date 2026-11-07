package person

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
)

// PersonResponse is the HTTP projection of a Person.
//
// Handlers return this (or FaceResponse) instead of the GORM models so that
// persistence concerns — the 2048-byte feature vector, gorm tags, and the
// internal column layout — can never reach an API client.
type PersonResponse struct {
	ID            string         `json:"id"`
	PersonID      *string        `json:"personId"`
	Name          string         `json:"name"`
	Source        string         `json:"source"`
	SourceName    string         `json:"sourceName"`
	PrimaryFaceID *string        `json:"primaryFaceId,omitempty"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	Faces         []FaceResponse `json:"faces,omitempty"`
}

// FaceResponse is the HTTP projection of an enrolled Face template.
type FaceResponse struct {
	ID         string    `json:"id"`
	OwnerRowID string    `json:"ownerRowId"`
	ImagePath  string    `json:"imagePath"`
	CreatedAt  time.Time `json:"createdAt"`
}

// newFaceResponse projects a persisted Face onto its API contract. Timestamps
// are normalized to UTC so they always serialize with a trailing Z.
func newFaceResponse(f Face) FaceResponse {
	return FaceResponse{
		ID:         f.ID,
		OwnerRowID: f.PersonRowID,
		ImagePath:  f.ImagePath,
		CreatedAt:  f.CreatedAt.UTC(),
	}
}

// newFaceResponses projects a face list. It always returns a non-nil slice so an
// empty list serializes as [] rather than null.
func newFaceResponses(faces []Face) []FaceResponse {
	out := make([]FaceResponse, 0, len(faces))
	for _, f := range faces {
		out = append(out, newFaceResponse(f))
	}
	return out
}

// newPersonResponse projects a persisted Person onto its API contract.
func newPersonResponse(p *Person) PersonResponse {
	resp := PersonResponse{
		ID:            p.ID,
		PersonID:      p.PersonID,
		Name:          p.Name,
		Source:        p.Source,
		SourceName:    p.SourceName,
		PrimaryFaceID: p.PrimaryFaceID,
		CreatedAt:     p.CreatedAt.UTC(),
		UpdatedAt:     p.UpdatedAt.UTC(),
	}
	if len(p.Faces) > 0 {
		resp.Faces = newFaceResponses(p.Faces)
	}
	return resp
}

// newPersonResponses projects a person list. It always returns a non-nil slice
// so an empty page serializes as [] rather than null.
func newPersonResponses(persons []Person) []PersonResponse {
	out := make([]PersonResponse, 0, len(persons))
	for i := range persons {
		out = append(out, newPersonResponse(&persons[i]))
	}
	return out
}

// PersonImportIssueResponse identifies one row validation problem.
type PersonImportIssueResponse struct {
	Field string `json:"field,omitempty"`
	Code  string `json:"code"`
}

// PersonImportRowResponse reports the outcome for one source CSV record.
type PersonImportRowResponse struct {
	Line      int                         `json:"line"`
	Name      string                      `json:"name"`
	PersonID  string                      `json:"personId,omitempty"`
	CanImport bool                        `json:"canImport"`
	Imported  bool                        `json:"imported,omitempty"`
	Issues    []PersonImportIssueResponse `json:"issues"`
}

// PersonImportPreviewResponse contains the validation preview without writing data.
type PersonImportPreviewResponse struct {
	Items      []PersonImportRowResponse `json:"items"`
	Total      int                       `json:"total"`
	Importable int                       `json:"importable"`
	Failed     int                       `json:"failed"`
}

// PersonImportCommitResponse summarizes a confirmed import.
type PersonImportCommitResponse struct {
	Items    []PersonImportRowResponse `json:"items"`
	Total    int                       `json:"total"`
	Imported int                       `json:"imported"`
	Failed   int                       `json:"failed"`
}

// FaceImportJobResponse represents the HTTP status projection of an asynchronous face import job.
type FaceImportJobResponse struct {
	JobID        string             `json:"jobId"`
	Status       asyncjob.JobStatus `json:"status"`
	Progress     int                `json:"progress"`
	Total        int                `json:"total"`
	Processed    int                `json:"processed"`
	Succeeded    int                `json:"succeeded"`
	Failed       int                `json:"failed"`
	CurrentStage string             `json:"currentStage,omitempty"`
	CurrentFile  string             `json:"currentFile,omitempty"`
	ErrorCode    string             `json:"errorCode,omitempty"`
	ErrorMessage string             `json:"errorMessage,omitempty"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
	FinishedAt   *time.Time         `json:"finishedAt,omitempty"`
}

func newFaceImportJobResponse(job *asyncjob.Job, locale string) FaceImportJobResponse {
	var finishedAt *time.Time
	if job.FinishedAt != nil {
		utc := job.FinishedAt.UTC()
		finishedAt = &utc
	}
	errorMessage := job.ErrorMessage
	if job.ErrorCode != "" {
		if localized, ok := httputil.LookupMessage(job.ErrorCode, locale); ok && localized != "" {
			errorMessage = localized
		}
	}
	return FaceImportJobResponse{
		JobID:        job.ID,
		Status:       job.Status,
		Progress:     job.Progress,
		Total:        job.Total,
		Processed:    job.Processed,
		Succeeded:    job.Succeeded,
		Failed:       job.Failed,
		CurrentStage: job.CurrentStage,
		CurrentFile:  job.CurrentFile,
		ErrorCode:    job.ErrorCode,
		ErrorMessage: errorMessage,
		CreatedAt:    job.CreatedAt.UTC(),
		UpdatedAt:    job.UpdatedAt.UTC(),
		FinishedAt:   finishedAt,
	}
}

// ImageSearchMatchInfo represents a candidate person hit in an image-based facial search.
type ImageSearchMatchInfo struct {
	PersonID         string  `json:"personId"`
	PersonExternalID *string `json:"personExternalId"`
	PersonName       string  `json:"personName"`
	MatchedFaceID    string  `json:"matchedFaceId"`
	Similarity       float32 `json:"similarity"`
}

// ImageSearchResponse is the response payload for 1:N facial search.
type ImageSearchResponse struct {
	Matches []ImageSearchMatchInfo `json:"matches"`
}
