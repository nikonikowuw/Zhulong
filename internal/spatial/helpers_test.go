package spatial

import (
	"strings"
	"testing"
	"time"
)

func TestEscapeLikeNeutralisesWildcards(t *testing.T) {
	cases := map[string]string{
		"粤B88888": `粤B88888`,
		"%":       `\%`,
		"_":       `\_`,
		`a\%b`:    `a\\\%b`,
	}
	for in, want := range cases {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSVGIntrinsicSize(t *testing.T) {
	cases := []struct {
		name   string
		svg    string
		wantW  int
		wantH  int
		wantOK bool
	}{
		{
			name:   "width and height attributes",
			svg:    `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="480"><rect/></svg>`,
			wantW:  640,
			wantH:  480,
			wantOK: true,
		},
		{
			name:   "px suffix",
			svg:    `<svg width="800px" height="600px"></svg>`,
			wantW:  800,
			wantH:  600,
			wantOK: true,
		},
		{
			name:   "viewBox fallback when percentages carry no pixels",
			svg:    `<svg width="100%" height="100%" viewBox="0 0 1200 900"></svg>`,
			wantW:  1200,
			wantH:  900,
			wantOK: true,
		},
		{
			name:   "no usable size",
			svg:    `<svg width="100%" height="100%"></svg>`,
			wantOK: false,
		},
		{
			name:   "nested svg is not the canvas size",
			svg:    `<html><svg width="10" height="10"></svg></html>`,
			wantOK: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h, ok := svgIntrinsicSize(strings.NewReader(tc.svg))
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("size = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestUTCKeepsZeroValue(t *testing.T) {
	if got := utc(time.Time{}); !got.IsZero() {
		t.Fatalf("zero time must stay zero, got %v", got)
	}

	cst := time.FixedZone("CST", 8*3600)
	shifted := time.Date(2026, 10, 29, 18, 0, 0, 0, cst)
	got := utc(shifted)
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC location, got %v", got.Location())
	}
	if got.Hour() != 10 {
		t.Fatalf("expected 10:00 UTC, got %v", got)
	}
}
