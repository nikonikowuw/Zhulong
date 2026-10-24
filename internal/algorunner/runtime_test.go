package algorunner_test

import (
	"path/filepath"
	"testing"

	"github.com/nikonikowuw/Zhulong/internal/algorunner"
)

// TestRuntimeLayout pins the installed-package layout. The installer creates
// these paths and the supervisor later resolves the plugin from the same value,
// so changing this layout changes the on-disk package format, not just code:
// every already-installed package would stop being found.
func TestRuntimeLayout(t *testing.T) {
	root := filepath.Join("var", "lib", "zhulong", "algorithms")
	rt := algorunner.Runtime{AlgorithmsDir: root, RunnerPath: "runner"}

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
			got:  rt.PluginPath("face_recognition", "1.2.0"),
			want: filepath.Join(root, "face_recognition", "1.2.0", "plugin.so"),
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

// TestRuntimeZeroValueKeepsPathsRelative documents that a Runtime without an
// install root is not rejected: unit tests rely on empty-runner Runtimes to
// bypass process work, and path helpers degrade to relative paths.
func TestRuntimeZeroValueKeepsPathsRelative(t *testing.T) {
	var rt algorunner.Runtime

	if got := rt.PluginPath("pkg", "1.0.0"); got != filepath.Join("pkg", "1.0.0", "plugin.so") {
		t.Errorf("got %q, want a relative path under the package id", got)
	}
	if rt.RunnerPath != "" {
		t.Errorf("zero Runtime must not name a runner, got %q", rt.RunnerPath)
	}
}
