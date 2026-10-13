package mediafile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/nikonikowuw/Zhulong/internal/apperr"
)

// mediaRootKind is the top-level directory whitelist. The service does not
// interpret the meaning of these directories; it only guarantees that reads
// stay inside them, so a future stray file directly under the media root (a
// lock marker, an index file) can never be served.
var mediaRootKind = map[string]bool{
	"recordings": true,
	"snapshots":  true,
	"exports":    true,
}

// mediaPathSegment matches one path element: letters, digits, underscore,
// hyphen and dot, at most 128 characters.
//
// Note that "." and ".." also match this pattern, so they must be rejected
// explicitly. Relying on the pattern alone would let a traversal sequence
// through the first defence layer.
var mediaPathSegment = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)

const (
	// mediaPathMaxLength bounds the accepted relative path, matching the
	// contract the event domain must satisfy when it persists a path.
	mediaPathMaxLength = 512

	// escapeErrText is the unexported text os.Root reports when a path escapes
	// the root. The standard library exposes no sentinel error for it, so the
	// text is the only available signal; resolver_test.go asserts it so a Go
	// upgrade that rewords the message fails loudly instead of silently
	// downgrading the classification.
	escapeErrText = "path escapes"
)

// resolver turns an untrusted relative path into a file descriptor that is
// guaranteed to live inside the media root.
//
// It is the single entry point for every filesystem read this service performs.
// Nothing else in the package may call os.Open or filepath.Join on a
// client-supplied value.
type resolver struct {
	root *os.Root
}

// newResolver opens the media root and validates it is not a fallen-through
// external mount.
//
// The fallthrough check runs here, at handle construction, and not on the
// request path: inspecting a filesystem reads /proc/mounts, and a single video
// preview issues tens to hundreds of range requests.
func newResolver(rootPath string, inspector DeviceInspector) (*resolver, error) {
	if rootPath == "" {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is not configured", nil)
	}

	// OpenRoot follows a symlinked root path, which is deliberate: operators
	// commonly point the media directory at a symlinked mount point.
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
	}

	selfDev, err := deviceIDOf(root)
	if err != nil {
		root.Close()
		return nil, apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
	}

	if err := verifyNotFallenThrough(inspector, rootPath, selfDev); err != nil {
		root.Close()
		return nil, err
	}

	return &resolver{
		root: root,
	}, nil
}

// Close releases the root handle. File descriptors obtained earlier through
// open or stat stay valid, which is what lets an in-flight transfer finish
// across a configuration hot switch.
func (r *resolver) Close() error {
	if r.root == nil {
		return nil
	}
	return r.root.Close()
}

// open resolves the relative path and returns an open file inside the root.
func (r *resolver) open(relPath string) (*os.File, error) {
	clean, err := r.validate(relPath)
	if err != nil {
		return nil, err
	}

	file, err := r.root.Open(clean)
	if err != nil {
		return nil, mapOpenError(err)
	}
	return file, nil
}

// stat resolves the relative path and returns the validated relative path plus
// its metadata, without opening a transfer.
func (r *resolver) stat(relPath string) (string, os.FileInfo, error) {
	clean, err := r.validate(relPath)
	if err != nil {
		return "", nil, err
	}

	info, err := r.root.Stat(clean)
	if err != nil {
		return "", nil, mapOpenError(err)
	}
	return clean, info, nil
}

// validate applies the identifier-shape defence layer: it rejects NUL bytes,
// empty paths, absolute paths, traversal segments and characters that cannot
// appear in a media path.
//
// It accepts only a path relative to the media root. Stripping the leading
// slash that Gin leaves on wildcard parameters is the handler's job, not this
// function's: accepting a leading slash here would make "//etc/passwd" and
// "/etc/passwd" indistinguishable from a legitimate "etc/passwd", and the
// contract this service publishes is that it never accepts an absolute path.
//
// The returned value is never reassembled with filepath.Join: joining a
// cleaned path and reopening it would reintroduce the TOCTOU window that
// os.Root exists to eliminate.
func (r *resolver) validate(relPath string) (string, error) {
	if relPath == "" {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must not be empty", nil)
	}
	if strings.ContainsRune(relPath, '\x00') {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must not contain NUL bytes", nil)
	}
	if len(relPath) > mediaPathMaxLength {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path is too long", nil)
	}
	if filepath.IsAbs(relPath) {
		return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must be relative to the media root", nil)
	}

	segments := strings.Split(relPath, "/")
	for _, segment := range segments {
		// Rejects the empty segment produced by "//" and the trailing slash
		// produced by "a.mp4/", plus "." and ".." which match the pattern.
		if segment == "." || segment == ".." {
			return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path must not contain relative segments", nil)
		}
		if !mediaPathSegment.MatchString(segment) {
			return "", apperr.Invalid("MEDIA_PATH_INVALID", "Media path contains an invalid segment", nil)
		}
	}

	if !mediaRootKind[segments[0]] {
		return "", apperr.Invalid("MEDIA_KIND_INVALID", "Media path must start with a known media directory", nil)
	}

	return relPath, nil
}

