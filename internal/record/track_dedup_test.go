package record_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// AC1: 同一目标框在连续帧中微小抖动 (±0.002) 跨越旧坐标边界，在 3s 冷却窗口内只产生 1 条抓拍记录。
func TestTrackDeduplication_JitterSuppression(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// Frame 1: Initial observation
	res1 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1000,
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				BodyBox:   [4]float64{0.100, 0.200, 0.300, 0.700},
				BodyScore: 0.92,
			},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res1); err != nil {
		t.Fatalf("frame 1 failed: %v", err)
	}

	// Next 5 frames: Jitter slightly by ±0.002 (in old coordinate scheme, this changed keys and caused flooding)
	for i := 1; i <= 5; i++ {
		resJitter := res1
		resJitter.Timestamp = baseTime.Add(time.Duration(i*100) * time.Millisecond)
		resJitter.Persons = []aitypes.PersonItem{
			{
				BodyBox:   [4]float64{0.100 + float64(i)*0.001, 0.200 - float64(i)*0.001, 0.300, 0.700},
				BodyScore: 0.92,
			},
		}
		if err := svc.ProcessInferenceResult(ctx, resJitter); err != nil {
			t.Fatalf("frame %d failed: %v", i, err)
		}
	}

	caps, total, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || total != 1 || len(caps) != 1 {
		t.Fatalf("expected exactly 1 capture despite jitter, got total=%d len=%d err=%v", total, len(caps), err)
	}
}

// AC2: 目标 A 产生记录后 1s 内，不同目标 B 出现在与 A 重叠极小或不同位置，B 必定产生自己的记录（消除同位置误抑制）。
func TestTrackDeduplication_DifferentTargetsAtSameLocation(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// Target A appears at pos [0.1, 0.1, 0.3, 0.5]
	resA := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{BodyBox: [4]float64{0.1, 0.1, 0.3, 0.5}, BodyScore: 0.9},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, resA); err != nil {
		t.Fatalf("resA failed: %v", err)
	}

	// 1s later, Target B appears at completely different location [0.6, 0.1, 0.8, 0.5]
	resB := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime.Add(1 * time.Second),
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{BodyBox: [4]float64{0.6, 0.1, 0.8, 0.5}, BodyScore: 0.88},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, resB); err != nil {
		t.Fatalf("resB failed: %v", err)
	}

	caps, total, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || total != 2 || len(caps) != 2 {
		t.Fatalf("expected 2 distinct captures for targets A and B, got total=%d len=%d", total, len(caps))
	}
	if caps[0].TrackID == caps[1].TrackID {
		t.Fatalf("expected different TrackIDs for targets A and B, got %d and %d", caps[0].TrackID, caps[1].TrackID)
	}
}

// AC10: 检测路径多目标独立抓拍且后续帧未冷却目标不饥饿。
func TestTrackDeduplication_DetectionMultiTargetAndNoStarvation(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// Frame 1: 3 car detections in same frame
	res1 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Label: "car", Score: 0.95, Box: [4]float64{0.1, 0.1, 0.3, 0.3}},
			{Label: "car", Score: 0.90, Box: [4]float64{0.4, 0.1, 0.6, 0.3}},
			{Label: "car", Score: 0.85, Box: [4]float64{0.7, 0.1, 0.9, 0.3}},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res1); err != nil {
		t.Fatalf("frame 1 failed: %v", err)
	}

	// All 3 cars should be captured independently
	caps1, total1, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || total1 != 3 || len(caps1) != 3 {
		t.Fatalf("expected 3 captures in frame 1, got total=%d len=%d", total1, len(caps1))
	}

	// Frame 2 (500ms later): The 3 cars are still there (in cooldown), plus a 4th brand new car arrives!
	res2 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime.Add(500 * time.Millisecond),
		TaskType:    aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{
			{Label: "car", Score: 0.95, Box: [4]float64{0.1, 0.1, 0.3, 0.3}}, // in cooldown
			{Label: "car", Score: 0.90, Box: [4]float64{0.4, 0.1, 0.6, 0.3}}, // in cooldown
			{Label: "car", Score: 0.85, Box: [4]float64{0.7, 0.1, 0.9, 0.3}}, // in cooldown
			{Label: "car", Score: 0.80, Box: [4]float64{0.1, 0.5, 0.3, 0.7}}, // BRAND NEW CAR
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res2); err != nil {
		t.Fatalf("frame 2 failed: %v", err)
	}

	// Total captures must be 4: the 3 existing cars were suppressed by cooldown, and the new car was NOT starved!
	caps2, total2, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || total2 != 4 || len(caps2) != 4 {
		t.Fatalf("expected 4 total captures without starvation, got total=%d len=%d", total2, len(caps2))
	}
}

