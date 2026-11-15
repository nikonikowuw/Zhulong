package webui

import (
	"errors"
	"io"
	"io/fs"
	"strings"
	"testing"
)

func TestFileSystem_ReturnsValidFS(t *testing.T) {
	fsys := FileSystem()
	if fsys == nil {
		t.Fatal("FileSystem() returned nil")
	}
}

func TestFileSystem_Open(t *testing.T) {
	fsys := FileSystem()

	tests := []struct {
		name        string
		path        string
		wantErr     bool
		isDir       bool
		wantContent string
	}{
		{
			name:        "open root directory",
			path:        "/",
			wantErr:     false,
			isDir:       true,
			wantContent: "",
		},
		{
			name:        "open index.html",
			path:        "/index.html",
			wantErr:     false,
			isDir:       false,
			wantContent: "<!doctype html>",
		},
		{
			name:        "open favicon.ico",
			path:        "/favicon.ico",
			wantErr:     false,
			isDir:       false,
			wantContent: "",
		},
		{
			name:        "non-existent file",
			path:        "/non-existent-file.xyz",
			wantErr:     true,
			isDir:       false,
			wantContent: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := fsys.Open(tc.path)
			if tc.wantErr {
				if err == nil {
					t.Cleanup(func() {
						if closeErr := file.Close(); closeErr != nil {
							t.Errorf("file.Close() returned error: %v", closeErr)
						}
					})
					t.Fatalf("Open(%q) expected error, got nil", tc.path)
				}
				if !errors.Is(err, fs.ErrNotExist) {
					t.Logf("Open(%q) returned expected error: %v", tc.path, err)
				}
				return
			}

			if err != nil {
				t.Fatalf("Open(%q) returned unexpected error: %v", tc.path, err)
			}
			t.Cleanup(func() {
				if closeErr := file.Close(); closeErr != nil {
					t.Errorf("file.Close() returned error: %v", closeErr)
				}
			})

			stat, err := file.Stat()
			if err != nil {
				t.Fatalf("file.Stat() returned unexpected error: %v", err)
			}

			if stat.IsDir() != tc.isDir {
				t.Errorf("file.Stat().IsDir() = %v, want %v", stat.IsDir(), tc.isDir)
			}

			if tc.wantContent != "" {
				contentBytes, readErr := io.ReadAll(file)
				if readErr != nil {
					t.Fatalf("io.ReadAll returned unexpected error: %v", readErr)
				}
				content := string(contentBytes)
				if !strings.Contains(content, tc.wantContent) {
					t.Errorf("expected content of %q to contain %q", tc.path, tc.wantContent)
				}
			}
		})
	}
}

func TestFileSystem_Readdir(t *testing.T) {
	fsys := FileSystem()

	rootDir, err := fsys.Open("/")
	if err != nil {
		t.Fatalf("Open(\"/\") returned error: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := rootDir.Close(); closeErr != nil {
			t.Errorf("rootDir.Close() returned error: %v", closeErr)
		}
	})

	entries, err := rootDir.Readdir(-1)
	if err != nil {
		t.Fatalf("rootDir.Readdir(-1) returned error: %v", err)
	}

	if len(entries) == 0 {
		t.Fatal("rootDir.Readdir(-1) returned empty file list")
	}

	var hasIndex bool
	for _, entry := range entries {
		if entry.Name() == "index.html" {
			hasIndex = true
			break
		}
	}

	if !hasIndex {
		t.Errorf("expected embedded files to contain index.html")
	}
}
