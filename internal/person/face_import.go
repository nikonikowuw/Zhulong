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

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
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
	maxFacesPerPerson    int   = 10
	qosYieldDuration           = 5 * time.Millisecond
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

// FaceImportEntry represents a normalized item to be enrolled.
type FaceImportEntry struct {
	Line      int
	Name      string
	PersonID  string
	ImagePath string
	IsPrimary bool
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
func (s *Service) ExecuteFaceImportJob(ctx context.Context, job *asyncjob.Job, updater asyncjob.ProgressUpdater) error {
	var meta FaceImportMetadata
	if err := json.Unmarshal([]byte(job.MetadataJSON), &meta); err != nil {
		return fmt.Errorf("parse job metadata: %w", err)
	}

	jobDir := meta.JobDir
	defer func() {
		if err := os.RemoveAll(jobDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.logger.Warn("failed to clean up job directory", zap.String("jobDir", jobDir), zap.Error(err))
		}
	}()

	s.logger.Info("executing face import job", zap.String("jobId", job.ID))

	// 1. Stage: EXTRACTING
	_ = updater.Update(ctx, asyncjob.ProgressUpdate{Stage: "EXTRACTING"})

	zipPath := meta.ZipPath
	extractedDir := filepath.Join(jobDir, "extracted")
	if err := os.MkdirAll(extractedDir, 0755); err != nil {
		return fmt.Errorf("create extraction dir: %w", err)
	}

	// 2. Safe extraction
	if err := safeExtractZip(zipPath, extractedDir); err != nil {
		switch {
		case errors.Is(err, ErrZipSlip):
			return errors.New("ZIP_SLIP_ATTEMPT: 非法的压缩包路径逃逸文件")
		case errors.Is(err, ErrZipBomb):
			return errors.New("ZIP_BOMB_DETECTED: 解压内容超出安全容量上限")
		case errors.Is(err, ErrTooManyFiles):
			return errors.New("ZIP_TOO_MANY_FILES: 压缩包内文件数量超出上限")
		default:
			return fmt.Errorf("ZIP_READ_FAILED: %w", err)
		}
	}

	// 3. Manifest parsing
	entries, parseErr := parseFaceImportManifest(extractedDir)
	if parseErr != nil {
		return parseErr
	}
	if len(entries) == 0 {
		return errors.New("未在压缩包中找到有效图片或清单")
	}

	total := len(entries)
	_ = updater.Update(ctx, asyncjob.ProgressUpdate{
		Stage: "PROCESSING",
		Total: total,
	})

	var succeeded int
	var failed int
	failedItems := []FaceImportFailedItem{}
	newGalleryItems := []GalleryItem{}

	// 4. Sequential AI vetting with QoS yield
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}

		processed := i + 1
		_ = updater.Update(ctx, asyncjob.ProgressUpdate{
			Stage:       "PROCESSING",
			CurrentFile: entry.ImagePath,
			Total:       total,
			Processed:   processed,
			Succeeded:   succeeded,
			Failed:      failed,
		})

		itemErrCode, itemErrMsg := s.processOneFace(ctx, extractedDir, entry, &newGalleryItems)
		if itemErrCode != "" {
			failed++
			failedItems = append(failedItems, FaceImportFailedItem{
				Line:      entry.Line,
				FilePath:  entry.ImagePath,
				Name:      entry.Name,
				PersonID:  entry.PersonID,
				ErrorCode: itemErrCode,
				Message:   itemErrMsg,
			})
		} else {
			succeeded++
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(qosYieldDuration):
		}
	}

	// 5. Save result summary into Job.ResultJSON
	res := FaceImportResult{
		Total:       total,
		Succeeded:   succeeded,
		Failed:      failed,
		FailedItems: failedItems,
	}
	resBytes, _ := json.Marshal(res)
	job.ResultJSON = string(resBytes)

	_ = updater.Update(ctx, asyncjob.ProgressUpdate{
		Stage:     "COMPLETED",
		Total:     total,
		Processed: total,
		Succeeded: succeeded,
		Failed:    failed,
	})

	// 6. Batched gallery synchronization
	if len(newGalleryItems) > 0 {
		for _, item := range newGalleryItems {
			s.broadcastUpsert(ctx, item)
		}
		s.logger.Info("broadcasted batch gallery sync", zap.String("jobId", job.ID), zap.Int("items", len(newGalleryItems)))
	}

	s.logger.Info("completed face import job", zap.String("jobId", job.ID), zap.Int("total", total), zap.Int("succeeded", succeeded), zap.Int("failed", failed))
	return nil
}

