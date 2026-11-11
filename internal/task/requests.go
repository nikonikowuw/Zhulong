package task

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
	"github.com/nikonikowuw/Zhulong/internal/region"
)

// TaskAlgorithmItem represents an algorithm configuration item in creation or update requests.
//
// 同时接受 camelCase 与 snake_case 两种键名（历史客户端兼容）；camelCase 优先。
type TaskAlgorithmItem struct {
	ID                  string `json:"id,omitempty"`
	AlgorithmID         string `json:"algorithmId"`
	AlgorithmIDAlt      string `json:"algorithm_id,omitempty"`
	Enabled             *bool  `json:"enabled,omitempty"`
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
	ScheduleJSON        string `json:"scheduleJson,omitempty"`
	ScheduleJSONAlt     string `json:"schedule_json,omitempty"`
}

// CreateTaskRequest is the request payload for creating an analysis task.
// Both modern multi-algorithm payloads and legacy single-algorithm payloads are supported.
type CreateTaskRequest struct {
	ID          string              `json:"id,omitempty"`
	CameraID    string              `json:"cameraId"`
	CameraIDAlt string              `json:"camera_id,omitempty"`
	Name        string              `json:"name,omitempty"`
	Enabled     *bool               `json:"enabled,omitempty"`
	Crop        *region.Rect        `json:"crop,omitempty"`
	Algorithms  []TaskAlgorithmItem `json:"algorithms,omitempty"`
	// Legacy single-algorithm fields for backward compatibility
	AlgorithmID         string `json:"algorithmId,omitempty"`
	AlgorithmIDAlt      string `json:"algorithm_id,omitempty"`
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
}

// UpdateTaskRequest is the request payload for updating an analysis task.
type UpdateTaskRequest struct {
	Name       *string             `json:"name,omitempty"`
	Enabled    *bool               `json:"enabled,omitempty"`
	Algorithms []TaskAlgorithmItem `json:"algorithms,omitempty"`
	// Crop 是三态合并字段：省略=不改、null/[]/{}=清除、[x1,y1,x2,y2]=设置。
	//
	// 用 json.RawMessage 而不是 *region.Rect：后者无法区分“字段缺省”与“显式 null”，
	// 而 PUT /tasks/:id 的合并语义必须区分这两者（缺省不得清空已保存的裁剪）。
	Crop json.RawMessage `json:"crop,omitempty" swaggertype:"array,number"`
	// Legacy single-algorithm update fields
	SampleFPS           int    `json:"sampleFps,omitempty"`
	SampleFPSAlt        int    `json:"sample_fps,omitempty"`
	ConfigJSON          string `json:"configJson,omitempty"`
	ConfigJSONAlt       string `json:"config_json,omitempty"`
	RegionConfigJSON    string `json:"regionConfigJson,omitempty"`
	RegionConfigJSONAlt string `json:"region_config_json,omitempty"`
}

// toEntity converts TaskAlgorithmItem to a TaskAlgorithm entity.
//
// 负数 sampleFps 不会被吞成默认值，交由 Service 的 ValidateSampleFPS 拒绝。
func (item TaskAlgorithmItem) toEntity(taskID string) TaskAlgorithm {
	enabled := true
	if item.Enabled != nil {
		enabled = *item.Enabled
	}
	return TaskAlgorithm{
		ID:               item.ID,
		TaskID:           taskID,
		AlgorithmID:      cmp.Or(item.AlgorithmID, item.AlgorithmIDAlt),
		Enabled:          enabled,
		SampleFPS:        cmp.Or(item.SampleFPS, item.SampleFPSAlt, DefaultSampleFPS),
		ConfigJSON:       cmp.Or(item.ConfigJSON, item.ConfigJSONAlt, defaultConfigJSON),
		RegionConfigJSON: cmp.Or(item.RegionConfigJSON, item.RegionConfigJSONAlt, defaultConfigJSON),
		ScheduleJSON:     cmp.Or(item.ScheduleJSON, item.ScheduleJSONAlt, defaultConfigJSON),
	}
}

// toTask converts CreateTaskRequest into a Task entity skeleton,
// resolving camelCase and snake_case alias fields and supporting legacy single-algorithm payloads.
// It returns an error if the crop region is invalid.
func (req *CreateTaskRequest) toTask() (*Task, error) {
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	cropJSON, err := serializeCrop(req.Crop)
	if err != nil {
		return nil, err
	}
	t := &Task{
		ID:       req.ID,
		CameraID: cmp.Or(req.CameraID, req.CameraIDAlt),
		Name:     req.Name,
		Enabled:  enabled,
		CropJSON: cropJSON,
	}

	items := req.Algorithms
	if legacy := req.legacyAlgorithmItem(); len(items) == 0 && legacy != nil {
		items = []TaskAlgorithmItem{*legacy}
	}

	t.Algorithms = make([]TaskAlgorithm, 0, len(items))
	for _, item := range items {
		t.Algorithms = append(t.Algorithms, item.toEntity(t.ID))
	}
	return t, nil
}

