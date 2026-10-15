package person

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"go.uber.org/zap"
)

// FaceFeatureExtractor abstracts single-shot facial feature embedding generation.
type FaceFeatureExtractor interface {
	ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)
}

// GalleryNotifier notifies running algorithm runners of face gallery changes.
type GalleryNotifier interface {
	BroadcastUpsert(ctx context.Context, item GalleryUpsertPayload) error
	BroadcastDelete(ctx context.Context, faceID, personID string) error
}

// Service provides high-level business logic for personnel and face gallery.
type Service struct {
	store           *Store
	extractor       FaceFeatureExtractor
	galleryNotifier GalleryNotifier
	facesDir        string
	logger          *zap.Logger
}

// NewService creates a Service instance.
func NewService(store *Store, facesDir string, extractor FaceFeatureExtractor, logger *zap.Logger) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:     store,
		extractor: extractor,
		facesDir:  facesDir,
		logger:    logger.Named("person_service"),
	}
}

// SetGalleryNotifier binds a gallery hot-update broadcaster to this service.
func (s *Service) SetGalleryNotifier(notifier GalleryNotifier) {
	s.galleryNotifier = notifier
}

// newID returns a random identifier for a person or face row.
//
// Row ids used to be derived from time.Now().UnixNano(), which is not collision-free:
// concurrent callers, or two cores reading the same clock tick, can obtain an identical
// value and produce a duplicate primary key, surfacing as a 500 on the second request.
// rand.Text() draws from the OS entropy source instead and never fails.
func newID() string {
	return rand.Text()
}

// CreatePerson creates a new person profile.
func (s *Service) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	if req.Name == "" {
		return nil, apperr.Invalid("NAME_REQUIRED", "Name is required", nil)
	}

	id := newID()
	var personID *string
	trimmed := strings.TrimSpace(req.PersonID)
	if trimmed != "" {
		personID = &trimmed
	}

	p := &Person{
		ID:       id,
		PersonID: personID,
		Name:     req.Name,
	}

	if err := s.store.CreatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrDuplicateID) {
			return nil, apperr.Conflict("PERSON_ALREADY_EXISTS", "Person already exists", err)
		}
		if errors.Is(err, ErrDuplicatePersonID) {
			return nil, apperr.Conflict("PERSON_ID_ALREADY_EXISTS", "Person with this personId already exists", err)
		}
		return nil, apperr.New(apperr.KindInternal, "PERSON_CREATE_FAILED", "Failed to create person", err)
	}

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

func (s *Service) ListPersons(ctx context.Context, limit, offset int, search string) ([]Person, int64, error) {
	list, total, err := s.store.ListPersons(ctx, limit, offset, search)
	if err != nil {
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list persons", err)
	}
	return list, total, nil
}

// UpdatePerson updates an existing person's metadata.
func (s *Service) UpdatePerson(ctx context.Context, id string, req UpdatePersonRequest) (*Person, error) {

	p, err := s.store.GetPersonMeta(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to get person", err)
	}

	// person_id is the business key external systems correlate on. If not yet assigned (nil or empty),
	// it can be bound. Once assigned, attempts to change it are rejected with PERSON_ID_IMMUTABLE.
	if req.PersonID != nil {
		trimmed := strings.TrimSpace(*req.PersonID)
		if p.PersonID == nil || *p.PersonID == "" {
			if trimmed != "" {
				p.PersonID = &trimmed
			}
		} else if trimmed != *p.PersonID {
			return nil, apperr.Conflict("PERSON_ID_IMMUTABLE", "personId cannot be changed", nil)
		}
	}
	if req.Name != nil {
		if *req.Name == "" {
			return nil, apperr.Invalid("NAME_REQUIRED", "Name is required", nil)
		}
		p.Name = *req.Name
	}

	if err := s.store.UpdatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to update person", err)
	}

	if (req.Name != nil || req.PersonID != nil) && s.galleryNotifier != nil {
		faces, err := s.store.ListFacesByPerson(ctx, id)
		if err == nil {
			var pid string
			if p.PersonID != nil {
				pid = *p.PersonID
			}
			for _, f := range faces {
				_ = s.galleryNotifier.BroadcastUpsert(ctx, GalleryUpsertPayload{
					FaceID:     f.ID,
					PersonID:   pid,
					PersonName: p.Name,
					ImagePath:  f.ImagePath,
					Features:   bytesToFloat32(f.FeatureVector),
				})
			}
		}
	}

	return p, nil
}

// SetPrimaryFace updates a person's primary face pointer.
func (s *Service) SetPrimaryFace(ctx context.Context, id, faceID string) (*Person, error) {
	faceID = strings.TrimSpace(faceID)
	if faceID == "" {
		return nil, apperr.Invalid("FACE_ID_REQUIRED", "Face ID is required", nil)
	}

	if err := s.store.SetPrimaryFace(ctx, id, &faceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_OR_FACE_NOT_FOUND", "Person or face not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to set primary face", err)
	}

	return s.store.GetPerson(ctx, id)
}