// mapOpenError converts an os.Root failure into the public error contract.
//
// Three rejection shapes exist:
//
//   - A symlink whose target leaves the root (or is absolute, even when it
//     points back inside) reports "path escapes from parent". That is the only
//     shape that is classified as an escape.
//   - A traversal sequence containing ".." reports ENOENT, indistinguishable
//     from a genuinely missing file.
//   - A path that traverses *through* a regular file reports ENOTDIR.
//
// The last two are deliberately classified as not-found rather than escape.
// Telling the caller that a path was recognised as structurally interesting
// leaks information, and not-found is also what the caller experiences for a
// file deleted by the retention cleaner. Reporting them as an internal error
// would be worse still: a malformed request would look like a server fault and
// pollute the 5xx signal.
//
// The important guarantee is that none of the three is ever a success.
func mapOpenError(err error) error {
	if err == nil {
		return nil
	}

	var pathErr *os.PathError
	if errors.As(err, &pathErr) && strings.Contains(pathErr.Err.Error(), escapeErrText) {
		return apperr.Invalid("MEDIA_PATH_ESCAPE", "Media path escapes the media root", err)
	}

	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return apperr.NotFound("MEDIA_ENTRY_NOT_FOUND", "Media entry not found", err)
	}

	// Fail closed: an unrecognised failure is never reported as a missing file
	// the caller may retry into, nor as success.
	return apperr.Internal("MEDIA_ENTRY_UNREADABLE", "Media entry could not be read", err)
}

// deviceIDOf returns the st_dev of the directory the handle points at, or 0 if
// device identity is unsupported on this platform.
func deviceIDOf(root *os.Root) (uint64, error) {
	info, err := root.Stat(".")
	if err != nil {
		return 0, fmt.Errorf("stat media root: %w", err)
	}
	deviceID, ok := deviceIDFromFileInfo(info)
	if !ok {
		return 0, nil
	}
	return deviceID, nil
}

// verifyNotFallenThrough rejects a media root whose device identity shows an
// external disk dropped off.
//
// The rule mirrors storage's dismount detection without reading its private
// state: only a root that started on a non-root device and now resolves to the
// root device has fallen through. A media root that legitimately lives on the
// system filesystem is never judged, which is the default configuration
// ("data/media") and the case a naive "device != expected" comparison would
// reject on every single request.
//
// The check is skipped where the inspector does not return real kernel
// telemetry. Comparing the real st_dev that os.Root.Stat reports against
// storage's synthetic non-Linux constants would produce a false fallthrough and
// refuse every read on a developer host.
func verifyNotFallenThrough(inspector DeviceInspector, mediaRoot string, selfDev uint64) error {
	if inspector == nil || !deviceTelemetryIsReal {
		return nil
	}

	stats, err := inspector.InspectFS(mediaRoot)
	if err != nil {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
	}

	rootDev, err := inspector.GetRootDeviceID()
	if err != nil {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root is unavailable", err)
	}

	if isFallenThrough(selfDev, stats.DeviceID, rootDev) {
		return apperr.Internal("MEDIA_ROOT_UNAVAILABLE", "Media root device changed", nil)
	}
	return nil
}

// isFallenThrough is the decision itself, separated from the platform gate and
// the inspector calls so the rule can be tested on every host rather than only
// where real kernel telemetry exists.
//
// selfDev is the media root's device captured when the handle was built; statsDev
// is the device the root resolves to now; rootDev is the system filesystem's
// device. Only a root that started on a non-root device and now resolves to the
// root device has fallen through.
func isFallenThrough(selfDev, statsDev, rootDev uint64) bool {
	return selfDev != rootDev && statsDev == rootDev
}
