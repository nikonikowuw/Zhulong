package person

import (
	"bytes"
	"context"
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
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	// maxFaceImageBytes caps an uploaded enrollment photo.
	maxFaceImageBytes = 10 * 1024 * 1024 // 10MB
	// faceFeatureDim is the embedding width every face extractor must produce.
	// It is persisted as faceFeatureDim*4 Little-Endian bytes.
	faceFeatureDim = 512
	// faceNameMaxLen mirrors the max=64 binding on the create/update payloads.
	faceNameMaxLen = 64
)

// allowedFaceImageExts lists the accepted enrollment photo file extensions.
var allowedFaceImageExts = map[string]struct{}{
	".jpg":  {},
	".jpeg": {},
	".png":  {},
	".webp": {},
}

// ErrNoFaceDetected is the sentinel a FaceFeatureExtractor returns when the image
// decodes but contains no detectable face. Extractors must wrap it so the service
// can answer 422 instead of reporting an infrastructure failure as a bad photo.
var ErrNoFaceDetected = errors.New("no face detected in image")

// ErrUnsupportedImageFormat is the sentinel a FaceFeatureExtractor returns when the
// image bytes cannot be decoded at all (unsupported format or corrupted file). It is
// the caller's input that is at fault, so the service answers 400 rather than 500 —
// unlike a genuine extractor failure, which must never be blamed on the upload.
var ErrUnsupportedImageFormat = errors.New("unsupported image format")

// FaceFeatureExtractor abstracts single-shot facial feature embedding generation.
type FaceFeatureExtractor interface {
	ExtractFaceFeature(ctx context.Context, imageBytes []byte) ([]float32, error)
}

// GalleryNotifier notifies running algorithm runners of face gallery changes.
type GalleryNotifier interface {
	BroadcastUpsert(ctx context.Context, item GalleryItem) error
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

// NewService creates a Service instance. extractor and notifier are captured at
// construction time and never swapped afterwards.
func NewService(
	store *Store,
	facesDir string,
	extractor FaceFeatureExtractor,
	notifier GalleryNotifier,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:           store,
		extractor:       extractor,
		galleryNotifier: notifier,
		facesDir:        facesDir,
		logger:          logger.Named("person.service"),
	}
}

// normalizeName trims a display name and rejects the blank result, so a name made
// only of whitespace cannot be persisted.
func normalizeName(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", apperr.Invalid("NAME_REQUIRED", "Name is required", nil)
	}
	if len([]rune(trimmed)) > faceNameMaxLen {
		return "", apperr.Invalid("NAME_TOO_LONG", "Name is too long", nil)
	}
	return trimmed, nil
}

// CreatePerson creates a new person profile.
func (s *Service) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	name, err := normalizeName(req.Name)
	if err != nil {
		return nil, err
	}

	var personID *string
	if req.PersonID != nil {
		if trimmed := strings.TrimSpace(*req.PersonID); trimmed != "" {
			personID = &trimmed
		}
	}

	p := &Person{
		ID:       idutil.New(),
		PersonID: personID,
		Name:     name,
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

// ListPersons returns one page of person profiles plus the total row count.
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
		name, err := normalizeName(*req.Name)
		if err != nil {
			return nil, err
		}
		p.Name = name
	}

	if err := s.store.UpdatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		if errors.Is(err, ErrDuplicatePersonID) {
			return nil, apperr.Conflict("PERSON_ID_ALREADY_EXISTS", "Person with this personId already exists", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to update person", err)
	}

	// The gallery keys faces by personId and personName, so both must be re-published
	// whenever either changes.
	if req.Name != nil || req.PersonID != nil {
		s.publishPersonFaces(ctx, p)
	}

	return p, nil
}

// publishPersonFaces re-broadcasts every face of a person to the algorithm runner
// so its gallery picks up a renamed or re-keyed person.
func (s *Service) publishPersonFaces(ctx context.Context, p *Person) {
	if s.galleryNotifier == nil {
		return
	}
	faces, err := s.store.ListFacesByPerson(ctx, p.ID)
	if err != nil {
		s.logger.Warn("failed to list faces for gallery re-publish", zap.String("person_id", p.ID), zap.Error(err))
		return
	}
	personID := ""
	if p.PersonID != nil {
		personID = *p.PersonID
	}
	for _, f := range faces {
		s.broadcastUpsert(ctx, newGalleryItem(f, personID, p.Name))
	}
}

