package region

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	MaxTripwires  = 8
	MinLineLength = 0.01
)

var tripwireIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Point is a full-frame normalized coordinate, independent of the input crop.
type Point [2]float64

func (p *Point) UnmarshalJSON(data []byte) error {
	var values []float64
	if err := json.Unmarshal(data, &values); err != nil {
		return fmt.Errorf("unmarshal point: %w", err)
	}
	if len(values) != 2 {
		return errors.New("point requires exactly two coordinates")
	}
	*p = Point{values[0], values[1]}
	return nil
}

// Tripwire names a finite line segment. In screen coordinates, negative cross
// product relative to Points[0] -> Points[1] is side A, positive is side B.
type Tripwire struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	Points    []Point `json:"points"`
	Direction string  `json:"direction"`
}

func (t Tripwire) Validate() error {
	if !tripwireIDPattern.MatchString(t.ID) {
		return errors.New("invalid tripwire id")
	}
	if strings.TrimSpace(t.Name) == "" || utf8.RuneCountInString(t.Name) > 64 {
		return errors.New("tripwire name requires 1 to 64 characters")
	}
	switch t.Direction {
	case "a_to_b", "b_to_a", "both":
		// valid direction
	default:
		return errors.New("invalid tripwire direction")
	}
	if len(t.Points) != 2 {
		return errors.New("tripwire requires two endpoints")
	}
	for _, p := range t.Points {
		for _, v := range p {
			isNonFinite := math.IsNaN(v) || math.IsInf(v, 0)
			isOutOfRange := v < 0 || v > 1
			if isNonFinite || isOutOfRange {
				return errors.New("invalid tripwire coordinate")
			}
		}
	}
	if math.Hypot(t.Points[1][0]-t.Points[0][0], t.Points[1][1]-t.Points[0][1]) < MinLineLength-1e-9 {
		return errors.New("tripwire is too short")
	}
	return nil
}

func validateTripwires(lines []Tripwire) error {
	if len(lines) > MaxTripwires {
		return errors.New("too many tripwires")
	}
	seen := make(map[string]bool, len(lines))
	for i, line := range lines {
		if err := line.Validate(); err != nil {
			return fmt.Errorf("tripwires[%d]: %w", i, err)
		}
		if seen[line.ID] {
			return errors.New("duplicate tripwire id")
		}
		seen[line.ID] = true
	}
	return nil
}