// DeletePerson deletes a person and their faces.
func (s *Service) DeletePerson(ctx context.Context, id string) error {
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err)
	}

	faces, err := s.store.ListFacesByPerson(ctx, id)
	if err != nil {
		s.logger.Warn("failed to list faces before person delete", zap.String("personId", id), zap.Error(err))
	}

	if err := s.store.DeletePerson(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete person", err)
	}

	for _, f := range faces {
		if f.ImagePath != "" {
			if safePath, err := s.resolveFaceFilePath(f.ImagePath); err == nil {
				_ = os.Remove(safePath)
			} else {
				s.logger.Warn("skipped deletion of face file outside faces directory", zap.String("path", f.ImagePath), zap.Error(err))
			}
		}
	}

	if s.galleryNotifier != nil && p != nil {
		for _, f := range faces {
			_ = s.galleryNotifier.BroadcastDelete(ctx, f.ID, "")
		}
		if p.PersonID != nil && *p.PersonID != "" {
			_ = s.galleryNotifier.BroadcastDelete(ctx, "", *p.PersonID)
		}
	}

	return nil
}

// resolveFaceFilePath safely verifies that imagePath resolves to a file within s.facesDir.
func (s *Service) resolveFaceFilePath(imagePath string) (string, error) {
	if imagePath == "" {
		return "", apperr.NotFound("IMAGE_NOT_FOUND", "image path is empty", nil)
	}
	cleanDir := filepath.Clean(s.facesDir)
	var target string
	if filepath.IsAbs(imagePath) {
		target = filepath.Clean(imagePath)
	} else {
		target = filepath.Clean(filepath.Join(cleanDir, imagePath))
	}
	rel, err := filepath.Rel(cleanDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", apperr.Invalid("INVALID_IMAGE_PATH", "image path escapes allowed storage directory", nil)
	}
	return target, nil
}

// isSupportedImageContent checks whether data starts with known JPEG, PNG, or WebP magic bytes,
// or is recognized as such by http.DetectContentType.
func isSupportedImageContent(data []byte) bool {
	if len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff {
		return true // JPEG
	}
	if len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")) {
		return true // PNG
	}
	if len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP" {
		return true // WebP
	}
	sniffed := http.DetectContentType(data)
	return sniffed == "image/jpeg" || sniffed == "image/png" || sniffed == "image/webp"
}

// AddFace validates and enrolls a face template from an uploaded multipart file header.
func (s *Service) AddFace(ctx context.Context, personID string, file *multipart.FileHeader) (*Face, error) {
	if file == nil {
		return nil, apperr.Invalid("FILE_REQUIRED", "file is required", nil)
	}
	const maxFileSize = 10 * 1024 * 1024 // 10MB
	if file.Size > maxFileSize {
		return nil, apperr.Invalid("FILE_TOO_LARGE", "file size exceeds 10MB limit", nil)
	}
	src, err := file.Open()
	if err != nil {
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to open uploaded file", err)
	}
	defer src.Close()

	return s.AddFaceWithReader(ctx, personID, file.Filename, src)
}

