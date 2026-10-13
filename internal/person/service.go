package person

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	"go.uber.org/zap"
)

// Auditor records user operations.
type Auditor interface {
	Record(entry audit.Entry)
}

// Service provides high-level business logic for personnel and face gallery.
type Service struct {
	store   *Store
	auditor Auditor
	logger  *zap.Logger
}

// NewService creates a Service instance.
func NewService(store *Store, auditor Auditor, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:   store,
		auditor: auditor,
		logger:  logger.Named("person_service"),
	}
}

// maxImagePathLength caps the stored enrollment-photo reference length.
const maxImagePathLength = 512

// newID returns a random identifier for a person or face row.
//
// Row ids used to be derived from time.Now().UnixNano(), which is not collision-free:
// concurrent callers, or two cores reading the same clock tick, can obtain an identical
// value and produce a duplicate primary key, surfacing as a 500 on the second request.
// rand.Text() draws from the OS entropy source instead and never fails.
func newID() string {
	return rand.Text()
}

// validateImagePath enforces that a client-supplied enrollment-photo reference is a clean
// absolute path.
//
// This module has no upload endpoint yet, so the value is a reference to a file managed
// elsewhere. What we can and must guarantee is that it cannot smuggle a traversal sequence
// ("../../etc/shadow") or a relative path into storage, where any future consumer that
// opens or serves it would inherit the escape. Requiring filepath.Clean to be a no-op also
// rejects "./", "//" and trailing separators.
func validateImagePath(p string) error {
	if p == "" || len(p) > maxImagePathLength {
		return apperr.Invalid("INVALID_IMAGE_PATH", "imagePath must be a non-empty path of at most 512 bytes", nil)
	}
	if strings.ContainsRune(p, '\x00') {
		return apperr.Invalid("INVALID_IMAGE_PATH", "imagePath must not contain NUL bytes", nil)
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return apperr.Invalid("INVALID_IMAGE_PATH", "imagePath must be a clean absolute path", nil)
	}
	return nil
}

// recordAudit writes one audit entry for a person-domain operation. A nil opErr records a
// success; otherwise the entry is recorded as failed with the cause attached, so the audit
// trail captures refused attempts (a rejected person_id reassignment, a missing person)
// and not only the operations that went through.
func (s *Service) recordAudit(action, target, detail string, opErr error) {
	if s.auditor == nil {
		return
	}
	entry := audit.Entry{
		Action: action,
		Target: target,
		Detail: detail,
		Status: audit.StatusSuccess,
	}
	if opErr != nil {
		entry.Status = audit.StatusFailed
		entry.ErrorMsg = opErr.Error()
	}
	s.auditor.Record(entry)
}

// failAudit records a failed operation and returns opErr unchanged, keeping error paths a
// single expression: `return nil, s.failAudit(action, target, detail, err)`.
func (s *Service) failAudit(action, target, detail string, opErr error) error {
	s.recordAudit(action, target, detail, opErr)
	return opErr
}

// CreatePerson creates a new person profile.
func (s *Service) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	if req.Name == "" {
		return nil, s.failAudit(ActionPersonCreate, "person:", "Create person",
			apperr.Invalid("NAME_REQUIRED", "Name is required", nil))
	}

	id := newID()
	personID := strings.TrimSpace(req.PersonID)
	if personID == "" {
		personID = id
	}

	p := &Person{
		ID:       id,
		PersonID: personID,
		Name:     req.Name,
	}
	target := "person:" + p.ID
	detail := "Create person " + p.Name

	if err := s.store.CreatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrDuplicateID) {
			return nil, s.failAudit(ActionPersonCreate, target, detail,
				apperr.Conflict("PERSON_ALREADY_EXISTS", "Person already exists", err))
		}
		if errors.Is(err, ErrDuplicatePersonID) {
			return nil, s.failAudit(ActionPersonCreate, target, detail,
				apperr.Conflict("PERSON_ID_ALREADY_EXISTS", "Person with this personId already exists", err))
		}
		return nil, s.failAudit(ActionPersonCreate, target, detail,
			apperr.New(apperr.KindInternal, "PERSON_CREATE_FAILED", "Failed to create person", err))
	}

	s.recordAudit(ActionPersonCreate, target, detail, nil)
	return p, nil
}

