package asyncjob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/audit"
	audithttp "github.com/nikonikowuw/Zhulong/internal/audit/http"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"github.com/nikonikowuw/Zhulong/internal/httputil"
	"go.uber.org/zap"
)

const jobTestTimeout = 5 * time.Second

func newTestService(t *testing.T) (*Service, *Store) {
	t.Helper()
	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	store := NewStore(dbStore)
	return NewService(store, zap.NewNop()), store
}

// cleanupStop registers the shutdown as a test cleanup and fails the test when
// it errors, instead of discarding the error with `_ =`.
func cleanupStop(t *testing.T, svc *Service) {
	t.Helper()
	t.Cleanup(func() {
		if err := svc.Stop(context.Background()); err != nil {
			t.Errorf("async job service Stop failed: %v", err)
		}
	})
}

// waitFor polls a condition with a bounded deadline. It uses a ticker and a
// timer instead of time.After inside the loop, so no timer is leaked per tick.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(jobTestTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()

	for {
		if cond() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		case <-ticker.C:
		}
	}
}

func assertCode(t *testing.T, err error, want string) {
	t.Helper()
	var appErr *apperr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("expected *apperr.Error, got %v", err)
	}
	if appErr.Code != want {
		t.Fatalf("expected error code %s, got %s", want, appErr.Code)
	}
}

func mustGetJob(t *testing.T, svc *Service, id string) *Job {
	t.Helper()
	job, err := svc.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob(%s) failed: %v", id, err)
	}
	return job
}

func waitForStatus(t *testing.T, svc *Service, id string, want JobStatus) *Job {
	t.Helper()
	var latest *Job
	waitFor(t, fmt.Sprintf("job %s to reach %s", id, want), func() bool {
		job, err := svc.GetJob(context.Background(), id)
		if err != nil {
			return false
		}
		latest = job
		return job.Status == want
	})
	return latest
}

// scriptedHandler is a deterministic JobHandler used across the suite. All
// mutable state is guarded: the worker goroutine and the test goroutine read
// the same counters.
type scriptedHandler struct {
	jobType  string
	updates  []ProgressUpdate
	block    bool
	release  chan struct{}
	started  chan struct{}
	finished chan struct{}
	// resultJSON is written into the job before Execute returns.
	resultJSON string

	mu         sync.Mutex
	executions int
	aborts     int
}

func (h *scriptedHandler) Type() string { return h.jobType }

