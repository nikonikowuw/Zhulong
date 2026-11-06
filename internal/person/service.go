package person

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"

	_ "golang.org/x/image/webp"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
)

const (
	// MaxFaceImageBytes caps an uploaded enrollment photo.
	MaxFaceImageBytes = 10 * 1024 * 1024 // 10MB
	// MaxFacesPerPerson is the maximum number of enrolled face templates per person.
	MaxFacesPerPerson = 10
	// faceFeatureDim is the embedding width every face extractor must produce.
	// It is persisted as faceFeatureDim*4 Little-Endian bytes.
	faceFeatureDim = 512
	// personNameMaxLen mirrors the max=64 binding on the create/update payloads. It is
	// compared against rune counts, so a CJK name is measured the way the user sees it.
	personNameMaxLen = 64
	// personExternalIDMaxLen mirrors the max=64 binding on the external business key.
	personExternalIDMaxLen = 64
	// searchFaceBatchSize bounds how many feature vectors one 1:N search query loads.
	searchFaceBatchSize = 500
	// gallerySyncPageSize bounds how many persons (each with every feature vector) are
	// materialised per query during a cold-start gallery sync. The assembled item slice is
	// inherently whole — sharding needs all of it — but the database round trip does not
	// have to be, and on an edge box with tens of thousands of faces that is the difference
	// between one 40MB read and a stream of small ones.
	gallerySyncPageSize = 200
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
	// Extractor turns an enrollment photo into the embedding that is persisted with the
	// face row. Nil disables every path that needs features (enrollment and search) and
	// surfaces FEATURE_EXTRACTOR_UNAVAILABLE instead of a generic failure.
	Extractor FaceFeatureExtractor

	// GalleryNotifier publishes gallery changes to the running algorithm runners. Nil
	// means "no runner to notify": every broadcast becomes a no-op.
	GalleryNotifier GalleryNotifier

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

	// AsyncJobs is the system-wide background task runner.
	AsyncJobs AsyncJobSubmitter
}

// AsyncJobSubmitter abstracts job submission to the system background task runner.
type AsyncJobSubmitter interface {
	SubmitJob(ctx context.Context, jobType string, metadataJSON string) (*asyncjob.Job, error)
	GetJob(ctx context.Context, id string) (*asyncjob.Job, error)
}

// Service provides high-level business logic for personnel and face gallery.
type Service struct {
	store                        *Store
	extractor                    FaceFeatureExtractor
	galleryNotifier              GalleryNotifier
	keyBackfiller                RecognitionKeyBackfiller
	asyncJobs                    AsyncJobSubmitter
	facesDir                     string
	tempDir                      string
	logger                       *zap.Logger
	requirePersonIDForEnrollment bool
}

// NewService creates a Service instance. Every collaborator and policy flag is
// captured here and never swapped afterwards.
func NewService(store *Store, facesDir string, logger *zap.Logger, opts Options) *Service {
	if logger == nil {
		logger = zap.NewNop()
	}
	tempDir := filepath.Join(filepath.Dir(facesDir), "tmp")

	return &Service{
		store:                        store,
		extractor:                    opts.Extractor,
		galleryNotifier:              opts.GalleryNotifier,
		keyBackfiller:                opts.RecognitionKeyBackfiller,
		asyncJobs:                    opts.AsyncJobs,
		facesDir:                     facesDir,
		tempDir:                      tempDir,
		logger:                       logger.Named("person.service"),
		requirePersonIDForEnrollment: opts.RequirePersonIDForEnrollment,
	}
}

// Start prepares filesystem prerequisites and cleans up stale artifacts left by prior runs.
func (s *Service) Start(_ context.Context) error {
	if err := os.MkdirAll(s.tempDir, 0755); err != nil {
		s.logger.Warn("failed to create person service temp directory",
			zap.String("dir", s.tempDir),
			zap.Error(err),
		)
		return err
	}
	cleanStaleFaceImportDirs(s.tempDir, s.logger)
	return nil
}

func cleanStaleFaceImportDirs(tempDir string, logger *zap.Logger) {
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "face_import_") {
			fullPath := filepath.Join(tempDir, entry.Name())
			if err := os.RemoveAll(fullPath); err != nil && logger != nil {
				logger.Warn("failed to clean up stale face import directory", zap.String("path", fullPath), zap.Error(err))
			}
		}
	}
}

// normalizePersonName trims a display name and rejects the blank or over-long result.
//
// Every entry point that writes a person name goes through it, so the CSV import, the
// REST handler and the machine API cannot disagree about what a valid name is (they did:
// one path counted bytes, the others counted runes).
func normalizePersonName(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", apperr.Invalid("NAME_REQUIRED", "Name is required", nil)
	}
	if utf8.RuneCountInString(trimmed) > personNameMaxLen {
		return "", apperr.Invalid("NAME_TOO_LONG", "Name is too long", nil)
	}
	return trimmed, nil
}

