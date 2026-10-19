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
	captureMs := int64(1729000000123)
	psJSON := []byte(`{"stream_id":1,"pts":3000,"capture_time_ms":1729000000123,"cost_ms":25,"task_type":"person_structure","detections":[],"classifications":[],"persons":[{"track_id":10,"body_box":[0.1,0.1,0.5,0.8],"body_score":0.9,"faces":[{"box":[0.2,0.2,0.3,0.3],"quality":0.95,"landmarks":[[0.22,0.25],[0.28,0.25],[0.25,0.27],[0.23,0.29],[0.27,0.29]],"matched":true,"similarity":0.91,"person_id":"user_1","person_name":"Bob","matched_face_id":"face_bob_01","candidates":[{"person_id":"user_1","person_name":"Bob","face_id":"face_bob_01","similarity":0.91}]}]}]}`)
	psRes, err := aitypes.ParseInferenceResult("cam_1", "algo_face", psJSON)
	if err != nil {
		t.Fatalf("ParseInferenceResult person structure failed: %v", err)
	}
	if psRes.TaskType != aitypes.TaskTypePersonStructure || len(psRes.Persons) != 1 {
		t.Fatalf("unexpected person structure result: %+v", psRes)
	}
	if psRes.Timestamp.UnixMilli() != captureMs {
		t.Errorf("expected Timestamp UnixMilli to match capture_time_ms %d, got %d", captureMs, psRes.Timestamp.UnixMilli())
	}
	if psRes.Persons[0].TrackID != 10 || len(psRes.Persons[0].Faces) != 1 || psRes.Persons[0].Faces[0].PersonID != "user_1" {
		t.Errorf("unexpected person content: %+v", psRes.Persons[0])
	}
	if psRes.Persons[0].Faces[0].MatchedFaceID != "face_bob_01" {
		t.Errorf("expected matched_face_id 'face_bob_01', got %s", psRes.Persons[0].Faces[0].MatchedFaceID)
	}
	if len(psRes.Persons[0].Faces[0].Candidates) != 1 || psRes.Persons[0].Faces[0].Candidates[0].FaceID != "face_bob_01" {
		t.Errorf("unexpected candidates in parsed result: %+v", psRes.Persons[0].Faces[0].Candidates)
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

	// 5. Invalid or empty
	if _, err := aitypes.ParseInferenceResult("cam_1", "algo", nil); err == nil {
		t.Errorf("expected error for nil payload")
	}
	if _, err := aitypes.ParseInferenceResult("cam_1", "algo", []byte(`{invalid}`)); err == nil {
		t.Errorf("expected error for invalid json")
	}
}
