//go:build linux

package algorunner

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
)

// Broker resource directories hold two kinds of artefact that are created in two
// steps, each with a window in which the file exists but is not yet usable:
//
//   - /dev/shm: shm_open publishes the name before the creator mmaps it, so a
//     path-and-maps check sees "mapped by nobody" for a live broker.
//   - the socket dir: bind() publishes the socket inode before listen(), so a
//     connect probe answers ECONNREFUSED for a live broker.
//
// Both windows are indistinguishable from a genuinely abandoned artefact, which
// is why the startup sweep and the broker's creation path share an advisory
// exclusive lock over the directory inodes themselves. native FrameBroker::start
// (native/src/algorithm/ipc/broker_resource_lock.{hpp,cpp}) holds it while it
// creates the shm segment and binds the socket; the sweep takes it before it
// unlinks anything.
//
// ⚠️ The lock is advisory and cooperative: it only works because both sides agree
// on the SAME lock objects. Both sides therefore resolve the directory set to
// unique (device, inode) pairs and sort them, so aliased paths (a base dir that
// *is* /dev/shm, a bind-mounted copy, a symlink) collapse to one lock and two
// processes can never take the same pair in opposite orders.

const (
	// brokerSocketPrefix and brokerSocketSuffix delimit the broker socket file
	// names inside the base directory (native frame_broker.cpp).
	brokerSocketPrefix = "zhulong_broker_"
	brokerSocketSuffix = ".sock"

	// shmSegmentPrefix delimits the POSIX shm segment names frame_broker.cpp
	// creates ("/zhulong_shm_<stream_id>").
	shmSegmentPrefix = "zhulong_shm_"

	// defaultBrokerSockBase is the broker socket base directory when the native
	// side has no override; it mirrors pipeline/engine.cpp.
	defaultBrokerSockBase = "/tmp"

	// brokerSockOverrideEnv lets the native side relocate the broker socket dir.
	brokerSockOverrideEnv = "ZHULONG_FRAME_BROKER_SOCK"
)

// fileID identifies a file by filesystem identity rather than by path. It is what
// /proc/<pid>/maps reports (major:minor plus inode), so it can be compared
// against a mapping without ever resolving the path.
type fileID struct {
	devMajor uint32
	devMinor uint32
	ino      uint64
}

// statFileID returns the identity of path.
func statFileID(path string) (fileID, error) {
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		return fileID{}, err
	}
	return fileIDFromStat(&st), nil
}

// fileIDFromStat converts a stat result to the kernel's (major, minor, inode)
// identity.
func fileIDFromStat(st *syscall.Stat_t) fileID {
	major, minor := linuxDeviceParts(uint64(st.Dev))
	return fileID{devMajor: major, devMinor: minor, ino: st.Ino}
}

// linuxDeviceParts splits a glibc dev_t into the major/minor pair the kernel
// prints in /proc maps (and in /proc/net/unix indirectly via the inode's fs).
//
// The kernel encodes dev_t as: minor bits 0-7 and 20-31, major bits 8-19 and
// 32-43. A naive uint64(dev)>>8 would silently mismatch on devices whose major
// exceeds 0xff and make every "is it still mapped" answer wrong.
func linuxDeviceParts(dev uint64) (major, minor uint32) {
	major = uint32((dev>>8)&0xfff) | uint32((dev>>32)&^uint64(0xfff))
	minor = uint32(dev&0xff) | uint32((dev>>12)&^uint64(0xff))
	return major, minor
}

// lockMode selects whether acquireBrokerResourceLock waits for the lock.
//
// It exists so call sites read as intent instead of as a bare bool literal: the
// native creator must wait, the sweep must not. It is an int rather than a bool
// so an accidental `false` argument does not compile.
type lockMode int

const (
	// lockBlocking waits until every directory lock is held.
	lockBlocking lockMode = iota
	// lockNonBlocking fails with EWOULDBLOCK instead of waiting.
	lockNonBlocking
)