// storeError converts a Store failure into the error the caller must return.
//
// notFoundCode/notFoundMessage describe the missing row, so callers keep naming the
// resource the user asked for; every other failure is a DATABASE_ERROR whose text is
// only ever seen in the server log. It exists because the same five-line mapping was
// repeated at every store call in this file, and a mapping that is copied is a mapping
// that drifts.
func storeError(err error, notFoundCode, notFoundMessage, operation string) error {
	if errors.Is(err, ErrNotFound) {
		return apperr.NotFound(notFoundCode, notFoundMessage, err)
	}
	return apperr.Internal("DATABASE_ERROR", operation, err)
}

// personLookupError maps a failed person lookup, the most common store failure in
// this service.
func personLookupError(err error, operation string) error {
	return storeError(err, "PERSON_NOT_FOUND", "Person not found", operation)
}

// faceLookupError maps a failed face lookup.
func faceLookupError(err error, operation string) error {
	return storeError(err, "FACE_NOT_FOUND", "Face not found", operation)
}

// CreatePerson creates a new person profile.
func (s *Service) CreatePerson(ctx context.Context, req CreatePersonRequest) (*Person, error) {
	name, err := normalizePersonName(req.Name)
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
		ID:         idutil.New(),
		PersonID:   personID,
		Name:       name,
		Source:     PersonSourceLocal,
		SourceName: "",
	}

	if err := s.store.CreatePerson(ctx, p); err != nil {
		if errors.Is(err, ErrDuplicateID) {
			return nil, apperr.Conflict("PERSON_ALREADY_EXISTS", "Person already exists", err)
		}
		if errors.Is(err, ErrDuplicatePersonID) {
			return nil, apperr.Conflict("PERSON_ID_ALREADY_EXISTS", "Person with this personId already exists", err)
		}
		return nil, apperr.Internal("PERSON_CREATE_FAILED", "Failed to create person", err)
	}

	return p, nil
}

// GetPerson returns a person by ID.
func (s *Service) GetPerson(ctx context.Context, id string) (*Person, error) {
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		return nil, personLookupError(err, "Failed to get person")
	}
	return p, nil
}

// ListPersons returns one page of person profiles plus the total row count.
func (s *Service) ListPersons(
	ctx context.Context,
	limit, offset int,
	search, faceStatus string,
) ([]Person, int64, error) {
	list, total, err := s.store.ListPersons(
		ctx,
		limit,
		offset,
		search,
		faceStatus,
	)
	if err != nil {
		return nil, 0, apperr.Internal("DATABASE_ERROR", "Failed to list persons", err)
	}
	return list, total, nil
}

// UpdatePerson updates an existing person's metadata.
func (s *Service) UpdatePerson(ctx context.Context, id string, req UpdatePersonRequest) (*Person, error) {
	p, err := s.store.GetPersonMeta(ctx, id)
	if err != nil {
		return nil, personLookupError(err, "Failed to get person")
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
		name, err := normalizePersonName(*req.Name)
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
		switch {
		case errors.Is(err, ErrDuplicatePersonID):
			return nil, apperr.Conflict("PERSON_ID_ALREADY_EXISTS", "Person with this personId already exists", err)
		default:
			return nil, personLookupError(err, "Failed to update person")
		}
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
		return nil, personLookupError(err, "Failed to verify person")
	}

	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		return nil, faceLookupError(err, "Failed to load face")
	}
	if face.PersonRowID != id {
		return nil, apperr.Invalid("INVALID_FACE_OWNER", "Face does not belong to this person", nil)
	}

	if err := s.store.SetPrimaryFace(ctx, id, faceID); err != nil {
		return nil, personLookupError(err, "Failed to set primary face")
	}

	// Reload so the caller sees the pointer as persisted, not the pre-update copy.
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		return nil, personLookupError(err, "Failed to reload person")
	}
	return p, nil
}

