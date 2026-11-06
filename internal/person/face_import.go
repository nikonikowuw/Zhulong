package person

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"github.com/nikonikowuw/Zhulong/internal/idutil"
	"go.uber.org/zap"
	_ "golang.org/x/image/webp"
)

const (
	FaceImportJobType          = "person.face_import"
	maxZipArchiveBytes   int64 = 200 * 1024 * 1024  // 200 MiB
	maxDecompressedBytes int64 = 1024 * 1024 * 1024 // 1 GiB
	maxZipFiles          int   = 1000
	maxImageDimension    int   = 4096
	// qosYieldDuration is how long the import pipeline sleeps between images so the
	// NPU and the HTTP handlers keep their share of the box.
	qosYieldDuration = 5 * time.Millisecond
)

var (
	ErrZipSlip      = errors.New("zip slip path traversal detected")
	ErrZipBomb      = errors.New("decompression size limit exceeded")
	ErrTooManyFiles = errors.New("too many files in zip archive")
)

// FaceImportMetadata holds initial job input stored in Job.MetadataJSON.
type FaceImportMetadata struct {
	ZipPath  string `json:"zipPath"`
	JobDir   string `json:"jobDir"`
	Filename string `json:"filename"`
}

// FaceImportResult holds final job result summary stored in Job.ResultJSON.
type FaceImportResult struct {
	Total       int                    `json:"total"`
	Succeeded   int                    `json:"succeeded"`
	Failed      int                    `json:"failed"`
	FailedItems []FaceImportFailedItem `json:"failedItems"`
}

// faceImportEntry represents a normalized item to be enrolled.
type faceImportEntry struct {
	Line      int
	Name      string
	PersonID  string
	ImagePath string
	IsPrimary bool
}

// faceProcessError is the per-image verdict of a batch import.
//
// code is the machine contract: it is what the failed report and the UI switch on.
// message is diagnostic detail rendered verbatim (dynamic values such as the offending
// dimensions or the conflicting name do not fit a static catalog), so it is not part of
// the i18n message catalog the HTTP envelope uses.
type faceProcessError struct {
	code    string
	message string
}

// FaceImportJobHandler implements asyncjob.JobHandler for face bulk imports.
type FaceImportJobHandler struct {
	service *Service
}

// NewFaceImportJobHandler constructs the job handler.
func NewFaceImportJobHandler(s *Service) *FaceImportJobHandler {
	return &FaceImportJobHandler{service: s}
}

// Type returns "person.face_import".
func (h *FaceImportJobHandler) Type() string {
	return FaceImportJobType
}

// Execute performs the asynchronous face bulk import.
func (h *FaceImportJobHandler) Execute(ctx context.Context, job *asyncjob.Job, updater asyncjob.ProgressUpdater) error {
	return h.service.ExecuteFaceImportJob(ctx, job, updater)
}

// ExecuteFaceImportJob executes the extraction, vetting and enrollment pipeline.
//
// The stages are separate calls so each one can be read — and tested — on its own:
// unpack, parse, process every image, then publish and summarise.
func (s *Service) ExecuteFaceImportJob(ctx context.Context, job *asyncjob.Job, updater asyncjob.ProgressUpdater) error {
	var meta FaceImportMetadata
	if err := json.Unmarshal([]byte(job.MetadataJSON), &meta); err != nil {
		return fmt.Errorf("parse job metadata: %w", err)
	}

	defer func() {
		if err := os.RemoveAll(meta.JobDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("failed to clean up job directory", zap.String("jobDir", meta.JobDir), zap.Error(err))
		}
	}()

	s.logger.Info("executing face import job", zap.String("jobId", job.ID))

	entries, extractedDir, err := s.unpackFaceImport(ctx, job.ID, meta, updater)
	if err != nil {
		return err
	}

	result, galleryItems, err := s.processFaceImportEntries(ctx, job.ID, extractedDir, entries, updater)
	if err != nil {
		return err
	}

	if resultJSON, marshalErr := json.Marshal(result); marshalErr == nil {
		job.ResultJSON = string(resultJSON)
	} else {
		// The summary is diagnostic only; losing it must not turn a finished import
		// into a failed one, but it is logged because the failed report will be empty.
		s.logger.Warn("failed to encode face import result", zap.String("jobId", job.ID), zap.Error(marshalErr))
	}

	s.updateImportProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage:     "COMPLETED",
		Total:     result.Total,
		Processed: result.Total,
		Succeeded: result.Succeeded,
		Failed:    result.Failed,
	}, job.ID)

	for _, item := range galleryItems {
		s.broadcastUpsert(ctx, item)
	}
	if len(galleryItems) > 0 {
		s.logger.Info("broadcasted batch gallery sync", zap.String("jobId", job.ID), zap.Int("items", len(galleryItems)))
	}

	s.logger.Info("completed face import job",
		zap.String("jobId", job.ID),
		zap.Int("total", result.Total),
		zap.Int("succeeded", result.Succeeded),
		zap.Int("failed", result.Failed),
	)
	return nil
}

