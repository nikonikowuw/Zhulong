//go:build linux

package systemtime

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	rtcSysfsRoot  = "/sys/class/rtc"
	localTimePath = "/etc/localtime"
	localZonePath = "/etc/timezone"
	zoneInfoDir   = "/usr/share/zoneinfo"
)

// rtcCandidates lists the device nodes probed for a hardware RTC, in order.
var rtcCandidates = []string{"/dev/rtc0", "/dev/rtc", "/dev/rtc1"}

// timezoneMu serialises timezone application. time.Local cannot be swapped
// atomically (the standard library documents it as not safe for concurrent use),
// so at least two concurrent applies must not interleave their writes.
var timezoneMu sync.Mutex

type linuxClockDriver struct{}

func newPlatformClockDriver() ClockDriver {
	return &linuxClockDriver{}
}

func (d *linuxClockDriver) GetSystemTime() (time.Time, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_REALTIME, &ts); err != nil {
		return time.Time{}, fmt.Errorf("clock_gettime(CLOCK_REALTIME): %w", err)
	}
	return time.Unix(ts.Sec, ts.Nsec), nil
}

func (d *linuxClockDriver) SetRealtime(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())
	if err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts); err != nil {
		return fmt.Errorf("clock_settime(CLOCK_REALTIME): %w", err)
	}
	return nil
}

func (d *linuxClockDriver) AdjTime(offset time.Duration) error {
	var tx unix.Timex
	tx.Modes = unix.ADJ_OFFSET_SINGLESHOT
	tx.Offset = int64(offset.Microseconds())
	if _, err := unix.Adjtimex(&tx); err != nil {
		return fmt.Errorf("adjtimex(ADJ_OFFSET_SINGLESHOT): %w", err)
	}
	return nil
}

func findRTCDevice() string {
	for _, dev := range rtcCandidates {
		if _, err := os.Stat(dev); err == nil {
			return dev
		}
	}
	return ""
}

// rtcSysfsName maps a device node to its /sys/class/rtc entry name.
func rtcSysfsName(dev string) string {
	if resolved, err := filepath.EvalSymlinks(dev); err == nil {
		return filepath.Base(resolved)
	}
	return filepath.Base(dev)
}

// readRTCFromSysfs reads the RTC through sysfs, used when the device node exists
// but is not accessible to this process.
func readRTCFromSysfs(dev string) (time.Time, error) {
	dir := filepath.Join(rtcSysfsRoot, rtcSysfsName(dev))

	// since_epoch is a single value; the date/time pair can straddle a second
	// boundary and yield an RTC timestamp that is off by one second (or a day).
	if content, err := os.ReadFile(filepath.Join(dir, "since_epoch")); err == nil {
		seconds, parseErr := strconv.ParseInt(strings.TrimSpace(string(content)), 10, 64)
		if parseErr == nil {
			return time.Unix(seconds, 0).UTC(), nil
		}
	}

	dateBytes, err := os.ReadFile(filepath.Join(dir, "date"))
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s/date: %w", dir, err)
	}
	timeBytes, err := os.ReadFile(filepath.Join(dir, "time"))
	if err != nil {
		return time.Time{}, fmt.Errorf("read %s/time: %w", dir, err)
	}

	combined := strings.TrimSpace(string(dateBytes)) + " " + strings.TrimSpace(string(timeBytes))
	rtcTime, err := time.ParseInLocation("2006-01-02 15:04:05", combined, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse sysfs rtc %q: %w", combined, err)
	}
	return rtcTime, nil
}

func (d *linuxClockDriver) ReadRTC() (time.Time, error) {
	dev := findRTCDevice()
	if dev == "" {
		return time.Time{}, errors.New("rtc device not found")
	}
	return readRTCOnce(dev)
}

// readRTCOnce reads the RTC through the device node, falling back to sysfs when
// the node exists but is not accessible to this process.
func readRTCOnce(dev string) (time.Time, error) {
	fd, err := unix.Open(dev, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			rtcTime, sysfsErr := readRTCFromSysfs(dev)
			if sysfsErr == nil {
				return rtcTime, nil
			}
		}
		return time.Time{}, fmt.Errorf("open rtc %s: %w", dev, err)
	}
	defer unix.Close(fd)

	rt, err := unix.IoctlGetRTCTime(fd)
	if err != nil {
		return time.Time{}, fmt.Errorf("ioctl RTC_RD_TIME on %s: %w", dev, err)
	}

	return time.Date(
		int(rt.Year)+1900,
		time.Month(rt.Mon+1),
		int(rt.Mday),
		int(rt.Hour),
		int(rt.Min),
		int(rt.Sec),
		0,
		time.UTC,
	), nil
}

