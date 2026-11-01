//go:build linux

package algorunner

import (
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

// orphanExitTimeout bounds how long a SIGKILLed orphan may keep its resources.
const orphanExitTimeout = 3 * time.Second

// procRoot is /proc in production. It is a variable so tests can drive the
// inspection logic against a synthetic process tree instead of depending on which
// unrelated processes happen to be running on the machine. It is a test seam only:
// production code must never assign it.
var procRoot = "/proc"

// errMalformedProcStat marks a /proc/<pid>/stat line that cannot be parsed. It is
// distinct from a read error: "gone" and "unparseable" lead to opposite decisions.
var errMalformedProcStat = errors.New("malformed /proc stat line")

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
//  1. A process is only considered if its executable resolves to
//     <algorithmsRoot>/<id>/<version>/<id>, so unrelated processes are never
//     candidates.
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
func ReapOrphanRunners(ctx context.Context, algorithmsDir string, logger *zap.Logger) (OrphanCleanupReport, error) {
	if logger == nil {
		logger = zap.NewNop()
	}

	report := OrphanCleanupReport{}

	root, err := resolveSweepRoot(algorithmsDir)
	if err != nil {
		return report, err
	}

	candidates, err := findOrphanCandidates(root)
	if err != nil {
		return report, fmt.Errorf("orphan sweep: scan /proc: %w", err)
	}

	failures := sweepOrphanProcesses(ctx, candidates, &report, logger)
	failures = append(failures, sweepOrphanArtifacts(ctx, candidates, &report, logger)...)

	logSweepOutcome(&report, logger)
	return report, errors.Join(failures...)
}

// resolveSweepRoot validates the install root and makes it symlink-free.
//
// /proc/<pid>/exe is already symlink-free, so the root must be too — otherwise a
// data directory reached through a symlink (bind mount, relocated volume) never
// matches and the sweep silently stops working.
func resolveSweepRoot(algorithmsDir string) (string, error) {
	isEmpty := algorithmsDir == "" || algorithmsDir == "."
	root, absErr := filepath.Abs(algorithmsDir)
	if isEmpty || absErr != nil {
		return "", fmt.Errorf("orphan sweep: algorithms directory %q is unusable", algorithmsDir)
	}
	if resolved, evalErr := filepath.EvalSymlinks(root); evalErr == nil {
		return resolved, nil
	}
	return root, nil
}

// sweepOrphanProcesses kills the runners this sweep owns and reports the ones it
// left alone.
func sweepOrphanProcesses(
	ctx context.Context,
	candidates []orphanCandidate,
	report *OrphanCleanupReport,
	logger *zap.Logger,
) []error {
	var failures []error
	for _, cand := range candidates {
		if ctx.Err() != nil {
			return append(failures, ctx.Err())
		}
		if !parentIsReaper(cand.PID) {
			report.Skipped = append(report.Skipped, cand.PID)
			logger.Warn("leaving runner alone: it still has a live supervisor",
				zap.Int("pid", cand.PID),
				zap.Strings("broker_socks", cand.brokerSocks),
			)
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
		if waitErr := waitForProcessExit(ctx, cand.PID, orphanExitTimeout); waitErr != nil {
			failures = append(failures, fmt.Errorf("orphan runner %d did not exit after SIGKILL", cand.PID))
		}
	}
	return failures
}

// sweepOrphanArtifacts reclaims the socket files and shm segments nothing owns.
//
// Process termination is independent of the artefact lock (a stuck lock must not
// leave an orphan burning a core), but every unlink here happens under the same
// directory lock native FrameBroker::start holds while creating shm segments and
// broker sockets.
func sweepOrphanArtifacts(
	ctx context.Context,
	candidates []orphanCandidate,
	report *OrphanCleanupReport,
	logger *zap.Logger,
) []error {
	lock, lockErr := acquireBrokerResourceLock(brokerResourceDirs(), lockNonBlocking)
	switch {
	case isResourceLockBusy(lockErr):
		// A broker is being created right now, so every artefact under the lock is
		// potentially in its create window. Skip reclamation entirely; the next
		// boot will retry. Not a failure: the host is fully usable.
		logger.Warn("skipping orphan artefact sweep: broker resources are being created",
			zap.String("reason", "resource_lock_busy"),
		)
		return nil
	case lockErr != nil:
		return []error{fmt.Errorf("orphan sweep: %w", lockErr)}
	}
	defer lock.release()

	failures := sweepReapedRunnerSockets(ctx, candidates, report)

	// Second pass: artefacts left by a host whose runners were already reaped by
	// Pdeathsig (or that never got as far as spawning one). These have no live
	// candidate to derive paths from, so they are judged by their own liveness.
	failures = append(failures, report.mergeShmBatch(removeUnheldShmSegments(ctx, logger))...)
	failures = append(failures, report.mergeSocketBatch(removeAbandonedBrokerSockets())...)
	return failures
}

// sweepReapedRunnerSockets removes the broker sockets that belonged to runners
// this sweep actually killed. A supervised runner's socket must never be touched,
// which is why the candidate set is filtered by report.Killed.
func sweepReapedRunnerSockets(
	ctx context.Context,
	candidates []orphanCandidate,
	report *OrphanCleanupReport,
) []error {
	var failures []error
	for _, cand := range candidates {
		if ctx.Err() != nil {
			return failures
		}
		if !slices.Contains(report.Killed, cand.PID) {
			continue
		}
		for _, sock := range cand.brokerSocks {
			failures = append(failures, report.recordSocket(sock, removeStaleBrokerSocket(sock))...)
		}
	}
	return failures
}

// logSweepOutcome reports what the sweep did, in the two categories an operator
// needs to distinguish: reclaimed, and left in place because ownership was
// undecidable.
func logSweepOutcome(report *OrphanCleanupReport, logger *zap.Logger) {
	reclaimedAnything := len(report.Killed) > 0 ||
		len(report.RemovedShm) > 0 ||
		len(report.RemovedSockets) > 0
	if reclaimedAnything {
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
}

// sweepBatch is the outcome of one artefact batch sweep: what was removed, what
// was left because ownership could not be proven, and what failed.
type sweepBatch struct {
	removed    []string
	unverified []string
	failures   []error
}

// recordSocket folds one socket decision into the report.
func (r *OrphanCleanupReport) recordSocket(sock string, outcome socketSweepResult) []error {
	switch {
	case outcome.removed:
		r.RemovedSockets = append(r.RemovedSockets, sock)
	case outcome.err != nil:
		return []error{outcome.err}
	case outcome.unverified:
		r.UnverifiedSockets = append(r.UnverifiedSockets, sock)
	}
	return nil
}

func (r *OrphanCleanupReport) mergeSocketBatch(batch sweepBatch) []error {
	r.RemovedSockets = append(r.RemovedSockets, batch.removed...)
	r.UnverifiedSockets = append(r.UnverifiedSockets, batch.unverified...)
	return batch.failures
}

func (r *OrphanCleanupReport) mergeShmBatch(batch sweepBatch) []error {
	r.RemovedShm = append(r.RemovedShm, batch.removed...)
	r.UnverifiedShm = append(r.UnverifiedShm, batch.unverified...)
	return batch.failures
}

type orphanCandidate struct {
	PID         int
	brokerSocks []string
	algorithmID string
}

// isPackageEntryPath reports whether exePath is `<algorithmsRoot>/<id>/<version>/<id>`,
// i.e. a package process this host could have started.
//
// Matching on the *shape* rather than on a basename is what makes the sweep work
// for package executables: every package has its own name, so there is no single
// runner name left to compare against, but the install layout is fixed. The
// trailing component must equal the id component, so an unrelated binary placed
// under the version directory is not mistaken for the package.
func isPackageEntryPath(exePath, algorithmsRoot string) bool {
	if exePath == "" || !filepath.IsAbs(exePath) {
		return false
	}
	rel, err := filepath.Rel(algorithmsRoot, filepath.Clean(exePath))
	if err != nil {
		return false
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) != 3 {
		return false
	}
	return parts[0] != "." && parts[0] != ".." && parts[0] == parts[2]
}

// readExePath resolves /proc/<pid>/exe to an absolute path.
//
// This — not argv[0] — is the identity of a running process: argv[0] is whatever
// the parent passed, so `exec -a <package entry> /bin/sleep 300` forges it
// trivially. Killing on a forged argv[0] would be an arbitrary-process kill.
//
// The kernel appends " (deleted)" once the executable has been replaced or
// removed (a package upgrade in place does exactly that); stripping it keeps a
// legitimately orphaned package process recognizable.
func readExePath(pid int) (string, error) {
	target, err := os.Readlink(filepath.Join(procRoot, strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(target, " (deleted)"), nil
}

// findOrphanCandidates lists processes whose *executable* is a package entry
// inside algorithmsRoot. argv is still read, but only for `--broker` /
// `--algorithm-id` bookkeeping. No liveness judgement happens here; callers
// decide.
func findOrphanCandidates(algorithmsRoot string) ([]orphanCandidate, error) {
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
		exePath, exeErr := readExePath(pid)
		if exeErr != nil {
			// EACCES (another user's process), ENOENT (exited mid-scan): nothing to
			// judge, and guessing from argv[0] is exactly what we refuse to do.
			continue
		}
		if !isPackageEntryPath(exePath, algorithmsRoot) {
			continue
		}
		argv, readErr := readCmdline(pid)
		if readErr != nil {
			// The executable is ours but the arguments are gone; still a candidate,
			// just without broker bookkeeping.
			argv = nil
		}
		candidates = append(candidates, candidateFromArgv(pid, argv))
	}
	return candidates, nil
}

// candidateFromArgv extracts the broker and algorithm bookkeeping from argv.
func candidateFromArgv(pid int, argv []string) orphanCandidate {
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
	return cand
}

// readCmdline returns the NUL-separated argv of pid.
func readCmdline(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimRight(string(raw), "\x00")
	if trimmed == "" {
		return nil, errors.New("empty cmdline")
	}
	return strings.Split(trimmed, "\x00"), nil
}

// procStatFields returns the /proc/<pid>/stat fields after the command name:
// index 0 is the process state, index 1 is the parent pid.
//
// stat is "pid (comm) state ppid ..." and comm may contain spaces or parentheses,
// so the line is parsed from the LAST ')' instead of being split: splitting would
// shift every field after a comm such as "my (odd) name". This lives in one
// function because the same parse decides both "is it a zombie" and "is it
// reparented", and two copies drift.
func procStatFields(pid int) ([]string, error) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, err
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return nil, errMalformedProcStat
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) == 0 {
		return nil, errMalformedProcStat
	}
	return fields, nil
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
	fields, err := procStatFields(pid)
	if err != nil {
		// The entry is gone. Whether it was a zombie or vanished, no supervisor
		// remains to own this process, so the caller may reap it. An unparseable
		// line is not evidence of that, so it is treated as "not an orphan".
		return !errors.Is(err, errMalformedProcStat)
	}
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
	fields, err := procStatFields(pid)
	if err != nil {
		// The entry is gone, which is the outcome the caller wants anyway. An
		// unparseable line proves nothing, so it is not reported as a zombie.
		return !errors.Is(err, errMalformedProcStat)
	}
	return fields[0] == "Z" || fields[0] == "X"
}
