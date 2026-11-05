package apitoken

import (
	"errors"
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
		name      string
		input     string
		want      string
		targetErr error
	}{
		{name: "valid normal", input: "Platform-A", want: "Platform-A", targetErr: nil},
		{name: "valid trimmed", input: "  Platform-B  ", want: "Platform-B", targetErr: nil},
		{name: "empty", input: "   ", want: "", targetErr: ErrTokenNameRequired},
		{name: "too long", input: strings.Repeat("a", 49), want: "", targetErr: ErrTokenNameTooLong},
		{name: "max length", input: strings.Repeat("a", 48), want: strings.Repeat("a", 48), targetErr: nil},
		{name: "control char", input: "abc\n123", want: "", targetErr: ErrTokenNameInvalid},
		{name: "null byte", input: "abc\x00123", want: "", targetErr: ErrTokenNameInvalid},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTokenName(tc.input)
			if tc.targetErr != nil {
				if !errors.Is(err, tc.targetErr) {
					t.Fatalf("normalizeTokenName(%q) expected error %v, got %v", tc.input, tc.targetErr, err)
				}
			} else if err != nil {
				t.Fatalf("normalizeTokenName(%q) unexpected error: %v", tc.input, err)
			}
			if got != tc.want {
				t.Errorf("normalizeTokenName(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
