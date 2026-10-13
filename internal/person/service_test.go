package person

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
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
	svc := NewService(store, nil, zap.NewNop())
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

// recordingAuditor captures audit entries so tests can assert on the trail the service
// produces, including entries for refused operations.
type recordingAuditor struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (r *recordingAuditor) Record(entry audit.Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
}

func (r *recordingAuditor) snapshot() []audit.Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]audit.Entry(nil), r.entries...)
}

func newTestServiceWithAuditor(t *testing.T) (*Service, *recordingAuditor) {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditor := &recordingAuditor{}
	return NewService(NewStore(dbStore), auditor, zap.NewNop()), auditor
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	svc, _ := newTestServiceWithAuditor(t)
	return svc
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
			face, err := svc.AddFace(ctx, created.ID, AddFaceRequest{ImagePath: "/data/faces/enroll.jpg"})
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

// TestAddFace_RejectsUncleanImagePath covers the containment rule on the client-supplied
// enrollment-photo reference. The module has no upload endpoint, so the value is a path
// reference that a future consumer would open — a traversal segment must never be stored.
func TestAddFace_RejectsUncleanImagePath(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}

	rejected := []string{
		"../../etc/shadow",
		"/data/faces/../../etc/shadow",
		"faces/relative.jpg",
		"/data/faces/",
		"/data/faces//face.jpg",
		"/data/faces/./face.jpg",
		"/data/faces/\x00.jpg",
	}
	for _, path := range rejected {
		_, err := svc.AddFace(ctx, created.ID, AddFaceRequest{ImagePath: path})
		if err == nil {
			t.Errorf("imagePath %q must be rejected", path)
			continue
		}
		requireDomainError(t, err, apperr.KindInvalid, "INVALID_IMAGE_PATH")
	}

	faces, err := svc.ListFaces(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListFaces failed: %v", err)
	}
	if len(faces) != 0 {
		t.Errorf("rejected enrolments must not persist, got %d faces", len(faces))
	}

	if _, err := svc.AddFace(ctx, created.ID, AddFaceRequest{ImagePath: "/data/faces/face_01.jpg"}); err != nil {
		t.Fatalf("a clean absolute imagePath must be accepted, got %v", err)
	}
}

// TestListFaces_MissingPersonReturnsNotFound keeps GET .../faces consistent with the rest of
// the resource: an empty 200 would be indistinguishable from "no photos enrolled".
func TestListFaces_MissingPersonReturnsNotFound(t *testing.T) {
	svc := newTestService(t)

	_, err := svc.ListFaces(context.Background(), "ghost")
	requireDomainError(t, err, apperr.KindNotFound, "PERSON_NOT_FOUND")
}

// TestAudit_NamespacedActionsAndFailures pins both halves of the audit contract: actions
// carry the <domain>.<resource>.<verb> namespace used across the codebase, and refused
// operations are recorded as failures instead of being dropped.
func TestAudit_NamespacedActionsAndFailures(t *testing.T) {
	svc, auditor := newTestServiceWithAuditor(t)
	ctx := context.Background()

	for _, action := range []string{
		ActionPersonCreate, ActionPersonUpdate, ActionPersonDelete,
		ActionPersonFaceAdd, ActionPersonFaceDelete,
	} {
		if !strings.HasPrefix(action, "person.") {
			t.Errorf("audit action %q must be namespaced under person.", action)
		}
	}

	created, err := svc.CreatePerson(ctx, CreatePersonRequest{PersonID: "EMP_1", Name: "张三"})
	if err != nil {
		t.Fatalf("CreatePerson failed: %v", err)
	}
	entries := auditor.snapshot()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one audit entry per create, got %d", len(entries))
	}
	if entries[0].Action != ActionPersonCreate || entries[0].Status != audit.StatusSuccess {
		t.Errorf("expected %s/success, got %s/%s", ActionPersonCreate, entries[0].Action, entries[0].Status)
	}

	// A refused immutable-key reassignment must leave a failed entry behind.
	other := "EMP_OTHER"
	if _, err := svc.UpdatePerson(ctx, created.ID, UpdatePersonRequest{PersonID: &other}); err == nil {
		t.Fatal("expected the personId reassignment to be rejected")
	}
	entries = auditor.snapshot()
	if len(entries) != 2 {
		t.Fatalf("expected a failed entry for the refused update, got %d entries", len(entries))
	}
	refused := entries[1]
	if refused.Action != ActionPersonUpdate {
		t.Errorf("expected action %q, got %q", ActionPersonUpdate, refused.Action)
	}
	if refused.Status != audit.StatusFailed {
		t.Errorf("expected failed status, got %q", refused.Status)
	}
	if refused.ErrorMsg == "" {
		t.Error("a failed audit entry must carry the cause in ErrorMsg")
	}
}
