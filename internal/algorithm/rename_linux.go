//go:build linux

package algorithm

import "golang.org/x/sys/unix"

func renameDirectoryNoReplace(src, dst string) error {
	return unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
}
