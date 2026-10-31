package person

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

func TestBytesToFloat32_Aligned(t *testing.T) {
	const count = 512
	raw := make([]byte, count*4)
	expected := make([]float32, count)

	for i := 0; i < count; i++ {
		val := float32(i) * 1.25
		expected[i] = val
		binary.LittleEndian.PutUint32(raw[i*4:(i+1)*4], math.Float32bits(val))
	}

	got := bytesToFloat32(raw)
	if len(got) != count {
		t.Fatalf("expected length %d, got %d", count, len(got))
	}

	for i := 0; i < count; i++ {
		if got[i] != expected[i] {
			t.Fatalf("at index %d: expected %f, got %f", i, expected[i], got[i])
		}
	}

	// Verify that fast-path zero-copy actually points to the underlying byte slice
	if isLittleEndian && uintptr(unsafe.Pointer(&raw[0]))%unsafe.Alignof(float32(0)) == 0 {
		rawPtr := unsafe.Pointer(&raw[0])
		gotPtr := unsafe.Pointer(&got[0])
		if rawPtr != gotPtr {
			t.Errorf("expected zero-copy pointer match %p == %p", rawPtr, gotPtr)
		}
	}
}

func TestBytesToFloat32_UnalignedFallback(t *testing.T) {
	const count = 512
	// Allocate extra bytes so we can pick an unaligned offset
	buffer := make([]byte, count*4+8)

	offset := 1
	for uintptr(unsafe.Pointer(&buffer[offset]))%unsafe.Alignof(float32(0)) == 0 {
		offset++
	}

	unaligned := buffer[offset : offset+count*4]
	expected := make([]float32, count)

	for i := 0; i < count; i++ {
		val := float32(i) * 0.5
		expected[i] = val
		binary.LittleEndian.PutUint32(unaligned[i*4:(i+1)*4], math.Float32bits(val))
	}

	got := bytesToFloat32(unaligned)
	if len(got) != count {
		t.Fatalf("expected length %d, got %d", count, len(got))
	}

	for i := 0; i < count; i++ {
		if got[i] != expected[i] {
			t.Fatalf("at index %d: expected %f, got %f", i, expected[i], got[i])
		}
	}

	// In the unaligned case, got should NOT point to unaligned memory directly
	if unsafe.Pointer(&got[0]) == unsafe.Pointer(&unaligned[0]) {
		t.Errorf("unaligned slice should not point to original unaligned address")
	}
}

func TestBytesToFloat32_EdgeCases(t *testing.T) {
	if got := bytesToFloat32(nil); got != nil {
		t.Errorf("expected nil for nil input, got %v", got)
	}

	if got := bytesToFloat32([]byte{}); got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}

	if got := bytesToFloat32([]byte{1, 2, 3}); got != nil {
		t.Errorf("expected nil for length not divisible by 4, got %v", got)
	}
}

func BenchmarkBytesToFloat32_ZeroCopy(b *testing.B) {
	raw := make([]byte, 512*4)
	for i := 0; i < 512; i++ {
		binary.LittleEndian.PutUint32(raw[i*4:(i+1)*4], math.Float32bits(float32(i)))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bytesToFloat32(raw)
	}
}

func BenchmarkBytesToFloat32_UnalignedFallback(b *testing.B) {
	buf := make([]byte, 512*4+8)
	offset := 1
	for uintptr(unsafe.Pointer(&buf[offset]))%unsafe.Alignof(float32(0)) == 0 {
		offset++
	}
	unaligned := buf[offset : offset+512*4]
	for i := 0; i < 512; i++ {
		binary.LittleEndian.PutUint32(unaligned[i*4:(i+1)*4], math.Float32bits(float32(i)))
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = bytesToFloat32(unaligned)
	}
}

