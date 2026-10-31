package person

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
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

// RecognitionKeyBackfiller fills in the business-key snapshot of recognitions that
// were captured before their person had an external key.
//
// It exists because the person domain owns the binding while the record domain owns
// the rows: binding a key retroactively is a person decision, but only the record
// store may touch recognitions. Implemented by the record domain; nil disables it.
type RecognitionKeyBackfiller interface {
	BackfillPersonExternalID(ctx context.Context, personRowID, externalID string) (int64, error)
}

// Options carries deployment policy and the cross-domain collaborator that the person
// domain cannot construct itself. Both are captured at construction time.
type Options struct {
	// RequirePersonIDForEnrollment rejects face enrollment for a person that has no
	// external business key yet.
	//
	// Enable it once recognitions are reported to an external system: a face whose
	// owner has no business key can only ever produce an unreportable record, so the
	// refusal belongs at enrollment instead of surfacing later as a recognition nobody
	// can hand to the business system.
	RequirePersonIDForEnrollment bool

	// RecognitionKeyBackfiller backfills historical recognitions when a person's
	// business key is bound for the first time. Nil disables the backfill.
	RecognitionKeyBackfiller RecognitionKeyBackfiller
}

// Service provides high-level business logic for personnel and face gallery.
type Service struct {
	store                        *Store
	extractor                    FaceFeatureExtractor
	galleryNotifier              GalleryNotifier
	keyBackfiller                RecognitionKeyBackfiller
	facesDir                     string
	logger                       *zap.Logger
	requirePersonIDForEnrollment bool
}

// NewService creates a Service instance. Every collaborator and policy flag is
// captured here and never swapped afterwards.
func NewService(
	store *Store,
	facesDir string,
	extractor FaceFeatureExtractor,
	notifier GalleryNotifier,
	opts Options,
	logger *zap.Logger,
) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Service{
		store:                        store,
		extractor:                    extractor,
		galleryNotifier:              notifier,
		keyBackfiller:                opts.RecognitionKeyBackfiller,
		facesDir:                     facesDir,
		logger:                       logger.Named("person.service"),
		requirePersonIDForEnrollment: opts.RequirePersonIDForEnrollment,
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

	nameChanged := false
	personIDChanged := false

	// person_id is the business key external systems correlate on. If not yet assigned (nil or empty),
	// it can be bound. Once assigned, attempts to change it are rejected with PERSON_ID_IMMUTABLE.
	if req.PersonID != nil {
		trimmed := strings.TrimSpace(*req.PersonID)
		switch {
		case p.PersonID == nil || *p.PersonID == "":
			if trimmed != "" {
				p.PersonID = &trimmed
				personIDChanged = true
			}
		case trimmed != *p.PersonID:
			return nil, apperr.Conflict("PERSON_ID_IMMUTABLE", "personId cannot be changed", nil)
		}
	}
	if req.Name != nil {
		name, err := normalizeName(*req.Name)
		if err != nil {
			return nil, err
		}
		if name != p.Name {
			p.Name = name
			nameChanged = true
		}
	}

	// A payload that repeats the stored values is not an update: writing it would
	// bump updated_at and re-broadcast the whole gallery entry of a person whose
	// identity did not change.
	if !nameChanged && !personIDChanged {
		return s.reloadPerson(ctx, id, p)
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

	// Binding the business key also retro-fills the identity snapshot of records that
	// were captured while the person had none, so they become reportable too.
	if personIDChanged {
		s.backfillRecognitionKeys(ctx, p)
	}

	// The gallery keys faces by personId and personName, so both must be re-published
	// whenever either changes.
	s.publishPersonFaces(ctx, p)

	return s.reloadPerson(ctx, id, p)
}

// reloadPerson re-reads a person with the face summary preloaded, so every write path
// answers with the same projection GET returns.
//
// A reload failure is logged rather than returned: the caller's write is already
// committed, and reporting an error would describe work that did happen as failed.
func (s *Service) reloadPerson(ctx context.Context, id string, fallback *Person) (*Person, error) {
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		s.logger.Warn("failed to reload person after update", zap.String("person_id", id), zap.Error(err))
		return fallback, nil
	}
	return p, nil
}