// SetPrimaryFace updates a person's primary face pointer. The person and the face
// are resolved separately so a missing person, a missing face and a face owned by
// someone else are reported as the distinct conditions they are.
func (s *Service) SetPrimaryFace(ctx context.Context, id, faceID string) (*Person, error) {
	faceID = strings.TrimSpace(faceID)
	if faceID == "" {
		return nil, apperr.Invalid("FACE_ID_REQUIRED", "Face ID is required", nil)
	}

	if _, err := s.store.GetPersonMeta(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err)
	}

	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to load face", err)
	}
	if face.PersonRowID != id {
		return nil, apperr.Invalid("INVALID_FACE_OWNER", "Face does not belong to this person", nil)
	}

	if err := s.store.SetPrimaryFace(ctx, id, faceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to set primary face", err)
	}

	// Reload so the caller sees the pointer as persisted, not the pre-update copy.
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to reload person", err)
	}
	return p, nil
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

	// GetPerson preloads the face rows, so the enrolled photos are known without a
	// second query and the broadcast below can name each face individually.
	faces := p.Faces

	if err := s.store.DeletePerson(ctx, id); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete person", err)
	}

	for _, f := range faces {
		s.removeFaceFile(f.ImagePath)
		s.broadcastDelete(ctx, f.ID, "")
	}
	if p.PersonID != nil && *p.PersonID != "" {
		s.broadcastDelete(ctx, "", *p.PersonID)
	}

	return nil
}

// removeFaceFile deletes an enrolled photo from disk. The database row is already
// gone by the time this runs, so a failure leaves an orphaned file rather than a
// dangling record: it is logged, not returned.
func (s *Service) removeFaceFile(imagePath string) {
	if imagePath == "" {
		return
	}
	safePath, err := s.resolveFaceFilePath(imagePath)
	if err != nil {
		s.logger.Warn("skipped deletion of face file outside faces directory", zap.String("path", imagePath), zap.Error(err))
		return
	}
	if err := os.Remove(safePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("failed to delete face file", zap.String("path", safePath), zap.Error(err))
	}
}

// broadcastUpsert publishes one gallery change to the algorithm runner.
//
// A failed broadcast is logged and swallowed deliberately: the enrollment is
// already committed locally, so failing the request would report an error for
// work that did happen. Runners rebuild their gallery from the full snapshot on
// reconnect, so a dropped incremental update self-heals.
func (s *Service) broadcastUpsert(ctx context.Context, item GalleryItem) {
	if s.galleryNotifier == nil {
		return
	}
	if err := s.galleryNotifier.BroadcastUpsert(ctx, item); err != nil {
		s.logger.Warn("failed to broadcast gallery upsert",
			zap.String("face_id", item.FaceID),
			zap.String("person_id", item.PersonID),
			zap.Error(err))
	}
}

// broadcastDelete publishes a gallery removal. An empty faceID removes every face
// of personID; an empty personID removes the single face named by faceID.
func (s *Service) broadcastDelete(ctx context.Context, faceID, personID string) {
	if s.galleryNotifier == nil {
		return
	}
	if err := s.galleryNotifier.BroadcastDelete(ctx, faceID, personID); err != nil {
		s.logger.Warn("failed to broadcast gallery delete",
			zap.String("face_id", faceID),
			zap.String("person_id", personID),
			zap.Error(err))
	}
}

// newGalleryItem builds the IPC gallery entry for an enrolled face. It is the one
// place that assembles the payload, so the cold-start snapshot and the
// incremental upsert can never drift apart.
func newGalleryItem(f Face, personID, personName string) GalleryItem {
	var features []float32
	if len(f.FeatureVector) > 0 {
		features = bytesToFloat32(f.FeatureVector)
	}
	return GalleryItem{
		FaceID:     f.ID,
		PersonID:   personID,
		PersonName: personName,
		ImagePath:  f.ImagePath,
		Features:   features,
	}
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
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", apperr.Invalid("INVALID_IMAGE_PATH", "image path escapes allowed storage directory", nil)
	}
	return target, nil
}

