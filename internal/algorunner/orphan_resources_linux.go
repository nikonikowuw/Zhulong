//go:build linux

package algorunner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"go.uber.org/zap"
)

// socketSweepResult is the outcome of one socket-file decision.
type socketSweepResult struct {
	removed    bool
	unverified bool
	err        error
}

// removeStaleBrokerSocket unlinks sockPath only when it can prove no process
// still owns the socket.
//
// ⚠️ A connect probe alone is not proof of death. UnixSocketServer::start does
// bind() then listen(), so between those two calls a LIVE broker answers
// ECONNREFUSED exactly like an abandoned socket file. Liveness is therefore
// decided primarily from /proc/net/unix, which lists a path from bind() onwards
// regardless of listen state; the probe stays as a fallback for a broker that is
// bound in a different network namespace (a container sharing /tmp), whose entry
// this namespace's table cannot show.
func removeStaleBrokerSocket(sockPath string) socketSweepResult {
	if sockPath == "" {
		return socketSweepResult{}
	}
	before, err := statFileID(sockPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return socketSweepResult{}
		}
		return socketSweepResult{err: err}
	}

	bound, err := unixSocketPaths()
	if err != nil {
		// Unknown ownership: leave the file for a later boot.
		return socketSweepResult{unverified: true}
	}
	if _, listed := bound[sockPath]; listed {
		return socketSweepResult{}
	}

	alive, probeErr := brokerSocketServing(sockPath)
	if probeErr != nil {
		return socketSweepResult{unverified: true} // Unclassifiable: leave it alone.
	}
	if alive {
		return socketSweepResult{}
	}

	removed, err := removeIfUnchanged(sockPath, before)
	if err != nil {
		return socketSweepResult{err: err}
	}
	return socketSweepResult{removed: removed}
}

// removeIfUnchanged unlinks path only when it still identifies the object that was
// inspected.
//
// Between the liveness probe and the unlink, another process can unlink the stale
// file and bind a fresh broker at the same path. Unlinking by name would then
// delete the NEW socket and leave an unreachable live broker, so the identity is
// re-checked immediately before the removal.
func removeIfUnchanged(path string, expected fileID) (bool, error) {
	current, err := statFileID(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if current != expected {
		return false, nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("remove stale broker socket %q: %w", path, err)
	}
	return true, nil
}

// brokerSocketServing reports whether something is accepting connections. A
// missing or dead socket yields (false, nil); unix sockets use a lightweight
// connect probe because the bind state has to be read out of /proc/net/unix.
//
// EAGAIN (backlog full) and EACCES mean "someone is there", not "dead", so they
// are reported as errors and the caller leaves the file alone.
func brokerSocketServing(sockPath string) (bool, error) {
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return false, err
	}
	defer syscall.Close(fd)

	addr := &syscall.SockaddrUnix{Name: sockPath}
	connErr := syscall.Connect(fd, addr)
	if connErr == nil {
		return true, nil
	}
	if errors.Is(connErr, syscall.ECONNREFUSED) || errors.Is(connErr, syscall.ENOENT) {
		return false, nil
	}
	return false, connErr
}

// unixSocketPaths returns the set of filesystem paths with a live AF_UNIX
// endpoint in this network namespace. The kernel publishes the path at bind()
// time, so a socket that is bound but not yet listening is reported as live.
//
// An unreadable table is an error, not an empty set: "no endpoints" would make
// every socket file look abandoned.
func unixSocketPaths() (map[string]struct{}, error) {
	raw, err := os.ReadFile(filepath.Join(procRoot, "net", "unix"))
	if err != nil {
		return nil, err
	}
	paths := make(map[string]struct{})
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue // Unnamed endpoint (no path column).
		}
		path := fields[7]
		if strings.HasPrefix(path, "/") {
			paths[path] = struct{}{}
		}
	}
	return paths, nil
}