// unpackFaceImport extracts the archive into the job directory and returns the manifest
// entries together with the directory they are relative to.
func (s *Service) unpackFaceImport(
	ctx context.Context,
	jobID string,
	meta FaceImportMetadata,
	updater asyncjob.ProgressUpdater,
) ([]faceImportEntry, string, error) {
	s.updateImportProgress(ctx, updater, asyncjob.ProgressUpdate{Stage: "EXTRACTING"}, jobID)

	extractedDir := filepath.Join(meta.JobDir, "extracted")
	if err := os.MkdirAll(extractedDir, 0755); err != nil {
		return nil, "", fmt.Errorf("create extraction dir: %w", err)
	}

	if err := safeExtractZip(meta.ZipPath, extractedDir); err != nil {
		switch {
		case errors.Is(err, ErrZipSlip):
			return nil, "", s.abortFaceImport("ZIP_SLIP_ATTEMPT",
				"The archive contains a path that escapes the extraction directory", err)
		case errors.Is(err, ErrZipBomb):
			return nil, "", s.abortFaceImport("ZIP_BOMB_DETECTED",
				"Decompressed contents exceed the size limit", err)
		case errors.Is(err, ErrTooManyFiles):
			return nil, "", s.abortFaceImport("ZIP_TOO_MANY_FILES",
				"The archive contains too many files", err)
		default:
			return nil, "", s.abortFaceImport("ZIP_READ_FAILED",
				"Failed to read or unpack the archive", err)
		}
	}

	entries, err := parseFaceImportManifest(extractedDir)
	if err != nil {
		return nil, "", err
	}
	if len(entries) == 0 {
		return nil, "", apperr.Invalid("FACE_IMPORT_ARCHIVE_EMPTY",
			"No usable image or manifest was found in the archive", nil)
	}
	return entries, extractedDir, nil
}

// processFaceImportEntries runs every image through vetting and enrollment, yielding the
// CPU/NPU between images, and reports the per-image verdicts.
func (s *Service) processFaceImportEntries(
	ctx context.Context,
	jobID string,
	extractedDir string,
	entries []faceImportEntry,
	updater asyncjob.ProgressUpdater,
) (FaceImportResult, []GalleryItem, error) {
	total := len(entries)
	s.updateImportProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage: "PROCESSING",
		Total: total,
	}, jobID)

	result := FaceImportResult{
		Total:       total,
		FailedItems: []FaceImportFailedItem{},
	}
	galleryItems := make([]GalleryItem, 0, total)

	for index, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, nil, err
		}

		s.updateImportProgress(ctx, updater, asyncjob.ProgressUpdate{
			Stage:       "PROCESSING",
			CurrentFile: entry.ImagePath,
			Total:       total,
			Processed:   index + 1,
			Succeeded:   result.Succeeded,
			Failed:      result.Failed,
		}, jobID)

		item, procErr := s.processOneFace(ctx, extractedDir, entry)
		if procErr != nil {
			result.Failed++
			result.FailedItems = append(result.FailedItems, FaceImportFailedItem{
				Line:      entry.Line,
				FilePath:  entry.ImagePath,
				Name:      entry.Name,
				PersonID:  entry.PersonID,
				ErrorCode: procErr.code,
				Message:   procErr.message,
			})
		} else {
			result.Succeeded++
			galleryItems = append(galleryItems, item)
		}

		if err := yieldFor(ctx, qosYieldDuration); err != nil {
			return result, nil, err
		}
	}
	return result, galleryItems, nil
}

