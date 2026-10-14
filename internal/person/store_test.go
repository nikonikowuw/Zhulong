package person_test

import (
	"context"
	"errors"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/person"
	"go.uber.org/zap"
)

func setupTestStore(t *testing.T) (*person.Store, func()) {
	t.Helper()
	tempDir := t.TempDir()

	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	store := person.NewStore(dbStore)
	cleanup := func() {
		_ = dbStore.Close()
	}
	return store, cleanup
}

func TestStorePersonMultiFaceCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p := &person.Person{
		ID:       "emp_9527",
		PersonID: "EMP_9527",
		Name:     "张三",
	}

	// 1. Create Person
	if err := store.CreatePerson(ctx, p); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// 2. Add Face 1 (正脸)
	fakeVector1 := make([]byte, 2048)
	fakeVector1[0] = 0x01
	face1 := &person.Face{
		ID:            "face_01",
		OwnerRowID:    "emp_9527",
		ImagePath:     "/data/faces/face_01.jpg",
		FeatureVector: fakeVector1,
	}
	if err := store.AddFace(ctx, face1); err != nil {
		t.Fatalf("AddFace 1 failed: %v", err)
	}

	// 3. Add Face 2 (戴眼镜照)
	fakeVector2 := make([]byte, 2048)
	fakeVector2[0] = 0x02
	face2 := &person.Face{
		ID:            "face_02",
		OwnerRowID:    "emp_9527",
		ImagePath:     "/data/faces/face_02.jpg",
		FeatureVector: fakeVector2,
	}
	if err := store.AddFace(ctx, face2); err != nil {
		t.Fatalf("AddFace 2 failed: %v", err)
	}

	// 4. Query Person with Preloaded Faces (一人多照)
	foundPerson, err := store.GetPerson(ctx, "emp_9527")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if foundPerson.Name != "张三" {
		t.Errorf("expected name '张三', got '%s'", foundPerson.Name)
	}
	if foundPerson.PersonID != "EMP_9527" {
		t.Errorf("expected personID 'EMP_9527', got '%s'", foundPerson.PersonID)
	}
	if len(foundPerson.Faces) != 2 {
		t.Fatalf("expected 2 registered faces for person, got %d", len(foundPerson.Faces))
	}

	// 4.1 Test ListPersons (paginated) and ListAllPersons
	pList, total, err := store.ListPersons(ctx, 10, 0, "")
	if err != nil {
		t.Fatalf("ListPersons failed: %v", err)
	}
	if total != 1 || len(pList) != 1 {
		t.Fatalf("expected total 1 and len 1, got total=%d len=%d", total, len(pList))
	}
	allPersons, err := store.ListAllPersons(ctx)
	if err != nil {
		t.Fatalf("ListAllPersons failed: %v", err)
	}
	if len(allPersons) != 1 {
		t.Fatalf("expected len 1 for ListAllPersons, got %d", len(allPersons))
	}

	// 5. List all faces
	allFaces, err := store.ListAllFaces(ctx)
	if err != nil {
		t.Fatalf("ListAllFaces failed: %v", err)
	}
	if len(allFaces) != 2 {
		t.Errorf("expected 2 faces total, got %d", len(allFaces))
	}

	// 6. Delete Face 1
	if err := store.DeleteFace(ctx, "emp_9527", "face_01"); err != nil {
		t.Fatalf("DeleteFace failed: %v", err)
	}
	foundPerson, err = store.GetPerson(ctx, "emp_9527")
	if err != nil {
		t.Fatalf("GetPerson after deleting face 1 failed: %v", err)
	}
	if len(foundPerson.Faces) != 1 {
		t.Errorf("expected 1 remaining face, got %d", len(foundPerson.Faces))
	}

	// 7. Delete Person (cascade delete Face 2)
	if err := store.DeletePerson(ctx, "emp_9527"); err != nil {
		t.Fatalf("DeletePerson failed: %v", err)
	}
	_, err = store.GetPerson(ctx, "emp_9527")
	if !errors.Is(err, person.ErrNotFound) {
		t.Errorf("expected ErrNotFound for deleted person, got %v", err)
	}

	allFaces, err = store.ListAllFaces(ctx)
	if err != nil {
		t.Fatalf("ListAllFaces after person delete failed: %v", err)
	}
	if len(allFaces) != 0 {
		t.Errorf("expected 0 remaining faces after cascade delete, got %d", len(allFaces))
	}
}

