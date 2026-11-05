package region

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// MinCropSide 是任务级裁剪窗口的最小边长（归一化）。
//
// 刻意不复用 ROI/MASK 的 MinSideLength（0.01）：裁剪窗口会被算法端放大到模型输入，
// 太小的窗口只剩无意义插值，且会让“画得出来、后端 400”的阈值分裂。
const MinCropSide = 0.02

var (
	// ErrCropShape 表示裁剪配置不是“恰好 4 个数字”的数组形状。
	ErrCropShape = errors.New("crop must be an array of exactly four numbers")
	// ErrCropNonFinite 表示裁剪坐标含 NaN/Inf（只能来自程序化调用，JSON 无法表达）。
	ErrCropNonFinite = errors.New("crop contains a non-finite number")
)

// ViewState 是引擎回报的生效窗口：Since 之前发布的帧属于上一代窗口。
//
// Since 的零值表示“尚未发生过窗口切换”，此时 Rect 通常是 Identity。
type ViewState struct {
	Rect  Rect
	Since time.Time
}

// ParseCrop 解析 analysis_tasks.crop_json 的存储形态。
//
// 存储/响应/引擎入口的 null / 缺省 / [] / {} 一律等价于“不裁剪”，
// 返回 (nil, nil)；整帧 [0,0,1,1] 同样归一化为 nil。
// 未知键忽略（向前兼容）。形状错误（长度 != 4）与坐标非法返回 error。
func ParseCrop(raw string) (*Rect, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" || trimmed == "null" {
		return nil, nil
	}

	// 顶层 4 元数组是 {"crop":[...]} 的同义别名（手工 SQL / 旧格式兜底）。
	if strings.HasPrefix(trimmed, "[") {
		return parseCropArray([]byte(trimmed))
	}

	var payload struct {
		Crop json.RawMessage `json:"crop"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, fmt.Errorf("unmarshal crop config: %w", err)
	}
	// 没有 crop 键（含只有未知键）与 {"crop":null} 都落到这里。
	if len(payload.Crop) == 0 {
		return nil, nil
	}
	return parseCropArray(payload.Crop)
}

// parseCropArray 解析一个裁剪数组；null 与空数组表示“不裁剪”。
func parseCropArray(raw []byte) (*Rect, error) {
	trimmed := bytes.TrimSpace(raw)
	if bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) {
		return nil, nil
	}

	var values []float64
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return nil, fmt.Errorf("unmarshal crop rect: %w", err)
	}
	if len(values) != 4 {
		return nil, fmt.Errorf("%w: got %d value(s)", ErrCropShape, len(values))
	}

	rect := Rect{values[0], values[1], values[2], values[3]}
	if err := ValidateCrop(rect); err != nil {
		return nil, err
	}
	if rect.IsIdentity() {
		return nil, nil
	}
	return &rect, nil
}

// ValidateCrop 校验裁剪矩形：有限、严格归一化、非退化、边长 >= MinCropSide。
//
// ⚠️ 不要复用 Rect.Validate()：那里写死 MinSideLength=0.01，且不显式拒绝 NaN/Inf。
// 阈值权威只在 Go（HTTP / 存储 / 引擎入口共用本函数）；宿主 C ABI 只做结构性校验，
// 不在 float32 上比较 0.02（0.02f ≈ 0.0199999996，边界值会被误拒）。
func ValidateCrop(r Rect) error {
	for i, v := range r {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("%w: component[%d]=%v", ErrCropNonFinite, i, v)
		}
		if v < 0.0 || v > 1.0 {
			return fmt.Errorf("%w: component[%d]=%v", ErrInvalidCoordinate, i, v)
		}
	}
	if r[0] >= r[2] || r[1] >= r[3] {
		return fmt.Errorf("%w: [%v, %v, %v, %v]", ErrDegenerateRect, r[0], r[1], r[2], r[3])
	}
	// 与 Rect.Validate 使用同一 epsilon 口径，避免浮点比较在边界上抖动。
	const eps = 1e-9
	if (r[2]-r[0]) < MinCropSide-eps || (r[3]-r[1]) < MinCropSide-eps {
		return fmt.Errorf("%w: width=%v height=%v min=%v", ErrRectTooSmall, r[2]-r[0], r[3]-r[1], MinCropSide)
	}
	return nil
}