// DeletePerson deletes a person and their faces.
func (s *Service) DeletePerson(ctx context.Context, id string) error {
	p, err := s.store.GetPerson(ctx, id)
	if err != nil {
		return personLookupError(err, "Failed to verify person")
	}

	// GetPerson preloads the face rows, so the enrolled photos are known without a
	// second query and the broadcast below can name each face individually.
	faces := p.Faces

	if err := s.store.DeletePerson(ctx, id); err != nil {
		return personLookupError(err, "Failed to delete person")
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

// sniffImageExt identifies a supported image container from its leading bytes and
// returns the canonical extension, or "" when the bytes are none of them. The
// extension check and the content check therefore share one definition of
// "supported image" instead of two copies of the same magic numbers.
func sniffImageExt(data []byte) string {
	switch {
	case len(data) >= 3 && data[0] == 0xff && data[1] == 0xd8 && data[2] == 0xff:
		return ".jpg" // JPEG
	case len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n")):
		return ".png" // PNG
	case len(data) >= 12 && string(data[0:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return ".webp" // WebP
	default:
		return ""
	}
}

// isSupportedImageContent reports whether data is a JPEG, PNG or WebP payload. The
// magic bytes come first because they are exact; http.DetectContentType is only the
// fallback for payloads whose leading bytes alone are inconclusive.
func isSupportedImageContent(data []byte) bool {
	if sniffImageExt(data) != "" {
		return true
	}
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png", "image/webp":
		return true
	default:
		return false
	}
}

// isSearchableImageFormat reports whether a decoded container may be searched. Animated
// containers are excluded: a single embedding cannot describe a moving subject.
func isSearchableImageFormat(format string) bool {
	switch format {
	case "jpeg", "png", "webp":
		return true
	default:
		return false
	}
}

// tooLargeError reports an upload over MaxFaceImageBytes. It is shared so every
// enrollment path answers 413 with the same code instead of one path saying 413 and
// another saying 422.
func tooLargeError() error {
	return apperr.PayloadTooLarge("FILE_TOO_LARGE", "file size exceeds the 10MB limit", nil)
}

// addFaceRowWithLimit persists an enrolled face row and removes the image file it was
// written from when the row cannot be created. Writing the file and writing the row are
// two separate steps, so the compensating delete belongs with the step that failed —
// every enrollment path shares it instead of repeating the same five lines.
//
// The returned flag says whether the row became the person's primary face, as decided
// inside the inserting transaction.
func (s *Service) addFaceRowWithLimit(ctx context.Context, face *Face, savePath string) (bool, error) {
	isPrimary, err := s.store.AddFaceWithLimit(ctx, face, MaxFacesPerPerson)
	if err == nil {
		return isPrimary, nil
	}
	if rmErr := os.Remove(savePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
		s.logger.Warn("failed to clean up face image after enrollment failure",
			zap.String("path", savePath), zap.Error(rmErr))
	}
	return false, err
}

// faceEnrollError maps a failed face insert onto the API contract.
func faceEnrollError(err error, personName string) error {
	if errors.Is(err, ErrFaceLimitExceeded) {
		return apperr.Conflict("FACE_LIMIT_EXCEEDED",
			fmt.Sprintf("Person %q has reached the maximum of %d enrolled faces", personName, MaxFacesPerPerson),
			err)
	}
	return apperr.Internal("FACE_ADD_FAILED", "Failed to add face template", err)
}

// AddFace validates and enrolls a face template from an uploaded multipart file header.
func (s *Service) AddFace(ctx context.Context, personID string, file *multipart.FileHeader) (*Face, error) {
	if file == nil {
		return nil, apperr.Invalid("FILE_REQUIRED", "file is required", nil)
	}
	if file.Size > MaxFaceImageBytes {
		return nil, tooLargeError()
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
		return nil, personLookupError(err, "Failed to verify person")
	}

	// An enrolled photo is the anchor a recognition is attributed by, and attribution
	// travels as the external business key. Refusing here keeps unreportable faces out
	// of the gallery instead of surfacing them later as records nobody can deliver.
	if s.requirePersonIDForEnrollment && (personMeta.PersonID == nil || *personMeta.PersonID == "") {
		return nil, apperr.Conflict("PERSON_ID_REQUIRED_FOR_ENROLLMENT",
			"Person must be assigned a personId before faces can be enrolled", nil)
	}

	face, savePath, err := s.enrollFace(ctx, personMeta, FaceUpload{
		Filename:   filename,
		Content:    r,
		RequireExt: true,
	})
	if err != nil {
		return nil, err
	}

	if _, err := s.addFaceRowWithLimit(ctx, face, savePath); err != nil {
		return nil, faceEnrollError(err, personMeta.Name)
	}

	personIDKey := ""
	if personMeta.PersonID != nil {
		personIDKey = *personMeta.PersonID
	}
	s.broadcastUpsert(ctx, newGalleryItem(*face, personIDKey, personMeta.Name))

	return face, nil
}

// NormalizeExternalPersonID validates and trims an external person ID.
// It enforces 1-64 characters and rejects URI delimiters and control characters.
func NormalizeExternalPersonID(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", apperr.Invalid("PERSON_ID_REQUIRED", "person_id is required", nil)
	}
	if len(trimmed) > personExternalIDMaxLen {
		return "", apperr.Invalid("PERSON_ID_TOO_LONG", "person_id exceeds maximum length of 64 characters", nil)
	}
	for i := 0; i < len(trimmed); i++ {
		b := trimmed[i]
		if b < 32 || b == 127 {
			return "", apperr.Invalid("PERSON_ID_INVALID_CHARS", "person_id cannot contain control characters", nil)
		}
		if b == '/' || b == '\\' || b == '?' || b == '#' || b == '%' {
			return "", apperr.Invalid("PERSON_ID_INVALID_CHARS", "person_id cannot contain URI path or query delimiters", nil)
		}
	}
	return trimmed, nil
}

// FaceUpload is one enrollment photo plus the policy its entry point applies.
//
// The entry points disagree about the client-supplied filename: a multipart upload must
// carry an accepted extension, while the machine API only promises image bytes and gets
// its extension sniffed from the content. Grouping the payload with that policy keeps
// the signature readable instead of adding another positional bool.
type FaceUpload struct {
	// Filename is the name the client sent; only its extension is used.
	Filename string
	// Content is the image bytes. The reader is consumed once and not retained.
	Content io.Reader
	// Replace asks the machine API to drop the person's existing templates. It is
	// ignored by the multipart enrollment path, which always appends.
	Replace bool
	// RequireExt trusts the client extension instead of sniffing the content. Only the
	// multipart path sets it.
	RequireExt bool
}

func (s *Service) enrollFace(ctx context.Context, personMeta *Person, upload FaceUpload) (*Face, string, error) {
	r := upload.Content
	ext := strings.ToLower(filepath.Ext(upload.Filename))
	if upload.RequireExt {
		if _, ok := allowedFaceImageExts[ext]; !ok {
			return nil, "", invalidFileTypeError()
		}
	}

	headerBuf := make([]byte, 512)
	nHeader, err := io.ReadFull(r, headerBuf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, "", apperr.Invalid("FILE_READ_FAILED", "Failed to read file content", err)
	}
	if nHeader == 0 {
		return nil, "", apperr.Invalid("FILE_REQUIRED", "file is required", nil)
	}
	if !isSupportedImageContent(headerBuf[:nHeader]) {
		return nil, "", invalidFileTypeError()
	}

	if !upload.RequireExt {
		if _, ok := allowedFaceImageExts[ext]; !ok {
			ext = sniffImageExt(headerBuf[:nHeader])
			if ext == "" {
				return nil, "", invalidFileTypeError()
			}
		}
	}

	combinedReader := io.MultiReader(bytes.NewReader(headerBuf[:nHeader]), r)
	lr := io.LimitReader(combinedReader, MaxFaceImageBytes+1)
	fullBytes, err := io.ReadAll(lr)
	if err != nil {
		return nil, "", apperr.Invalid("FILE_READ_FAILED", "Failed to read file content", err)
	}
	if len(fullBytes) > MaxFaceImageBytes {
		return nil, "", tooLargeError()
	}

	if s.extractor == nil {
		return nil, "", apperr.Internal("FEATURE_EXTRACTOR_UNAVAILABLE",
			"Face feature extraction is not configured",
			nil,
		)
	}
	features, err := s.extractor.ExtractFaceFeature(ctx, fullBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return nil, "", apperr.BadRequest("NO_FACE_DETECTED", "No detectable face found in the uploaded photo", err)
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return nil, "", apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "The uploaded image could not be decoded", err)
		}
		return nil, "", apperr.Internal("FEATURE_EXTRACT_FAILED", "Failed to extract facial features", err)
	}
	if len(features) != faceFeatureDim {
		return nil, "", apperr.Internal("FEATURE_DIM_MISMATCH", "Unexpected feature dimension", nil)
	}
	featBytes := float32ToBytes(features)

	if err := os.MkdirAll(s.facesDir, 0755); err != nil {
		return nil, "", apperr.Internal("STORAGE_ERROR", "Failed to create faces directory", err)
	}

	faceID := idutil.New()
	fileName, err := faceFileName(personMeta.ID, faceID, ext)
	if err != nil {
		return nil, "", err
	}
	savePath := filepath.Join(s.facesDir, fileName)
	if err := os.WriteFile(savePath, fullBytes, 0644); err != nil {
		return nil, "", apperr.Internal("STORAGE_ERROR", "Failed to save face image", err)
	}

	face := &Face{
		ID:            faceID,
		PersonRowID:   personMeta.ID,
		ImagePath:     fileName,
		FeatureVector: featBytes,
	}
	return face, savePath, nil
}

