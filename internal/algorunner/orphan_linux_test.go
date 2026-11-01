//go:build linux

package algorunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.uber.org/zap"
)

// installFakePackage materialises a package whose entry is a real ELF binary and
// returns the install root plus the entry path.
//
// ⚠️ The entry must be a real binary, not a shell script: for interpreted
// scripts the kernel rewrites /proc/<pid>/cmdline to "/bin/sh <script> ...", so
// argv[0] would be the interpreter and the sweep would never see the entry.
//
// The layout (<root>/<id>/<version>/<id>) is not incidental — it is exactly what
// isPackageEntryPath matches, because package executables all have different
// names and only the install layout is shared.
func installFakePackage(t *testing.T, id string) (root, entry string) {
	t.Helper()
	root = t.TempDir()
	entry = filepath.Join(root, id, "1.0.0", id)
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatalf("create package dir: %v", err)
	}
	copyBinary(t, "/bin/sleep", entry)
	return root, entry
}

// startReparentedRunner launches runnerPath as a grandchild so that it becomes
// an orphan, mirroring a runner left behind by a crashed host. The intermediate
// sh exits immediately after printing the grandchild's pid.
func startReparentedRunner(t *testing.T, runnerPath string) int {
	t.Helper()
	cmd := exec.Command("/bin/sh", "-c", `"$0" 300 >/dev/null 2>&1 & echo $!`, runnerPath)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch reparented runner: %v", err)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || pid <= 0 {
		t.Fatalf("captured pid %q is not a pid: %v", strings.TrimSpace(string(out)), convErr)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForReparent(t, pid)
	return pid
}

// copyBinary copies an ELF binary to dst with mode 0755. A hardlink is not usable
// here because t.TempDir() can live on a different filesystem than /bin.
func copyBinary(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatalf("open %s: %v", src, err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatalf("create %s: %v", dst, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		t.Fatalf("copy to %s: %v", dst, err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("close %s: %v", dst, err)
	}
}

// startReparentedWithArgv0 launches binary with an arbitrary argv[0], detaches it
// so it is reparented to init, and returns its pid.
//
// ⚠️ 必须用 bash 而不是 /bin/sh：Debian 的 dash 不支持 `exec -a`，用 sh 时命令会直接
//
//	失败，测试就变成「什么都没启动、也没杀任何人」的空断言（绿灯但没验证任何东西）。
func startReparentedWithArgv0(t *testing.T, binary, argv0 string, args ...string) int {
	t.Helper()
	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("bash is required for `exec -a` (dash does not support it)")
	}
	quoted := make([]string, 0, len(args)+2)
	quoted = append(quoted, shellQuote(binary), shellQuote(argv0))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	script := "exec -a " + quoted[1] + " " + quoted[0] + " " + strings.Join(quoted[2:], " ")
	cmd := exec.Command("/bin/bash", "-c", script+" >/dev/null 2>&1 & echo $!")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch with forged argv0: %v", err)
	}
	pid, convErr := strconv.Atoi(strings.TrimSpace(string(out)))
	if convErr != nil || pid <= 0 {
		t.Fatalf("captured pid %q is not a pid: %v", strings.TrimSpace(string(out)), convErr)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	waitForReparent(t, pid)
	// 断言它真的活着：否则「没被误杀」可能只是「没启动」。
	if processGoneOrZombie(pid) {
		t.Fatalf("process %d exited before the sweep; the assertion would be vacuous", pid)
	}
	return pid
}

// shellQuote wraps a string in single quotes for /bin/sh.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func TestReapOrphanRunners_KillsReparentedRunner(t *testing.T) {
	root, entry := installFakePackage(t, "reparented_pkg")
	pid := startReparentedRunner(t, entry)

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}

	if len(report.Killed) != 1 || report.Killed[0] != pid {
		t.Fatalf("killed pids = %v, want exactly [%d]", report.Killed, pid)
	}
	// A reparented orphan is reaped by init/systemd, which can lag behind the
	// signal. What matters is that it no longer runs: gone, or an unreaped zombie
	// that has already released every fd and mapping.
	if !processGoneOrZombie(pid) {
		t.Fatalf("orphan runner %d is still running after the sweep", pid)
	}
}

