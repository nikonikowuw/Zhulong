package logsafe

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSafe(t *testing.T) {
	long := strings.Repeat("a", MaxSafeBytes+500)
	multibyte := strings.Repeat("界", MaxSafeBytes) // 3 bytes each: 6144 bytes

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain ascii", in: "camera started", want: "camera started"},
		{name: "utf8 passthrough", in: "相机启动 正常", want: "相机启动 正常"},
		{name: "crlf injection", in: "line1\r\nline2", want: "line1  line2"},
		{name: "tab and bell", in: "a\tb\x07c", want: "a b c"},
		{name: "nul byte", in: "a\x00b", want: "a b"},
		{name: "del", in: "a\x7fb", want: "a b"},
		{name: "ansi escape", in: "\x1b[31mred\x1b[0m", want: " [31mred [0m"},
		{name: "unicode line separator", in: "a\u2028b\u2029c", want: "a b c"},
		{name: "c1 control", in: "a\u0085b", want: "a b"},
		{name: "invalid utf8 single byte", in: "a\xffb", want: "a\uFFFDb"},
		{name: "invalid utf8 continuation", in: "a\x80\x80b", want: "a\uFFFD\uFFFDb"},
		{name: "truncated multibyte", in: "a\xe7\x95b", want: "a\uFFFD\uFFFDb"},
		{
			name: "exactly max bytes is untouched",
			in:   strings.Repeat("a", MaxSafeBytes),
			want: strings.Repeat("a", MaxSafeBytes),
		},
		{
			name: "oversized is truncated and marked",
			in:   long,
			want: strings.Repeat("a", MaxSafeBytes-len(SafeTruncatedMarker)) + SafeTruncatedMarker,
		},
		{
			name: "oversized multibyte truncates on a rune boundary",
			in:   multibyte,
			want: strings.Repeat("界", (MaxSafeBytes-len(SafeTruncatedMarker))/3) + SafeTruncatedMarker,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Safe(tc.in)
			if got != tc.want {
				t.Fatalf("Safe(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if len(got) > MaxSafeBytes {
				t.Fatalf("Safe(%q) returned %d bytes, over the %d-byte bound", tc.in, len(got), MaxSafeBytes)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Safe(%q) returned invalid UTF-8: %q", tc.in, got)
			}
			if strings.ContainsAny(got, "\r\n") {
				t.Fatalf("Safe(%q) kept a line break: %q", tc.in, got)
			}
		})
	}
}

// TestSafeDoesNotDetectSecrets pins the documented limit: Safe shapes text, it
// does not recognize credentials embedded in arbitrary strings.
func TestSafeDoesNotDetectSecrets(t *testing.T) {
	const credentialBearing = "connecting with password=hunter2 and token=abc123"
	if got := Safe(credentialBearing); got != credentialBearing {
		t.Fatalf("Safe changed an ordinary string it is not supposed to interpret: %q", got)
	}
	if got := Safe("rtsp://user:secret@10.0.0.1/live"); got != "rtsp://user:secret@10.0.0.1/live" {
		t.Fatalf("Safe is not a URL redactor, got %q", got)
	}
}

func TestURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: URLMarker},
		{name: "blank", in: "   ", want: URLMarker},
		{name: "userinfo hidden", in: "rtsp://admin:super_secret@192.168.1.10:554/live/main", want: "rtsp://" + URLMarker},
		{name: "no userinfo still hidden", in: "https://example.com/path", want: "https://" + URLMarker},
		{name: "query and fragment hidden", in: "https://example.com/a?token=abc#frag", want: "https://" + URLMarker},
		{name: "scheme lower-cased", in: "RTSP://admin:x@10.0.0.1/live", want: "rtsp://" + URLMarker},
		{name: "surrounding space trimmed", in: "  http://10.0.0.1/x  ", want: "http://" + URLMarker},
		{name: "relative path", in: "/live/main", want: URLMarker},
		{name: "no scheme", in: "example.com/x", want: URLMarker},
		{name: "malformed ipv6", in: "rtsp://[::1", want: URLMarker},
		{name: "control characters", in: "ht\ntp://a/b", want: URLMarker},
		{name: "oversized", in: "https://example.com/" + strings.Repeat("a", MaxSafeBytes), want: URLMarker},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := URL(tc.in)
			if got != tc.want {
				t.Fatalf("URL(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, leak := range []string{"admin", "super_secret", "192.168.1.10", "example.com", "/live", "token=abc", "frag", "10.0.0.1"} {
				if strings.Contains(got, leak) {
					t.Fatalf("URL(%q) leaked %q: %q", tc.in, leak, got)
				}
			}
			if tc.in != "" && len(tc.in) <= MaxSafeBytes {
				if strings.Contains(got, "a"+strings.Repeat("a", 10)) && tc.want == URLMarker {
					t.Fatalf("URL(%q) echoed input: %q", tc.in, got)
				}
			}
		})
	}
}

