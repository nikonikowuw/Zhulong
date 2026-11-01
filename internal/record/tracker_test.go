package record_test

import (
	"sync"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/record"
)

func TestTargetTracker_LifecycleAndMatching(t *testing.T) {
	tr := record.NewTargetTrackerForTest()
	ttl := 6 * time.Second
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	b := record.NewBucketKeyForTest("cam_1", "algo_1", "person")

	// 1. Initial observation: assigns new ID = 1
	obs1 := []record.ObservationForTest{
		{Box: [4]float64{0.100, 0.100, 0.300, 0.500}, Score: 0.9},
	}
	ids1 := tr.ResolveFrame(b, obs1, base, ttl)
	if len(ids1) != 1 || ids1[0] != 1 {
		t.Fatalf("expected initial ID 1, got %v", ids1)
	}

	// 2. Jitter within ±0.002: should keep same ID 1
	obsJitter := []record.ObservationForTest{
		{Box: [4]float64{0.102, 0.098, 0.301, 0.502}, Score: 0.91},
	}
	idsJitter := tr.ResolveFrame(b, obsJitter, base.Add(100*time.Millisecond), ttl)
	if len(idsJitter) != 1 || idsJitter[0] != 1 {
		t.Fatalf("expected jitter to reuse ID 1, got %v", idsJitter)
	}

	// 3. Fast motion with low IoU (< 0.2) but close center distance: should keep same ID 1
	// Box shifted horizontally: center moves slightly, IoU drops to ~0.15
	obsFast := []record.ObservationForTest{
		{Box: [4]float64{0.250, 0.100, 0.450, 0.500}, Score: 0.88},
	}
	idsFast := tr.ResolveFrame(b, obsFast, base.Add(300*time.Millisecond), ttl)
	if len(idsFast) != 1 || idsFast[0] != 1 {
		t.Fatalf("expected fast move to reuse ID 1 via center distance, got %v", idsFast)
	}

	// 4. Two targets crossing: each maintains its own ID and does not merge
	obsCross := []record.ObservationForTest{
		{Box: [4]float64{0.255, 0.100, 0.455, 0.500}, Score: 0.88}, // near ID 1
		{Box: [4]float64{0.600, 0.100, 0.800, 0.500}, Score: 0.85}, // new target
	}
	idsCross := tr.ResolveFrame(b, obsCross, base.Add(500*time.Millisecond), ttl)
	if len(idsCross) != 2 || idsCross[0] != 1 || idsCross[1] != 2 {
		t.Fatalf("expected IDs [1, 2], got %v", idsCross)
	}

	// 5. Short detection gap (< TTL): target 1 still recognized after 2s absence
	obsReappear := []record.ObservationForTest{
		{Box: [4]float64{0.260, 0.100, 0.460, 0.500}, Score: 0.87},
	}
	idsReappear := tr.ResolveFrame(b, obsReappear, base.Add(2500*time.Millisecond), ttl)
	if len(idsReappear) != 1 || idsReappear[0] != 1 {
		t.Fatalf("expected target 1 to persist after 2s gap, got %v", idsReappear)
	}

	// 6. Target expired after TTL: reappearance allocates new ID 3
	obsExpired := []record.ObservationForTest{
		{Box: [4]float64{0.260, 0.100, 0.460, 0.500}, Score: 0.87},
	}
	idsExpired := tr.ResolveFrame(b, obsExpired, base.Add(10*time.Second), ttl)
	if len(idsExpired) != 1 || idsExpired[0] != 3 {
		t.Fatalf("expected expired target to allocate new ID 3, got %v", idsExpired)
	}
}