// AC11: 两级抑制不回退。同一已登记人员在窗口内产生两个身份仍只产出 1 条识别；同一车牌亦然。
func TestTrackDeduplication_TwoLevelCooldown(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Register dummy face for existing person_alice
	if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
		VALUES ('face_alice_01', 'person_alice', 'faces/alice.jpg', X'00', CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert dummy person_face: %v", err)
	}

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	// 1. Alice recognized at position 1
	res1 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				BodyBox: [4]float64{0.1, 0.1, 0.3, 0.5},
				Faces: []aitypes.FaceSubItem{
					{
						Box:           [4]float64{0.15, 0.15, 0.25, 0.25},
						Matched:       true,
						PersonID:      "person_alice",
						MatchedFaceID: "face_alice_01",
					},
				},
			},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res1); err != nil {
		t.Fatalf("res1 failed: %v", err)
	}

	// 2. 1s later, Alice appears at far away position [0.8, 0.8, 0.95, 0.95] (broken tracking -> allocates new track ID)
	// Because of two-level suppression (AllowAll with personKey), recognition must be suppressed!
	res2 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime.Add(1 * time.Second),
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				BodyBox: [4]float64{0.8, 0.8, 0.95, 0.95},
				Faces: []aitypes.FaceSubItem{
					{
						Box:           [4]float64{0.82, 0.82, 0.9, 0.9},
						Matched:       true,
						PersonID:      "person_alice",
						MatchedFaceID: "face_alice_01",
					},
				},
			},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, res2); err != nil {
		t.Fatalf("res2 failed: %v", err)
	}

	recs, totalRecs, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalRecs != 1 || len(recs) != 1 {
		t.Fatalf("expected exactly 1 recognition due to two-level personID cooldown, got %d", totalRecs)
	}

	// 3. Same two-level cooldown test on Vehicle plate
	resV1 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypeVehicleStructure,
		Vehicles: []aitypes.VehicleItem{
			{
				VehicleBox: [4]float64{0.1, 0.1, 0.3, 0.3},
				Plates: []aitypes.PlateSubItem{
					{PlateNumber: "粤B12345"},
				},
			},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, resV1); err != nil {
		t.Fatalf("resV1 failed: %v", err)
	}

	// Vehicle jumps to another position (new track identity), but same plate "粤B12345"
	resV2 := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime.Add(1 * time.Second),
		TaskType:    aitypes.TaskTypeVehicleStructure,
		Vehicles: []aitypes.VehicleItem{
			{
				VehicleBox: [4]float64{0.7, 0.7, 0.9, 0.9},
				Plates: []aitypes.PlateSubItem{
					{PlateNumber: "粤B12345"},
				},
			},
		},
	}
	if err := svc.ProcessInferenceResult(ctx, resV2); err != nil {
		t.Fatalf("resV2 failed: %v", err)
	}

	vehCaps, totalVehCaps, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01", TargetType: "vehicle"})
	if err != nil || totalVehCaps != 1 || len(vehCaps) != 1 {
		t.Fatalf("expected exactly 1 vehicle capture due to plate cooldown, got %d", totalVehCaps)
	}
}