// GetPerson returns a person by ID.
func (s *Service) GetPerson(ctx context.Context, id string) (*Person, error) {
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get person", err)
	}
	return p, nil
}

func (s *Service) ListPersons(ctx context.Context, limit, offset int) ([]Person, int64, error) {
	list, total, err := s.store.ListPersons(ctx, limit, offset)
	if err != nil {
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list persons", err)
	}
	return list, total, nil
}

// UpdatePerson updates an existing person's metadata.
func (s *Service) UpdatePerson(ctx context.Context, id string, req UpdatePersonRequest) (*Person, error) {
	target := "person:" + id
	detail := "Update person " + id

	p, err := s.store.GetPersonMeta(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, s.failAudit(ActionPersonUpdate, target, detail,
				apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err))
		}
		return nil, s.failAudit(ActionPersonUpdate, target, detail,
			apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get person", err))
	}
	detail = "Update person " + p.Name

	// person_id is the business key external systems correlate on, so it is immutable:
	// an attempt to change it fails loudly rather than being silently dropped. The field
	// is kept in the DTO precisely so this rejection is reachable.
	if req.PersonID != nil && *req.PersonID != p.PersonID {
		return nil, s.failAudit(ActionPersonUpdate, target, detail,
			apperr.Conflict("PERSON_ID_IMMUTABLE", "personId cannot be changed", nil))
	}
	if req.Name != nil {
		if *req.Name == "" {
			return nil, s.failAudit(ActionPersonUpdate, target, detail,
				apperr.Invalid("NAME_REQUIRED", "Name is required", nil))
		}
		p.Name = *req.Name
		detail = "Update person " + p.Name
	}

	if err := s.store.UpdatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, s.failAudit(ActionPersonUpdate, target, detail,
				apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err))
		}
		return nil, s.failAudit(ActionPersonUpdate, target, detail,
			apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to update person", err))
	}

	s.recordAudit(ActionPersonUpdate, target, detail, nil)
	return p, nil
}

// DeletePerson deletes a person and their faces.
func (s *Service) DeletePerson(ctx context.Context, id string) error {
	target := "person:" + id
	detail := "Delete person " + id

	if err := s.store.DeletePerson(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.failAudit(ActionPersonDelete, target, detail,
				apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err))
		}
		return s.failAudit(ActionPersonDelete, target, detail,
			apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete person", err))
	}

	// TODO: 同步删除Engine中的人脸特征向量

	s.recordAudit(ActionPersonDelete, target, detail, nil)
	return nil
}

// AddFace adds a face template to a person profile.
//
// TODO(api/storage): 待改造成文件流接收 (multipart/form-data) 并落盘，当前接收 ImagePath 为临时占位。
func (s *Service) AddFace(ctx context.Context, personID string, req AddFaceRequest) (*Face, error) {
	// Until the face row exists the only resource we can name is the owning person.
	target := "person:" + personID
	detail := "Add face for person " + personID

	if err := validateImagePath(req.ImagePath); err != nil {
		return nil, s.failAudit(ActionPersonFaceAdd, target, detail, err)
	}

	// Verify the person exists without preloading their faces: this path needs the row only.
	if _, err := s.store.GetPersonMeta(ctx, personID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, s.failAudit(ActionPersonFaceAdd, target, detail,
				apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err))
		}
		return nil, s.failAudit(ActionPersonFaceAdd, target, detail,
			apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err))
	}

	// TODO(algo): 接入算法包/NPU 视觉推理流水线提取特征向量 (Face Detection -> Alignment -> Feature Extraction).
	// 当前算法插件底库特征提取通道尚未就绪，暂使用 2048 字节占位向量（以常见 512 维 float32 为例），待算法包生命周期完善后替换为真实推理结果。
	feat := make([]byte, 2048)

	face := &Face{
		ID:            newID(),
		OwnerRowID:    personID,
		ImagePath:     req.ImagePath,
		FeatureVector: feat,
	}
	target = "face:" + face.ID

	if err := s.store.AddFace(ctx, face); err != nil {
		return nil, s.failAudit(ActionPersonFaceAdd, target, detail,
			apperr.New(apperr.KindInternal, "FACE_ADD_FAILED", "Failed to add face template", err))
	}

	// TODO: 同步添加的人脸到Engine中

	s.recordAudit(ActionPersonFaceAdd, target, detail, nil)
	return face, nil
}