func TestReapOrphanRunners_NeverKillsSupervisedRunner(t *testing.T) {
	// A runner whose parent is a live process must be left alone. This is the
	// shape of a second running Zhulong instance, and killing it would be a
	// severe regression.
	root, entry := installFakePackage(t, "supervised_pkg")

	// Parent the package process under a separate long-lived process (/bin/sleep)
	// rather than the test binary, which is the harder case: the supervisor is
	// neither this process nor init, so any "is it us?" shortcut would
	// misclassify it.
	supervisor := exec.Command("/bin/sh", "-c", `"$0" 300 & wait`, entry)
	if err := supervisor.Start(); err != nil {
		t.Fatalf("start supervisor: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-supervisor.Process.Pid, syscall.SIGKILL)
		_ = supervisor.Process.Kill()
		_ = supervisor.Wait()
	})

	runnerPID := waitForChildOf(t, supervisor.Process.Pid, entry)
	t.Cleanup(func() { _ = syscall.Kill(runnerPID, syscall.SIGKILL) })

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if len(report.Killed) != 0 {
		t.Fatalf("sweep killed a supervised runner: %v", report.Killed)
	}
	if len(report.Skipped) != 1 || report.Skipped[0] != runnerPID {
		t.Fatalf("skipped pids = %v, want [%d]", report.Skipped, runnerPID)
	}
	if err := syscall.Kill(runnerPID, 0); err != nil {
		t.Fatalf("supervised runner %d was killed: %v", runnerPID, err)
	}
}

