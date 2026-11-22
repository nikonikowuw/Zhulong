//go:build !unix

package filestore

import "io/fs"

func deviceIDFromFileInfo(info fs.FileInfo) (uint64, bool) {
	return 0, false
}
