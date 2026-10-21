//go:build linux

package algorunner

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"go.uber.org/zap"
)

// OrphanCleanupReport summarises what a startup sweep reclaimed. Every field is
// what was actually acted on, so an operator reading the log can tell a no-op
// sweep from one that reaped real leftovers.
type OrphanCleanupReport struct {
	// Killed lists the PIDs of orphaned runners that were terminated.
	Killed []int
	// RemovedSockets lists broker socket files that no live listener owned.
	RemovedSockets []string
	// RemovedShm lists /dev/shm segments no live process still mapped.
	RemovedShm []string
	// Skipped lists PIDs that matched the runner binary but are owned by a live
	// host (the current process or another running instance) and were left alone.
	Skipped []int
	// UnverifiedShm lists segments that were left in place because the sweep could
	// not prove no live process still held them (unreadable /proc state). They are
	// reported instead of deleted: a false "nobody holds it" answer deletes a
	// segment a running stream still maps.
	UnverifiedShm []string
	// UnverifiedSockets is the socket counterpart of UnverifiedShm.
	UnverifiedSockets []string
}

// procRoot is /proc in production. It is a variable so tests can drive the
// inspection logic against a synthetic process tree instead of depending on which
// unrelated processes happen to be running on the machine.
var procRoot = "/proc"

