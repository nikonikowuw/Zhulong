package app

import (
	"strings"
	"testing"
	"time"
)

func TestValidateHTTPAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{name: "empty", address: "", wantErr: true},
		{name: "whitespace", address: "  ", wantErr: true},
		{name: "surrounding whitespace", address: " 127.0.0.1:8080", wantErr: true},
		{name: "missing port", address: "127.0.0.1", wantErr: true},
		{name: "implicit wildcard host", address: ":8080", wantErr: true},
		{name: "non-numeric port", address: "127.0.0.1:http", wantErr: true},
		{name: "signed port", address: "127.0.0.1:+80", wantErr: true},
		{name: "negative port", address: "127.0.0.1:-1", wantErr: true},
		{name: "port out of range", address: "127.0.0.1:65536", wantErr: true},
		{name: "loopback", address: "127.0.0.1:8080"},
		{name: "ephemeral port", address: "127.0.0.1:0"},
		{name: "IPv6 loopback", address: "[::1]:8080"},
		{name: "explicit wildcard", address: "0.0.0.0:8080"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateHTTPAddress(test.address)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateHTTPAddress(%q) error = %v, wantErr %t", test.address, err, test.wantErr)
			}
		})
	}
}

func TestNewHTTPServerRejectsInvalidAddress(t *testing.T) {
	if _, err := newHTTPServer(Config{HTTPAddress: ""}, nil); err == nil {
		t.Fatal("expected an empty HTTP address to fail validation")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HTTPAddress != "127.0.0.1:8000" {
		t.Fatalf("unexpected default HTTPAddress: %q", cfg.HTTPAddress)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("unexpected default DataDir: %q", cfg.DataDir)
	}
	if cfg.AlgorithmTarget != "onnx" {
		t.Fatalf("unexpected default AlgorithmTarget: %q", cfg.AlgorithmTarget)
	}
	if cfg.Logging.Format != "" {
		t.Fatalf("expected the format to stay unset so json is derived, got %q", cfg.Logging.Format)
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("unexpected default SessionTTL: %v", cfg.SessionTTL)
	}
	loggingInvalid := cfg.Logging.Level != "info" ||
		cfg.Logging.Output != "stderr" ||
		cfg.Logging.Sampling.Enabled ||
		cfg.Logging.Rotation.Enabled
	if loggingInvalid {
		t.Fatalf("unexpected default logging policy: %+v", cfg.Logging)
	}
}

func TestParseSessionTTL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "empty", raw: "", wantErr: true},
		{name: "whitespace", raw: "  ", wantErr: true},
		{name: "invalid syntax", raw: "abc", wantErr: true},
		{name: "negative duration", raw: "-1h", wantErr: true},
		{name: "zero duration", raw: "0s", wantErr: true},
		{name: "hours", raw: "24h", want: 24 * time.Hour},
		{name: "minutes", raw: "30m", want: 30 * time.Minute},
		{name: "days integer", raw: "7d", want: 7 * 24 * time.Hour},
		{name: "days float", raw: "0.5d", want: 12 * time.Hour},
		{name: "days invalid", raw: "abcd", wantErr: true},
		{name: "days negative", raw: "-2d", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSessionTTL(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ParseSessionTTL(%q) error = %v, wantErr = %v", tc.raw, err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("ParseSessionTTL(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestValidateAlertPushConfig(t *testing.T) {
	tests := []struct {
		name        string
		rawBaseURL  string
		rawTTL      string
		wantBaseURL string
		wantTTL     time.Duration
		wantErrPart string
	}{
		{
			name:        "empty base URL and empty TTL uses default",
			rawBaseURL:  "",
			rawTTL:      "",
			wantBaseURL: "",
			wantTTL:     24 * time.Hour,
		},
		{
			name:        "valid http with trailing slash trimmed",
			rawBaseURL:  "  http://192.168.1.50:8000/  ",
			rawTTL:      "12h",
			wantBaseURL: "http://192.168.1.50:8000",
			wantTTL:     12 * time.Hour,
		},
		{
			name:        "uppercase scheme normalized",
			rawBaseURL:  "HTTPS://edge.example.com:8443/sub/",
			rawTTL:      "1m",
			wantBaseURL: "https://edge.example.com:8443/sub",
			wantTTL:     1 * time.Minute,
		},
		{
			name:        "max TTL accepted",
			rawBaseURL:  "https://10.0.0.12:8000",
			rawTTL:      "168h",
			wantBaseURL: "https://10.0.0.12:8000",
			wantTTL:     168 * time.Hour,
		},
		{
			name:        "invalid scheme rejected",
			rawBaseURL:  "ftp://192.168.1.50:8000",
			rawTTL:      "24h",
			wantErrPart: "scheme",
		},
		{
			name:        "missing host rejected",
			rawBaseURL:  "http://",
			rawTTL:      "24h",
			wantErrPart: "host",
		},
		{
			name:        "port without host rejected",
			rawBaseURL:  "https://:8000",
			rawTTL:      "24h",
			wantErrPart: "host",
		},
		{
			name:        "userinfo rejected",
			rawBaseURL:  "http://user:pass@10.0.0.12:8000",
			rawTTL:      "24h",
			wantErrPart: "userinfo",
		},
		{
			name:        "whitespace in host rejected",
			rawBaseURL:  "http://10.0.0.12 8000",
			rawTTL:      "24h",
			wantErrPart: "whitespace",
		},
		{
			name:        "whitespace in path rejected",
			rawBaseURL:  "https://10.0.0.12:8000/a b",
			rawTTL:      "24h",
			wantErrPart: "whitespace",
		},
		{
			name:        "TTL below min rejected",
			rawBaseURL:  "",
			rawTTL:      "59s",
			wantErrPart: "out of bounds",
		},
		{
			name:        "TTL above max rejected",
			rawBaseURL:  "",
			rawTTL:      "169h",
			wantErrPart: "out of bounds",
		},
		{
			name:        "malformed TTL rejected",
			rawBaseURL:  "",
			rawTTL:      "not-a-duration",
			wantErrPart: "signed_url_ttl",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotTTL, err := ValidateAlertPushConfig(tc.rawBaseURL, tc.rawTTL)
			if tc.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf(
						"ValidateAlertPushConfig(%q, %q) err = %v, want substring %q",
						tc.rawBaseURL,
						tc.rawTTL,
						err,
						tc.wantErrPart,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateAlertPushConfig(%q, %q) unexpected err = %v", tc.rawBaseURL, tc.rawTTL, err)
			}
			if gotURL != tc.wantBaseURL || gotTTL != tc.wantTTL {
				t.Fatalf(
					"ValidateAlertPushConfig(%q, %q) = (%q, %v), want (%q, %v)",
					tc.rawBaseURL,
					tc.rawTTL,
					gotURL,
					gotTTL,
					tc.wantBaseURL,
					tc.wantTTL,
				)
			}
		})
	}
}

func TestValidateAlertPushRateLimit(t *testing.T) {
	tests := []struct {
		name        string
		perMin      int
		burst       int
		wantPerMin  int
		wantBurst   int
		wantErrPart string
	}{
		{name: "both zero resolve to defaults", perMin: 0, burst: 0, wantPerMin: 120, wantBurst: 8},
		{name: "zero perMin resolves to default", perMin: 0, burst: 16, wantPerMin: 120, wantBurst: 16},
		{name: "zero burst resolves to default", perMin: 600, burst: 0, wantPerMin: 600, wantBurst: 8},
		{name: "minimum positive bounds", perMin: 1, burst: 1, wantPerMin: 1, wantBurst: 1},
		{name: "maximum bounds", perMin: 60000, burst: 1000, wantPerMin: 60000, wantBurst: 1000},
		{name: "negative perMin rejected", perMin: -1, burst: 8, wantErrPart: "must be 0 for default, or 1..60000"},
		{name: "excessive perMin rejected", perMin: 60001, burst: 8, wantErrPart: "must be 0 for default, or 1..60000"},
		{name: "negative burst rejected", perMin: 120, burst: -1, wantErrPart: "must be 0 for default, or 1..1000"},
		{name: "excessive burst rejected", perMin: 120, burst: 1001, wantErrPart: "must be 0 for default, or 1..1000"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotPerMin, gotBurst, err := ValidateAlertPushRateLimit(tc.perMin, tc.burst)
			if tc.wantErrPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErrPart) {
					t.Fatalf(
						"ValidateAlertPushRateLimit(%d, %d) err = %v, want substring %q",
						tc.perMin,
						tc.burst,
						err,
						tc.wantErrPart,
					)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateAlertPushRateLimit(%d, %d) unexpected err = %v", tc.perMin, tc.burst, err)
			}
			if gotPerMin != tc.wantPerMin || gotBurst != tc.wantBurst {
				t.Fatalf(
					"ValidateAlertPushRateLimit(%d, %d) = (%d, %d), want (%d, %d)",
					tc.perMin,
					tc.burst,
					gotPerMin,
					gotBurst,
					tc.wantPerMin,
					tc.wantBurst,
				)
			}
		})
	}
}
