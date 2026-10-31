package person

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrNotFound       = errors.New("person or face not found")
	ErrDatabaseClosed = errors.New("database connection is not available")
	ErrDuplicateID    = errors.New("person with this ID already exists")
	// ErrDuplicatePersonID reports a collision on the external business key, which is
	// distinct from ErrDuplicateID so callers can name the offending field.
	ErrDuplicatePersonID = errors.New("person with this person_id already exists")
)

// clampPagination applies the shared page bounds as a second line of defence
// for callers that reach the store without going through a handler.
//
// 边界值直接引用 httputil 的常量而不是本地再抄一份：两处各写一份 20/100，
// 一边改了另一边不动就会静默漂移。
func clampPagination(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = httputil.DefaultPageSize
	} else if limit > httputil.MaxPageSize {
		limit = httputil.MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// selectFaceSummary loads only the columns a face list needs. It deliberately
// omits feature_vector: every vector is faceFeatureDim*4 bytes, and list views
// render thumbnails from image_path alone.
func selectFaceSummary(db *gorm.DB) *gorm.DB {
	return db.Select("id", "person_id", "image_path", "created_at")
}

// Store handles SQLite persistence for the personnel and face gallery domain.
type Store struct {
	dbProvider database.DBProvider
}

// NewStore creates a new Store instance.
func NewStore(dbProvider database.DBProvider) *Store {
	return &Store{dbProvider: dbProvider}
}

func (s *Store) db(ctx context.Context) (*gorm.DB, error) {
	if s.dbProvider == nil {
		return nil, ErrDatabaseClosed
	}
	db := s.dbProvider.DB()
	if db == nil {
		return nil, ErrDatabaseClosed
	}
	return db.WithContext(ctx), nil
}

// CreatePerson inserts a new Person record. It reports ErrDuplicateID or
// ErrDuplicatePersonID instead of surfacing a raw UNIQUE constraint error.
func (s *Store) CreatePerson(ctx context.Context, p *Person) error {
	if p == nil {
		return errors.New("person is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	} else {
		p.CreatedAt = p.CreatedAt.UTC()
	}
	if p.UpdatedAt.IsZero() {
		p.UpdatedAt = now
	} else {
		p.UpdatedAt = p.UpdatedAt.UTC()
	}
	// DO NOTHING keeps the duplicate-key detection inside SQLite (RowsAffected == 0)
	// without enabling GORM's global TranslateError option or matching error strings.
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(p)
	if res.Error != nil {
		return fmt.Errorf("create person: %w", res.Error)
	}
	if res.RowsAffected == 0 {
		// SQLite's unqualified ON CONFLICT DO NOTHING swallows every uniqueness
		// violation, not just the primary key, so the conflict must be attributed by
		// probing. persons has exactly two unique constraints: id and person_id.
		var existing int64
		if err := db.Model(&Person{}).Where("id = ?", p.ID).Count(&existing).Error; err != nil {
			return fmt.Errorf("resolve person conflict: %w", err)
		}
		if existing > 0 {
			return ErrDuplicateID
		}
		return ErrDuplicatePersonID
	}
	return nil
}

// ListPersonIDs returns the external keys from the supplied candidates that already exist.
func (s *Store) ListPersonIDs(ctx context.Context, candidates []string) (map[string]struct{}, error) {
	if len(candidates) == 0 {
		return map[string]struct{}{}, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var personIDs []string
	if err := db.Model(&Person{}).
		Where("person_id IN ?", candidates).
		Pluck("person_id", &personIDs).Error; err != nil {
		return nil, fmt.Errorf("list existing person ids: %w", err)
	}
	existing := make(map[string]struct{}, len(personIDs))
	for _, personID := range personIDs {
		existing[personID] = struct{}{}
	}
	return existing, nil
}

// CreatePersonImport inserts valid rows in one transaction and returns uniqueness conflicts per row.
func (s *Store) CreatePersonImport(ctx context.Context, entries []personImportEntry) ([]personImportCreateResult, error) {
	results := make([]personImportCreateResult, 0, len(entries))
	if len(entries) == 0 {
		return results, nil
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		for _, entry := range entries {
			p := &Person{
				ID:       idutil.New(),
				PersonID: entry.personID,
				Name:     entry.name,
			}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(p)
			if res.Error != nil {
				return fmt.Errorf("create imported person: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				if entry.personID == nil {
					return ErrDuplicateID
				}
				var count int64
				if err := tx.Model(&Person{}).Where("person_id = ?", *entry.personID).Count(&count).Error; err != nil {
					return fmt.Errorf("resolve imported person conflict: %w", err)
				}
				if count == 0 {
					return ErrDuplicateID
				}
				results = append(results, personImportCreateResult{line: entry.line, err: ErrDuplicatePersonID})
				continue
			}
			results = append(results, personImportCreateResult{line: entry.line})
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("create person import: %w", err)
	}
	return results, nil
}

// GetPerson fetches a person by ID along with all associated Face templates.
//
// The preload is narrowed to the summary columns: no caller of GetPerson needs a
// feature vector. The gallery paths that do (GetGallerySyncData, the re-publish
// after a rename) read them through ListPersonsForGallery / ListFacesByPerson.
func (s *Store) GetPerson(ctx context.Context, id string) (*Person, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var p Person
	if err := db.Preload("Faces", selectFaceSummary).Where("id = ?", id).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetPersonMeta fetches a person's own row without preloading Faces.
//
// Use it on paths that need only the person's scalar columns — the update merge and the
// existence checks behind AddFace / ListFaces — so they do not pay to load every
// feature vector belonging to that person.
func (s *Store) GetPersonMeta(ctx context.Context, id string) (*Person, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var p Person
	if err := db.Where("id = ?", id).First(&p).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListPersons returns paginated persons with associated Face templates, optionally filtered by keyword.
func (s *Store) ListPersons(ctx context.Context, limit, offset int, search string) ([]Person, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := db.Model(&Person{})
	search = strings.TrimSpace(search)
	if search != "" {
		// The keyword is data, not a pattern: % and _ must match themselves instead of
		// turning a search for "50%" into a full-table wildcard.
		likePattern := "%" + escapeLike(search) + "%"
		query = query.Where(`name LIKE ? ESCAPE '\' OR (person_id IS NOT NULL AND person_id LIKE ? ESCAPE '\')`,
			likePattern, likePattern)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count persons: %w", err)
	}

	limit, offset = clampPagination(limit, offset)

	var list []Person
	if err := query.Preload("Faces", selectFaceSummary).Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// escapeLike neutralises the LIKE metacharacters of a user-supplied keyword.
//
// The escape character is the backslash declared by the callers' ESCAPE clause, so a
// backslash in the input has to be escaped first; doing it in the other order would
// double-escape the metacharacters added for % and _.
func escapeLike(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`%`, `\%`,
		`_`, `\_`,
	)
	return r.Replace(s)
}

// ListPersonsForGallery returns one page of persons with every face template, feature
// vectors included. Gallery sync walks it page by page instead of loading the whole
// gallery in one query.
//
// The page bound of clampPagination deliberately does not apply: this reader is an
// internal iterator, not a client-facing page, and silently capping it would truncate
// the gallery that the caller believes it has read in full.
func (s *Store) ListPersonsForGallery(ctx context.Context, limit, offset int) ([]Person, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = httputil.DefaultPageSize
	}
	if offset < 0 {
		offset = 0
	}
	var list []Person
	if err := db.Preload("Faces").Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// UpdatePerson updates an existing person's mutable scalar columns.
//
// person_id is deliberately absent from the UPDATE: it is the external business key
// and is immutable once created (Service.UpdatePerson rejects any attempt to change it).
//
// It deliberately uses Model().Where().Updates() rather than db.Save(p):
//   - Save(p) also upserts the Faces association, which resurrects
//     concurrently deleted face rows and rewrites their person_id from a
//     stale snapshot;
//   - Save(p) silently degrades to an INSERT when RowsAffected == 0, so a
//     missing person was re-created instead of reporting ErrNotFound.
func (s *Store) UpdatePerson(ctx context.Context, p *Person) error {
	if p == nil {
		return errors.New("person is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"name":       p.Name,
		"updated_at": now,
	}
	if p.PersonID != nil && *p.PersonID != "" {
		updates["person_id"] = gorm.Expr("CASE WHEN person_id IS NULL OR person_id = '' THEN ? ELSE person_id END", *p.PersonID)
	}
	res := db.Model(&Person{}).Where("id = ?", p.ID).Updates(updates)
	if res.Error != nil {
		// Attribute a uniqueness violation by re-reading who owns the business key
		// rather than by matching the driver's error text, which is not part of any
		// stable contract. The CASE above only ever writes person_id when the row
		// currently has none, so a conflicting owner can only mean a real collision.
		if p.PersonID != nil && *p.PersonID != "" {
			var owners int64
			probeErr := db.Model(&Person{}).
				Where("person_id = ? AND id <> ?", *p.PersonID, p.ID).
				Count(&owners).Error
			if probeErr == nil && owners > 0 {
				return ErrDuplicatePersonID
			}
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	p.UpdatedAt = now
	return nil
}

// DeletePerson deletes a person and cascades deletion to all their face templates.
func (s *Store) DeletePerson(ctx context.Context, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	res := db.Where("id = ?", id).Delete(&Person{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// AddFace inserts a new Face template for a person. If the person has no primary
// face assigned, the new face is automatically designated as primary.
func (s *Store) AddFace(ctx context.Context, face *Face) error {
	if face == nil {
		return errors.New("face is nil")
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	if face.CreatedAt.IsZero() {
		face.CreatedAt = time.Now().UTC()
	} else {
		face.CreatedAt = face.CreatedAt.UTC()
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(face).Error; err != nil {
			return fmt.Errorf("create face template: %w", err)
		}

		// Proactively assign as primary face if person currently has none.
		var p Person
		if err := tx.Where("id = ?", face.PersonRowID).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}
		if p.PrimaryFaceID == nil || *p.PrimaryFaceID == "" {
			now := time.Now().UTC()
			if err := tx.Model(&Person{}).Where("id = ?", face.PersonRowID).Updates(map[string]any{
				"primary_face_id": face.ID,
				"updated_at":      now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetFace fetches a face template by ID.
func (s *Store) GetFace(ctx context.Context, id string) (*Face, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var f Face
	if err := db.Where("id = ?", id).First(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &f, nil
}

// ListFacesByPerson returns all face templates registered for a person.
func (s *Store) ListFacesByPerson(ctx context.Context, personID string) ([]Face, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var list []Face
	if err := db.Where("person_id = ?", personID).Order("created_at ASC, id ASC").Find(&list).Error; err != nil {
		return nil, err
	}
	return list, nil
}

// ListFacesByPersonPaged returns one page of a person's face templates together
// with the total count.
func (s *Store) ListFacesByPersonPaged(ctx context.Context, personID string, limit, offset int) ([]Face, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	query := db.Model(&Face{}).Where("person_id = ?", personID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count faces: %w", err)
	}

	limit, offset = clampPagination(limit, offset)

	var list []Face
	if err := query.Order("created_at ASC, id ASC").Limit(limit).Offset(offset).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// DeleteFace deletes a specific face template, scoped to its owning person so
// that a face cannot be removed through another person's path segment.
// If the deleted face was the person's primary face, the earliest remaining
// face is automatically promoted to primary, or primary_face_id is cleared if none remain.
func (s *Store) DeleteFace(ctx context.Context, personID, id string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var p Person
		if err := tx.Where("id = ?", personID).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		res := tx.Where("id = ? AND person_id = ?", id, personID).Delete(&Face{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}

		// If the deleted face was the primary face, promote the next available or clear it.
		if p.PrimaryFaceID != nil && *p.PrimaryFaceID == id {
			var nextFace Face
			now := time.Now().UTC()
			err := tx.Where("person_id = ?", personID).Order("created_at ASC, id ASC").First(&nextFace).Error
			if err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					// No more faces remain
					if err := tx.Model(&Person{}).Where("id = ?", personID).Updates(map[string]any{
						"primary_face_id": nil,
						"updated_at":      now,
					}).Error; err != nil {
						return err
					}
				} else {
					return err
				}
			} else {
				// Promote earliest remaining face
				if err := tx.Model(&Person{}).Where("id = ?", personID).Updates(map[string]any{
					"primary_face_id": nextFace.ID,
					"updated_at":      now,
				}).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// SetPrimaryFace points a person's primary_face_id at one of their own faces.
//
// The face must exist and belong to personID; callers that need to tell "person
// missing" apart from "face missing" resolve those separately first (see
// Service.SetPrimaryFace). Clearing the pointer is not a supported transition:
// the pointer is maintained by AddFace and DeleteFace as the person's face set
// changes.
func (s *Store) SetPrimaryFace(ctx context.Context, personID, faceID string) error {
	db, err := s.db(ctx)
	if err != nil {
		return err
	}

	return db.Transaction(func(tx *gorm.DB) error {
		var p Person
		if err := tx.Where("id = ?", personID).First(&p).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		var count int64
		if err := tx.Model(&Face{}).Where("id = ? AND person_id = ?", faceID, personID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return ErrNotFound
		}

		return tx.Model(&Person{}).Where("id = ?", personID).Updates(map[string]any{
			"primary_face_id": faceID,
			"updated_at":      time.Now().UTC(),
		}).Error
	})
}