// yieldFor gives the rest of the process a slice of the box between import images. The
// import is the lowest-priority workload on an edge device, so it pays for its own
// throughput instead of starving the inference and HTTP paths.
func yieldFor(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// abortFaceImport logs why a batch import was abandoned and returns the error the job
// record will show.
//
// The cause is logged but not attached to the returned error: asyncjob stores
// err.Error() verbatim in the job record, and that string is rendered to the operator —
// the stable code and the sanitized message are the whole public story.
func (s *Service) abortFaceImport(code, message string, cause error) error {
	s.logger.Warn("face import job aborted", zap.String("code", code), zap.Error(cause))
	return apperr.Invalid(code, message, nil)
}

func (s *Service) updateImportProgress(
	ctx context.Context,
	updater asyncjob.ProgressUpdater,
	update asyncjob.ProgressUpdate,
	jobID string,
) {
	if err := updater.Update(ctx, update); err != nil {
		s.logger.Warn("failed to update face import progress",
			zap.String("jobId", jobID),
			zap.String("stage", update.Stage),
			zap.Error(err),
		)
	}
}

// processOneFace handles validation, decoding, AI gate, feature extraction and storage
// for one image, returning the gallery entry the caller has to publish.
func (s *Service) processOneFace(
	ctx context.Context,
	baseDir string,
	entry faceImportEntry,
) (GalleryItem, *faceProcessError) {
	name, nameErr := normalizePersonName(entry.Name)
	if nameErr != nil {
		// The catalogue entry is the message: this path validates with the same rules
		// as the REST handler, so the report names the same codes it would return.
		return GalleryItem{}, &faceProcessError{
			code:    apperr.CodeOf(nameErr),
			message: "姓名不符合要求（必填，且不超过 64 个字符）",
		}
	}

	personIDKey := strings.TrimSpace(entry.PersonID)
	if utf8.RuneCountInString(personIDKey) > personExternalIDMaxLen {
		return GalleryItem{}, &faceProcessError{code: "PERSON_ID_TOO_LONG", message: "人员编号超出 64 字符限制"}
	}

	imgBytes, loadErr := loadAndValidateFaceImage(baseDir, entry.ImagePath)
	if loadErr != nil {
		return GalleryItem{}, loadErr
	}

	featBytes, extErr := s.extractFaceFeatures(ctx, imgBytes)
	if extErr != nil {
		return GalleryItem{}, extErr
	}

	targetPerson, personErr := s.resolveOrCreateTargetPerson(ctx, name, personIDKey)
	if personErr != nil {
		return GalleryItem{}, personErr
	}

	photo := importedFace{
		Image:    imgBytes,
		Features: featBytes,
		Ext:      strings.ToLower(filepath.Ext(entry.ImagePath)),
		Primary:  entry.IsPrimary,
	}
	face, enrollErr := s.enrollFaceRecord(ctx, targetPerson, photo)
	if enrollErr != nil {
		return GalleryItem{}, enrollErr
	}

	return newGalleryItem(*face, personIDKey, targetPerson.Name), nil
}

func loadAndValidateFaceImage(baseDir, imgRelPath string) ([]byte, *faceProcessError) {
	relClean := filepath.Clean(imgRelPath)
	if strings.HasPrefix(relClean, "..") || filepath.IsAbs(relClean) {
		return nil, &faceProcessError{code: "INVALID_IMAGE_PATH", message: "图片路径非法"}
	}

	root, err := os.OpenRoot(baseDir)
	if err != nil {
		return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "未找到导入解压目录"}
	}
	defer root.Close()

	imgFile, err := root.Open(relClean)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "未找到图片文件"}
		}
		return nil, &faceProcessError{code: "INVALID_IMAGE_PATH", message: "图片路径非法"}
	}
	defer imgFile.Close()

	info, err := imgFile.Stat()
	if err != nil || info.IsDir() {
		return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "未找到图片文件"}
	}

	cfg, _, err := image.DecodeConfig(imgFile)
	if err != nil {
		return nil, &faceProcessError{code: "CORRUPT_OR_OVERSIZED_IMAGE", message: "图片损坏或格式不支持"}
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		return nil, &faceProcessError{
			code: "CORRUPT_OR_OVERSIZED_IMAGE",
			message: fmt.Sprintf(
				"图片尺寸 (%dx%d) 超出 %d 像素限制",
				cfg.Width,
				cfg.Height,
				maxImageDimension,
			),
		}
	}

	if _, err := imgFile.Seek(0, io.SeekStart); err != nil {
		return nil, &faceProcessError{code: "FILE_READ_FAILED", message: "读取图片文件失败"}
	}

	imgBytes, err := io.ReadAll(io.LimitReader(imgFile, MaxFaceImageBytes+1))
	if err != nil {
		return nil, &faceProcessError{code: "FILE_READ_FAILED", message: "读取图片文件失败"}
	}
	if len(imgBytes) > MaxFaceImageBytes {
		return nil, &faceProcessError{code: "FILE_TOO_LARGE", message: "图片文件超出 10MB 大小限制"}
	}
	return imgBytes, nil
}