func TestGalleryItem_JSONContract(t *testing.T) {
	item := GalleryItem{
		FaceID:     "face_123",
		PersonID:   "emp_9527",
		PersonName: "张三",
		Features:   []float32{0.12, 0.34, 0.56},
	}

	rawJSON, err := json.Marshal(item)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(rawJSON, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, ok := m["features"]; !ok {
		t.Fatalf("missing required 'features' key in JSON payload: %s", string(rawJSON))
	}
	if _, ok := m["feature_vector"]; ok {
		t.Fatalf("deprecated 'feature_vector' key found in JSON payload: %s", string(rawJSON))
	}
}

func TestGetGallerySyncData(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	defer dbStore.Close()

	store := NewStore(dbStore)
	svc := NewService(store, tempDir, nil, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p := &Person{
		ID:       "p_test",
		PersonID: strPtr("EMP_001"),
		Name:     "李四",
	}
	if err := store.CreatePerson(ctx, p); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	rawFeat := make([]byte, 512*4)
	binary.LittleEndian.PutUint32(rawFeat[0:4], math.Float32bits(0.99))
	face := &Face{
		ID:            "face_test",
		PersonRowID:   "p_test",
		ImagePath:     "/img.jpg",
		FeatureVector: rawFeat,
	}
	if err := store.AddFace(ctx, face); err != nil {
		t.Fatalf("AddFace failed: %v", err)
	}

	items, err := svc.GetGallerySyncData(ctx)
	if err != nil {
		t.Fatalf("GetGallerySyncData failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	if items[0].FaceID != "face_test" {
		t.Errorf("expected FaceID 'face_test', got '%s'", items[0].FaceID)
	}
	if items[0].PersonID != "EMP_001" {
		t.Errorf("expected PersonID 'EMP_001', got '%s'", items[0].PersonID)
	}
	if len(items[0].Features) != 512 {
		t.Fatalf("expected 512 floats, got %d", len(items[0].Features))
	}
	if items[0].Features[0] != 0.99 {
		t.Errorf("expected Features[0] 0.99, got %f", items[0].Features[0])
	}
}

var (
	testJPEGBytes = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x01\x00`\x00`\x00\x00\xff\xdb\x00C\x00mock-jpeg-data")
	testPNGBytes  = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06mock-png-data")
	testWebPBytes = []byte("RIFF\x1a\x00\x00\x00WEBPVP8 \x0e\x00\x00\x00mock-webp-data")
)

// defaultTestExtractor returns a well-formed 512-dim embedding so tests that are
// not about feature extraction can still enroll faces.
func defaultTestExtractor() FaceFeatureExtractor {
	return &mockExtractor{features: make([]float32, faceFeatureDim)}
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	return NewService(NewStore(dbStore), tempDir, defaultTestExtractor(), nil, Options{}, zap.NewNop())
}

func requireDomainError(t *testing.T, err error, kind apperr.Kind, code string) {
	t.Helper()
	var domainErr *apperr.Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected a domain error with code %s, got %v", code, err)
	}
	if domainErr.Kind != kind || domainErr.Code != code {
		t.Fatalf("expected %s/%s, got %s/%s", kind, code, domainErr.Kind, domainErr.Code)
	}
}

// TestCreatePerson_DuplicatePersonIDIsConflict keeps the business-key collision mapped to
// its own error code, so integrators can tell it apart from a row-id collision.
func TestCreatePerson_DuplicatePersonIDIsConflict(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if _, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	_, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "李四"})
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_ALREADY_EXISTS")
}

// TestCreatePerson_DefaultsPersonIDToNil verifies that omitting personId leaves it nil
// without falling back to the primary key id.
func TestCreatePerson_DefaultsPersonIDToNil(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "王五"})
	if err != nil {
		t.Fatalf("CreatePerson without personId failed: %v", err)
	}
	if p.ID == "" {
		t.Fatal("expected non-empty primary key id")
	}
	if strings.HasPrefix(p.ID, "person_") {
		t.Fatalf("primary key id %q must not have person_ prefix", p.ID)
	}
	if p.PersonID != nil {
		t.Fatalf("expected personId to be nil when omitted, got %q", *p.PersonID)
	}
}

// TestUpdatePerson_RejectsPersonIDChange makes person_id immutable at the service layer:
// it is the key external systems hold, so a silent no-op would let integrators believe a
// reassignment succeeded.
func TestUpdatePerson_RejectsPersonIDChange(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	other := "EMP_OTHER"
	_, err = svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &other})
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_IMMUTABLE")

	got, err := svc.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.PersonID == nil || *got.PersonID != "EMP_1" {
		t.Errorf("person_id must survive a rejected reassignment, got %v", got.PersonID)
	}

	// Re-sending the identical value is a no-op rather than a conflict.
	same := "EMP_1"
	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &same}); err != nil {
		t.Fatalf("idempotent personId resend must succeed, got %v", err)
	}
}

func TestUpdatePerson_AllowsBindingWhenEmpty(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "无工号员工"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if created.PersonID != nil {
		t.Fatalf("expected initial personId to be nil, got %v", created.PersonID)
	}

	bound := "EMP_BIND_1"
	updated, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &bound})
	if err != nil {
		t.Fatalf("UpdatePerson binding failed: %v", err)
	}
	if updated.PersonID == nil || *updated.PersonID != bound {
		t.Fatalf("expected updated personId %q, got %v", bound, updated.PersonID)
	}

	// Once bound, changing it must fail
	other := "EMP_BIND_2"
	_, err = svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &other})
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_IMMUTABLE")
}

// TestUpdatePerson_RejectsBlankName replaces the previous silent ignore: a blank name used
// to return 200 with the old value still in place, which is indistinguishable from success.
func TestUpdatePerson_RejectsBlankName(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	blank := ""
	_, err = svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{Name: &blank})
	requireDomainError(t, err, apperr.KindInvalid, "NAME_REQUIRED")

	got, err := svc.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if got.Name != "张三" {
		t.Errorf("name must survive a rejected blank update, got %q", got.Name)
	}
}

