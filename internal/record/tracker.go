package record

import (
	"math"
	"sync"
	"time"
)

// bucketKey defines an isolated identity namespace per camera, algorithm, and target kind.
type bucketKey struct {
	camera    string
	algorithm string
	kind      string
}

// observation represents a single bounding box observation in a frame.
type observation struct {
	box   [4]float64
	score float64
}

// activeTrack records an active tracked target in memory.
type activeTrack struct {
	id       int64
	box      [4]float64
	lastSeen time.Time
}

// trackerConfig configures matching thresholds and garbage collection for targetTracker.
type trackerConfig struct {
	matchIoU           float64
	maxCenterDistRatio float64
	minHeightRatio     float64
	maxHeightRatio     float64
	gcEntryThreshold   int
	gcInterval         time.Duration
}

func defaultTrackerConfig() trackerConfig {
	return trackerConfig{
		matchIoU:           0.2,
		maxCenterDistRatio: 0.5,
		minHeightRatio:     0.5,
		maxHeightRatio:     2.0,
		gcEntryThreshold:   4096,
		gcInterval:         30 * time.Second,
	}
}

// TrackerStats provides read-only operational metrics for debugging.
type TrackerStats struct {
	ActiveTracks   int
	TotalAllocated int64
	Matches        int64
	TotalQueries   int64
}

// targetTracker resolves cross-frame target identities in the host process.
type targetTracker struct {
	mu      sync.Mutex
	cfg     trackerConfig
	buckets map[bucketKey][]*activeTrack
	nextID  int64
	lastGC  time.Time
	matches int64
	queries int64
}

func newTargetTracker(cfg trackerConfig) *targetTracker {
	return &targetTracker{
		cfg:     cfg,
		buckets: make(map[bucketKey][]*activeTrack),
		lastGC:  time.Now(),
	}
}

// resolveFrame matches bounding box observations to active tracks in bucket b.
// It returns an identity slice with the exact same length and order as obs.
func (t *targetTracker) resolveFrame(b bucketKey, obs []observation, at time.Time, ttl time.Duration) []int64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	tracks := t.buckets[b]

	// 1. Monotonic guard: if at is earlier than any track's lastSeen, skip aging to avoid false deletion.
	skipAging := false
	for _, tr := range tracks {
		if at.Before(tr.lastSeen) {
			skipAging = true
			break
		}
	}

	// 2. Aging: remove tracks inactive for longer than ttl (unless aging skipped).
	if !skipAging && ttl > 0 {
		cutoff := at.Add(-ttl)
		valid := tracks[:0]
		for _, tr := range tracks {
			if !tr.lastSeen.Before(cutoff) {
				valid = append(valid, tr)
			}
		}
		tracks = valid
	}

	if len(obs) == 0 {
		if len(tracks) == 0 {
			delete(t.buckets, b)
		} else {
			t.buckets[b] = tracks
		}
		return nil
	}

	t.queries += int64(len(obs))

	used := make([]bool, len(tracks))
	ids := make([]int64, len(obs))

	// 3. Greedy 1-to-1 matching per observation.
	for i, o := range obs {
		bestIdx := -1
		bestIoU := -1.0

		// Step A: prioritize IoU match.
		for j, tr := range tracks {
			if used[j] {
				continue
			}
			iou := boxIoU(o.box, tr.box)
			if iou > bestIoU {
				bestIoU = iou
				bestIdx = j
			}
		}

		matched := false
		if bestIdx >= 0 && bestIoU >= t.cfg.matchIoU {
			matched = true
		} else {
			// Step B: fallback to center-distance ratio and height ratio.
			bestDistRatio := math.MaxFloat64
			bestFallbackIdx := -1

			for j, tr := range tracks {
				if used[j] {
					continue
				}
				distRatio, hRatio, ok := boxDistanceAndHeightRatio(o.box, tr.box)
				if ok && distRatio <= t.cfg.maxCenterDistRatio &&
					hRatio >= t.cfg.minHeightRatio && hRatio <= t.cfg.maxHeightRatio {
					if distRatio < bestDistRatio {
						bestDistRatio = distRatio
						bestFallbackIdx = j
					}
				}
			}

			if bestFallbackIdx >= 0 {
				bestIdx = bestFallbackIdx
				matched = true
			}
		}

		if matched {
			tr := tracks[bestIdx]
			tr.box = o.box
			// Monotonic guard for lastSeen update: never regress timestamp.
			if at.After(tr.lastSeen) {
				tr.lastSeen = at
			}
			used[bestIdx] = true
			ids[i] = tr.id
			t.matches++
		} else {
			// Unmatched: allocate new monotonic identity.
			t.nextID++
			newID := t.nextID
			newTr := &activeTrack{
				id:       newID,
				box:      o.box,
				lastSeen: at,
			}
			tracks = append(tracks, newTr)
			used = append(used, true)
			ids[i] = newID
		}
	}

	t.buckets[b] = tracks
	t.lazyGCLocked(at, ttl)
	return ids
}

