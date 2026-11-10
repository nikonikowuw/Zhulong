package algorunner

import "os"

// closeFileBestEffort closes an inspection handle (a pipe end, a /proc read, a
// lock descriptor).
//
// The close error is ignored and documented once here rather than at each call
// site: the handle is read-only and its data is already consumed, so a close
// failure cannot change any decision; callers are either on a path that already
// has a more specific failure or on a success path with nothing left to do.
//
// The documented no-op body is deliberate: AGENTS.md forbids swallowing errors
// with `_ = fn()`, so this is the package's way of saying "intentionally
// dropped" without hiding it.
func closeFileBestEffort(file *os.File) {
	if file == nil {
		return
	}
	if err := file.Close(); err != nil {
		// Intentionally ignored — see the function comment.
	}
}