// TestNewID_IsUnique pins the replacement for the time.Now().UnixNano() row ids, which
// could collide and surface as a duplicate-primary-key 500.
// TestNewID_IsUnique pins the row-id generator used for every person and face
// primary key: ids must be unique without coordination.
func TestNewID_IsUnique(t *testing.T) {
	const draws = 1000
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		id := idutil.New()
		if id == "" {
			t.Fatal("idutil.New returned an empty id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("idutil.New repeated %q after %d draws", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestAddFace_IDsAreUniqueUnderConcurrency exercises the case the nanosecond clock could
// not survive: simultaneous enrolments racing for the same primary key.
func TestAddFace_IDsAreUniqueUnderConcurrency(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	const workers = 20
	ids := make([]string, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			face, err := svc.AddFaceWithReader(ctx, created.ID, fmt.Sprintf("enroll_%d.jpg", i), bytes.NewReader(testJPEGBytes))
			if err != nil {
				errs[i] = err
				return
			}
			ids[i] = face.ID
		}(i)
	}
	wg.Wait()

	seen := make(map[string]struct{}, workers)
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("concurrent AddFace #%d failed: %v", i, errs[i])
		}
		if _, dup := seen[ids[i]]; dup {
			t.Fatalf("concurrent enrolments produced duplicate face id %q", ids[i])
		}
		seen[ids[i]] = struct{}{}
	}
}

// TestAddFace_RejectsInvalidFileType verifies that only supported image types are accepted.
func TestAddFace_RejectsInvalidFileType(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	rejected := []string{
		"script.sh",
		"binary.exe",
		"document.pdf",
		"face.txt",
		"archive.zip",
	}
	for _, filename := range rejected {
		_, err := svc.AddFaceWithReader(ctx, created.ID, filename, bytes.NewReader([]byte("some-data")))
		if err == nil {
			t.Errorf("filename %q must be rejected", filename)
			continue
		}
		requireDomainError(t, err, apperr.KindInvalid, "INVALID_FILE_TYPE")
	}

	// Rejection when file has valid extension but invalid content
	_, err = svc.AddFaceWithReader(ctx, created.ID, "fake.jpg", bytes.NewReader([]byte("not an image")))
	if err == nil {
		t.Errorf("fake.jpg with non-image content must be rejected")
	}
	requireDomainError(t, err, apperr.KindInvalid, "INVALID_FILE_TYPE")

	faces, _, err := svc.ListFaces(ctx, created.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListFaces failed: %v", err)
	}
	if len(faces) != 0 {
		t.Errorf("rejected enrolments must not persist, got %d faces", len(faces))
	}

	if _, err := svc.AddFaceWithReader(ctx, created.ID, "face_01.jpg", bytes.NewReader(testJPEGBytes)); err != nil {
		t.Fatalf("a valid jpg must be accepted, got %v", err)
	}

	// Nil file header must be rejected with FILE_REQUIRED
	_, err = svc.AddFace(ctx, created.ID, nil)
	requireDomainError(t, err, apperr.KindInvalid, "FILE_REQUIRED")
}

// TestListFaces_MissingPersonReturnsNotFound keeps GET .../faces consistent with the rest of
// the resource: an empty 200 would be indistinguishable from "no photos enrolled".
func TestListFaces_MissingPersonReturnsNotFound(t *testing.T) {
	svc := newTestService(t)

	_, _, err := svc.ListFaces(context.Background(), "ghost", 0, 0)
	requireDomainError(t, err, apperr.KindNotFound, "PERSON_NOT_FOUND")
}

// TestSetPrimaryFace_DistinguishesFailureModes pins the per-condition error codes:
// a missing person, a missing face and a face owned by somebody else are three
// different problems and must not collapse into one code.
func TestSetPrimaryFace_DistinguishesFailureModes(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	owner, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	other, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Other"})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	face, err := svc.AddFaceWithReader(ctx, owner.ID, "owner.jpg", bytes.NewReader(testJPEGBytes))
	if err != nil {
		t.Fatalf("enroll face: %v", err)
	}

	t.Run("missing person", func(t *testing.T) {
		_, err := svc.SetPrimaryFace(ctx, "ghost", face.ID)
		requireDomainError(t, err, apperr.KindNotFound, "PERSON_NOT_FOUND")
	})

	t.Run("missing face", func(t *testing.T) {
		_, err := svc.SetPrimaryFace(ctx, owner.ID, "ghost")
		requireDomainError(t, err, apperr.KindNotFound, "FACE_NOT_FOUND")
	})

	t.Run("face owned by another person", func(t *testing.T) {
		_, err := svc.SetPrimaryFace(ctx, other.ID, face.ID)
		requireDomainError(t, err, apperr.KindInvalid, "INVALID_FACE_OWNER")
	})

	t.Run("blank face id", func(t *testing.T) {
		_, err := svc.SetPrimaryFace(ctx, owner.ID, "   ")
		requireDomainError(t, err, apperr.KindInvalid, "FACE_ID_REQUIRED")
	})

	t.Run("success", func(t *testing.T) {
		updated, err := svc.SetPrimaryFace(ctx, owner.ID, face.ID)
		if err != nil {
			t.Fatalf("SetPrimaryFace failed: %v", err)
		}
		if updated.PrimaryFaceID == nil || *updated.PrimaryFaceID != face.ID {
			t.Fatalf("expected primary face %q, got %v", face.ID, updated.PrimaryFaceID)
		}
	})
}