// TestUpdatePerson_DoesNotTouchFaces is the regression guard for the db.Save(p)
// defect: saving a Person with a Preloaded Faces slice used to upsert the whole
// association, resurrecting concurrently deleted faces and rewriting person_id.
func TestUpdatePerson_DoesNotTouchFaces(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "E1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	for _, id := range []string{"f1", "f2"} {
		if err := store.AddFace(ctx, &person.Face{ID: id, OwnerRowID: "p1", FeatureVector: make([]byte, 2048)}); err != nil {
			t.Fatalf("AddFace %s failed: %v", id, err)
		}
	}

	// Stale snapshot holding both faces.
	stale, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if len(stale.Faces) != 2 {
		t.Fatalf("expected 2 preloaded faces, got %d", len(stale.Faces))
	}

	// Concurrent deletion of f2.
	if err := store.DeleteFace(ctx, "p1", "f2"); err != nil {
		t.Fatalf("DeleteFace failed: %v", err)
	}

	// Stale update must not resurrect f2.
	stale.Name = "李四"
	if err := store.UpdatePerson(ctx, stale); err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}

	faces, err := store.ListFacesByPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("ListFacesByPerson failed: %v", err)
	}
	if len(faces) != 1 || faces[0].ID != "f1" {
		t.Fatalf("UpdatePerson modified the Faces association: got %d faces %v", len(faces), faces)
	}

	updated, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson after update failed: %v", err)
	}
	if updated.Name != "李四" {
		t.Errorf("expected name '李四', got %q", updated.Name)
	}
}

// TestUpdatePerson_MissingIDReturnsNotFound guards against GORM Save() silently
// degrading to an INSERT (upsert) when RowsAffected == 0.
func TestUpdatePerson_MissingIDReturnsNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	err := store.UpdatePerson(ctx, &person.Person{ID: "ghost", PersonID: "E9", Name: "nobody"})
	if !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if _, err := store.GetPerson(ctx, "ghost"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("UpdatePerson must not create the row; GetPerson returned %v", err)
	}
}

// TestUpdatePerson_IdempotentUpdateNotMisreportedAsNotFound ensures a no-op
// update (same values) still reports success rather than 404.
func TestUpdatePerson_IdempotentUpdateNotMisreportedAsNotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "E1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if err := store.UpdatePerson(ctx, &person.Person{ID: "p1", PersonID: "E1", Name: "张三"}); err != nil {
		t.Fatalf("idempotent UpdatePerson must succeed, got %v", err)
	}
}

// TestCreatePerson_DuplicateIDReturnsErrDuplicateID covers the previously dead
// ErrDuplicateID sentinel and the resulting 409 mapping.
func TestCreatePerson_DuplicateIDReturnsErrDuplicateID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "E1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "E2", Name: "李四"})
	if !errors.Is(err, person.ErrDuplicateID) {
		t.Fatalf("expected ErrDuplicateID, got %v", err)
	}

	// The original record must be untouched.
	got, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.Name != "张三" {
		t.Errorf("duplicate insert mutated the existing row: name=%q", got.Name)
	}
}

