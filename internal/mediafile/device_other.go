//go:build !unix

package mediafile

import "io/fs"

// deviceIDFromFileInfo reports that no device identity is available on this
// platform. Fallthrough detection is a Linux edge-device concern; on other
// hosts the resolver simply skips the device comparison.
func deviceIDFromFileInfo(info fs.FileInfo) (uint64, bool) {
	return 0, false
}
