package schedule

import (
	"testing"
	"time"
)

func TestParseAndNormalize(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectError   bool
		alwaysActive  bool
		expectedDays  int
		expectedRange string
	}{
		{
			name:         "empty string yields always active",
			input:        "",
			alwaysActive: true,
		},
		{
			name:         "empty json yields always active",
			input:        "{}",
			alwaysActive: true,
		},
		{
			name:         "disabled schedule yields always active",
			input:        `{"enabled": false, "days": {"monday": [{"start": "08:00", "end": "18:00"}]}}`,
			alwaysActive: true,
		},
		{
			name:          "valid schedule normalizes and merges contiguous",
			input:         `{"enabled": true, "days": {"monday": [{"start": "08:00", "end": "10:00"}, {"start": "10:00", "end": "12:00"}]}}`,
			alwaysActive:  false,
			expectedDays:  1,
			expectedRange: "08:00-12:00",
		},
		{
			name:          "24:00 bound is allowed and canonicalized",
			input:         `{"enabled": true, "days": {"friday": [{"start": "20:00", "end": "24:00"}]}}`,
			alwaysActive:  false,
			expectedDays:  1,
			expectedRange: "20:00-24:00",
		},
		{
			name:        "invalid time format",
			input:       `{"enabled": true, "days": {"monday": [{"start": "25:00", "end": "26:00"}]}}`,
			expectError: true,
		},
		{
			name:        "start after end",
			input:       `{"enabled": true, "days": {"monday": [{"start": "18:00", "end": "08:00"}]}}`,
			expectError: true,
		},
		{
			name:        "invalid day key",
			input:       `{"enabled": true, "days": {"someday": [{"start": "08:00", "end": "18:00"}]}}`,
			expectError: true,
		},
		{
			name:          "alternative day key alias",
			input:         `{"enabled": true, "days": {"mon": [{"start": "08:00", "end": "18:00"}]}}`,
			alwaysActive:  false,
			expectedDays:  1,
			expectedRange: "08:00-18:00",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Parse(tc.input)
			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if s.IsAlwaysActive() != tc.alwaysActive {
				t.Errorf("expected IsAlwaysActive=%v, got %v", tc.alwaysActive, s.IsAlwaysActive())
			}
			if tc.expectedDays > 0 {
				if len(s.Days) != tc.expectedDays {
					t.Errorf("expected %d days, got %d", tc.expectedDays, len(s.Days))
				}
				for _, ranges := range s.Days {
					if len(ranges) > 0 {
						actual := ranges[0].Start + "-" + ranges[0].End
						if actual != tc.expectedRange {
							t.Errorf("expected range %s, got %s", tc.expectedRange, actual)
						}
					}
				}
			}
		})
	}
}

func TestIsActiveAt(t *testing.T) {
	raw := `{
		"enabled": true,
		"days": {
			"monday": [{"start": "09:00", "end": "18:00"}],
			"friday": [{"start": "20:00", "end": "24:00"}]
		}
	}`
	s, err := Parse(raw)
	if err != nil {
		t.Fatalf("failed to parse: %v", err)
	}

	loc := time.UTC
	mon0859 := time.Date(2026, 10, 19, 8, 59, 0, 0, loc)
	mon0900 := time.Date(2026, 10, 19, 9, 0, 0, 0, loc)
	mon1759 := time.Date(2026, 10, 19, 17, 59, 0, 0, loc)
	mon1800 := time.Date(2026, 10, 19, 18, 0, 0, 0, loc)
	fri2359 := time.Date(2026, 10, 23, 23, 59, 0, 0, loc)
	tue1000 := time.Date(2026, 10, 20, 10, 0, 0, 0, loc)

	if s.IsActiveAt(mon0859) {
		t.Errorf("08:59 should be inactive")
	}
	if !s.IsActiveAt(mon0900) {
		t.Errorf("09:00 should be active")
	}
	if !s.IsActiveAt(mon1759) {
		t.Errorf("17:59 should be active")
	}
	if s.IsActiveAt(mon1800) {
		t.Errorf("18:00 should be inactive")
	}
	if !s.IsActiveAt(fri2359) {
		t.Errorf("Friday 23:59 should be active")
	}
	if s.IsActiveAt(tue1000) {
		t.Errorf("Tuesday 10:00 should be inactive")
	}
}

func TestCalculatePeakPoints(t *testing.T) {
	schedA, _ := Parse(`{"enabled": true, "days": {"monday": [{"start": "08:00", "end": "18:00"}]}}`)
	schedB, _ := Parse(`{"enabled": true, "days": {"monday": [{"start": "18:00", "end": "24:00"}]}}`)
	schedOverlap, _ := Parse(`{"enabled": true, "days": {"monday": [{"start": "12:00", "end": "20:00"}]}}`)
	schedAlways, _ := Parse(`{}`)

	t.Run("non-overlapping handover", func(t *testing.T) {
		items := []SchedulableItem{
			{Points: 600, Schedule: schedA},
			{Points: 600, Schedule: schedB},
		}
		peak := CalculatePeakPoints(items)
		if peak != 600 {
			t.Errorf("expected peak 600 at handover, got %d", peak)
		}
	})

	t.Run("overlapping items spike", func(t *testing.T) {
		items := []SchedulableItem{
			{Points: 600, Schedule: schedA},
			{Points: 600, Schedule: schedOverlap},
		}
		peak := CalculatePeakPoints(items)
		if peak != 1200 {
			t.Errorf("expected peak 1200 during overlap, got %d", peak)
		}
	})

	t.Run("always active baseline adds to peak", func(t *testing.T) {
		items := []SchedulableItem{
			{Points: 300, Schedule: schedAlways},
			{Points: 500, Schedule: schedA},
			{Points: 400, Schedule: schedB},
		}
		peak := CalculatePeakPoints(items)
		if peak != 800 {
			t.Errorf("expected peak 800, got %d", peak)
		}
	})

	t.Run("empty items", func(t *testing.T) {
		if peak := CalculatePeakPoints(nil); peak != 0 {
			t.Errorf("expected 0, got %d", peak)
		}
	})
}
