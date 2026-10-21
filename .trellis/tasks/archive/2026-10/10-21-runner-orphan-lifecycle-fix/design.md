# Design: Runner Orphan Lifecycle Fix

## Boundaries

The fix spans `internal/algorunner`, the native FrameBroker startup boundary, the runner JSON event parser, and the algorithm lifecycle spec. It does not change the public C ABI, IPC payload layout, runner process-group stop behavior, or frontend ROI/MASK files.

## Decisions

### 1. One goroutine owns the Pdeathsig thread

`RunnerProcess.Start` will keep its existing synchronous contract but delegate process lifetime to one dedicated goroutine. That goroutine calls `runtime.LockOSThread`, executes `cmd.Start`, reports the start result over a buffered handshake, waits for a readiness gate, and then executes `watchExit` synchronously. `watchExit` remains the only successful-path unlock point and calls `cmd.Wait` before releasing the pinned thread. The caller configures `p.cmd`, state, and pipe readers before opening the readiness gate. On start failure the same goroutine unlocks and returns; the caller closes all created pipes and settles the process state.

The readiness gate is necessary because an already-exited child may make `watchExit` reach process bookkeeping immediately. It prevents the watcher from racing incomplete `RunnerProcess` initialization without holding a goroutine lock across the API call.

### 2. Shared resource creation and sweeping use a common Linux lock

The Linux Go sweeper and native `FrameBroker::start` will acquire an exclusive advisory `flock` over the unique directory inodes for the configured broker base directory and `/dev/shm`. Both sides sort the unique `(device, inode)` set identically before locking, avoiding lock-order inversions when paths alias. Native broker setup waits for the lock while creating shm and binding/listening on its socket. The Go sweeper uses nonblocking acquisition; lock contention or an acquisition error skips resource unlinking and is reported to the existing warning path, so host startup is not blocked by cleanup.

The sweep lock covers candidate-derived socket cleanup and the broad shm/socket artifact pass. Runner process termination remains independent and can proceed before lock acquisition.

### 3. Liveness checks fail closed

The shm ownership scan distinguishes vanished processes from unreadable state. It checks `/proc/<pid>/maps` and open file descriptors, comparing open descriptors against the scanned shm inode so the `shm_open`-before-`mmap` window is covered. Permission and read errors are propagated; any uninspectable process prevents removal of the candidate segment. A path is re-statted before unlink and removed only if it still identifies the checked inode.

For sockets, the sweeper reads `/proc/net/unix` and treats any matching bound endpoint as live, regardless of whether `listen` has run yet. Failure to read or parse the table fails closed for socket cleanup. The check runs under the shared resource lock, followed by a path inode revalidation before unlink; this is the compatibility fallback for processes that do not participate in the new lock protocol.

### 4. Runner protocol allowlist stays synchronized

The Go runner parser will accept `info` as a protocol status and `broker_disconnected` as a stage. A parser regression test will assert the event is valid, retains both fields, and does not increment invalid-record counters.

### 5. Project instruction restoration

Only the deleted closing `TRELLIS:END` marker and Pi subagent contract are restored in `AGENTS.md`. Existing frontend edits belonging to the active ROI/MASK task are preserved and left untouched.

## Failure and Compatibility Behavior

- Failure to acquire cleanup locks or inspect `/proc` leaves artifacts in place, returns a cleanup error, and continues host startup through the existing warning behavior.
- Failure to acquire the native setup lock fails that broker start with a status/error message; no broker resource is created outside the synchronization contract.
- Non-Linux orphan cleanup remains a no-op; the native lock implementation is Linux-only because the destructive `/proc` sweep is Linux-only.
- No Go pointer, C ABI, IPC payload, or shared-memory layout changes are introduced.

## Validation Design

- A runner test starts from a short-lived goroutine, waits for that goroutine to return, verifies the child remains alive, then stops and reaps it.
- Linux sweeper tests cover mapped shm, fd-open but not mapped shm, inaccessible maps/fd state, busy lock, stale socket reclamation, a bound-but-not-listening socket, an unreadable `/proc/net/unix`, and preservation of live/replaced socket paths.
- A native FrameBroker test holds the `/dev/shm` directory lock, proves startup waits before creating resources, releases the lock, and verifies successful startup/stop.
- Go protocol-event tests cover `status=info` and `stage=broker_disconnected`.
- Final gates: `go test -race` for affected Go packages, `make native-test`, `make check`, cross-build sanity for the non-Linux algorunner build tag, and `git diff --check`.