// faceLimitMessage is the per-row detail shown when a person is already at the template
// limit. The pre-check and the insert-failure path both use it so the report cannot show
// two wordings for one condition.
func faceLimitMessage(personName string) string {
	return fmt.Sprintf("人员 %q 底库照片已达 %d 张上限", personName, MaxFacesPerPerson)
}

// faceImportEnrollError renders the per-row verdict for a failed face insert.
func faceImportEnrollError(err error, personName string) *faceProcessError {
	if errors.Is(err, ErrFaceLimitExceeded) {
		return &faceProcessError{code: "FACE_LIMIT_EXCEEDED", message: faceLimitMessage(personName)}
	}
	return &faceProcessError{code: "FACE_ADD_FAILED", message: "保存人脸底库数据失败"}
}

// isMultipleFacesError reports whether an extractor rejected a group photo.
//
// This is the **legacy** path: the provider protocol now carries a real code
// (`MULTIPLE_FACES` → faceextract.ErrAmbiguous → person.ErrMultipleFaces), and the
// primary check is `errors.Is(err, ErrMultipleFaces)`. The textual fallback is kept
// only for extractors outside this repo that phrase the verdict themselves; do not add
// new string matching here.
func isMultipleFacesError(err error) bool {
	detail := strings.ToLower(err.Error())
	return strings.Contains(detail, "multiple faces") || strings.Contains(detail, "multiple_faces")
}

func (s *Service) extractFaceFeatures(ctx context.Context, imgBytes []byte) ([]byte, *faceProcessError) {
	if s.extractor == nil {
		return nil, &faceProcessError{code: "FEATURE_EXTRACTOR_UNAVAILABLE", message: "人脸算法引擎未就绪"}
	}
	features, err := s.extractor.ExtractFaceFeature(ctx, imgBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return nil, &faceProcessError{code: "NO_FACE_DETECTED", message: "上传的照片中未检测到清晰人脸"}
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return nil, &faceProcessError{code: "CORRUPT_OR_OVERSIZED_IMAGE", message: "图片格式不支持或无法解码"}
		}
		if errors.Is(err, ErrMultipleFaces) || isMultipleFacesError(err) {
			return nil, &faceProcessError{
				code:    "MULTIPLE_FACES_DETECTED",
				message: "检测到多张人脸（合影照拒绝录入）",
			}
		}
		return nil, &faceProcessError{code: "FEATURE_EXTRACT_FAILED", message: "人脸特征提取失败: " + err.Error()}
	}
	if len(features) != faceFeatureDim {
		return nil, &faceProcessError{code: "FEATURE_DIM_MISMATCH", message: "特征维度与系统模型不匹配"}
	}
	return float32ToBytes(features), nil
}

