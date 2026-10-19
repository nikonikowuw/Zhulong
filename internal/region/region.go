package region

import (
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
)

// Rect represents a bounding box or region in strictly normalized coordinates [x1, y1, x2, y2].
type Rect [4]float64

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
	ROI  []Rect `json:"roi,omitempty"`
	MASK []Rect `json:"mask,omitempty"`
}

// IsEmpty returns true if there are no ROI and no MASK regions defined.
func (c Config) IsEmpty() bool {
	return len(c.ROI) == 0 && len(c.MASK) == 0
}

// Validate checks all rectangles and category count limits in the configuration.
func (c Config) Validate() error {
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
