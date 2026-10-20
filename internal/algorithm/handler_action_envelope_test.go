package algorithm_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/nikonikowuw/Zhulong/internal/algorithm"
	"github.com/nikonikowuw/Zhulong/internal/database"
	"go.uber.org/zap"
)

// TestHandlerEnableDisableActionEnvelope pins the algorithm action endpoints to
// a bare `{"status": "<STATUS>"}` payload instead of a full algorithm entity.
//
// The web client parses this payload with a dedicated
// `algorithmActionResponseSchema` (web/src/features/algorithms/data/schema.ts).
// When the backend used to be parsed with the full `algorithmSchema`, the
// missing fields raised a ZodError that is neither an AxiosError nor carries a
// server message, so the global MutationCache fallback surfaced
// "Something went wrong!" on every click of the scheduling switch and the
// mutation never reached onSuccess.
//
// If this payload is ever widened or narrowed, update the frontend contract and
// its tests in the same change.
func TestHandlerEnableDisableActionEnvelope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dataDir := t.TempDir()

	dbStore := database.New(dataDir, zap.NewNop())
	if err := dbStore.OpenAndMigrate(context.Background()); err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	t.Cleanup(func() { _ = dbStore.Close() })

	// A runner stub that ignores the --plugin/--broker arguments and stays alive
	// until it is signalled keeps the supervised status deterministic.
	t.Setenv("ZHULONG_ALGO_RUNNER", writeIdleRunner(t))

	comp := algorithm.NewAlgorithmComponents(dbStore, dataDir, "cpu", zap.NewNop())
	t.Cleanup(func() { comp.Supervisor.StopAll(time.Second) })

	seed := &algorithm.Algorithm{
		ID:           "action_contract",
		Name:         "action_contract",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "cpu",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusDisabled,
	}
	if err := comp.Store.CreateAlgorithm(context.Background(), seed); err != nil {
		t.Fatalf("seed algorithm: %v", err)
	}

	// Reset only succeeds on an algorithm the backend already records as
	// CRASH_LOOP. Seeding that state directly also covers supervisor path B (no
	// in-memory watcher, e.g. after a host restart).
	crashLooped := &algorithm.Algorithm{
		ID:           "action_contract_crash_loop",
		Name:         "action_contract_crash_loop",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "cpu",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusCrashLoop,
	}
	if err := comp.Store.CreateAlgorithm(context.Background(), crashLooped); err != nil {
		t.Fatalf("seed crash-looped algorithm: %v", err)
	}

	// ACTIVE in the DB with no live watcher: reset must be rejected with 409,
	// not silently reported as success.
	activeNoWatcher := &algorithm.Algorithm{
		ID:           "action_contract_active",
		Name:         "action_contract_active",
		Version:      "1.0.0",
		TaskType:     algorithm.TaskTypeDetection,
		Category:     algorithm.CategorySafety,
		TargetArch:   "x86_64",
		TargetDevice: "cpu",
		ComputeCost:  10,
		MemoryMB:     64,
		Status:       algorithm.StatusActive,
	}
	if err := comp.Store.CreateAlgorithm(context.Background(), activeNoWatcher); err != nil {
		t.Fatalf("seed active algorithm: %v", err)
	}

	router := gin.New()
	comp.Handler.RegisterRoutes(router.Group("/api/v1"))

	postAction := func(id, action string) map[string]any {
		t.Helper()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/"+id+"/"+action, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("%s %s: expected 200 OK, got %d body=%s", id, action, w.Code, w.Body.String())
		}

		var envelope struct {
			Code string         `json:"code"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("%s %s: unmarshal envelope: %v", id, action, err)
		}
		if envelope.Code != "OK" {
			t.Fatalf("%s %s: expected envelope code OK, got %q", id, action, envelope.Code)
		}
		return envelope.Data
	}

	if data := postAction("action_contract", "enable"); len(data) != 1 || data["status"] != algorithm.StatusActive {
		t.Fatalf(`enable payload drifted from {"status":"ACTIVE"}: %#v`, data)
	}
	if data := postAction("action_contract", "disable"); len(data) != 1 || data["status"] != algorithm.StatusDisabled {
		t.Fatalf(`disable payload drifted from {"status":"DISABLED"}: %#v`, data)
	}

	// Reset is an unlock, not a restart: the payload must report DISABLED.
	if data := postAction("action_contract_crash_loop", "reset"); len(data) != 1 || data["status"] != algorithm.StatusDisabled {
		t.Fatalf(`reset payload drifted from {"status":"DISABLED"}: %#v`, data)
	}

	// Reset is idempotent once the algorithm is DISABLED, so the dialog can
	// safely offer a retry after a transient failure.
	if data := postAction("action_contract_crash_loop", "reset"); len(data) != 1 || data["status"] != algorithm.StatusDisabled {
		t.Fatalf(`repeated reset payload drifted from {"status":"DISABLED"}: %#v`, data)
	}
	if data := postAction("action_contract", "reset"); len(data) != 1 || data["status"] != algorithm.StatusDisabled {
		t.Fatalf(`reset on a DISABLED algorithm must stay idempotent: %#v`, data)
	}

	// Resetting an algorithm that is neither CRASH_LOOP nor DISABLED must be
	// rejected with the stable machine code the dialog surfaces to the operator.
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/algorithms/action_contract_active/reset", nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for reset outside CRASH_LOOP, got %d body=%s", w.Code, w.Body.String())
	}
	var errEnvelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &errEnvelope); err != nil {
		t.Fatalf("unmarshal error envelope: %v", err)
	}
	if errEnvelope.Code != "ALGORITHM_NOT_IN_CRASH_LOOP" {
		t.Fatalf("expected code ALGORITHM_NOT_IN_CRASH_LOOP, got %q", errEnvelope.Code)
	}
}