// DeleteFace deletes a specific face template belonging to the given person.
func (s *Service) DeleteFace(ctx context.Context, personID, faceID string) error {
	target := "face:" + faceID
	detail := "Delete face " + faceID

	if err := s.store.DeleteFace(ctx, personID, faceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.failAudit(ActionPersonFaceDelete, target, detail,
				apperr.NotFound("FACE_NOT_FOUND", "Face not found", err))
		}
		return s.failAudit(ActionPersonFaceDelete, target, detail,
			apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete face", err))
	}

	// TODO: 同步删除Engine中的人脸特征

	s.recordAudit(ActionPersonFaceDelete, target, detail, nil)
	return nil
}

// ListFaces returns all face templates registered for a person.
//
// A missing person is reported as 404 rather than as an empty list: returning 200 with no
// items would make "this person has no enrolled photos" indistinguishable from "this
// person does not exist", and would disagree with GET /persons/{id} and POST .../faces.
func (s *Service) ListFaces(ctx context.Context, personID string) ([]Face, error) {
	if _, err := s.store.GetPersonMeta(ctx, personID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err)
	}

	faces, err := s.store.ListFacesByPerson(ctx, personID)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list faces", err)
	}
	return faces, nil
}

// GetGallerySyncData prepares all enrolled face templates for IPC downlinking to algorithm runners.
//
// ⚠️ NOT WIRED: no caller exists. The gallery hot-update channel (transport,
// ACK semantics, and sharding) is not implemented — see
// .trellis/tasks/10-12-algo-lifecycle-remediation/prd.md R6. The person-domain
// field Features (JSON features) matches the IPC payload field
// "features" used by algorithm/process.go.

// TODO: 死代码， 预留实现，占位用的
func (s *Service) GetGallerySyncData(ctx context.Context) ([]GallerySyncItem, error) {
	persons, err := s.store.ListAllPersons(ctx)
	if err != nil {
		return nil, err
	}

	var syncItems []GallerySyncItem
	for _, p := range persons {
		// PersonID is the gallery identity verbatim: it is mandatory and unique, so
		// there is no fallback to the internal row id — mixing the two identifier
		// spaces here would hand algorithm runners identities the external systems
		// cannot resolve.
		for _, f := range p.Faces {
			floats := bytesToFloat32(f.FeatureVector)
			syncItems = append(syncItems, GallerySyncItem{
				FaceID:     f.ID,
				PersonID:   p.PersonID,
				PersonName: p.Name,
				Features:   floats,
			})
		}
	}
	return syncItems, nil
}

var isLittleEndian = func() bool {
	var v uint16 = 1
	return *(*byte)(unsafe.Pointer(&v)) == 1
}()

// bytesToFloat32 converts a Little-Endian byte buffer into a float32 slice.
// If the buffer satisfies 4-byte memory alignment and the host system is little-endian,
// it uses unsafe.Slice for zero-copy slicing, eliminating heap allocations.
// Otherwise, it safely falls back to standard per-element unmarshaling.
//
// ⚠️ The fast path returns a view that SHARES the input buffer's backing array.
// Callers must treat the result as read-only: writing through it mutates the
// source bytes (e.g. a Face.FeatureVector loaded from the database). Copy the
// result first when it needs to be mutated or retained past the input's lifetime.
func bytesToFloat32(b []byte) []float32 {
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}

	ptr := unsafe.Pointer(&b[0])

	// Fast-path: zero-copy view if 4-byte aligned and little-endian host
	if isLittleEndian && uintptr(ptr)%unsafe.Alignof(float32(0)) == 0 {
		return unsafe.Slice((*float32)(ptr), len(b)/4)
	}

	// Safe-fallback: unaligned or big-endian architecture
	res := make([]float32, len(b)/4)
	for i := range res {
		bits := binary.LittleEndian.Uint32(b[i*4 : (i+1)*4])
		res[i] = math.Float32frombits(bits)
	}
	return res
}