func (d *linuxClockDriver) WriteRTC(t time.Time) error {
	dev := findRTCDevice()
	if dev == "" {
		return errors.New("rtc device not found")
	}

	fd, err := unix.Open(dev, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open rtc %s for write: %w", dev, err)
	}
	defer unix.Close(fd)

	utc := t.UTC()
	rt := unix.RTCTime{
		Sec:   int32(utc.Second()),
		Min:   int32(utc.Minute()),
		Hour:  int32(utc.Hour()),
		Mday:  int32(utc.Day()),
		Mon:   int32(utc.Month() - 1),
		Year:  int32(utc.Year() - 1900),
		Isdst: 0,
	}

	if err := unix.IoctlSetRTCTime(fd, &rt); err != nil {
		return fmt.Errorf("ioctl RTC_SET_TIME on %s: %w", dev, err)
	}
	return nil
}

func (d *linuxClockDriver) GetRTCStatus() RTCStatus {
	dev := findRTCDevice()
	if dev == "" {
		return RTCStatusMissing
	}

	// An unreadable device node is still a healthy RTC as long as sysfs exposes
	// the time; only a failure of both paths is an error state. readRTCOnce
	// already handles the permission fallback, and the extra probe also covers a
	// node whose ioctl fails.
	if _, err := readRTCOnce(dev); err == nil {
		return RTCStatusNormal
	}
	if _, sysfsErr := readRTCFromSysfs(dev); sysfsErr == nil {
		return RTCStatusNormal
	}
	return RTCStatusError
}

// GetTimezone resolves the active system timezone from the files the platform
// actually configures, rather than from the process's own snapshot: the latter
// reads back as "Local" once ApplyTimezone has run and is not an IANA name.
func (d *linuxClockDriver) GetTimezone() (string, error) {
	if target, err := os.Readlink(localTimePath); err == nil {
		if idx := strings.Index(target, "zoneinfo/"); idx != -1 {
			return target[idx+len("zoneinfo/"):], nil
		}
	}

	if content, err := os.ReadFile(localZonePath); err == nil {
		if zone := strings.TrimSpace(string(content)); zone != "" {
			return zone, nil
		}
	}

	if zone := strings.TrimPrefix(os.Getenv("TZ"), ":"); zone != "" {
		return zone, nil
	}

	return "", errors.New("no system timezone source available")
}

// ApplyTimezone points /etc/localtime at the requested IANA zone and mirrors it
// into /etc/timezone.
//
// Every step is reported. A timezone that is applied half way but returns nil is
// worse than a loud failure: /etc/localtime then disagrees with the persisted
// configuration and nothing in the logs explains why.
func (d *linuxClockDriver) ApplyTimezone(iana string) error {
	loc, err := time.LoadLocation(iana)
	if err != nil {
		return fmt.Errorf("load timezone %q: %w", iana, err)
	}

	zonePath := filepath.Join(zoneInfoDir, iana)
	if _, err := os.Stat(zonePath); err != nil {
		return fmt.Errorf("zoneinfo entry for %q: %w", iana, err)
	}

	timezoneMu.Lock()
	defer timezoneMu.Unlock()

	// Build the new link beside the target and rename it into place, so a
	// concurrent reader never observes a missing /etc/localtime. The temp path is
	// fixed because timezoneMu serialises every apply in this process.
	tmpPath := localTimePath + ".tmp"
	if err := os.Remove(tmpPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale %s: %w", tmpPath, err)
	}
	if err := os.Symlink(zonePath, tmpPath); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", tmpPath, zonePath, err)
	}
	if err := os.Rename(tmpPath, localTimePath); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpPath, localTimePath, err)
	}

	if err := os.WriteFile(localZonePath, []byte(iana+"\n"), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", localZonePath, err)
	}
	if err := os.Setenv("TZ", ":"+localTimePath); err != nil {
		return fmt.Errorf("set TZ: %w", err)
	}

	// time.Local is the process's cached view; without this the API would report
	// the new zone name but format currentTime with the boot-time offset.
	//
	// Known limitation: the standard library does not make this assignment safe
	// against concurrent time.Now() readers, so it is kept as the last step and
	// serialised here. Readers still see either the old or the new pointer.
	time.Local = loc
	return nil
}

func (d *linuxClockDriver) HasClockPermission() bool {
	if os.Geteuid() == 0 {
		return true
	}

	caps, err := effectiveCapabilities()
	if err != nil {
		return false
	}
	return caps&(1<<unix.CAP_SYS_TIME) != 0
}

// effectiveCapabilities reads the effective capability bitmap of this process.
func effectiveCapabilities() (uint64, error) {
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return 0, fmt.Errorf("open /proc/self/status: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "CapEff:") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("malformed CapEff line %q", line)
		}
		caps, err := strconv.ParseUint(fields[1], 16, 64)
		if err != nil {
			return 0, fmt.Errorf("parse CapEff %q: %w", fields[1], err)
		}
		return caps, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan /proc/self/status: %w", err)
	}
	return 0, errors.New("CapEff not found in /proc/self/status")
}