// backfillRecognitionKeys asks the record domain to stamp the business key onto every
// recognition of this person that was captured before the key existed. A failure is
// logged and swallowed: the binding itself is committed and stays valid.
func (s *Service) backfillRecognitionKeys(ctx context.Context, p *Person) {
	if s.keyBackfiller == nil || p.PersonID == nil || *p.PersonID == "" {
		return
	}
	n, err := s.keyBackfiller.BackfillPersonExternalID(ctx, p.ID, *p.PersonID)
	if err != nil {
		s.logger.Warn("failed to backfill recognition business keys",
			zap.String("person_id", p.ID),
			zap.String("external_person_id", *p.PersonID),
			zap.Error(err))
		return
	}
	if n > 0 {
		s.logger.Info("backfilled recognition business keys",
			zap.String("person_id", p.ID),
			zap.Int64("rows", n))
	}
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
//
// The removal goes through an os.Root handle for the same reason reads do: a symlink
// planted inside the faces directory must not be able to steer the delete elsewhere.
// os.Remove only unlinks the link itself, but keeping every filesystem operation on the
// same containment path removes the need to reason about which ones are safe.
func (s *Service) removeFaceFile(imagePath string) {
	if imagePath == "" {
		return
	}
	rel, err := s.faceStorageName(imagePath)
	if err != nil {
		s.logger.Warn("skipped deletion of face file outside faces directory", zap.String("path", imagePath), zap.Error(err))
		return
	}
	root, err := os.OpenRoot(s.facesDir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("failed to open faces directory for deletion", zap.String("dir", s.facesDir), zap.Error(err))
		}
		return
	}
	defer root.Close()
	if err := root.Remove(rel); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Warn("failed to delete face file", zap.String("path", rel), zap.Error(err))
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

// faceStorageName validates a stored image_path and returns it as a path relative to
// the faces directory, so it can be handed to an os.Root handle.
//
// Rows written by this service hold a bare file name. Rows restored from an older
// deployment may hold an absolute path; those are accepted only when they resolve
// inside the faces directory, and they are still converted back to a relative name so
// the open itself never re-traverses the path from the root.
func (s *Service) faceStorageName(imagePath string) (string, error) {
	if imagePath == "" {
		return "", apperr.NotFound("IMAGE_NOT_FOUND", "image path is empty", nil)
	}
	if strings.ContainsRune(imagePath, '\x00') {
		return "", apperr.Invalid("INVALID_IMAGE_PATH", "image path contains NUL bytes", nil)
	}

	clean := filepath.Clean(imagePath)
	if filepath.IsAbs(clean) {
		rel, err := filepath.Rel(filepath.Clean(s.facesDir), clean)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", apperr.Invalid("INVALID_IMAGE_PATH", "image path escapes allowed storage directory", nil)
		}
		clean = rel
	}

	for _, segment := range strings.Split(clean, string(filepath.Separator)) {
		if segment == "" || segment == "." || segment == ".." {
			return "", apperr.Invalid("INVALID_IMAGE_PATH", "image path must not contain relative segments", nil)
		}
	}
	return clean, nil
}