// waitForChildOf polls /proc for a process whose cmdline basename matches
// runnerBase and whose ppid is parentPID.
func waitForChildOf(t *testing.T, parentPID int, runnerPath string) int {
	t.Helper()
	want := filepath.Base(runnerPath)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir("/proc")
		if err == nil {
			for _, e := range entries {
				pid, convErr := strconv.Atoi(e.Name())
				if convErr != nil || pid <= 0 {
					continue
				}
				raw, readErr := os.ReadFile("/proc/" + e.Name() + "/cmdline")
				if readErr != nil || len(raw) == 0 {
					continue
				}
				argv0 := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")[0]
				if filepath.Base(argv0) != want {
					continue
				}
				if ppid := parentOf(t, pid); ppid == parentPID {
					return pid
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no child of %d running %s appeared", parentPID, want)
	return 0
}

func parentOf(t *testing.T, pid int) int {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return -1
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return -1
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	if len(fields) < 2 {
		return -1
	}
	ppid, convErr := strconv.Atoi(fields[1])
	if convErr != nil {
		return -1
	}
	return ppid
}

func TestReapOrphanRunners_IgnoresBinariesOutsideInstallRoot(t *testing.T) {
	root, _ := installFakePackage(t, "some_pkg")

	// A package-shaped path that lives outside the install root must not be a
	// candidate: the sweep's blast radius is the host's own algorithms directory.
	otherDir := t.TempDir()
	impostor := filepath.Join(otherDir, "some_pkg", "1.0.0", "some_pkg")
	if err := os.MkdirAll(filepath.Dir(impostor), 0o755); err != nil {
		t.Fatalf("create impostor dir: %v", err)
	}
	copyBinary(t, "/bin/sleep", impostor)
	cmd := exec.Command(impostor, "300")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start impostor: %v", err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if len(report.Killed) != 0 {
		t.Fatalf("sweep touched an unrelated process: %v", report.Killed)
	}
}

func TestReapOrphanRunners_IgnoresForgedArgv0(t *testing.T) {
	root, entry := installFakePackage(t, "argv0_pkg")
	_ = entry

	// argv[0] is whatever the parent passed: `exec -a` forges it in one line. The
	// sweep must identify a package process by its executable, otherwise anyone
	// running a binary with a package-shaped argv[0] gets killed.
	forged := filepath.Join(root, "argv0_pkg", "1.0.0", "argv0_pkg")
	pid := startReparentedWithArgv0(t, "/bin/sleep", forged, "300")

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if len(report.Killed) != 0 {
		t.Fatalf("sweep killed a process on a forged argv[0]: %v", report.Killed)
	}
	// 「没被误杀」必须配上「它还活着」，否则测试在进程没起来时也会绿。
	// 注意：它压根不该成为候选，所以断言的是「不在 Killed 里且仍在运行」，
	// 而不是「出现在 Skipped 里」——Skipped 表示「是我们的进程但有活着的监管者」。
	if processGoneOrZombie(pid) {
		t.Fatalf("forged-argv0 process %d is gone; the sweep may have killed it", pid)
	}
}

func TestReapOrphanRunners_KillsEntryEvenWithOddArgv0(t *testing.T) {
	root, entry := installFakePackage(t, "odd_argv0_pkg")

	// The mirror image: a real package entry started with an arbitrary argv[0].
	// Its executable is what the host started, so it must still be reaped.
	pid := startReparentedWithArgv0(t, entry, "not-a-package-name", "300")

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if len(report.Killed) != 1 || report.Killed[0] != pid {
		t.Fatalf("killed pids = %v, want exactly [%d]", report.Killed, pid)
	}
	if !processGoneOrZombie(pid) {
		t.Fatalf("orphan package process %d is still running after the sweep", pid)
	}
}

func TestReapOrphanRunners_CleanHostIsNoOp(t *testing.T) {
	root, _ := installFakePackage(t, "clean_pkg")

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if len(report.Killed) != 0 || len(report.Skipped) != 0 {
		t.Fatalf("clean sweep acted on processes: killed=%v skipped=%v", report.Killed, report.Skipped)
	}
}

// TestReapOrphanRunners_RejectsUnusableRoot pins the guard against a sweep that
// would otherwise scan /proc against a bogus root and match nothing (silently) or
// match everything (catastrophically).
func TestReapOrphanRunners_RejectsUnusableRoot(t *testing.T) {
	for _, root := range []string{"", "."} {
		if _, err := ReapOrphanRunners(context.Background(), root, zap.NewNop()); err == nil {
			t.Errorf("expected an error for algorithms dir %q", root)
		}
	}
}

// TestReapOrphanRunners_ReclaimsDeadSocketsAndShm covers the artefact half of the
// sweep, which has no live process to derive paths from and is judged purely by
// liveness.
func TestReapOrphanRunners_ReclaimsDeadSocketsAndShm(t *testing.T) {
	root, _ := installFakePackage(t, "sockets_pkg")

	// Broker dir override mirrors pipeline/engine.cpp's ZHULONG_FRAME_BROKER_SOCK.
	brokerDir := t.TempDir()
	t.Setenv("ZHULONG_FRAME_BROKER_SOCK", brokerDir)
	deadSock := filepath.Join(brokerDir, "zhulong_broker_4242.sock")
	if err := os.WriteFile(deadSock, nil, 0o755); err != nil {
		t.Fatalf("create dead socket file: %v", err)
	}

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("ReapOrphanRunners returned error: %v", err)
	}
	if _, statErr := os.Stat(deadSock); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dead broker socket was not reclaimed (stat err=%v), report=%+v", statErr, report)
	}
}

