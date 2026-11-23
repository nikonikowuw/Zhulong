package algorunner

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// wireGalleryForTest installs the gallery wiring SetGallerySync installs, but
// without starting the loop at the production 30s cadence: each test drives
// either the loop itself (with a millisecond tick) or a single pass.
func wireGalleryForTest(supervisor *Supervisor, provider GallerySyncProvider, broadcaster *GalleryBroadcaster) {
	supervisor.mu.Lock()
	supervisor.gallerySyncProvider = provider
	supervisor.galleryBroadcaster = broadcaster
	supervisor.mu.Unlock()
}

// startReconcileLoopForTest runs the reconcile loop the way SetGallerySync does,
// at a test cadence. The started flag is set here so StopAll joins this loop
// exactly as it joins a production one.
func startReconcileLoopForTest(supervisor *Supervisor, interval time.Duration, liveRunners func() int) {
	supervisor.mu.Lock()
	supervisor.galleryReconcileStarted = true
	supervisor.mu.Unlock()

	go supervisor.galleryReconcileLoop(interval, liveRunners)
}

// newObservedSupervisor builds a supervisor whose logs are captured, so the
// Warn half of the contract (V6: failures visible, idle host silent) can be
// asserted instead of inferred from call counts.
func newObservedSupervisor(t *testing.T, level zapcore.Level) (*Supervisor, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(level)
	supervisor := NewSupervisor(nil, Runtime{}, DefaultRecoveryOptions(), zap.New(core))
	return supervisor, logs
}

// waitForCond polls until cond holds or the budget runs out. The reconcile loop
// runs on its own schedule, so tests wait for a condition instead of sleeping a
// fixed amount and hoping.
func waitForCond(t *testing.T, budget time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return cond()
}

// TestGalleryReconcileConverges is the discriminator for this change: with a
// constant delivered=1 the one-shot implementation reports exactly one push and
// stops, while the loop keeps ticking. The budget is the contract's 100ms, not a
// generous second — a slow cadence would let a one-shot push look convergent.
func TestGalleryReconcileConverges(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{}, zap.NewNop())
	wireGalleryForTest(supervisor, provider, broadcaster)
	startReconcileLoopForTest(supervisor, 10*time.Millisecond, func() int { return 1 })
	t.Cleanup(func() { supervisor.StopAll(time.Millisecond) })

	if !waitForCond(t, 100*time.Millisecond, func() bool { return provider.callCount() >= 3 }) {
		t.Fatalf("expected >=3 snapshot pushes within 100ms, got %d", provider.callCount())
	}
}

// TestGalleryReconcileHealsLateRunner models the production failure: the first
// pushes reach an empty broker (the runner is still loading its models) and a
// later tick finds it connected.
//
// The 50ms cadence is chosen so the delivery has to land inside 5 x interval: a
// cold-start ladder retries 500ms apart, so it physically cannot produce a
// delivery that fast — the timing itself excludes the ladder, and successAt pins
// that the loop simply kept ticking.
func TestGalleryReconcileHealsLateRunner(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}

	const interval = 50 * time.Millisecond
	var successAt atomic.Int64
	success := make(chan struct{})
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{report: func(call int, _ []byte) int {
		if call <= 3 {
			return 0
		}
		if successAt.CompareAndSwap(0, int64(call)) {
			close(success)
		}
		return 1
	}}, zap.NewNop())

	wireGalleryForTest(supervisor, provider, broadcaster)
	startReconcileLoopForTest(supervisor, interval, func() int { return 1 })
	t.Cleanup(func() { supervisor.StopAll(time.Millisecond) })

	select {
	case <-success:
	case <-time.After(5 * interval):
		t.Fatalf("no delivery within 5 x %s after %d pushes", interval, provider.callCount())
	}
	if got := successAt.Load(); got != 4 {
		t.Fatalf("expected delivery on push 4, got push %d", got)
	}
}

// TestGalleryReconcileSkipsWithoutRunners keeps an idle host cheap: with no
// runner process there is nobody to push to, so the snapshot must not even be
// loaded from the database — and the pass must stay silent, because a Warn here
// would fire every 30s on a host that has no camera configured yet.
func TestGalleryReconcileSkipsWithoutRunners(t *testing.T) {
	supervisor, logs := newObservedSupervisor(t, zapcore.WarnLevel)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{}, zap.NewNop())
	wireGalleryForTest(supervisor, provider, broadcaster)

	for i := 0; i < 3; i++ {
		supervisor.reconcileGallery(func() int { return 0 })
	}

	if got := provider.callCount(); got != 0 {
		t.Fatalf("expected no snapshot pushes without a live runner, got %d", got)
	}
	if got := logs.Len(); got != 0 {
		t.Fatalf("expected no Warn logs without a live runner, got %d: %v", got, logs.All())
	}
}

