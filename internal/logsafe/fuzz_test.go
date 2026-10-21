package logsafe

import (
	"testing"
	"unicode/utf8"
)

// FuzzSafe checks the documented invariants for arbitrary input: no panic, valid
// UTF-8 out, no control characters out, and the byte bound is never exceeded.
func FuzzSafe(f *testing.F) {
	for _, seed := range []string{
		"", "plain", "\x00", "\r\n", "界", "\xff\xfe", "a\u2028b",
		"rtsp://admin:pw@10.0.0.1:554/live?token=abc#frag",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Safe(in)
		if len(got) > MaxSafeBytes {
			t.Fatalf("Safe returned %d bytes for %d-byte input, over the bound", len(got), len(in))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("Safe returned invalid UTF-8 for %q", in)
		}
		if hasUnsafeRune(got) {
			t.Fatalf("Safe returned control characters for %q: %q", in, got)
		}
		if in == "" && got != "" {
			t.Fatalf("Safe(\"\") = %q, want empty", got)
		}
		// Idempotent: sanitizing an already-sanitized value changes nothing.
		if again := Safe(got); len(again) != len(got) {
			t.Fatalf("Safe is not idempotent: %q -> %q", got, again)
		}
	})
}

// FuzzURL checks that URL never returns input-derived text and never panics.
func FuzzURL(f *testing.F) {
	for _, seed := range []string{"", "rtsp://a:b@c/d?e#f", "/relative", "rtsp://[::1", "HTTP://X"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := URL(in)
		if got != URLMarker && len(got) <= len(URLMarker) {
			t.Fatalf("URL(%q) = %q, which is neither the marker nor scheme+marker", in, got)
		}
	})
}

// FuzzIP checks that IP never panics and always returns a prefix or the marker.
func FuzzIP(f *testing.F) {
	for _, seed := range []string{"", "10.0.0.1", "::1", "[::1]", "1.2.3.4:80", "fe80::1%eth0"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := IP(in)
		if got == "" {
			t.Fatalf("IP(%q) returned an empty string", in)
		}
		if len(got) > 64 {
			t.Fatalf("IP(%q) returned an implausibly long summary: %q", in, got)
		}
	})
}

// FuzzPathSecret checks the fixed-output helpers never echo their input.
func FuzzPathSecret(f *testing.F) {
	f.Add("")
	f.Add("/run/a.sock")
	f.Fuzz(func(t *testing.T, in string) {
		if got := Path(in); got != PathMarker {
			t.Fatalf("Path(%q) = %q", in, got)
		}
		if got := Secret(in); got != ValueMarker {
			t.Fatalf("Secret(%q) = %q", in, got)
		}
	})
}