func TestTargetTracker_BucketIsolation(t *testing.T) {
	tr := record.NewTargetTrackerForTest()
	ttl := 6 * time.Second
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

	bCam1 := record.NewBucketKeyForTest("cam_1", "algo_1", "person")
	bCam2 := record.NewBucketKeyForTest("cam_2", "algo_1", "person")
	bAlgo2 := record.NewBucketKeyForTest("cam_1", "algo_2", "person")
	bVehicle := record.NewBucketKeyForTest("cam_1", "algo_1", "vehicle")

	box := [4]float64{0.2, 0.2, 0.4, 0.4}
	obs := []record.ObservationForTest{{Box: box, Score: 0.9}}

	id1 := tr.ResolveFrame(bCam1, obs, base, ttl)[0]
	id2 := tr.ResolveFrame(bCam2, obs, base, ttl)[0]
	id3 := tr.ResolveFrame(bAlgo2, obs, base, ttl)[0]
	id4 := tr.ResolveFrame(bVehicle, obs, base, ttl)[0]

	// IDs are monotonically assigned across buckets, but targets in different buckets must NOT connect
	if id1 != 1 || id2 != 2 || id3 != 3 || id4 != 4 {
		t.Fatalf("expected distinct IDs [1, 2, 3, 4], got [%d, %d, %d, %d]", id1, id2, id3, id4)
	}

	// Feeding same box to cam_1 again must match id1, not other buckets
	matchCam1 := tr.ResolveFrame(bCam1, obs, base.Add(100*time.Millisecond), ttl)[0]
	if matchCam1 != 1 {
		t.Fatalf("expected match on bucket cam_1 with ID 1, got %d", matchCam1)
	}
}

func TestTargetTracker_MonotonicTimestampGuard(t *testing.T) {
	tr := record.NewTargetTrackerForTest()
	ttl := 3 * time.Second
	base := time.Date(2026, 11, 1, 12, 0, 5, 0, time.UTC)
	b := record.NewBucketKeyForTest("cam_1", "algo_1", "person")

	box := [4]float64{0.1, 0.1, 0.3, 0.3}
	// Initial at 12:00:05
	id1 := tr.ResolveFrame(b, []record.ObservationForTest{{Box: box}}, base, ttl)[0]
	if id1 != 1 {
		t.Fatalf("expected ID 1, got %d", id1)
	}

	// Earlier frame arrives at 12:00:01 (timestamp regress, e.g. out of order)
	// Monotonic guard skips aging, should not delete existing track, and matching should preserve ID 1
	earlier := base.Add(-4 * time.Second)
	idEarlier := tr.ResolveFrame(b, []record.ObservationForTest{{Box: box}}, earlier, ttl)[0]
	if idEarlier != 1 {
		t.Fatalf("expected regression frame to still match ID 1, got %d", idEarlier)
	}

	// Next normal frame at 12:00:06 (only 1s after base):
	// If lastSeen was erroneously regressed to 12:00:01, 12:00:06 - 12:00:01 = 5s > 3s ttl, which would cause false expiration!
	// With monotonic lastSeen guard, track 1 must NOT be expired.
	normal := base.Add(1 * time.Second)
	idNormal := tr.ResolveFrame(b, []record.ObservationForTest{{Box: box}}, normal, ttl)[0]
	if idNormal != 1 {
		t.Fatalf("expected track 1 to survive and not be falsely expired, got %d", idNormal)
	}
}

func TestTargetTracker_EmptyAndConcurrentAccess(t *testing.T) {
	tr := record.NewTargetTrackerForTest()
	ttl := 3 * time.Second
	base := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	b := record.NewBucketKeyForTest("cam_1", "algo_1", "person")

	// Empty observations slice returns nil without panic
	ids := tr.ResolveFrame(b, nil, base, ttl)
	if len(ids) != 0 {
		t.Fatalf("expected empty ids, got %v", ids)
	}

	// Concurrent calls across goroutines must be race-free
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(camIdx int) {
			defer wg.Done()
			bucket := record.NewBucketKeyForTest("cam_conc", "algo", "person")
			for j := 0; j < 30; j++ {
				obs := []record.ObservationForTest{
					{Box: [4]float64{0.1, 0.1, 0.3, 0.3}},
				}
				_ = tr.ResolveFrame(bucket, obs, base.Add(time.Duration(j)*100*time.Millisecond), ttl)
			}
		}(i)
	}
	wg.Wait()

	stats := tr.Stats()
	if stats.TotalQueries == 0 {
		t.Fatalf("expected positive TotalQueries")
	}
}