func (s *Service) resolveOrCreateTargetPerson(
	ctx context.Context,
	name, personIDKey string,
) (*Person, *faceProcessError) {
	if personIDKey != "" {
		// Metadata only: enrollment needs the row's id, name and primary pointer, not
		// the face list that the full reader would preload.
		existing, err := s.store.GetPersonMetaByExternalID(ctx, personIDKey)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, &faceProcessError{code: "DATABASE_ERROR", message: "查询已有人员失败"}
		}
		if existing != nil {
			if existing.Name != name {
				return nil, &faceProcessError{
					code: "PERSON_NAME_MISMATCH",
					message: fmt.Sprintf(
						"该工号已绑定人员 %q，与导入姓名 %q 不一致",
						existing.Name,
						name,
					),
				}
			}
			return existing, nil
		}
	}

	newPersonID := idutil.New()
	var pKey *string
	if personIDKey != "" {
		pKey = &personIDKey
	}
	newPerson := &Person{
		ID:         newPersonID,
		Name:       name,
		PersonID:   pKey,
		Source:     PersonSourceLocal,
		SourceName: "",
		CreatedAt:  time.Now().UTC(),
		UpdatedAt:  time.Now().UTC(),
	}
	if err := s.store.CreatePerson(ctx, newPerson); err != nil {
		if errors.Is(err, ErrDuplicatePersonID) {
			return nil, &faceProcessError{code: "PERSON_ID_ALREADY_EXISTS", message: "人员编号已被占用"}
		}
		return nil, &faceProcessError{code: "PERSON_CREATE_FAILED", message: "创建人员档案失败"}
	}
	return newPerson, nil
}

// importedFace is one archive image that passed extraction, ready to be persisted.
type importedFace struct {
	Image    []byte
	Features []byte
	Ext      string
	Primary  bool
}

func (s *Service) enrollFaceRecord(
	ctx context.Context,
	targetPerson *Person,
	photo importedFace,
) (*Face, *faceProcessError) {
	faces, err := s.store.ListFacesByPerson(ctx, targetPerson.ID)
	if err != nil {
		return nil, &faceProcessError{code: "DATABASE_ERROR", message: "查询人脸列表失败"}
	}
	if len(faces) >= MaxFacesPerPerson {
		return nil, &faceProcessError{code: "FACE_LIMIT_EXCEEDED", message: faceLimitMessage(targetPerson.Name)}
	}

	if err := os.MkdirAll(s.facesDir, 0755); err != nil {
		return nil, &faceProcessError{code: "STORAGE_ERROR", message: "创建人脸存储目录失败"}
	}
	ext := photo.Ext
	if ext == "" {
		ext = ".jpg"
	}
	faceID := idutil.New()
	fileName, err := faceFileName(targetPerson.ID, faceID, ext)
	if err != nil {
		return nil, &faceProcessError{code: "STORAGE_ERROR", message: "生成人脸文件名失败"}
	}
	savePath := filepath.Join(s.facesDir, fileName)
	if err := os.WriteFile(savePath, photo.Image, 0644); err != nil {
		return nil, &faceProcessError{code: "STORAGE_ERROR", message: "写入人脸图片文件失败"}
	}

	face := &Face{
		ID:            faceID,
		PersonRowID:   targetPerson.ID,
		ImagePath:     fileName,
		FeatureVector: photo.Features,
		CreatedAt:     time.Now().UTC(),
	}

	if _, err := s.addFaceRowWithLimit(ctx, face, savePath); err != nil {
		return nil, faceImportEnrollError(err, targetPerson.Name)
	}

	shouldSetPrimary := photo.Primary || targetPerson.PrimaryFaceID == nil || *targetPerson.PrimaryFaceID == ""
	if shouldSetPrimary {
		if err := s.store.SetPrimaryFace(ctx, targetPerson.ID, face.ID); err != nil {
			s.logger.Warn("failed to set primary face during face import",
				zap.String("person_id", targetPerson.ID),
				zap.String("face_id", face.ID),
				zap.Error(err))
		} else {
			targetPerson.PrimaryFaceID = &face.ID
		}
	}
	return face, nil
}

