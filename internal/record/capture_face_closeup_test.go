package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// TestProcessFaceCloseupCapture 钉住「纯人脸退化项」的记录语义。
//
// 背景：v1 结果结构没有「无人体」取值，近景特写人脸只能把人体框列成人脸框的别名
// （见 native/sdk/include/zhulong/plugin.h 的退化语义约定）。如果宿主照抄成 person，
// 记录列表会把一张脸显示成「人体置信度 84%」——那是假的。
//
// 约定：track_id == 0 且 body_box 与唯一人脸框逐位相等 ⇒ TargetType = "face"，
// Score 仍取 BodyScore（退化项该字段是人脸质量分）。
func TestProcessFaceCloseupCapture(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	faceBox := [4]float64{0.21, 0.16, 0.29, 0.31}

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         2000,
		Timestamp:   ts,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				// 退化项：有脸无人体，body_box 就是人脸框
				TrackID:   0,
				BodyBox:   faceBox,
				BodyScore: 0.838,
				Faces: []aitypes.FaceSubItem{
					{Box: faceBox, Quality: 0.838, Matched: false, Similarity: 0.21},
				},
			},
			{
				// 真人体项：body_box 与人脸框不同，必须仍然是 person
				TrackID:   42,
				BodyBox:   [4]float64{0.10, 0.10, 0.50, 0.95},
				BodyScore: 0.91,
				Faces: []aitypes.FaceSubItem{
					{Box: [4]float64{0.22, 0.15, 0.34, 0.28}, Quality: 0.88},
				},
			},
		},
	}

	// D3 契约：宿主关联器为两目标分配独立身份（两项均有独立有效 TrackID，载荷 42
	// 不进库），退化项根据 IsFaceCloseup() 识别为 face
	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Order("target_type").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 captures (face + person), got %d", len(captures))
	}

	// 排序后：face < person
	closeup := captures[0]
	if closeup.TargetType != "face" {
		t.Errorf("退化项应记为 TargetType 'face'，实际 %q", closeup.TargetType)
	}
	if closeup.Score != 0.838 {
		t.Errorf("退化项 Score 应沿用人脸质量分 0.838，实际 %f", closeup.Score)
	}
	if closeup.TrackID <= 0 {
		t.Errorf("退化项应由宿主分配正数 TrackID，实际 %d", closeup.TrackID)
	}

	realPerson := captures[1]
	if realPerson.TargetType != "person" {
		t.Errorf("真人体项应记为 TargetType 'person'，实际 %q", realPerson.TargetType)
	}
	if realPerson.TrackID <= 0 || realPerson.TrackID == 42 {
		t.Errorf("真人体项 TrackID 应由宿主分配且不等于载荷值 42，实际 %d", realPerson.TrackID)
	}
	if realPerson.TrackID == closeup.TrackID {
		t.Errorf("退化项与真人体项的宿主 TrackID 不应相同，实际 %d", realPerson.TrackID)
	}
}

