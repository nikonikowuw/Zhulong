package apitoken

import (
	"strings"
	"testing"
)

func TestGenerateToken(t *testing.T) {
	raw, hash, masked, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken failed: %v", err)
	}
	if !strings.HasPrefix(raw, "sk-") {
		t.Errorf("raw token expected to start with 'sk-', got %q", raw)
	}
	if len(raw) != 67 {
		t.Errorf("expected raw token length 67, got %d (%q)", len(raw), raw)
	}
	if len(hash) != 64 {
		t.Errorf("expected SHA-256 hash length 64, got %d (%q)", len(hash), hash)
	}
	if hash != HashToken(raw) {
		t.Errorf("hash mismatch: expected %q, got %q", HashToken(raw), hash)
	}
	if !strings.HasPrefix(masked, "sk-") || !strings.Contains(masked, "...") {
		t.Errorf("expected masked key format sk-...xxxx, got %q", masked)
	}
}

func TestNormalizeTokenName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid normal", input: "Platform-A", want: "Platform-A", wantErr: false},
		{name: "valid trimmed", input: "  Platform-B  ", want: "Platform-B", wantErr: false},
		{name: "empty", input: "   ", want: "", wantErr: true},
		{name: "too long", input: strings.Repeat("a", 49), want: "", wantErr: true},
		{name: "max length", input: strings.Repeat("a", 48), want: strings.Repeat("a", 48), wantErr: false},
		{name: "control char", input: "abc\n123", want: "", wantErr: true},
		{name: "null byte", input: "abc\x00123", want: "", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTokenName(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("normalizeTokenName(%q) error = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("normalizeTokenName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