// UpsertPersonByExternalID provisions a person by their external business key.
// If the person exists, their name is updated (if changed) without touching source tracking.
// If the person does not exist, a new person is created with Source=API and SourceName=tokenName.
// Returns the person model, a boolean indicating whether a new row was created, and an error.
func (s *Service) UpsertPersonByExternalID(
	ctx context.Context,
	externalID, name, tokenName string,
) (*Person, bool, error) {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return nil, false, err
	}
	trimmedName, err := normalizePersonName(name)
	if err != nil {
		return nil, false, err
	}

	p, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	switch {
	case err == nil:
		updated, upErr := s.renameProvisionedPerson(ctx, p, trimmedName)
		return updated, false, upErr

	case errors.Is(err, ErrNotFound):
		now := time.Now().UTC()
		newPerson := &Person{
			ID:         idutil.New(),
			PersonID:   &normID,
			Name:       trimmedName,
			Source:     PersonSourceAPI,
			SourceName: strings.TrimSpace(tokenName),
			CreatedAt:  now,
			UpdatedAt:  now,
		}
		createErr := s.store.CreatePerson(ctx, newPerson)
		if createErr == nil {
			return newPerson, true, nil
		}
		// A concurrent provision of the same key may have won the insert: re-read and
		// fall back to the update path instead of reporting a spurious failure.
		if errors.Is(createErr, ErrDuplicatePersonID) {
			existing, getErr := s.store.GetPersonMetaByExternalID(ctx, normID)
			if getErr == nil {
				updated, upErr := s.renameProvisionedPerson(ctx, existing, trimmedName)
				return updated, false, upErr
			}
		}
		return nil, false, apperr.Internal("PERSON_CREATE_FAILED", "Failed to create person", createErr)

	default:
		return nil, false, apperr.Internal("DATABASE_ERROR", "Failed to query person", err)
	}
}

