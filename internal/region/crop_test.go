package region

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseCrop(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *Rect
		wantErr error
	}{
		{name: "empty string", raw: ""},
		{name: "blank", raw: "   \n\t"},
		{name: "empty object", raw: "{}"},
		{name: "json null", raw: "null"},
		{name: "top level empty array", raw: "[]"},
		{name: "null crop", raw: `{"crop":null}`},
		{name: "empty crop array", raw: `{"crop":[]}`},
		{name: "unknown keys only", raw: `{"legacy":true}`},
		{name: "full frame normalizes to nil", raw: `{"crop":[0,0,1,1]}`},
		{name: "top level array alias", raw: `[0.1,0.2,0.5,0.6]`, want: &Rect{0.1, 0.2, 0.5, 0.6}},
		{name: "object crop", raw: `{"crop":[0.1,0.2,0.5,0.6]}`, want: &Rect{0.1, 0.2, 0.5, 0.6}},
		{name: "unknown keys ignored", raw: `{"crop":[0.1,0.2,0.5,0.6],"x":1}`, want: &Rect{0.1, 0.2, 0.5, 0.6}},
		{name: "three values", raw: `{"crop":[0.1,0.2,0.5]}`, wantErr: ErrCropShape},
		{name: "five values", raw: `{"crop":[0.1,0.2,0.5,0.6,0.7]}`, wantErr: ErrCropShape},
		{name: "degenerate", raw: `{"crop":[0.5,0.2,0.5,0.6]}`, wantErr: ErrDegenerateRect},
		{name: "out of range", raw: `{"crop":[-0.1,0,0.5,0.5]}`, wantErr: ErrInvalidCoordinate},
		{name: "too small", raw: `{"crop":[0.1,0.1,0.115,0.115]}`, wantErr: ErrRectTooSmall},
		{name: "invalid json", raw: `{"crop":`, wantErr: errAny},
		{name: "non array crop", raw: `{"crop":42}`, wantErr: errAny},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCrop(tt.raw)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("expected error, got %v", got)
				}
				if tt.wantErr != errAny && !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected error %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("expected %v, got %v", *tt.want, got)
			}
		})
	}
}

// errAny 表示“只关心是否报错、不关心具体哨兵”的用例。
var errAny = errors.New("any error")

func TestValidateCrop(t *testing.T) {
	tests := []struct {
		name string
		rect Rect
		ok   bool
	}{
		{name: "valid", rect: Rect{0.1, 0.2, 0.5, 0.8}, ok: true},
		{name: "minimum side", rect: Rect{0.1, 0.1, 0.12, 0.12}, ok: true},
		{name: "full frame", rect: Rect{0, 0, 1, 1}, ok: true},
		{name: "NaN", rect: Rect{math.NaN(), 0, 1, 1}},
		{name: "Inf", rect: Rect{0, 0, math.Inf(1), 1}},
		{name: "below minimum", rect: Rect{0.1, 0.1, 0.119, 0.2}},
		{name: "degenerate", rect: Rect{0.5, 0.1, 0.5, 0.9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateCrop(tt.rect)
			if tt.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatalf("expected error, got nil")
			}
		})
	}
}

func TestRectUnmarshalJSONShape(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Rect
		wantErr bool
	}{
		{name: "four values", raw: `[0.1,0.2,0.3,0.4]`, want: Rect{0.1, 0.2, 0.3, 0.4}},
		{name: "three values", raw: `[0.1,0.2,0.3]`, wantErr: true},
		{name: "five values", raw: `[0.1,0.2,0.3,0.4,0.5]`, wantErr: true},
		{name: "null", raw: `null`, wantErr: true},
		{name: "object", raw: `{"x":1}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rect Rect
			err := json.Unmarshal([]byte(tt.raw), &rect)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %v", rect)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if rect != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, rect)
			}
		})
	}
}
