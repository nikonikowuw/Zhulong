package algorithm

import (
	"github.com/nikonikowuw/Zhulong/internal/aitypes"
)

// ResultType corresponds to native zhulong_result_type_t in zhulong/plugin.h.
type ResultType = aitypes.ResultType

const (
	ResultTypeUnknown         = aitypes.ResultTypeUnknown
	ResultTypeDetection       = aitypes.ResultTypeDetection
	ResultTypeClassification  = aitypes.ResultTypeClassification
	ResultTypePersonStructure = aitypes.ResultTypePersonStructure
	ResultTypeCustom          = aitypes.ResultTypeCustom
)

// DetectionItem represents a single detected object with normalized coordinates.
type DetectionItem = aitypes.DetectionItem

// ClassificationItem represents a classification output.
type ClassificationItem = aitypes.ClassificationItem

// FaceCandidate represents one candidate match in Top-K face search.
type FaceCandidate = aitypes.FaceCandidate

// FaceSubItem represents a face sub-target mounted onto a person entity.
type FaceSubItem = aitypes.FaceSubItem

// PersonItem represents a detected person with body box and mounted face items (1:N).
type PersonItem = aitypes.PersonItem

// InferenceResult is the unified inference output event payload.
type InferenceResult = aitypes.InferenceResult

// RawInferencePayload models the JSON payload serialized by zhulong_algo_runner.
type RawInferencePayload = aitypes.RawInferencePayload

// Factory functions delegated to aitypes.
var (
	NewDetectionResult       = aitypes.NewDetectionResult
	NewClassificationResult  = aitypes.NewClassificationResult
	NewPersonStructureResult = aitypes.NewPersonStructureResult
	ParseInferenceResult     = aitypes.ParseInferenceResult
)