// renameProvisionedPerson brings an existing provisioned person's name in line with the
// caller's, reusing the ordinary update path so the gallery re-publish and the audit of
// a rename happen exactly once, wherever the rename came from.
//
// Both branches answer with the same projection: the update path reloads the person
// with its face summary, and the no-op path has to do the same or a repeated upsert
// would answer with an empty face list that the create path never returns.
func (s *Service) renameProvisionedPerson(ctx context.Context, p *Person, name string) (*Person, error) {
	if p.Name == name {
		return s.reloadPerson(ctx, p.ID, p)
	}
	return s.UpdatePerson(ctx, p.ID, UpdatePersonRequest{Name: &name})
}

// FaceEnrollment is the outcome of one enrollment: the stored template plus the two
// facts the caller cannot derive afterwards.
//
// IsPrimary is resolved by the transaction that inserted the row. Re-deriving it from a
// later read races with concurrent enrollments of the same person, and the loser of that
// race reports a primary face it did not get.
type FaceEnrollment struct {
	Face      *Face
	Replaced  bool
	IsPrimary bool
}

// AddFaceByExternalID enrolls a face for the person identified by externalID. When
// upload.Replace is set, all existing faces are atomically replaced with the new one.
func (s *Service) AddFaceByExternalID(
	ctx context.Context,
	externalID string,
	upload FaceUpload,
) (*FaceEnrollment, error) {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return nil, err
	}
	personMeta, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	if err != nil {
		return nil, personLookupError(err, "Failed to verify person")
	}

	face, savePath, err := s.enrollFace(ctx, personMeta, upload)
	if err != nil {
		return nil, err
	}

	if upload.Replace {
		removed, err := s.store.ReplaceFaces(ctx, personMeta.ID, face)
		if err != nil {
			if rmErr := os.Remove(savePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				s.logger.Warn("failed to clean up face image after replace failure",
					zap.String("path", savePath), zap.Error(rmErr))
			}
			return nil, apperr.Internal("FACE_REPLACE_FAILED", "Failed to replace face templates", err)
		}

		for _, f := range removed {
			s.removeFaceFile(f.ImagePath)
		}

		// Atomic IPC notification: broadcast delete for externalID, followed by upsert for new face
		s.broadcastDelete(ctx, "", normID)
		s.broadcastUpsert(ctx, newGalleryItem(*face, normID, personMeta.Name))

		// ReplaceFaces rewrites primary_face_id to the replacement inside its transaction.
		return &FaceEnrollment{Face: face, Replaced: true, IsPrimary: true}, nil
	}

	// Normal append enrollment
	isPrimary, err := s.addFaceRowWithLimit(ctx, face, savePath)
	if err != nil {
		return nil, faceEnrollError(err, personMeta.Name)
	}

	s.broadcastUpsert(ctx, newGalleryItem(*face, normID, personMeta.Name))
	return &FaceEnrollment{Face: face, Replaced: false, IsPrimary: isPrimary}, nil
}

// ClearFacesByExternalID deletes all enrolled faces for the given externalID.
func (s *Service) ClearFacesByExternalID(ctx context.Context, externalID string) (int, error) {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return 0, err
	}
	personMeta, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	if err != nil {
		return 0, personLookupError(err, "Failed to verify person")
	}

	removed, err := s.store.ClearFaces(ctx, personMeta.ID)
	if err != nil {
		return 0, apperr.Internal("FACE_CLEAR_FAILED", "Failed to clear faces", err)
	}

	for _, f := range removed {
		s.removeFaceFile(f.ImagePath)
	}
	if len(removed) > 0 {
		s.broadcastDelete(ctx, "", normID)
	}
	return len(removed), nil
}

