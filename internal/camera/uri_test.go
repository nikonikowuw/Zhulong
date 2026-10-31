package camera

import (
	"net/url"
	"testing"
)

func TestParseAndNormalizeRTSP(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		transport   string
		expectClean string
		expectUser  string
		expectPass  string
		expectTrans string
		shouldFail  bool
	}{
		{
			name:        "Standard RTSP URL with port 554 omitted",
			raw:         "rtsp://192.168.1.100:554/live/main",
			transport:   "tcp",
			expectClean: "rtsp://192.168.1.100/live/main",
			expectUser:  "",
			expectPass:  "",
			expectTrans: "tcp",
			shouldFail:  false,
		},
		{
			name:        "RTSP with custom port and credentials",
			raw:         "rtsp://admin:secret123@192.168.1.50:8554/h264?channel=1",
			transport:   "UDP",
			expectClean: "rtsp://192.168.1.50:8554/h264?channel=1",
			expectUser:  "admin",
			expectPass:  "secret123",
			expectTrans: "udp",
			shouldFail:  false,
		},
		{
			name:        "Special characters in password",
			raw:         "rtsp://admin:p%40ss%3Aword@cam.local:554/ch0",
			transport:   "",
			expectClean: "rtsp://cam.local/ch0",
			expectUser:  "admin",
			expectPass:  "p@ss:word",
			expectTrans: "tcp",
			shouldFail:  false,
		},
		{
			name:        "Uppercase Scheme and Host Normalized",
			raw:         "RTSP://MY-CAMERA.LOCAL:554/LIVE/MAIN",
			transport:   "tcp",
			expectClean: "rtsp://my-camera.local/LIVE/MAIN",
			expectUser:  "",
			expectPass:  "",
			expectTrans: "tcp",
			shouldFail:  false,
		},
		{
			name:       "Invalid Scheme (HTTP)",
			raw:        "http://192.168.1.100/live",
			transport:  "tcp",
			shouldFail: true,
		},
		{
			name:       "Empty Host",
			raw:        "rtsp:///live",
			transport:  "tcp",
			shouldFail: true,
		},
		{
			name:       "Invalid Transport",
			raw:        "rtsp://192.168.1.100/live",
			transport:  "quic",
			shouldFail: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := ParseAndNormalizeRTSP(tc.raw, tc.transport)
			if tc.shouldFail {
				if err == nil {
					t.Fatalf("expected failure, but got success: %+v", info)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.CleanURI != tc.expectClean {
				t.Errorf("CleanURI expected %q, got %q", tc.expectClean, info.CleanURI)
			}
			if info.Username != tc.expectUser {
				t.Errorf("Username expected %q, got %q", tc.expectUser, info.Username)
			}
			if info.Password != tc.expectPass {
				t.Errorf("Password expected %q, got %q", tc.expectPass, info.Password)
			}
			if info.Transport != tc.expectTrans {
				t.Errorf("Transport expected %q, got %q", tc.expectTrans, info.Transport)
			}
		})
	}
}

func TestSanitizeURL(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{
			input:    "rtsp://admin:super_secret@192.168.1.10:554/stream",
			expected: "rtsp://admin:xxxxx@192.168.1.10:554/stream",
		},
		{
			input:    "rtsp://192.168.1.10:554/stream",
			expected: "rtsp://192.168.1.10:554/stream",
		},
		{
			input:    "rtsp://admin:@192.168.1.10/stream",
			expected: "rtsp://admin:xxxxx@192.168.1.10/stream",
		},
		{
			input:    "",
			expected: "",
		},
	}

	for _, c := range cases {
		got := SanitizeURL(c.input)
		if got != c.expected {
			t.Errorf("SanitizeURL(%q) = %q, expected %q", c.input, got, c.expected)
		}
	}
}

func TestStreamHost(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "credentialed URL",
			input:    "rtsp://admin:pass123@192.168.1.10:554/live/main",
			expected: "192.168.1.10",
		},
		{
			// 旧前端正则依赖 `@`，无凭据 URL 会被误判为回环地址
			name:     "credential-less URL",
			input:    "rtsp://192.168.1.10:554/live",
			expected: "192.168.1.10",
		},
		{
			name:     "password containing at sign",
			input:    "rtsp://admin:p@ss@1.2.3.4/live",
			expected: "1.2.3.4",
		},
		{
			name:     "percent-encoded at sign in password",
			input:    "rtsp://admin:p%40ss@1.2.3.4/live",
			expected: "1.2.3.4",
		},
		{
			name:     "IPv6 literal without brackets",
			input:    "rtsp://[fe80::1]:554/live",
			expected: "fe80::1",
		},
		{
			name:     "hostname instead of IP",
			input:    "rtsp://camera.local/live",
			expected: "camera.local",
		},
		{
			name:     "encrypted placeholder yields empty host",
			input:    "[ENCRYPTED]",
			expected: "",
		},
		{
			name:     "empty authority yields empty host",
			input:    "rtsp:///live",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := streamHost(c.input); got != c.expected {
				t.Errorf("streamHost(%q) = %q, expected %q", c.input, got, c.expected)
			}
		})
	}
}

func TestBuildRTSPURL(t *testing.T) {
	q := url.Values{}
	q.Set("token", "123")
	built, err := BuildRTSPURL(BuildRTSPURLParams{
		Host:     "192.168.1.10",
		Port:     8554,
		Path:     "live/main",
		Query:    q,
		Username: "admin",
		Password: "pass",
	})
	if err != nil {
		t.Fatalf("BuildRTSPURL failed: %v", err)
	}
	expected := "rtsp://admin:pass@192.168.1.10:8554/live/main?token=123"
	if built != expected {
		t.Errorf("expected %q, got %q", expected, built)
	}
}

func TestIsGB28181ID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "20 digits", id: "34020000001310000001", want: true},
		{name: "all zeros", id: "00000000000000000000", want: true},
		{name: "19 digits", id: "3402000000131000000", want: false},
		{name: "21 digits", id: "340200000013100000011", want: false},
		{name: "letter inside", id: "3402000000131000000X", want: false},
		{name: "empty", id: "", want: false},
		{name: "hostname shaped", id: "camera.example.local", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsGB28181ID(tt.id); got != tt.want {
				t.Errorf("IsGB28181ID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
