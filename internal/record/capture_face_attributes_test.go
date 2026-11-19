package record_test

import (
	"context"
	"testing"
	"time"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
	"github.com/nikonikowuw/Zhulong/internal/record"
	"go.uber.org/zap"
)

// faceAttributeResult 是一帧同时带两条属性通道的场景输入：
// 人体 0 带人体属性（PPLCNet，192×256 人体框），它的关联人脸带人脸属性
// （genderage，96×96 对齐人脸）；另有一张孤立人脸带不同的人脸属性。
func faceAttributeResult(ts time.Time) aitypes.InferenceResult {
	return aitypes.InferenceResult{
		CameraID:    "cam_01",
		AlgorithmID: "algo_01",
		PTS:         3000,
		Timestamp:   ts,
		TaskType:    aitypes.TaskTypeSceneStructure,
		Persons: []aitypes.PersonItem{
			{
				BodyBox:   [4]float64{0.10, 0.10, 0.55, 0.95},
				BodyScore: 0.92,
				Attributes: map[string]any{
					"ShortSleeve": float64(0.88),
					"Female":      float64(0.54),
				},
			},
		},
		Faces: []aitypes.FaceStructureItem{
			{
				Box:         [4]float64{0.20, 0.15, 0.34, 0.30},
				Quality:     0.90,
				PersonIndex: 0,
				Attributes: map[string]any{
					"Female":   float64(0.93),
					"Age18-30": float64(1.0),
				},
			},
			{
				Box:         [4]float64{0.70, 0.20, 0.88, 0.40},
				Quality:     0.85,
				PersonIndex: -1,
				Attributes: map[string]any{
					"Male":     float64(0.97),
					"Age30-45": float64(1.0),
				},
			},
		},
	}
}

// TestProcessFaceAttributesStayOnTheirOwnItem 钉住「人脸属性挂人脸、人体属性挂人体」。
//
// 两条通道的标签空间会重叠（人体模型与人脸模型都有 "Female"），所以这里同时断言：
//  1. 人体抓拍只带人体属性，人脸抓拍只带人脸属性；
//  2. 识别记录两条通道都在，同名标签不互相覆盖 —— 人脸那条改名为 "<标签>@face"。
func TestProcessFaceAttributesStayOnTheirOwnItem(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)

	if err := svc.ProcessInferenceResult(ctx, faceAttributeResult(ts)); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Order("target_type, id").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 captures (1 face + 1 person), got %d", len(captures))
	}

	// 排序后 face < person
	// GORM 模型只有 AttributesJSON 列；属性 map 由 ToResponse 解出来
	// （与 API 消费者看到的完全一致，避免测试自己另写一套解析）。
	faceCap, personCap := captures[0].ToResponse(), captures[1].ToResponse()
	if faceCap.TargetType != "face" || personCap.TargetType != "person" {
		t.Fatalf("unexpected capture order: %q, %q", faceCap.TargetType, personCap.TargetType)
	}

	if _, ok := faceCap.Attributes["Male"]; !ok {
		t.Errorf("face capture must carry the face's own attributes, got %+v", faceCap.Attributes)
	}
	if _, ok := faceCap.Attributes["ShortSleeve"]; ok {
		t.Errorf("face capture must not carry person attributes, got %+v", faceCap.Attributes)
	}
	if _, ok := personCap.Attributes["ShortSleeve"]; !ok {
		t.Errorf("person capture must keep person attributes, got %+v", personCap.Attributes)
	}
	if _, ok := personCap.Attributes["Male"]; ok {
		t.Errorf("person capture must not carry face attributes, got %+v", personCap.Attributes)
	}

	var recs []record.Recognition
	if err := dbStore.DB().Order("id").Find(&recs).Error; err != nil {
		t.Fatalf("query recognitions failed: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("expected 2 recognitions, got %d", len(recs))
	}

	// 关联人脸：两条通道合并，同名 "Female" 保留两份证据。
	associated := recognitionAttributes(t, recs[0])
	if associated["ShortSleeve"] == nil {
		t.Errorf("associated face recognition must keep person attributes, got %+v", associated)
	}
	if associated["Age18-30"] == nil {
		t.Errorf("associated face recognition must keep face attributes, got %+v", associated)
	}
	if got := associated["Female"]; got != float64(0.54) {
		t.Errorf("person-channel Female must survive verbatim, got %v", got)
	}
	if got := associated["Female@face"]; got != float64(0.93) {
		t.Errorf("face-channel Female must be kept under the @face suffix, got %v", got)
	}

	// 孤立人脸：没有人体通道，属性原样落库，不加后缀。
	isolated := recognitionAttributes(t, recs[1])
	if isolated["Male"] == nil || isolated["Age30-45"] == nil {
		t.Errorf("isolated face recognition must carry face attributes, got %+v", isolated)
	}
	if _, ok := isolated["Female@face"]; ok {
		t.Errorf("single-channel attributes must not be suffixed, got %+v", isolated)
	}
}

// recognitionAttributes 解出识别记录落库的属性（同上：只走 ToResponse）。
func recognitionAttributes(t *testing.T, rec record.Recognition) map[string]any {
	t.Helper()
	return rec.ToResponse().Attributes
}

