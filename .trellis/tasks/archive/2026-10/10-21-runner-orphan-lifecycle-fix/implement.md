# Implementation Plan: Runner Orphan Lifecycle Fix

## Ordered Work

1. **Fix the runner thread owner**
   - Refactor `RunnerProcess.Start` to use a dedicated lock-owning lifecycle goroutine and start/readiness handshakes.
   - Keep state transitions, pipe ownership, immediate child exit, and start-error cleanup serialized.
   - Rewrite `TestRunnerProcessSpawnThreadSurvivesWhileChildRuns` so the caller goroutine returns before the child-liveness assertion; add a start-failure cleanup assertion if current test seams support it.

2. **Make Linux cleanup fail closed**
   - Add directory-inode lock acquisition shared by Go and C++; deterministic sort/dedup, blocking native acquire, nonblocking sweep acquire.
   - Move all sweep resource unlinks under the acquired guard while keeping process termination independent.
   - Extend shm liveness checks to maps and open fds; ignore only processes proven to have vanished, propagate access/read errors, and revalidate inode identity before removal.
   - Replace connect-only socket liveness with `/proc/net/unix` bound-endpoint detection, treating unreadable state as unknown; revalidate socket inode before unlink.
   - Add Linux tests for inaccessible process state, fd-held shm, busy lock, bound-but-not-listening socket, unreadable socket table, path replacement, clean stale-artifact removal, and continued startup when cleanup cannot acquire the lock.

3. **Synchronize native FrameBroker setup**
   - Add an RAII directory-lock helper in the native IPC implementation and acquire it around shm initialization plus Unix socket bind/listen in `FrameBroker::start`.
   - Keep the helper Linux-only; report setup failure through the existing `error_msg` return path.
   - Add a native test that holds the shared `/dev/shm` lock and proves broker setup does not publish resources until the lock is released.

4. **Accept the disconnect event**
   - Extend status/stage allowlists in `internal/algorunner/process.go`.
   - Add a parser/log test proving the new event is structured and not counted as invalid.

5. **Restore project guidance and update the executable contract**
   - Restore only the accidentally removed `TRELLIS:END` marker and Pi subagent paragraph in `AGENTS.md`.
   - Update `.trellis/spec/backend/algorithm-guidelines.md` to state that the same goroutine owns `LockOSThread` through `cmd.Wait`, and document the shared resource lock, fail-closed `/proc` checks, and parser allowlist regression contract.
   - Do not edit `web/vite.config.ts` or the three ROI-related frontend tests; they remain with their active task.

## Verification

- `gofmt -w internal/algorunner internal/algorithm`
- `go test -race -count=1 ./internal/algorunner/`
- `go test -race -count=1 ./internal/algorithm/` (use the native wrapper if dependency inspection shows it reaches `internal/engine`)
- `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./internal/algorunner/`
- `make native-test`
- `make check`
- `git diff --check`

## Review Gates and Risks

- Verify the Start handshake cannot strand the pinned goroutine on either success or failure, and that the child cannot exit/reap before log drains and `doneCh` are initialized.
- Verify Go and C++ compute the exact same directory lock set and order, including aliased base paths; verify incomplete `/proc` visibility always preserves candidate resources.
- Lock contention and `/proc` permission failures must preserve resources and must not prevent algorithm lifecycle startup.
- Check the final diff for accidental changes to the four pre-existing ROI/MASK frontend files and unrelated user work.
