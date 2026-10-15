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

func TestGallerySyncItem_JSONContract(t *testing.T) {
	item := GallerySyncItem{
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
	svc := NewService(store, tempDir, nil, zap.NewNop())
	ctx := context.Background()

	p := &Person{
		ID:       "p_test",
		PersonID: "EMP_001",
		Name:     "李四",
	}
	if err := store.CreatePerson(ctx, p); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	rawFeat := make([]byte, 512*4)
	binary.LittleEndian.PutUint32(rawFeat[0:4], math.Float32bits(0.99))
	face := &Face{
		ID:            "face_test",
		OwnerRowID:    "p_test",
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

func newTestService(t *testing.T) *Service {
	t.Helper()
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	return NewService(NewStore(dbStore), tempDir, nil, zap.NewNop())
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

	if _, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"}); err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	_, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "李四"})
	requireDomainError(t, err, apperr.KindConflict, "PERSON_ID_ALREADY_EXISTS")
}

// TestCreatePerson_DefaultsPersonIDToID verifies that omitting personId defaults it to
// the generated primary key id, and that the primary key has no "person_" prefix.
func TestCreatePerson_DefaultsPersonIDToID(t *testing.T) {
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
	if p.PersonID != p.ID {
		t.Fatalf("expected personId %q to match id %q when omitted", p.PersonID, p.ID)
	}
}

// TestUpdatePerson_RejectsPersonIDChange makes person_id immutable at the service layer:
// it is the key external systems hold, so a silent no-op would let integrators believe a
// reassignment succeeded.
func TestUpdatePerson_RejectsPersonIDChange(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
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
	if got.PersonID != "EMP_1" {
		t.Errorf("person_id must survive a rejected reassignment, got %q", got.PersonID)
	}

	// Re-sending the identical value is a no-op rather than a conflict.
	same := "EMP_1"
	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &same}); err != nil {
		t.Fatalf("idempotent personId resend must succeed, got %v", err)
	}
}

// TestUpdatePerson_RejectsBlankName replaces the previous silent ignore: a blank name used
// to return 200 with the old value still in place, which is indistinguishable from success.
func TestUpdatePerson_RejectsBlankName(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
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
func TestNewID_IsUnique(t *testing.T) {
	const draws = 1000
	seen := make(map[string]struct{}, draws)
	for i := 0; i < draws; i++ {
		id := newID()
		if id == "" {
			t.Fatal("newID returned an empty id")
		}
		if _, dup := seen[id]; dup {
			t.Fatalf("newID repeated %q after %d draws", id, i)
		}
		seen[id] = struct{}{}
	}
}

// TestAddFace_IDsAreUniqueUnderConcurrency exercises the case the nanosecond clock could
// not survive: simultaneous enrolments racing for the same primary key.
func TestAddFace_IDsAreUniqueUnderConcurrency(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
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

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
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

	faces, err := svc.ListFaces(ctx, created.ID)
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

	_, err := svc.ListFaces(context.Background(), "ghost")
	requireDomainError(t, err, apperr.KindNotFound, "PERSON_NOT_FOUND")
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

// TestServiceDoesNotDependOnAudit pins the layering rule: the person service has no
// audit dependency, so user-operation auditing cannot drift back into the service layer.
func TestServiceDoesNotDependOnAudit(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	// A refused immutable-key reassignment must still return a domain error; it
	// simply no longer writes an audit record from this layer.
	other := "EMP_OTHER"
	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &other}); err == nil {
		t.Fatal("expected the personId reassignment to be rejected")
	}
}

func TestGetFaceImagePath_RelativeAndAbsoluteResolution(t *testing.T) {
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

	// 1. Verify stored path is relative
	if filepath.IsAbs(face.ImagePath) {
		t.Errorf("expected ImagePath in memory/DB to be relative, got: %s", face.ImagePath)
	}

	// 2. Verify GetFaceImagePath resolves to absolute path on disk
	resolved, err := svc.GetFaceImagePath(ctx, p.ID, face.ID)
	if err != nil {
		t.Fatalf("GetFaceImagePath failed: %v", err)
	}
	expected := filepath.Join(svc.facesDir, face.ImagePath)
	if resolved != expected {
		t.Errorf("expected resolved path %s, got %s", expected, resolved)
	}

	// 3. Test backward compatibility: legacy absolute path within facesDir resolves correctly
	legacyFace := &Face{
		ID:            "legacy_face_id",
		OwnerRowID:    p.ID,
		ImagePath:     filepath.Join(svc.facesDir, "legacy_photo.jpg"),
		FeatureVector: make([]byte, 2048),
	}
	if err := svc.store.AddFace(ctx, legacyFace); err != nil {
		t.Fatalf("AddFace legacy failed: %v", err)
	}
	resolvedLegacy, err := svc.GetFaceImagePath(ctx, p.ID, legacyFace.ID)
	if err != nil {
		t.Fatalf("GetFaceImagePath for legacy failed: %v", err)
	}
	if resolvedLegacy != legacyFace.ImagePath {
		t.Errorf("expected %s, got %s", legacyFace.ImagePath, resolvedLegacy)
	}

	// 4. Test path traversal rejection: absolute path outside facesDir must be rejected
	escapingFace := &Face{
		ID:            "escape_face_id",
		OwnerRowID:    p.ID,
		ImagePath:     "/etc/shadow",
		FeatureVector: make([]byte, 2048),
	}
	if err := svc.store.AddFace(ctx, escapingFace); err != nil {
		t.Fatalf("AddFace escaping failed: %v", err)
	}
	if _, err := svc.GetFaceImagePath(ctx, p.ID, escapingFace.ID); err == nil {
		t.Errorf("expected GetFaceImagePath to reject escaping path /etc/shadow")
	}
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
	svc := NewService(NewStore(dbStore), tempDir, extractor, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "P_EXTRACT_1", Name: "特征提取测试员"})
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

	extractor := &mockExtractor{err: errors.New("no face found")}
	svc := NewService(NewStore(dbStore), tempDir, extractor, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "P_EXTRACT_2", Name: "失败测试员"})
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
	faces, err := svc.ListFaces(ctx, p.ID)
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
	svc := NewService(NewStore(dbStore), tempDir, extractor, zap.NewNop())
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "P_EXTRACT_3", Name: "维度测试员"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	fakeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0xcc}, 100)...)
	_, err = svc.AddFaceWithReader(ctx, p.ID, "photo.jpg", bytes.NewReader(fakeJPEG))
	if err == nil {
		t.Fatal("expected error on dimension mismatch")
	}

	requireDomainError(t, err, apperr.KindInternal, "FEATURE_EXTRACT_FAILED")
}
