package person

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const (
	FaceReextractJobType  = "person.face_reextract"
	maxReextractPersonIDs = 1000
	reextractScopeAll     = "all"
	reextractScopeSelect  = "selected"
)

// FaceReextractMetadata holds initial job scope stored in Job.MetadataJSON.
type FaceReextractMetadata struct {
	Scope     string   `json:"scope"`
	PersonIDs []string `json:"personIds,omitempty"`
}

// FaceReextractResult holds final job result summary stored in Job.ResultJSON.
type FaceReextractResult struct {
	Total       int                       `json:"total"`
	Succeeded   int                       `json:"succeeded"`
	Failed      int                       `json:"failed"`
	FailedItems []FaceReextractFailedItem `json:"failedItems"`
}

// FaceReextractJobHandler implements asyncjob.JobHandler for face feature re-extraction.
type FaceReextractJobHandler struct {
	service *Service
}

// NewFaceReextractJobHandler constructs the job handler.
func NewFaceReextractJobHandler(s *Service) *FaceReextractJobHandler {
	return &FaceReextractJobHandler{service: s}
}

// Type returns "person.face_reextract".
func (h *FaceReextractJobHandler) Type() string {
	return FaceReextractJobType
}

// Execute performs the asynchronous face feature re-extraction job.
func (h *FaceReextractJobHandler) Execute(
	ctx context.Context,
	job *asyncjob.Job,
	updater asyncjob.ProgressUpdater,
) error {
	return h.service.ExecuteFaceReextractJob(ctx, job, updater)
}

func normalizeReextractPersonIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
		if len(out) > maxReextractPersonIDs {
			return nil, apperr.Invalid(
				"REEXTRACT_TOO_MANY_PERSONS",
				fmt.Sprintf("At most %d persons may be selected per re-extraction job", maxReextractPersonIDs),
				nil,
			)
		}
	}
	return out, nil
}

// StartFaceReextractJob validates prerequisites, guards against concurrent re-extraction
// jobs, and submits an asynchronous feature re-extraction job.
func (s *Service) StartFaceReextractJob(ctx context.Context, personIDs []string) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.Internal("ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	if s.extractor == nil {
		return nil, apperr.Internal(
			"FEATURE_EXTRACTOR_UNAVAILABLE",
			"Face feature extraction is not configured",
			nil,
		)
	}

	normalizedIDs, err := normalizeReextractPersonIDs(personIDs)
	if err != nil {
		return nil, err
	}

	existingJobs, _, err := s.asyncJobs.ListJobs(ctx, FaceReextractJobType, 20, 0)
	if err != nil {
		return nil, err
	}
	for _, j := range existingJobs {
		if j.Status.IsActive() {
			return nil, apperr.Conflict(
				"FACE_REEXTRACT_IN_PROGRESS",
				"A face feature re-extraction job is already in progress",
				nil,
			)
		}
	}

	targets, err := s.store.ListFacesForReextract(ctx, normalizedIDs)
	if err != nil {
		return nil, apperr.Internal("DATABASE_ERROR", "Failed to query faces for re-extraction", err)
	}
	if len(targets) == 0 {
		return nil, apperr.Conflict(
			"NO_FACES_TO_REEXTRACT",
			"No enrolled faces found to re-extract",
			nil,
		)
	}

	scope := reextractScopeAll
	if len(normalizedIDs) > 0 {
		scope = reextractScopeSelect
	}
	meta := FaceReextractMetadata{
		Scope:     scope,
		PersonIDs: normalizedIDs,
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		return nil, apperr.Internal("STORAGE_ERROR", "Failed to encode face re-extraction job metadata", err)
	}

	return s.asyncJobs.SubmitJob(ctx, FaceReextractJobType, string(metaJSON))
}

