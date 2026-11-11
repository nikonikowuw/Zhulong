package region

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

func reprojectTestView() Rect { return Rect{0.25, 0.2, 0.75, 0.8} }

func nearlyEqual(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func assertBox(t *testing.T, got, want [4]float64) {
	t.Helper()
	for i := range want {
		if !nearlyEqual(got[i], want[i]) {
			t.Fatalf("box mismatch at %d: got %v want %v", i, got, want)
		}
	}
}

func TestReprojectIdentityIsNoop(t *testing.T) {
	res := aitypes.InferenceResult{
		TaskType:   aitypes.TaskTypeDetection,
		Detections: []aitypes.DetectionItem{{Box: [4]float64{0.1, 0.2, 0.3, 0.4}}},
		Faces:      []aitypes.FaceStructureItem{{Box: [4]float64{0.5, 0.5, 0.6, 0.6}, Landmarks: [][2]float64{{0.55, 0.55}}}},
	}
	got := Reproject(res, Identity())
	if !reflect.DeepEqual(got, res) {
		t.Fatalf("identity view must return the input untouched")
	}
}

func TestReprojectMapsAllShapes(t *testing.T) {
	res := aitypes.InferenceResult{
		TaskType: aitypes.TaskTypeSceneStructure,
		Detections: []aitypes.DetectionItem{
			{Box: [4]float64{0, 0, 1, 1}},
			{Box: [4]float64{1.2, -0.2, 2, 1}}, // 越界输入按 clamp 收敛
		},
		Persons: []aitypes.PersonItem{{
			BodyBox: [4]float64{0.5, 0.5, 0.5, 0.5},
			Faces: []aitypes.FaceSubItem{{
				Box:       [4]float64{0.5, 0.5, 0.6, 0.6},
				Landmarks: [][2]float64{{0.5, 0.5}, {math.NaN(), 0.5}},
			}},
		}},
		Faces: []aitypes.FaceStructureItem{{
			Box:         [4]float64{0, 0, 0.5, 0.5},
			Landmarks:   [][2]float64{{0.25, 0.25}},
			PersonIndex: 0,
		}},
		Vehicles: []aitypes.VehicleItem{{
			VehicleBox: [4]float64{0.5, 0, 1, 0.5},
			Plates: []aitypes.PlateSubItem{
				{Box: [4]float64{0.6, 0.1, 0.8, 0.2}},
				{Box: [4]float64{math.Inf(-1), 0, 0.5, 0.5}},
			},
		}},
		Classifications: []aitypes.ClassificationItem{{Label: "car", Score: 0.9}},
	}

	got := Reproject(res, reprojectTestView())

	if len(got.Detections) != 2 {
		t.Fatalf("expected 2 detections, got %d", len(got.Detections))
	}
	assertBox(t, got.Detections[0].Box, [4]float64{0.25, 0.2, 0.75, 0.8})
	// 0.25+1.2*0.5=0.85, 0.2-0.2*0.6=0.08, 0.25+2*0.5=1.25->1, 0.2+1*0.6=0.8
	assertBox(t, got.Detections[1].Box, [4]float64{0.85, 0.08, 1, 0.8})

	if len(got.Persons) != 1 {
		t.Fatalf("expected 1 person, got %d", len(got.Persons))
	}
	assertBox(t, got.Persons[0].BodyBox, [4]float64{0.5, 0.5, 0.5, 0.5})
	if len(got.Persons[0].Faces) != 1 {
		t.Fatalf("expected non-finite nested face to be dropped, got %d", len(got.Persons[0].Faces))
	}
	assertBox(t, got.Persons[0].Faces[0].Box, [4]float64{0.5, 0.5, 0.55, 0.56})
	if len(got.Persons[0].Faces[0].Landmarks) != 1 {
		t.Fatalf("expected non-finite landmark to be dropped")
	}

	if len(got.Faces) != 1 {
		t.Fatalf("expected 1 face, got %d", len(got.Faces))
	}
	assertBox(t, got.Faces[0].Box, [4]float64{0.25, 0.2, 0.5, 0.5})
	if got.Faces[0].PersonIndex != 0 {
		t.Fatalf("expected person index 0, got %d", got.Faces[0].PersonIndex)
	}
	if len(got.Faces[0].Landmarks) != 1 {
		t.Fatalf("expected 1 landmark, got %d", len(got.Faces[0].Landmarks))
	}
	if !nearlyEqual(got.Faces[0].Landmarks[0][0], 0.375) || !nearlyEqual(got.Faces[0].Landmarks[0][1], 0.35) {
		t.Fatalf("landmark mismatch: %v", got.Faces[0].Landmarks[0])
	}

	if len(got.Vehicles) != 1 {
		t.Fatalf("expected 1 vehicle, got %d", len(got.Vehicles))
	}
	assertBox(t, got.Vehicles[0].VehicleBox, [4]float64{0.5, 0.2, 0.75, 0.5})
	if len(got.Vehicles[0].Plates) != 1 {
		t.Fatalf("expected non-finite plate to be dropped, got %d", len(got.Vehicles[0].Plates))
	}
	assertBox(t, got.Vehicles[0].Plates[0].Box, [4]float64{0.55, 0.26, 0.65, 0.32})

	if !reflect.DeepEqual(got.Classifications, res.Classifications) {
		t.Fatalf("classifications must pass through untouched")
	}
}

func TestReprojectDropsNonFiniteAndRepairsPersonIndex(t *testing.T) {
	res := aitypes.InferenceResult{
		TaskType: aitypes.TaskTypeSceneStructure,
		Persons: []aitypes.PersonItem{
			{BodyBox: [4]float64{0.1, 0.1, 0.2, 0.2}},
			{BodyBox: [4]float64{math.NaN(), 0.1, 0.2, 0.2}},
			{BodyBox: [4]float64{0.3, 0.3, 0.4, 0.4}},
		},
		Faces: []aitypes.FaceStructureItem{
			{Box: [4]float64{0.1, 0.1, 0.2, 0.2}, PersonIndex: 0},
			{Box: [4]float64{0.1, 0.1, 0.2, 0.2}, PersonIndex: 1}, // 宿主 person 被丢弃 → face 一并丢弃
			{Box: [4]float64{0.3, 0.3, 0.4, 0.4}, PersonIndex: 2}, // 重映射到 1
			{Box: [4]float64{0.3, 0.3, 0.4, 0.4}, PersonIndex: -1},
		},
	}

	got := Reproject(res, reprojectTestView())

	if len(got.Persons) != 2 {
		t.Fatalf("expected 2 persons, got %d", len(got.Persons))
	}
	if len(got.Faces) != 3 {
		t.Fatalf("expected 3 faces, got %d", len(got.Faces))
	}
	if got.Faces[0].PersonIndex != 0 || got.Faces[1].PersonIndex != 1 || got.Faces[2].PersonIndex != -1 {
		t.Fatalf("unexpected person index remap: %d %d %d",
			got.Faces[0].PersonIndex, got.Faces[1].PersonIndex, got.Faces[2].PersonIndex)
	}
}

// TestReprojectFacePersonIndexMatchesFilter 钉住「人脸跟随父人体」的判据。
//
// 这条规则此前只存在于注释里：Persons 为空时，带 PersonIndex >= 0 的顶层人脸会被整条
// 丢弃，与人脸框是否落在窗口内无关。判据必须与 region.Filter 完全一致，否则同一份结果
// 在「配了 crop」与「配了 ROI」下留下的人脸不一样——两个特性各自看着都对，合起来是 bug。
func TestReprojectFacePersonIndexMatchesFilter(t *testing.T) {
	res := aitypes.InferenceResult{
		TaskType: aitypes.TaskTypeSceneStructure,
		Faces: []aitypes.FaceStructureItem{
			{Box: [4]float64{0.1, 0.1, 0.3, 0.3}, PersonIndex: 0},  // 没有 Persons → 丢弃
			{Box: [4]float64{0.1, 0.1, 0.3, 0.3}, PersonIndex: -1}, // 孤立人脸 → 保留
			{Box: [4]float64{0.2, 0.2, 0.3, 0.3}, PersonIndex: 7},  // 越界 → 丢弃
		},
	}

	got := Reproject(res, reprojectTestView())
	if len(got.Faces) != 1 {
		t.Fatalf("expected only the isolated face to survive reprojection, got %d", len(got.Faces))
	}
	if got.Faces[0].PersonIndex != -1 {
		t.Fatalf("expected isolated face, got PersonIndex %d", got.Faces[0].PersonIndex)
	}

	// 全幅 ROI 只让「父人体」判据起作用，于是过滤结果的形状必须与逆映射一致。
	cfg := Config{ROI: []Rect{Identity()}}
	filtered := cfg.Filter(res)
	if len(filtered.Faces) != 1 || filtered.Faces[0].PersonIndex != -1 {
		t.Fatalf("filter and reproject disagree on face retention: %+v", filtered.Faces)
	}
}

func TestReprojectAndFilterPreserveFaceFeatureVector(t *testing.T) {
	fakeFeat := make([]byte, 2048)
	fakeFeat[0] = 0xAB
	fakeFeat[2047] = 0xCD

	res := aitypes.InferenceResult{
		TaskType: aitypes.TaskTypeSceneStructure,
		Faces: []aitypes.FaceStructureItem{
			{
				Box:           [4]float64{0.1, 0.1, 0.3, 0.3},
				PersonIndex:   -1,
				FeatureVector: fakeFeat,
			},
		},
	}

	// 1. Reproject test
	reprojected := Reproject(res, reprojectTestView())
	if len(reprojected.Faces) != 1 {
		t.Fatalf("expected 1 face after reproject, got %d", len(reprojected.Faces))
	}
	if !bytes.Equal(reprojected.Faces[0].FeatureVector, fakeFeat) {
		t.Fatalf("reproject failed to preserve FeatureVector")
	}

	// 2. Filter test
	cfg := Config{ROI: []Rect{Identity()}}
	filtered := cfg.Filter(res)
	if len(filtered.Faces) != 1 {
		t.Fatalf("expected 1 face after filter, got %d", len(filtered.Faces))
	}
	if !bytes.Equal(filtered.Faces[0].FeatureVector, fakeFeat) {
		t.Fatalf("filter failed to preserve FeatureVector")
	}
}