// DeletePersonByExternalID deletes a person and all their faces using the external business key.
func (s *Service) DeletePersonByExternalID(ctx context.Context, externalID string) error {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return err
	}
	personMeta, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	if err != nil {
		return personLookupError(err, "Failed to verify person")
	}
	return s.DeletePerson(ctx, personMeta.ID)
}

// DeleteFaceByExternalID deletes a specific face template under the person identified by externalID.
func (s *Service) DeleteFaceByExternalID(ctx context.Context, externalID, faceID string) error {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return err
	}
	personMeta, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	if err != nil {
		return personLookupError(err, "Failed to verify person")
	}

	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		return faceLookupError(err, "Failed to verify face")
	}
	if face.PersonRowID != personMeta.ID {
		return apperr.NotFound("FACE_NOT_FOUND", "Face does not belong to this person", nil)
	}

	return s.DeleteFace(ctx, personMeta.ID, faceID)
}

// SetPrimaryFaceByExternalID designates faceID as the primary face for the person identified by externalID.
func (s *Service) SetPrimaryFaceByExternalID(ctx context.Context, externalID, faceID string) (*Person, error) {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return nil, err
	}
	personMeta, err := s.store.GetPersonMetaByExternalID(ctx, normID)
	if err != nil {
		return nil, personLookupError(err, "Failed to verify person")
	}

	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		return nil, faceLookupError(err, "Failed to verify face")
	}
	if face.PersonRowID != personMeta.ID {
		return nil, apperr.NotFound("FACE_NOT_FOUND", "Face does not belong to this person", nil)
	}

	return s.SetPrimaryFace(ctx, personMeta.ID, faceID)
}

// GetPersonByExternalID retrieves a person and their faces by external business key.
func (s *Service) GetPersonByExternalID(ctx context.Context, externalID string) (*Person, error) {
	normID, err := NormalizeExternalPersonID(externalID)
	if err != nil {
		return nil, err
	}
	p, err := s.store.GetPersonByExternalID(ctx, normID)
	if err != nil {
		return nil, personLookupError(err, "Failed to get person")
	}
	return p, nil
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
		return nil, nil, faceLookupError(err, "Failed to load face")
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
		return nil, nil, apperr.Internal("STORAGE_ERROR", "Failed to open faces directory", err)
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
		return nil, nil, apperr.Internal("STORAGE_ERROR", "Failed to stat face image", err)
	}
	if info.IsDir() {
		// os.Root.Open succeeds on a directory; a face image never is one.
		file.Close()
		return nil, nil, apperr.NotFound("IMAGE_NOT_FOUND", "Face image file not found on disk", nil)
	}
	return file, info, nil
}

func isFinite(f float32) bool {
	return !math.IsNaN(float64(f)) && !math.IsInf(float64(f), 0)
}

// SearchByImage extracts an embedding vector from the uploaded face photo,
// computes dot-product similarities against all valid enrolled face templates,
// aggregates candidate scores per person taking the maximum similarity and its corresponding
// face template, filters by threshold, and returns the top-K matches sorted descending by similarity.
//
// The query image resides strictly in ephemeral memory during request execution and is never persisted.
func (s *Service) SearchByImage(
	ctx context.Context,
	imgBytes []byte,
	threshold float32,
	topK int,
) (*ImageSearchResponse, error) {
	if len(imgBytes) == 0 {
		return nil, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Image is empty", nil)
	}
	if len(imgBytes) > MaxFaceImageBytes {
		return nil, tooLargeError()
	}

	// Validate static JPEG, PNG, or WebP and dimension <= 4096.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Unsupported image format or corrupt image", err)
	}
	if !isSearchableImageFormat(format) {
		return nil, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Only static JPEG, PNG, and WebP are allowed", nil)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		return nil, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Image dimensions exceed the size limit", nil)
	}

	if s.extractor == nil {
		return nil, apperr.Internal("EXTRACTOR_ERROR", "Face feature extraction is not configured", nil)
	}

	features, err := s.extractor.ExtractFaceFeature(ctx, imgBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return nil, apperr.BadRequest("NO_FACE_DETECTED", "No face detected in image", err)
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return nil, apperr.BadRequest("UNSUPPORTED_IMAGE_FORMAT", "Unsupported image format or corrupt image", err)
		}
		return nil, apperr.Internal("EXTRACTOR_ERROR", "Facial feature extraction failed", err)
	}

	if len(features) != faceFeatureDim {
		return nil, apperr.Internal("EXTRACTOR_ERROR", "Unexpected feature dimension", nil)
	}
	for _, v := range features {
		if !isFinite(v) {
			return nil, apperr.Internal("EXTRACTOR_ERROR", "Feature vector contains invalid numbers", nil)
		}
	}

	bestPerPerson := make(map[string]ImageSearchMatchInfo)
	for offset := 0; ; offset += searchFaceBatchSize {
		items, err := s.store.ListSearchFaceItemsPaged(ctx, searchFaceBatchSize, offset)
		if err != nil {
			return nil, apperr.Internal("DATABASE_ERROR", "Failed to list face templates", err)
		}

		for _, item := range items {
			if len(item.FeatureVector) != faceFeatureDim*4 {
				continue
			}
			gf := bytesToFloat32(item.FeatureVector)
			if len(gf) != faceFeatureDim {
				continue
			}
			valid := true
			for _, v := range gf {
				if !isFinite(v) {
					valid = false
					break
				}
			}
			if !valid {
				continue
			}

			var sim float32
			for i := 0; i < faceFeatureDim; i++ {
				sim += features[i] * gf[i]
			}
			sim = max(-1, min(1, sim))

			existing, ok := bestPerPerson[item.PersonRowID]
			if !ok || sim > existing.Similarity || (sim == existing.Similarity && item.FaceID < existing.MatchedFaceID) {
				bestPerPerson[item.PersonRowID] = ImageSearchMatchInfo{
					PersonID:         item.PersonRowID,
					PersonExternalID: item.PersonExternalID,
					PersonName:       item.PersonName,
					MatchedFaceID:    item.FaceID,
					Similarity:       sim,
				}
			}
		}

		if len(items) < searchFaceBatchSize {
			break
		}
	}

	candidates := make([]ImageSearchMatchInfo, 0, len(bestPerPerson))
	for _, match := range bestPerPerson {
		if match.Similarity >= threshold {
			candidates = append(candidates, match)
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Similarity != candidates[j].Similarity {
			return candidates[i].Similarity > candidates[j].Similarity
		}
		return candidates[i].PersonID < candidates[j].PersonID
	})

	if topK > 0 && len(candidates) > topK {
		candidates = candidates[:topK]
	}

	return &ImageSearchResponse{Matches: candidates}, nil
}

