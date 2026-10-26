package algorunner

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Runtime pins the filesystem location the algorithm subsystem depends on:
// the directory holding versioned packages. It is resolved once at the
// composition root and handed to every component (verifier, supervisor,
// service) as one value, so the install root and the "<dir>/<id>/<version>/..."
// convention below is expressed exactly once.
//
// Build it with keyed fields:
//
//	algorunner.Runtime{AlgorithmsDir: dir}
//
// There is deliberately no runner path any more: a package *is* its own
// executable. The host neither dlopens the package nor spawns a shared runner
// on its behalf — see ResolveEntry.
type Runtime struct {
	// AlgorithmsDir holds one directory per package version:
	// <AlgorithmsDir>/<id>/<version>.
	AlgorithmsDir string
}

// Files and names whose role the installer, the supervisor and the package
// itself must agree on. They are part of the package contract, not an
// implementation detail of either side.
const (
	// LegacyEntryFileName is the retired entry point: a shared object the host
	// dlopen'd through a generic runner process. Packages that still carry one
	// are rejected outright instead of being half-supported, because the two
	// forms have different lifecycle, error and cleanup semantics.
	LegacyEntryFileName = "plugin.so"

	// EnvFileName is the optional per-package environment file given to the
	// package process.
	EnvFileName = ".env"
)

// Entry resolution errors. They are wrapped with the offending path so the
// operator can act on them, and they are distinguished from another so the
// installer (fatal, reject the archive) and the supervisor (report a stable
// code, do not retry) can react differently.
var (
	// ErrMissingEntry means the package root has no executable named after
	// manifest.id. Packages installed by an older host, or archives whose
	// executable was stripped by a later tool, land here.
	ErrMissingEntry = errors.New("missing package entry executable in package root")
	// ErrLegacyEntry means the package still ships the retired plugin.so entry.
	ErrLegacyEntry = errors.New("package ships the retired plugin.so entry")
	// ErrInvalidEntry means the entry exists but is not a plain regular file
	// (directory, symlink, device, …) or the id is not a safe path token.
	ErrInvalidEntry = errors.New("package entry is not a regular file")
)

// entryIDPattern mirrors the manifest token charset: a package id becomes a
// path component *and* an executable name, so it must not carry separators,
// dots or anything a shell could reinterpret.
var entryIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// IDDir returns the directory grouping every installed version of one package.
func (r Runtime) IDDir(id string) string {
	return filepath.Join(r.AlgorithmsDir, id)
}

// PackageDir returns the immutable install directory of one package version.
func (r Runtime) PackageDir(id, version string) string {
	return filepath.Join(r.IDDir(id), version)
}

// EntryPath returns the convention path to the package entry executable.
//
// It is a pure path computation: use ResolveEntry to obtain a path that is
// known to exist and to be a regular file.
func (r Runtime) EntryPath(id, version string) string {
	return filepath.Join(r.PackageDir(id, version), id)
}

// EnvPath returns the convention path to the optional per-package env file.
func (r Runtime) EnvPath(id, version string) string {
	return filepath.Join(r.PackageDir(id, version), EnvFileName)
}

// ResolveEntry validates and returns the executable entry of the package rooted
// at pkgDir whose manifest declares the given id.
//
// Installer and supervisor share this function on purpose: "what counts as a
// runnable package" must not be decided twice, or a package will install fine
// and then refuse to start (or the reverse).
//
// Rejections, in order:
//   - an id that is not a safe token (path traversal, separators, "plugin.so");
//   - the presence of the retired plugin.so entry — even next to a valid
//     executable, because that is an ambiguous package and silently picking one
//     of the two forms is how mismatched code ships unnoticed;
//   - a missing entry (ErrMissingEntry);
//   - an entry that is a directory or a symlink (ErrInvalidEntry). Symlinks are
//     refused rather than followed: the package directory is written by the
//     installer and must be immutable, and a link could point outside of it.
//
// Permission normalisation is *not* done here: resolution must stay read-only so
// the supervisor can call it on every start. The installer chmods after this
// check succeeds (see verifier.VerifyDirectory).
func ResolveEntry(pkgDir, id string) (string, error) {
	if id == "" || id == LegacyEntryFileName || !entryIDPattern.MatchString(id) {
		return "", fmt.Errorf("%w: illegal package id %q", ErrInvalidEntry, id)
	}

	legacy := filepath.Join(pkgDir, LegacyEntryFileName)
	if _, err := os.Lstat(legacy); err == nil {
		return "", fmt.Errorf("%w: %s", ErrLegacyEntry, legacy)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect legacy entry %s: %w", legacy, err)
	}

	entry := filepath.Join(pkgDir, id)
	info, err := os.Lstat(entry)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", ErrMissingEntry, entry)
		}
		return "", fmt.Errorf("inspect package entry %s: %w", entry, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s (mode %s)", ErrInvalidEntry, entry, info.Mode())
	}
	return entry, nil
}
