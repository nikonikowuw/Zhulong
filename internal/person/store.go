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
	ErrNotFound = errors.New("person or face not found")
	// ErrDatabaseClosed is an alias of the shared sentinel: callers outside this
	// package keep the domain-specific name, but there is only one error value.
	ErrDatabaseClosed = database.ErrClosed
	ErrDuplicateID    = errors.New("person with this ID already exists")
	// ErrDuplicatePersonID reports a collision on the external business key, which is
	// distinct from ErrDuplicateID so callers can name the offending field.
	ErrDuplicatePersonID = errors.New("person with this person_id already exists")
	// ErrFaceLimitExceeded is returned when adding a face would exceed the per-person template limit.
	ErrFaceLimitExceeded = errors.New("face limit exceeded")
	// ErrNilPerson and ErrNilFace guard the write paths against a caller that forgot to
	// build the row. They are package sentinels so a caller can assert on them with
	// errors.Is instead of matching an error string built at the call site.
	ErrNilPerson = errors.New("person is nil")
	ErrNilFace   = errors.New("face is nil")
)

// clampPagination applies the shared page bounds as a second line of defence
// for callers that reach the store without going through a handler.
//
// The bounds reference the httputil constants rather than restating 20/100 here:
// a local copy would silently drift the day one side changes.
func clampPagination(limit, offset int) (int, int) {
	switch {
	case limit <= 0:
		limit = httputil.DefaultPageSize
	case limit > httputil.MaxPageSize:
		limit = httputil.MaxPageSize
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func normalizeTimestamp(t, fallback time.Time) time.Time {
	if t.IsZero() {
		return fallback.UTC()
	}
	return t.UTC()
}

// preloadFaceSummary narrows a Faces preload to the columns a face list needs. It
// deliberately omits feature_vector: every vector is faceFeatureDim*4 bytes, and list
// views render thumbnails from image_path alone. Every path that preloads faces shares
// it, so the column list has a single definition.
func preloadFaceSummary(pb gorm.PreloadBuilder) error {
	pb.Select("id", "person_id", "image_path", "created_at")
	return nil
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
		return ErrNilPerson
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	p.CreatedAt = normalizeTimestamp(p.CreatedAt, now)
	p.UpdatedAt = normalizeTimestamp(p.UpdatedAt, now)
	// DO NOTHING keeps the duplicate-key detection inside SQLite (RowsAffected == 0)
	// without enabling GORM's global TranslateError option or matching error strings.
	result := gorm.WithResult()
	err = gorm.G[Person](db, clause.OnConflict{DoNothing: true}, result).Create(ctx, p)
	if err != nil {
		return fmt.Errorf("create person: %w", err)
	}
	if result.RowsAffected == 0 {
		// SQLite's unqualified ON CONFLICT DO NOTHING swallows every uniqueness
		// violation, not just the primary key, so the conflict must be attributed by
		// probing. persons has exactly two unique constraints: id and person_id.
		existing, err := gorm.G[Person](db).Where("id = ?", p.ID).Count(ctx, "*")
		if err != nil {
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
func (s *Store) CreatePersonImport(
	ctx context.Context,
	entries []personImportEntry,
) ([]personImportCreateResult, error) {
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
				ID:         idutil.New(),
				PersonID:   entry.personID,
				Name:       entry.name,
				Source:     PersonSourceLocal,
				SourceName: "",
			}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(p)
			if res.Error != nil {
				return fmt.Errorf("create imported person: %w", res.Error)
			}
			if res.RowsAffected == 0 {
				// A row without a business key can only collide on id, which this
				// package generates: that is not a per-row data problem but a broken
				// invariant, so it aborts the whole batch instead of being reported
				// as one failed line.
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
	p, err := gorm.G[Person](db).Preload("Faces", preloadFaceSummary).Where("id = ?", id).First(ctx)
	if err != nil {
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
	p, err := gorm.G[Person](db).Where("id = ?", id).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListPersons returns paginated persons with associated Face templates, optionally filtered by keyword and faceStatus.
func (s *Store) ListPersons(ctx context.Context, limit, offset int, search, faceStatus string) ([]Person, int64, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, 0, err
	}

	// The generics API only exposes Count/Find on ChainInterface, and the search
	// filter is optional, so the chain is opened once here and the predicate is added
	// below only when a keyword was supplied.
	g := gorm.G[Person](db).Scopes()
	search = strings.TrimSpace(search)
	if search != "" {
		// The keyword is data, not a pattern: % and _ must match themselves instead of
		// turning a search for "50%" into a full-table wildcard.
		likePattern := "%" + escapeLike(search) + "%"
		g = g.Where(`name LIKE ? ESCAPE '\' OR (person_id IS NOT NULL AND person_id LIKE ? ESCAPE '\')`,
			likePattern, likePattern)
	}

	switch strings.ToLower(strings.TrimSpace(faceStatus)) {
	case "enrolled":
		g = g.Where("EXISTS (SELECT 1 FROM person_faces WHERE person_faces.person_id = persons.id)")
	case "missing":
		g = g.Where("NOT EXISTS (SELECT 1 FROM person_faces WHERE person_faces.person_id = persons.id)")
	}

	total, err := g.Count(ctx, "*")
	if err != nil {
		return nil, 0, fmt.Errorf("count persons: %w", err)
	}

	limit, offset = clampPagination(limit, offset)

	list, err := g.Preload("Faces", preloadFaceSummary).Order("created_at ASC, id ASC").
		Limit(limit).
		Offset(offset).
		Find(ctx)
	if err != nil {
		return nil, 0, err
	}
	if list == nil {
		list = []Person{}
	}
	return list, total, nil
}

// PersonManifestRow is one row of the photo-manifest CSV export: identity fields plus the
// number of templates already enrolled.
//
// It exists instead of a `[]Person` with a preloaded Faces slice on purpose: the export
// walks the whole table, and preloading every template row (up to MaxFacesPerPerson each)
// would pull the entire gallery into memory just to print a count. The counts are read
// separately with one grouped aggregation.
type PersonManifestRow struct {
	Name      string
	PersonID  *string
	FaceCount int
}

// ListAllPersonsForExport returns every person matching the filter, unpaginated, together
// with its enrolled-template count.
//
// This is intentionally the only unbounded reader in the store: it backs a file the
// operator asked for explicitly, and the caller streams it straight into a CSV writer
// rather than holding a page in a UI. It still avoids Preload("Faces") so the cost stays
// proportional to the person count, not the face count.
func (s *Store) ListAllPersonsForExport(ctx context.Context, search, faceStatus string) ([]PersonManifestRow, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	g := gorm.G[Person](db).Scopes()
	search = strings.TrimSpace(search)
	if search != "" {
		likePattern := "%" + escapeLike(search) + "%"
		g = g.Where(`name LIKE ? ESCAPE '\' OR (person_id IS NOT NULL AND person_id LIKE ? ESCAPE '\')`,
			likePattern, likePattern)
	}

	switch strings.ToLower(strings.TrimSpace(faceStatus)) {
	case "enrolled":
		g = g.Where("EXISTS (SELECT 1 FROM person_faces WHERE person_faces.person_id = persons.id)")
	case "missing":
		g = g.Where("NOT EXISTS (SELECT 1 FROM person_faces WHERE person_faces.person_id = persons.id)")
	}

	persons, err := g.Select("id", "name", "person_id").Order("created_at ASC, id ASC").Find(ctx)
	if err != nil {
		return nil, err
	}
	if len(persons) == 0 {
		return []PersonManifestRow{}, nil
	}

	ids := make([]string, 0, len(persons))
	for _, p := range persons {
		ids = append(ids, p.ID)
	}

	counts, err := faceCountsByPerson(ctx, db, ids)
	if err != nil {
		return nil, err
	}

	rows := make([]PersonManifestRow, 0, len(persons))
	for _, p := range persons {
		rows = append(rows, PersonManifestRow{
			Name:      p.Name,
			PersonID:  p.PersonID,
			FaceCount: counts[p.ID],
		})
	}
	return rows, nil
}

// faceCountsByPerson reads template counts for the given person row ids in one grouped
// query. Chunking keeps the IN list below SQLite's default 32766 bound-parameter ceiling.
func faceCountsByPerson(ctx context.Context, db *gorm.DB, ids []string) (map[string]int, error) {
	type countRow struct {
		PersonRowID string
		Total       int
	}

	const chunkSize = 500
	counts := make(map[string]int, len(ids))
	for start := 0; start < len(ids); start += chunkSize {
		end := min(start+chunkSize, len(ids))
		chunk := ids[start:end]

		var rows []countRow
		if err := db.WithContext(ctx).
			Table("person_faces").
			Select("person_id AS person_row_id, COUNT(*) AS total").
			Where("person_id IN ?", chunk).
			Group("person_id").
			Scan(&rows).Error; err != nil {
			return nil, fmt.Errorf("count faces by person: %w", err)
		}
		for _, row := range rows {
			counts[row.PersonRowID] = row.Total
		}
	}
	return counts, nil
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
	list, err := gorm.G[Person](db).
		Preload("Faces", nil).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Offset(offset).
		Find(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Person{}
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
		return ErrNilPerson
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
		updates["person_id"] = gorm.Expr(
			"CASE WHEN person_id IS NULL OR person_id = '' THEN ? ELSE person_id END",
			*p.PersonID,
		)
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
	rows, err := gorm.G[Person](db).Where("id = ?", id).Delete(ctx)
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrNotFound
	}
	return nil
}

// AddFace inserts a new Face template for a person. If the person has no primary
// face assigned, the new face is automatically designated as primary.
// Note: callers that enforce maximum enrollment limits should use AddFaceWithLimit.
func (s *Store) AddFace(ctx context.Context, face *Face) error {
	if face == nil {
		return ErrNilFace
	}
	db, err := s.db(ctx)
	if err != nil {
		return err
	}
	face.CreatedAt = normalizeTimestamp(face.CreatedAt, time.Now())

	return db.Transaction(func(tx *gorm.DB) error {
		if err := gorm.G[Face](tx).Create(ctx, face); err != nil {
			return fmt.Errorf("create face template: %w", err)
		}

		// Proactively assign as primary face if person currently has none.
		p, err := gorm.G[Person](tx).Where("id = ?", face.PersonRowID).First(ctx)
		if err != nil {
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

// AddFaceWithLimit inserts a new Face template for a person inside a transaction,
// checking that the current face count has not reached or exceeded limit.
//
// It also reports whether the inserted row became the person's primary face. That
// decision is made by the same transaction that inserts the row, so the answer is the
// one the database committed; callers must not re-derive it from a later read, which
// can observe a different primary face when two enrollments race.
func (s *Store) AddFaceWithLimit(ctx context.Context, face *Face, limit int) (bool, error) {
	if face == nil {
		return false, ErrNilFace
	}
	db, err := s.db(ctx)
	if err != nil {
		return false, err
	}
	face.CreatedAt = normalizeTimestamp(face.CreatedAt, time.Now())

	isPrimary := false
	err = db.Transaction(func(tx *gorm.DB) error {
		p, err := gorm.G[Person](tx).Where("id = ?", face.PersonRowID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		if limit > 0 {
			var count int64
			if err := tx.Model(&Face{}).Where("person_id = ?", face.PersonRowID).Count(&count).Error; err != nil {
				return err
			}
			if int(count) >= limit {
				return ErrFaceLimitExceeded
			}
		}

		if err := gorm.G[Face](tx).Create(ctx, face); err != nil {
			return fmt.Errorf("create face template: %w", err)
		}

		if p.PrimaryFaceID == nil || *p.PrimaryFaceID == "" {
			now := time.Now().UTC()
			if err := tx.Model(&Person{}).Where("id = ?", face.PersonRowID).Updates(map[string]any{
				"primary_face_id": face.ID,
				"updated_at":      now,
			}).Error; err != nil {
				return err
			}
			isPrimary = true
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	return isPrimary, nil
}

// ReplaceFaces atomically replaces all existing face templates of a person with a single new face,
// setting the person's primary_face_id to the new face, so the replacement is always
// primary once this returns nil.
// It returns the list of removed Face records so callers can clean up the corresponding files on disk.
func (s *Store) ReplaceFaces(ctx context.Context, personRowID string, face *Face) ([]Face, error) {
	if face == nil {
		return nil, ErrNilFace
	}
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	face.CreatedAt = normalizeTimestamp(face.CreatedAt, time.Now())

	var removed []Face
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[Person](tx).Where("id = ?", personRowID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		if err := tx.Model(&Face{}).Where("person_id = ?", personRowID).Find(&removed).Error; err != nil {
			return err
		}

		if len(removed) > 0 {
			if err := tx.Where("person_id = ?", personRowID).Delete(&Face{}).Error; err != nil {
				return err
			}
		}

		face.PersonRowID = personRowID
		if err := gorm.G[Face](tx).Create(ctx, face); err != nil {
			return fmt.Errorf("create replacement face: %w", err)
		}

		now := time.Now().UTC()
		if err := tx.Model(&Person{}).Where("id = ?", personRowID).Updates(map[string]any{
			"primary_face_id": face.ID,
			"updated_at":      now,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// ClearFaces deletes all face templates associated with personRowID and clears the primary_face_id pointer.
// It returns the list of removed Face records for caller disk cleanup.
func (s *Store) ClearFaces(ctx context.Context, personRowID string) ([]Face, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}

	var removed []Face
	err = db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[Person](tx).Where("id = ?", personRowID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		if err := tx.Model(&Face{}).Where("person_id = ?", personRowID).Find(&removed).Error; err != nil {
			return err
		}

		if len(removed) > 0 {
			if err := tx.Where("person_id = ?", personRowID).Delete(&Face{}).Error; err != nil {
				return err
			}
		}

		now := time.Now().UTC()
		return tx.Model(&Person{}).Where("id = ?", personRowID).Updates(map[string]any{
			"primary_face_id": nil,
			"updated_at":      now,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}

// GetFace fetches a face template by ID.
func (s *Store) GetFace(ctx context.Context, id string) (*Face, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	f, err := gorm.G[Face](db).Where("id = ?", id).First(ctx)
	if err != nil {
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
	list, err := gorm.G[Face](db).Where("person_id = ?", personID).Order("created_at ASC, id ASC").Find(ctx)
	if err != nil {
		return nil, err
	}
	if list == nil {
		list = []Face{}
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

	total, err := gorm.G[Face](db).Where("person_id = ?", personID).Count(ctx, "*")
	if err != nil {
		return nil, 0, fmt.Errorf("count faces: %w", err)
	}

	limit, offset = clampPagination(limit, offset)

	list, err := gorm.G[Face](db).
		Where("person_id = ?", personID).
		Order("created_at ASC, id ASC").
		Limit(limit).
		Offset(offset).
		Find(ctx)
	if err != nil {
		return nil, 0, err
	}
	if list == nil {
		list = []Face{}
	}
	return list, total, nil
}

// SearchFaceItem is the database projection loaded for 1:N face feature search.
// It joins person_faces with persons to obtain template and owner metadata in a single pass.
// image_path is deliberately excluded so internal filesystem paths are never read into search memory.
type SearchFaceItem struct {
	FaceID           string  `gorm:"column:face_id"`
	PersonRowID      string  `gorm:"column:person_row_id"`
	PersonExternalID *string `gorm:"column:person_external_id"`
	PersonName       string  `gorm:"column:person_name"`
	FeatureVector    []byte  `gorm:"column:feature_vector"`
}

const searchFaceColumns = "f.id AS face_id, " +
	"f.person_id AS person_row_id, " +
	"p.person_id AS person_external_id, " +
	"p.name AS person_name, " +
	"f.feature_vector AS feature_vector"

// searchFaceQuery is the shared projection behind both 1:N search readers: it joins
// person_faces with persons so a candidate arrives with its template and owner
// metadata in a single pass.
func searchFaceQuery(db *gorm.DB) *gorm.DB {
	return db.Table("person_faces f").
		Select(searchFaceColumns).
		Joins("INNER JOIN persons p ON f.person_id = p.id")
}

// ListSearchFaceItems retrieves all enrolled face templates along with their owner's
// identifiers and names for in-memory 1:N similarity search.
func (s *Store) ListSearchFaceItems(ctx context.Context) ([]SearchFaceItem, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var items []SearchFaceItem
	if err := searchFaceQuery(db).Find(&items).Error; err != nil {
		return nil, fmt.Errorf("list search face items: %w", err)
	}
	return items, nil
}

// ListSearchFaceItemsPaged retrieves a paged chunk of enrolled face templates
// to bound memory overhead during 1:N similarity search.
func (s *Store) ListSearchFaceItemsPaged(ctx context.Context, limit, offset int) ([]SearchFaceItem, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	var items []SearchFaceItem
	err = searchFaceQuery(db).
		Order("f.created_at ASC, f.id ASC").
		Limit(limit).
		Offset(offset).
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("list search face items paged: %w", err)
	}
	return items, nil
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
		p, err := gorm.G[Person](tx).Where("id = ?", personID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		rows, err := gorm.G[Face](tx).Where("id = ? AND person_id = ?", id, personID).Delete(ctx)
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrNotFound
		}

		// If the deleted face was the primary face, promote the next available or clear it.
		if p.PrimaryFaceID != nil && *p.PrimaryFaceID == id {
			now := time.Now().UTC()
			nextFace, err := gorm.G[Face](tx).Where("person_id = ?", personID).Order("created_at ASC, id ASC").First(ctx)
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				// No more faces remain
				if err := tx.Model(&Person{}).Where("id = ?", personID).Updates(map[string]any{
					"primary_face_id": nil,
					"updated_at":      now,
				}).Error; err != nil {
					return err
				}
			case err != nil:
				return err
			default:
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
		_, err := gorm.G[Person](tx).Where("id = ?", personID).First(ctx)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrNotFound
			}
			return err
		}

		count, err := gorm.G[Face](tx).Where("id = ? AND person_id = ?", faceID, personID).Count(ctx, "*")
		if err != nil {
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

// GetPersonByExternalID retrieves a person by their external business key (person_id),
// preloading their face template summaries.
func (s *Store) GetPersonByExternalID(ctx context.Context, externalID string) (*Person, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	p, err := gorm.G[Person](db).
		Preload("Faces", preloadFaceSummary).
		Where("person_id = ?", externalID).
		First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetPersonMetaByExternalID retrieves a person's row by external business key without preloading Faces.
func (s *Store) GetPersonMetaByExternalID(ctx context.Context, externalID string) (*Person, error) {
	db, err := s.db(ctx)
	if err != nil {
		return nil, err
	}
	p, err := gorm.G[Person](db).Where("person_id = ?", externalID).First(ctx)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}
