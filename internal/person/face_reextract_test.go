package person

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/asyncjob"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

type reextractMockExtractor struct {
	mu sync.Mutex
	fn func(ctx context.Context, img []byte) ([]float32, error)
}

func (m *reextractMockExtractor) ExtractFaceFeature(ctx context.Context, img []byte) ([]float32, error) {
	m.mu.Lock()
	fn := m.fn
	m.mu.Unlock()
	if fn == nil {
		vec := make([]float32, faceFeatureDim)
		vec[0] = 1.0
		return vec, nil
	}
	return fn(ctx, img)
}

func makeUniformVector(val float32) []float32 {
	vec := make([]float32, faceFeatureDim)
	vec[0] = val
	return vec
}

func setupReextractTestEnv(
	t *testing.T,
	extractor FaceFeatureExtractor,
) (*Service, *Store, *asyncjob.Service, *mockGalleryNotifier) {
	t.Helper()
	tmpDir := t.TempDir()
	facesDir := filepath.Join(tmpDir, "faces")
	if err := os.MkdirAll(facesDir, 0o755); err != nil {
		t.Fatalf("mkdir facesDir: %v", err)
	}

	dbStore := database.New(tmpDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	jobComp := asyncjob.NewAsyncJobComponents(dbStore, zap.NewNop())
	if err := jobComp.Start(context.Background()); err != nil {
		t.Fatalf("start asyncjob: %v", err)
	}
	t.Cleanup(func() { _ = jobComp.Stop(context.Background()) })

	notifier := &mockGalleryNotifier{}
	comp := NewPersonComponents(dbStore, facesDir, zap.NewNop(), Options{
		Extractor:       extractor,
		GalleryNotifier: notifier,
		AsyncJobs:       jobComp.Service,
	})
	jobComp.Service.RegisterHandler(comp.JobHandler)
	jobComp.Service.RegisterHandler(comp.ReextractJobHandler)

	return comp.Service, comp.Store, jobComp.Service, notifier
}

func waitForJobTerminal(t *testing.T, svc *Service, jobID string) *asyncjob.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		job, err := svc.GetFaceReextractJob(context.Background(), jobID)
		if err != nil {
			t.Fatalf("GetFaceReextractJob: %v", err)
		}
		if job.Status.IsTerminal() {
			return job
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for job %s to reach terminal state", jobID)
	return nil
}

func TestFaceReextractFullAndSelected(t *testing.T) {
	pngA := createTestPNG(32, 32)
	pngB := createTestPNG(40, 40)

	version := float32(1.0)
	extractor := &reextractMockExtractor{
		fn: func(_ context.Context, img []byte) ([]float32, error) {
			if bytes.Equal(img, pngA) {
				return makeUniformVector(0.1 * version), nil
			}
			return makeUniformVector(0.2 * version), nil
		},
	}

	svc, store, _, notifier := setupReextractTestEnv(t, extractor)
	ctx := context.Background()

	p1, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Alice"})
	if err != nil {
		t.Fatalf("create p1: %v", err)
	}
	ext2 := "EMP-002"
	p2, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Bob", PersonID: &ext2})
	if err != nil {
		t.Fatalf("create p2: %v", err)
	}

	f1, err := svc.AddFaceWithReader(ctx, p1.ID, "a.png", bytes.NewReader(pngA))
	if err != nil {
		t.Fatalf("add f1: %v", err)
	}
	f2, err := svc.AddFaceWithReader(ctx, p2.ID, "b.png", bytes.NewReader(pngB))
	if err != nil {
		t.Fatalf("add f2: %v", err)
	}

	// Simulate model upgrade: extractor now outputs v2 embeddings.
	version = 2.0
	notifier.upserts = nil

	// 1. Selected-person re-extraction: only p2 should update.
	jobSel, err := svc.StartFaceReextractJob(ctx, []string{p2.ID, p2.ID, "  "})
	if err != nil {
		t.Fatalf("StartFaceReextractJob selected: %v", err)
	}
	doneSel := waitForJobTerminal(t, svc, jobSel.ID)
	if doneSel.Status != asyncjob.StatusCompleted || doneSel.Succeeded != 1 || doneSel.Failed != 0 {
		t.Fatalf("unexpected selected job outcome: %+v", doneSel)
	}

	gotF1, _ := store.GetFace(ctx, f1.ID)
	gotF2, _ := store.GetFace(ctx, f2.ID)
	if bytesToFloat32(gotF1.FeatureVector)[0] != 0.1 {
		t.Fatalf("expected f1 untouched at 0.1, got %v", bytesToFloat32(gotF1.FeatureVector)[0])
	}
	if bytesToFloat32(gotF2.FeatureVector)[0] != 0.4 {
		t.Fatalf("expected f2 updated to 0.4, got %v", bytesToFloat32(gotF2.FeatureVector)[0])
	}

	// 2. Full-gallery re-extraction with v3: both f1 and f2 update, IDs & primary preserved.
	version = 3.0
	jobAll, err := svc.StartFaceReextractJob(ctx, nil)
	if err != nil {
		t.Fatalf("StartFaceReextractJob all: %v", err)
	}
	doneAll := waitForJobTerminal(t, svc, jobAll.ID)
	if doneAll.Status != asyncjob.StatusCompleted || doneAll.Succeeded != 2 || doneAll.Failed != 0 {
		t.Fatalf("unexpected full job outcome: %+v", doneAll)
	}

	gotF1, _ = store.GetFace(ctx, f1.ID)
	gotF2, _ = store.GetFace(ctx, f2.ID)
	if bytesToFloat32(gotF1.FeatureVector)[0] != 0.3 {
		t.Fatalf("expected f1 updated to 0.3, got %v", bytesToFloat32(gotF1.FeatureVector)[0])
	}
	if bytesToFloat32(gotF2.FeatureVector)[0] != 0.6 {
		t.Fatalf("expected f2 updated to 0.6, got %v", bytesToFloat32(gotF2.FeatureVector)[0])
	}

	p1Reloaded, _ := svc.GetPerson(ctx, p1.ID)
	if p1Reloaded.PrimaryFaceID == nil || *p1Reloaded.PrimaryFaceID != f1.ID {
		t.Fatalf("primary face id mutated: %v", p1Reloaded.PrimaryFaceID)
	}
	if len(notifier.upserts) < 3 {
		t.Fatalf("expected gallery upserts to be broadcast, got %d", len(notifier.upserts))
	}
}

func TestFaceReextractPreservesOldVectorOnFailuresAndExportsCSV(t *testing.T) {
	pngGood := createTestPNG(24, 24)
	pngNoFace := createTestPNG(25, 25)
	pngMulti := createTestPNG(26, 26)
	pngBadDim := createTestPNG(27, 27)
	pngNaN := createTestPNG(28, 28)

	failMode := false
	extractor := &reextractMockExtractor{
		fn: func(_ context.Context, img []byte) ([]float32, error) {
			if !failMode {
				return makeUniformVector(0.55), nil
			}
			switch {
			case bytes.Equal(img, pngNoFace):
				return nil, ErrNoFaceDetected
			case bytes.Equal(img, pngMulti):
				return nil, ErrMultipleFaces
			case bytes.Equal(img, pngBadDim):
				return []float32{0.1, 0.2}, nil
			case bytes.Equal(img, pngNaN):
				v := makeUniformVector(0.1)
				v[3] = float32(math.NaN())
				return v, nil
			default:
				return makeUniformVector(0.88), nil
			}
		},
	}

	svc, store, _, _ := setupReextractTestEnv(t, extractor)
	ctx := context.Background()

	p, err := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Tester"})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}

	fGood, _ := svc.AddFaceWithReader(ctx, p.ID, "good.png", bytes.NewReader(pngGood))
	fNoFace, _ := svc.AddFaceWithReader(ctx, p.ID, "noface.png", bytes.NewReader(pngNoFace))
	fMulti, _ := svc.AddFaceWithReader(ctx, p.ID, "multi.png", bytes.NewReader(pngMulti))
	fBadDim, _ := svc.AddFaceWithReader(ctx, p.ID, "baddim.png", bytes.NewReader(pngBadDim))
	fNaN, _ := svc.AddFaceWithReader(ctx, p.ID, "nan.png", bytes.NewReader(pngNaN))
	fMissing, _ := svc.AddFaceWithReader(ctx, p.ID, "missing.png", bytes.NewReader(pngGood))
	// Remove missing.png from disk to simulate orphaned image_path.
	_ = os.Remove(filepath.Join(svc.facesDir, fMissing.ImagePath))

	failMode = true
	job, err := svc.StartFaceReextractJob(ctx, nil)
	if err != nil {
		t.Fatalf("StartFaceReextractJob: %v", err)
	}
	done := waitForJobTerminal(t, svc, job.ID)
	if done.Status != asyncjob.StatusCompleted || done.Succeeded != 1 || done.Failed != 5 {
		t.Fatalf("unexpected mixed outcome: %+v", done)
	}

	// Verify fGood updated, while all 5 failed faces kept old 0.55 embedding.
	gotGood, _ := store.GetFace(ctx, fGood.ID)
	if bytesToFloat32(gotGood.FeatureVector)[0] != 0.88 {
		t.Fatalf("expected fGood updated to 0.88, got %v", bytesToFloat32(gotGood.FeatureVector)[0])
	}
	for _, id := range []string{fNoFace.ID, fMulti.ID, fBadDim.ID, fNaN.ID, fMissing.ID} {
		got, _ := store.GetFace(ctx, id)
		if bytesToFloat32(got.FeatureVector)[0] != 0.55 {
			t.Fatalf("failed face %s overwrote prior vector: got %v", id, bytesToFloat32(got.FeatureVector)[0])
		}
	}

	csvBytes, err := svc.GetFaceReextractFailedReportCSV(ctx, job.ID, "en")
	if err != nil {
		t.Fatalf("GetFaceReextractFailedReportCSV: %v", err)
	}
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(csvBytes, []byte("\xef\xbb\xbf"))))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse csv: %v", err)
	}
	if len(rows) != 6 { // header + 5 failures
		t.Fatalf("expected 6 CSV rows, got %d: %+v", len(rows), rows)
	}
}

