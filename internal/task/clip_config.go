package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 事件录像片段配置的取值范围与默认值（每个 task_algorithm 一份，存于 clip_config_json）。
const (
	// ClipPreSecondsMin / ClipPreSecondsMax 是事件前段时长的合法范围（秒）。
	ClipPreSecondsMin = 1
	ClipPreSecondsMax = 15
	// ClipPostSecondsMin / ClipPostSecondsMax 是事件后段时长的合法范围（秒）。
	ClipPostSecondsMin = 1
	ClipPostSecondsMax = 60
	// DefaultClipPreSeconds / DefaultClipPostSeconds 是字段缺省时的默认值。
	DefaultClipPreSeconds  = 5
	DefaultClipPostSeconds = 10
)

// 识别事件的录像触发过滤。
const (
	// ClipFilterAll 表示所有识别事件都触发（默认）。
	ClipFilterAll = "all"
	// ClipFilterStranger 表示只有未匹配（陌生人）的识别事件触发。
	ClipFilterStranger = "stranger"
	// ClipFilterKnown 表示只有已匹配底库人员的识别事件触发。
	ClipFilterKnown = "known"
)

// errInvalidClipConfig 表示录像配置 JSON 无法解析或取值越界。
var errInvalidClipConfig = errors.New("invalid clip configuration")

// ClipConfig 是单个任务算法的事件录像配置。
//
// 存储形态是 JSON 对象（camelCase 键）；空串或 "{}" 表示关闭录像，
// 此时 PreSeconds / PostSeconds / RecognitionFilter 取默认值。
type ClipConfig struct {
	Enabled           bool   `json:"enabled"`
	PreSeconds        int    `json:"preSeconds"`
	PostSeconds       int    `json:"postSeconds"`
	RecognitionFilter string `json:"recognitionFilter"`
}

// DefaultClipConfig 返回关闭状态的默认录像配置。
func DefaultClipConfig() ClipConfig {
	return ClipConfig{
		Enabled:           false,
		PreSeconds:        DefaultClipPreSeconds,
		PostSeconds:       DefaultClipPostSeconds,
		RecognitionFilter: ClipFilterAll,
	}
}

// ParseClipConfig 解析并校验 clip_config_json，缺省字段补默认值。
//
// 空串 / 纯空白 / "{}" / "null" 视为关闭。未知键被拒绝，避免前端拼错键名后静默不生效。
// 越界值不做静默纠正，直接返回错误（与 sample_fps 的取舍一致）。
// 返回的错误包装 errInvalidClipConfig，由 Service 映射为 422 INVALID_CLIP_CONFIG。
func ParseClipConfig(raw string) (ClipConfig, error) {
	cfg := DefaultClipConfig()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "null" {
		return cfg, nil
	}

	var doc struct {
		Enabled           *bool   `json:"enabled"`
		PreSeconds        *int    `json:"preSeconds"`
		PostSeconds       *int    `json:"postSeconds"`
		RecognitionFilter *string `json:"recognitionFilter"`
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(trimmed)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&doc); err != nil {
		return ClipConfig{}, fmt.Errorf("%w: %v", errInvalidClipConfig, err)
	}
	if dec.More() {
		return ClipConfig{}, fmt.Errorf("%w: trailing data after JSON object", errInvalidClipConfig)
	}

	if doc.Enabled != nil {
		cfg.Enabled = *doc.Enabled
	}
	if doc.PreSeconds != nil {
		if *doc.PreSeconds < ClipPreSecondsMin || *doc.PreSeconds > ClipPreSecondsMax {
			return ClipConfig{}, fmt.Errorf(
				"%w: preSeconds %d out of range [%d, %d]",
				errInvalidClipConfig, *doc.PreSeconds, ClipPreSecondsMin, ClipPreSecondsMax,
			)
		}
		cfg.PreSeconds = *doc.PreSeconds
	}
	if doc.PostSeconds != nil {
		if *doc.PostSeconds < ClipPostSecondsMin || *doc.PostSeconds > ClipPostSecondsMax {
			return ClipConfig{}, fmt.Errorf(
				"%w: postSeconds %d out of range [%d, %d]",
				errInvalidClipConfig, *doc.PostSeconds, ClipPostSecondsMin, ClipPostSecondsMax,
			)
		}
		cfg.PostSeconds = *doc.PostSeconds
	}
	if doc.RecognitionFilter != nil {
		switch *doc.RecognitionFilter {
		case "":
			// 空串按缺省处理。
		case ClipFilterAll, ClipFilterStranger, ClipFilterKnown:
			cfg.RecognitionFilter = *doc.RecognitionFilter
		default:
			return ClipConfig{}, fmt.Errorf(
				"%w: recognitionFilter %q must be one of all|stranger|known",
				errInvalidClipConfig, *doc.RecognitionFilter,
			)
		}
	}
	return cfg, nil
}

// AllowsRecognition 报告识别事件（按是否匹配底库）是否满足过滤条件。
// 告警事件不经过该过滤。
func (c ClipConfig) AllowsRecognition(matched bool) bool {
	switch c.RecognitionFilter {
	case ClipFilterStranger:
		return !matched
	case ClipFilterKnown:
		return matched
	default:
		return true
	}
}

// PreMS 返回前段时长（毫秒）。
func (c ClipConfig) PreMS() int64 { return int64(c.PreSeconds) * 1000 }

// PostMS 返回后段时长（毫秒）。
func (c ClipConfig) PostMS() int64 { return int64(c.PostSeconds) * 1000 }
