package app

import (
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
	if cfg.HTTPAddress != "127.0.0.1:8080" {
		t.Fatalf("unexpected default HTTPAddress: %q", cfg.HTTPAddress)
	}
	if cfg.DataDir != "./data" {
		t.Fatalf("unexpected default DataDir: %q", cfg.DataDir)
	}
	if cfg.AlgorithmTarget != "cpu" {
		t.Fatalf("unexpected default AlgorithmTarget: %q", cfg.AlgorithmTarget)
	}
	if cfg.Development {
		t.Fatalf("expected Development to default to false, got true")
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Fatalf("unexpected default SessionTTL: %v", cfg.SessionTTL)
	}
	if cfg.Logging.Level != "info" || cfg.Logging.Output != "stderr" || cfg.Logging.Sampling.Enabled || cfg.Logging.Rotation.Enabled {
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