// faceFileName composes the stored file name for a newly enrolled face.
//
// personID arrives as a path parameter, so the characters that would turn the name
// into a nested path are rejected here instead of being assumed absent because the
// person lookup happened to succeed first.
func faceFileName(personID, faceID, ext string) (string, error) {
	name := personID + "_" + faceID + ext
	if name != filepath.Base(name) || strings.ContainsAny(name, `/:\\`) || strings.ContainsRune(name, '\x00') {
		return "", apperr.Internal("STORAGE_ERROR", "Failed to compose face image file name", nil)
	}
	return name, nil
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

	// An enrolled photo is the anchor a recognition is attributed by, and attribution
	// travels as the external business key. Refusing here keeps unreportable faces out
	// of the gallery instead of surfacing them later as records nobody can deliver.
	if s.requirePersonIDForEnrollment && (personMeta.PersonID == nil || *personMeta.PersonID == "") {
		return nil, apperr.Conflict("PERSON_ID_REQUIRED_FOR_ENROLLMENT",
			"Person must be assigned a personId before faces can be enrolled", nil)
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
	fileName, err := faceFileName(personID, faceID, ext)
	if err != nil {
		return nil, err
	}
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

// OpenFaceImage resolves the stored photo of a face template and returns it opened for
// reading, together with its metadata. The caller owns the file and must close it.
//
// The read goes through an os.Root handle rather than a validated absolute path. A
// prefix check followed by a separate open leaves exactly the window a symlink inside
// the faces directory needs: os.Root resolves relative to the directory handle and
// refuses anything that escapes it, absolute symlink targets included.
func (s *Service) OpenFaceImage(ctx context.Context, personID, faceID string) (*os.File, os.FileInfo, error) {
	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil, apperr.NotFound("FACE_NOT_FOUND", "Face not found", err)
		}
		return nil, nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to load face", err)
	}
	if face.PersonRowID != personID {
		return nil, nil, apperr.NotFound("FACE_NOT_FOUND", "Face not found", nil)
	}

	rel, err := s.faceStorageName(face.ImagePath)
	if err != nil {
		return nil, nil, err
	}

	root, err := os.OpenRoot(s.facesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, apperr.NotFound("IMAGE_NOT_FOUND", "Face image file not found on disk", err)
		}
		return nil, nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to open faces directory", err)
	}
	// Files opened through the root stay valid after it is closed.
	defer root.Close()

	file, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, apperr.NotFound("IMAGE_NOT_FOUND", "Face image file not found on disk", err)
		}
		return nil, nil, apperr.Invalid("INVALID_IMAGE_PATH", "image path escapes allowed storage directory", err)
	}

	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to stat face image", err)
	}
	if info.IsDir() {
		// os.Root.Open succeeds on a directory; a face image never is one.
		file.Close()
		return nil, nil, apperr.NotFound("IMAGE_NOT_FOUND", "Face image file not found on disk", nil)
	}
	return file, info, nil
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

// gallerySyncPageSize bounds how many persons (each with every feature vector) are
// materialised per query during a cold-start gallery sync. The assembled item slice is
// inherently whole — sharding needs all of it — but the database round trip does not
// have to be, and on an edge box with tens of thousands of faces that is the difference
// between one 40MB read and a stream of small ones.
const gallerySyncPageSize = 200