// DeleteFace deletes a specific face template belonging to the given person.
func (s *Service) DeleteFace(ctx context.Context, personID, faceID string) error {
	face, err := s.store.GetFace(ctx, faceID)
	if err != nil {
		return faceLookupError(err, "Failed to load face")
	}
	// Scoping to the owning person keeps the nested resource path meaningful: a
	// face is not addressable through another person's path segment.
	if face.PersonRowID != personID {
		return apperr.NotFound("FACE_NOT_FOUND", "Face not found", nil)
	}

	person, err := s.store.GetPersonMeta(ctx, personID)
	if err != nil {
		return personLookupError(err, "Failed to load person")
	}

	if err := s.store.DeleteFace(ctx, personID, faceID); err != nil {
		return faceLookupError(err, "Failed to delete face")
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
		return nil, 0, personLookupError(err, "Failed to verify person")
	}

	faces, total, err := s.store.ListFacesByPersonPaged(ctx, personID, limit, offset)
	if err != nil {
		return nil, 0, apperr.Internal("DATABASE_ERROR", "Failed to list faces", err)
	}
	return faces, total, nil
}

// GetGallerySyncData prepares all enrolled face templates for IPC downlinking to algorithm runners.
func (s *Service) GetGallerySyncData(ctx context.Context) ([]GalleryItem, error) {
	syncItems := make([]GalleryItem, 0)
	for offset := 0; ; offset += gallerySyncPageSize {
		persons, err := s.store.ListPersonsForGallery(ctx, gallerySyncPageSize, offset)
		if err != nil {
			return nil, apperr.Internal("DATABASE_ERROR", "Failed to list gallery persons", err)
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

func (s *Service) validatePersonImport(
	ctx context.Context,
	rows []personImportRow,
) ([]personImportEntry, []personImportRow, error) {
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
		// FieldPos returns (line, column) and reports 0/0 only when the record has no
		// field 0 — impossible here, because an empty record is skipped above — so the
		// arithmetic fallback is a belt-and-braces default rather than a real branch.
		row := personImportRow{line: len(rows) + 2}
		if line, _ := csvReader.FieldPos(0); line > 0 {
			row.line = line
		}

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

func validatePersonImportRows(
	rows []personImportRow,
	existing map[string]struct{},
) ([]personImportEntry, []personImportRow) {
	counts := make(map[string]int)
	for _, row := range rows {
		if row.personID != "" {
			counts[row.personID]++
		}
	}

	entries := make([]personImportEntry, 0, len(rows))
	validated := make([]personImportRow, 0, len(rows))
	for _, row := range rows {
		row.issues = slices.Clone(row.issues)
		switch {
		case row.name == "":
			row.issues = append(row.issues, personImportIssue{Field: "name", Code: "NAME_REQUIRED"})
		case utf8.RuneCountInString(row.name) > personNameMaxLen:
			row.issues = append(row.issues, personImportIssue{Field: "name", Code: "NAME_TOO_LONG"})
		}
		if utf8.RuneCountInString(row.personID) > personExternalIDMaxLen {
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

// ExportPersonsManifestCSV generates a manifest CSV for all persons matching the filter.
//
// The output is the round-trip companion of the ZIP importer: its columns are the ones
// parseManifestColumns accepts (name / personId / imagePath / isPrimary), and the
// suggested imagePath uses the `images/<personId>_<name>.jpg` shape that both the
// manifest reader and the manifest-less filename fallback can resolve.
func (s *Service) ExportPersonsManifestCSV(ctx context.Context, search, faceStatus string) ([]byte, string, error) {
	persons, err := s.store.ListAllPersonsForExport(ctx, search, faceStatus)
	if err != nil {
		return nil, "", apperr.Internal("DATABASE_ERROR", "Failed to list persons for export", err)
	}

	var buf bytes.Buffer
	if _, err := buf.Write([]byte("\xef\xbb\xbf")); err != nil {
		return nil, "", fmt.Errorf("write csv bom: %w", err)
	}

	w := csv.NewWriter(&buf)
	if err := w.Write([]string{"name", "personId", "imagePath", "isPrimary", "faceCount"}); err != nil {
		return nil, "", fmt.Errorf("write csv header: %w", err)
	}

	// Two operators may legitimately share a name and carry no business key, so the
	// suggested paths are de-duplicated: two rows pointing at one photo would enroll the
	// same face under two identities and poison the gallery.
	usedPaths := make(map[string]int, len(persons))

	for _, p := range persons {
		personID := ""
		if p.PersonID != nil {
			personID = strings.TrimSpace(*p.PersonID)
		}

		// isPrimary stays false for a person that already owns a face: the import path
		// would otherwise silently replace a curated primary template with whatever
		// photo happens to come back. A brand-new person starts with its first face as
		// primary per the auto-provision contract.
		isPrimary := strconv.FormatBool(p.FaceCount == 0)

		record := []string{
			escapeCSVFormula(p.Name),
			escapeCSVFormula(personID),
			dedupeManifestImagePath(usedPaths, manifestImageStem(personID, p.Name)),
			isPrimary,
			strconv.Itoa(p.FaceCount),
		}
		if err := w.Write(record); err != nil {
			return nil, "", fmt.Errorf("write csv row: %w", err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, "", fmt.Errorf("flush csv writer: %w", err)
	}

	now := time.Now().UTC().Format("20060102_150405")
	statusTag := "all"
	switch strings.ToLower(strings.TrimSpace(faceStatus)) {
	case "missing":
		statusTag = "missing"
	case "enrolled":
		statusTag = "enrolled"
	}
	filename := fmt.Sprintf("person_photo_manifest_%s_%s.csv", statusTag, now)

	return buf.Bytes(), filename, nil
}

// manifestImageStem builds the `images/<stem>` path suggested for one person, minus the
// collision suffix.
func manifestImageStem(personID, name string) string {
	namePart := sanitizeManifestPathSegment(name)
	if namePart == "" {
		namePart = "person"
	}
	personIDPart := sanitizeManifestPathSegment(personID)
	if personIDPart == "" {
		return "images/" + namePart
	}
	return "images/" + personIDPart + "_" + namePart
}

// sanitizeManifestPathSegment makes one filename segment safe for both consumers:
//
//   - the separator heuristic in scanImagesFallback splits on the FIRST `.` or `_`, so a
//     name carrying one of those would be cut in half (and a name like `张_三` with no
//     personId would even be re-imported with `张` as its business key);
//   - `/` and `\` would escape the `images/` directory or break unzipping on Windows.
func sanitizeManifestPathSegment(segment string) string {
	replacer := strings.NewReplacer(
		"_", "-",
		".", "-",
		"/", "-",
		"\\", "-",
		"\x00", "",
	)
	cleaned := strings.TrimSpace(replacer.Replace(segment))
	// A leading formula trigger would turn the path cell into a spreadsheet formula.
	if cleaned != "" && strings.ContainsRune("=+-@\t\r", rune(cleaned[0])) {
		cleaned = "_" + cleaned
	}
	return cleaned
}

// dedupeManifestImagePath appends `.ext`→`_2.ext` style suffixes so every row owns a
// distinct path. `used` is carried across rows by the caller.
func dedupeManifestImagePath(used map[string]int, stem string) string {
	used[stem]++
	if used[stem] == 1 {
		return stem + ".jpg"
	}
	return fmt.Sprintf("%s_%d.jpg", stem, used[stem])
}

// escapeCSVFormula prefixes a leading formula trigger so Excel/WPS evaluates the cell as
// text. Deliberately narrow — real names and business keys never start with one of these,
// so the round-trip through the importer stays faithful.
func escapeCSVFormula(val string) string {
	if val != "" && strings.ContainsRune("=+-@\t\r", rune(val[0])) {
		return "'" + val
	}
	return val
}