// isArchiveMetadata reports the entries an archiver adds for its own bookkeeping. They
// are dropped before extraction so they cannot be mistaken for enrollment material.
func isArchiveMetadata(name string) bool {
	inMacOSXDir := strings.HasPrefix(name, "__MACOSX/")
	isFinderJunk := strings.HasSuffix(name, ".DS_Store") || strings.HasSuffix(name, "Thumbs.db")
	isDesktopIni := strings.HasSuffix(name, "desktop.ini")
	return inMacOSXDir || isFinderJunk || isDesktopIni
}

// safeExtractZip unpacks a zip archive into destDir with Zip Slip and Zip Bomb defenses.
func safeExtractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	if len(r.File) > maxZipFiles {
		return ErrTooManyFiles
	}

	cleanDest := filepath.Clean(destDir) + string(os.PathSeparator)
	var totalExtracted int64

	for _, f := range r.File {
		if isArchiveMetadata(filepath.ToSlash(f.Name)) {
			continue
		}

		targetPath := filepath.Clean(filepath.Join(destDir, f.Name))

		if !strings.HasPrefix(targetPath, cleanDest) && targetPath != filepath.Clean(destDir) {
			return ErrZipSlip
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return fmt.Errorf("mkdir: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return fmt.Errorf("mkdir parent: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open zip file %s: %w", f.Name, err)
		}

		out, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, f.Mode()|0600)
		if err != nil {
			rc.Close()
			return fmt.Errorf("create target %s: %w", targetPath, err)
		}

		remainingAllowed := maxDecompressedBytes - totalExtracted
		if remainingAllowed <= 0 {
			out.Close()
			rc.Close()
			return ErrZipBomb
		}

		lr := io.LimitReader(rc, remainingAllowed+1)
		written, copyErr := io.Copy(out, lr)
		out.Close()
		rc.Close()

		if copyErr != nil {
			return fmt.Errorf("extract %s: %w", f.Name, copyErr)
		}
		if written > remainingAllowed {
			return ErrZipBomb
		}
		totalExtracted += written
	}

	return nil
}

// parseFaceImportManifest parses manifest.csv or falls back to scanning filenames.
func parseFaceImportManifest(destDir string) ([]faceImportEntry, error) {
	manifestPath := filepath.Join(destDir, "manifest.csv")
	if _, err := os.Stat(manifestPath); err == nil {
		return parseManifestCSV(manifestPath)
	}
	return scanImagesFallback(destDir)
}

// manifestColumns is the resolved column layout of a manifest header. -1 means absent:
// only name and imagePath are mandatory, because a person can be created from a row that
// carries no business key.
type manifestColumns struct {
	name     int
	personID int
	image    int
	primary  int
}

// parseManifestColumns maps a header row onto manifestColumns. The accepted spellings are
// the English ones the exporter writes plus the Chinese ones a human operator types.
func parseManifestColumns(headers []string) (manifestColumns, error) {
	columns := manifestColumns{name: -1, personID: -1, image: -1, primary: -1}
	for index, header := range headers {
		switch strings.ToLower(strings.TrimSpace(header)) {
		case "name", "姓名":
			columns.name = index
		case "personid", "person_id", "工号", "人员编号":
			columns.personID = index
		case "imagepath", "image_path", "image", "file", "photo", "图片路径":
			columns.image = index
		case "isprimary", "is_primary", "primary", "主人脸":
			columns.primary = index
		}
	}
	if columns.name < 0 || columns.image < 0 {
		return manifestColumns{}, apperr.Invalid("FACE_IMPORT_MANIFEST_INVALID",
			"manifest.csv must contain the name and imagePath columns", nil)
	}
	return columns, nil
}

// manifestField reads one column out of a record, tolerating a short row instead of
// indexing past its end.
func manifestField(record []string, index int) string {
	if index < 0 || index >= len(record) {
		return ""
	}
	return strings.TrimSpace(record[index])
}