// GetGallerySyncData prepares all enrolled face templates for IPC downlinking to algorithm runners.
func (s *Service) GetGallerySyncData(ctx context.Context) ([]GalleryItem, error) {
	syncItems := make([]GalleryItem, 0)
	for offset := 0; ; offset += gallerySyncPageSize {
		persons, err := s.store.ListPersonsForGallery(ctx, gallerySyncPageSize, offset)
		if err != nil {
			return nil, apperr.New(apperr.KindInternal, "DATABASE_ERROR", "Failed to list gallery persons", err)
		}
		for _, p := range persons {
			personID := ""
			if p.PersonID != nil {
				personID = *p.PersonID
			}
			for _, f := range p.Faces {
				syncItems = append(syncItems, newGalleryItem(f, personID, p.Name))
			}
		}
		if len(persons) < gallerySyncPageSize {
			return syncItems, nil
		}
	}
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

// --- Bulk Person Import ---

const (
	// PersonImportMaxBytes bounds the CSV payload independent of the wider upload group limit.
	PersonImportMaxBytes int64 = 2 * 1024 * 1024
	// PersonImportMaxRows keeps preview and commit payloads bounded on the edge host.
	PersonImportMaxRows = 1000
)

var (
	errPersonImportEmpty       = errors.New("person import file is empty")
	errPersonImportHeader      = errors.New("person import header is invalid")
	errPersonImportTooLarge    = errors.New("person import file is too large")
	errPersonImportTooManyRows = errors.New("person import has too many rows")
)

type personImportIssue struct {
	Field string
	Code  string
}

type personImportRow struct {
	line     int
	name     string
	personID string
	issues   []personImportIssue
}

type personImportEntry struct {
	line     int
	name     string
	personID *string
}

type personImportCreateResult struct {
	line int
	err  error
}

// PreviewPersonImport parses and validates a CSV without creating person rows.
func (s *Service) PreviewPersonImport(ctx context.Context, reader io.Reader) (*PersonImportPreviewResponse, error) {
	rows, err := parsePersonImportCSV(reader)
	if err != nil {
		return nil, personImportFileError(err)
	}
	_, validated, err := s.validatePersonImport(ctx, rows)
	if err != nil {
		return nil, err
	}

	items := importRowResponses(validated)
	var failed int
	for _, item := range items {
		if !item.CanImport {
			failed++
		}
	}
	return &PersonImportPreviewResponse{
		Items:      items,
		Total:      len(items),
		Importable: len(items) - failed,
		Failed:     failed,
	}, nil
}

// CommitPersonImport parses and validates a CSV again, then creates valid rows in one transaction.
func (s *Service) CommitPersonImport(ctx context.Context, reader io.Reader) (*PersonImportCommitResponse, error) {
	rows, err := parsePersonImportCSV(reader)
	if err != nil {
		return nil, personImportFileError(err)
	}
	entries, validated, err := s.validatePersonImport(ctx, rows)
	if err != nil {
		return nil, err
	}

	created, err := s.store.CreatePersonImport(ctx, entries)
	if err != nil {
		return nil, apperr.Internal("PERSON_IMPORT_FAILED", "Failed to import persons", err)
	}
	createdByLine := make(map[int]personImportCreateResult, len(created))
	for _, result := range created {
		createdByLine[result.line] = result
	}

	items := make([]PersonImportRowResponse, 0, len(validated))
	var imported int
	for _, row := range validated {
		item := importRowResponse(row)
		if len(row.issues) == 0 {
			result := createdByLine[row.line]
			switch {
			case errors.Is(result.err, ErrDuplicatePersonID):
				item.Issues = []PersonImportIssueResponse{{Field: "personId", Code: "PERSON_ID_ALREADY_EXISTS"}}
				item.CanImport = false
			case result.err != nil:
				return nil, apperr.Internal("PERSON_IMPORT_FAILED", "Failed to import persons", result.err)
			default:
				item.CanImport = true
				item.Imported = true
				imported++
			}
		}
		items = append(items, item)
	}
	return &PersonImportCommitResponse{
		Items:    items,
		Total:    len(items),
		Imported: imported,
		Failed:   len(items) - imported,
	}, nil
}

func (s *Service) validatePersonImport(ctx context.Context, rows []personImportRow) ([]personImportEntry, []personImportRow, error) {
	candidates := make([]string, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.personID == "" {
			continue
		}
		if _, exists := seen[row.personID]; exists {
			continue
		}
		seen[row.personID] = struct{}{}
		candidates = append(candidates, row.personID)
	}
	existing, err := s.store.ListPersonIDs(ctx, candidates)
	if err != nil {
		return nil, nil, apperr.Internal("DATABASE_ERROR", "Failed to validate person import", err)
	}
	entries, validated := validatePersonImportRows(rows, existing)
	return entries, validated, nil
}

func parsePersonImportCSV(reader io.Reader) ([]personImportRow, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, PersonImportMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read person import file: %w", err)
	}
	if int64(len(payload)) > PersonImportMaxBytes {
		return nil, errPersonImportTooLarge
	}

	csvReader := csv.NewReader(bytes.NewReader(payload))
	csvReader.FieldsPerRecord = -1
	csvReader.ReuseRecord = false

	headers, err := csvReader.Read()
	if errors.Is(err, io.EOF) {
		return nil, errPersonImportEmpty
	}
	if err != nil {
		return nil, fmt.Errorf("read person import header: %w", err)
	}
	if len(headers) > 0 {
		headers[0] = strings.TrimPrefix(headers[0], "\uFEFF")
	}
	nameIndex, personIDIndex, err := personImportHeaderIndexes(headers)
	if err != nil {
		return nil, err
	}

	rows := []personImportRow{}
	for {
		record, readErr := csvReader.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read person import row: %w", readErr)
		}
		if len(record) == 0 {
			continue
		}
		line, _ := csvReader.FieldPos(0)
		if line == 0 {
			line = len(rows) + 2
		}

		row := personImportRow{line: line}
		if nameIndex < len(record) {
			row.name = strings.TrimSpace(record[nameIndex])
		}
		if personIDIndex < len(record) {
			row.personID = strings.TrimSpace(record[personIDIndex])
		}
		if len(record) != len(headers) {
			row.issues = append(row.issues, personImportIssue{Code: "IMPORT_COLUMN_COUNT"})
		}
		rows = append(rows, row)
		if len(rows) > PersonImportMaxRows {
			return nil, errPersonImportTooManyRows
		}
	}
	if len(rows) == 0 {
		return nil, errPersonImportEmpty
	}
	return rows, nil
}