// removeAbandonedBrokerSockets sweeps the broker socket directory for socket files
// that nothing is bound to.
func removeAbandonedBrokerSockets() sweepBatch {
	baseDir := brokerSocketBaseDir()
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return sweepBatch{
			removed:    []string{},
			unverified: []string{},
			failures:   []error{},
		} // Unreadable dir is not a sweep failure.
	}

	result := sweepBatch{
		removed:    []string{},
		unverified: []string{},
		failures:   []error{},
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, brokerSocketPrefix) || !strings.HasSuffix(name, brokerSocketSuffix) {
			continue
		}
		full := filepath.Join(baseDir, name)
		outcome := removeStaleBrokerSocket(full)
		switch {
		case outcome.removed:
			result.removed = append(result.removed, full)
		case outcome.err != nil:
			result.failures = append(result.failures, outcome.err)
		case outcome.unverified:
			result.unverified = append(result.unverified, full)
		}
	}
	return result
}

// shmRoot is where POSIX shm objects live on Linux. frame_broker.cpp builds the
// names as "/zhulong_shm_<stream_id>", which shm_open maps to <shmRoot>.
const shmRoot = "/dev/shm"

// holdState is the result of asking /proc whether a file is still in use.
type holdState int

const (
	// holdNone: the kernel proves no live process maps or has the file open.
	holdNone holdState = iota
	// holdLive: a live process still maps or has the file open.
	holdLive
	// holdUnknown: at least one process could not be inspected, so ownership is
	// undecidable. Callers must treat it as held.
	holdUnknown
)

// removeUnheldShmSegments unlinks zhulong shm segments that no live process still
// holds. A segment is only reclaimable once its owner AND every reader are gone:
// unlinking while mapped would not corrupt them (POSIX semantics keep the mapping
// alive) but would make a running stream look broken on restart.
func removeUnheldShmSegments(ctx context.Context, logger *zap.Logger) sweepBatch {
	result := sweepBatch{
		removed:    []string{},
		unverified: []string{},
		failures:   []error{},
	}
	entries, err := os.ReadDir(shmRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result
		}
		result.failures = append(result.failures, fmt.Errorf("orphan sweep: read %q: %w", shmRoot, err))
		return result
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			result.failures = append(result.failures, ctx.Err())
			break
		}
		name := entry.Name()
		if !strings.HasPrefix(name, shmSegmentPrefix) {
			continue
		}
		full := filepath.Join(shmRoot, name)
		state, stateErr := shmHoldState(full)
		if stateErr != nil {
			result.failures = append(result.failures, stateErr)
			continue
		}
		switch state {
		case holdLive:
			logger.Debug("leaving shm segment alone: still held by a live process", zap.String("path", full))
			continue
		case holdUnknown:
			// Ownership could not be verified; report it instead of deleting.
			result.unverified = append(result.unverified, full)
			continue
		}
		if rmErr := os.Remove(full); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
			result.failures = append(result.failures, fmt.Errorf("remove leaked shm segment %q: %w", full, rmErr))
			continue
		}
		result.removed = append(result.removed, full)
	}
	return result
}

// shmHoldState reports whether any live process still maps or has open the shm
// object at path.
//
// ⚠️ Fail-closed: an unreadable process (EACCES under hidepid or for another
// uid, an I/O error mid-scan) is NOT evidence that the segment is free. Only a
// process the kernel proves gone (ENOENT / ESRCH) is skipped. Getting this
// backwards deletes a segment a live runner still maps, which surfaces as a
// broken stream rather than a leaked file.
func shmHoldState(path string) (holdState, error) {
	id, err := statFileID(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return holdNone, nil // Already gone; nothing to decide.
		}
		return holdUnknown, fmt.Errorf("stat shm segment %q: %w", path, err)
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return holdUnknown, fmt.Errorf("orphan sweep: read %q: %w", procRoot, err)
	}
	unknown := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid <= 0 {
			continue
		}
		switch processHoldsFile(pid, id, path) {
		case holdLive:
			return holdLive, nil
		case holdUnknown:
			// Keep scanning: a later process may prove the segment is live, which
			// is the answer the caller acts on either way.
			unknown = true
		}
	}
	if unknown {
		return holdUnknown, nil
	}
	return holdNone, nil
}