// ReapOrphanRunners terminates runners left behind by a previous host process and
// reclaims the broker sockets and shared-memory segments they were holding.
//
// Why this exists: Pdeathsig (see runnerSysProcAttr) stops *new* orphans from
// surviving a host crash, but it cannot help runners that were already orphaned
// before the fix shipped, nor can it remove the on-disk artefacts. Without this
// sweep an operator who upgrades still finds a runner burning a full CPU core,
// pinned to no supervisor, that only a manual kill can stop.
//
// Safety rules, in order of importance:
//  1. A process is only considered if its argv[0] basename matches runnerPath, so
//     unrelated processes are never candidates.
//  2. A process is NEVER touched unless it has actually been reparented to an
//     init/systemd reaper, so a runner still owned by a live host — including a
//     second running Zhulong instance — is always left alone.
//  3. Termination is SIGKILL to the candidate's process group (it was started
//     with Setpgid) with a direct-signal fallback, so grandchildren die too.
//  4. Shared memory is only unlinked when no live process still has it mapped or
//     open, and sockets only when no process has it bound, so a live stream is
//     never pulled out from under a running engine.
//  5. Every artefact decision happens under brokerResourceLock, the same
//     directory lock native FrameBroker::start holds while it creates them, so a
//     broker that is halfway through shm_open→mmap or bind→listen cannot be
//     mistaken for an abandoned leftover.
//
// It is safe to call on every startup; a clean boot performs zero destructive
// actions and reports an empty result.
func ReapOrphanRunners(ctx context.Context, runnerPath string, logger *zap.Logger) (OrphanCleanupReport, error) {
	report := OrphanCleanupReport{}
	if logger == nil {
		logger = zap.NewNop()
	}
	runnerBase := filepath.Base(runnerPath)
	if runnerBase == "" || runnerBase == "." || runnerBase == "/" {
		return report, errors.New("orphan sweep: runner path is empty")
	}

	candidates, err := findOrphanCandidates(runnerBase)
	if err != nil {
		return report, fmt.Errorf("orphan sweep: scan /proc: %w", err)
	}

	var failures []error
	for _, cand := range candidates {
		if ctx.Err() != nil {
			failures = append(failures, ctx.Err())
			break
		}
		if !parentIsReaper(cand.PID) {
			report.Skipped = append(report.Skipped, cand.PID)
			logger.Warn("leaving runner alone: it still has a live supervisor",
				zap.Int("pid", cand.PID), zap.Strings("broker_socks", cand.brokerSocks))
			continue
		}

		logger.Warn("reaping orphaned algorithm runner left by a previous host process",
			zap.Int("pid", cand.PID),
			zap.Strings("broker_socks", cand.brokerSocks),
			zap.String("algorithm_id", cand.algorithmID),
		)
		if killErr := killOrphan(cand.PID); killErr != nil {
			failures = append(failures, fmt.Errorf("kill orphan runner %d: %w", cand.PID, killErr))
			continue
		}
		report.Killed = append(report.Killed, cand.PID)

		// The runner holds its shared-memory mapping and socket connection open
		// until it actually dies, so wait for the kernel to retire it before
		// deciding the files are reclaimable.
		if waitForProcessExit(ctx, cand.PID, 3*time.Second) != nil {
			failures = append(failures, fmt.Errorf("orphan runner %d did not exit after SIGKILL", cand.PID))
		}
	}

	// Process termination is independent of the artefact lock (a stuck lock must
	// not leave an orphan burning a core), but every unlink below happens under
	// the same directory lock native FrameBroker::start holds while creating
	// shm segments and broker sockets.
	lock, lockErr := acquireBrokerResourceLock(brokerResourceDirs(), false)
	switch {
	case lockErr == nil:
		defer lock.release()
		for _, cand := range candidates {
			if ctx.Err() != nil {
				break
			}
			if !slices.Contains(report.Killed, cand.PID) {
				// Only sockets belonging to runners this sweep actually reaped are
				// candidates; a supervised runner's socket must never be touched.
				continue
			}
			for _, sock := range cand.brokerSocks {
				outcome := removeStaleBrokerSocket(sock)
				switch {
				case outcome.removed:
					report.RemovedSockets = append(report.RemovedSockets, sock)
				case outcome.err != nil:
					failures = append(failures, outcome.err)
				case outcome.unverified:
					report.UnverifiedSockets = append(report.UnverifiedSockets, sock)
				}
			}
		}

		// Second pass: artefacts left by a host whose runners were already reaped by
		// Pdeathsig (or that never got as far as spawning one). These have no live
		// candidate to derive paths from, so they are judged by their own liveness.
		shmResult := removeUnheldShmSegments(ctx, logger)
		report.RemovedShm = append(report.RemovedShm, shmResult.removed...)
		report.UnverifiedShm = append(report.UnverifiedShm, shmResult.unverified...)
		failures = append(failures, shmResult.failures...)

		sockResult := removeAbandonedBrokerSockets(logger)
		report.RemovedSockets = append(report.RemovedSockets, sockResult.removed...)
		report.UnverifiedSockets = append(report.UnverifiedSockets, sockResult.unverified...)
		failures = append(failures, sockResult.failures...)
	case isResourceLockBusy(lockErr):
		// A broker is being created right now, so every artefact under the lock is
		// potentially in its create window. Skip reclamation entirely; the next
		// boot will retry. Not a failure: the host is fully usable.
		logger.Warn("skipping orphan artefact sweep: broker resources are being created",
			zap.String("reason", "resource_lock_busy"))
	default:
		failures = append(failures, fmt.Errorf("orphan sweep: %w", lockErr))
	}

	if len(report.Killed) > 0 || len(report.RemovedShm) > 0 || len(report.RemovedSockets) > 0 {
		logger.Warn("startup orphan sweep reclaimed leaked algorithm resources",
			zap.Ints("killed_pids", report.Killed),
			zap.Strings("removed_sockets", report.RemovedSockets),
			zap.Strings("removed_shm", report.RemovedShm),
			zap.Ints("skipped_pids", report.Skipped),
		)
	}
	if len(report.UnverifiedShm) > 0 || len(report.UnverifiedSockets) > 0 {
		logger.Warn("startup orphan sweep left artefacts in place: ownership could not be verified",
			zap.Strings("unverified_shm", report.UnverifiedShm),
			zap.Strings("unverified_sockets", report.UnverifiedSockets),
		)
	}
	return report, errors.Join(failures...)
}

type orphanCandidate struct {
	PID         int
	brokerSocks []string
	algorithmID string
}