// brokerSocketBaseDir is the directory holding broker socket files.
func brokerSocketBaseDir() string {
	if base := os.Getenv(brokerSockOverrideEnv); base != "" {
		return base
	}
	return defaultBrokerSockBase
}

// brokerResourceDirs lists the directories the sweep and the native broker must
// not mutate concurrently. The base dir mirrors pipeline/engine.cpp: the
// ZHULONG_FRAME_BROKER_SOCK override, else /tmp.
func brokerResourceDirs() []string {
	return []string{brokerSocketBaseDir(), shmRoot}
}

// brokerResourceLock is a held set of directory locks.
type brokerResourceLock struct {
	files []*os.File
}

// acquireBrokerResourceLock takes an exclusive flock on every directory in dirs.
//
// block semantics are deliberately different per caller:
//   - the native broker creator blocks: it must not skip its own setup.
//   - the sweep is non-blocking: a busy lock means someone is creating a broker
//     right now, and the only safe action is to leave everything alone.
func acquireBrokerResourceLock(dirs []string, mode lockMode) (*brokerResourceLock, error) {
	byID, err := uniqueDirsByID(dirs)
	if err != nil {
		return nil, err
	}

	// Deterministic order: the lock set is unordered (a map), and two processes
	// taking an unordered set can deadlock against each other.
	ids := make([]fileID, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, compareFileIDs)

	lock := &brokerResourceLock{}
	for _, id := range ids {
		if err := lock.acquireOne(byID[id], mode); err != nil {
			lock.release()
			return nil, err
		}
	}
	return lock, nil
}

// uniqueDirsByID resolves dirs to their filesystem identity, collapsing aliases.
func uniqueDirsByID(dirs []string) (map[fileID]string, error) {
	byID := make(map[fileID]string, len(dirs))
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		var st syscall.Stat_t
		if err := syscall.Stat(dir, &st); err != nil {
			return nil, fmt.Errorf("broker lock: %q: %w", dir, err)
		}
		if st.Mode&syscall.S_IFMT != syscall.S_IFDIR {
			return nil, fmt.Errorf("broker lock: %q is not a directory", dir)
		}
		id := fileIDFromStat(&st)
		if _, exists := byID[id]; !exists {
			byID[id] = dir
		}
	}
	return byID, nil
}

func compareFileIDs(a, b fileID) int {
	return cmp.Or(
		cmp.Compare(a.devMajor, b.devMajor),
		cmp.Compare(a.devMinor, b.devMinor),
		cmp.Compare(a.ino, b.ino),
	)
}

// acquireOne opens and flocks a single directory, appending it to the held set.
func (l *brokerResourceLock) acquireOne(path string, mode lockMode) error {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("broker lock: open %q: %w", path, err)
	}

	flags := syscall.LOCK_EX
	if mode == lockNonBlocking {
		flags |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(file.Fd()), flags); err != nil {
		closeFileBestEffort(file)
		return fmt.Errorf("broker lock: %q: %w", path, err)
	}

	l.files = append(l.files, file)
	return nil
}

// release drops every lock. Closing the descriptor releases the flock on its own;
// the explicit LOCK_UN only makes the intent visible in strace.
//
// Both errors are ignored on purpose: release runs on success paths and on error
// paths that are already returning a more specific failure, so there is nothing
// useful left to do with "unlock failed on a descriptor we are about to close".
func (l *brokerResourceLock) release() {
	if l == nil {
		return
	}
	for i := len(l.files) - 1; i >= 0; i-- {
		if err := syscall.Flock(int(l.files[i].Fd()), syscall.LOCK_UN); err != nil {
			// best effort: close() releases the advisory flock anyway
		}
		closeFileBestEffort(l.files[i])
	}
	l.files = nil
}

// isResourceLockBusy reports whether a non-blocking acquire lost the race with a
// concurrent creator. Lock contention is not a failure condition for the sweep:
// it means a broker is being built right now, so every artefact must be left
// alone.
func isResourceLockBusy(err error) bool {
	return errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN)
}