// processHoldsFile reports whether pid maps or has an open descriptor for the
// file identified by id.
//
// Both questions matter:
//   - maps covers a runner that already mmap'ed the segment.
//   - open descriptors cover the shm_open → mmap window, where the object is
//     published under its name but not yet mapped by anyone, so a maps-only check
//     sees "free" for a broker that is one statement away from mapping it.
//
// A process that vanished between readdir and the read returns holdNone: the
// kernel entry is gone, so it cannot be holding anything.
func processHoldsFile(pid int, id fileID, path string) holdState {
	procDir := filepath.Join(procRoot, strconv.Itoa(pid))

	mapped, mapsState := processMapsFile(procDir, id)
	if mapsState != holdNone {
		return mapsState
	}
	if mapped {
		return holdLive
	}
	return processHoldsOpenFile(procDir, id, path)
}

// processMapsFile reports whether pid maps id, or why that could not be decided.
// holdNone means "read successfully and not mapped".
func processMapsFile(procDir string, id fileID) (bool, holdState) {
	mapsFile, err := os.Open(filepath.Join(procDir, "maps"))
	if err != nil {
		return false, stateForProcError(err)
	}
	mapped, readErr := mapsFileContains(mapsFile, id)
	closeFileBestEffort(mapsFile)
	if readErr == nil && !mapped {
		return false, holdNone
	}
	if readErr != nil {
		return false, stateForProcError(readErr)
	}
	return true, holdLive
}

// processHoldsOpenFile reports whether any descriptor of pid targets id.
func processHoldsOpenFile(procDir string, id fileID, path string) holdState {
	fdDir, err := os.Open(filepath.Join(procDir, "fd"))
	if err != nil {
		return stateForProcError(err)
	}
	fds, readErr := fdDir.Readdirnames(-1)
	closeFileBestEffort(fdDir)
	if readErr != nil {
		return stateForProcError(readErr)
	}

	for _, fd := range fds {
		target, linkErr := os.Readlink(filepath.Join(procDir, "fd", fd))
		if linkErr != nil {
			if errors.Is(linkErr, os.ErrNotExist) {
				continue // Descriptor closed between readdir and readlink.
			}
			return holdUnknown
		}
		if fdTargetsFile(target, id, path) {
			return holdLive
		}
	}
	return holdNone
}

// stateForProcError classifies a /proc read error: a vanished process proves the
// file is not held, anything else leaves ownership undecidable.
func stateForProcError(err error) holdState {
	if processVanished(err) {
		return holdNone
	}
	return holdUnknown
}

// fdTargetsFile reports whether an /proc/<pid>/fd link target refers to the file
// identified by id.
//
// The textual comparison is the common case (a descriptor opened on the very path
// we are about to unlink). The identity comparison catches aliases — a bind
// mount, a rename, a hardlink — and is only attempted for targets under shmRoot
// so a process with thousands of unrelated descriptors stays cheap.
func fdTargetsFile(target string, id fileID, path string) bool {
	target = strings.TrimSuffix(target, " (deleted)")
	if target == path {
		return true
	}
	if !strings.HasPrefix(target, shmRoot+"/") {
		return false
	}
	targetID, err := statFileID(target)
	if err != nil {
		// Vanished (deleted) or unreadable target: the textual comparison above is
		// the only evidence available, and it did not match.
		return false
	}
	return targetID == id
}

// processVanished reports whether err means the process (or its /proc entry)
// disappeared, as opposed to existing but being unreadable.
func processVanished(err error) bool {
	isNotExist := errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT)
	isNoProcess := errors.Is(err, syscall.ESRCH)
	return isNotExist || isNoProcess
}

// mapsFileContains streams a maps file looking for a mapping of the file
// identified by id, stopping at the first hit.
//
// It matches on the kernel-reported identity (device major:minor plus inode,
// columns 4 and 5) rather than on the pathname, because the pathname column is
// unreliable exactly when it matters: it gains a " (deleted)" suffix once the
// segment has been unlinked, and it is rewritten by a rename.
func mapsFileContains(file *os.File, id fileID) (bool, error) {
	wantDev := fmt.Sprintf("%02x:%02x", id.devMajor, id.devMinor)
	wantIno := strconv.FormatUint(id.ino, 10)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		// Format: addr perms offset dev inode [pathname]
		fields := strings.Fields(scanner.Text())
		if len(fields) < 5 {
			continue
		}
		if fields[3] == wantDev && fields[4] == wantIno {
			return true, nil
		}
	}
	return false, scanner.Err()
}
