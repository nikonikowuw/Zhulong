package asyncjob

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

type mockEchoHandler struct {
	executed bool
}

func (m *mockEchoHandler) Type() string {
	return "test.echo"
}

func (m *mockEchoHandler) Execute(ctx context.Context, job *Job, updater ProgressUpdater) error {
	m.executed = true
	_ = updater.Update(ctx, ProgressUpdate{
		Stage:     "PROCESSING",
		Total:     10,
		Processed: 5,
		Succeeded: 5,
		Failed:    0,
	})
	_ = updater.Update(ctx, ProgressUpdate{
		Stage:     "COMPLETED",
		Total:     10,
		Processed: 10,
		Succeeded: 10,
		Failed:    0,
	})
	return nil
}

func TestAsyncJobService_EndToEnd(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	handler := &mockEchoHandler{}
	svc.RegisterHandler(handler)

	job, err := svc.SubmitJob(context.Background(), "test.echo", `{"param":"value"}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	// Poll until completed
	var latest *Job
	for i := 0; i < 40; i++ {
		time.Sleep(25 * time.Millisecond)
		latest, err = svc.GetJob(context.Background(), job.ID)
		if err == nil && latest.Status == StatusCompleted {
			break
		}
	}

	if latest == nil || latest.Status != StatusCompleted {
		t.Fatalf("expected job status COMPLETED, got: %+v", latest)
	}
	if latest.Progress != 100 || latest.Succeeded != 10 {
		t.Fatalf("unexpected progress or counts: %+v", latest)
	}
	if !handler.executed {
		t.Fatalf("handler was not executed")
	}
}

type mockBlockingHandler struct {
	started chan struct{}
}

func (m *mockBlockingHandler) Type() string {
	return "test.block"
}

func (m *mockBlockingHandler) Execute(ctx context.Context, job *Job, updater ProgressUpdater) error {
	close(m.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestAsyncJobService_CancelJob(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	handler := &mockBlockingHandler{started: make(chan struct{})}
	svc.RegisterHandler(handler)

	job, err := svc.SubmitJob(context.Background(), "test.block", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	select {
	case <-handler.started:
	case <-time.After(2 * time.Second):
		t.Fatalf("handler did not start in time")
	}

	if err := svc.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("CancelJob failed: %v", err)
	}

	latest, err := svc.GetJob(context.Background(), job.ID)
	if err != nil || latest.Status != StatusCancelled {
		t.Fatalf("expected status CANCELLED, got: %+v (err: %v)", latest, err)
	}
}

func TestAsyncJobService_RecoverOrphans(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)

	// Pre-populate stale pending/processing jobs
	orphan1 := &Job{ID: "job-stale-1", Type: "test.echo", Status: StatusPending}
	orphan2 := &Job{ID: "job-stale-2", Type: "test.echo", Status: StatusProcessing}
	_ = store.CreateJob(context.Background(), orphan1)
	_ = store.CreateJob(context.Background(), orphan2)

	svc := NewService(store, zap.NewNop())
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	j1, _ := store.GetJob(context.Background(), "job-stale-1")
	j2, _ := store.GetJob(context.Background(), "job-stale-2")

	if j1.Status != StatusFailed || j2.Status != StatusFailed {
		t.Fatalf("expected both orphan jobs to be marked FAILED, got j1=%s, j2=%s", j1.Status, j2.Status)
	}
}

type mockPanicHandler struct{}

func (m *mockPanicHandler) Type() string {
	return "test.panic"
}

func (m *mockPanicHandler) Execute(ctx context.Context, job *Job, updater ProgressUpdater) error {
	panic("unexpected runtime error in worker")
}

func TestAsyncJobService_PanicRecovery(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	svc.RegisterHandler(&mockPanicHandler{})

	job, err := svc.SubmitJob(context.Background(), "test.panic", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	var latest *Job
	for i := 0; i < 40; i++ {
		time.Sleep(25 * time.Millisecond)
		latest, err = svc.GetJob(context.Background(), job.ID)
		if err == nil && latest.Status == StatusFailed {
			break
		}
	}

	if latest == nil || latest.Status != StatusFailed {
		t.Fatalf("expected status FAILED after panic, got: %+v", latest)
	}
	if latest.ErrorMessage == "" {
		t.Fatalf("expected error message to record panic info")
	}
}

func TestStore_FinishJob_ProgressNotOverwrittenOnFailure(t *testing.T) {
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	job := &Job{
		ID:       "job-partial-fail",
		Type:     "test",
		Status:   StatusProcessing,
		Progress: 35,
	}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	if err := store.FinishJob(context.Background(), job.ID, StatusFailed, "{}", "something failed"); err != nil {
		t.Fatalf("FinishJob failed: %v", err)
	}

	fetched, err := store.GetJob(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}

	if fetched.Status != StatusFailed {
		t.Fatalf("expected status FAILED, got %s", fetched.Status)
	}
	if fetched.Progress != 35 {
		t.Fatalf("expected progress to stay 35, got %d", fetched.Progress)
	}
}

func TestHandler_Endpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tempDir := t.TempDir()
	dbStore := database.New(tempDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() { _ = svc.Stop(context.Background()) })

	handler := NewHandler(svc, zap.NewNop())

	router := gin.New()
	rg := router.Group("/api/v1")
	handler.RegisterRoutes(rg)

	// Pre-create a job
	job := &Job{ID: "job-http-1", Type: "test.sample", Status: StatusPending}
	_ = store.CreateJob(context.Background(), job)

	// Test GET list
	reqList := httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs?page=1&pageSize=10", nil)
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, reqList)
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 from List, got %d", wList.Code)
	}

	// Test GET by ID
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs/job-http-1", nil)
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, reqGet)
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 from Get, got %d", wGet.Code)
	}

	// Test Cancel
	reqCancel := httptest.NewRequest(http.MethodPost, "/api/v1/system/jobs/job-http-1/cancel", nil)
	wCancel := httptest.NewRecorder()
	router.ServeHTTP(wCancel, reqCancel)
	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 from Cancel, got %d", wCancel.Code)
	}
}