func TestRemoveUnheldShmSegments_SparesMappedSegment(t *testing.T) {
	// A segment that a live process still maps must survive: unlinking it would
	// make the running stream's broker look gone on the next attach.
	segment := filepath.Join(shmRoot, "zhulong_shm_test_"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, err := os.OpenFile(segment, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		t.Skipf("cannot create %s: %v", shmRoot, err)
	}
	if err := f.Truncate(4096); err != nil {
		f.Close()
		t.Fatalf("truncate segment: %v", err)
	}
	t.Cleanup(func() { f.Close(); _ = os.Remove(segment) })

	// This process now maps it, so the sweep must consider it held.
	if _, err := syscall.Mmap(int(f.Fd()), 0, 4096, syscall.PROT_READ, syscall.MAP_SHARED); err != nil {
		t.Fatalf("mmap segment: %v", err)
	}

	result := removeUnheldShmSegments(context.Background(), zap.NewNop())
	for _, r := range result.removed {
		if r == segment {
			t.Fatalf("sweep removed a still-mapped segment: %s (failures=%v)", segment, result.failures)
		}
	}
	if _, statErr := os.Stat(segment); statErr != nil {
		t.Fatalf("mapped segment disappeared: %v", statErr)
	}
}

// --- /proc fixtures ---
//
// Ownership decisions depend on /proc state that the test host cannot arrange
// (a process of another uid, a vanished pid, an unreadable maps file), so the
// inspection routines are driven against a synthetic process tree instead of
// whatever happens to be running on the machine.

// fakeProcRoot builds a /proc-shaped tree and points procRoot at it for the
// duration of the test.
func fakeProcRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	previous := procRoot
	procRoot = root
	t.Cleanup(func() { procRoot = previous })
	if err := os.MkdirAll(filepath.Join(root, "net"), 0o755); err != nil {
		t.Fatalf("create net dir: %v", err)
	}
	// An empty, readable socket table: no endpoints, so nothing is "bound".
	if err := os.WriteFile(filepath.Join(root, "net", "unix"), []byte("Num RefCount Protocol Flags Type St Inode Path\n"), 0o644); err != nil {
		t.Fatalf("write net/unix: %v", err)
	}
	return root
}

// fakeProcess creates /proc/<pid>/{maps,fd} under root.
func fakeProcess(t *testing.T, root string, pid int, mapsLines []string, fdTargets []string) string {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "fd"), 0o755); err != nil {
		t.Fatalf("create fake proc dir: %v", err)
	}
	body := ""
	for _, line := range mapsLines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "maps"), []byte(body), 0o644); err != nil {
		t.Fatalf("write fake maps: %v", err)
	}
	for i, target := range fdTargets {
		if err := os.Symlink(target, filepath.Join(dir, "fd", strconv.Itoa(3+i))); err != nil {
			t.Fatalf("create fake fd link: %v", err)
		}
	}
	return dir
}

// fakeMappingLine renders a maps line for id in the kernel's column layout:
// addr perms offset dev inode [pathname].
func fakeMappingLine(id fileID, path string) string {
	return fmt.Sprintf("7f0000000000-7f0000001000 r--s 00000000 %02x:%02x %d %s",
		id.devMajor, id.devMinor, id.ino, path)
}