// TestProcessSceneStructureFeatureVectorPersistence 的同源场景：SCENE_STRUCTURE 出口
// 才是人脸属性真正挂载的地方，这里单独钉住「人脸属性不会漏进人体项」。
func TestProcessSceneStructureFaceAttributesAreNotPersonAttributes(t *testing.T) {
	store, dbStore, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	svc := record.NewService(store, zap.NewNop())
	ts := time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)

	res := faceAttributeResult(ts)
	// 去掉人体属性：只剩人脸通道，人体抓拍的 attributes 必须是空对象而不是人脸属性。
	res.Persons[0].Attributes = nil

	if err := svc.ProcessInferenceResult(ctx, res); err != nil {
		t.Fatalf("ProcessInferenceResult failed: %v", err)
	}

	var captures []record.Capture
	if err := dbStore.DB().Order("target_type, id").Find(&captures).Error; err != nil {
		t.Fatalf("query captures failed: %v", err)
	}
	if len(captures) != 2 {
		t.Fatalf("expected 2 captures, got %d", len(captures))
	}
	personCap := captures[1].ToResponse()
	if personCap.TargetType != "person" {
		t.Fatalf("unexpected capture: %+v", personCap)
	}
	if len(personCap.Attributes) != 0 {
		t.Errorf("person capture must stay empty when the person channel is empty, got %+v", personCap.Attributes)
	}
}

// TestMergeAttributeChannels 钉住合并规则本身（不经过数据库，纯函数表驱动）。
func TestMergeAttributeChannels(t *testing.T) {
	person := map[string]any{"ShortSleeve": 0.88, "Female": 0.54}
	face := map[string]any{"Female": 0.93, "Age18-30": 1.0}

	tests := []struct {
		name   string
		person map[string]any
		face   map[string]any
		want   map[string]any
	}{
		{
			name:   "两条通道都为空",
			person: nil,
			face:   nil,
			want:   nil,
		},
		{
			name:   "只有人体通道",
			person: person,
			face:   nil,
			want:   map[string]any{"ShortSleeve": 0.88, "Female": 0.54},
		},
		{
			name:   "只有人脸通道",
			person: nil,
			face:   face,
			want:   map[string]any{"Female": 0.93, "Age18-30": 1.0},
		},
		{
			name:   "同名标签两条通道都保留",
			person: person,
			face:   face,
			want: map[string]any{
				"ShortSleeve": 0.88,
				"Female":      0.54,
				"Female@face": 0.93,
				"Age18-30":    1.0,
			},
		},
		{
			// 人脸通道自己就带 @face（上游合并过一轮）：继续追加后缀，不能覆盖。
			name:   "人脸通道自带 @face 后缀",
			person: map[string]any{"Female": 0.54},
			face:   map[string]any{"Female": 0.93, "Female@face": 0.11},
			want: map[string]any{
				"Female":           0.54,
				"Female@face":      0.93,
				"Female@face@face": 0.11,
			},
		},
		{
			// 撞名键在人体通道里：人脸的同名标签同样要一路追加，而不是踩掉人体那条。
			name:   "人体通道自带 @face 后缀",
			person: map[string]any{"Female": 0.54, "Female@face": 0.40},
			face:   map[string]any{"Female": 0.93},
			want: map[string]any{
				"Female":           0.54,
				"Female@face":      0.40,
				"Female@face@face": 0.93,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := record.MergeAttributeChannelsForTest(tc.person, tc.face)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d attributes, got %d: %+v", len(tc.want), len(got), got)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("attribute %q: expected %v, got %v", k, v, got[k])
				}
			}
		})
	}
}

// TestMergeAttributeChannelsSuffixChainIsOrderIndependent 人脸通道万一自己就带
// "<标签>@face"（例如上游已经合并过一次），改名要继续追加后缀而不是覆盖 —— 覆盖会
// 静默丢掉一条证据。这里连跑多轮：Go 的 map 迭代顺序是随机的，只探一次后缀的实现
// 会在 `{Female, Female@face}` 互相踩到时随机丢键，单次运行抓不住。
func TestMergeAttributeChannelsSuffixChainIsOrderIndependent(t *testing.T) {
	want := map[string]any{
		"Female":           0.54,
		"Female@face":      0.93,
		"Female@face@face": 0.11,
	}
	for i := 0; i < 200; i++ {
		got := record.MergeAttributeChannelsForTest(
			map[string]any{"Female": 0.54},
			map[string]any{"Female": 0.93, "Female@face": 0.11},
		)
		if len(got) != len(want) {
			t.Fatalf("iteration %d: expected %d keys, got %d: %+v", i, len(want), len(got), got)
		}
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("iteration %d: attribute %q: expected %v, got %v", i, k, v, got[k])
			}
		}
	}
}

// TestMergeAttributeChannelsDoesNotMutateInputs 合并绝不能改调用方的 map：
// personAttrs 是载荷里 PersonItem 的切片元素，faceAttrs 同理，改它们是跨记录的副作用。
func TestMergeAttributeChannelsDoesNotMutateInputs(t *testing.T) {
	person := map[string]any{"Female": 0.54}
	face := map[string]any{"Female": 0.93}

	got := record.MergeAttributeChannelsForTest(person, face)

	if got["Female"] != 0.54 || got["Female@face"] != 0.93 {
		t.Errorf("unexpected merge result: %+v", got)
	}

	if len(person) != 1 || person["Female"] != 0.54 {
		t.Errorf("person channel was mutated: %+v", person)
	}
	if len(face) != 1 || face["Female"] != 0.93 {
		t.Errorf("face channel was mutated: %+v", face)
	}
}

// TestMergeAttributeChannelsSingleChannelIdentity 只有一条通道时直接返回它本身
// （不复制）：最常见的情况不该为每条记录多分配一次 map。
func TestMergeAttributeChannelsSingleChannelIdentity(t *testing.T) {
	person := map[string]any{"Female": 0.54}
	if got := record.MergeAttributeChannelsForTest(person, nil); got == nil || len(got) != len(person) {
		t.Fatalf("expected the person channel back, got %+v", got)
	}
	face := map[string]any{"Male": 0.97}
	if got := record.MergeAttributeChannelsForTest(nil, face); got == nil || len(got) != len(face) {
		t.Fatalf("expected the face channel back, got %+v", got)
	}
}