func TestPath(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "absolute", in: "/var/lib/zhulong/devices/cam-001/clip.mp4"},
		{name: "device socket", in: "/run/zhulong/broker.sock"},
		{name: "user in path", in: "/home/zhulong/secrets/token.txt"},
		{name: "control characters", in: "/tmp/a\nb\x00c"},
		{name: "oversized", in: "/" + strings.Repeat("x", MaxSafeBytes*2)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Path(tc.in)
			if got != PathMarker {
				t.Fatalf("Path(%q) = %q, want %q", tc.in, got, PathMarker)
			}
			if tc.in != "" && strings.Contains(got, tc.in) {
				t.Fatalf("Path(%q) echoed the input", tc.in)
			}
		})
	}
}

func TestPaths(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := Paths(nil); got != nil {
			t.Fatalf("Paths(nil) = %v, want nil", got)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if got := Paths([]string{}); got != nil {
			t.Fatalf("Paths(empty) = %v, want nil", got)
		}
	})
	t.Run("cardinality kept, values hidden", func(t *testing.T) {
		in := []string{"/run/a.sock", "", "/home/zhulong/secrets/b", strings.Repeat("c", MaxSafeBytes+1)}
		got := Paths(in)
		if len(got) != len(in) {
			t.Fatalf("Paths returned %d entries, want %d", len(got), len(in))
		}
		for i, value := range got {
			if value != PathMarker {
				t.Fatalf("Paths()[%d] = %q, want %q", i, value, PathMarker)
			}
		}
	})
	t.Run("input slice is not modified", func(t *testing.T) {
		in := []string{"/run/a.sock", "/run/b.sock"}
		before := strings.Join(in, ",")
		Paths(in)
		if after := strings.Join(in, ","); after != before {
			t.Fatalf("Paths mutated its input: %q -> %q", before, after)
		}
	})
}

func TestSecret(t *testing.T) {
	for _, in := range []string{"", "hunter2", "Bearer eyJhbGciOi", strings.Repeat("s", MaxSafeBytes*2), "a\r\nb"} {
		got := Secret(in)
		if got != ValueMarker {
			t.Fatalf("Secret(%q) = %q, want %q", in, got, ValueMarker)
		}
		if in != "" && strings.Contains(got, in) {
			t.Fatalf("Secret(%q) echoed the input", in)
		}
	}
}

func TestIP(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: IPMarker},
		{name: "blank", in: "  ", want: IPMarker},
		{name: "ipv4 masked to /24", in: "192.168.1.77", want: "192.168.1.0/24"},
		{name: "ipv4 with port", in: "192.168.1.77:554", want: IPMarker},
		{name: "ipv4 loopback", in: "127.0.0.1", want: "127.0.0.0/24"},
		{name: "ipv6 masked to /64", in: "2001:db8:1234:5678:9abc::1", want: "2001:db8:1234:5678::/64"},
		{name: "ipv6 bracketed", in: "[2001:db8::1]", want: "2001:db8::/64"},
		{name: "ipv6 loopback", in: "::1", want: "::/64"},
		{name: "ipv4-mapped ipv6 unmapped", in: "[::ffff:192.168.1.5]", want: "192.168.1.0/24"},
		{name: "ipv6 with zone", in: "fe80::1%eth0", want: "fe80::/64"},
		{name: "garbage", in: "not-an-ip", want: IPMarker},
		{name: "control characters", in: "192.168.1.5\n", want: "192.168.1.0/24"},
		{name: "malformed", in: "192.168.1.999", want: IPMarker},
		{name: "oversized", in: strings.Repeat("1", MaxSafeBytes+1), want: IPMarker},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IP(tc.in); got != tc.want {
				t.Fatalf("IP(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestErrorSummary(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if got := ErrorSummary(nil); got != NoErrorMarker {
			t.Fatalf("ErrorSummary(nil) = %q, want %q", got, NoErrorMarker)
		}
	})
	t.Run("plain error", func(t *testing.T) {
		got := ErrorSummary(errors.New("dial tcp 10.0.0.1:554: password=hunter2"))
		if got != ErrorMarker {
			t.Fatalf("ErrorSummary = %q, want %q", got, ErrorMarker)
		}
		if strings.Contains(got, "hunter2") || strings.Contains(got, "10.0.0.1") {
			t.Fatalf("ErrorSummary leaked error text: %q", got)
		}
	})
	t.Run("wrapped error", func(t *testing.T) {
		inner := errors.New("secret=abc")
		got := ErrorSummary(fmt.Errorf("camera connect failed: %w", inner))
		if got != ErrorMarker {
			t.Fatalf("ErrorSummary = %q, want %q", got, ErrorMarker)
		}
	})
	t.Run("error text is never rendered", func(t *testing.T) {
		got := ErrorSummary(explodingError{})
		if got != ErrorMarker {
			t.Fatalf("ErrorSummary = %q, want %q", got, ErrorMarker)
		}
	})
}