// newShmSegment creates a zhulong shm object in /dev/shm (skipping the test when
// the directory is not writable) and returns its path and identity.
func newShmSegment(t *testing.T) (string, fileID) {
	t.Helper()
	segment := filepath.Join(shmRoot, "zhulong_shm_test_"+strconv.FormatInt(time.Now().UnixNano(), 36))
	f, err := os.OpenFile(segment, os.O_CREATE|os.O_RDWR, 0o666)
	if err != nil {
		t.Skipf("cannot create %s: %v", shmRoot, err)
	}
	if err := f.Truncate(4096); err != nil {
		f.Close()
		t.Fatalf("truncate segment: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close segment: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(segment) })
	id, err := statFileID(segment)
	if err != nil {
		t.Fatalf("stat segment: %v", err)
	}
	return segment, id
}

func TestShmHoldState_DetectsMappedSegment(t *testing.T) {
	segment, id := newShmSegment(t)
	root := fakeProcRoot(t)
	fakeProcess(t, root, 4242, []string{fakeMappingLine(id, segment)}, nil)

	state, err := shmHoldState(segment)
	if err != nil {
		t.Fatalf("shmHoldState: %v", err)
	}
	if state != holdLive {
		t.Fatalf("state = %v, want holdLive", state)
	}
}

// TestShmHoldState_DetectsUnmappedButOpenSegment covers the shm_open → mmap
// window: the creator holds a descriptor but has not mapped the segment yet. A
// maps-only check calls this segment abandoned and unlinks it under a live broker.
func TestShmHoldState_DetectsUnmappedButOpenSegment(t *testing.T) {
	segment, _ := newShmSegment(t)
	root := fakeProcRoot(t)
	fakeProcess(t, root, 4343, nil, []string{segment})

	state, err := shmHoldState(segment)
	if err != nil {
		t.Fatalf("shmHoldState: %v", err)
	}
	if state != holdLive {
		t.Fatalf("state = %v, want holdLive for an open-but-unmapped descriptor", state)
	}
}

// TestShmHoldState_UnreadableProcessIsUnknown is the fail-closed contract: the
// sweep may not conclude "free" from state it could not read.
func TestShmHoldState_UnreadableProcessIsUnknown(t *testing.T) {
	segment, _ := newShmSegment(t)
	root := fakeProcRoot(t)
	dir := filepath.Join(root, "5151")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fake proc dir: %v", err)
	}
	// A directory where maps should be: open(2) fails with EISDIR, which is neither
	// "vanished" nor readable.
	if err := os.Mkdir(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatalf("create maps directory: %v", err)
	}

	state, err := shmHoldState(segment)
	if err != nil {
		t.Fatalf("shmHoldState: %v", err)
	}
	if state != holdUnknown {
		t.Fatalf("state = %v, want holdUnknown for uninspectable process state", state)
	}
}

func TestShmHoldState_FreeSegmentIsReclaimable(t *testing.T) {
	segment, _ := newShmSegment(t)
	root := fakeProcRoot(t)
	// A live process that holds nothing of ours, plus one that vanished between
	// readdir and the read (no maps/fd files at all).
	fakeProcess(t, root, 6262, nil, []string{"/dev/null", "/tmp/unrelated.sock"})
	if err := os.MkdirAll(filepath.Join(root, "7272"), 0o755); err != nil {
		t.Fatalf("create vanished pid dir: %v", err)
	}

	state, err := shmHoldState(segment)
	if err != nil {
		t.Fatalf("shmHoldState: %v", err)
	}
	if state != holdNone {
		t.Fatalf("state = %v, want holdNone", state)
	}

	result := removeUnheldShmSegments(context.Background(), zap.NewNop())
	found := false
	for _, removed := range result.removed {
		if removed == segment {
			found = true
		}
	}
	if !found {
		t.Fatalf("unheld segment was not reclaimed: removed=%v unverified=%v failures=%v",
			result.removed, result.unverified, result.failures)
	}
}

// TestRemoveUnheldShmSegments_ReportsUnknownInsteadOfDeleting is the end-to-end
// version of the fail-closed rule: an uninspectable process must leave the
// segment on disk AND be reported, so operators can see why nothing was swept.
func TestRemoveUnheldShmSegments_ReportsUnknownInsteadOfDeleting(t *testing.T) {
	segment, _ := newShmSegment(t)
	root := fakeProcRoot(t)
	dir := filepath.Join(root, "8383")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fake proc dir: %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "maps"), 0o755); err != nil {
		t.Fatalf("create maps directory: %v", err)
	}

	result := removeUnheldShmSegments(context.Background(), zap.NewNop())
	for _, removed := range result.removed {
		if removed == segment {
			t.Fatal("segment was deleted although ownership could not be verified")
		}
	}
	if _, statErr := os.Stat(segment); statErr != nil {
		t.Fatalf("unverified segment disappeared: %v", statErr)
	}
	unverified := false
	for _, path := range result.unverified {
		if path == segment {
			unverified = true
		}
	}
	if !unverified {
		t.Fatalf("unverified segment was not reported: %+v", result)
	}
}

