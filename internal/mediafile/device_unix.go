//go:build unix

package mediafile

import (
	"io/fs"
	"syscall"
)

// deviceIDFromFileInfo extracts st_dev from a file's platform-specific stat
// structure.
//
// It reads only the Dev field. syscall.Stat_t is a struct containing nested
// arrays, so comparing two of them with == compiles but compares every field,
// including the nanosecond parts of the access and modification timestamps; two
// stats of the same directory are not equal that way.
func deviceIDFromFileInfo(info fs.FileInfo) (uint64, bool) {
	if info == nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat == nil {
		return 0, false
	}
	return uint64(stat.Dev), true
}
