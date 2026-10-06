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

func TestBuildRTSPURL(t *testing.T) {
	q := url.Values{}
	q.Set("token", "123")
	built, err := BuildRTSPURL("192.168.1.10", 8554, "live/main", q, "admin", "pass")
	if err != nil {
		t.Fatalf("BuildRTSPURL failed: %v", err)
	}
	expected := "rtsp://admin:pass@192.168.1.10:8554/live/main?token=123"
	if built != expected {
		t.Errorf("expected %q, got %q", expected, built)
	}
}
