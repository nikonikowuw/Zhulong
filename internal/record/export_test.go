package record

import (
	"time"

	"github.com/nikonikowuw/Zhulong/internal/engine"
)

// Test helpers exposing internal record types for package record_test.
//
// 这些镜像只存在于测试构建里：Go 的 external test package（record_test）无法访问
// 未导出字段，而构造抓拍任务需要未导出的 snapshotJob。生产文件不承担测试脚手架。

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

func (t *TargetTrackerForTest) ResolveFrame(
	b BucketKeyForTest,
	obs []ObservationForTest,
	at time.Time,
	ttl time.Duration,
) []int64 {
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

// SnapshotOutputForTest 是 snapshotOutput 的对外镜像。
type SnapshotOutputForTest struct {
	BoxIndex  int
	RelPath   string
	Protected bool
}

// NewSnapshotJobForTest 构造一个等价于内部 snapshotJob 的任务。
func NewSnapshotJobForTest(cameraID string, boxes []engine.SnapshotBox, outputs []SnapshotOutputForTest) snapshotJob {
	return NewSnapshotJobWithPTSForTest(cameraID, 0, boxes, outputs)
}

// NewSnapshotJobWithPTSForTest 构造携带指定 PTS 的测试抓拍任务。
func NewSnapshotJobWithPTSForTest(
	cameraID string,
	pts int64,
	boxes []engine.SnapshotBox,
	outputs []SnapshotOutputForTest,
) snapshotJob {
	converted := make([]snapshotOutput, 0, len(outputs))
	for _, out := range outputs {
		converted = append(converted, snapshotOutput{
			boxIndex:  out.BoxIndex,
			relPath:   out.RelPath,
			protected: out.Protected,
		})
	}
	return snapshotJob{cameraID: cameraID, pts: pts, boxes: boxes, outputs: converted}
}

var (
	RecognitionCooldownKeyForTest      = recognitionCooldownKey
	RecognitionCooldownKeysForTest     = recognitionCooldownKeys
	PersonCaptureCooldownKeyForTest    = personCaptureCooldownKey
	FaceCaptureCooldownKeyForTest      = faceCaptureCooldownKey
	VehicleCaptureCooldownKeyForTest   = vehicleCaptureCooldownKey
	VehicleCaptureCooldownKeysForTest  = vehicleCaptureCooldownKeys
	DetectionCaptureCooldownKeyForTest = detectionCaptureCooldownKey
	AlarmCooldownKeyForTest            = alarmCooldownKey
	SnapshotRelDirForTest              = snapshotRelDir
)
