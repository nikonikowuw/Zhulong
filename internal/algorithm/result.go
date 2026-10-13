package algorithm

import "time"

// DetectionItem represents a single detected object with normalized coordinates.
// Box is [x1, y1, x2, y2] strictly normalized to [0.0, 1.0].
type DetectionItem struct {
	Box   [4]float64 `json:"box"`
	Label string     `json:"label"`
	Score float64    `json:"score"`
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
	Similarity float64 `json:"similarity"`
}

// FaceSubItem represents a face sub-target mounted onto a person entity.
type FaceSubItem struct {
	Box        [4]float64      `json:"box"`
	Quality    float64         `json:"quality"`
	Landmarks  [][2]float64    `json:"landmarks"`
	Matched    bool            `json:"matched"`
	PersonID   string          `json:"person_id"`
	PersonName string          `json:"person_name"`
	Similarity float64         `json:"similarity"`
	IsFused    bool            `json:"is_fused"`
	Candidates []FaceCandidate `json:"candidates"`
}

// PersonItem represents a detected person with body box and mounted face items (1:N).
type PersonItem struct {
	TrackID   int64         `json:"track_id"`
	BodyBox   [4]float64    `json:"body_box"`
	BodyScore float64       `json:"body_score"`
	Faces     []FaceSubItem `json:"faces"`
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
}

// NewDetectionResult creates an empty detection inference result.
func NewDetectionResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return InferenceResult{
		CameraID:        cameraID,
		AlgorithmID:     algorithmID,
		PTS:             pts,
		Timestamp:       time.Now().UTC(),
		TaskType:        TaskTypeDetection,
		CostMs:          costMs,
		Detections:      make([]DetectionItem, 0),
		Classifications: make([]ClassificationItem, 0),
		Persons:         make([]PersonItem, 0),
	}
}

// NewClassificationResult creates an empty classification inference result.
func NewClassificationResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return InferenceResult{
		CameraID:        cameraID,
		AlgorithmID:     algorithmID,
		PTS:             pts,
		Timestamp:       time.Now().UTC(),
		TaskType:        TaskTypeClassification,
		CostMs:          costMs,
		Detections:      make([]DetectionItem, 0),
		Classifications: make([]ClassificationItem, 0),
		Persons:         make([]PersonItem, 0),
	}
}

// NewPersonStructureResult creates an empty person-structure inference result.
func NewPersonStructureResult(cameraID, algorithmID string, pts, costMs int64) InferenceResult {
	return InferenceResult{
		CameraID:        cameraID,
		AlgorithmID:     algorithmID,
		PTS:             pts,
		Timestamp:       time.Now().UTC(),
		TaskType:        TaskTypePersonStructure,
		CostMs:          costMs,
		Detections:      make([]DetectionItem, 0),
		Classifications: make([]ClassificationItem, 0),
		Persons:         make([]PersonItem, 0),
	}
}
