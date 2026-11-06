//go:build linux

package network

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func TestIsEthernetLikeInterface(t *testing.T) {
	tests := []struct {
		name     string
		linkType string
		want     bool
	}{
		{name: "ethernet", linkType: strconv.Itoa(unix.ARPHRD_ETHER), want: true},
		{name: "CAN", linkType: strconv.Itoa(unix.ARPHRD_CAN), want: false},
		{name: "invalid type", linkType: "not-a-number", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			interfacePath := t.TempDir()
			if err := os.WriteFile(filepath.Join(interfacePath, "type"), []byte(tt.linkType), 0o600); err != nil {
				t.Fatalf("write interface type: %v", err)
			}

			if got := isEthernetLikeInterface(interfacePath); got != tt.want {
				t.Errorf("isEthernetLikeInterface() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("missing type", func(t *testing.T) {
		if isEthernetLikeInterface(t.TempDir()) {
			t.Error("isEthernetLikeInterface() = true for a missing type file, want false")
		}
	})
}