// legacyAlgorithmItem 把旧的扁平单算法字段折成一个算法项；没有 legacy 字段时返回 nil。
func (req *CreateTaskRequest) legacyAlgorithmItem() *TaskAlgorithmItem {
	algorithmID := cmp.Or(req.AlgorithmID, req.AlgorithmIDAlt)
	if algorithmID == "" {
		return nil
	}
	return &TaskAlgorithmItem{
		AlgorithmID:         algorithmID,
		Enabled:             req.Enabled,
		SampleFPS:           req.SampleFPS,
		SampleFPSAlt:        req.SampleFPSAlt,
		ConfigJSON:          req.ConfigJSON,
		ConfigJSONAlt:       req.ConfigJSONAlt,
		RegionConfigJSON:    req.RegionConfigJSON,
		RegionConfigJSONAlt: req.RegionConfigJSONAlt,
	}
}

// apply 先校验三态 crop，再将更新字段一次性合并进目标任务实体。
// 校验失败时 t 的任何字段都不会被改动。
func (req *UpdateTaskRequest) apply(t *Task) error {
	if t == nil {
		return nil
	}
	cropJSON, hasCrop, err := req.resolveCrop()
	if err != nil {
		return err
	}
	if hasCrop {
		t.CropJSON = cropJSON
	}
	if req.Name != nil {
		t.Name = *req.Name
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Algorithms != nil {
		t.Algorithms = make([]TaskAlgorithm, 0, len(req.Algorithms))
		for _, item := range req.Algorithms {
			t.Algorithms = append(t.Algorithms, item.toEntity(t.ID))
		}
		return nil
	}
	// 兼容旧的扁平单算法字段：只在任务恰好挂了一个算法子项时生效。
	if len(t.Algorithms) == 1 {
		req.applyLegacyFields(&t.Algorithms[0])
		return nil
	}
	if req.hasLegacyAlgorithmFields() && len(t.Algorithms) > 1 {
		return apperr.Invalid(
			"INVALID_REQUEST",
			"cannot apply flat algorithm fields to a multi-algorithm task; specify algorithms array instead",
			nil,
		)
	}
	return nil
}

// hasLegacyAlgorithmFields reports whether any flat single-algorithm update fields were provided.
func (req *UpdateTaskRequest) hasLegacyAlgorithmFields() bool {
	return req.SampleFPS != 0 ||
		req.SampleFPSAlt != 0 ||
		req.ConfigJSON != "" ||
		req.ConfigJSONAlt != "" ||
		req.RegionConfigJSON != "" ||
		req.RegionConfigJSONAlt != ""
}

func (req *UpdateTaskRequest) resolveCrop() (string, bool, error) {
	trimmed := bytes.TrimSpace(req.Crop)
	if len(trimmed) == 0 {
		return "", false, nil
	}
	// 显式清空：null / [] / {}。
	switch string(trimmed) {
	case "null", "[]", "{}":
		return defaultConfigJSON, true, nil
	}
	crop, err := region.ParseCrop(string(trimmed))
	if err != nil {
		return "", false, apperr.Invalid(
			"INVALID_CROP_CONFIG",
			fmt.Sprintf("invalid crop config: %v", err),
			err,
		)
	}
	cropJSON, err := serializeCrop(crop)
	if err != nil {
		return "", false, err
	}
	return cropJSON, true, nil
}

// applyLegacyFields 把扁平单算法字段合并进既有子项，只覆盖调用方显式给出的字段。
func (req *UpdateTaskRequest) applyLegacyFields(sub *TaskAlgorithm) {
	if fps := cmp.Or(req.SampleFPS, req.SampleFPSAlt); fps != 0 {
		sub.SampleFPS = fps
	}
	if cfg := cmp.Or(req.ConfigJSON, req.ConfigJSONAlt); cfg != "" {
		sub.ConfigJSON = cfg
	}
	if regionCfg := cmp.Or(req.RegionConfigJSON, req.RegionConfigJSONAlt); regionCfg != "" {
		sub.RegionConfigJSON = regionCfg
	}
}

// serializeCrop 把裁剪矩形序列化为存储形态；nil / 整帧统一为 '{}'。
func serializeCrop(crop *region.Rect) (string, error) {
	if crop == nil {
		return defaultConfigJSON, nil
	}
	if err := region.ValidateCrop(*crop); err != nil {
		return "", apperr.Invalid(
			"INVALID_CROP_CONFIG",
			fmt.Sprintf("invalid crop config: %v", err),
			err,
		)
	}
	if crop.IsIdentity() {
		return defaultConfigJSON, nil
	}
	payload, err := json.Marshal(struct {
		Crop *region.Rect `json:"crop"`
	}{Crop: crop})
	if err != nil {
		return "", apperr.Invalid(
			"INVALID_CROP_CONFIG",
			fmt.Sprintf("invalid crop config: %v", err),
			err,
		)
	}
	return string(payload), nil
}