func TestFaceReextractGuardsAndValidation(t *testing.T) {
	ctx := context.Background()

	t.Run("no extractor returns FEATURE_EXTRACTOR_UNAVAILABLE", func(t *testing.T) {
		svc, _, _, _ := setupReextractTestEnv(t, nil)
		_, err := svc.StartFaceReextractJob(ctx, nil)
		if apperr.CodeOf(err) != "FEATURE_EXTRACTOR_UNAVAILABLE" {
			t.Fatalf("expected FEATURE_EXTRACTOR_UNAVAILABLE, got %v", err)
		}
	})

	t.Run("empty gallery returns NO_FACES_TO_REEXTRACT", func(t *testing.T) {
		svc, _, _, _ := setupReextractTestEnv(t, &reextractMockExtractor{})
		_, err := svc.StartFaceReextractJob(ctx, nil)
		if apperr.CodeOf(err) != "NO_FACES_TO_REEXTRACT" {
			t.Fatalf("expected NO_FACES_TO_REEXTRACT, got %v", err)
		}
	})

	t.Run("too many personIds returns REEXTRACT_TOO_MANY_PERSONS", func(t *testing.T) {
		svc, _, _, _ := setupReextractTestEnv(t, &reextractMockExtractor{})
		ids := make([]string, maxReextractPersonIDs+1)
		for i := range ids {
			ids[i] = fmt.Sprintf("p-%d", i)
		}
		_, err := svc.StartFaceReextractJob(ctx, ids)
		if apperr.CodeOf(err) != "REEXTRACT_TOO_MANY_PERSONS" {
			t.Fatalf("expected REEXTRACT_TOO_MANY_PERSONS, got %v", err)
		}
	})

	t.Run("concurrent active job rejected with FACE_REEXTRACT_IN_PROGRESS and cancel works", func(t *testing.T) {
		startedCh := make(chan struct{})
		releaseCh := make(chan struct{})
		var once sync.Once

		extractor := &reextractMockExtractor{
			fn: func(ctx context.Context, _ []byte) ([]float32, error) {
				if once.Do(func() { close(startedCh) }); false {
				}
				select {
				case <-releaseCh:
					return makeUniformVector(0.9), nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		}
		svc, _, _, _ := setupReextractTestEnv(t, extractor)
		p, _ := svc.CreatePerson(ctx, CreatePersonRequest{Name: "Blocking"})

		// Temporarily bypass blocking extractor to enroll initial face.
		origFn := extractor.fn
		extractor.fn = func(context.Context, []byte) ([]float32, error) {
			return makeUniformVector(0.1), nil
		}
		_, err := svc.AddFaceWithReader(ctx, p.ID, "init.png", bytes.NewReader(createTestPNG(20, 20)))
		if err != nil {
			t.Fatalf("enroll: %v", err)
		}
		extractor.fn = origFn

		job, err := svc.StartFaceReextractJob(ctx, nil)
		if err != nil {
			t.Fatalf("StartFaceReextractJob: %v", err)
		}
		<-startedCh

		_, err = svc.StartFaceReextractJob(ctx, nil)
		if apperr.CodeOf(err) != "FACE_REEXTRACT_IN_PROGRESS" {
			t.Fatalf("expected FACE_REEXTRACT_IN_PROGRESS, got %v", err)
		}

		if err := svc.CancelFaceReextractJob(ctx, job.ID); err != nil {
			t.Fatalf("CancelFaceReextractJob: %v", err)
		}
		close(releaseCh)

		done := waitForJobTerminal(t, svc, job.ID)
		if done.Status != asyncjob.StatusCancelled {
			t.Fatalf("expected StatusCancelled, got %s", done.Status)
		}
	})

	t.Run("foreign job type rejected with FACE_REEXTRACT_JOB_NOT_FOUND", func(t *testing.T) {
		svc := &Service{
			asyncJobs: stubAsyncJobs{job: &asyncjob.Job{ID: "job-other", Type: FaceImportJobType}},
			logger:    zap.NewNop(),
		}
		_, err := svc.GetFaceReextractJob(ctx, "job-other")
		if apperr.CodeOf(err) != "FACE_REEXTRACT_JOB_NOT_FOUND" {
			t.Fatalf("expected FACE_REEXTRACT_JOB_NOT_FOUND, got %v", err)
		}
		err = svc.CancelFaceReextractJob(ctx, "job-other")
		if apperr.CodeOf(err) != "FACE_REEXTRACT_JOB_NOT_FOUND" {
			t.Fatalf("expected FACE_REEXTRACT_JOB_NOT_FOUND on cancel, got %v", err)
		}
	})
}

func TestHandlerFaceReextractEndpointsAndAudit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tmpDir := t.TempDir()
	facesDir := filepath.Join(tmpDir, "faces")
	_ = os.MkdirAll(facesDir, 0o755)

	dbStore := database.New(tmpDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 50, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 500,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit: %v", err)
	}
	t.Cleanup(func() { _ = auditSvc.Stop(context.Background()) })

	jobComp := asyncjob.NewAsyncJobComponents(dbStore, zap.NewNop())
	if err := jobComp.Start(context.Background()); err != nil {
		t.Fatalf("start asyncjob: %v", err)
	}
	t.Cleanup(func() { _ = jobComp.Stop(context.Background()) })

	comp := NewPersonComponents(dbStore, facesDir, zap.NewNop(), Options{
		Extractor: &reextractMockExtractor{},
		AsyncJobs: jobComp.Service,
	})
	jobComp.Service.RegisterHandler(comp.ReextractJobHandler)

	r := gin.New()
	api := r.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	comp.Handler.RegisterRoutes(api)

	// 1. Latest before any job returns 200 with null data
	reqLatest := httptest.NewRequest(http.MethodGet, "/api/v1/persons/reextract-face-jobs/latest", nil)
	wLatest := httptest.NewRecorder()
	r.ServeHTTP(wLatest, reqLatest)
	if wLatest.Code != http.StatusOK {
		t.Fatalf("expected 200 for latest when empty, got %d: %s", wLatest.Code, wLatest.Body.String())
	}

	// 2. Enroll a person & face, then start re-extract job via HTTP
	p, err := comp.Service.CreatePerson(context.Background(), CreatePersonRequest{Name: "Charlie"})
	if err != nil {
		t.Fatalf("create person: %v", err)
	}
	_, err = comp.Service.AddFaceWithReader(context.Background(), p.ID, "c.png", bytes.NewReader(createTestPNG(20, 20)))
	if err != nil {
		t.Fatalf("add face: %v", err)
	}

	reqStart := httptest.NewRequest(http.MethodPost, "/api/v1/persons/reextract-face-jobs", strings.NewReader(`{}`))
	reqStart.Header.Set("Content-Type", "application/json")
	wStart := httptest.NewRecorder()
	r.ServeHTTP(wStart, reqStart)
	if wStart.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", wStart.Code, wStart.Body.String())
	}

	var startResp struct {
		Data FaceReextractJobResponse `json:"data"`
	}
	if err := json.Unmarshal(wStart.Body.Bytes(), &startResp); err != nil {
		t.Fatalf("decode start response: %v", err)
	}
	if startResp.Data.JobID == "" || startResp.Data.Scope != reextractScopeAll {
		t.Fatalf("unexpected start response: %+v", startResp.Data)
	}

	waitForJobTerminal(t, comp.Service, startResp.Data.JobID)

	// 3. Get job & latest & failed-report
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/persons/reextract-face-jobs/"+startResp.Data.JobID, nil)
	wGet := httptest.NewRecorder()
	r.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 on get job, got %d", wGet.Code)
	}

	reqReport := httptest.NewRequest(http.MethodGet, "/api/v1/persons/reextract-face-jobs/"+startResp.Data.JobID+"/failed-report", nil)
	wReport := httptest.NewRecorder()
	r.ServeHTTP(wReport, reqReport)
	if wReport.Code != http.StatusOK || !strings.Contains(wReport.Body.String(), "faceId,personRowId") {
		t.Fatalf("unexpected failed report response (%d): %s", wReport.Code, wReport.Body.String())
	}

	// 4. Verify audit entry recorded
	if err := auditSvc.Stop(context.Background()); err != nil {
		t.Fatalf("stop audit: %v", err)
	}
	logs, _, err := auditSvc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	foundAudit := false
	for _, entry := range logs {
		if entry.Action == ActionPersonFaceReextractCreate && entry.Target == "job:"+startResp.Data.JobID {
			foundAudit = true
			break
		}
	}
	if !foundAudit {
		t.Fatalf("expected audit entry for %s, got %+v", ActionPersonFaceReextractCreate, logs)
	}
}
