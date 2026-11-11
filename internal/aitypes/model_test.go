package aitypes_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

func TestInferenceResultJSONEmptySlices(t *testing.T) {
	res := aitypes.NewDetectionResult("cam_01", "safety_detector", 1000, 25)
	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	s := string(data)

	// Verify that empty arrays are rendered as [] and not null
	if !strings.Contains(s, `"detections":[]`) {
		t.Errorf("expected detections to be empty array [], got JSON: %s", s)
	}
	if !strings.Contains(s, `"classifications":[]`) {
		t.Errorf("expected classifications to be empty array [], got JSON: %s", s)
	}
	if !strings.Contains(s, `"persons":[]`) {
		t.Errorf("expected persons to be empty array [], got JSON: %s", s)
	}
	if !strings.Contains(s, `"vehicles":[]`) {
		t.Errorf("expected vehicles to be empty array [], got JSON: %s", s)
	}
}

func TestResultConstructorsInitializeEmptySlices(t *testing.T) {
	constructors := []struct {
		name      string
		taskType  string
		newResult func(string, string, int64, int64) aitypes.InferenceResult
	}{
		{"detection", aitypes.TaskTypeDetection, aitypes.NewDetectionResult},
		{"classification", aitypes.TaskTypeClassification, aitypes.NewClassificationResult},
		{"person_structure", aitypes.TaskTypePersonStructure, aitypes.NewPersonStructureResult},
		{"scene_structure", aitypes.TaskTypeSceneStructure, aitypes.NewSceneStructureResult},
		{"vehicle_structure", aitypes.TaskTypeVehicleStructure, aitypes.NewVehicleStructureResult},
	}

	for _, constructor := range constructors {
		t.Run(constructor.name, func(t *testing.T) {
			res := constructor.newResult("cam_01", "algo_01", 123, 7)
			if res.TaskType != constructor.taskType {
				t.Fatalf("expected task type %q, got %q", constructor.taskType, res.TaskType)
			}
			if res.Detections == nil || res.Classifications == nil || res.Persons == nil || res.Faces == nil || res.Vehicles == nil {
				t.Fatalf("expected all result slices to be initialized: %+v", res)
			}
		})
	}
}

func TestPersonStructureResultWithTopK(t *testing.T) {
	res := aitypes.NewPersonStructureResult("cam_gate_01", "face_reid", 1728800000123, 42)
	res.Persons = append(res.Persons, aitypes.PersonItem{
		TrackID:   101,
		BodyBox:   [4]float64{0.25, 0.12, 0.45, 0.85},
		BodyScore: 0.95,
		Faces: []aitypes.FaceSubItem{
			{
				Box:           [4]float64{0.31, 0.13, 0.39, 0.28},
				Quality:       0.92,
				Landmarks:     [][2]float64{{0.335, 0.18}, {0.365, 0.178}},
				Matched:       true,
				PersonID:      "emp_9527",
				PersonName:    "张三",
				MatchedFaceID: "face_img_01",
				Similarity:    0.94,
				IsFused:       true,
				Candidates: []aitypes.FaceCandidate{
					{
						PersonID:   "emp_9527",
						PersonName: "张三",
						FaceID:     "face_img_01",
						Similarity: 0.94,
					},
					{
						PersonID:   "emp_1002",
						PersonName: "李四",
						FaceID:     "face_img_02",
						Similarity: 0.82,
					},
				},
			},
		},
		Attributes: map[string]any{"gender": "male", "upper_color": "blue"},
	})

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed aitypes.InferenceResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if len(parsed.Persons) != 1 {
		t.Fatalf("expected 1 person, got %d", len(parsed.Persons))
	}
	p := parsed.Persons[0]
	if len(p.Faces) != 1 {
		t.Fatalf("expected 1 face, got %d", len(p.Faces))
	}
	face := p.Faces[0]
	if face.PersonID != "emp_9527" || face.PersonName != "张三" || face.MatchedFaceID != "face_img_01" {
		t.Errorf("unexpected top-1 match: %+v", face)
	}
	if len(face.Candidates) != 2 {
		t.Fatalf("expected 2 candidates in top-k, got %d", len(face.Candidates))
	}
	if face.Candidates[0].FaceID != "face_img_01" || face.Candidates[1].FaceID != "face_img_02" {
		t.Errorf("unexpected candidates face_id list: %+v", face.Candidates)
	}
	if p.Attributes["gender"] != "male" {
		t.Errorf("unexpected attributes: %+v", p.Attributes)
	}
}