// processOneFace handles validation, decoding, AI gate, feature extraction and storage for one image.
func (s *Service) processOneFace(ctx context.Context, baseDir string, entry FaceImportEntry, galleryItems *[]GalleryItem) (string, string) {
	name := strings.TrimSpace(entry.Name)
	if name == "" {
		return "NAME_REQUIRED", "姓名不能为空"
	}
	if len(name) > 64 {
		return "NAME_TOO_LONG", "姓名长度超出 64 字符限制"
	}

	personIDKey := strings.TrimSpace(entry.PersonID)
	if len(personIDKey) > 64 {
		return "PERSON_ID_TOO_LONG", "人员编号超出 64 字符限制"
	}

	relClean := filepath.Clean(entry.ImagePath)
	if strings.HasPrefix(relClean, "..") || filepath.IsAbs(relClean) {
		return "INVALID_IMAGE_PATH", "图片路径非法"
	}

	absPath := filepath.Join(baseDir, relClean)
	imgFile, err := os.Open(absPath)
	if err != nil {
		return "IMAGE_NOT_FOUND", "未找到图片文件"
	}
	defer imgFile.Close()

	cfg, _, err := image.DecodeConfig(imgFile)
	if err != nil {
		return "CORRUPT_OR_OVERSIZED_IMAGE", "图片损坏或格式不支持"
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxImageDimension || cfg.Height > maxImageDimension {
		return "CORRUPT_OR_OVERSIZED_IMAGE", fmt.Sprintf("图片尺寸 (%dx%d) 超出 %d 像素限制", cfg.Width, cfg.Height, maxImageDimension)
	}

	if _, err := imgFile.Seek(0, io.SeekStart); err != nil {
		return "FILE_READ_FAILED", "读取图片文件失败"
	}

	imgBytes, err := io.ReadAll(io.LimitReader(imgFile, maxFaceImageBytes+1))
	if err != nil {
		return "FILE_READ_FAILED", "读取图片文件失败"
	}
	if len(imgBytes) > maxFaceImageBytes {
		return "FILE_TOO_LARGE", "图片文件超出 10MB 大小限制"
	}

	if s.extractor == nil {
		return "FEATURE_EXTRACTOR_UNAVAILABLE", "人脸算法引擎未就绪"
	}
	features, err := s.extractor.ExtractFaceFeature(ctx, imgBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return "NO_FACE_DETECTED", "上传的照片中未检测到清晰人脸"
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return "CORRUPT_OR_OVERSIZED_IMAGE", "图片格式不支持或无法解码"
		}
		errStr := err.Error()
		if strings.Contains(errStr, "multiple faces") || strings.Contains(errStr, "MULTIPLE_FACES_DETECTED") {
			return "MULTIPLE_FACES_DETECTED", "检测到多张人脸（合影照拒绝录入）"
		}
		return "FEATURE_EXTRACT_FAILED", "人脸特征提取失败: " + errStr
	}
	if len(features) != faceFeatureDim {
		return "FEATURE_DIM_MISMATCH", "特征维度与系统模型不匹配"
	}
	featBytes := float32ToBytes(features)

	var targetPerson *Person
	if personIDKey != "" {
		existing, err := s.store.GetPersonByExternalID(ctx, personIDKey)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "DATABASE_ERROR", "查询已有人员失败"
		}
		if existing != nil {
			if existing.Name != name {
				return "PERSON_NAME_MISMATCH", fmt.Sprintf("该工号已绑定人员 %q，与导入姓名 %q 不一致", existing.Name, name)
			}
			targetPerson = existing
		}
	}

	if targetPerson == nil {
		newPersonID := idutil.New()
		var pKey *string
		if personIDKey != "" {
			pKey = &personIDKey
		}
		newPerson := &Person{
			ID:        newPersonID,
			Name:      name,
			PersonID:  pKey,
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}
		if err := s.store.CreatePerson(ctx, newPerson); err != nil {
			if errors.Is(err, ErrDuplicatePersonID) {
				return "PERSON_ID_ALREADY_EXISTS", "人员编号已被占用"
			}
			return "PERSON_CREATE_FAILED", "创建人员档案失败"
		}
		targetPerson = newPerson
	}

	faces, err := s.store.ListFacesByPerson(ctx, targetPerson.ID)
	if err != nil {
		return "DATABASE_ERROR", "查询人脸列表失败"
	}
	if len(faces) >= maxFacesPerPerson {
		return "FACE_LIMIT_EXCEEDED", fmt.Sprintf("人员 %q 底库照片已达 %d 张上限", targetPerson.Name, maxFacesPerPerson)
	}

	if err := os.MkdirAll(s.facesDir, 0755); err != nil {
		return "STORAGE_ERROR", "创建人脸存储目录失败"
	}
	ext := strings.ToLower(filepath.Ext(entry.ImagePath))
	if ext == "" {
		ext = ".jpg"
	}
	faceID := idutil.New()
	fileName, err := faceFileName(targetPerson.ID, faceID, ext)
	if err != nil {
		return "STORAGE_ERROR", "生成人脸文件名失败"
	}
	savePath := filepath.Join(s.facesDir, fileName)
	if err := os.WriteFile(savePath, imgBytes, 0644); err != nil {
		return "STORAGE_ERROR", "写入人脸图片文件失败"
	}

	face := &Face{
		ID:            faceID,
		PersonRowID:   targetPerson.ID,
		ImagePath:     fileName,
		FeatureVector: featBytes,
		CreatedAt:     time.Now().UTC(),
	}

	if err := s.store.AddFace(ctx, face); err != nil {
		if rmErr := os.Remove(savePath); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.logger.Warn("failed to clean up face image after enrollment failure",
				zap.String("path", savePath), zap.Error(rmErr))
		}
		return "FACE_ADD_FAILED", "保存人脸底库数据失败"
	}

	shouldSetPrimary := entry.IsPrimary || targetPerson.PrimaryFaceID == nil || *targetPerson.PrimaryFaceID == ""
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

	*galleryItems = append(*galleryItems, newGalleryItem(*face, personIDKey, targetPerson.Name))
	return "", ""
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
		normName := filepath.ToSlash(f.Name)
		if strings.HasPrefix(normName, "__MACOSX/") ||
			strings.HasSuffix(normName, ".DS_Store") ||
			strings.HasSuffix(normName, "Thumbs.db") ||
			strings.HasSuffix(normName, "desktop.ini") {
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
func parseFaceImportManifest(destDir string) ([]FaceImportEntry, error) {
	manifestPath := filepath.Join(destDir, "manifest.csv")
	if _, err := os.Stat(manifestPath); err == nil {
		return parseManifestCSV(manifestPath)
	}
	return scanImagesFallback(destDir)
}

func parseManifestCSV(csvPath string) ([]FaceImportEntry, error) {
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

	nameIdx := -1
	personIDIdx := -1
	imageIdx := -1
	primaryIdx := -1

	for idx, h := range headers {
		key := strings.ToLower(strings.TrimSpace(h))
		switch key {
		case "name", "姓名":
			nameIdx = idx
		case "personid", "person_id", "工号", "人员编号":
			personIDIdx = idx
		case "imagepath", "image_path", "image", "file", "photo", "图片路径":
			imageIdx = idx
		case "isprimary", "is_primary", "primary", "主人脸":
			primaryIdx = idx
		}
	}

	if nameIdx == -1 || imageIdx == -1 {
		return nil, errors.New("manifest.csv 缺少必需列: name, imagePath")
	}

	entries := []FaceImportEntry{}
	lineNum := 1
	for {
		record, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		lineNum++
		if err != nil {
			continue
		}
		if len(record) <= nameIdx || len(record) <= imageIdx {
			continue
		}

		name := strings.TrimSpace(record[nameIdx])
		imgPath := strings.TrimSpace(record[imageIdx])
		if name == "" || imgPath == "" {
			continue
		}

		var personID string
		if personIDIdx != -1 && personIDIdx < len(record) {
			personID = strings.TrimSpace(record[personIDIdx])
		}

		var isPrimary bool
		if primaryIdx != -1 && primaryIdx < len(record) {
			val := strings.ToLower(strings.TrimSpace(record[primaryIdx]))
			isPrimary = val == "true" || val == "1" || val == "yes" || val == "是"
		}

		entries = append(entries, FaceImportEntry{
			Line:      lineNum,
			Name:      name,
			PersonID:  personID,
			ImagePath: imgPath,
			IsPrimary: isPrimary,
		})
	}

	return entries, nil
}

var fileNameRegex = regexp.MustCompile(`(?i)^([^._]+)[._]([^.]+)\.(jpg|jpeg|png|webp)$`)

func scanImagesFallback(destDir string) ([]FaceImportEntry, error) {
	entries := []FaceImportEntry{}
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
			isAlphanumericP1 := isAlphaNum(p1)
			isAlphanumericP2 := isAlphaNum(p2)

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

		entries = append(entries, FaceImportEntry{
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

func isAlphaNum(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' {
			return false
		}
	}
	return true
}

// StartFaceImportJob initializes an asynchronous face bulk import job and delegates to asyncjob service.
func (s *Service) StartFaceImportJob(ctx context.Context, fileHeader *multipart.FileHeader) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.New(apperr.KindInternal, "ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
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
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to create temp job directory", err)
	}
	cleanupTemp := func() {
		if rmErr := os.RemoveAll(jobDir); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			s.logger.Warn("failed to clean up temp job directory", zap.String("jobDir", jobDir), zap.Error(rmErr))
		}
	}

	zipSavePath := filepath.Join(jobDir, "upload.zip")
	dst, err := os.Create(zipSavePath)
	if err != nil {
		cleanupTemp()
		return nil, apperr.New(apperr.KindInternal, "STORAGE_ERROR", "Failed to save uploaded ZIP file", err)
	}

	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		cleanupTemp()
		return nil, apperr.Invalid("FILE_READ_FAILED", "Failed to copy uploaded ZIP content", err)
	}
	dst.Close()

	meta := FaceImportMetadata{
		ZipPath:  zipSavePath,
		JobDir:   jobDir,
		Filename: fileHeader.Filename,
	}
	metaJSON, _ := json.Marshal(meta)

	job, err := s.asyncJobs.SubmitJob(ctx, FaceImportJobType, string(metaJSON))
	if err != nil {
		cleanupTemp()
		return nil, err
	}

	return job, nil
}

// GetFaceImportJob retrieves the live state of an asynchronous face import job.
func (s *Service) GetFaceImportJob(ctx context.Context, jobID string) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.New(apperr.KindInternal, "ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	return s.asyncJobs.GetJob(ctx, jobID)
}

// GetFaceImportFailedReportCSV generates a CSV diagnostic report for failed rows.
func (s *Service) GetFaceImportFailedReportCSV(ctx context.Context, jobID string) ([]byte, error) {
	job, err := s.GetFaceImportJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	buf.Write([]byte("\xef\xbb\xbf"))
	_ = w.Write([]string{"line", "filePath", "name", "personId", "errorCode", "message"})

	if job.ResultJSON != "" && job.ResultJSON != "{}" {
		var res FaceImportResult
		if err := json.Unmarshal([]byte(job.ResultJSON), &res); err == nil {
			for _, item := range res.FailedItems {
				_ = w.Write([]string{
					strconv.Itoa(item.Line),
					item.FilePath,
					item.Name,
					item.PersonID,
					item.ErrorCode,
					item.Message,
				})
			}
		}
	}

	w.Flush()
	return buf.Bytes(), nil
}