func parseManifestCSV(csvPath string) ([]faceImportEntry, error) {
	data, err := os.ReadFile(csvPath)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))

	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true

	headers, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("read manifest header: %w", err)
	}
	columns, err := parseManifestColumns(headers)
	if err != nil {
		return nil, err
	}

	entries := []faceImportEntry{}
	for {
		record, readErr := r.Read()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			continue
		}

		name := manifestField(record, columns.name)
		imagePath := manifestField(record, columns.image)
		if name == "" || imagePath == "" {
			continue
		}

		line := len(entries) + 2
		if pos, _ := r.FieldPos(0); pos > 0 {
			line = pos
		}

		entries = append(entries, faceImportEntry{
			Line:      line,
			Name:      name,
			PersonID:  manifestField(record, columns.personID),
			ImagePath: imagePath,
			IsPrimary: manifestPrimary(manifestField(record, columns.primary)),
		})
	}

	return entries, nil
}

// manifestPrimary reads the optional primary-face flag. The accepted spellings cover the
// exports this importer is meant to consume rather than an invented schema.
func manifestPrimary(raw string) bool {
	switch strings.ToLower(raw) {
	case "true", "1", "yes", "是":
		return true
	default:
		return false
	}
}

var fileNameRegex = regexp.MustCompile(`(?i)^([^._]+)[._]([^.]+)\.(jpg|jpeg|png|webp)$`)

func scanImagesFallback(destDir string) ([]faceImportEntry, error) {
	entries := []faceImportEntry{}
	line := 1

	err := filepath.Walk(destDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
			return nil
		}

		relPath, err := filepath.Rel(destDir, path)
		if err != nil {
			return nil
		}

		baseName := info.Name()
		matches := fileNameRegex.FindStringSubmatch(baseName)

		var name string
		var personID string

		if len(matches) == 4 {
			p1 := matches[1]
			p2 := matches[2]
			isAlphanumericP1 := isAlphaNumOrDash(p1)
			isAlphanumericP2 := isAlphaNumOrDash(p2)

			if !isAlphanumericP1 && isAlphanumericP2 {
				name = p1
				personID = p2
			} else {
				personID = p1
				name = p2
			}
		} else {
			name = strings.TrimSuffix(baseName, ext)
		}

		entries = append(entries, faceImportEntry{
			Line:      line,
			Name:      name,
			PersonID:  personID,
			ImagePath: relPath,
			IsPrimary: false,
		})
		line++
		return nil
	})

	return entries, err
}

func isAlphaNumOrDash(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		isDigit := r >= '0' && r <= '9'
		isLower := r >= 'a' && r <= 'z'
		isUpper := r >= 'A' && r <= 'Z'
		if !isDigit && !isLower && !isUpper && r != '-' {
			return false
		}
	}
	return true
}

// StartFaceImportJob initializes an asynchronous face bulk import job and delegates to asyncjob service.
func (s *Service) StartFaceImportJob(ctx context.Context, fileHeader *multipart.FileHeader) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.Internal("ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	if fileHeader == nil {
		return nil, apperr.Invalid("FILE_REQUIRED", "ZIP file is required", nil)
	}
	if fileHeader.Size > maxZipArchiveBytes {
		return nil, apperr.Invalid("FILE_TOO_LARGE", "ZIP file exceeds 200MB limit", nil)
	}

	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if ext != ".zip" {
		return nil, apperr.Invalid("INVALID_FILE_TYPE", "Only ZIP archive files are allowed", nil)
	}

	src, err := fileHeader.Open()
	if err != nil {
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to open uploaded ZIP file", err)
	}
	defer src.Close()

	jobID := idutil.New()
	jobDir := filepath.Join(s.tempDir, "face_import_"+jobID)
	if err := os.MkdirAll(jobDir, 0755); err != nil {
		return nil, apperr.Internal("STORAGE_ERROR", "Failed to create temp job directory", err)
	}
	cleanupTemp := func() {
		if rmErr := os.RemoveAll(jobDir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.logger.Warn("failed to clean up temp job directory", zap.String("jobDir", jobDir), zap.Error(rmErr))
		}
	}

	zipSavePath := filepath.Join(jobDir, "upload.zip")
	if err := saveUploadedZip(zipSavePath, src); err != nil {
		cleanupTemp()
		return nil, err
	}

	meta := FaceImportMetadata{
		ZipPath:  zipSavePath,
		JobDir:   jobDir,
		Filename: fileHeader.Filename,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		cleanupTemp()
		return nil, apperr.Internal("STORAGE_ERROR", "Failed to encode face import job metadata", err)
	}

	job, err := s.asyncJobs.SubmitJob(ctx, FaceImportJobType, string(metaJSON))
	if err != nil {
		cleanupTemp()
		return nil, err
	}

	return job, nil
}