// AC9: 载荷 track_id 不参与身份，宿主独占；退化项与真人体项均获得独立宿主身份；宿主身份不写回 PersonItem。
func TestTrackDeduplication_PayloadTrackIDIgnoredAndNoWriteback(t *testing.T) {
	store, _, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	faceBox := [4]float64{0.1, 0.1, 0.3, 0.3}
	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		Timestamp:   baseTime,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			// Closeup degradation item: TrackID is 0
			{
				TrackID:   0,
				BodyBox:   faceBox,
				BodyScore: 0.88,
				Faces:     []aitypes.FaceSubItem{{Box: faceBox}},
			},
			// Real person item: payload contains non-zero TrackID: 9999
			{
				TrackID:   9999,
				BodyBox:   [4]float64{0.5, 0.1, 0.7, 0.9},
				BodyScore: 0.92,
				Faces:     []aitypes.FaceSubItem{{Box: [4]float64{0.55, 0.15, 0.65, 0.3}}},
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	// 1. Verify host did NOT write back to payload struct
	if res.Persons[0].TrackID != 0 {
		t.Fatalf("host identity must NOT write back to PersonItem.TrackID, got %d", res.Persons[0].TrackID)
	}
	if res.Persons[1].TrackID != 9999 {
		t.Fatalf("payload TrackID must remain unchanged, got %d", res.Persons[1].TrackID)
	}

	// 2. Verify DB captures
	caps, total, err := svc.ListCaptures(ctx, record.CaptureFilter{CameraID: "cam_01"})
	if err != nil || total != 2 || len(caps) != 2 {
		t.Fatalf("expected 2 captures, got %d", total)
	}

	for _, c := range caps {
		if c.TrackID == 9999 {
			t.Fatalf("payload track_id 9999 must NOT be stored in DB!")
		}
		if c.TrackID <= 0 {
			t.Fatalf("host track_id must be positive, got %d", c.TrackID)
		}
	}
}

// 表驱动测试：严格断言键格式契约与 trk: 前缀。
func TestTrackDeduplication_CooldownKeyContract(t *testing.T) {
	t.Run("recognition keys", func(t *testing.T) {
		tests := []struct {
			name     string
			cam      string
			trackID  int64
			face     aitypes.FaceSubItem
			expected string
		}{
			{
				name:     "with positive track ID",
				cam:      "cam_01",
				trackID:  12,
				face:     aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, PersonID: "person_1"},
				expected: "rec:cam_01:trk:12",
			},
			{
				name:     "with positive track ID (unmatched)",
				cam:      "cam_01",
				trackID:  12,
				face:     aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}},
				expected: "rec:cam_01:trk:12:unmatched",
			},
			{
				name:     "fallback to person ID",
				cam:      "cam_01",
				trackID:  0,
				face:     aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, PersonID: "person_1"},
				expected: "rec:cam_01:person:person_1",
			},
			{
				name:     "fallback to unmatched coordinate",
				cam:      "cam_01",
				trackID:  0,
				face:     aitypes.FaceSubItem{Box: [4]float64{0.1234, 0.5678, 0.3, 0.4}},
				expected: "rec:cam_01:unmatched:0.123:0.568",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				k := record.RecognitionCooldownKeyForTest(tt.cam, tt.trackID, tt.face)
				if k != tt.expected {
					t.Errorf("got %q, want %q", k, tt.expected)
				}
			})
		}
	})

	t.Run("person capture keys", func(t *testing.T) {
		tests := []struct {
			name     string
			cam      string
			trackID  int64
			box      [4]float64
			expected string
		}{
			{
				name:     "with positive track ID",
				cam:      "cam_01",
				trackID:  7,
				box:      [4]float64{0.1, 0.2, 0.3, 0.4},
				expected: "cap:cam_01:person:trk:7",
			},
			{
				name:     "fallback to body box",
				cam:      "cam_01",
				trackID:  0,
				box:      [4]float64{0.1234, 0.5678, 0.3, 0.4},
				expected: "cap:cam_01:person:box:0.123:0.568",
			},
		}

		// 近景人脸不走这条键：它在 planPersonStructure 里被降级为孤立人脸，
		// 由 faceCaptureCooldownKey 负责（契约见 record-guidelines §1）。
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				k := record.PersonCaptureCooldownKeyForTest(tt.cam, tt.trackID, tt.box)
				if k != tt.expected {
					t.Errorf("got %q, want %q", k, tt.expected)
				}
			})
		}
	})

	t.Run("vehicle capture keys", func(t *testing.T) {
		tests := []struct {
			name         string
			cam          string
			trackID      int64
			firstPlateNo string
			box          [4]float64
			expected     string
		}{
			{
				name:         "with positive track ID",
				cam:          "cam_01",
				trackID:      42,
				firstPlateNo: "粤B12345",
				box:          [4]float64{0.1, 0.2, 0.3, 0.4},
				expected:     "cap:cam_01:vehicle:trk:42",
			},
			{
				name:         "fallback to plate number",
				cam:          "cam_01",
				trackID:      0,
				firstPlateNo: "粤B12345",
				box:          [4]float64{0.1, 0.2, 0.3, 0.4},
				expected:     "cap:cam_01:vehicle:plate:粤B12345",
			},
			{
				name:         "fallback to vehicle box",
				cam:          "cam_01",
				trackID:      0,
				firstPlateNo: "",
				box:          [4]float64{0.1234, 0.5678, 0.3, 0.4},
				expected:     "cap:cam_01:vehicle:box:0.123:0.568",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				k := record.VehicleCaptureCooldownKeyForTest(tt.cam, tt.trackID, tt.firstPlateNo, tt.box)
				if k != tt.expected {
					t.Errorf("got %q, want %q", k, tt.expected)
				}
			})
		}
	})
}