// TestFaceSubresourcesAreScopedToOwningPerson pins that a face cannot be reached
// or removed through another person's path segment.
func TestFaceSubresourcesAreScopedToOwningPerson(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	owner, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Owner"})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	other, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Other"})
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	face, err := svc.AddFaceWithReader(ctx, owner.ID, "owner.jpg", bytes.NewReader(testJPEGBytes))
	if err != nil {
		t.Fatalf("enroll face: %v", err)
	}

	_, _, err = svc.OpenFaceImage(ctx, other.ID, face.ID)
	requireDomainError(t, err, apperr.KindNotFound, "FACE_NOT_FOUND")
	requireDomainError(t, svc.DeleteFace(ctx, other.ID, face.ID), apperr.KindNotFound, "FACE_NOT_FOUND")

	// The owner's own path still resolves, proving the rejection above was scoping
	// and not a blanket failure.
	file, _, err := svc.OpenFaceImage(ctx, owner.ID, face.ID)
	if err != nil {
		t.Fatalf("owner should resolve their own face: %v", err)
	}
	file.Close()
}

// TestAuditActionNamespaces keeps the <domain>.<resource>.<verb> naming contract that
// the audit query relies on. The audit trail itself is produced by the HTTP handler, so
// the recording assertions live in the handler tests.
func TestAuditActionNamespaces(t *testing.T) {
	for _, action := range []string{
		ActionPersonCreate, ActionPersonUpdate, ActionPersonDelete,
		ActionPersonFaceAdd, ActionPersonFaceDelete,
	} {
		if !strings.HasPrefix(action, "person.") {
			t.Errorf("audit action %q must be namespaced under person.", action)
		}
	}
}

// TestUpdatePersonRejectsBusinessKeyReassignment pins the immutability rule at the
// service boundary: a rejected reassignment returns a domain error and leaves the
// stored key untouched.
func TestUpdatePersonRejectsBusinessKeyReassignment(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_1"), Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	other := "EMP_OTHER"
	_, err = svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &other})
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_IMMUTABLE")

	stored, err := svc.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if stored.PersonID == nil || *stored.PersonID != "EMP_1" {
		t.Errorf("expected the business key to stay EMP_1, got %v", stored.PersonID)
	}
}

// TestOpenFaceImageStaysInsideFacesDirectory pins the containment contract of the
// only read path for enrolled photos: reads go through an os.Root handle, so neither a
// relative escape nor a symlink inside the faces directory can reach outside it.
func TestOpenFaceImageStaysInsideFacesDirectory(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "PathTest"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	face, err := svc.AddFaceWithReader(ctx, p.ID, "avatar.png", bytes.NewReader(testPNGBytes))
	if err != nil {
		t.Fatalf("AddFaceWithReader failed: %v", err)
	}
	if filepath.IsAbs(face.ImagePath) {
		t.Errorf("expected ImagePath in memory/DB to be relative, got: %s", face.ImagePath)
	}

	// A relative name resolves to the enrolled photo itself.
	file, info, err := svc.OpenFaceImage(ctx, p.ID, face.ID)
	if err != nil {
		t.Fatalf("OpenFaceImage failed: %v", err)
	}
	if info.Name() != face.ImagePath {
		t.Errorf("expected %q, got %q", face.ImagePath, info.Name())
	}
	if info.IsDir() {
		t.Error("expected a regular file, got a directory")
	}
	file.Close()

	// Rows from an older deployment may hold an absolute path; it resolves only when
	// it lives inside the faces directory.
	legacy := &Face{
		ID:            "legacy_face_id",
		PersonRowID:   p.ID,
		ImagePath:     filepath.Join(svc.facesDir, "legacy_photo.jpg"),
		FeatureVector: make([]byte, 2048),
	}
	if err := os.WriteFile(legacy.ImagePath, testPNGBytes, 0o644); err != nil {
		t.Fatalf("write legacy photo: %v", err)
	}
	if err := svc.store.AddFace(ctx, legacy); err != nil {
		t.Fatalf("AddFace legacy failed: %v", err)
	}
	legacyFile, _, err := svc.OpenFaceImage(ctx, p.ID, legacy.ID)
	if err != nil {
		t.Fatalf("expected a legacy absolute path inside the faces directory to resolve: %v", err)
	}
	legacyFile.Close()

	// Absolute paths outside the faces directory are refused.
	escaping := &Face{
		ID:            "escape_face_id",
		PersonRowID:   p.ID,
		ImagePath:     "/etc/shadow",
		FeatureVector: make([]byte, 2048),
	}
	if err := svc.store.AddFace(ctx, escaping); err != nil {
		t.Fatalf("AddFace escaping failed: %v", err)
	}
	if _, _, err := svc.OpenFaceImage(ctx, p.ID, escaping.ID); err == nil {
		t.Error("expected an absolute path outside the faces directory to be rejected")
	}

	// A symlink planted inside the faces directory must not be followed out of it.
	symlinkFace := &Face{
		ID:            "symlink_face_id",
		PersonRowID:   p.ID,
		ImagePath:     "planted.jpg",
		FeatureVector: make([]byte, 2048),
	}
	if err := svc.store.AddFace(ctx, symlinkFace); err != nil {
		t.Fatalf("AddFace symlink failed: %v", err)
	}
	if err := os.Symlink("/etc/shadow", filepath.Join(svc.facesDir, "planted.jpg")); err != nil {
		t.Skipf("symlinks unavailable in this environment: %v", err)
	}
	if _, _, err := svc.OpenFaceImage(ctx, p.ID, symlinkFace.ID); err == nil {
		t.Error("expected os.Root to refuse a symlink pointing outside the faces directory")
	}

	// A database row whose file vanished is a 404 on the image, not a 500.
	missing := &Face{
		ID:            "missing_face_id",
		PersonRowID:   p.ID,
		ImagePath:     "gone.jpg",
		FeatureVector: make([]byte, 2048),
	}
	if err := svc.store.AddFace(ctx, missing); err != nil {
		t.Fatalf("AddFace missing failed: %v", err)
	}
	_, _, err = svc.OpenFaceImage(ctx, p.ID, missing.ID)
	requireDomainError(t, err, apperr.KindNotFound, "IMAGE_NOT_FOUND")
}