// findOrphanCandidates lists processes whose argv[0] resolves to the runner
// binary. No liveness judgement happens here; callers decide.
func findOrphanCandidates(runnerBase string) ([]orphanCandidate, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}

	self := os.Getpid()
	candidates := make([]orphanCandidate, 0, 4)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, convErr := strconv.Atoi(entry.Name())
		if convErr != nil || pid <= 0 || pid == self {
			continue
		}
		argv, readErr := readCmdline(pid)
		if readErr != nil || len(argv) == 0 {
			continue
		}
		if filepath.Base(argv[0]) != runnerBase {
			continue
		}

		cand := orphanCandidate{PID: pid}
		for i := 1; i < len(argv); i++ {
			switch argv[i] {
			case "--broker":
				if i+1 < len(argv) && argv[i+1] != "" {
					cand.brokerSocks = append(cand.brokerSocks, argv[i+1])
					i++
				}
			case "--algorithm-id":
				if i+1 < len(argv) {
					cand.algorithmID = argv[i+1]
					i++
				}
			}
		}
		candidates = append(candidates, cand)
	}
	return candidates, nil
}

// readCmdline returns the NUL-separated argv of pid.
func readCmdline(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, errors.New("empty cmdline")
	}
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return nil, errors.New("empty cmdline")
	}
	return strings.Split(trimmed, "\x00"), nil
}

// parentIsReaper reports whether pid was reparented to an init/systemd reaper,
// which is the only reliable signature of an orphan.
//
// ⚠️ Do NOT try to detect this by "the parent is not our executable": that
// misclassifies any runner supervised by a *different* host instance (second
// Zhulong build, different path, a wrapper) and kills a live, supervised process.
// A reparented orphan, by contrast, always has a reaper as its parent, and a
// supervised runner never does — so this predicate is both safer and simpler.
//
// Observed detail: on a systemd user session the reaper is `systemd --user`
// (pid 2020 during development), not pid 1, so ppid == 1 alone is insufficient.
func parentIsReaper(pid int) bool {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		// The entry is gone. Whether it was a zombie or vanished, no supervisor
		// remains to own this process, so the caller may reap it.
		return true
	}
	// stat is "pid (comm) state ppid ..." and comm may contain spaces or parens,
	// so parse from the last ')' instead of splitting the line.
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return false
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) < 2 {
		return false
	}
	ppid, convErr := strconv.Atoi(fields[1])
	if convErr != nil {
		return false
	}
	if ppid <= 1 {
		// Adopted by init itself: definitively orphaned.
		return true
	}

	// /proc/<pid>/comm is world-readable, whereas /proc/<pid>/exe is not readable
	// across some security boundaries (observed: reading systemd --user's exe as its
	// own user returned an empty link here). comm is the reliable signal.
	if comm, commErr := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(ppid), "comm")); commErr == nil {
		name := strings.TrimSpace(string(comm))
		if name == "systemd" || name == "init" {
			return true
		}
	}

	// Fall back to the executable name when comm is unavailable.
	parentExe, linkErr := os.Readlink(filepath.Join(procRoot, strconv.Itoa(ppid), "exe"))
	if linkErr != nil {
		// Parent vanished between the stat and the readlink: also orphaned.
		return errors.Is(linkErr, os.ErrNotExist)
	}
	base := filepath.Base(strings.TrimSuffix(parentExe, " (deleted)"))
	return base == "systemd" || base == "init"
}

// killOrphan terminates the candidate's whole process group when it leads one
// (runners are started with Setpgid, so a negative pid reaches every descendant
// they spawned), then always confirms with a direct signal.
//
// ⚠️ ESRCH from the group signal does NOT mean the process is gone: a runner that
// was not its own group leader (e.g. spawned by a shell, or by an older build
// that predates Setpgid) makes kill(-pid) fail with ESRCH while the process is
// still happily running. Treating that as success would silently skip the kill
// and leave the orphan alive forever, so the direct signal always follows.
func killOrphan(pid int) error {
	groupErr := syscall.Kill(-pid, syscall.SIGKILL)
	if groupErr == nil {
		return nil
	}
	if !errors.Is(groupErr, syscall.ESRCH) {
		// EPERM and friends are real failures worth reporting, but still attempt
		// the direct signal so a group-permission quirk cannot mask a killable pid.
		if dirErr := syscall.Kill(pid, syscall.SIGKILL); dirErr != nil && !errors.Is(dirErr, syscall.ESRCH) {
			return errors.Join(groupErr, dirErr)
		}
		return nil
	}

	if dirErr := syscall.Kill(pid, syscall.SIGKILL); dirErr != nil && !errors.Is(dirErr, syscall.ESRCH) {
		return dirErr
	}
	return nil
}

