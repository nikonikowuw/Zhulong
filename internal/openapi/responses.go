package openapi

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/person"
)

// PersonFaceInfo represents the face summary nested inside PersonProvisionResponse.
type PersonFaceInfo struct {
	FaceID    string    `json:"faceId"`
	ImagePath string    `json:"imagePath"`
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
			ImagePath: f.ImagePath,
			IsPrimary: isPrimary,
			CreatedAt: f.CreatedAt,
		})
	}
	return PersonProvisionResponse{
		PersonID:      personKey,
		Name:          p.Name,
		PrimaryFaceID: p.PrimaryFaceID,
		Faces:         faces,
		CreatedAt:     p.CreatedAt,
		UpdatedAt:     p.UpdatedAt,
	}
}