// explodingError fails the test if ErrorSummary or its formatter calls Error()
// or String().
type explodingError struct{}

func (explodingError) Error() string { panic("Error() must never be called") }

func (e explodingError) String() string { return fmt.Sprintf("%v", e.errorSentinel()) }

func (explodingError) errorSentinel() any { panic("String() must never be called") }

// TestSafeBoundarySemantics pins the two distinct outcomes at the size bound: a
// value whose *sanitized* form fits is returned without a marker, while only a
// value that genuinely overflows is cut and marked.
func TestSafeBoundarySemantics(t *testing.T) {
	padded := make([]byte, MaxSafeBytes)
	for i := range padded {
		padded[i] = 'a'
	}
	padded[0] = 0x01 // one control byte: sanitizes to a space, still MaxSafeBytes

	t.Run("sanitized form fits so no marker", func(t *testing.T) {
		got := Safe(string(padded))
		if strings.Contains(got, SafeTruncatedMarker) {
			t.Fatalf("Safe added a truncation marker to an in-bounds value: %q", got)
		}
		if len(got) != MaxSafeBytes {
			t.Fatalf("Safe returned %d bytes, want %d", len(got), MaxSafeBytes)
		}
		if got[0] != ' ' {
			t.Fatalf("control byte was not replaced: %q", got[:4])
		}
	})

	t.Run("one byte over the bound is marked", func(t *testing.T) {
		in := string(padded) + "a"
		got := Safe(in)
		if !strings.HasSuffix(got, SafeTruncatedMarker) {
			t.Fatalf("Safe did not mark a genuinely truncated value: %q", got)
		}
		if len(got) > MaxSafeBytes {
			t.Fatalf("Safe returned %d bytes, over the bound", len(got))
		}
		if !utf8.ValidString(got) {
			t.Fatalf("truncated value is not valid UTF-8: %q", got)
		}
	})

	t.Run("truncation never splits a rune", func(t *testing.T) {
		// 3-byte runes cannot land exactly on the cut point, so this exercises
		// the walk-back to a rune start.
		in := strings.Repeat("界", MaxSafeBytes)
		got := Safe(in)
		if !utf8.ValidString(got) {
			t.Fatalf("truncated multibyte value is not valid UTF-8: %q", got)
		}
		if !strings.HasSuffix(got, SafeTruncatedMarker) {
			t.Fatalf("Safe did not mark truncation: %q", got)
		}
		if len(got) > MaxSafeBytes {
			t.Fatalf("Safe returned %d bytes, over the bound", len(got))
		}
	})
}

// TestIPIsIdempotent pins that masking an already-masked value is a no-op.
//
// This is not theoretical: call sites call logsafe.IP and then SanitizingCore
// applies the same helper again to the "clientIp" key. A non-idempotent IP()
// silently replaced the useful network summary with the unparseable marker,
// which reads like "we redacted this" while actually meaning "we lost it".
func TestIPIsIdempotent(t *testing.T) {
	for _, input := range []string{
		"127.0.0.1",
		"203.0.113.77",
		"2001:db8::1",
		"[2001:db8::1]",
	} {
		once := IP(input)
		twice := IP(once)
		if once != twice {
			t.Fatalf("IP is not idempotent for %q: once=%q twice=%q", input, once, twice)
		}
		if once == IPMarker {
			t.Fatalf("IP(%q) collapsed to the unparseable marker: %q", input, once)
		}
	}

	// An already-masked network must be normalised, not rejected.
	if got := IP("203.0.113.77/24"); got != "203.0.113.0/24" {
		t.Fatalf("IP(CIDR) = %q, want 203.0.113.0/24", got)
	}
}