// TestRemoveStaleBrokerSocket_KeepsBoundButNotListeningSocket covers the
// bind→listen window with the REAL /proc/net/unix table: the socket file exists
// and connect() answers ECONNREFUSED exactly like an abandoned file, yet the
// kernel already knows the path has an endpoint. A connect-only check deletes a
// live broker's socket here.
func TestRemoveStaleBrokerSocket_KeepsBoundButNotListeningSocket(t *testing.T) {
	if _, err := os.Stat(filepath.Join(procRoot, "net", "unix")); err != nil {
		t.Skipf("no %s/net/unix: %v", procRoot, err)
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "zhulong_broker_77.sock")

	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socket: %v", err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: sock}); err != nil {
		syscall.Close(fd)
		t.Fatalf("bind: %v", err)
	}
	// Deliberately no listen(2): this is the pre-listen window.

	// Precondition: the probe alone cannot tell this apart from a dead socket.
	if serving, probeErr := brokerSocketServing(sock); probeErr != nil || serving {
		syscall.Close(fd)
		t.Fatalf("probe = (%v, %v), want a refused connection before listen", serving, probeErr)
	}

	outcome := removeStaleBrokerSocket(sock)
	if outcome.removed {
		syscall.Close(fd)
		t.Fatal("sweeper removed a socket that was bound but not yet listening")
	}
	if _, statErr := os.Stat(sock); statErr != nil {
		syscall.Close(fd)
		t.Fatalf("bound socket disappeared: %v", statErr)
	}

	// Reverse check: once the endpoint is gone the same path MUST be reclaimed,
	// otherwise the test above would pass for an implementation that never sweeps.
	if err := syscall.Close(fd); err != nil {
		t.Fatalf("close bound socket: %v", err)
	}
	outcome = removeStaleBrokerSocket(sock)
	if !outcome.removed {
		t.Fatalf("outcome = %+v, want removal after the endpoint disappeared", outcome)
	}
}

func TestRemoveStaleBrokerSocket_UnreadableTableIsUnverified(t *testing.T) {
	root := fakeProcRoot(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "zhulong_broker_78.sock")
	if err := os.WriteFile(sock, nil, 0o755); err != nil {
		t.Fatalf("create socket file: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "net", "unix")); err != nil {
		t.Fatalf("remove net/unix: %v", err)
	}

	outcome := removeStaleBrokerSocket(sock)
	if outcome.removed || !outcome.unverified || outcome.err != nil {
		t.Fatalf("outcome = %+v, want unverified without removal", outcome)
	}
	if _, statErr := os.Stat(sock); statErr != nil {
		t.Fatalf("socket was removed despite an unreadable endpoint table: %v", statErr)
	}
}

func TestRemoveStaleBrokerSocket_ReclaimsDeadSocket(t *testing.T) {
	fakeProcRoot(t)
	dir := t.TempDir()
	sock := filepath.Join(dir, "zhulong_broker_79.sock")
	if err := os.WriteFile(sock, nil, 0o755); err != nil {
		t.Fatalf("create socket file: %v", err)
	}

	outcome := removeStaleBrokerSocket(sock)
	if !outcome.removed {
		t.Fatalf("outcome = %+v, want removal of an unbound socket file", outcome)
	}
	if _, statErr := os.Stat(sock); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dead socket survived: %v", statErr)
	}
}

// TestRemoveIfUnchanged_SparesReplacedPath pins the identity guard: a broker that
// binds a fresh socket at the same path between the probe and the unlink must not
// lose it. Removing by name would leave a live broker unreachable.
func TestRemoveIfUnchanged_SparesReplacedPath(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "zhulong_broker_80.sock")
	if err := os.WriteFile(sock, nil, 0o755); err != nil {
		t.Fatalf("create socket file: %v", err)
	}
	inspected, err := statFileID(sock)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}

	// The replacement: same path, different object.
	if err := os.Remove(sock); err != nil {
		t.Fatalf("remove socket: %v", err)
	}
	if err := os.WriteFile(sock, nil, 0o755); err != nil {
		t.Fatalf("recreate socket: %v", err)
	}
	replacement, err := statFileID(sock)
	if err != nil {
		t.Fatalf("stat recreated socket: %v", err)
	}
	if inspected == replacement {
		t.Skip("filesystem reused the inode; the replacement cannot be observed")
	}

	removed, err := removeIfUnchanged(sock, inspected)
	if err != nil {
		t.Fatalf("removeIfUnchanged: %v", err)
	}
	if removed {
		t.Fatal("removeIfUnchanged deleted a file that replaced the inspected one")
	}
	if _, statErr := os.Stat(sock); statErr != nil {
		t.Fatalf("replacement socket disappeared: %v", statErr)
	}

	// And it still removes the inspected object when nothing replaced it.
	if removed, err = removeIfUnchanged(sock, replacement); err != nil || !removed {
		t.Fatalf("removeIfUnchanged(same object) = (%v, %v), want (true, nil)", removed, err)
	}
}

// --- directory lock ---

