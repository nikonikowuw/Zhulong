package region

import (
	"math"
	"strings"
	"testing"
)

func TestTripwireConfig(t *testing.T) {
	valid := `{"tripwires":[{"id":"entry","name":"入口","points":[[0.2,0.5],[0.8,0.5]],"direction":"a_to_b"}]}`
	cfg, err := ParseConfig(valid)
	if err != nil || cfg.IsEmpty() || len(cfg.Tripwires) != 1 {
		t.Fatalf("parse: %+v %v", cfg, err)
	}
	for _, raw := range []string{
		`{"tripwires":[{"id":"x","name":"x","points":[[0,0],[0,0]],"direction":"both"}]}`,
		`{"tripwires":[{"id":"x","name":"x","points":[[0,0,0],[1,1]],"direction":"both"}]}`,
		`{"tripwires":[{"id":"x","name":"x","points":[[0,0],[1,1]],"direction":"forward"}]}`,
		`{"tripwires":[{"id":"x","name":"x","points":[[-0.1,0],[1,1]],"direction":"both"}]}`,
	} {
		if _, err := ParseConfig(raw); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	line := cfg.Tripwires[0]
	if err := validateTripwires([]Tripwire{line, line}); err == nil {
		t.Error("accepted duplicate IDs")
	}
	if err := validateTripwires(make([]Tripwire, 9)); err == nil {
		t.Error("accepted too many lines")
	}
}

func TestTripwireValidationBoundaries(t *testing.T) {
	valid := Tripwire{ID: "entry", Name: "入口", Points: []Point{{0, 0}, {0.01, 0}}, Direction: "both"}
	tests := []struct {
		name   string
		mutate func(*Tripwire)
		valid  bool
	}{
		{
			name:   "minimum length",
			mutate: func(*Tripwire) {},
			valid:  true,
		},
		{
			name: "vertical",
			mutate: func(line *Tripwire) {
				line.Points = []Point{{1, 0}, {1, 1}}
			},
			valid: true,
		},
		{
			name: "reverse endpoints",
			mutate: func(line *Tripwire) {
				line.Points = []Point{{1, 1}, {0, 0}}
			},
			valid: true,
		},
		{
			name:   "empty id",
			mutate: func(line *Tripwire) { line.ID = "" },
			valid:  false,
		},
		{
			name:   "unsafe id",
			mutate: func(line *Tripwire) { line.ID = "with space" },
			valid:  false,
		},
		{
			name:   "empty name",
			mutate: func(line *Tripwire) { line.Name = "  " },
			valid:  false,
		},
		{
			name: "missing endpoint",
			mutate: func(line *Tripwire) {
				line.Points = []Point{{0, 0}}
			},
			valid: false,
		},
		{
			name: "NaN",
			mutate: func(line *Tripwire) {
				line.Points = []Point{{math.NaN(), 0}, {1, 1}}
			},
			valid: false,
		},
		{
			name: "infinity",
			mutate: func(line *Tripwire) {
				line.Points = []Point{{0, 0}, {math.Inf(1), 1}}
			},
			valid: false,
		},
		{
			name:   "long name",
			mutate: func(line *Tripwire) { line.Name = strings.Repeat("线", 65) },
			valid:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			line := valid
			tt.mutate(&line)
			err := line.Validate()
			if (err == nil) != tt.valid {
				t.Fatalf("Validate() = %v, valid=%v", err, tt.valid)
			}
		})
	}
}
