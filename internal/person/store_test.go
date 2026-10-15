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

func strPtr(s string) *string {
	return &s
}

func TestStorePersonMultiFaceCRUD(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p := &person.Person{
		ID:       "emp_9527",
		PersonID: strPtr("EMP_9527"),
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
	if foundPerson.PersonID == nil || *foundPerson.PersonID != "EMP_9527" {
		t.Errorf("expected personID 'EMP_9527', got '%v'", foundPerson.PersonID)
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

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("E1"), Name: "张三"}); err != nil {
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

	err := store.UpdatePerson(ctx, &person.Person{ID: "ghost", PersonID: strPtr("E9"), Name: "nobody"})
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

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("E1"), Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if err := store.UpdatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("E1"), Name: "张三"}); err != nil {
		t.Fatalf("idempotent UpdatePerson must succeed, got %v", err)
	}
}

// TestCreatePerson_DuplicateIDReturnsErrDuplicateID covers the previously dead
// ErrDuplicateID sentinel and the resulting 409 mapping.
func TestCreatePerson_DuplicateIDReturnsErrDuplicateID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("E1"), Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("E2"), Name: "李四"})
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
		if err := store.CreatePerson(ctx, &person.Person{ID: p.id, PersonID: strPtr(p.personID), Name: p.name}); err != nil {
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

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("EMP_1"), Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// Different row id, same business key: the conflict is on person_id only.
	err := store.CreatePerson(ctx, &person.Person{ID: "p2", PersonID: strPtr("EMP_1"), Name: "李四"})
	if !errors.Is(err, person.ErrDuplicatePersonID) {
		t.Fatalf("expected ErrDuplicatePersonID, got %v", err)
	}
	if _, err := store.GetPerson(ctx, "p2"); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("rejected insert must not leave a row behind; GetPerson returned %v", err)
	}
}

// TestCreatePerson_NilPersonIDAllowed verifies that a person without an external business
// key is accepted and stored with a NULL person_id.
func TestCreatePerson_NilPersonIDAllowed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", Name: "张三"}); err != nil {
		t.Fatalf("expected CreatePerson to accept nil person_id, got %v", err)
	}
	got, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson returned %v", err)
	}
	if got.PersonID != nil {
		t.Fatalf("expected nil person_id, got %q", *got.PersonID)
	}
}

// TestUpdatePerson_DoesNotRewritePersonID locks the business key down at the store
// layer too, so immutability survives a caller that bypasses the service.
func TestUpdatePerson_DoesNotRewritePersonID(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("EMP_1"), Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	if err := store.UpdatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("EMP_HIJACK"), Name: "张三"}); err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}

	got, err := store.GetPerson(ctx, "p1")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.PersonID == nil || *got.PersonID != "EMP_1" {
		t.Errorf("person_id is immutable, but UpdatePerson rewrote it to %v", got.PersonID)
	}
}

// TestGetPersonMeta_SkipsFacePreload pins the reason GetPersonMeta exists: the update merge
// and the existence checks must not pay to load every enrolled 2048-byte feature vector.
func TestGetPersonMeta_SkipsFacePreload(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := store.CreatePerson(ctx, &person.Person{ID: "p1", PersonID: strPtr("EMP_1"), Name: "张三"}); err != nil {
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
	if meta.Name != "张三" || meta.PersonID == nil || *meta.PersonID != "EMP_1" {
		t.Errorf("GetPersonMeta returned the wrong row: %+v", meta)
	}

	if _, err := store.GetPersonMeta(ctx, "ghost"); !errors.Is(err, person.ErrNotFound) {
		t.Errorf("expected ErrNotFound for an unknown id, got %v", err)
	}
}

func TestStore_PrimaryFaceAutoPromotionAndFallback(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	p := &person.Person{ID: "p_primary", PersonID: strPtr("EMP_PRIMARY"), Name: "王五"}
	if err := store.CreatePerson(ctx, p); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	got, err := store.GetPerson(ctx, "p_primary")
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.PrimaryFaceID != nil {
		t.Fatalf("expected nil PrimaryFaceID initially, got %v", *got.PrimaryFaceID)
	}

	// 1. Add first face: automatically promoted to primary
	f1 := &person.Face{ID: "face_auto_1", OwnerRowID: "p_primary", FeatureVector: make([]byte, 2048)}
	if err := store.AddFace(ctx, f1); err != nil {
		t.Fatalf("AddFace 1 failed: %v", err)
	}
	got, _ = store.GetPerson(ctx, "p_primary")
	if got.PrimaryFaceID == nil || *got.PrimaryFaceID != "face_auto_1" {
		t.Fatalf("expected PrimaryFaceID 'face_auto_1', got %v", got.PrimaryFaceID)
	}

	// 2. Add second face: primary face remains unchanged
	f2 := &person.Face{ID: "face_auto_2", OwnerRowID: "p_primary", FeatureVector: make([]byte, 2048)}
	if err := store.AddFace(ctx, f2); err != nil {
		t.Fatalf("AddFace 2 failed: %v", err)
	}
	got, _ = store.GetPerson(ctx, "p_primary")
	if got.PrimaryFaceID == nil || *got.PrimaryFaceID != "face_auto_1" {
		t.Fatalf("expected PrimaryFaceID to remain 'face_auto_1', got %v", got.PrimaryFaceID)
	}

	// 3. Explicitly switch primary face to face_auto_2
	face2ID := "face_auto_2"
	if err := store.SetPrimaryFace(ctx, "p_primary", &face2ID); err != nil {
		t.Fatalf("SetPrimaryFace to face_auto_2 failed: %v", err)
	}
	got, _ = store.GetPerson(ctx, "p_primary")
	if got.PrimaryFaceID == nil || *got.PrimaryFaceID != "face_auto_2" {
		t.Fatalf("expected PrimaryFaceID to be 'face_auto_2', got %v", got.PrimaryFaceID)
	}

	// 4. Invalid faceID returns ErrNotFound
	fakeID := "non_existent_face"
	if err := store.SetPrimaryFace(ctx, "p_primary", &fakeID); !errors.Is(err, person.ErrNotFound) {
		t.Fatalf("expected ErrNotFound for non existent face, got %v", err)
	}

	// 5. Delete face_auto_2 (the primary face): fallback to earliest face_auto_1
	if err := store.DeleteFace(ctx, "p_primary", "face_auto_2"); err != nil {
		t.Fatalf("DeleteFace failed: %v", err)
	}
	got, _ = store.GetPerson(ctx, "p_primary")
	if got.PrimaryFaceID == nil || *got.PrimaryFaceID != "face_auto_1" {
		t.Fatalf("expected fallback PrimaryFaceID to 'face_auto_1', got %v", got.PrimaryFaceID)
	}

	// 6. Delete remaining face: primary face cleared to nil
	if err := store.DeleteFace(ctx, "p_primary", "face_auto_1"); err != nil {
		t.Fatalf("DeleteFace 1 failed: %v", err)
	}
	got, _ = store.GetPerson(ctx, "p_primary")
	if got.PrimaryFaceID != nil {
		t.Fatalf("expected PrimaryFaceID to be nil when no faces remain, got %v", *got.PrimaryFaceID)
	}
}