// GetFaceReextractJob retrieves the live state of a face feature re-extraction job.
func (s *Service) GetFaceReextractJob(ctx context.Context, jobID string) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.Internal("ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	job, err := s.asyncJobs.GetJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Type != FaceReextractJobType {
		return nil, apperr.NotFound("FACE_REEXTRACT_JOB_NOT_FOUND", "Face re-extraction job not found", nil)
	}
	return job, nil
}

// GetLatestFaceReextractJob returns the most recent face feature re-extraction job,
// or nil when none has been created yet.
func (s *Service) GetLatestFaceReextractJob(ctx context.Context) (*asyncjob.Job, error) {
	if s.asyncJobs == nil {
		return nil, apperr.Internal("ASYNC_JOB_UNAVAILABLE", "System async job service is not available", nil)
	}
	jobs, _, err := s.asyncJobs.ListJobs(ctx, FaceReextractJobType, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(jobs) == 0 {
		return nil, nil
	}
	job := jobs[0]
	return &job, nil
}

// CancelFaceReextractJob cancels an active face feature re-extraction job.
func (s *Service) CancelFaceReextractJob(ctx context.Context, jobID string) error {
	if _, err := s.GetFaceReextractJob(ctx, jobID); err != nil {
		return err
	}
	return s.asyncJobs.CancelJob(ctx, jobID)
}

// ExecuteFaceReextractJob re-reads original face photos from disk, extracts updated
// embeddings, persists them in place (preserving face IDs and primary pointers), and
// notifies running algorithm processes. Failures keep the prior vector intact.
func (s *Service) ExecuteFaceReextractJob(
	ctx context.Context,
	job *asyncjob.Job,
	updater asyncjob.ProgressUpdater,
) error {
	var meta FaceReextractMetadata
	if job.MetadataJSON != "" && job.MetadataJSON != "{}" {
		if err := json.Unmarshal([]byte(job.MetadataJSON), &meta); err != nil {
			return fmt.Errorf("parse job metadata: %w", err)
		}
	}

	s.logger.Info("executing face re-extract job",
		zap.String("jobId", job.ID),
		zap.String("scope", meta.Scope),
		zap.Int("personFilterCount", len(meta.PersonIDs)),
	)

	targets, err := s.store.ListFacesForReextract(ctx, meta.PersonIDs)
	if err != nil {
		return fmt.Errorf("list faces for reextract: %w", err)
	}

	total := len(targets)
	s.updateReextractProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage: asyncjob.StageProcessing,
		Total: total,
	}, job.ID)

	result := FaceReextractResult{
		Total:       total,
		FailedItems: []FaceReextractFailedItem{},
	}

	saveResult := func() {
		if encoded, marshalErr := json.Marshal(result); marshalErr == nil {
			job.ResultJSON = string(encoded)
		} else {
			s.logger.Warn("failed to encode face re-extract result",
				zap.String("jobId", job.ID),
				zap.Error(marshalErr),
			)
		}
	}

	for index, target := range targets {
		if ctxErr := ctx.Err(); ctxErr != nil {
			saveResult()
			return ctxErr
		}

		currentLabel := target.PersonName
		if target.ImagePath != "" {
			if currentLabel != "" {
				currentLabel = fmt.Sprintf("%s (%s)", target.PersonName, target.ImagePath)
			} else {
				currentLabel = target.ImagePath
			}
		}

		s.updateReextractProgress(ctx, updater, asyncjob.ProgressUpdate{
			Stage:       asyncjob.StageProcessing,
			CurrentFile: currentLabel,
			Total:       total,
			Processed:   index + 1,
			Succeeded:   result.Succeeded,
			Failed:      result.Failed,
		}, job.ID)

		if procErr := s.reextractOneFace(ctx, target); procErr != nil {
			var extID string
			if target.PersonExternalID != nil {
				extID = *target.PersonExternalID
			}
			result.Failed++
			result.FailedItems = append(result.FailedItems, FaceReextractFailedItem{
				FaceID:      target.FaceID,
				PersonRowID: target.PersonRowID,
				PersonID:    extID,
				PersonName:  target.PersonName,
				FilePath:    target.ImagePath,
				ErrorCode:   procErr.code,
				Message:     procErr.message,
			})
		} else {
			result.Succeeded++
		}

		if yieldErr := yieldFor(ctx, qosYieldDuration); yieldErr != nil {
			saveResult()
			return yieldErr
		}
	}

	saveResult()
	s.updateReextractProgress(ctx, updater, asyncjob.ProgressUpdate{
		Stage:     asyncjob.StageCompleted,
		Total:     total,
		Processed: total,
		Succeeded: result.Succeeded,
		Failed:    result.Failed,
	}, job.ID)

	s.logger.Info("completed face re-extract job",
		zap.String("jobId", job.ID),
		zap.Int("total", result.Total),
		zap.Int("succeeded", result.Succeeded),
		zap.Int("failed", result.Failed),
	)
	return nil
}

