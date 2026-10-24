//go:build linux

package storage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// GetRootDeviceID returns the st_dev of root directory "/".
func (p *DefaultPathInspector) GetRootDeviceID() (uint64, error) {
	p.rootDevMu.RLock()
	if p.hasRootDev {
		id := p.rootDevID
		p.rootDevMu.RUnlock()
		return id, nil
	}
	p.rootDevMu.RUnlock()

	p.rootDevMu.Lock()
	defer p.rootDevMu.Unlock()
	if p.hasRootDev {
		return p.rootDevID, nil
	}

	var stat unix.Stat_t
	if err := unix.Stat("/", &stat); err != nil {
		return 0, fmt.Errorf("stat rootfs /: %w", err)
	}
	p.rootDevID = stat.Dev
	p.hasRootDev = true
	return p.rootDevID, nil
}

// InspectFS queries kernel VFS telemetry for the given directory path.
func (p *DefaultPathInspector) InspectFS(path string) (*FSStats, error) {
	cleanPath := filepath.Clean(path)
	if err := os.MkdirAll(cleanPath, 0o750); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("ensure directory %s: %w", cleanPath, err)
	}

	var stat unix.Stat_t
	if err := unix.Stat(cleanPath, &stat); err != nil {
		return nil, fmt.Errorf("stat path %s: %w", cleanPath, err)
	}

	var statfs unix.Statfs_t
	if err := unix.Statfs(cleanPath, &statfs); err != nil {
		return nil, fmt.Errorf("statfs path %s: %w", cleanPath, err)
	}

	rootDev, err := p.GetRootDeviceID()
	if err != nil {
		return nil, err
	}

	bsize := uint64(statfs.Bsize)
	totalBytes := statfs.Blocks * bsize
	freeBytes := statfs.Bavail * bsize

	var usedBytes uint64
	switch {
	case statfs.Blocks >= statfs.Bfree:
		usedBytes = (statfs.Blocks - statfs.Bfree) * bsize
	case totalBytes >= freeBytes:
		usedBytes = totalBytes - freeBytes
	}

	mountPoint, fsName := findMountInfoLinux(cleanPath)
	if fsName == "" {
		fsName = fsTypeFromMagic(uint64(statfs.Type))
	}
	if mountPoint == "" {
		mountPoint = "/"
	}

	return &FSStats{
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
		DeviceID:   stat.Dev,
		MountPoint: mountPoint,
		FSType:     fsName,
		IsReadOnly: statfs.Flags&unix.MS_RDONLY != 0,
		IsExternal: stat.Dev != rootDev,
	}, nil
}

// fsTypeFromMagic maps a statfs magic number to a filesystem name. Magic
// numbers without a constant in x/sys/unix stay literal.
func fsTypeFromMagic(magic uint64) string {
	switch magic {
	case unix.EXT4_SUPER_MAGIC:
		return "ext4"
	case unix.XFS_SUPER_MAGIC:
		return "xfs"
	case unix.BTRFS_SUPER_MAGIC:
		return "btrfs"
	case unix.MSDOS_SUPER_MAGIC, 0x4006: // 0x4006 is the FAT12/16 alias, not exported by x/sys.
		return "vfat"
	case unix.NFS_SUPER_MAGIC:
		return "nfs"
	case unix.TMPFS_MAGIC:
		return "tmpfs"
	case unix.OVERLAYFS_SUPER_MAGIC:
		return "overlay"
	default:
		return fmt.Sprintf("0x%x", magic)
	}
}

// findMountInfoLinux returns the longest mount point that contains targetPath
// along with its filesystem type.
//
// Unparseable entries are skipped rather than aborting the scan, so the answer
// degrades to the entries read before /proc/mounts changed under us.
func findMountInfoLinux(targetPath string) (string, string) {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return "", ""
	}
	defer file.Close()

	bestMount := ""
	bestFSType := ""

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		mountPoint := unescapeMountField(fields[1])
		if !mountPointCovers(targetPath, mountPoint) || len(mountPoint) <= len(bestMount) {
			continue
		}

		bestMount = mountPoint
		bestFSType = fields[2]
	}

	return bestMount, bestFSType
}

// mountPointCovers reports whether targetPath lives at or below mountPoint.
func mountPointCovers(targetPath, mountPoint string) bool {
	if targetPath == mountPoint {
		return true
	}
	if mountPoint == "/" {
		return strings.HasPrefix(targetPath, "/")
	}

	return strings.HasPrefix(targetPath, mountPoint+"/")
}

// unescapeMountField decodes the octal escapes (\040 for a space, \011 for a
// tab) that keep one /proc/mounts entry per line. Without decoding, a mount
// point containing a space never matches and silently degrades to "/".
func unescapeMountField(field string) string {
	if !strings.Contains(field, `\`) {
		return field
	}

	var decoded strings.Builder
	decoded.Grow(len(field))

	for i := 0; i < len(field); i++ {
		if field[i] != '\\' || i+3 >= len(field) {
			decoded.WriteByte(field[i])
			continue
		}

		octal, err := strconv.ParseUint(field[i+1:i+4], 8, 8)
		if err != nil {
			decoded.WriteByte(field[i])
			continue
		}

		decoded.WriteByte(byte(octal))
		i += 3
	}

	return decoded.String()
}
