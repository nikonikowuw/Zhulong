package algorithm

import "testing"

// TestDetectArchiveType 是包内测试：archiveType 与 detectArchiveType 都是
// 实现细节，不对外导出，所以验证从内部做。
func TestDetectArchiveType(t *testing.T) {
	tests := []struct {
		name   string
		header []byte
		want   archiveType
	}{
		{name: "tar.gz", header: []byte{0x1F, 0x8B, 0x08, 0x00}, want: archiveTypeTarGz},
		{name: "zip", header: []byte{0x50, 0x4B, 0x03, 0x04}, want: archiveTypeZip},
		{name: "unknown", header: []byte{0x00, 0x01, 0x02, 0x03}, want: archiveTypeUnknown},
		{name: "short read", header: []byte{0x1F}, want: archiveTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := detectArchiveType(tt.header); got != tt.want {
				t.Errorf("detectArchiveType(% x) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}
