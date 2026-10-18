package person

import "time"

// PersonResponse is the HTTP projection of a Person.
//
// Handlers return this (or FaceResponse) instead of the GORM models so that
// persistence concerns — the 2048-byte feature vector, gorm tags, and the
// internal column layout — can never reach an API client.
type PersonResponse struct {
	ID            string         `json:"id"`
	PersonID      *string        `json:"personId"`
	Name          string         `json:"name"`
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
