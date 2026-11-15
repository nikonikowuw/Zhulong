package idutil

import (
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
)

var (
	benchmarkID   string
	benchmarkBool bool
)

func TestNew_FormatAndLength(t *testing.T) {
	id := New()
	if len(id) != encodedLength {
		t.Fatalf("expected %d characters, got %d: %q", encodedLength, len(id), id)
	}
	if !IsValid(id) {
		t.Fatalf("expected generated ID %q to be valid ULID", id)
	}
}

func TestNew_Monotonic(t *testing.T) {
	const count = 1000
	ids := make([]string, count)
	for i := range count {
		ids[i] = New()
	}

	for i := 1; i < count; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("expected ids[%d] (%q) > ids[%d] (%q)", i, ids[i], i-1, ids[i-1])
		}
	}
}

func TestNew_ConcurrentSafe(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 50
	var wg sync.WaitGroup
	var idMap sync.Map

	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			for range perGoroutine {
				id := New()
				if _, loaded := idMap.LoadOrStore(id, true); loaded {
					t.Errorf("collision detected for ID %q", id)
				}
			}
		}()
	}
	wg.Wait()
}

func TestParse(t *testing.T) {
	validID := New()
	parsed, err := Parse(validID)
	if err != nil {
		t.Fatalf("Parse(%q) returned unexpected error: %v", validID, err)
	}
	if parsed.String() != validID {
		t.Fatalf("Parse(%q) = %q, want %q", validID, parsed.String(), validID)
	}

	invalidCases := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "too short", input: "short"},
		{name: "invalid character", input: "01HMGW84W8E42Z5G85Z1RJZ4N$"},
		{name: "invalid base32 character U", input: "01HMGW84W8E42Z5G85Z1RJZ4NU"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			var zero ulid.ULID
			got, err := Parse(tc.input)
			if err == nil {
				t.Fatalf("Parse(%q) expected error, got nil (result: %v)", tc.input, got)
			}
			if got != zero {
				t.Fatalf("Parse(%q) on error expected zero value, got %v", tc.input, got)
			}
		})
	}
}

func TestIsValid(t *testing.T) {
	valid := New()
	if !IsValid(valid) {
		t.Errorf("expected %q to be valid", valid)
	}

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "valid generated ULID", input: valid, want: true},
		{name: "empty string", input: "", want: false},
		{name: "short", input: "short", want: false},
		{name: "25 chars", input: "01HMGW84W8E42Z5G85Z1RJZ4N", want: false},
		{name: "27 chars", input: "01HMGW84W8E42Z5G85Z1RJZ4N22", want: false},
		{name: "illegal special char", input: "01HMGW84W8E42Z5G85Z1RJZ4N$", want: false},
		{name: "non Crockford char U", input: "01HMGW84W8E42Z5G85Z1RJZ4NU", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsValid(tc.input); got != tc.want {
				t.Errorf("IsValid(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestTime(t *testing.T) {
	before := time.Now().UTC().Truncate(time.Millisecond)
	id := New()
	after := time.Now().UTC().Add(time.Millisecond)

	parsedTime, err := Time(id)
	if err != nil {
		t.Fatalf("Time(%q) returned error: %v", id, err)
	}

	if parsedTime.Before(before) || parsedTime.After(after) {
		t.Errorf("expected parsed time %v to be between %v and %v", parsedTime, before, after)
	}

	invalidCases := []struct {
		name  string
		input string
	}{
		{name: "empty string", input: ""},
		{name: "short", input: "invalid"},
		{name: "illegal base32 character", input: "01HMGW84W8E42Z5G85Z1RJZ4NU"},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			var zero time.Time
			got, err := Time(tc.input)
			if err == nil {
				t.Fatalf("Time(%q) expected error, got nil (time: %v)", tc.input, got)
			}
			if !got.Equal(zero) {
				t.Fatalf("Time(%q) on error expected zero time, got %v", tc.input, got)
			}
		})
	}
}

func BenchmarkNew(b *testing.B) {
	for b.Loop() {
		benchmarkID = New()
	}
}

func BenchmarkIsValid(b *testing.B) {
	id := New()
	b.ResetTimer()
	for b.Loop() {
		benchmarkBool = IsValid(id)
	}
}