// TestPersonItemIsFaceCloseup 覆盖判据本身：只有「无 track、单脸、框逐位相等」才算退化项。
func TestPersonItemIsFaceCloseup(t *testing.T) {
	faceBox := [4]float64{0.2, 0.2, 0.3, 0.3}

	cases := []struct {
		name string
		item aitypes.PersonItem
		want bool
	}{
		{
			name: "无人体：track 0 + 单脸 + 框逐位相等",
			item: aitypes.PersonItem{TrackID: 0, BodyBox: faceBox,
				Faces: []aitypes.FaceSubItem{{Box: faceBox}}},
			want: true,
		},
		{
			name: "有跟踪 ID：真人体",
			item: aitypes.PersonItem{TrackID: 7, BodyBox: faceBox,
				Faces: []aitypes.FaceSubItem{{Box: faceBox}}},
			want: false,
		},
		{
			name: "人体框不等于人脸框：真人体",
			item: aitypes.PersonItem{TrackID: 0, BodyBox: [4]float64{0.1, 0.1, 0.5, 0.9},
				Faces: []aitypes.FaceSubItem{{Box: faceBox}}},
			want: false,
		},
		{
			name: "multiple faces 不视为退化项（一框多脸只能来自真实人体）",
			item: aitypes.PersonItem{TrackID: 0, BodyBox: faceBox,
				Faces: []aitypes.FaceSubItem{{Box: faceBox}, {Box: faceBox}}},
			want: false,
		},
		{
			name: "没有脸：背身人体",
			item: aitypes.PersonItem{TrackID: 0, BodyBox: faceBox},
			want: false,
		},
	}

	for _, tc := range cases {
		if got := tc.item.IsFaceCloseup(); got != tc.want {
			t.Errorf("%s: IsFaceCloseup() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestProcessMultipleFaceCloseupsDistinctCooldown 验证同一帧内多张不同特写人脸不会在
// 冷却层互斥（过去按 TrackID=0 生成同一个 capKey 会导致仅存下首个特写）。
func TestProcessMultipleFaceCloseupsDistinctCooldown(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

	boxA := [4]float64{0.10, 0.10, 0.20, 0.25}
	boxB := [4]float64{0.60, 0.50, 0.75, 0.70}

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         2000,
		Timestamp:   ts,
		TaskType:    aitypes.TaskTypePersonStructure,
		Persons: []aitypes.PersonItem{
			{
				TrackID:   0,
				BodyBox:   boxA,
				BodyScore: 0.85,
				Faces:     []aitypes.FaceSubItem{{Box: boxA, Quality: 0.85}},
			},
			{
				TrackID:   0,
				BodyBox:   boxB,
				BodyScore: 0.89,
				Faces:     []aitypes.FaceSubItem{{Box: boxB, Quality: 0.89}},
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Where("target_type = ?", "face").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 face captures, got %d", len(captures))
	}
}

// TestProcessSceneStructureDecoupledObjects 验证 SCENE_STRUCTURE v2 原生平级对象的入库与跟踪契约：
// 1. 真人体落 captures (target_type="person", body_score)
// 2. 孤立人脸落 captures (target_type="face", face quality, 独立 track_id)
// 3. 关联人脸落 recognitions，继承父人体的 track_id 与 body_box
func TestProcessSceneStructureDecoupledObjects(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)

	res := aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         1000,
		Timestamp:   ts,
		TaskType:    aitypes.TaskTypeSceneStructure,
		Persons: []aitypes.PersonItem{
			{
				TrackID:   0,
				BodyBox:   [4]float64{0.10, 0.10, 0.50, 0.90},
				BodyScore: 0.93,
			},
		},
		Faces: []aitypes.FaceStructureItem{
			{
				// 关联到 Person 0
				Box:           [4]float64{0.20, 0.15, 0.35, 0.30},
				Quality:       0.95,
				PersonIndex:   0,
				Matched:       true,
				PersonID:      "emp_001",
				PersonName:    "Alice",
				MatchedFaceID: "face_alice",
				Similarity:    0.91,
			},
			{
				// 孤立人脸特写
				Box:           [4]float64{0.65, 0.20, 0.85, 0.40},
				Quality:       0.89,
				PersonIndex:   -1,
				Matched:       false,
				MatchedFaceID: "",
			},
		},
	}

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	// 1. 验证 captures 表
	var captures []record.Capture
	if err := dbStore.DB().Order("target_type").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 captures (1 face + 1 person), got %d", len(captures))
	}

	faceCap := captures[0]
	if faceCap.TargetType != "face" || faceCap.Score != 0.89 {
		t.Errorf("unexpected face capture: %+v", faceCap)
	}
	if faceCap.TrackID <= 0 {
		t.Errorf("isolated face must be assigned positive host TrackID, got %d", faceCap.TrackID)
	}

	personCap := captures[1]
	if personCap.TargetType != "person" || personCap.Score != 0.93 {
		t.Errorf("unexpected person capture: %+v", personCap)
	}
	if personCap.TrackID <= 0 {
		t.Errorf("person must be assigned positive host TrackID, got %d", personCap.TrackID)
	}

	// 2. 验证 recognitions 表
	var recs []record.Recognition
	if err := dbStore.DB().Order("id").Find(&recs).Error; err != nil {
		t.Fatalf("query recognitions failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 recognition records, got %d", len(recs))
	}

	// 关联人脸应该继承真人体 track_id
	var associatedRec *record.Recognition
	for i := range recs {
		if recs[i].PersonName == "Alice" {
			associatedRec = &recs[i]
			break
		}
	}
	if associatedRec == nil {
		t.Fatalf("expected to find Alice recognition record")
	}
	if associatedRec.TrackID != personCap.TrackID {
		t.Errorf("associated face must inherit person track_id %d, got %d", personCap.TrackID, associatedRec.TrackID)
	}
}
