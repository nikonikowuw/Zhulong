//go:build !linux

package algorunner

import "syscall"

// runnerSysProcAttr mirrors the Linux variant minus Pdeathsig, which only
// exists on Linux (prctl(PR_SET_PDEATHSIG)). Process-group isolation still
// applies, so Stop keeps reaching grandchildren.
//
// On these platforms the host-death cascade relies on the runner observing the
// peer hang-up itself (see ipc::UnixSocketClient::peer_closed), so an orphan is
// reaped within one receive-poll cycle instead of never.
// PackageSysProcAttr is runnerSysProcAttr under an exported name; see the Linux
// variant for why the two must not be written twice.
func PackageSysProcAttr() *syscall.SysProcAttr { return runnerSysProcAttr() }

func runnerSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}
