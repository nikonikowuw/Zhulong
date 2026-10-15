package person

import (
	"mime/multipart"
	"time"
)

// Audit actions for person-domain operations. Namespaced as <domain>.<resource>.<verb> to
// match the rest of the codebase (audit/model.go, systemtime, storage): the audit log is
// queried by exact action string, so bare verbs like "CREATE" cannot be grouped or
// filtered alongside the other domains.
const (
	ActionPersonCreate         = "person.create"
	ActionPersonUpdate         = "person.update"
	ActionPersonDelete         = "person.delete"
	ActionPersonFaceAdd        = "person.face.add"
	ActionPersonFaceDelete     = "person.face.delete"
	ActionPersonPrimaryFaceSet = "person.primary_face.set"
)

// Person represents a personnel profile entity in the personnel database.
type Person struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`
	// PersonID is the external business key consumed by third-party systems. It is
	// optional, unique when assigned, and immutable once set.
	PersonID      *string   `gorm:"column:person_id" json:"personId"`
	Name          string    `gorm:"column:name;not null" json:"name"`
	PrimaryFaceID *string   `gorm:"column:primary_face_id" json:"primaryFaceId,omitempty"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
	// Faces is a read-only projection populated by Preload. Write paths MUST go
	// through AddFace / DeleteFace; never persist a Person with a populated
	// Faces slice (see Store.UpdatePerson). The CASCADE below is declarative
	// only — the effective constraint lives in the persons_tables migration.
	Faces []Face `gorm:"foreignKey:OwnerRowID;references:ID;constraint:OnDelete:CASCADE" json:"faces,omitempty"`
}

// TableName sets the table name for Person.
func (Person) TableName() string {
	return "persons"
}

// Face represents an enrolled facial template belonging to a Person (1:N model).
type Face struct {
	ID string `gorm:"primaryKey;column:id" json:"id"`
	// OwnerRowID is the internal persons.id of the owning record. It deliberately does
	// NOT serialize as "personId": Person.PersonID already owns that JSON name and means
	// the external business key, so sharing it made the two identifiers indistinguishable
	// to API clients. The owning person is already implied by the enclosing resource.
	OwnerRowID    string    `gorm:"column:person_id;not null;index" json:"ownerRowId"`
	ImagePath     string    `gorm:"column:image_path;not null;default:''" json:"imagePath"`
	FeatureVector []byte    `gorm:"column:feature_vector;not null" json:"-"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName sets the table name for Face.
func (Face) TableName() string {
	return "person_faces"
}

// CreatePersonRequest represents request payload to create a new person.
type CreatePersonRequest struct {
	// PersonID is the external business key. It is optional: if omitted or empty,
	// it is left unset (NULL) without falling back to the internal primary key.
	PersonID string `json:"personId"`
	Name     string `json:"name" binding:"required"`
}

// UpdatePersonRequest represents request payload to update person metadata.
type UpdatePersonRequest struct {
	// PersonID is accepted only so that an attempt to change it fails loudly instead
	// of being silently dropped by the JSON decoder. Supplying a different value is
	// rejected with PERSON_ID_IMMUTABLE.
	PersonID *string `json:"personId,omitempty"`
	Name     *string `json:"name,omitempty"`
}

// AddFaceRequest represents the multipart form payload to enroll a face template.
type AddFaceRequest struct {
	File *multipart.FileHeader `form:"file" binding:"required"`
}

// SetPrimaryFaceRequest represents the payload to set the primary face for a person.
type SetPrimaryFaceRequest struct {
	FaceID string `json:"faceId" binding:"required"`
}

// GallerySyncItem represents a serialized face embedding item for algorithm runner IPC.
type GallerySyncItem struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}

// GalleryUpsertPayload represents an incremental enrollment update for algorithm runner IPC.
type GalleryUpsertPayload struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}
