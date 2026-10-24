package algorunner

import "path/filepath"

// Runtime pins the two filesystem locations the algorithm subsystem depends on:
// the directory holding versioned packages and the binary that executes them.
//
// It is resolved once at the composition root and handed to every component
// (verifier, supervisor, service) as one value, so the install root and the
// runner path cannot drift apart, and the "<dir>/<id>/<version>/..." convention
// below is expressed exactly once.
//
// Build it with keyed fields:
//
//	algorunner.Runtime{AlgorithmsDir: dir, RunnerPath: bin}
type Runtime struct {
	// AlgorithmsDir holds one directory per package version:
	// <AlgorithmsDir>/<id>/<version>.
	AlgorithmsDir string

	// RunnerPath is the algorithm runner binary the supervisor spawns to serve
	// inference. Empty leaves runner processes unstartable.
	RunnerPath string

	// VerifyRunnerPath is the same binary invoked in --verify-plugin mode while
	// installing a package. It is a separate field because verifying a package and
	// serving it are different invocations: a host may stage packages with a build
	// that never serves traffic, and tests run static checks against a stub that
	// only knows how to idle. Empty disables the dynamic verification stage.
	VerifyRunnerPath string
}

// Files whose names both the installer and the runner side depend on. They are
// part of the package contract, not an implementation detail of either side.
const (
	// EntryFileName is the package entry point the supervisor loads.
	EntryFileName = "plugin.so"
	// EnvFileName is the optional per-package environment file given to the runner.
	EnvFileName = ".env"
)

// IDDir returns the directory grouping every installed version of one package.
func (r Runtime) IDDir(id string) string {
	return filepath.Join(r.AlgorithmsDir, id)
}

// PackageDir returns the immutable install directory of one package version.
func (r Runtime) PackageDir(id, version string) string {
	return filepath.Join(r.IDDir(id), version)
}

// PluginPath returns the convention path to the package entry point.
func (r Runtime) PluginPath(id, version string) string {
	return filepath.Join(r.PackageDir(id, version), EntryFileName)
}

// EnvPath returns the convention path to the optional per-package env file.
func (r Runtime) EnvPath(id, version string) string {
	return filepath.Join(r.PackageDir(id, version), EnvFileName)
}