type mockExtractor struct {
	features []float32
	err      error
}

func (m *mockExtractor) ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.features, nil
}

func TestAddFace_WithExtractor_Success(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	expectedFeats := make([]float32, 512)
	for i := range expectedFeats {
		expectedFeats[i] = float32(i) * 0.001
	}

	extractor := &mockExtractor{features: expectedFeats}
	svc := NewService(NewStore(dbStore), tempDir, extractor, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("P_EXTRACT_1"), Name: "特征提取测试员"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// 构造简单合法 JPEG 头部与内容
	fakeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0xaa}, 100)...)
	face, err := svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(fakeJPEG))
	if err != nil {
		t.Fatalf("AddFaceWithReader failed: %v", err)
	}

	if len(face.FeatureVector) != 2048 {
		t.Fatalf("expected 2048 bytes feature vector, got %d", len(face.FeatureVector))
	}

	decodedFloats := bytesToFloat32(face.FeatureVector)
	if len(decodedFloats) != 512 {
		t.Fatalf("expected 512 decoded floats, got %d", len(decodedFloats))
	}
	if math.Abs(float64(decodedFloats[10]-expectedFeats[10])) > 1e-6 {
		t.Errorf("expected float %f, got %f", expectedFeats[10], decodedFloats[10])
	}

	// 验证磁盘物理文件切实存在
	fullPath := filepath.Join(tempDir, face.ImagePath)
	if _, err := os.Stat(fullPath); err != nil {
		t.Fatalf("expected saved image on disk at %s: %v", fullPath, err)
	}

	// 验证 GetGallerySyncData 包含真实特征与 ImagePath
	items, err := svc.GetGallerySyncData(ctx)
	if err != nil {
		t.Fatalf("GetGallerySyncData failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ImagePath != face.ImagePath {
		t.Errorf("expected image_path %s, got %s", face.ImagePath, items[0].ImagePath)
	}
	if len(items[0].Features) != 512 {
		t.Fatalf("expected 512 features, got %d", len(items[0].Features))
	}
}

func TestAddFace_WithExtractor_NoFaceDetected(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	extractor := &mockExtractor{err: fmt.Errorf("%w: mock extractor", ErrNoFaceDetected)}
	svc := NewService(NewStore(dbStore), tempDir, extractor, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("P_EXTRACT_2"), Name: "失败测试员"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	fakeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0xbb}, 100)...)
	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(fakeJPEG))
	if err == nil {
		t.Fatal("expected error when no face is detected")
	}

	requireDomainError(t, err, apperr.KindInvalid, "NO_FACE_DETECTED")

	// 验证磁盘上不得残留任何图片文件
	entries, _ := os.ReadDir(tempDir)
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".jpg") {
			t.Errorf("unexpected image file found on disk after failed extraction: %s", entry.Name())
		}
	}

	// 验证数据库中不得存在该人脸
	faces, _, err := svc.ListFaces(ctx, p.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListFaces failed: %v", err)
	}
	if len(faces) != 0 {
		t.Fatalf("expected 0 faces, got %d", len(faces))
	}
}

func TestAddFace_WithExtractor_DimMismatch(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	extractor := &mockExtractor{features: make([]float32, 256)} // 维度不符
	svc := NewService(NewStore(dbStore), tempDir, extractor, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("P_EXTRACT_3"), Name: "维度测试员"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	fakeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0xcc}, 100)...)
	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(fakeJPEG))
	if err == nil {
		t.Fatal("expected error on dimension mismatch")
	}

	requireDomainError(t, err, apperr.KindInternal, "FEATURE_DIM_MISMATCH")
}

// TestAddFace_WithoutExtractorFails pins the removal of the zero-vector
// placeholder: an enrollment without a working extractor must fail loudly rather
// than persist an all-zero embedding that is indistinguishable from a real one.
func TestAddFace_WithoutExtractorFails(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	svc := NewService(NewStore(dbStore), tempDir, nil, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "无提取器"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(testJPEGBytes))
	requireDomainError(t, err, apperr.KindInternal, "FEATURE_EXTRACTOR_UNAVAILABLE")

	faces, total, err := svc.ListFaces(ctx, p.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListFaces failed: %v", err)
	}
	if total != 0 || len(faces) != 0 {
		t.Fatalf("expected no face to be persisted, got total=%d len=%d", total, len(faces))
	}
}