func (s *Service) updateReextractProgress(
	ctx context.Context,
	updater asyncjob.ProgressUpdater,
	update asyncjob.ProgressUpdate,
	jobID string,
) {
	if updater == nil {
		return
	}
	if err := updater.Update(ctx, update); err != nil {
		s.logger.Warn("failed to update face re-extract progress",
			zap.String("jobId", jobID),
			zap.String("stage", string(update.Stage)),
			zap.Error(err),
		)
	}
}

func (s *Service) reextractOneFace(ctx context.Context, target FaceReextractTarget) *faceProcessError {
	imgBytes, readErr := s.readStoredFaceImage(target.ImagePath)
	if readErr != nil {
		return readErr
	}

	if s.extractor == nil {
		return &faceProcessError{code: "FEATURE_EXTRACTOR_UNAVAILABLE", message: "人脸特征提取未配置"}
	}

	features, err := s.extractor.ExtractFaceFeature(ctx, imgBytes)
	if err != nil {
		if errors.Is(err, ErrNoFaceDetected) {
			return &faceProcessError{code: "NO_FACE_DETECTED", message: "照片中未检测到清晰人脸"}
		}
		if errors.Is(err, ErrUnsupportedImageFormat) {
			return &faceProcessError{code: "CORRUPT_OR_OVERSIZED_IMAGE", message: "图片格式不支持或无法解码"}
		}
		if errors.Is(err, ErrMultipleFaces) || isMultipleFacesError(err) {
			return &faceProcessError{code: "MULTIPLE_FACES_DETECTED", message: "检测到多张人脸"}
		}
		return &faceProcessError{
			code:    "FEATURE_EXTRACT_FAILED",
			message: "人脸特征提取失败: " + err.Error(),
		}
	}
	if len(features) != faceFeatureDim {
		return &faceProcessError{code: "FEATURE_DIM_MISMATCH", message: "人脸特征维度异常"}
	}
	for _, v := range features {
		if !isFinite(v) {
			return &faceProcessError{code: "FEATURE_EXTRACT_FAILED", message: "提取结果包含无效数值"}
		}
	}

	featBytes := float32ToBytes(features)
	if err := s.store.UpdateFaceFeature(ctx, target.FaceID, featBytes); err != nil {
		if errors.Is(err, ErrNotFound) {
			return &faceProcessError{code: "FACE_NOT_FOUND", message: "人脸底库记录已被删除"}
		}
		return &faceProcessError{code: "DATABASE_ERROR", message: "更新人脸特征向量失败"}
	}

	var extID string
	if target.PersonExternalID != nil {
		extID = *target.PersonExternalID
	}
	s.broadcastUpsert(ctx, GalleryItem{
		FaceID:     target.FaceID,
		PersonID:   extID,
		PersonName: target.PersonName,
		ImagePath:  target.ImagePath,
		Features:   features,
	})
	return nil
}

