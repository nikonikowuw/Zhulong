package region

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const (
	// MaxRectsPerCategory defines the maximum number of rectangles per ROI/MASK category.
	MaxRectsPerCategory = 8
	// MinSideLength defines the minimum width and height of a rectangle in normalized space.
	MinSideLength = 0.01
)

var (
	ErrTooManyRects      = errors.New("number of rectangles exceeds category limit")
	ErrInvalidCoordinate = errors.New("coordinate component outside [0.0, 1.0]")
	ErrDegenerateRect    = errors.New("degenerate rectangle: x1 >= x2 or y1 >= y2")
	ErrRectTooSmall      = errors.New("rectangle width or height smaller than minimum side length")
	// ErrRectShape 是**通用**矩形解码错误：ROI/MASK 与裁剪窗口都会命中它。
	// 裁剪专用的 ErrCropShape（internal/region/crop.go）只描述 crop 文档形状，
	// 两者不能混用——否则 ROI 配置报错时会显示「crop must be ...」，排查方向被带偏。
	ErrRectShape = errors.New("rectangle must be an array of exactly four numbers")
)

// Rect represents a bounding box or region in strictly normalized coordinates [x1, y1, x2, y2].
type Rect [4]float64

// Identity returns the full-frame rect.
func Identity() Rect { return Rect{0, 0, 1, 1} }

// IsIdentity reports whether the rect is exactly the full frame.
//
// Bit-exact comparison, no epsilon: every producer of a full-frame value (engine reply,
// normalized config) uses Identity() verbatim. Tolerating "almost full frame" would
// silently skip a reprojection, which is worse than a slightly-off crop.
func (r Rect) IsIdentity() bool { return r == Identity() }

// UnmarshalJSON 要求**恰好 4 个数字**。
//
// 不能依赖 Go 数组的默认解码语义：少元素会静默补零、多元素会静默截断，
// 两种都会把畸形配置变成另一个矩形。`null` 不是合法矩形（清空语义由上层的三态
// 请求字段 / ParseCrop 负责）。
func (r *Rect) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return fmt.Errorf("%w: null is not a rectangle", ErrRectShape)
	}
	var values []float64
	if err := json.Unmarshal(trimmed, &values); err != nil {
		return fmt.Errorf("unmarshal rect: %w", err)
	}
	if len(values) != 4 {
		return fmt.Errorf("%w: got %d value(s)", ErrRectShape, len(values))
	}
	*r = Rect{values[0], values[1], values[2], values[3]}
	return nil
}

// X1 returns the left coordinate.
func (r Rect) X1() float64 { return r[0] }

// Y1 returns the top coordinate.
func (r Rect) Y1() float64 { return r[1] }

// X2 returns the right coordinate.
func (r Rect) X2() float64 { return r[2] }

// Y2 returns the bottom coordinate.
func (r Rect) Y2() float64 { return r[3] }

// Validate verifies normalization, non-degeneracy, and minimum side length.
func (r Rect) Validate() error {
	for i := 0; i < 4; i++ {
		if r[i] < 0.0 || r[i] > 1.0 {
			return fmt.Errorf("%w: component[%d]=%v", ErrInvalidCoordinate, i, r[i])
		}
	}
	if r[0] >= r[2] || r[1] >= r[3] {
		return fmt.Errorf("%w: [%v, %v, %v, %v]", ErrDegenerateRect, r[0], r[1], r[2], r[3])
	}
	// Use epsilon for floating point comparison tolerance
	const eps = 1e-9
	if (r[2]-r[0]) < MinSideLength-eps || (r[3]-r[1]) < MinSideLength-eps {
		return fmt.Errorf("%w: width=%v height=%v min=%v", ErrRectTooSmall, r[2]-r[0], r[3]-r[1], MinSideLength)
	}
	return nil
}

// BottomCenter returns the bottom-center point ((x1 + x2) / 2, y2) of the rectangle.
func (r Rect) BottomCenter() (float64, float64) {
	return (r[0] + r[2]) / 2.0, r[3]
}

// Contains checks if point (px, py) is inside or on the boundaries of Rect.
func (r Rect) Contains(px, py float64) bool {
	return px >= r[0] && px <= r[2] && py >= r[1] && py <= r[3]
}

// Config defines the ROI and MASK region policy for a task.
type Config struct {
	ROI       []Rect     `json:"roi,omitempty"`
	MASK      []Rect     `json:"mask,omitempty"`
	Tripwires []Tripwire `json:"tripwires,omitempty"`
}

// IsEmpty returns true if there are no ROI and no MASK regions defined.
func (c Config) IsEmpty() bool {
	return len(c.ROI) == 0 && len(c.MASK) == 0 && len(c.Tripwires) == 0
}

// Validate checks all rectangles and category count limits in the configuration.
func (c Config) Validate() error {
	if err := validateTripwires(c.Tripwires); err != nil {
		return err
	}
	if len(c.ROI) > MaxRectsPerCategory {
		return fmt.Errorf("%w: roi count %d > %d", ErrTooManyRects, len(c.ROI), MaxRectsPerCategory)
	}
	if len(c.MASK) > MaxRectsPerCategory {
		return fmt.Errorf("%w: mask count %d > %d", ErrTooManyRects, len(c.MASK), MaxRectsPerCategory)
	}
	for i, r := range c.ROI {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("roi[%d]: %w", i, err)
		}
	}
	for i, r := range c.MASK {
		if err := r.Validate(); err != nil {
			return fmt.Errorf("mask[%d]: %w", i, err)
		}
	}
	return nil
}

// ParseConfig parses a JSON string into a Config. An empty or blank string produces an empty Config.
func ParseConfig(raw string) (Config, error) {
	if raw == "" || raw == "{}" {
		return Config{}, nil
	}
	var cfg Config
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return Config{}, fmt.Errorf("unmarshal region config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