// TestAddFace_ExtractorFailureIsNotBlamedOnThePhoto keeps a genuine engine failure
// distinct from an unusable upload: only ErrNoFaceDetected may answer 422.
func TestAddFace_ExtractorFailureIsNotBlamedOnThePhoto(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	extractor := &mockExtractor{err: errors.New("engine crashed")}
	svc := NewService(NewStore(dbStore), tempDir, extractor, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "引擎故障"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(testJPEGBytes))
	requireDomainError(t, err, apperr.KindInternal, "FEATURE_EXTRACT_FAILED")
}

// TestAddFace_UndecodableImageIsAClientError 确认「图片解不开」被归咎于上传（400），
// 而不是像引擎故障那样报 500：否则前端只能把用户输入错误当成服务端崩溃。
func TestAddFace_UndecodableImageIsAClientError(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	extractor := &mockExtractor{err: fmt.Errorf("%w: image: unknown format", ErrUnsupportedImageFormat)}
	svc := NewService(NewStore(dbStore), tempDir, extractor, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "格式错误"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.webp", bytes.NewReader(testJPEGBytes))
	requireDomainError(t, err, apperr.KindInvalid, "UNSUPPORTED_IMAGE_FORMAT")

	faces, total, err := svc.ListFaces(ctx, p.ID, 0, 0)
	if err != nil {
		t.Fatalf("ListFaces failed: %v", err)
	}
	if total != 0 || len(faces) != 0 {
		t.Fatalf("expected no face to be persisted, got total=%d len=%d", total, len(faces))
	}
}

func strPtr(s string) *string {
	return &s
}

type mockGalleryNotifier struct {
	upserts []GalleryItem
	deletes []struct {
		faceID   string
		personID string
	}
}

func (m *mockGalleryNotifier) BroadcastUpsert(ctx context.Context, item GalleryItem) error {
	m.upserts = append(m.upserts, item)
	return nil
}

func (m *mockGalleryNotifier) BroadcastDelete(ctx context.Context, faceID, personID string) error {
	m.deletes = append(m.deletes, struct {
		faceID   string
		personID string
	}{faceID: faceID, personID: personID})
	return nil
}

func TestGalleryNotifier_AddUpdateDelete(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	notifier := &mockGalleryNotifier{}
	svc := NewService(NewStore(dbStore), tempDir, defaultTestExtractor(), notifier, Options{}, zap.NewNop())
	ctx := context.Background()

	// 1. Create Person
	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("NOTIFY_P1"), Name: "通知测试员"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// 2. Add Face
	fakeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0xdd}, 50)...)
	face, err := svc.AddFaceWithReader(ctx, p.ID, "face.jpg", bytes.NewReader(fakeJPEG))
	if err != nil {
		t.Fatalf("AddFaceWithReader failed: %v", err)
	}

	if len(notifier.upserts) != 1 {
		t.Fatalf("expected 1 upsert notification, got %d", len(notifier.upserts))
	}
	if notifier.upserts[0].FaceID != face.ID || notifier.upserts[0].PersonID != "NOTIFY_P1" {
		t.Errorf("upsert mismatch: %+v", notifier.upserts[0])
	}

	// 3. Update Person Name
	newName := "新名字"
	_, err = svc.UpdatePerson(ctx, p.ID, UpdatePersonRequest{Name: &newName})
	if err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}
	if len(notifier.upserts) != 2 {
		t.Fatalf("expected 2 upserts after name update, got %d", len(notifier.upserts))
	}
	if notifier.upserts[1].PersonName != newName {
		t.Errorf("expected updated name %s, got %s", newName, notifier.upserts[1].PersonName)
	}

	// 4. Delete Face
	if err := svc.DeleteFace(ctx, p.ID, face.ID); err != nil {
		t.Fatalf("DeleteFace failed: %v", err)
	}
	if len(notifier.deletes) != 1 {
		t.Fatalf("expected 1 delete notification, got %d", len(notifier.deletes))
	}
	if notifier.deletes[0].faceID != face.ID || notifier.deletes[0].personID != "NOTIFY_P1" {
		t.Errorf("delete mismatch: %+v", notifier.deletes[0])
	}

	// 5. Delete Person
	if err := svc.DeletePerson(ctx, p.ID); err != nil {
		t.Fatalf("DeletePerson failed: %v", err)
	}
	if len(notifier.deletes) != 2 {
		t.Fatalf("expected 2 delete notifications, got %d", len(notifier.deletes))
	}
	if notifier.deletes[1].faceID != "" || notifier.deletes[1].personID != "NOTIFY_P1" {
		t.Errorf("person delete mismatch: %+v", notifier.deletes[1])
	}
}