func (h *scriptedHandler) Execute(ctx context.Context, job *Job, updater ProgressUpdater) error {
	h.mu.Lock()
	h.executions++
	h.mu.Unlock()
	defer func() {
		if h.finished != nil {
			close(h.finished)
		}
	}()
	if h.started != nil {
		close(h.started)
	}
	for _, update := range h.updates {
		if err := updater.Update(ctx, update); err != nil {
			return err
		}
	}
	if h.resultJSON != "" {
		job.ResultJSON = h.resultJSON
	}
	if h.block {
		select {
		case <-h.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (h *scriptedHandler) Abort(context.Context, *Job) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.aborts++
	return nil
}

func (h *scriptedHandler) counts() (executions, aborts int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.executions, h.aborts
}

type panicHandler struct{ jobType string }

func (h *panicHandler) Type() string { return h.jobType }

func (h *panicHandler) Execute(context.Context, *Job, ProgressUpdater) error {
	panic("unexpected runtime error in worker")
}

func TestSubmitJobExecutesAndCompletes(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	handler := &scriptedHandler{
		jobType: "test.echo",
		updates: []ProgressUpdate{
			{Stage: StageProcessing, Total: 10, Processed: 5, Succeeded: 5},
			{Stage: StageCompleted, Total: 10, Processed: 10, Succeeded: 10},
		},
		resultJSON: `{"ok":true}`,
	}
	svc.RegisterHandler(handler)

	job, err := svc.SubmitJob(context.Background(), "test.echo", `{"param":"value"}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	latest := waitForStatus(t, svc, job.ID, StatusCompleted)
	if latest.Progress != 100 || latest.Succeeded != 10 {
		t.Fatalf("unexpected progress or counts: %+v", latest)
	}
	if latest.ResultJSON != `{"ok":true}` {
		t.Fatalf("expected handler result to survive, got %q", latest.ResultJSON)
	}
	if latest.FinishedAt == nil {
		t.Fatalf("expected finished_at to be set")
	}
	if executions, _ := handler.counts(); executions != 1 {
		t.Fatalf("expected exactly one execution, got %d", executions)
	}
}

func TestJobReportsExtractingStage(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	handler := &scriptedHandler{
		jobType:  "test.extract",
		block:    true,
		release:  make(chan struct{}),
		finished: make(chan struct{}),
		updates: []ProgressUpdate{{
			Stage:  StageExtracting,
			Status: StatusExtracting,
			Total:  10,
		}},
	}
	svc.RegisterHandler(handler)

	job, err := svc.SubmitJob(context.Background(), "test.extract", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	extracting := waitForStatus(t, svc, job.ID, StatusExtracting)
	if extracting.CurrentStage != string(StageExtracting) {
		t.Fatalf("expected current stage EXTRACTING, got %q", extracting.CurrentStage)
	}

	close(handler.release)
	waitForStatus(t, svc, job.ID, StatusCompleted)
}

func TestCancelRunningJob(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	handler := &scriptedHandler{
		jobType:    "test.block",
		block:      true,
		release:    make(chan struct{}),
		started:    make(chan struct{}),
		finished:   make(chan struct{}),
		resultJSON: `{"partial":1}`,
	}
	svc.RegisterHandler(handler)

	job, err := svc.SubmitJob(context.Background(), "test.block", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	select {
	case <-handler.started:
	case <-time.After(jobTestTimeout):
		t.Fatalf("handler did not start in time")
	}

	if err := svc.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("CancelJob failed: %v", err)
	}
	waitFor(t, "handler to observe cancellation", func() bool {
		select {
		case <-handler.finished:
			return true
		default:
			return false
		}
	})

	latest := mustGetJob(t, svc, job.ID)
	if latest.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED after the worker finished, got %s", latest.Status)
	}
	if latest.FinishedAt == nil {
		t.Fatalf("expected cancelled job to carry finished_at")
	}
	// The handler's partial result must not be replaced by a failure payload.
	if latest.ResultJSON != `{"partial":1}` && latest.ResultJSON != "{}" {
		t.Fatalf("unexpected result payload after cancellation: %q", latest.ResultJSON)
	}

	err = svc.CancelJob(context.Background(), job.ID)
	if err == nil {
		t.Fatalf("expected a second cancellation to be rejected")
	}
	assertCode(t, err, CodeJobNotCancellable)
}

func TestCancelQueuedJobAbortsWithoutExecuting(t *testing.T) {
	svc, _ := newTestService(t)
	handler := &scriptedHandler{jobType: "test.queued"}
	svc.RegisterHandler(handler)

	// Submit while the worker is not running, so the job is still PENDING when
	// it is cancelled.
	job, err := svc.SubmitJob(context.Background(), "test.queued", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}
	if err := svc.CancelJob(context.Background(), job.ID); err != nil {
		t.Fatalf("CancelJob failed: %v", err)
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	waitFor(t, "abort hook to run for the queued job", func() bool {
		_, aborts := handler.counts()
		return aborts == 1
	})
	if executions, _ := handler.counts(); executions != 0 {
		t.Fatalf("cancelled queued job must not execute, saw %d executions", executions)
	}
	if latest := mustGetJob(t, svc, job.ID); latest.Status != StatusCancelled {
		t.Fatalf("expected CANCELLED, got %s", latest.Status)
	}
}

func TestCancelRejectsUnknownAndFinishedJobs(t *testing.T) {
	svc, store := newTestService(t)

	err := svc.CancelJob(context.Background(), "does-not-exist")
	if err == nil {
		t.Fatalf("expected cancelling an unknown job to fail")
	}
	assertCode(t, err, CodeJobNotFound)

	finishedAt := time.Now().UTC()
	done := &Job{
		ID:         "job-done",
		Type:       "test.done",
		Status:     StatusCompleted,
		ResultJSON: `{"kept":true}`,
		FinishedAt: &finishedAt,
	}
	if err := store.CreateJob(context.Background(), done); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	err = svc.CancelJob(context.Background(), done.ID)
	if err == nil {
		t.Fatalf("expected cancelling a completed job to fail")
	}
	assertCode(t, err, CodeJobNotCancellable)

	latest := mustGetJob(t, svc, done.ID)
	if latest.Status != StatusCompleted {
		t.Fatalf("completed job was rewritten to %s", latest.Status)
	}
	if latest.ResultJSON != `{"kept":true}` {
		t.Fatalf("completed job lost its result: %q", latest.ResultJSON)
	}
}

func TestLateProgressDoesNotResurrectTerminalJob(t *testing.T) {
	svc, store := newTestService(t)
	job := &Job{ID: "job-late", Type: "test", Status: StatusCompleted, Progress: 100}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	if err := store.UpdateProgress(
		context.Background(),
		job.ID,
		40,
		ProgressUpdate{
			Stage:     StageProcessing,
			Total:     10,
			Processed: 4,
		},
	); err != nil {
		t.Fatalf("UpdateProgress failed: %v", err)
	}

	latest := mustGetJob(t, svc, job.ID)
	if latest.Status != StatusCompleted {
		t.Fatalf("late progress resurrected the job to %s", latest.Status)
	}
	if latest.Progress != 100 {
		t.Fatalf("late progress overwrote the terminal percentage: %d", latest.Progress)
	}
}

func TestPanicMarksJobFailedWithCode(t *testing.T) {
	svc, _ := newTestService(t)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	svc.RegisterHandler(&panicHandler{jobType: "test.panic"})
	job, err := svc.SubmitJob(context.Background(), "test.panic", `{}`)
	if err != nil {
		t.Fatalf("SubmitJob failed: %v", err)
	}

	latest := waitForStatus(t, svc, job.ID, StatusFailed)
	if latest.ErrorCode != CodeJobPanicked {
		t.Fatalf("expected error code %s, got %q", CodeJobPanicked, latest.ErrorCode)
	}
	// The panic text belongs to the server log, never to the job row.
	if latest.ErrorMessage != "" {
		t.Fatalf("expected no raw panic text in the job row, got %q", latest.ErrorMessage)
	}
}

func TestStartupRecoveryFailsOrphansAndClearsProgress(t *testing.T) {
	svc, store := newTestService(t)

	stale := []*Job{
		{ID: "job-stale-1", Type: "test.echo", Status: StatusPending},
		{ID: "job-stale-2", Type: "test.echo", Status: StatusProcessing, CurrentFile: "images/a.jpg"},
		{ID: "job-stale-3", Type: "test.echo", Status: StatusExtracting, CurrentStage: string(StageExtracting)},
	}
	for _, job := range stale {
		if err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}

	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	cleanupStop(t, svc)

	for _, job := range stale {
		latest := mustGetJob(t, svc, job.ID)
		if latest.Status != StatusFailed {
			t.Fatalf("expected %s to be FAILED, got %s", job.ID, latest.Status)
		}
		if latest.ErrorCode != CodeJobInterrupted {
			t.Fatalf("expected code %s on %s, got %q", CodeJobInterrupted, job.ID, latest.ErrorCode)
		}
		if latest.CurrentFile != "" || latest.CurrentStage != "" {
			t.Fatalf("recovered job %s kept stale progress: %+v", job.ID, latest)
		}
	}
}

func TestStopFailsQueuedJobsAndRejectsNewWork(t *testing.T) {
	svc, _ := newTestService(t)
	handler := &scriptedHandler{jobType: "test.stop"}
	svc.RegisterHandler(handler)

	ids := make([]string, 0, 2)
	for range 2 {
		job, err := svc.SubmitJob(context.Background(), "test.stop", `{}`)
		if err != nil {
			t.Fatalf("SubmitJob failed: %v", err)
		}
		ids = append(ids, job.ID)
	}

	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	for _, id := range ids {
		latest := mustGetJob(t, svc, id)
		if latest.Status != StatusFailed {
			t.Fatalf("expected queued job %s to be FAILED at shutdown, got %s", id, latest.Status)
		}
		if latest.ErrorCode != CodeJobShutdown {
			t.Fatalf("expected code %s on %s, got %q", CodeJobShutdown, id, latest.ErrorCode)
		}
	}
	if _, aborts := handler.counts(); aborts != len(ids) {
		t.Fatalf("expected the abort hook for each queued job, got %d", aborts)
	}

	_, err := svc.SubmitJob(context.Background(), "test.stop", `{}`)
	if err == nil {
		t.Fatalf("expected submissions to be rejected after Stop")
	}
	assertCode(t, err, CodeJobServiceStopped)
}

func TestQueueOverflowRejectsSubmission(t *testing.T) {
	svc, _ := newTestService(t)
	handler := &scriptedHandler{jobType: "test.overflow"}
	svc.RegisterHandler(handler)

	for i := range defaultQueueDepth {
		if _, err := svc.SubmitJob(context.Background(), "test.overflow", `{}`); err != nil {
			t.Fatalf("SubmitJob %d failed: %v", i, err)
		}
	}

	overflowJob, err := svc.SubmitJob(context.Background(), "test.overflow", `{}`)
	if err == nil {
		t.Fatalf("expected the %dth submission to be rejected", defaultQueueDepth+1)
	}
	if overflowJob != nil {
		t.Fatalf("expected no job to be returned on overflow")
	}
	assertCode(t, err, CodeJobQueueFull)

	jobs, total, listErr := svc.ListJobs(
		context.Background(),
		"test.overflow",
		100,
		0,
	)
	if listErr != nil {
		t.Fatalf("ListJobs failed: %v", listErr)
	}
	if total != int64(defaultQueueDepth+1) {
		t.Fatalf("expected %d rows, got %d", defaultQueueDepth+1, total)
	}
	var failed int
	for _, job := range jobs {
		if job.Status == StatusFailed {
			failed++
			if job.ErrorCode != CodeJobQueueFull {
				t.Fatalf("expected overflow row to carry %s, got %q", CodeJobQueueFull, job.ErrorCode)
			}
		}
	}
	if failed != 1 {
		t.Fatalf("expected exactly one failed overflow row, got %d", failed)
	}
}

func TestListJobsPagesAndFilters(t *testing.T) {
	svc, store := newTestService(t)
	base := time.Now().UTC().Add(-time.Hour)

	// Five "a" jobs and two "b" jobs, oldest first.
	for i := range 5 {
		job := &Job{
			ID:        fmt.Sprintf("job-a-%d", i),
			Type:      "test.a",
			Status:    StatusFailed,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}
	for i := range 2 {
		job := &Job{
			ID:        fmt.Sprintf("job-b-%d", i),
			Type:      "test.b",
			Status:    StatusFailed,
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		}
		if err := store.CreateJob(context.Background(), job); err != nil {
			t.Fatalf("CreateJob failed: %v", err)
		}
	}

	page2, total, err := svc.ListJobs(
		context.Background(),
		"test.a",
		2,
		2,
	)
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if total != 5 {
		t.Fatalf("expected total 5, got %d", total)
	}
	if len(page2) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(page2))
	}
	// Newest first: a-4, a-3, a-2, a-1, a-0. Offset 2 starts at a-2.
	if page2[0].ID != "job-a-2" || page2[1].ID != "job-a-1" {
		t.Fatalf("unexpected page 2 order: %s, %s", page2[0].ID, page2[1].ID)
	}

	all, total, err := svc.ListJobs(
		context.Background(),
		"",
		100,
		0,
	)
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if total != 7 || len(all) != 7 {
		t.Fatalf("expected 7 jobs, got total=%d len=%d", total, len(all))
	}

	// A zero window must fall back to the shared default, not to "no LIMIT".
	defaulted, _, err := svc.ListJobs(
		context.Background(),
		"",
		0,
		0,
	)
	if err != nil {
		t.Fatalf("ListJobs failed: %v", err)
	}
	if len(defaulted) != 7 {
		t.Fatalf("expected the default page size to return 7, got %d", len(defaulted))
	}
}

type jobEnvelope struct {
	Code    string      `json:"code"`
	Message string      `json:"message"`
	Data    JobResponse `json:"data"`
}

type jobListEnvelope struct {
	Code string                              `json:"code"`
	Data httputil.PaginatedData[JobResponse] `json:"data"`
}

func newTestRouter(t *testing.T) (*gin.Engine, *Store) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	svc, store := newTestService(t)
	handler := NewHandler(svc)
	router := gin.New()
	handler.RegisterRoutes(router.Group("/api/v1"))
	return router, store
}

func TestHandlerEndpoints(t *testing.T) {
	router, store := newTestRouter(t)

	createdAt := time.Now().UTC()
	pending := &Job{
		ID:           "job-http-1",
		Type:         "test.sample",
		Status:       StatusPending,
		MetadataJSON: `{"jobDir":"/data/tmp/secret"}`,
		CreatedAt:    createdAt,
	}
	if err := store.CreateJob(context.Background(), pending); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	// List
	wList := httptest.NewRecorder()
	router.ServeHTTP(wList, httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs?page=1&pageSize=10", nil))
	if wList.Code != http.StatusOK {
		t.Fatalf("expected 200 from List, got %d: %s", wList.Code, wList.Body.String())
	}
	var list jobListEnvelope
	if err := json.Unmarshal(wList.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if list.Data.Total != 1 || len(list.Data.Items) != 1 {
		t.Fatalf("unexpected list payload: %+v", list.Data)
	}
	if list.Data.Items[0].CreatedAt.Location() != time.UTC {
		t.Fatalf("createdAt is not UTC: %v", list.Data.Items[0].CreatedAt.Location())
	}
	// The staging metadata must never reach the client.
	if strings.Contains(wList.Body.String(), "jobDir") || strings.Contains(wList.Body.String(), "metadata") {
		t.Fatalf("list response leaked job metadata: %s", wList.Body.String())
	}

	// Get by id
	wGet := httptest.NewRecorder()
	router.ServeHTTP(wGet, httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs/job-http-1", nil))
	if wGet.Code != http.StatusOK {
		t.Fatalf("expected 200 from Get, got %d", wGet.Code)
	}

	// Get unknown id -> localized 404
	reqMissing := httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs/nope", nil)
	reqMissing.Header.Set("Accept-Language", "zh-Hans")
	wMissing := httptest.NewRecorder()
	router.ServeHTTP(wMissing, reqMissing)
	if wMissing.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", wMissing.Code)
	}
	var missing jobEnvelope
	if err := json.Unmarshal(wMissing.Body.Bytes(), &missing); err != nil {
		t.Fatalf("decode missing: %v", err)
	}
	if missing.Code != CodeJobNotFound {
		t.Fatalf("expected %s, got %s", CodeJobNotFound, missing.Code)
	}
	if missing.Message != "后台任务不存在" {
		t.Fatalf("expected the catalog message, got %q", missing.Message)
	}

	// Cancel an active job
	wCancel := httptest.NewRecorder()
	router.ServeHTTP(wCancel, httptest.NewRequest(http.MethodPost, "/api/v1/system/jobs/job-http-1/cancel", nil))
	if wCancel.Code != http.StatusOK {
		t.Fatalf("expected 200 from Cancel, got %d: %s", wCancel.Code, wCancel.Body.String())
	}

	// Cancel again -> 409
	wAgain := httptest.NewRecorder()
	router.ServeHTTP(wAgain, httptest.NewRequest(http.MethodPost, "/api/v1/system/jobs/job-http-1/cancel", nil))
	if wAgain.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a finished job, got %d", wAgain.Code)
	}
	var conflict jobEnvelope
	if err := json.Unmarshal(wAgain.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict: %v", err)
	}
	if conflict.Code != CodeJobNotCancellable {
		t.Fatalf("expected %s, got %s", CodeJobNotCancellable, conflict.Code)
	}
}

func TestHandlerLocalizesPersistedErrorCode(t *testing.T) {
	router, store := newTestRouter(t)

	failed := &Job{
		ID:        "job-failed-1",
		Type:      "test.sample",
		Status:    StatusFailed,
		ErrorCode: CodeJobPanicked,
	}
	if err := store.CreateJob(context.Background(), failed); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/jobs/job-failed-1", nil)
	req.Header.Set("Accept-Language", "zh-Hant")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var env jobEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Data.ErrorCode != CodeJobPanicked {
		t.Fatalf("expected errorCode %s, got %s", CodeJobPanicked, env.Data.ErrorCode)
	}
	if env.Data.ErrorMessage != "任務因內部錯誤中止" {
		t.Fatalf("expected the Traditional Chinese catalog message, got %q", env.Data.ErrorMessage)
	}
}

func TestHandlerCancelAudited(t *testing.T) {
	gin.SetMode(gin.TestMode)

	dbStore := database.New(t.TempDir(), zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() {
		if err := dbStore.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})

	auditSvc := audit.NewService(audit.NewStore(dbStore), zap.NewNop(), audit.Config{
		BufferSize: 100, BatchSize: 10, FlushInterval: 5 * time.Millisecond, MaxEntries: 1000,
	})
	if err := auditSvc.Start(context.Background()); err != nil {
		t.Fatalf("start audit service: %v", err)
	}
	t.Cleanup(func() {
		if err := auditSvc.Stop(context.Background()); err != nil {
			t.Errorf("stop audit service: %v", err)
		}
	})

	store := NewStore(dbStore)
	svc := NewService(store, zap.NewNop())
	job := &Job{ID: "job-audit-1", Type: "test.sample", Status: StatusPending}
	if err := store.CreateJob(context.Background(), job); err != nil {
		t.Fatalf("CreateJob failed: %v", err)
	}

	router := gin.New()
	api := router.Group("/api/v1")
	api.Use(audithttp.Middleware(auditSvc))
	NewHandler(svc).RegisterRoutes(api)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/system/jobs/job-audit-1/cancel", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Drain the asynchronous queue before asserting; otherwise "no record" cannot
	// be distinguished from "not flushed yet".
	stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := auditSvc.Stop(stopCtx); err != nil {
		t.Fatalf("drain audit service: %v", err)
	}
	entries, _, err := auditSvc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit entries: %v", err)
	}

	var found bool
	for _, entry := range entries {
		if entry.Action != ActionCancelJob {
			continue
		}
		found = true
		if entry.Status != audit.StatusSuccess {
			t.Fatalf("expected success status, got %s", entry.Status)
		}
		if entry.Target != "job:job-audit-1" {
			t.Fatalf("unexpected audit target: %s", entry.Target)
		}
	}
	if !found {
		t.Fatalf("expected an audit record with action %s, got %+v", ActionCancelJob, entries)
	}
}
