//go:build unix

package filestore

import (
	"io/fs"
	"syscall"
)

// deviceIDFromFileInfo extracts st_dev from a file's platform-specific stat
// structure. It inspects only the Dev field so nanosecond timestamp differences
// do not break directory identity comparisons.
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
