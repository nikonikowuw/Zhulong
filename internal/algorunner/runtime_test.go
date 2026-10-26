package algorunner_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

// TestRuntimeLayout pins the installed-package layout. The installer creates
// these paths and the supervisor later resolves the entry from the same value,
// so changing this layout changes the on-disk package format, not just code:
// every already-installed package would stop being found.
func TestRuntimeLayout(t *testing.T) {
	root := filepath.Join("var", "lib", "zhulong", "algorithms")
	rt := algorunner.Runtime{AlgorithmsDir: root}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "package version directory",
			got:  rt.PackageDir("face_recognition", "1.2.0"),
			want: filepath.Join(root, "face_recognition", "1.2.0"),
		},
		{
			name: "package group directory ignores version",
			got:  rt.IDDir("face_recognition"),
			want: filepath.Join(root, "face_recognition"),
		},
		{
			name: "entry point",
			got:  rt.EntryPath("face_recognition", "1.2.0"),
			want: filepath.Join(root, "face_recognition", "1.2.0", "face_recognition"),
		},
		{
			name: "env file",
			got:  rt.EnvPath("face_recognition", "1.2.0"),
			want: filepath.Join(root, "face_recognition", "1.2.0", ".env"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("got %q, want %q", tc.got, tc.want)
			}
		})
	}
}

// TestResolveEntry is the contract the installer and the supervisor share: what
// counts as a runnable package is decided exactly once. The table is the
// specification, so every rejection stays observable.
func TestResolveEntry(t *testing.T) {
	tests := []struct {
		name      string
		pkgID     string
		setup     func(t *testing.T, dir string)
		wantErr   error
		wantEntry bool
	}{
		{
			name:      "valid executable",
			setup:     func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, "pkg_a"), 0o755) },
			wantEntry: true,
		},
		{
			name:    "missing entry",
			setup:   func(t *testing.T, dir string) {},
			wantErr: algorunner.ErrMissingEntry,
		},
		{
			name: "legacy plugin.so only",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "plugin.so"), 0o755)
			},
			wantErr: algorunner.ErrLegacyEntry,
		},
		{
			name: "legacy plugin.so next to a valid entry",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "pkg_a"), 0o755)
				writeFile(t, filepath.Join(dir, "plugin.so"), 0o755)
			},
			// 两种形态并存是歧义包：静默挑一个，另一个就悄悄变成死代码。
			wantErr: algorunner.ErrLegacyEntry,
		},
		{
			name: "entry is a directory",
			setup: func(t *testing.T, dir string) {
				if err := os.Mkdir(filepath.Join(dir, "pkg_a"), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
			},
			wantErr: algorunner.ErrInvalidEntry,
		},
		{
			name: "entry is a symlink",
			setup: func(t *testing.T, dir string) {
				target := filepath.Join(t.TempDir(), "outside")
				writeFile(t, target, 0o755)
				if err := os.Symlink(target, filepath.Join(dir, "pkg_a")); err != nil {
					t.Fatalf("symlink: %v", err)
				}
			},
			wantErr: algorunner.ErrInvalidEntry,
		},
		{
			name:    "illegal id with traversal",
			pkgID:   "../pkg_a",
			setup:   func(t *testing.T, dir string) {},
			wantErr: algorunner.ErrInvalidEntry,
		},
		{
			name:    "id that names the retired entry",
			pkgID:   "plugin.so",
			setup:   func(t *testing.T, dir string) {},
			wantErr: algorunner.ErrInvalidEntry,
		},
		{
			name:    "empty id",
			pkgID:   "",
			setup:   func(t *testing.T, dir string) {},
			wantErr: algorunner.ErrInvalidEntry,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			id := tc.pkgID
			if id == "" && tc.wantErr != algorunner.ErrInvalidEntry {
				id = "pkg_a"
			}

			got, err := algorunner.ResolveEntry(dir, id)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if want := filepath.Join(dir, "pkg_a"); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

// TestRuntimeZeroValueKeepsPathsRelative documents that a Runtime without an
// install root is not rejected: path helpers degrade to relative paths.
func TestRuntimeZeroValueKeepsPathsRelative(t *testing.T) {
	var rt algorunner.Runtime

	if got := rt.EntryPath("pkg", "1.0.0"); got != filepath.Join("pkg", "1.0.0", "pkg") {
		t.Errorf("got %q, want a relative path under the package id", got)
	}
}

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
