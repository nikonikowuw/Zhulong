package record

import "time"

// Test helpers exposing internal targetTracker types for package record_test.

type ObservationForTest struct {
	Box   [4]float64
	Score float64
}

type BucketKeyForTest = bucketKey

type TargetTrackerForTest struct {
	inner *targetTracker
}

func NewTargetTrackerForTest() *TargetTrackerForTest {
	return &TargetTrackerForTest{
		inner: newTargetTracker(defaultTrackerConfig()),
	}
}

func NewBucketKeyForTest(camera, algorithm, kind string) BucketKeyForTest {
	return bucketKey{
		camera:    camera,
		algorithm: algorithm,
		kind:      kind,
	}
}

func (t *TargetTrackerForTest) ResolveFrame(b BucketKeyForTest, obs []ObservationForTest, at time.Time, ttl time.Duration) []int64 {
	internalObs := make([]observation, len(obs))
	for i, o := range obs {
		internalObs[i] = observation{
			box:   o.Box,
			score: o.Score,
		}
	}
	return t.inner.resolveFrame(b, internalObs, at, ttl)
}

func (t *TargetTrackerForTest) Stats() TrackerStats {
	return t.inner.Stats()
}

var (
	RecognitionCooldownKeyForTest    = recognitionCooldownKey
	PersonCaptureCooldownKeyForTest  = personCaptureCooldownKey
	FaceCaptureCooldownKeyForTest    = faceCaptureCooldownKey
	VehicleCaptureCooldownKeyForTest = vehicleCaptureCooldownKey
)