// invalidFileTypeError reports a rejected enrollment photo. The accepted set is
// stated once so the extension check and the content sniff cannot disagree.
func invalidFileTypeError() error {
	return apperr.Invalid("INVALID_FILE_TYPE", "only jpg, jpeg, png and webp images are allowed", nil)
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
	if file.Size > maxFaceImageBytes {
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
	if _, ok := allowedFaceImageExts[ext]; !ok {
		return nil, invalidFileTypeError()
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
		return nil, invalidFileTypeError()
	}

	combinedReader := io.MultiReader(bytes.NewReader(headerBuf[:nHeader]), r)
	lr := io.LimitReader(combinedReader, maxFaceImageBytes+1)
	fullBytes, err := io.ReadAll(lr)
	if err != nil {
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to read file content", err)
	}
	if len(fullBytes) > maxFaceImageBytes {
		return nil, apperr.Invalid("FILE_TOO_LARGE", "file size exceeds 10MB limit", nil)
	}

	// A face template is only ever enrolled with a real embedding. There is no
	// placeholder path: a zeroed vector is indistinguishable from a genuine
	// enrollment in storage and would silently never match.
	if s.extractor == nil {
		return nil, apperr.New(apperr.KindInternal, "FEATURE_EXTRACTOR_UNAVAILABLE", "Face feature extraction is not configured", nil)
	}
	features, err := s.extractor.ExtractFaceFeature(ctx, fullBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return nil, apperr.Invalid("NO_FACE_DETECTED", "No detectable face found in the uploaded photo", err)
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return nil, apperr.Invalid("UNSUPPORTED_IMAGE_FORMAT", "The uploaded image could not be decoded", err)
		}
		return nil, apperr.New(apperr.KindInternal, "FEATURE_EXTRACT_FAILED", "Failed to extract facial features", err)
	}
	if len(features) != faceFeatureDim {
		return nil, apperr.New(apperr.KindInternal, "FEATURE_DIM_MISMATCH", "Unexpected feature dimension", nil)
	}
	featBytes := float32ToBytes(features)

	if err := os.MkdirAll(s.facesDir, 0755); err != nil {
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to create faces directory", err)
	}

	faceID := idutil.New()
	fileName := fmt.Sprintf("%s_%s%s", personID, faceID, ext)
	savePath := filepath.Join(s.facesDir, fileName)
	if err := os.WriteFile(savePath, fullBytes, 0644); err != nil {
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to save face image", err)
	}

	face := &Face{
		ID:            faceID,
		PersonRowID:   personID,
		ImagePath:     fileName,
		FeatureVector: featBytes,
	}

	if err := s.store.AddFace(ctx, face); err != nil {
		if err := os.Remove(savePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("failed to clean up face image after enrollment failure",
				zap.String("path", savePath), zap.Error(err))
		}
		return nil, apperr.New(apperr.KindInternal, "FACE_ADD_FAILED", "Failed to add face template", err)
	}

	personIDKey := ""
	if personMeta.PersonID != nil {
		personIDKey = *personMeta.PersonID
	}
	s.broadcastUpsert(ctx, newGalleryItem(*face, personIDKey, personMeta.Name))

	return face, nil
}

// GetFaceImagePath returns the disk path of the requested face template.
func (s *Service) GetFaceImagePath(ctx context.Context, personID, faceID string) (string, error) {
	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return "", apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to load face", err)
	}
	if face.PersonRowID != personID {
		return "", apperr.NotFound("FACE_NOT_FOUND", "Face not found", nil)
	}
	return s.resolveFaceFilePath(face.ImagePath)
}

// DeleteFace deletes a specific face template belonging to the given person.
func (s *Service) DeleteFace(ctx context.Context, personID, faceID string) error {
	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to load face", err)
	}
	// Scoping to the owning person keeps the nested resource path meaningful: a
	// face is not addressable through another person's path segment.
	if face.PersonRowID != personID {
		return apperr.NotFound("FACE_NOT_FOUND", "Face not found", nil)
	}

	person, err := s.store.GetPersonMeta(ctx, personID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to load person", err)
	}

	if err := s.store.DeleteFace(ctx, personID, faceID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to delete face", err)
	}

	s.removeFaceFile(face.ImagePath)

	personIDKey := ""
	if person.PersonID != nil {
		personIDKey = *person.PersonID
	}
	s.broadcastDelete(ctx, faceID, personIDKey)

	return nil
}

// ListFaces returns one page of face templates registered for a person plus the
// total count.
//
// A missing person is reported as 404 rather than as an empty list: returning 200 with no
// items would make "this person has no enrolled photos" indistinguishable from "this
// person does not exist", and would disagree with GET /persons/{id} and POST .../faces.
func (s *Service) ListFaces(ctx context.Context, personID string, limit, offset int) ([]Face, int64, error) {
	if _, err := s.store.GetPersonMeta(ctx, personID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, 0, apperr.NotFound("PERSON_NOT_FOUND", "Person not found", err)
		}
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to verify person", err)
	}

	faces, total, err := s.store.ListFacesByPersonPaged(ctx, personID, limit, offset)
	if err != nil {
		return nil, 0, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list faces", err)
	}
	return faces, total, nil
}

// GetGallerySyncData prepares all enrolled face templates for IPC downlinking to algorithm runners.
func (s *Service) GetGallerySyncData(ctx context.Context) ([]GalleryItem, error) {
	persons, err := s.store.ListAllPersons(ctx)
	if err != nil {
		return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list gallery persons", err)
	}

	var syncItems []GalleryItem
	for _, p := range persons {
		personID := ""
		if p.PersonID != nil {
			personID = *p.PersonID
		}
		for _, f := range p.Faces {
			syncItems = append(syncItems, newGalleryItem(f, personID, p.Name))
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