// AddFaceWithReader saves uploaded image file into data directory and enrolls face template.
func (s *Service) AddFaceWithReader(ctx context.Context, personID string, filename string, r io.Reader) (*Face, error) {
	personMeta, err := s.store.GetPersonMeta(ctx, personID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp":
	default:
		return nil, apperr.Invalid("INVALID_FILE_TYPE", "only jpg, jpeg, png and webp images are allowed", nil)
	}

	headerBuf := make([]byte, 512)
	nHeader, err := io.ReadFull(r, headerBuf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to read file content", err)
	}
	if nHeader == 0 {
		return nil, apperr.Invalid("FILE_REQUIRED", "file is required", nil)
	}
	if !isSupportedImageContent(headerBuf[:nHeader]) {
		return nil, apperr.Invalid("INVALID_FILE_TYPE", "only jpg, jpeg, png and webp images are allowed", nil)
	}

	faceID := newID()
	combinedReader := io.MultiReader(bytes.NewReader(headerBuf[:nHeader]), r)
	const maxFileSize = 10 * 1024 * 1024 // 10MB
	lr := io.LimitReader(combinedReader, maxFileSize+1)
	fullBytes, err := io.ReadAll(lr)
	if err != nil {
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to read file content", err)
	}
	if len(fullBytes) > maxFileSize {
		return nil, apperr.Invalid("FILE_TOO_LARGE", "file size exceeds 10MB limit", nil)
	}

	var featBytes []byte
	if s.extractor != nil {
		features, err := s.extractor.ExtractFaceFeature(ctx, fullBytes)
		if err != nil {
			return nil, apperr.Invalid("NO_FACE_DETECTED", "No face detected in uploaded image", err)
		}
		if len(features) != 512 {
			return nil, apperr.New(apperr.KindInternal, "FEATURE_EXTRACT_FAILED", "Unexpected feature vector dimension", nil)
		}
		featBytes = float32ToBytes(features)
	} else {
		// Mock fallback if extractor is not configured
		featBytes = make([]byte, 2048)
	}

	if err := os.MkdirAll(s.facesDir, 0755); err != nil {
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to create faces directory", err)
	}

	fileName := fmt.Sprintf("%s_%s%s", personID, faceID, ext)
	savePath := filepath.Join(s.facesDir, fileName)
	if err := os.WriteFile(savePath, fullBytes, 0644); err != nil {
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to save face image", err)
	}

	face := &Face{
		ID:            faceID,
		OwnerRowID:    personID,
		ImagePath:     fileName,
		FeatureVector: featBytes,
	}

	if err := s.store.AddFace(ctx, face); err != nil {
		_ = os.Remove(savePath)
		return nil, apperr.New(apperr.KindInternal, "FACE_ADD_FAILED", "Failed to add face template", err)
	}

	if s.galleryNotifier != nil {
		var floats []float32
		if len(featBytes) > 0 {
			floats = bytesToFloat32(featBytes)
		}
		var pid string
		if personMeta.PersonID != nil {
			pid = *personMeta.PersonID
		}
		if err := s.galleryNotifier.BroadcastUpsert(ctx, GalleryUpsertPayload{
			FaceID:     faceID,
			PersonID:   pid,
			PersonName: personMeta.Name,
			ImagePath:  fileName,
			Features:   floats,
		}); err != nil {
			s.logger.Warn("failed to broadcast face upsert", zap.String("face_id", faceID), zap.Error(err))
		}
	}

	return face, nil
}

// GetFaceImagePath returns the disk path of the requested face template.
func (s *Service) GetFaceImagePath(ctx context.Context, personID, faceID string) (string, error) {
	faces, err := s.store.ListFacesByPerson(ctx, personID)
	if err != nil {
		return "", apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list faces", err)
	}
	for _, f := range faces {
		if f.ID == faceID {
			return s.resolveFaceFilePath(f.ImagePath)
		}
	}
	return "", apperr.NotFound("FACE_NOT_FOUND", "Face not found", nil)
}

// DeleteFace deletes a specific face template belonging to the given person.
func (s *Service) DeleteFace(ctx context.Context, personID, faceID string) error {
	faces, err := s.store.ListFacesByPerson(ctx, personID)
	if err != nil {
		s.logger.Warn("failed to list faces before face delete", zap.String("personId", personID), zap.Error(err))
	}
	var targetPath string
	for _, f := range faces {
		if f.ID == faceID {
			targetPath = f.ImagePath
			break
		}
	}

	targetPerson, _ := s.store.GetPerson(ctx, personID)

	if err := s.store.DeleteFace(ctx, personID, faceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete face", err)
	}

	if targetPath != "" {
		if safePath, err := s.resolveFaceFilePath(targetPath); err == nil {
			_ = os.Remove(safePath)
		} else {
			s.logger.Warn("skipped deletion of face file outside faces directory", zap.String("path", targetPath), zap.Error(err))
		}
	}

	if s.galleryNotifier != nil && targetPerson != nil {
		var pid string
		if targetPerson.PersonID != nil {
			pid = *targetPerson.PersonID
		}
		_ = s.galleryNotifier.BroadcastDelete(ctx, faceID, pid)
	}

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
func (s *Service) GetGallerySyncData(ctx context.Context) ([]GallerySyncItem, error) {
	persons, err := s.store.ListAllPersons(ctx)
	if err != nil {
		return nil, err
	}

	var syncItems []GallerySyncItem
	for _, p := range persons {
		var pid string
		if p.PersonID != nil {
			pid = *p.PersonID
		}
		for _, f := range p.Faces {
			floats := bytesToFloat32(f.FeatureVector)
			syncItems = append(syncItems, GallerySyncItem{
				FaceID:     f.ID,
				PersonID:   pid,
				PersonName: p.Name,
				ImagePath:  f.ImagePath,
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

// float32ToBytes converts a slice of float32 into Little-Endian bytes.
func float32ToBytes(floats []float32) []byte {
	if len(floats) == 0 {
		return nil
	}
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		binary.LittleEndian.PutUint32(buf[i*4:(i+1)*4], math.Float32bits(f))
	}
	return buf
}
