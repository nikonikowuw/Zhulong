package idutil

import (
	"sync"
	"testing"
	"time"
)

func TestNew_FormatAndLength(t *testing.T) {
	id := New()
	if len(id) != 26 {
		t.Fatalf("expected 26 characters, got %d: %q", len(id), id)
	}
	if !IsValid(id) {
		t.Fatalf("expected generated ID %q to be valid ULID", id)
	}
}

func TestNew_Monotonic(t *testing.T) {
	const count = 1000
	ids := make([]string, count)
	for i := 0; i < count; i++ {
		ids[i] = New()
	}

	for i := 1; i < count; i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("expected ids[%d] (%s) > ids[%d] (%s)", i, ids[i], i-1, ids[i-1])
		}
	}
}

func TestNew_ConcurrentSafe(t *testing.T) {
	const goroutines = 20
	const perGoroutine = 50
	var wg sync.WaitGroup
	var idMap sync.Map

	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				id := New()
				if _, loaded := idMap.LoadOrStore(id, true); loaded {
					t.Errorf("collision detected for ID %s", id)
				}
			}
		}()
	}
	wg.Wait()
}

func TestIsValid(t *testing.T) {
	valid := New()
	if !IsValid(valid) {
		t.Errorf("expected %q to be valid", valid)
	}

	invalidCases := []string{
		"",
		"short",
		"01HMGW84W8E42Z5G85Z1RJZ4N",   // 25 chars
		"01HMGW84W8E42Z5G85Z1RJZ4N22", // 27 chars
		"01HMGW84W8E42Z5G85Z1RJZ4N$",  // illegal char $
		"01HMGW84W8E42Z5G85Z1RJZ4NU",  // 'U' is not Crockford Base32
	}

	for _, tc := range invalidCases {
		if IsValid(tc) {
			t.Errorf("expected %q to be invalid", tc)
		}
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

	if _, err := Time("invalid"); err == nil {
		t.Error("expected error for invalid ULID, got nil")
	}
}
