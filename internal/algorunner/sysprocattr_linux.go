//go:build linux

package algorunner

import "syscall"

// runnerSysProcAttr returns the SysProcAttr used to launch zhulong_algo_runner.
//
// Setpgid puts the runner in its own process group so Stop can signal the whole
// group (runner plus any grandchildren it spawns). Without it, a grandchild
// holding the broker socket or the shared-memory segment would outlive the
// runner and keep those resources pinned.
//
// Pdeathsig asks the kernel to SIGKILL the runner when the OS thread that
// created it dies. That is the only mechanism that survives host death modes in
// which no Go code can run at all — SIGKILL, the OOM killer, or a SIGSEGV in
// the C++ engine — all of which bypass every fx OnStop hook, leaving StopAll to
// never run.
//
// ⚠️ SIGKILL, not SIGTERM. The runner installs no signal handler, so SIGTERM
// would reach the kernel default action anyway (no graceful teardown either
// way), while a third-party plugin is free to install a TERM handler and
// survive — reintroducing exactly the orphan this guards against.
//
// ⚠️ Requires the spawn to happen on a pinned OS thread; see
// RunnerProcess.Start for the thread-lifetime contract.
func runnerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
}