// TestDeleteFace_ScopedToOwningPerson rejects cross-person face deletion, which
// the handler previously allowed by discarding the :id path segment.
func TestDeleteFace_ScopedToOwningPerson(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, p := range []struct{ id, personID, name string }{{"p1", "E1", "张三"}, {"p2", "E2", "李四"}} {
		if err := store.CreatePerson(ctx, &person.Person{ID: p.id, PersonID: p.personID, Name: p.name}); err != nil {
			t.Fatalf("CreatePerson %s failed: %v", p.id, err)
		}
	}
	if err := store.AddFace(ctx, &person.Face{ID: "f2", OwnerRowID: "p2", FeatureVector: make([]byte, 2048)}); err != nil {
		t.Fatalf("AddFace failed: %v", err)
	}

	if err := store.DeleteFace(ctx, "p1", "f2"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("expected ErrNotFound deleting p2's face via p1, got %v", err)
	}
	if _, err := store.GetFace(ctx, "f2"); err != nil {
		t.Fatalf("p2's face must survive: %v", err)
	}

	if err := store.DeleteFace(ctx, "p2", "f2"); err != nil {
		t.Fatalf("owner-scoped DeleteFace failed: %v", err)
	}
	if _, err := store.GetFace(ctx, "f2"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("expected face deleted, got %v", err)
	}
}

// TestCreatePerson_DuplicatePersonIDReturnsErrDuplicatePersonID pins the business-key
// conflict to its own sentinel. person_id is what external systems correlate on, so a
// collision there must not be reported as a primary-key collision (and vice versa).
func TestCreatePerson_DuplicatePersonIDReturnsErrDuplicatePersonID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "EMP_1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// Different row id, same business key: the conflict is on person_id only.
	err := store.CreatePerson(ctx, &person.Person{ID: "p2", PersonID: "EMP_1", Name: "李四"})
	if !errors.Is(err, person.ErrDuplicatePersonID) {
		t.Fatalf("expected ErrDuplicatePersonID, got %v", err)
	}
	if _, err := store.GetPerson(ctx, "p2"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("rejected insert must not leave a row behind; GetPerson returned %v", err)
	}
}

// TestCreatePerson_EmptyPersonIDRejected guards the CHECK constraint behind the API
// layer's required-field validation: a person without a business key is not addressable
// outside this service, so the store must not accept one either.
func TestCreatePerson_EmptyPersonIDRejected(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", Name: "张三"}); err == nil {
		t.Fatal("expected CreatePerson to reject an empty person_id")
	}
	if _, err := store.GetPerson(ctx, "p1"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("rejected insert must not leave a row behind; GetPerson returned %v", err)
	}
}

// TestUpdatePerson_DoesNotRewritePersonID locks the business key down at the store
// layer too, so immutability survives a caller that bypasses the service.
func TestUpdatePerson_DoesNotRewritePersonID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "EMP_1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	if err := store.UpdatePerson(ctx, &person.Person{ID: "p1", PersonID: "EMP_HIJACK", Name: "张三"}); err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}

	got, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.PersonID != "EMP_1" {
		t.Errorf("person_id is immutable, but UpdatePerson rewrote it to %q", got.PersonID)
	}
}

// TestGetPersonMeta_SkipsFacePreload pins the reason GetPersonMeta exists: the update merge
// and the existence checks must not pay to load every enrolled 2048-byte feature vector.
func TestGetPersonMeta_SkipsFacePreload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: "EMP_1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if err := store.AddFace(ctx, &person.Face{ID: "f1", OwnerRowID: "p1", FeatureVector: make([]byte, 2048)}); err != nil {
		t.Fatalf("AddFace failed: %v", err)
	}

	withFaces, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if len(withFaces.Faces) != 1 {
		t.Fatalf("GetPerson must preload faces, got %d", len(withFaces.Faces))
	}

	meta, err := store.GetPersonMeta(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPersonMeta failed: %v", err)
	}
	if len(meta.Faces) != 0 {
		t.Errorf("GetPersonMeta must not preload faces, got %d", len(meta.Faces))
	}
	if meta.Name != "张三" || meta.PersonID != "EMP_1" {
		t.Errorf("GetPersonMeta returned the wrong row: %+v", meta)
	}

	if _, err := store.GetPersonMeta(ctx, "ghost"); !errors.Is(err, person.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown id, got %v", err)
	}
}