func personImportHeaderIndexes(headers []string) (int, int, error) {
	if len(headers) != 2 {
		return 0, 0, errPersonImportHeader
	}
	nameIndex := -1
	personIDIndex := -1
	seen := make(map[string]struct{}, len(headers))
	for index, header := range headers {
		if _, exists := seen[header]; exists {
			return 0, 0, errPersonImportHeader
		}
		seen[header] = struct{}{}
		switch header {
		case "name":
			nameIndex = index
		case "personId":
			personIDIndex = index
		default:
			return 0, 0, errPersonImportHeader
		}
	}
	if nameIndex < 0 || personIDIndex < 0 {
		return 0, 0, errPersonImportHeader
	}
	return nameIndex, personIDIndex, nil
}

func validatePersonImportRows(rows []personImportRow, existing map[string]struct{}) ([]personImportEntry, []personImportRow) {
	counts := make(map[string]int)
	for _, row := range rows {
		if row.personID != "" {
			counts[row.personID]++
		}
	}

	entries := make([]personImportEntry, 0, len(rows))
	validated := make([]personImportRow, 0, len(rows))
	for _, row := range rows {
		row.issues = append([]personImportIssue(nil), row.issues...)
		switch {
		case row.name == "":
			row.issues = append(row.issues, personImportIssue{Field: "name", Code: "NAME_REQUIRED"})
		case utf8.RuneCountInString(row.name) > faceNameMaxLen:
			row.issues = append(row.issues, personImportIssue{Field: "name", Code: "NAME_TOO_LONG"})
		}
		if utf8.RuneCountInString(row.personID) > 64 {
			row.issues = append(row.issues, personImportIssue{Field: "personId", Code: "PERSON_ID_TOO_LONG"})
		}
		if row.personID != "" {
			if counts[row.personID] > 1 {
				row.issues = append(row.issues, personImportIssue{Field: "personId", Code: "IMPORT_DUPLICATE_PERSON_ID"})
			}
			if _, exists := existing[row.personID]; exists {
				row.issues = append(row.issues, personImportIssue{Field: "personId", Code: "PERSON_ID_ALREADY_EXISTS"})
			}
		}

		validated = append(validated, row)
		if len(row.issues) == 0 {
			var personID *string
			if row.personID != "" {
				value := row.personID
				personID = &value
			}
			entries = append(entries, personImportEntry{line: row.line, name: row.name, personID: personID})
		}
	}
	return entries, validated
}

func personImportFileError(err error) error {
	switch {
	case errors.Is(err, errPersonImportEmpty):
		return apperr.Invalid("IMPORT_FILE_EMPTY", "Person import file is empty", err)
	case errors.Is(err, errPersonImportHeader):
		return apperr.Invalid("IMPORT_HEADER_INVALID", "Person import header is invalid", err)
	case errors.Is(err, errPersonImportTooLarge):
		return apperr.Invalid("IMPORT_FILE_TOO_LARGE", "Person import file is too large", err)
	case errors.Is(err, errPersonImportTooManyRows):
		return apperr.Invalid("IMPORT_TOO_MANY_ROWS", "Person import has too many rows", err)
	default:
		return apperr.Invalid("IMPORT_CSV_INVALID", "Person import CSV is invalid", err)
	}
}

func importRowResponse(row personImportRow) PersonImportRowResponse {
	return PersonImportRowResponse{
		Line:      row.line,
		Name:      row.name,
		PersonID:  row.personID,
		CanImport: len(row.issues) == 0,
		Issues:    importIssueResponses(row.issues),
	}
}

func importRowResponses(rows []personImportRow) []PersonImportRowResponse {
	items := make([]PersonImportRowResponse, 0, len(rows))
	for _, row := range rows {
		items = append(items, importRowResponse(row))
	}
	return items
}

func importIssueResponses(issues []personImportIssue) []PersonImportIssueResponse {
	out := make([]PersonImportIssueResponse, 0, len(issues))
	for _, issue := range issues {
		out = append(out, PersonImportIssueResponse{Field: issue.Field, Code: issue.Code})
	}
	return out
}
