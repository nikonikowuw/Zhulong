package algorunner

import (
	"errors"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// PinnedProcess is an algorithm package subprocess whose OS thread stays pinned
// for the whole child lifetime.
//
// Why this exists at all: `Pdeathsig` is delivered when the *OS thread* that
// forked the child exits, not when the Go process exits (go.dev/issue/27505). A
// spawn that is not pinned therefore inherits the lifetime of whatever M happened
// to run exec — when Go retires that thread, the kernel SIGKILLs a perfectly
// healthy child. Pinning the caller's goroutine is equally wrong: an HTTP handler
// or Fx hook is free to return at any time, and the runtime would then retire the
// thread under a live child.
//
// The only safe shape is: pin a dedicated goroutine's thread, spawn there, and keep
// that goroutine (and thus the thread) alive until the child has been reaped.
// StartPinned is that shape, packaged so callers cannot get it subtly wrong.
//
// RunnerProcess.Start is the other (deliberately separate) implementation of this
// discipline: it additionally owns the ready handshake with its exit watcher and
// the stdout/stderr log drain, which is why it does not call StartPinned. Keep the
// two in sync when changing either spawn path.
type PinnedProcess struct {
	cmd *exec.Cmd
	pid int

	done chan struct{}

	mu      sync.RWMutex
	waitErr error
}

// StartPinned starts cmd under the pinned-thread contract described above.
//
// It installs the package SysProcAttr (own process group + Pdeathsig) when the
// caller did not set one, so a caller can never accidentally spawn an
// unkillable-by-group, orphan-surviving child.
//
// The returned process owns cmd.Wait: callers must never call Wait themselves.
// Read Err only after Done is closed.
func StartPinned(cmd *exec.Cmd) (*PinnedProcess, error) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = PackageSysProcAttr()
	}
	p := &PinnedProcess{cmd: cmd, done: make(chan struct{})}
	started := make(chan error, 1)

	go func() {
		runtime.LockOSThread()
		// Deliberately no defer: every exit path unlocks explicitly, and the
		// unlock must happen on this very goroutine — UnlockOSThread from another
		// goroutine is a no-op on its own thread and would leave this one pinned.
		if err := cmd.Start(); err != nil {
			runtime.UnlockOSThread()
			started <- err
			close(p.done)
			return
		}
		started <- nil
		waitErr := cmd.Wait()
		p.mu.Lock()
		p.waitErr = waitErr
		p.mu.Unlock()
		close(p.done)
		runtime.UnlockOSThread()
	}()

	if err := <-started; err != nil {
		return nil, err
	}
	p.pid = cmd.Process.Pid
	return p, nil
}

// PID returns the child pid (also the process-group id, thanks to Setpgid).
func (p *PinnedProcess) PID() int { return p.pid }

// Done is closed once the child has exited and been reaped.
func (p *PinnedProcess) Done() <-chan struct{} { return p.done }

// Err returns the result of cmd.Wait. It is only meaningful after Done is closed.
func (p *PinnedProcess) Err() error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.waitErr
}

// SignalGroup delivers sig to the child's whole process group.
//
// The group matters: the child may spawn grandchildren (an inference runtime's
// helper threads are threads, but a vendor SDK is free to fork). Signalling only
// the pid would leave those holding shared memory or sockets after the child is
// gone. ESRCH is reported as-is so callers can treat "already gone" distinctly
// from a real failure.
func (p *PinnedProcess) SignalGroup(sig syscall.Signal) error {
	return syscall.Kill(-p.pid, sig)
}

// KillGroup is the escalation path: unrecoverable children (a hung inference
// call, a protocol desync) must not be allowed to outlive the request.
func (p *PinnedProcess) KillGroup() error {
	err := p.SignalGroup(syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
