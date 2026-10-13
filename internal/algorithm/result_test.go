package algorithm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorithm"
)

func TestInferenceResultJSONEmptySlices(t *testing.T) {
	res := algorithm.NewDetectionResult("cam_01", "safety_detector", 1000, 25)
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
	res := algorithm.NewPersonStructureResult("cam_gate_01", "face_reid", 1728800000123, 42)
	res.Persons = append(res.Persons, algorithm.PersonItem{
		TrackID:   101,
		BodyBox:   [4]float64{0.25, 0.12, 0.45, 0.85},
		BodyScore: 0.95,
		Faces: []algorithm.FaceSubItem{
			{
				Box:        [4]float64{0.31, 0.13, 0.39, 0.28},
				Quality:    0.92,
				Landmarks:  [][2]float64{{0.335, 0.18}, {0.365, 0.178}},
				Matched:    true,
				PersonID:   "emp_9527",
				PersonName: "张三",
				Similarity: 0.94,
				IsFused:    true,
				Candidates: []algorithm.FaceCandidate{
					{
						PersonID:   "emp_9527",
						PersonName: "张三",
						Similarity: 0.94,
					},
					{
						PersonID:   "emp_1002",
						PersonName: "李四",
						Similarity: 0.82,
					},
				},
			},
		},
	})

	data, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var parsed algorithm.InferenceResult
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
	if face.PersonID != "emp_9527" || face.PersonName != "张三" {
		t.Errorf("unexpected top-1 match: %+v", face)
	}
	if len(face.Candidates) != 2 {
		t.Fatalf("expected 2 candidates in top-k, got %d", len(face.Candidates))
	}
	if face.Candidates[0].PersonID != "emp_9527" || face.Candidates[1].PersonID != "emp_1002" {
		t.Errorf("unexpected candidates list: %+v", face.Candidates)
	}
}