// TestTrackDeduplication_MultiKeyContract 守住两级抑制键的「无重复」不变量与升级计划契约。
//
// AllowAll 内部会去重，所以重复键不会表现为 bug，只会表现为白付一次键构造；
// 真正的契约是「回落键与人员/车牌键同源时必须只出现一次」。
func TestTrackDeduplication_MultiKeyContract(t *testing.T) {
	t.Run("recognition two-level keys", func(t *testing.T) {
		tracked := record.RecognitionCooldownKeysForTest(
			"cam_01",
			12,
			aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, PersonID: "person_1"},
		)
		wantTracked := []string{"rec:cam_01:trk:12", "rec:cam_01:person:person_1"}
		if !slices.Equal(tracked, wantTracked) {
			t.Errorf("tracked keys = %v, want %v", tracked, wantTracked)
		}

		checkMatched, stampMatched := record.RecognitionCooldownPlanForTest(
			"cam_01",
			12,
			aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, Matched: true, PersonID: "person_1"},
		)
		if !slices.Equal(checkMatched, wantTracked) {
			t.Errorf("matched plan checkKeys = %v, want %v", checkMatched, wantTracked)
		}
		wantStamp := []string{"rec:cam_01:trk:12:unmatched"}
		if !slices.Equal(stampMatched, wantStamp) {
			t.Errorf("matched plan stampOnlyKeys = %v, want %v", stampMatched, wantStamp)
		}

		checkUnmatched, stampUnmatched := record.RecognitionCooldownPlanForTest(
			"cam_01",
			12,
			aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}},
		)
		wantUnmatchedCheck := []string{"rec:cam_01:trk:12:unmatched"}
		if !slices.Equal(checkUnmatched, wantUnmatchedCheck) {
			t.Errorf("unmatched plan checkKeys = %v, want %v", checkUnmatched, wantUnmatchedCheck)
		}
		if len(stampUnmatched) != 0 {
			t.Errorf("unmatched plan stampOnlyKeys = %v, want empty", stampUnmatched)
		}

		fallback := record.RecognitionCooldownKeysForTest(
			"cam_01",
			0,
			aitypes.FaceSubItem{Box: [4]float64{0.1, 0.2, 0.3, 0.4}, PersonID: "person_1"},
		)
		wantFallback := []string{"rec:cam_01:person:person_1"}
		if !slices.Equal(fallback, wantFallback) {
			t.Errorf("fallback keys = %v, want %v (回落键不得重复)", fallback, wantFallback)
		}
	})

	t.Run("vehicle two-level keys", func(t *testing.T) {
		tracked := record.VehicleCaptureCooldownKeysForTest(
			"cam_01",
			42,
			"粤B12345",
			[4]float64{0.1, 0.2, 0.3, 0.4},
		)
		wantTracked := []string{"cap:cam_01:vehicle:trk:42", "cap:cam_01:vehicle:plate:粤B12345"}
		if !slices.Equal(tracked, wantTracked) {
			t.Errorf("tracked keys = %v, want %v", tracked, wantTracked)
		}

		fallback := record.VehicleCaptureCooldownKeysForTest(
			"cam_01",
			0,
			"粤B12345",
			[4]float64{0.1, 0.2, 0.3, 0.4},
		)
		wantFallback := []string{"cap:cam_01:vehicle:plate:粤B12345"}
		if !slices.Equal(fallback, wantFallback) {
			t.Errorf("fallback keys = %v, want %v (回落键不得重复)", fallback, wantFallback)
		}
	})

	t.Run("detection and alarm keys", func(t *testing.T) {
		if got, want := record.DetectionCaptureCooldownKeyForTest("cam_01", "person", 9), "cap:cam_01:person:trk:9"; got != want {
			t.Errorf("detection tracked key = %q, want %q", got, want)
		}
		// spec §1：无身份回落保持 cap:{camera}:{label}。
		if got, want := record.DetectionCaptureCooldownKeyForTest("cam_01", "person", 0), "cap:cam_01:person"; got != want {
			t.Errorf("detection fallback key = %q, want %q", got, want)
		}
		if got, want := record.AlarmCooldownKeyForTest("cam_01", "smoke"), "alarm:cam_01:smoke"; got != want {
			t.Errorf("alarm key = %q, want %q", got, want)
		}
	})
}