func TestParseInferenceResult(t *testing.T) {
	// 1. Detection
	detJSON := []byte(`{"stream_id":1,"pts":1000,"cost_ms":12,"task_type":"detection","detections":[{"box":[0.1,0.2,0.3,0.4],"label":"person","score":0.95}],"classifications":[],"persons":[]}`)
	detRes, err := aitypes.ParseInferenceResult("cam_1", "algo_det", detJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult detection failed: %v", err)
	}
	if detRes.TaskType != aitypes.TaskTypeDetection || len(detRes.Detections) != 1 {
		t.Fatalf("unexpected detection result: %+v", detRes)
	}
	if detRes.Detections[0].Label != "person" || detRes.Detections[0].Score != 0.95 {
		t.Errorf("unexpected detection item: %+v", detRes.Detections[0])
	}

	// 2. Classification
	clsJSON := []byte(`{"stream_id":1,"pts":2000,"cost_ms":8,"task_type":"classification","detections":[],"classifications":[{"label":"cat","score":0.88}],"persons":[]}`)
	clsRes, err := aitypes.ParseInferenceResult("cam_1", "algo_cls", clsJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult classification failed: %v", err)
	}
	if clsRes.TaskType != aitypes.TaskTypeClassification || len(clsRes.Classifications) != 1 {
		t.Fatalf("unexpected classification result: %+v", clsRes)
	}

	// 3. Person structure with capture_time_ms, matched_face_id and candidates face_id
	// 入口单向归一化：TaskTypePersonStructure 转换为 TaskTypeSceneStructure，faces 提取至顶层，回填 PersonIndex
	captureMs := int64(1729000000123)
	psJSON := []byte(`{"stream_id":1,"pts":3000,"capture_time_ms":1729000000123,"cost_ms":25,"task_type":"person_structure","detections":[],"classifications":[],"persons":[{"track_id":10,"body_box":[0.1,0.1,0.5,0.8],"body_score":0.9,"faces":[{"box":[0.2,0.2,0.3,0.3],"quality":0.95,"landmarks":[[0.22,0.25],[0.28,0.25],[0.25,0.27],[0.23,0.29],[0.27,0.29]],"matched":true,"similarity":0.91,"person_id":"user_1","person_name":"Bob","matched_face_id":"face_bob_01","candidates":[{"person_id":"user_1","person_name":"Bob","face_id":"face_bob_01","similarity":0.91}]}]}]}`)
	psRes, err := aitypes.ParseInferenceResult("cam_1", "algo_face", psJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult person structure failed: %v", err)
	}
	if psRes.TaskType != aitypes.TaskTypeSceneStructure || len(psRes.Persons) != 1 || len(psRes.Faces) != 1 {
		t.Fatalf("unexpected person structure normalized result: %+v", psRes)
	}
	if psRes.Timestamp.UnixMilli() != captureMs {
		t.Errorf("expected Timestamp UnixMilli to match capture_time_ms %d, got %d", captureMs, psRes.Timestamp.UnixMilli())
	}
	if psRes.Persons[0].TrackID != 10 || len(psRes.Persons[0].Faces) != 0 {
		t.Errorf("expected clean person without nested faces: %+v", psRes.Persons[0])
	}
	if psRes.Faces[0].PersonIndex != 0 || psRes.Faces[0].PersonID != "user_1" {
		t.Errorf("unexpected normalized face content: %+v", psRes.Faces[0])
	}
	if psRes.Faces[0].MatchedFaceID != "face_bob_01" {
		t.Errorf("expected matched_face_id 'face_bob_01', got %s", psRes.Faces[0].MatchedFaceID)
	}
	if len(psRes.Faces[0].Candidates) != 1 || psRes.Faces[0].Candidates[0].FaceID != "face_bob_01" {
		t.Errorf("unexpected candidates in parsed result: %+v", psRes.Faces[0].Candidates)
	}

	// 3b. Person structure 退化伪人体物理剔除测试
	degenJSON := []byte(`{"stream_id":1,"pts":3050,"task_type":"person_structure","persons":[{"track_id":0,"body_box":[0.2,0.2,0.3,0.3],"body_score":0.92,"faces":[{"box":[0.2,0.2,0.3,0.3],"quality":0.92,"matched":false}]}]}`)
	degenRes, err := aitypes.ParseInferenceResult("cam_1", "algo_face", degenJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult degen failed: %v", err)
	}
	if len(degenRes.Persons) != 0 {
		t.Errorf("expected degen pseudo-person to be physically eliminated, got %d persons", len(degenRes.Persons))
	}
	if len(degenRes.Faces) != 1 || degenRes.Faces[0].PersonIndex != -1 {
		t.Errorf("expected isolated face with PersonIndex=-1, got: %+v", degenRes.Faces)
	}

	// 3c. Scene structure (Type 5) 原生平级解析
	sceneJSON := []byte(`{"stream_id":1,"pts":3100,"task_type":"scene_structure","persons":[{"track_id":101,"body_box":[0.1,0.2,0.5,0.9],"body_score":0.88}],"faces":[{"box":[0.2,0.25,0.35,0.45],"quality":0.96,"person_index":0,"matched":true,"person_name":"Alice"}]}`)
	sceneRes, err := aitypes.ParseInferenceResult("cam_1", "algo_face", sceneJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult scene structure failed: %v", err)
	}
	if sceneRes.TaskType != aitypes.TaskTypeSceneStructure || len(sceneRes.Persons) != 1 || len(sceneRes.Faces) != 1 {
		t.Fatalf("unexpected scene structure result: %+v", sceneRes)
	}
	if sceneRes.Persons[0].TrackID != 101 || sceneRes.Faces[0].PersonName != "Alice" || sceneRes.Faces[0].PersonIndex != 0 {
		t.Errorf("unexpected scene fields: %+v", sceneRes)
	}

	// 4. Algorithm ID resolution from payload or fallback
	withAlgoIDJSON := []byte(`{"stream_id":1,"pts":1000,"task_type":"detection","algorithm_id":"algo_from_payload","detections":[]}`)
	resWithAlgo, err := aitypes.ParseInferenceResult("cam_1", "fallback_algo", withAlgoIDJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult failed: %v", err)
	}
	if resWithAlgo.AlgorithmID != "algo_from_payload" {
		t.Errorf("expected algorithm_id 'algo_from_payload', got %q", resWithAlgo.AlgorithmID)
	}

	withoutAlgoIDJSON := []byte(`{"stream_id":1,"pts":1000,"task_type":"detection","detections":[]}`)
	resWithoutAlgo, err := aitypes.ParseInferenceResult("cam_1", "fallback_algo", withoutAlgoIDJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult failed: %v", err)
	}
	if resWithoutAlgo.AlgorithmID != "fallback_algo" {
		t.Errorf("expected algorithm_id 'fallback_algo', got %q", resWithoutAlgo.AlgorithmID)
	}

	// 5. Vehicle structure
	vehJSON := []byte(`{"stream_id":1,"pts":4000,"cost_ms":18,"task_type":"vehicle_structure","detections":[],"classifications":[],"persons":[],"vehicles":[{"track_id":42,"vehicle_box":[0.1,0.2,0.6,0.7],"vehicle_score":0.93,"vehicle_type":"suv","vehicle_color":"black","plates":[{"box":[0.25,0.55,0.45,0.65],"score":0.97,"plate_number":"粤B88888","plate_color":"蓝色","plate_type":0}]}]}`)
	vehRes, err := aitypes.ParseInferenceResult("cam_1", "algo_plate", vehJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult vehicle structure failed: %v", err)
	}
	if vehRes.TaskType != aitypes.TaskTypeVehicleStructure || len(vehRes.Vehicles) != 1 {
		t.Fatalf("unexpected vehicle structure result: %+v", vehRes)
	}
	v := vehRes.Vehicles[0]
	if v.TrackID != 42 || v.VehicleType != "suv" || v.VehicleColor != "black" {
		t.Errorf("unexpected vehicle fields: %+v", v)
	}
	if len(v.Plates) != 1 || v.Plates[0].PlateNumber != "粤B88888" || v.Plates[0].PlateColor != "蓝色" {
		t.Errorf("unexpected plates content: %+v", v.Plates)
	}

	// 5. Invalid or empty
	if _, err := aitypes.ParseInferenceResult("cam_1", "algo", nil); err == nil {
		t.Errorf("expected error for nil payload")
	}
	if _, err := aitypes.ParseInferenceResult("cam_1", "algo", []byte(`{invalid}`)); err == nil {
		t.Errorf("expected error for invalid json")
	}

	// 6. PTSUS fidelity (pts_us = 0 preserved, positive preserved, missing omitted) & non-null nested slices
	ptsCases := []struct {
		name      string
		raw       string
		wantNil   bool
		wantPTSUS int64
	}{
		{
			name:      "first_frame_zero_pts_us",
			raw:       `{"stream_id":1,"pts":1000,"pts_us":0,"task_type":"scene_structure","persons":[{"track_id":7,"body_box":[0.1,0.2,0.4,0.8],"body_score":0.9}],"faces":[{"box":[0.15,0.22,0.25,0.35],"quality":0.92,"person_index":0,"matched":false}]}`,
			wantNil:   false,
			wantPTSUS: 0,
		},
		{
			name:      "positive_pts_us",
			raw:       `{"stream_id":1,"pts":4600,"pts_us":40000,"task_type":"detection","detections":[]}`,
			wantNil:   false,
			wantPTSUS: 40000,
		},
		{
			name:    "omitted_pts_us",
			raw:     `{"stream_id":1,"pts":4600,"task_type":"detection","detections":[]}`,
			wantNil: true,
		},
	}

	for _, tc := range ptsCases {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := aitypes.ParseInferenceResult("cam_1", "algo_test", []byte(tc.raw))
			if err != nil {
				t.Fatalf("ParseInferenceResult failed: %v", err)
			}
			marshaled, err := json.Marshal(parsed)
			if err != nil {
				t.Fatalf("json.Marshal failed: %v", err)
			}
			js := string(marshaled)
			if tc.wantNil {
				if parsed.PTSUS != nil {
					t.Fatalf("expected nil PTSUS, got %v", *parsed.PTSUS)
				}
				if strings.Contains(js, `"pts_us"`) {
					t.Fatalf("expected omitted pts_us in JSON, got %s", js)
				}
			} else {
				if parsed.PTSUS == nil || *parsed.PTSUS != tc.wantPTSUS {
					t.Fatalf("expected PTSUS=%d, got %v", tc.wantPTSUS, parsed.PTSUS)
				}
				if !strings.Contains(js, `"pts_us":`) {
					t.Fatalf("expected pts_us in JSON, got %s", js)
				}
			}
			if strings.Contains(js, `"faces":null`) || strings.Contains(js, `"landmarks":null`) {
				t.Fatalf("expected non-null faces/landmarks arrays in JSON, got %s", js)
			}
		})
	}
}
