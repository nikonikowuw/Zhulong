package person

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

func createTestPNG(width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func createZipArchive(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := zw.Create(name)
		if err != nil {
			return nil, err
		}
		if _, err := f.Write(content); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func TestSafeExtractZip(t *testing.T) {
	t.Run("extracts valid files and ignores junk", func(t *testing.T) {
		zipBytes, err := createZipArchive(map[string][]byte{
			"images/test.png":     createTestPNG(10, 10),
			"__MACOSX/._test.png": []byte("junk"),
			".DS_Store":           []byte("junk"),
			"images/Thumbs.db":    []byte("junk"),
			"manifest.csv":        []byte("name,imagePath\nAlice,images/test.png"),
		})
		if err != nil {
			t.Fatalf("create zip failed: %v", err)
		}

		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "test.zip")
		if err := os.WriteFile(zipPath, zipBytes, 0644); err != nil {
			t.Fatalf("write zip failed: %v", err)
		}

		destDir := filepath.Join(tmpDir, "out")
		if err := safeExtractZip(zipPath, destDir); err != nil {
			t.Fatalf("safeExtractZip failed: %v", err)
		}

		if _, err := os.Stat(filepath.Join(destDir, "images/test.png")); err != nil {
			t.Fatalf("expected extracted image: %v", err)
		}
		if _, err := os.Stat(filepath.Join(destDir, "manifest.csv")); err != nil {
			t.Fatalf("expected extracted manifest: %v", err)
		}
		if _, err := os.Stat(filepath.Join(destDir, ".DS_Store")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("expected junk to be skipped")
		}
	})

	t.Run("detects zip slip path traversal", func(t *testing.T) {
		zipBytes, err := createZipArchive(map[string][]byte{
			"../../evil.txt": []byte("pwned"),
		})
		if err != nil {
			t.Fatalf("create zip failed: %v", err)
		}

		tmpDir := t.TempDir()
		zipPath := filepath.Join(tmpDir, "slip.zip")
		_ = os.WriteFile(zipPath, zipBytes, 0644)

		destDir := filepath.Join(tmpDir, "out")
		err = safeExtractZip(zipPath, destDir)
		if !errors.Is(err, ErrZipSlip) {
			t.Fatalf("expected ErrZipSlip, got: %v", err)
		}
	})
}

func TestParseManifestCSV(t *testing.T) {
	t.Run("parses valid manifest with UTF-8 BOM", func(t *testing.T) {
		tmpDir := t.TempDir()
		manifestContent := "\xef\xbb\xbfname,person_id,image_path,is_primary\n" +
			"Alice,EMP001,images/a.png,true\n" +
			"Bob,EMP002,images/b.png,0\n"
		csvPath := filepath.Join(tmpDir, "manifest.csv")
		_ = os.WriteFile(csvPath, []byte(manifestContent), 0644)

		entries, err := parseManifestCSV(csvPath)
		if err != nil {
			t.Fatalf("parseManifestCSV failed: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("expected 2 entries, got %d", len(entries))
		}
		e0 := entries[0]
		if e0.Name != "Alice" || e0.PersonID != "EMP001" || e0.ImagePath != "images/a.png" || !e0.IsPrimary {
			t.Fatalf("unexpected entry 0: %+v", entries[0])
		}
		e1 := entries[1]
		if e1.Name != "Bob" || e1.PersonID != "EMP002" || e1.ImagePath != "images/b.png" || e1.IsPrimary {
			t.Fatalf("unexpected entry 1: %+v", entries[1])
		}
	})

	t.Run("missing required columns returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		csvPath := filepath.Join(tmpDir, "manifest.csv")
		_ = os.WriteFile(csvPath, []byte("personId,imagePath\nEMP001,images/a.png"), 0644)

		_, err := parseManifestCSV(csvPath)
		if err == nil {
			t.Fatalf("expected error for missing name column")
		}
	})
}

func TestScanImagesFallback(t *testing.T) {
	tmpDir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "EMP001_张三.jpg"), createTestPNG(10, 10), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "sub/李四_EMP002.png"), createTestPNG(10, 10), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "plain.jpeg"), createTestPNG(10, 10), 0644)

	entries, err := scanImagesFallback(tmpDir)
	if err != nil {
		t.Fatalf("scanImagesFallback failed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}

	foundAlice := false
	foundBob := false
	foundPlain := false

	for _, e := range entries {
		if e.PersonID == "EMP001" && e.Name == "张三" {
			foundAlice = true
		}
		if e.PersonID == "EMP002" && e.Name == "李四" {
			foundBob = true
		}
		if e.Name == "plain" && e.PersonID == "" {
			foundPlain = true
		}
	}

	if !foundAlice || !foundBob || !foundPlain {
		t.Fatalf("entries not resolved correctly: %+v", entries)
	}
}

type selectiveExtractor struct {
	features []float32
	failOn   map[string]error
}

func (m *selectiveExtractor) ExtractFaceFeature(ctx context.Context, img []byte) ([]float32, error) {
	// If image is less than 100 bytes, treat as corrupt
	if len(img) < 100 {
		return nil, ErrUnsupportedImageFormat
	}
	return m.features, nil
}

func TestFaceImportJob_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	features := make([]float32, faceFeatureDim)
	features[0] = 1.0
	extractor := &selectiveExtractor{features: features}

	asyncStore := asyncjob.NewStore(dbStore)
	asyncSvc := asyncjob.NewService(asyncStore, zap.NewNop())
	if err := asyncSvc.Start(context.Background()); err != nil {
		t.Fatalf("asyncSvc.Start failed: %v", err)
	}
	t.Cleanup(func() { _ = asyncSvc.Stop(context.Background()) })

	opts := Options{AsyncJobs: asyncSvc, Extractor: extractor}
	svc := NewService(NewStore(dbStore), filepath.Join(tempDir, "faces"), zap.NewNop(), opts)

	jobHandler := NewFaceImportJobHandler(svc)
	asyncSvc.RegisterHandler(jobHandler)

	// Prepare ZIP with 2 images: Alice (valid 100x100) and Corrupt (invalid 1x1 raw bytes)
	validPNG := createTestPNG(100, 100)
	corruptBytes := []byte("not an image")

	manifestData := []byte("name,personId,imagePath\n" +
		"Alice,EMP001,images/alice.png\n" +
		"Bob,EMP002,images/corrupt.png\n")

	zipBytes, err := createZipArchive(map[string][]byte{
		"manifest.csv":       manifestData,
		"images/alice.png":   validPNG,
		"images/corrupt.png": corruptBytes,
	})
	if err != nil {
		t.Fatalf("create zip failed: %v", err)
	}

	fh := &multipart.FileHeader{
		Filename: "upload.zip",
		Size:     int64(len(zipBytes)),
		Header:   make(map[string][]string),
	}
	// Write to temp file to mock multipart file opening
	zipFilePath := filepath.Join(tempDir, "source.zip")
	_ = os.WriteFile(zipFilePath, zipBytes, 0644)
	mockFile, _ := os.Open(zipFilePath)
	defer mockFile.Close()

	// Use Handler with Gin test recorder to test full HTTP flow
	gin.SetMode(gin.TestMode)
	handler := NewHandler(svc, zap.NewNop())
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	handler.RegisterUploadRoutes(router.Group("/api/v1"))

	// Create multipart request
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "upload.zip")
	_, _ = part.Write(zipBytes)
	writer.Close()

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/persons/import-face-jobs", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202 Accepted, got %d: %s", w.Code, w.Body.String())
	}

	// Wait for worker to finish processing (polling up to 5 seconds)
	jobID := ""
	for i := 0; i < 50; i++ {
		time.Sleep(100 * time.Millisecond)
		jobs, total, _ := svc.store.ListPersons(context.Background(), 10, 0, "", "")
		if total > 0 && len(jobs) > 0 {
			break
		}
	}

	// Verify Alice was enrolled
	alice, err := svc.store.GetPersonByExternalID(context.Background(), "EMP001")
	if err != nil {
		t.Fatalf("expected Alice to be created: %v", err)
	}
	if alice.Name != "Alice" {
		t.Fatalf("expected Alice name, got %s", alice.Name)
	}
	faces, err := svc.store.ListFacesByPerson(context.Background(), alice.ID)
	if err != nil || len(faces) != 1 {
		t.Fatalf("expected 1 face for Alice, got %d: %v", len(faces), err)
	}

	// Verify Bob was not enrolled
	_, err = svc.store.GetPersonByExternalID(context.Background(), "EMP002")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected Bob to not be enrolled, got err: %v", err)
	}

	// Find the job ID from DB
	var dbJob asyncjob.Job
	db := dbStore.DB()
	if err := db.Order("created_at desc").First(&dbJob).Error; err != nil {
		t.Fatalf("failed to query job: %v", err)
	}
	jobID = dbJob.ID

	if dbJob.Status != asyncjob.StatusCompleted {
		t.Fatalf("expected job status COMPLETED, got %s (error: %s)", dbJob.Status, dbJob.ErrorMessage)
	}
	if dbJob.Total != 2 || dbJob.Succeeded != 1 || dbJob.Failed != 1 {
		t.Fatalf(
			"expected total 2, succeeded 1, failed 1; got total %d, succ %d, fail %d",
			dbJob.Total,
			dbJob.Succeeded,
			dbJob.Failed,
		)
	}

	// Download diagnostic report
	reportReq, _ := http.NewRequest(http.MethodGet, "/api/v1/persons/import-face-jobs/"+jobID+"/failed-report", nil)
	reportW := httptest.NewRecorder()
	router.ServeHTTP(reportW, reportReq)

	if reportW.Code != http.StatusOK {
		t.Fatalf("expected report 200 OK, got %d: %s", reportW.Code, reportW.Body.String())
	}
	reportCSV := reportW.Body.String()
	if !strings.Contains(reportCSV, "EMP002") || !strings.Contains(reportCSV, "CORRUPT_OR_OVERSIZED_IMAGE") {
		t.Fatalf("report CSV should contain Bob and error: %s", reportCSV)
	}

	// Download diagnostic report with English locale
	reportReqEn, _ := http.NewRequest(http.MethodGet, "/api/v1/persons/import-face-jobs/"+jobID+"/failed-report", nil)
	reportReqEn.Header.Set("Accept-Language", "en")
	reportWEn := httptest.NewRecorder()
	router.ServeHTTP(reportWEn, reportReqEn)
	if reportWEn.Code != http.StatusOK {
		t.Fatalf("expected english report 200 OK, got %d: %s", reportWEn.Code, reportWEn.Body.String())
	}
	reportCSVEn := reportWEn.Body.String()
	if !strings.Contains(reportCSVEn, "Image is corrupted") {
		t.Fatalf("expected localized english message in CSV, got: %s", reportCSVEn)
	}

	_ = fh
}

func TestService_StartLifecycle(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer dbStore.Close()

	facesDir := filepath.Join(tempDir, "faces")
	svc := NewService(NewStore(dbStore), facesDir, zap.NewNop(), Options{})

	// Pre-create a stale import directory in tmpDir
	staleDir := filepath.Join(svc.tempDir, "face_import_stale123")
	if err := os.MkdirAll(staleDir, 0755); err != nil {
		t.Fatalf("create stale dir: %v", err)
	}
	otherDir := filepath.Join(svc.tempDir, "keep_other_dir")
	if err := os.MkdirAll(otherDir, 0755); err != nil {
		t.Fatalf("create other dir: %v", err)
	}

	// Execute Start lifecycle hook
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("svc.Start failed: %v", err)
	}

	// Stale face import directory should have been cleaned up
	if _, err := os.Stat(staleDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale directory to be removed")
	}
	// Other directory should remain
	if _, err := os.Stat(otherDir); err != nil {
		t.Fatalf("expected other directory to be kept: %v", err)
	}
}