// TestTrackDeduplication_RecognitionUnmatchedToMatchedUpgrade 验证同轨迹识别升级契约：
//  1. 同一轨迹在 3s 冷却期内先产出 unmatched 陌生人，后续跨帧融合命中人员时允许升级落库一次；
//  2. 升级后同一冷却期内后续 matched 与侧脸退化 unmatched 均被抑制；
//  3. 首帧直接 matched 的轨迹，在冷却期内侧脸退化 unmatched 不会倒退补发陌生人记录；
//  4. 持续 unmatched 的轨迹，在冷却期内仅产生 1 条陌生人记录；
//  5. 新轨迹命中已在冷却期内的 person_id 被两级抑制拒绝时，不得误打戳该新轨迹的 :unmatched 键（原子无副作用）。
func TestTrackDeduplication_RecognitionUnmatchedToMatchedUpgrade(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	if err := dbStore.DB().Exec(`INSERT INTO persons (id, name, created_at, updated_at)
		VALUES ('person_bob', 'Bob', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatalf("insert person_bob: %v", err)
	}
	for _, row := range []struct {
		faceID, personID, path string
	}{
		{"face_alice_01", "person_alice", "faces/alice.jpg"},
		{"face_bob_01", "person_bob", "faces/bob.jpg"},
	} {
		if err := dbStore.DB().Exec(`INSERT INTO person_faces (id, person_id, image_path, feature_vector, created_at)
			VALUES (?, ?, ?, X'00', CURRENT_TIMESTAMP)`, row.faceID, row.personID, row.path).Error; err != nil {
			t.Fatalf("insert person_face %s: %v", row.faceID, err)
		}
	}

	svc := record.NewService(store, zap.NewNop())
	svc.SetCooldownWindow(3 * time.Second)
	baseTime := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	sendPersonFace := func(cam string, dt time.Duration, bodyBox, faceBox [4]float64, face aitypes.FaceSubItem) {
		t.Helper()
		face.Box = faceBox
		res := aitypes.InferenceResult{
			CameraID:    cam,
			AlgorithmID: "algo_01",
			PTS:         int64(dt / time.Millisecond),
			Timestamp:   baseTime.Add(dt),
			TaskType:    aitypes.TaskTypePersonStructure,
			Persons: []aitypes.PersonItem{
				{
					BodyBox:   bodyBox,
					BodyScore: 0.92,
					Faces:     []aitypes.FaceSubItem{face},
				},
			},
		}
		if err := svc.ProcessInferenceResult(ctx, res); err != nil {
			t.Fatalf("ProcessInferenceResult(%s, +%v) failed: %v", cam, dt, err)
		}
	}

	// --- 场景 A & B: cam_01 同一轨迹 unmatched -> matched 升级，随后 matched 与退化 unmatched 均被抑制 ---
	track1Body := [4]float64{0.10, 0.10, 0.30, 0.60}
	track1Face := [4]float64{0.14, 0.12, 0.26, 0.26}

	// Frame 1 (t=0ms): 远距单帧未达阈值 -> 产生 1 条陌生人记录
	sendPersonFace("cam_01", 0, track1Body, track1Face, aitypes.FaceSubItem{
		Quality:    0.68,
		Matched:    false,
		Similarity: 0.48,
	})
	// Frame 2 (t=300ms): 跨帧特征融合后突破阈值 -> 允许升级，产生 1 条已匹配记录
	sendPersonFace("cam_01", 300*time.Millisecond, track1Body, track1Face, aitypes.FaceSubItem{
		Quality:       0.75,
		Matched:       true,
		IsFused:       true,
		PersonID:      "person_alice",
		PersonName:    "Alice",
		MatchedFaceID: "face_alice_01",
		Similarity:    0.58,
	})
	// Frame 3 (t=600ms): 继续命中 Alice -> 被 trk + person 冷却抑制
	sendPersonFace("cam_01", 600*time.Millisecond, track1Body, track1Face, aitypes.FaceSubItem{
		Quality:       0.78,
		Matched:       true,
		IsFused:       true,
		PersonID:      "person_alice",
		PersonName:    "Alice",
		MatchedFaceID: "face_alice_01",
		Similarity:    0.61,
	})
	// Frame 4 (t=900ms): 侧脸质量下降退化回 unmatched -> 已被升级顺带封锁，不补发陌生人
	sendPersonFace("cam_01", 900*time.Millisecond, track1Body, track1Face, aitypes.FaceSubItem{
		Quality:    0.55,
		Matched:    false,
		Similarity: 0.39,
	})

	recsCam1, totalCam1, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalCam1 != 2 || len(recsCam1) != 2 {
		t.Fatalf("expected 2 recognitions (1 unmatched + 1 upgraded matched) on cam_01, got total=%d len=%d err=%v",
			totalCam1, len(recsCam1), err)
	}
	// ListRecognitions 按 captured_at DESC 返回：recsCam1[0] 为升级后的 matched，recsCam1[1] 为首帧 unmatched
	if !recsCam1[0].Matched || recsCam1[0].PersonID == nil || *recsCam1[0].PersonID != "person_alice" {
		t.Fatalf("expected upgraded matched recognition for person_alice, got matched=%v personID=%v",
			recsCam1[0].Matched, recsCam1[0].PersonID)
	}
	if recsCam1[1].Matched || recsCam1[1].PersonID != nil {
		t.Fatalf("expected initial unmatched recognition, got matched=%v personID=%v",
			recsCam1[1].Matched, recsCam1[1].PersonID)
	}
	if recsCam1[0].TrackID <= 0 || recsCam1[0].TrackID != recsCam1[1].TrackID {
		t.Fatalf("expected both records to share positive host TrackID, got %d and %d",
			recsCam1[0].TrackID, recsCam1[1].TrackID)
	}

	// --- 场景 E: 新轨迹撞上已在冷却期的 person_alice 被拒绝时，不得污染新轨迹的 :unmatched 键 ---
	trackNewBody := [4]float64{0.65, 0.55, 0.85, 0.95}
	trackNewFace := [4]float64{0.69, 0.58, 0.81, 0.72}
	// t=1000ms: 新轨迹误判/重现为 person_alice -> 因 rec:cam_01:person:person_alice 冷却被拒
	sendPersonFace("cam_01", 1000*time.Millisecond, trackNewBody, trackNewFace, aitypes.FaceSubItem{
		Quality:       0.72,
		Matched:       true,
		PersonID:      "person_alice",
		PersonName:    "Alice",
		MatchedFaceID: "face_alice_01",
		Similarity:    0.55,
	})
	// t=1100ms: 该新轨迹回落为 unmatched -> 因上一步被拒未打戳 :unmatched，本条陌生人记录必须正常落库
	sendPersonFace("cam_01", 1100*time.Millisecond, trackNewBody, trackNewFace, aitypes.FaceSubItem{
		Quality:    0.66,
		Matched:    false,
		Similarity: 0.41,
	})
	recsAfterReject, totalAfterReject, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_01"})
	if err != nil || totalAfterReject != 3 || len(recsAfterReject) != 3 {
		t.Fatalf("expected 3 recognitions after atomic rejection + unmatched on new track, got total=%d err=%v",
			totalAfterReject, err)
	}
	if recsAfterReject[0].Matched || recsAfterReject[0].TrackID == recsCam1[0].TrackID {
		t.Fatalf("expected new track unmatched record to survive atomic rejection, got matched=%v trackID=%d",
			recsAfterReject[0].Matched, recsAfterReject[0].TrackID)
	}

	// --- 场景 C & D: cam_02 上验证纯 matched->unmatched 不倒退，且纯 unmatched->unmatched 不刷屏 ---
	bobBody := [4]float64{0.10, 0.10, 0.30, 0.60}
	bobFace := [4]float64{0.14, 0.12, 0.26, 0.26}
	sendPersonFace("cam_02", 0, bobBody, bobFace, aitypes.FaceSubItem{
		Quality:       0.88,
		Matched:       true,
		PersonID:      "person_bob",
		PersonName:    "Bob",
		MatchedFaceID: "face_bob_01",
		Similarity:    0.79,
	})
	// 400ms 后侧脸退化为 unmatched -> 必须被首帧顺带打戳的 :unmatched 抑制
	sendPersonFace("cam_02", 400*time.Millisecond, bobBody, bobFace, aitypes.FaceSubItem{
		Quality:    0.54,
		Matched:    false,
		Similarity: 0.36,
	})

	strangerBody := [4]float64{0.60, 0.10, 0.80, 0.60}
	strangerFace := [4]float64{0.64, 0.12, 0.76, 0.26}
	sendPersonFace("cam_02", 100*time.Millisecond, strangerBody, strangerFace, aitypes.FaceSubItem{
		Quality:    0.70,
		Matched:    false,
		Similarity: 0.31,
	})
	sendPersonFace("cam_02", 500*time.Millisecond, strangerBody, strangerFace, aitypes.FaceSubItem{
		Quality:    0.72,
		Matched:    false,
		Similarity: 0.33,
	})

	recsCam2, totalCam2, err := svc.ListRecognitions(ctx, record.RecognitionFilter{CameraID: "cam_02"})
	if err != nil || totalCam2 != 2 || len(recsCam2) != 2 {
		t.Fatalf("expected 2 recognitions on cam_02 (1 Bob + 1 stranger), got total=%d len=%d err=%v",
			totalCam2, len(recsCam2), err)
	}
}
