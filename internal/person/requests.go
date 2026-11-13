package person

import "mime/multipart"

// CreatePersonRequest represents request payload to create a new person.
type CreatePersonRequest struct {
	// PersonID is the external business key. It is optional: if omitted or empty,
	// it is left unset (NULL) without falling back to the internal primary key.
	PersonID *string `json:"personId" binding:"omitempty,max=64"`
	Name     string  `json:"name" binding:"required,min=1,max=64"`
}

// UpdatePersonRequest represents request payload to update person metadata.
type UpdatePersonRequest struct {
	// PersonID is accepted only so that an attempt to change it fails loudly instead
	// of being silently dropped by the JSON decoder. Supplying a different value is
	// rejected with PERSON_ID_IMMUTABLE.
	PersonID *string `json:"personId,omitempty" binding:"omitempty,max=64"`
	// Name is a pointer so that "not provided" (nil) stays distinguishable from
	// "provided": an explicitly empty name must be rejected rather than ignored.
	Name *string `json:"name,omitempty" binding:"omitempty,min=1,max=64"`
}

// AddFaceRequest represents the multipart form payload to enroll a face template.
type AddFaceRequest struct {
	File *multipart.FileHeader `form:"file" binding:"required"`
}

// SetPrimaryFaceRequest represents the payload to set the primary face for a person.
type SetPrimaryFaceRequest struct {
	FaceID string `json:"faceId" binding:"required"`
}

// CreateFaceImportJobRequest represents the multipart form payload to initiate an asynchronous face import job.
type CreateFaceImportJobRequest struct {
	File *multipart.FileHeader `form:"file" binding:"required"`
}

// CreateFaceReextractJobRequest represents the optional scope payload to start an
// asynchronous face feature re-extraction job. Empty/omitted personIds targets the
// entire enrolled gallery.
type CreateFaceReextractJobRequest struct {
	PersonIDs []string `json:"personIds,omitempty"`
}

// ImageSearchRequest represents the multipart form payload for 1:N facial search.
type ImageSearchRequest struct {
	Image     *multipart.FileHeader `form:"image" binding:"required"`
	Threshold *float32              `form:"threshold"`
	TopK      *int                  `form:"topK"`
}