// TestGalleryReconcileWarnsWhenNoRunnerTakesSnapshot is the signal the old code
// could not produce: a runner is alive, yet not a single one accepted the
// snapshot. That is the "another algorithm's process is eating the deliveries"
// case, and it has to reach the operator without leaking gallery contents.
func TestGalleryReconcileWarnsWhenNoRunnerTakesSnapshot(t *testing.T) {
	supervisor, logs := newObservedSupervisor(t, zapcore.WarnLevel)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(2)}
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{report: func(int, []byte) int {
		return 0
	}}, zap.NewNop())
	wireGalleryForTest(supervisor, provider, broadcaster)

	supervisor.reconcileGallery(func() int { return 1 })

	entries := logs.FilterMessage("gallery reconcile reached no runner").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one warn when no runner takes the snapshot, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	for _, key := range []string{"sync_id", "items_count", "alive_runners", "generation"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("warn is missing field %q: %v", key, fields)
		}
	}
	if got, ok := fields["items_count"].(int64); !ok || got != 2 {
		t.Fatalf("warn items_count = %v, want 2", fields["items_count"])
	}
}

// TestGalleryReconcileSkipsBroadcastWhenProviderFails pins the most dangerous
// coupling in this path: a failed journal read must never become an empty
// snapshot, because broadcasting it would clear every runner's gallery.
func TestGalleryReconcileSkipsBroadcastWhenProviderFails(t *testing.T) {
	supervisor, logs := newObservedSupervisor(t, zapcore.WarnLevel)
	provider := &gallerySyncFakeProvider{err: errors.New("database is locked")}
	transport := &gallerySyncFakeBroadcaster{}
	broadcaster := NewGalleryBroadcaster(transport, zap.NewNop())
	wireGalleryForTest(supervisor, provider, broadcaster)

	supervisor.reconcileGallery(func() int { return 1 })

	if got := transport.callCount(); got != 0 {
		t.Fatalf("broadcast %d message(s) after a failed snapshot read, want 0", got)
	}
	entries := logs.FilterMessage("gallery reconcile failed").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one warn for the failed snapshot, got %d", len(entries))
	}
	if _, ok := entries[0].ContextMap()["error"]; !ok {
		t.Fatal("failure warn is missing the error field")
	}
}

// TestGalleryReconcileRepublishesAfterMutation covers the stale-snapshot hazard:
// an enrollment that lands while the shards are in flight would be rolled back
// on every runner the snapshot reaches. One republish is enough; a second would
// turn an enrollment burst into a snapshot storm.
func TestGalleryReconcileRepublishesAfterMutation(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}

	var broadcaster *GalleryBroadcaster
	broadcaster = NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{report: func(call int, _ []byte) int {
		if call != 1 {
			return 1
		}
		// A new face is enrolled while the first shard is already out: the
		// snapshot in flight no longer represents the gallery.
		err := broadcaster.BroadcastUpsert(context.Background(), GalleryUpsertPayload{
			FaceID:   "face_new",
			PersonID: "person_new",
		})
		if err != nil {
			t.Errorf("broadcast upsert from the push path: %v", err)
		}
		return 1
	}}, zap.NewNop())

	wireGalleryForTest(supervisor, provider, broadcaster)
	supervisor.reconcileGallery(func() int { return 1 })

	if got := provider.callCount(); got != 2 {
		t.Fatalf("expected exactly 2 snapshots (original + 1 follow-up), got %d", got)
	}
}

// TestGalleryReconcileStopsOnShutdown pins the lifecycle contract: StopAll closes
// stopCh and waits for the loop to converge, and nothing is pushed afterwards.
func TestGalleryReconcileStopsOnShutdown(t *testing.T) {
	supervisor := newInternalTestSupervisor(nil)
	provider := &gallerySyncFakeProvider{items: gallerySyncTestItems(1)}
	broadcaster := NewGalleryBroadcaster(&gallerySyncFakeBroadcaster{}, zap.NewNop())
	wireGalleryForTest(supervisor, provider, broadcaster)
	startReconcileLoopForTest(supervisor, 10*time.Millisecond, func() int { return 1 })

	if !waitForCond(t, time.Second, func() bool { return provider.callCount() >= 2 }) {
		t.Fatalf("reconcile loop never pushed before shutdown, got %d", provider.callCount())
	}

	supervisor.StopAll(50 * time.Millisecond)

	select {
	case <-supervisor.galleryReconcileDone:
	default:
		t.Fatal("reconcile loop still running after StopAll returned")
	}

	stalled := provider.callCount()
	time.Sleep(30 * time.Millisecond)
	if got := provider.callCount(); got != stalled {
		t.Fatalf("reconcile loop pushed %d more snapshot(s) after shutdown", got-stalled)
	}
}
