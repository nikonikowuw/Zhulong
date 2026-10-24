package storage

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// mustMkdirAll creates dir and its parents, failing the test on error. A broken
// fixture must fail loudly instead of silently changing what the test asserts.
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()

	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("create dir %s: %v", dir, err)
	}
}

// mustWriteFile writes data to path, failing the test on error.
func mustWriteFile(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// mustSetModTime sets both timestamps of path to when, failing the test on error.
func mustSetModTime(t *testing.T, path string, when time.Time) {
	t.Helper()

	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatalf("backdate %s: %v", path, err)
	}
}

// mustUnmarshalJSON decodes body into target, failing the test on error.
func mustUnmarshalJSON(t *testing.T, body []byte, target any) {
	t.Helper()

	if err := json.Unmarshal(body, target); err != nil {
		t.Fatalf("decode response body: %v", err)
	}
}
