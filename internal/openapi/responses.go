package openapi

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/person"
)

// PersonFaceInfo is one enrolled face as the machine API describes it.
//
// It deliberately omits the stored image path: the on-disk name encodes the internal
// person row id, and this API exposes no endpoint that would let a caller read the
// photo back. Publishing it would leak an internal identifier for no consumer's benefit.
type PersonFaceInfo struct {
	FaceID    string    `json:"faceId"`
	IsPrimary bool      `json:"isPrimary"`
	CreatedAt time.Time `json:"createdAt"`
}

// PersonProvisionResponse describes the complete state of a provisioned person and their enrolled faces.
type PersonProvisionResponse struct {
	PersonID      string           `json:"personId"`
	Name          string           `json:"name"`
	PrimaryFaceID *string          `json:"primaryFaceId"`
	Faces         []PersonFaceInfo `json:"faces"`
	CreatedAt     time.Time        `json:"createdAt"`
	UpdatedAt     time.Time        `json:"updatedAt"`
}

// FaceUploadResponse represents the result of a single face upload or replacement.
type FaceUploadResponse struct {
	FaceID    string    `json:"faceId"`
	Replaced  bool      `json:"replaced"`
	IsPrimary bool      `json:"isPrimary"`
	CreatedAt time.Time `json:"createdAt"`
}

// FaceClearResponse represents the result of clearing all faces for a person.
type FaceClearResponse struct {
	Cleared int `json:"cleared"`
}

// DeleteResponse represents the standard response for deletion operations.
type DeleteResponse struct {
	Deleted bool `json:"deleted"`
}

// newPersonProvisionResponse projects a persisted person onto the machine API contract.
//
// Timestamps are normalized to UTC so they always serialize with a trailing Z: the
// stored location is whatever the driver hands back, and an offset-bearing timestamp
// would silently break clients parsing the documented RFC3339 UTC form.
func newPersonProvisionResponse(p *person.Person) PersonProvisionResponse {
	personKey := ""
	if p.PersonID != nil {
		personKey = *p.PersonID
	}
	faces := make([]PersonFaceInfo, 0, len(p.Faces))
	for _, f := range p.Faces {
		isPrimary := p.PrimaryFaceID != nil && *p.PrimaryFaceID == f.ID
		faces = append(faces, PersonFaceInfo{
			FaceID:    f.ID,
			IsPrimary: isPrimary,
			CreatedAt: f.CreatedAt.UTC(),
		})
	}
	return PersonProvisionResponse{
		PersonID:      personKey,
		Name:          p.Name,
		PrimaryFaceID: p.PrimaryFaceID,
		Faces:         faces,
		CreatedAt:     p.CreatedAt.UTC(),
		UpdatedAt:     p.UpdatedAt.UTC(),
	}
}