func TestBrokerResourceLock_BlocksConcurrentSweepWithoutFailingStartup(t *testing.T) {
	brokerDir := t.TempDir()
	t.Setenv("ZHULONG_FRAME_BROKER_SOCK", brokerDir)

	locked, err := acquireBrokerResourceLock(brokerResourceDirs(), lockNonBlocking)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer locked.release()

	// A second (sweep-side) acquire must fail fast instead of blocking startup.
	second, err := acquireBrokerResourceLock(brokerResourceDirs(), lockNonBlocking)
	if err == nil {
		second.release()
		t.Fatal("second non-blocking acquire succeeded while the lock was held")
	}
	if !isResourceLockBusy(err) {
		t.Fatalf("error = %v, want a busy-lock error", err)
	}
	if second != nil {
		t.Fatal("a failed acquire must not return a lock")
	}
}

func TestReapOrphanRunners_SkipsCleanupWhenLockIsBusy(t *testing.T) {
	root, _ := installFakePackage(t, "lock_busy_pkg")
	brokerDir := t.TempDir()
	t.Setenv("ZHULONG_FRAME_BROKER_SOCK", brokerDir)
	deadSock := filepath.Join(brokerDir, "zhulong_broker_4243.sock")
	if err := os.WriteFile(deadSock, nil, 0o755); err != nil {
		t.Fatalf("create dead socket file: %v", err)
	}

	// Health check: with the lock free the sweep reclaims the file.
	if _, err := ReapOrphanRunners(context.Background(), root, zap.NewNop()); err != nil {
		t.Fatalf("free-lock sweep: %v", err)
	}
	if _, statErr := os.Stat(deadSock); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dead socket was not reclaimed with a free lock (stat err=%v)", statErr)
	}

	// Now a broker creator holds the lock: the sweep must not unlink anything, and
	// startup must continue (no error returned).
	if err := os.WriteFile(deadSock, nil, 0o755); err != nil {
		t.Fatalf("recreate dead socket file: %v", err)
	}
	held, err := acquireBrokerResourceLock(brokerResourceDirs(), lockNonBlocking)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer held.release()

	report, err := ReapOrphanRunners(context.Background(), root, zap.NewNop())
	if err != nil {
		t.Fatalf("busy-lock sweep returned an error: %v", err)
	}
	if len(report.RemovedSockets) != 0 || len(report.RemovedShm) != 0 {
		t.Fatalf("busy-lock sweep still removed artefacts: %+v", report)
	}
	if _, statErr := os.Stat(deadSock); statErr != nil {
		t.Fatalf("socket was removed while a broker creator held the lock: %v", statErr)
	}
}

func TestBrokerResourceLock_DedupesAliasedDirectories(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(dir, "alias")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	lock, err := acquireBrokerResourceLock([]string{dir, alias, dir}, lockNonBlocking)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.release()
	if len(lock.files) != 1 {
		t.Fatalf("aliased directories were not deduped: %d locks held", len(lock.files))
	}
}

// --- helpers ---

// processGoneOrZombie reports whether pid no longer owns any resources: either it
// vanished, or it is an unreaped zombie.
func processGoneOrZombie(pid int) bool {
	if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
		return true
	}
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	closeParen := strings.LastIndexByte(string(raw), ')')
	if closeParen < 0 || closeParen+2 >= len(raw) {
		return false
	}
	fields := strings.Fields(string(raw[closeParen+2:]))
	return len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X")
}

// waitForReparent polls until the process no longer belongs to this test binary,
// which is the observable signature of an orphan. It cannot assume ppid == 1:
// on a systemd user session the reaper is systemd --user (observed as pid 2020
// during development), so the check is "parent is not us".
func waitForReparent(t *testing.T, pid int) {
	t.Helper()
	self := os.Getpid()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if err == nil {
			if closeParen := strings.LastIndexByte(string(raw), ')'); closeParen >= 0 {
				fields := strings.Fields(string(raw[closeParen+2:]))
				if len(fields) >= 2 {
					ppid, convErr := strconv.Atoi(fields[1])
					if convErr == nil && ppid != self && ppid > 0 {
						return
					}
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("runner %d was never reparented away from the test process", pid)
}
