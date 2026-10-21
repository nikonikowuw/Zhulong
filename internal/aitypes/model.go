package aitypes

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Common Task Types
const (
	TaskTypeDetection        = "detection"
	TaskTypeClassification   = "classification"
	TaskTypePersonStructure  = "person_structure"
	TaskTypeVehicleStructure = "vehicle_structure"
)

// ResultType corresponds to native zhulong_result_type_t in zhulong/plugin.h.
type ResultType int32

const (
	ResultTypeUnknown          ResultType = 0
	ResultTypeDetection        ResultType = 1
	ResultTypeClassification   ResultType = 2
	ResultTypePersonStructure  ResultType = 3
	ResultTypeVehicleStructure ResultType = 4
	ResultTypeCustom           ResultType = 99
)

// DetectionItem represents a single detected object with normalized coordinates.
// Box is [x1, y1, x2, y2] strictly normalized to [0.0, 1.0].
type DetectionItem struct {
	Box        [4]float64     `json:"box"`
	Label      string         `json:"label"`
	Score      float64        `json:"score"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// ClassificationItem represents a classification output.
type ClassificationItem struct {
	Label string  `json:"label"`
	Score float64 `json:"score"`
}

// FaceCandidate represents one candidate match in Top-K face search.
type FaceCandidate struct {
	PersonID   string  `json:"person_id"`
	PersonName string  `json:"person_name"`
	FaceID     string  `json:"face_id"`
	Similarity float64 `json:"similarity"`
}

// FaceSubItem represents a face sub-target mounted onto a person entity.
type FaceSubItem struct {
	Box           [4]float64      `json:"box"`
	Quality       float64         `json:"quality"`
	Landmarks     [][2]float64    `json:"landmarks"`
	Matched       bool            `json:"matched"`
	PersonID      string          `json:"person_id"`
	PersonName    string          `json:"person_name"`
	MatchedFaceID string          `json:"matched_face_id"`
	Similarity    float64         `json:"similarity"`
	IsFused       bool            `json:"is_fused"`
	Candidates    []FaceCandidate `json:"candidates"`
}

// PersonItem represents a detected person with body box and mounted face items (1:N).
type PersonItem struct {
	TrackID    int64          `json:"track_id"`
	BodyBox    [4]float64     `json:"body_box"`
	BodyScore  float64        `json:"body_score"`
	Faces      []FaceSubItem  `json:"faces"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// PlateSubItem represents a license plate mounted onto a vehicle entity.
// Box is [x1, y1, x2, y2] strictly normalized to [0.0, 1.0].
type PlateSubItem struct {
	Box         [4]float64 `json:"box"`
	Score       float64    `json:"score"`
	PlateNumber string     `json:"plate_number"`
	PlateColor  string     `json:"plate_color"`
	PlateType   int32      `json:"plate_type"`
}

// VehicleItem represents a detected vehicle with vehicle box and mounted plate items (1:N).
type VehicleItem struct {
	TrackID      int64          `json:"track_id"`
	VehicleBox   [4]float64     `json:"vehicle_box"`
	VehicleScore float64        `json:"vehicle_score"`
	VehicleType  string         `json:"vehicle_type,omitempty"`
	VehicleColor string         `json:"vehicle_color,omitempty"`
	Plates       []PlateSubItem `json:"plates"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// InferenceResult is the unified inference output event payload.
type InferenceResult struct {
	CameraID        string               `json:"camera_id"`
	AlgorithmID     string               `json:"algorithm_id"`
	PTS             int64                `json:"pts"`
	Timestamp       time.Time            `json:"timestamp"`
	TaskType        string               `json:"task_type"`
	CostMs          int64                `json:"cost_ms"`
	Detections      []DetectionItem      `json:"detections"`
	Classifications []ClassificationItem `json:"classifications"`
	Persons         []PersonItem         `json:"persons"`
	Vehicles        []VehicleItem        `json:"vehicles"`
}

// NewDetectionResult creates an empty detection inference result.
func NewDetectionResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return newInferenceResult(cameraID, algorithmID, pts, costMs, TaskTypeDetection)
}

// NewClassificationResult creates an empty classification inference result.
func NewClassificationResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return newInferenceResult(cameraID, algorithmID, pts, costMs, TaskTypeClassification)
}

// NewPersonStructureResult creates an empty person-structure inference result.
func NewPersonStructureResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return newInferenceResult(cameraID, algorithmID, pts, costMs, TaskTypePersonStructure)
}

// NewVehicleStructureResult creates an empty vehicle-structure inference result.
func NewVehicleStructureResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return newInferenceResult(cameraID, algorithmID, pts, costMs, TaskTypeVehicleStructure)
}

func newInferenceResult(cameraID, algorithmID string, pts, costMs int64, taskType string) InferenceResult {
	return InferenceResult{
		CameraID:        cameraID,
		AlgorithmID:     algorithmID,
		PTS:             pts,
		Timestamp:       time.Now().UTC(),
		TaskType:        taskType,
		CostMs:          costMs,
		Detections:      make([]DetectionItem, 0),
		Classifications: make([]ClassificationItem, 0),
		Persons:         make([]PersonItem, 0),
		Vehicles:        make([]VehicleItem, 0),
	}
}

// RawInferencePayload models the JSON payload serialized by zhulong_algo_runner.
type RawInferencePayload struct {
	StreamID        uint64               `json:"stream_id"`
	PTS             int64                `json:"pts"`
	CaptureTimeMs   int64                `json:"capture_time_ms"`
	CostMs          int64                `json:"cost_ms"`
	TaskType        string               `json:"task_type"`
	AlgorithmID     string               `json:"algorithm_id,omitempty"`
	Detections      []DetectionItem      `json:"detections"`
	Classifications []ClassificationItem `json:"classifications"`
	Persons         []PersonItem         `json:"persons"`
	Vehicles        []VehicleItem        `json:"vehicles"`
}

// ParseInferenceResult parses runner serialized JSON into a domain InferenceResult.
func ParseInferenceResult(cameraID, algorithmID string, data []byte) (InferenceResult, error) {
	if len(data) == 0 {
		return InferenceResult{}, errors.New("empty inference payload")
	}

	var payload RawInferencePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return InferenceResult{}, fmt.Errorf("unmarshal inference result: %w", err)
	}

	effectiveAlgoID := algorithmID
	if payload.AlgorithmID != "" {
		effectiveAlgoID = payload.AlgorithmID
	}

	captureTime := time.Now().UTC()
	if payload.CaptureTimeMs > 0 {
		captureTime = time.UnixMilli(payload.CaptureTimeMs).UTC()
	}

	var res InferenceResult
	switch payload.TaskType {
	case TaskTypeDetection:
		res = NewDetectionResult(cameraID, effectiveAlgoID, payload.PTS, payload.CostMs)
		res.Timestamp = captureTime
		if len(payload.Detections) > 0 {
			res.Detections = payload.Detections
		}
	case TaskTypeClassification:
		res = NewClassificationResult(cameraID, effectiveAlgoID, payload.PTS, payload.CostMs)
		res.Timestamp = captureTime
		if len(payload.Classifications) > 0 {
			res.Classifications = payload.Classifications
		}
	case TaskTypePersonStructure:
		res = NewPersonStructureResult(cameraID, effectiveAlgoID, payload.PTS, payload.CostMs)
		res.Timestamp = captureTime
		if len(payload.Persons) > 0 {
			res.Persons = payload.Persons
		}
	case TaskTypeVehicleStructure:
		res = NewVehicleStructureResult(cameraID, effectiveAlgoID, payload.PTS, payload.CostMs)
		res.Timestamp = captureTime
		if len(payload.Vehicles) > 0 {
			res.Vehicles = payload.Vehicles
		}
	default:
		res = InferenceResult{
			CameraID:        cameraID,
			AlgorithmID:     effectiveAlgoID,
			PTS:             payload.PTS,
			Timestamp:       captureTime,
			TaskType:        payload.TaskType,
			CostMs:          payload.CostMs,
			Detections:      payload.Detections,
			Classifications: payload.Classifications,
			Persons:         payload.Persons,
			Vehicles:        payload.Vehicles,
		}
		if res.Detections == nil {
			res.Detections = make([]DetectionItem, 0)
		}
		if res.Classifications == nil {
			res.Classifications = make([]ClassificationItem, 0)
		}
		if res.Persons == nil {
			res.Persons = make([]PersonItem, 0)
		}
		if res.Vehicles == nil {
			res.Vehicles = make([]VehicleItem, 0)
		}
	}
	return res, nil
}
