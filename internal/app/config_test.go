package app

import "testing"

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
	if cfg.Development {
		t.Fatalf("expected Development to default to false, got true")
	}
}
