package person

import "time"

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
//
// Person is a persistence model, not an API contract: every field is tagged
// `json:"-"` so a Person can never be serialized to a client by accident. The
// HTTP projection lives in PersonResponse (responses.go) and the inbound
// payloads in requests.go.
type Person struct {
	ID string `gorm:"primaryKey;column:id" json:"-"`
	// PersonID is the external business key consumed by third-party systems. It is
	// optional, unique when assigned, and immutable once set.
	PersonID      *string   `gorm:"column:person_id" json:"-"`
	Name          string    `gorm:"column:name;not null" json:"-"`
	PrimaryFaceID *string   `gorm:"column:primary_face_id" json:"-"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"-"`
	UpdatedAt     time.Time `gorm:"column:updated_at;not null" json:"-"`
	// Faces is a read-only projection populated by Preload. Write paths MUST go
	// through AddFace / DeleteFace; never persist a Person with a populated
	// Faces slice (see Store.UpdatePerson). The CASCADE below is declarative
	// only — the effective constraint lives in the persons_tables migration.
	Faces []Face `gorm:"foreignKey:PersonRowID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// TableName sets the table name for Person.
func (Person) TableName() string {
	return "persons"
}

// Face represents an enrolled facial template belonging to a Person (1:N model).
//
// Like Person, Face is a persistence model and never serialized directly; see
// FaceResponse for the API projection.
type Face struct {
	ID string `gorm:"primaryKey;column:id" json:"-"`
	// PersonRowID is the internal persons.id of the owning record, persisted in the
	// person_faces.person_id column. It is deliberately named for the internal row
	// id rather than reusing "PersonID": Person.PersonID already means the external
	// business key, and sharing the name made the two identifiers
	// indistinguishable. The owning person is also implied by the enclosing
	// resource path, which is why the API exposes it only as ownerRowId.
	PersonRowID   string    `gorm:"column:person_id;not null;index" json:"-"`
	ImagePath     string    `gorm:"column:image_path;not null;default:''" json:"-"`
	FeatureVector []byte    `gorm:"column:feature_vector;not null" json:"-"`
	CreatedAt     time.Time `gorm:"column:created_at;not null" json:"-"`
}

// TableName sets the table name for Face.
func (Face) TableName() string {
	return "person_faces"
}

// GalleryItem is one face-gallery entry as published to the algorithm runner over
// IPC. The same shape carries both the cold-start snapshot (GetGallerySyncData)
// and incremental changes (GalleryNotifier.BroadcastUpsert). Its snake_case JSON
// names are the IPC wire contract and deliberately differ from the camelCase
// names used by the HTTP API.
type GalleryItem struct {
	FaceID     string    `json:"face_id"`
	PersonID   string    `json:"person_id"`
	PersonName string    `json:"person_name"`
	ImagePath  string    `json:"image_path,omitempty"`
	Features   []float32 `json:"features,omitempty"`
}