func (s *Service) readStoredFaceImage(imagePath string) ([]byte, *faceProcessError) {
	rel, err := s.faceStorageName(imagePath)
	if err != nil {
		code := apperr.CodeOf(err)
		if code == "IMAGE_NOT_FOUND" {
			return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "磁盘上未找到人脸图片文件"}
		}
		return nil, &faceProcessError{code: "INVALID_IMAGE_PATH", message: "登记照路径无效"}
	}

	root, err := os.OpenRoot(s.facesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "磁盘上未找到人脸图片文件"}
		}
		return nil, &faceProcessError{code: "STORAGE_ERROR", message: "打开人脸存储目录失败"}
	}
	defer root.Close()

	file, err := root.Open(rel)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "磁盘上未找到人脸图片文件"}
		}
		return nil, &faceProcessError{code: "INVALID_IMAGE_PATH", message: "登记照路径无效"}
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, &faceProcessError{code: "FILE_READ_FAILED", message: "读取人脸图片文件失败"}
	}
	if info.IsDir() {
		return nil, &faceProcessError{code: "IMAGE_NOT_FOUND", message: "磁盘上未找到人脸图片文件"}
	}

	imgBytes, err := io.ReadAll(io.LimitReader(file, MaxFaceImageBytes+1))
	if err != nil {
		return nil, &faceProcessError{code: "FILE_READ_FAILED", message: "读取人脸图片文件失败"}
	}
	if len(imgBytes) == 0 {
		return nil, &faceProcessError{code: "FILE_READ_FAILED", message: "人脸图片文件为空"}
	}
	if len(imgBytes) > MaxFaceImageBytes {
		return nil, &faceProcessError{code: "FILE_TOO_LARGE", message: "图片大小超过 10MB 限制"}
	}
	if !isSupportedImageContent(imgBytes[:min(512, len(imgBytes))]) {
		return nil, &faceProcessError{code: "CORRUPT_OR_OVERSIZED_IMAGE", message: "图片格式不支持或文件损坏"}
	}
	return imgBytes, nil
}

// GetFaceReextractFailedReportCSV generates a CSV diagnostic report for faces that failed
// re-extraction in the specified job.
func (s *Service) GetFaceReextractFailedReportCSV(
	ctx context.Context,
	jobID string,
	acceptLanguages ...string,
) ([]byte, error) {
	job, err := s.GetFaceReextractJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if _, err := buf.Write([]byte("\xef\xbb\xbf")); err != nil {
		return nil, fmt.Errorf("write csv bom: %w", err)
	}
	if err := w.Write([]string{"faceId", "personRowId", "personId", "name", "filePath", "errorCode", "message"}); err != nil {
		return nil, fmt.Errorf("write csv header: %w", err)
	}

	if job.ResultJSON == "" || job.ResultJSON == "{}" {
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, fmt.Errorf("flush csv writer: %w", err)
		}
		return buf.Bytes(), nil
	}

	var res FaceReextractResult
	if err := json.Unmarshal([]byte(job.ResultJSON), &res); err != nil {
		w.Flush()
		if err := w.Error(); err != nil {
			return nil, fmt.Errorf("flush csv writer: %w", err)
		}
		return buf.Bytes(), nil
	}

	locale := "zh-Hans"
	if len(acceptLanguages) > 0 && acceptLanguages[0] != "" {
		locale = httputil.ResolveLocale(acceptLanguages[0])
	}
	for _, item := range res.FailedItems {
		msg := item.Message
		if localized, ok := httputil.LookupMessage(item.ErrorCode, locale); ok && localized != "" {
			if isStaticDiagnosticMessage(item.Message) {
				msg = localized
			}
		}
		if err := w.Write([]string{
			item.FaceID,
			item.PersonRowID,
			item.PersonID,
			item.PersonName,
			item.FilePath,
			item.ErrorCode,
			msg,
		}); err != nil {
			s.logger.Warn("failed to write re-extract failed-report row",
				zap.String("jobId", jobID),
				zap.Error(err),
			)
			break
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush csv writer: %w", err)
	}
	return buf.Bytes(), nil
}
