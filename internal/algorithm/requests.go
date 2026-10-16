package algorithm

// CreateTaskRequest is the request payload for creating an analysis task.
//
// Both camelCase and snake_case keys are accepted: the snake_case alias is used
// only when the camelCase key is absent, so legacy clients keep working without
// silently overriding the canonical field.
type CreateTaskRequest struct {
	ID             string `json:"id"`
	CameraID       string `json:"cameraId"`
	CameraIDAlt    string `json:"camera_id"`
	AlgorithmID    string `json:"algorithmId"`
	AlgorithmIDAlt string `json:"algorithm_id"`
	SampleFPS      int    `json:"sampleFps"`
	SampleFPSAlt   int    `json:"sample_fps"`
	ConfigJSON     string `json:"configJson"`
	ConfigJSONAlt  string `json:"config_json"`
}

// ToTask converts CreateTaskRequest into an AnalysisTask entity skeleton,
// resolving camelCase and snake_case alias fields.
func (req *CreateTaskRequest) ToTask() *AnalysisTask {
	camID := req.CameraID
	if camID == "" {
		camID = req.CameraIDAlt
	}
	algoID := req.AlgorithmID
	if algoID == "" {
		algoID = req.AlgorithmIDAlt
	}
	fps := req.SampleFPS
	if fps <= 0 {
		fps = req.SampleFPSAlt
	}
	cfgJSON := req.ConfigJSON
	if cfgJSON == "" {
		cfgJSON = req.ConfigJSONAlt
	}

	return &AnalysisTask{
		ID:          req.ID,
		CameraID:    camID,
		AlgorithmID: algoID,
		SampleFPS:   fps,
		ConfigJSON:  cfgJSON,
		Enabled:     true,
	}
}