// TestUpdatePersonNoOpIsNotAWrite pins that a payload repeating the stored values does
// not bump updated_at and does not re-broadcast the person's gallery entry.
func TestUpdatePersonNoOpIsNotAWrite(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	notifier := &mockGalleryNotifier{}
	svc := NewService(NewStore(dbStore), tempDir, defaultTestExtractor(), notifier, Options{}, zap.NewNop())
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_SAME"), Name: "同名"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if notifier.upserts != nil {
		t.Fatalf("CreatePerson must not publish to the gallery, got %d upserts", len(notifier.upserts))
	}

	before, err := svc.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}

	sameName := "同名"
	updated, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{
		Name:     &sameName,
		PersonID: strPtr("EMP_SAME"),
	})
	if err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}
	if !updated.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("expected updated_at to stay %v on a no-op update, got %v", before.UpdatedAt, updated.UpdatedAt)
	}
	if len(notifier.upserts) != 0 {
		t.Errorf("expected no gallery re-publish on a no-op update, got %d", len(notifier.upserts))
	}
}

// TestUpdatePersonReturnsTheSameProjectionAsGet pins that a write answers with the
// preloaded face list GET returns, so a client replacing its cached person with the
// response cannot lose the faces.
func TestUpdatePersonReturnsTheSameProjectionAsGet(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_FACES"), Name: "有脸"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if _, err := svc.AddFaceWithReader(ctx, created.ID, "a.jpg", bytes.NewReader(testJPEGBytes)); err != nil {
		t.Fatalf("AddFaceWithReader failed: %v", err)
	}

	newName := "改名了"
	updated, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{Name: &newName})
	if err != nil {
		t.Fatalf("UpdatePerson failed: %v", err)
	}
	got, err := svc.GetPerson(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetPerson failed: %v", err)
	}
	if len(updated.Faces) != len(got.Faces) || len(updated.Faces) != 1 {
		t.Errorf("expected the update response to carry the face list, got %d (GET has %d)",
			len(updated.Faces), len(got.Faces))
	}
}

// stubKeyBackfiller records the business-key backfill requests the person domain makes.
type stubKeyBackfiller struct {
	calls []struct {
		personRowID string
		externalID  string
	}
	err error
}

func (s *stubKeyBackfiller) BackfillPersonExternalID(_ context.Context, personRowID, externalID string) (int64, error) {
	s.calls = append(s.calls, struct {
		personRowID string
		externalID  string
	}{personRowID: personRowID, externalID: externalID})
	if s.err != nil {
		return 0, s.err
	}
	return 1, nil
}

// TestUpdatePersonBackfillsRecognitionKeysWhenBinding pins the retro-fill contract: only
// the transition from "no business key" to "keyed" reaches the record domain.
func TestUpdatePersonBackfillsRecognitionKeysWhenBinding(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	backfiller := &stubKeyBackfiller{}
	svc := NewService(NewStore(dbStore), tempDir, defaultTestExtractor(), nil,
		Options{RecognitionKeyBackfiller: backfiller}, zap.NewNop())
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "未绑定"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if len(backfiller.calls) != 0 {
		t.Fatalf("creation must not trigger a backfill, got %d calls", len(backfiller.calls))
	}

	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: strPtr("EMP_BIND")}); err != nil {
		t.Fatalf("binding UpdatePerson failed: %v", err)
	}
	if len(backfiller.calls) != 1 {
		t.Fatalf("expected exactly 1 backfill call after binding, got %d", len(backfiller.calls))
	}
	if call := backfiller.calls[0]; call.personRowID != created.ID || call.externalID != "EMP_BIND" {
		t.Errorf("unexpected backfill arguments: %+v", call)
	}

	// A rename does not change the business key, so it must not re-run the backfill.
	newName := "已绑定"
	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{Name: &newName}); err != nil {
		t.Fatalf("rename UpdatePerson failed: %v", err)
	}
	if len(backfiller.calls) != 1 {
		t.Errorf("expected the rename to leave the backfill untouched, got %d calls", len(backfiller.calls))
	}
}

// TestAddFaceRequiresBusinessKeyWhenPolicyEnabled pins the enrollment gate: with the
// policy on, an unreportable face cannot enter the gallery at all.
func TestAddFaceRequiresBusinessKeyWhenPolicyEnabled(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	svc := NewService(NewStore(dbStore), tempDir, defaultTestExtractor(), nil,
		Options{RequirePersonIDForEnrollment: true}, zap.NewNop())
	ctx := context.Background()

	unkeyed, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "未绑定"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	_, err = svc.AddFaceWithReader(ctx, unkeyed.ID, "a.jpg", bytes.NewReader(testJPEGBytes))
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_REQUIRED_FOR_ENROLLMENT")

	keyed, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: strPtr("EMP_OK"), Name: "已绑定"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	if _, err := svc.AddFaceWithReader(ctx, keyed.ID, "b.jpg", bytes.NewReader(testJPEGBytes)); err != nil {
		t.Fatalf("expected a keyed person to enroll normally: %v", err)
	}
}

