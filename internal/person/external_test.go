package person

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

func TestNormalizeExternalPersonID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "normal valid", input: "EMP-1001", want: "EMP-1001", wantErr: false},
		{name: "trimmed", input: "  EMP-1002  ", want: "EMP-1002", wantErr: false},
		{name: "empty", input: "   ", want: "", wantErr: true},
		{name: "too long", input: strings.Repeat("x", 65), want: "", wantErr: true},
		{name: "max length", input: strings.Repeat("x", 64), want: strings.Repeat("x", 64), wantErr: false},
		{name: "slash", input: "dept/emp1", want: "", wantErr: true},
		{name: "backslash", input: `dept\emp1`, want: "", wantErr: true},
		{name: "question mark", input: "emp?1", want: "", wantErr: true},
		{name: "hash", input: "emp#1", want: "", wantErr: true},
		{name: "percent", input: "emp%20", want: "", wantErr: true},
		{name: "newline", input: "emp\n1", want: "", wantErr: true},
		{name: "null byte", input: "emp\x001", want: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeExternalPersonID(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NormalizeExternalPersonID(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("NormalizeExternalPersonID(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// recordingNotifier records gallery broadcast calls for assertions.
type recordingNotifier struct {
	mu      sync.Mutex
	upserts []GalleryItem
	deletes []struct {
		faceID   string
		personID string
	}
}

func (r *recordingNotifier) BroadcastUpsert(ctx context.Context, item GalleryItem) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.upserts = append(r.upserts, item)
	return nil
}

func (r *recordingNotifier) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletes = append(r.deletes, struct {
		faceID   string
		personID string
	}{faceID: faceID, personID: personID})
	return nil
}

func (r *recordingNotifier) Snapshot() ([]GalleryItem, []struct{ faceID, personID string }) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u := make([]GalleryItem, len(r.upserts))
	copy(u, r.upserts)
	d := make([]struct{ faceID, personID string }, len(r.deletes))
	copy(d, r.deletes)
	return u, d
}

func TestExternalPersonFlow_UpsertAndSources(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// 1. Initial upsert via OpenAPI
	p, created, err := svc.UpsertPersonByExternalID(ctx, "EXT-001", "Alice", "DoorSystem")
	if err != nil {
		t.Fatalf("UpsertPersonByExternalID failed: %v", err)
	}
	if !created {
		t.Errorf("expected created=true on first upsert")
	}
	if p.Source != PersonSourceAPI || p.SourceName != "DoorSystem" {
		t.Errorf("unexpected source tracking: %s / %s", p.Source, p.SourceName)
	}

	// 2. Same name upsert returns existing person with created=false
	pSame, createdSame, err := svc.UpsertPersonByExternalID(ctx, "EXT-001", "Alice", "DoorSystem")
	if err != nil {
		t.Fatalf("second UpsertPersonByExternalID failed: %v", err)
	}
	if createdSame {
		t.Errorf("expected created=false on duplicate name upsert")
	}
	if pSame.ID != p.ID {
		t.Errorf("expected same person ID")
	}

	// 3. Update name via OpenAPI upsert preserves Source and SourceName
	pRenamed, createdRenamed, err := svc.UpsertPersonByExternalID(ctx, "EXT-001", "Alice Wonderland", "DoorSystem2")
	if err != nil {
		t.Fatalf("third UpsertPersonByExternalID failed: %v", err)
	}
	if createdRenamed {
		t.Errorf("expected created=false on rename")
	}
	if pRenamed.Name != "Alice Wonderland" {
		t.Errorf("expected name to be updated, got %q", pRenamed.Name)
	}
	if pRenamed.Source != PersonSourceAPI || pRenamed.SourceName != "DoorSystem" {
		t.Errorf("expected source to remain DoorSystem, got %s / %s", pRenamed.Source, pRenamed.SourceName)
	}

	// 4. Person created locally via WebUI should preserve Source=LOCAL even when updated by API
	localP, err := svc.CreatePerson(ctx, CreatePersonRequest{
		PersonID: strPtr("EXT-LOCAL"),
		Name:     "Bob",
	})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if localP.Source != PersonSourceLocal {
		t.Errorf("expected local person source=LOCAL, got %s", localP.Source)
	}

	pUpdatedLocal, _, err := svc.UpsertPersonByExternalID(ctx, "EXT-LOCAL", "Bob Dylan", "SyncJob")
	if err != nil {
		t.Fatalf("upsert on local person failed: %v", err)
	}
	if pUpdatedLocal.Source != PersonSourceLocal {
		t.Errorf("expected local person source to stay LOCAL, got %s", pUpdatedLocal.Source)
	}
}

func TestExternalPersonFlow_FaceReplaceAndLimit(t *testing.T) {
	svc := newTestService(t)
	notifier := &recordingNotifier{}
	svc.galleryNotifier = notifier
	ctx := context.Background()

	_, _, err := svc.UpsertPersonByExternalID(ctx, "EXT-REPLACE", "Charlie", "HR-Sync")
	if err != nil {
		t.Fatalf("upsert person failed: %v", err)
	}

	// 1. Add first face with inferred extension (no extension provided in filename)
	face1, replaced, err := svc.AddFaceByExternalID(ctx, "EXT-REPLACE", "rawphoto", bytes.NewReader(testJPEGBytes), false)
	if err != nil {
		t.Fatalf("AddFaceByExternalID 1 failed: %v", err)
	}
	if replaced {
		t.Errorf("expected replaced=false on append")
	}
	file1Path := filepath.Join(svc.facesDir, face1.ImagePath)
	if _, err := os.Stat(file1Path); err != nil {
		t.Fatalf("expected face1 file to exist on disk: %v", err)
	}

	// 2. Add second face
	face2, _, err := svc.AddFaceByExternalID(ctx, "EXT-REPLACE", "photo2.jpg", bytes.NewReader(testJPEGBytes), false)
	if err != nil {
		t.Fatalf("AddFaceByExternalID 2 failed: %v", err)
	}
	file2Path := filepath.Join(svc.facesDir, face2.ImagePath)

	// Verify person has 2 faces
	fullPerson, err := svc.GetPersonByExternalID(ctx, "EXT-REPLACE")
	if err != nil {
		t.Fatalf("GetPersonByExternalID failed: %v", err)
	}
	if len(fullPerson.Faces) != 2 {
		t.Fatalf("expected 2 faces, got %d", len(fullPerson.Faces))
	}

	// 3. Atomic replacement: replace=true
	faceRepl, replaced, err := svc.AddFaceByExternalID(ctx, "EXT-REPLACE", "new_single.png", bytes.NewReader(testPNGBytes), true)
	if err != nil {
		t.Fatalf("AddFaceByExternalID replace failed: %v", err)
	}
	if !replaced {
		t.Errorf("expected replaced=true")
	}

	// Old files should be removed from disk
	if _, err := os.Stat(file1Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected file1 to be deleted from disk, stat err: %v", err)
	}
	if _, err := os.Stat(file2Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected file2 to be deleted from disk, stat err: %v", err)
	}
	// New file should exist
	fileReplPath := filepath.Join(svc.facesDir, faceRepl.ImagePath)
	if _, err := os.Stat(fileReplPath); err != nil {
		t.Errorf("expected replacement face to exist on disk: %v", err)
	}

	// Person should now have exactly 1 face, and primary_face_id points to it
	fullPersonAfter, err := svc.GetPersonByExternalID(ctx, "EXT-REPLACE")
	if err != nil {
		t.Fatalf("GetPersonByExternalID after replace failed: %v", err)
	}
	if len(fullPersonAfter.Faces) != 1 || fullPersonAfter.Faces[0].ID != faceRepl.ID {
		t.Fatalf("expected exactly 1 face (new), got %+v", fullPersonAfter.Faces)
	}
	if fullPersonAfter.PrimaryFaceID == nil || *fullPersonAfter.PrimaryFaceID != faceRepl.ID {
		t.Errorf("expected primary_face_id to point to replacement face, got %v", fullPersonAfter.PrimaryFaceID)
	}

	// Verify gallery notifications: should have delete for externalID followed by upsert for new face
	_, deletes := notifier.Snapshot()
	foundDelete := false
	for _, d := range deletes {
		if d.personID == "EXT-REPLACE" && d.faceID == "" {
			foundDelete = true
			break
		}
	}
	if !foundDelete {
		t.Errorf("expected broadcastDelete for EXT-REPLACE during replacement")
	}

	// 4. Add until 10 faces
	for i := 2; i <= MaxFacesPerPerson; i++ {
		_, _, err := svc.AddFaceByExternalID(ctx, "EXT-REPLACE", fmt.Sprintf("face_%d.jpg", i), bytes.NewReader(testJPEGBytes), false)
		if err != nil {
			t.Fatalf("AddFaceByExternalID #%d failed: %v", i, err)
		}
	}
	// 11th should fail
	_, _, err = svc.AddFaceByExternalID(ctx, "EXT-REPLACE", "overflow.jpg", bytes.NewReader(testJPEGBytes), false)
	var appErr *apperr.Error
	if !errors.As(err, &appErr) || appErr.Code != "FACE_LIMIT_EXCEEDED" {
		t.Fatalf("expected FACE_LIMIT_EXCEEDED on 11th face, got %v", err)
	}

	// 5. Replace when at 10 faces should succeed and reset to 1
	faceReset, replacedReset, err := svc.AddFaceByExternalID(ctx, "EXT-REPLACE", "reset.jpg", bytes.NewReader(testJPEGBytes), true)
	if err != nil {
		t.Fatalf("replace at limit failed: %v", err)
	}
	if !replacedReset {
		t.Errorf("expected replaced=true on reset")
	}
	personReset, err := svc.GetPersonByExternalID(ctx, "EXT-REPLACE")
	if err != nil {
		t.Fatalf("GetPersonByExternalID after reset failed: %v", err)
	}
	if len(personReset.Faces) != 1 || personReset.Faces[0].ID != faceReset.ID {
		t.Fatalf("expected 1 face after reset, got %d", len(personReset.Faces))
	}
}

func TestExternalPersonFlow_ClearAndDelete(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	_, _, err := svc.UpsertPersonByExternalID(ctx, "EXT-CLEAR", "David", "API-Token")
	if err != nil {
		t.Fatalf("upsert person failed: %v", err)
	}

	f1, _, _ := svc.AddFaceByExternalID(ctx, "EXT-CLEAR", "1.jpg", bytes.NewReader(testJPEGBytes), false)
	f2, _, _ := svc.AddFaceByExternalID(ctx, "EXT-CLEAR", "2.jpg", bytes.NewReader(testJPEGBytes), false)
	p1Path := filepath.Join(svc.facesDir, f1.ImagePath)
	p2Path := filepath.Join(svc.facesDir, f2.ImagePath)

	// Set primary to f2
	pSet, err := svc.SetPrimaryFaceByExternalID(ctx, "EXT-CLEAR", f2.ID)
	if err != nil {
		t.Fatalf("SetPrimaryFaceByExternalID failed: %v", err)
	}
	if pSet.PrimaryFaceID == nil || *pSet.PrimaryFaceID != f2.ID {
		t.Errorf("expected primary_face_id to be f2.ID")
	}

	// Delete single face f1
	if err := svc.DeleteFaceByExternalID(ctx, "EXT-CLEAR", f1.ID); err != nil {
		t.Fatalf("DeleteFaceByExternalID failed: %v", err)
	}
	if _, err := os.Stat(p1Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected f1 file to be deleted")
	}

	// Clear remaining faces
	cleared, err := svc.ClearFacesByExternalID(ctx, "EXT-CLEAR")
	if err != nil {
		t.Fatalf("ClearFacesByExternalID failed: %v", err)
	}
	if cleared != 1 {
		t.Errorf("expected 1 face cleared, got %d", cleared)
	}
	if _, err := os.Stat(p2Path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected f2 file to be deleted")
	}

	pEmpty, err := svc.GetPersonByExternalID(ctx, "EXT-CLEAR")
	if err != nil {
		t.Fatalf("GetPersonByExternalID failed: %v", err)
	}
	if len(pEmpty.Faces) != 0 {
		t.Errorf("expected 0 faces, got %d", len(pEmpty.Faces))
	}
	if pEmpty.PrimaryFaceID != nil {
		t.Errorf("expected primary_face_id to be nil, got %v", pEmpty.PrimaryFaceID)
	}

	// Delete person
	if err := svc.DeletePersonByExternalID(ctx, "EXT-CLEAR"); err != nil {
		t.Fatalf("DeletePersonByExternalID failed: %v", err)
	}
	_, err = svc.GetPersonByExternalID(ctx, "EXT-CLEAR")
	if !errors.Is(err, ErrNotFound) {
		var aErr *apperr.Error
		if !errors.As(err, &aErr) || aErr.Code != "PERSON_NOT_FOUND" {
			t.Fatalf("expected PERSON_NOT_FOUND after deletion, got %v", err)
		}
	}
}
