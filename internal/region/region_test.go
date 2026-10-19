package region

import (
	"errors"
	"math"
	"testing"
)

func assertInDelta(t *testing.T, expected, actual, delta float64) {
	t.Helper()
	if math.Abs(expected-actual) > delta {
		t.Fatalf("expected %v, got %v (delta %v)", expected, actual, delta)
	}
}

func assertTrue(t *testing.T, condition bool) {
	t.Helper()
	if !condition {
		t.Fatalf("expected true, got false")
	}
}

func assertFalse(t *testing.T, condition bool) {
	t.Helper()
	if condition {
		t.Fatalf("expected false, got true")
	}
}

func assertNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func assertError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestRect_Validation(t *testing.T) {
	tests := []struct {
		name    string
		rect    Rect
		wantErr error
	}{
		{
			name: "valid rect",
			rect: Rect{0.1, 0.2, 0.5, 0.8},
		},
		{
			name: "exact boundary [0, 0, 1, 1]",
			rect: Rect{0.0, 0.0, 1.0, 1.0},
		},
		{
			name: "minimum side length 0.01",
			rect: Rect{0.1, 0.1, 0.11, 0.11},
		},
		{
			name:    "negative coordinate",
			rect:    Rect{-0.01, 0.1, 0.5, 0.5},
			wantErr: ErrInvalidCoordinate,
		},
		{
			name:    "coordinate > 1.0",
			rect:    Rect{0.1, 0.1, 1.01, 0.5},
			wantErr: ErrInvalidCoordinate,
		},
		{
			name:    "degenerate x1 >= x2",
			rect:    Rect{0.5, 0.1, 0.5, 0.5},
			wantErr: ErrDegenerateRect,
		},
		{
			name:    "degenerate y1 >= y2",
			rect:    Rect{0.1, 0.6, 0.5, 0.5},
			wantErr: ErrDegenerateRect,
		},
		{
			name:    "width too small (< 0.01)",
			rect:    Rect{0.1, 0.1, 0.105, 0.5},
			wantErr: ErrRectTooSmall,
		},
		{
			name:    "height too small (< 0.01)",
			rect:    Rect{0.1, 0.1, 0.5, 0.105},
			wantErr: ErrRectTooSmall,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rect.Validate()
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
			} else {
				assertNoError(t, err)
			}
		})
	}
}

func TestRect_BottomCenterAndContains(t *testing.T) {
	rect := Rect{0.2, 0.3, 0.6, 0.9}
	bx, by := rect.BottomCenter()
	assertInDelta(t, 0.4, bx, 1e-6)
	assertInDelta(t, 0.9, by, 1e-6)

	assertTrue(t, rect.Contains(0.4, 0.9))
	assertTrue(t, rect.Contains(0.2, 0.3)) // top-left boundary
	assertTrue(t, rect.Contains(0.6, 0.9)) // bottom-right boundary
	assertFalse(t, rect.Contains(0.19, 0.5))
	assertFalse(t, rect.Contains(0.4, 0.91))
}

func TestConfig_Validation(t *testing.T) {
	t.Run("empty config valid", func(t *testing.T) {
		cfg := Config{}
		assertNoError(t, cfg.Validate())
		assertTrue(t, cfg.IsEmpty())
	})

	t.Run("too many ROI", func(t *testing.T) {
		cfg := Config{
			ROI: make([]Rect, 9),
		}
		for i := 0; i < 9; i++ {
			cfg.ROI[i] = Rect{0.1, 0.1, 0.2, 0.2}
		}
		err := cfg.Validate()
		assertTrue(t, errors.Is(err, ErrTooManyRects))
	})

	t.Run("too many MASK", func(t *testing.T) {
		cfg := Config{
			MASK: make([]Rect, 9),
		}
		for i := 0; i < 9; i++ {
			cfg.MASK[i] = Rect{0.1, 0.1, 0.2, 0.2}
		}
		err := cfg.Validate()
		assertTrue(t, errors.Is(err, ErrTooManyRects))
	})

	t.Run("invalid inner rect in ROI", func(t *testing.T) {
		cfg := Config{
			ROI: []Rect{{0.5, 0.1, 0.4, 0.5}},
		}
		err := cfg.Validate()
		assertError(t, err)
		assertTrue(t, errors.Is(err, ErrDegenerateRect))
	})
}

func TestParseConfig(t *testing.T) {
	t.Run("empty string or {}", func(t *testing.T) {
		c1, err := ParseConfig("")
		assertNoError(t, err)
		assertTrue(t, c1.IsEmpty())

		c2, err := ParseConfig("{}")
		assertNoError(t, err)
		assertTrue(t, c2.IsEmpty())
	})

	t.Run("valid json", func(t *testing.T) {
		raw := `{"roi": [[0.1, 0.2, 0.5, 0.9]], "mask": [[0.3, 0.6, 0.4, 0.75]]}`
		cfg, err := ParseConfig(raw)
		assertNoError(t, err)
		assertFalse(t, cfg.IsEmpty())
		if len(cfg.ROI) != 1 {
			t.Fatalf("expected 1 roi, got %d", len(cfg.ROI))
		}
		if len(cfg.MASK) != 1 {
			t.Fatalf("expected 1 mask, got %d", len(cfg.MASK))
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseConfig(`{"roi": "invalid"}`)
		assertError(t, err)
	})

	t.Run("validation failed on parse", func(t *testing.T) {
		raw := `{"roi": [[0.8, 0.2, 0.5, 0.9]]}`
		_, err := ParseConfig(raw)
		assertError(t, err)
		assertTrue(t, errors.Is(err, ErrDegenerateRect))
	})
}