// TestListPersonsSearchTreatsWildcardsAsText pins that % and _ in a keyword match
// themselves instead of turning the search into a full-table wildcard.
func TestListPersonsSearchTreatsWildcardsAsText(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	for _, name := range []string{"50%折扣", "502员工", "A_B", "AXB"} {
		if _, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: name}); err != nil {
			t.Fatalf("CreatePerson(%q) failed: %v", name, err)
		}
	}

	for _, tc := range []struct {
		search string
		want   []string
	}{
		{search: "50%", want: []string{"50%折扣"}},
		{search: "A_B", want: []string{"A_B"}},
		{search: "502", want: []string{"502员工"}},
	} {
		got, total, err := svc.ListPersons(ctx, 20, 0, tc.search)
		if err != nil {
			t.Fatalf("ListPersons(%q) failed: %v", tc.search, err)
		}
		if int(total) != len(tc.want) {
			t.Errorf("search %q: expected %d matches, got %d (%+v)", tc.search, len(tc.want), total, got)
			continue
		}
		for i, want := range tc.want {
			if got[i].Name != want {
				t.Errorf("search %q: expected %q at %d, got %q", tc.search, want, i, got[i].Name)
			}
		}
	}
}

// TestGetGallerySyncDataWalksEveryPage guards the paged cold-start sync: a gallery
// larger than one page must not be truncated to the first page.
func TestGetGallerySyncDataWalksEveryPage(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	svc := NewService(store, tempDir, nil, nil, Options{}, zap.NewNop())
	ctx := context.Background()

	// One more person than a single page holds, with the only face on the last one:
	// a sync that stops after the first page returns no items at all.
	const total = gallerySyncPageSize + 5
	var last *Person
	for range total {
		p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "分页员"})
		if err != nil {
			t.Fatalf("CreatePerson failed: %v", err)
		}
		last = p
	}
	if err := store.AddFace(ctx, &Face{
		ID:            "face_last_page",
		PersonRowID:   last.ID,
		ImagePath:     "last.jpg",
		FeatureVector: make([]byte, faceFeatureDim*4),
	}); err != nil {
		t.Fatalf("AddFace failed: %v", err)
	}

	items, err := svc.GetGallerySyncData(ctx)
	if err != nil {
		t.Fatalf("GetGallerySyncData failed: %v", err)
	}
	if len(items) != 1 || items[0].FaceID != "face_last_page" {
		t.Fatalf("expected the face from the last page, got %d items: %+v", len(items), items)
	}
}

func TestParsePersonImportCSV(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantRows  int
		wantError error
		wantLine  int
	}{
		{
			name:     "BOM and reordered headers",
			input:    "personId,name\nEMP-1,Alice\n",
			wantRows: 1,
			wantLine: 2,
		},
		{
			name:      "empty file",
			input:     "",
			wantError: errPersonImportEmpty,
		},
		{
			name:      "invalid header",
			input:     "name\nAlice\n",
			wantError: errPersonImportHeader,
		},
		{
			name:      "empty data rows",
			input:     "name,personId\n",
			wantError: errPersonImportEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := tt.input
			if tt.name == "BOM and reordered headers" {
				input = "\uFEFF" + input
			}
			rows, err := parsePersonImportCSV(strings.NewReader(input))
			if tt.wantError != nil {
				if err != tt.wantError {
					t.Fatalf("expected error %v, got %v", tt.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if len(rows) != tt.wantRows || rows[0].line != tt.wantLine {
				t.Fatalf("unexpected rows: %+v", rows)
			}
		})
	}
}

func TestValidatePersonImportRows(t *testing.T) {
	rows := []personImportRow{
		{line: 2, name: "Alice", personID: "EMP-1"},
		{line: 3, name: "Bob", personID: "EMP-1"},
		{line: 4, name: "No ID"},
		{line: 5, name: "", personID: "EMP-2"},
	}
	entries, validated := validatePersonImportRows(rows, map[string]struct{}{"EMP-2": {}})
	if len(entries) != 1 || entries[0].line != 4 {
		t.Fatalf("expected only empty-personId row to be importable, got %+v", entries)
	}
	if len(validated[0].issues) == 0 || validated[0].issues[0].Code != "IMPORT_DUPLICATE_PERSON_ID" {
		t.Fatalf("expected duplicate issue on first row, got %+v", validated[0].issues)
	}
	if len(validated[3].issues) == 0 || validated[3].issues[0].Code != "NAME_REQUIRED" {
		t.Fatalf("expected name issue on fourth row, got %+v", validated[3].issues)
	}
}

func TestServicePersonImportPreviewAndCommit(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer dbStore.Close()

	store := NewStore(dbStore)
	service := NewService(store, tempDir, nil, nil, Options{}, zap.NewNop())
	ctx := context.Background()
	csv := "name,personId\nAlice,EMP-1\nBob,EMP-1\nNo ID,\n"

	preview, err := service.PreviewPersonImport(ctx, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("preview failed: %v", err)
	}
	if preview.Importable != 1 || preview.Failed != 2 {
		t.Fatalf("unexpected preview summary: %+v", preview)
	}
	var count int64
	if err := dbStore.DB().Model(&Person{}).Count(&count).Error; err != nil {
		t.Fatalf("count persons: %v", err)
	}
	if count != 0 {
		t.Fatalf("preview created %d persons", count)
	}

	result, err := service.CommitPersonImport(ctx, strings.NewReader(csv))
	if err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	if result.Imported != 1 || result.Failed != 2 {
		t.Fatalf("unexpected commit summary: %+v", result)
	}
	if err := dbStore.DB().Model(&Person{}).Count(&count).Error; err != nil {
		t.Fatalf("count committed persons: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one committed person, got %d", count)
	}
}