// saveUploadedZip streams the uploaded archive to disk.
//
// Two things are deliberate: the copy is bounded instead of trusting the size the client
// advertised, and the Close is checked, because a buffered write that fails at flush would
// otherwise hand a truncated archive to a job that fails later for the wrong reason.
func saveUploadedZip(path string, src io.Reader) error {
	dst, err := os.Create(path)
	if err != nil {
		return apperr.Internal("STORAGE_ERROR", "Failed to save uploaded ZIP file", err)
	}

	written, copyErr := io.Copy(dst, io.LimitReader(src, maxZipArchiveBytes+1))
	closeErr := dst.Close()
	if copyErr != nil {
		return apperr.Invalid("FILE_READ_FAILED", "Failed to copy uploaded ZIP content", copyErr)
	}
	if closeErr != nil {
		return apperr.Internal("STORAGE_ERROR", "Failed to save uploaded ZIP file", closeErr)
	}
	if written > maxZipArchiveBytes {
		return apperr.Invalid("FILE_TOO_LARGE", "ZIP file exceeds the 200MB limit", nil)
	}
	return nil
}

// GetFaceImportJob retrieves the live state of an asynchronous face import job.
func (s *Service) GetFaceImportJob(ctx context.Context, jobID string) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.Internal("ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	return s.asyncJobs.GetJob(ctx, jobID)
}

// GetFaceImportFailedReportCSV generates a CSV diagnostic report for failed rows.
// When an Accept-Language header (or locale) is provided, static diagnostic messages
// are localized to match the caller's language.
func (s *Service) GetFaceImportFailedReportCSV(
	ctx context.Context,
	jobID string,
	acceptLanguages ...string,
) ([]byte, error) {
	job, err := s.GetFaceImportJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if _, err := buf.Write([]byte("\xef\xbb\xbf")); err != nil {
		return nil, fmt.Errorf("write csv bom: %w", err)
	}
	if err := w.Write([]string{"line", "filePath", "name", "personId", "errorCode", "message"}); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}

	if job.ResultJSON != "" && job.ResultJSON != "{}" {
		var res FaceImportResult
		if err := json.Unmarshal([]byte(job.ResultJSON), &res); err == nil {
			locale := "zh-Hans"
			if len(acceptLanguages) > 0 && acceptLanguages[0] != "" {
				locale = httputil.ResolveLocale(acceptLanguages[0])
			}
			for _, item := range res.FailedItems {
				msg := item.Message
				if localized, ok := httputil.LookupMessage(item.ErrorCode, locale); ok && localized != "" {
					if !strings.Contains(item.Message, "(") &&
						!strings.Contains(item.Message, ": ") &&
						!strings.Contains(item.Message, "\"") &&
						!strings.Contains(item.Message, "“") {
						msg = localized
					}
				}
				if err := w.Write([]string{
					strconv.Itoa(item.Line),
					item.FilePath,
					item.Name,
					item.PersonID,
					item.ErrorCode,
					msg,
				}); err != nil {
					s.logger.Warn("failed to write failed-report row",
						zap.String("jobId", jobID),
						zap.Error(err),
					)
					break
				}
			}
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush csv writer: %w", err)
	}
	return buf.Bytes(), nil
}