func waitForProcessExit(ctx context.Context, pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return nil
		}
		// ⚠️ A zombie still answers kill(pid, 0) successfully, yet it has already
		// released every fd, mapping and socket. Since this sweep only cares about
		// resource ownership, a zombie counts as gone — waiting for a reparented
		// zombie to be reaped by init would otherwise burn the whole timeout and
		// report a false failure.
		if processIsZombie(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("process %d still present after %s", pid, timeout)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// processIsZombie reports whether pid has already exited and is only waiting to
// be reaped by its parent.
func processIsZombie(pid int) bool {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		// The entry is gone, which is the outcome the caller wants anyway.
		return true
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return false
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) == 0 {
		return false
	}
	return fields[0] == "Z" || fields[0] == "X"
}

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
// delete the NEW socket and leave a live broker unreachable, so the identity is
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
		return false, fmt.Errorf("remove stale broker socket %s: %w", path, err)
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

// shmRoot is where POSIX shm objects live on Linux. frame_broker.cpp builds the
// names as "/zhulong_shm_<stream_id>", which shm_open maps to <shmRoot>.
const shmRoot = "/dev/shm"

// shmSweepResult is the outcome of one shm decision batch.
type shmSweepResult struct {
	removed    []string
	unverified []string
	failures   []error
}

// socketSweepBatch is the outcome of one socket-directory sweep.
type socketSweepBatch struct {
	removed    []string
	unverified []string
	failures   []error
}

// removeUnheldShmSegments unlinks zhulong shm segments that no live process still
// holds. A segment is only reclaimable once its owner AND every reader are gone:
// unlinking while mapped would not corrupt them (POSIX semantics keep the mapping
// alive) but would make a running stream look broken on restart.
func removeUnheldShmSegments(ctx context.Context, logger *zap.Logger) shmSweepResult {
	var result shmSweepResult
	entries, err := os.ReadDir(shmRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return result
		}
		result.failures = append(result.failures, fmt.Errorf("orphan sweep: read %s: %w", shmRoot, err))
		return result
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			result.failures = append(result.failures, ctx.Err())
			break
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "zhulong_shm_") {
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
			result.failures = append(result.failures, fmt.Errorf("remove leaked shm segment %s: %w", full, rmErr))
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
		return holdUnknown, fmt.Errorf("stat shm segment %s: %w", path, err)
	}

	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return holdUnknown, fmt.Errorf("orphan sweep: read %s: %w", procRoot, err)
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

	mapsFile, err := os.Open(filepath.Join(procDir, "maps"))
	if err != nil {
		if processVanished(err) {
			return holdNone
		}
		return holdUnknown
	}
	mapped, readErr := mapsFileContains(mapsFile, id)
	_ = mapsFile.Close()
	if readErr != nil {
		if processVanished(readErr) {
			return holdNone
		}
		return holdUnknown
	}
	if mapped {
		return holdLive
	}

	fdDir, err := os.Open(filepath.Join(procDir, "fd"))
	if err != nil {
		if processVanished(err) {
			return holdNone
		}
		return holdUnknown
	}
	fds, err := fdDir.Readdirnames(-1)
	_ = fdDir.Close()
	if err != nil {
		if processVanished(err) {
			return holdNone
		}
		return holdUnknown
	}
	for _, fd := range fds {
		target, err := os.Readlink(filepath.Join(procDir, "fd", fd))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
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
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ESRCH)
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
	if err := scanner.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// removeAbandonedBrokerSockets sweeps broker directories for socket files that
// nothing is bound to. The base directory mirrors pipeline/engine.cpp: the
// ZHULONG_FRAME_BROKER_SOCK override, else /tmp.
func removeAbandonedBrokerSockets(logger *zap.Logger) socketSweepBatch {
	baseDir := brokerResourceDirs()[0]
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return socketSweepBatch{} // Unreadable dir is not a sweep failure.
	}

	var result socketSweepBatch
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, "zhulong_broker_") || !strings.HasSuffix(name, ".sock") {
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