// Stats returns read-only tracker statistics.
func (t *targetTracker) Stats() TrackerStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	active := 0
	for _, trs := range t.buckets {
		active += len(trs)
	}

	return TrackerStats{
		ActiveTracks:   active,
		TotalAllocated: t.nextID,
		Matches:        t.matches,
		TotalQueries:   t.queries,
	}
}

func (t *targetTracker) lazyGCLocked(now time.Time, ttl time.Duration) {
	totalEntries := 0
	for _, trs := range t.buckets {
		totalEntries += len(trs)
	}

	// Trigger GC if entries exceed threshold and throttled interval elapsed,
	// or periodically (every 10 minutes) to reap empty buckets.
	shouldGC := false
	if totalEntries > t.cfg.gcEntryThreshold && now.Sub(t.lastGC) >= t.cfg.gcInterval {
		shouldGC = true
	} else if now.Sub(t.lastGC) >= 10*time.Minute {
		shouldGC = true
	}

	if !shouldGC {
		return
	}

	cutoff := now.Add(-ttl)
	for b, trs := range t.buckets {
		valid := trs[:0]
		for _, tr := range trs {
			if !tr.lastSeen.Before(cutoff) {
				valid = append(valid, tr)
			}
		}
		if len(valid) == 0 {
			delete(t.buckets, b)
		} else {
			t.buckets[b] = valid
		}
	}
	t.lastGC = now
}

func boxIoU(a, b [4]float64) float64 {
	x1 := math.Max(a[0], b[0])
	y1 := math.Max(a[1], b[1])
	x2 := math.Min(a[2], b[2])
	y2 := math.Min(a[3], b[3])

	w := math.Max(0, x2-x1)
	h := math.Max(0, y2-y1)
	inter := w * h
	if inter <= 0 {
		return 0
	}

	areaA := math.Max(0, a[2]-a[0]) * math.Max(0, a[3]-a[1])
	areaB := math.Max(0, b[2]-b[0]) * math.Max(0, b[3]-b[1])
	union := areaA + areaB - inter
	if union <= 0 {
		return 0
	}
	return inter / union
}

func boxDistanceAndHeightRatio(o, tr [4]float64) (distRatio float64, heightRatio float64, ok bool) {
	wO := math.Abs(o[2] - o[0])
	hO := math.Abs(o[3] - o[1])
	wTr := math.Abs(tr[2] - tr[0])
	hTr := math.Abs(tr[3] - tr[1])

	maxDim := math.Max(math.Max(wO, hO), math.Max(wTr, hTr))
	if maxDim <= 0 || hTr <= 0 {
		return 0, 0, false
	}

	cOx := (o[0] + o[2]) / 2
	cOy := (o[1] + o[3]) / 2
	cTrx := (tr[0] + tr[2]) / 2
	cTry := (tr[1] + tr[3]) / 2

	dist := math.Hypot(cOx-cTrx, cOy-cTry)
	return dist / maxDim, hO / hTr, true
}
