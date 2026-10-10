//go:build linux

package storage

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
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
	if statfs.Blocks >= statfs.Bfree {
		usedBytes = (statfs.Blocks - statfs.Bfree) * bsize
	} else if totalBytes >= freeBytes {
		usedBytes = totalBytes - freeBytes
	}

	mountPoint, fsName := findMountInfoLinux(cleanPath)
	if fsName == "" {
		fsName = fsTypeFromMagic(uint64(statfs.Type))
	}
	if mountPoint == "" {
		mountPoint = "/"
	}

	isReadOnly := (statfs.Flags & unix.MS_RDONLY) != 0

	return &FSStats{
		TotalBytes: totalBytes,
		FreeBytes:  freeBytes,
		UsedBytes:  usedBytes,
		DeviceID:   stat.Dev,
		MountPoint: mountPoint,
		FSType:     fsName,
		IsReadOnly: isReadOnly,
		IsExternal: stat.Dev != rootDev,
	}, nil
}

func fsTypeFromMagic(magic uint64) string {
	switch magic {
	case 0xef53:
		return "ext4"
	case 0x58465342:
		return "xfs"
	case 0x9123683e:
		return "btrfs"
	case 0x4d44, 0x4006:
		return "vfat"
	case 0x6969:
		return "nfs"
	case 0x01021994:
		return "tmpfs"
	case 0x794c7630:
		return "overlay"
	default:
		return fmt.Sprintf("0x%x", magic)
	}
}

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
		mountPoint := fields[1]
		fsType := fields[2]

		if targetPath == mountPoint || strings.HasPrefix(targetPath, mountPoint+"/") || (mountPoint == "/" && strings.HasPrefix(targetPath, "/")) {
			if len(mountPoint) > len(bestMount) {
				bestMount = mountPoint
				bestFSType = fsType
			}
		}
	}

	return bestMount, bestFSType
}
