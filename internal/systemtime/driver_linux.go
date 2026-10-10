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
	"time"

	"golang.org/x/sys/unix"
)

type linuxClockDriver struct{}

func newLinuxClockDriver() ClockDriver {
	return &linuxClockDriver{}
}

func newPlatformClockDriver() ClockDriver {
	return newLinuxClockDriver()
}

func (d *linuxClockDriver) GetSystemTime() (time.Time, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_REALTIME, &ts); err != nil {
		return time.Now(), nil
	}
	return time.Unix(ts.Sec, ts.Nsec), nil
}

func (d *linuxClockDriver) SetRealtime(t time.Time) error {
	ts := unix.NsecToTimespec(t.UnixNano())
	return unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
}

func (d *linuxClockDriver) AdjTime(offset time.Duration) error {
	var tx unix.Timex
	tx.Modes = unix.ADJ_OFFSET_SINGLESHOT
	tx.Offset = int64(offset.Microseconds())
	_, err := unix.Adjtimex(&tx)
	return err
}

func findRtcDevice() string {
	candidates := []string{"/dev/rtc0", "/dev/rtc", "/dev/rtc1"}
	for _, dev := range candidates {
		if _, err := os.Stat(dev); err == nil {
			return dev
		}
	}
	return ""
}

func readRTCFromSysfs(dev string) (time.Time, error) {
	realDev, err := filepath.EvalSymlinks(dev)
	if err != nil {
		realDev = dev
	}
	base := filepath.Base(realDev)
	dateBytes, err := os.ReadFile(filepath.Join("/sys/class/rtc", base, "date"))
	if err != nil {
		return time.Time{}, err
	}
	timeBytes, err := os.ReadFile(filepath.Join("/sys/class/rtc", base, "time"))
	if err != nil {
		return time.Time{}, err
	}
	combined := strings.TrimSpace(string(dateBytes)) + " " + strings.TrimSpace(string(timeBytes))
	return time.ParseInLocation("2006-01-02 15:04:05", combined, time.UTC)
}

func (d *linuxClockDriver) ReadRTC() (time.Time, error) {
	dev := findRtcDevice()
	if dev == "" {
		return time.Time{}, errors.New("rtc device not found")
	}

	fd, err := unix.Open(dev, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			if t, sysfsErr := readRTCFromSysfs(dev); sysfsErr == nil {
				return t, nil
			}
		}
		return time.Time{}, fmt.Errorf("open rtc %s: %w", dev, err)
	}
	defer unix.Close(fd)

	rt, err := unix.IoctlGetRTCTime(fd)
	if err != nil {
		return time.Time{}, fmt.Errorf("ioctl RTC_RD_TIME: %w", err)
	}

	year := int(rt.Year) + 1900
	month := time.Month(rt.Mon + 1)
	return time.Date(year, month, int(rt.Mday), int(rt.Hour), int(rt.Min), int(rt.Sec), 0, time.UTC), nil
}

func (d *linuxClockDriver) WriteRTC(t time.Time) error {
	dev := findRtcDevice()
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
		return fmt.Errorf("ioctl RTC_SET_TIME: %w", err)
	}
	return nil
}

func (d *linuxClockDriver) GetRTCStatus() RtcStatus {
	dev := findRtcDevice()
	if dev == "" {
		return RtcStatusMissing
	}

	fd, err := unix.Open(dev, unix.O_RDONLY|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			if _, sysfsErr := readRTCFromSysfs(dev); sysfsErr == nil {
				return RtcStatusNormal
			}
		}
		return RtcStatusError
	}
	defer unix.Close(fd)

	_, err = unix.IoctlGetRTCTime(fd)
	if err != nil {
		// If ioctl fails but sysfs still reads fine, don't immediately panic with error
		if _, sysfsErr := readRTCFromSysfs(dev); sysfsErr == nil {
			return RtcStatusNormal
		}
		return RtcStatusError
	}
	return RtcStatusNormal
}

func (d *linuxClockDriver) GetTimezone() (string, error) {
	// 1. Try reading /etc/localtime symlink target
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if idx := strings.Index(target, "zoneinfo/"); idx != -1 {
			return target[idx+len("zoneinfo/"):], nil
		}
	}

	// 2. Try reading /etc/timezone
	if content, err := os.ReadFile("/etc/timezone"); err == nil {
		zone := strings.TrimSpace(string(content))
		if zone != "" {
			return zone, nil
		}
	}

	// 3. Fallback to Go location
	return time.Now().Location().String(), nil
}

func (d *linuxClockDriver) ApplyTimezone(iana string) error {
	loc, err := time.LoadLocation(iana)
	if err != nil {
		return fmt.Errorf("invalid timezone %q: %w", iana, err)
	}

	zonePath := filepath.Join("/usr/share/zoneinfo", iana)
	if _, err := os.Stat(zonePath); err == nil {
		tmpLink := "/etc/localtime.tmp"
		_ = os.Remove(tmpLink)
		if err := os.Symlink(zonePath, tmpLink); err == nil {
			_ = os.Rename(tmpLink, "/etc/localtime")
		}
		_ = os.WriteFile("/etc/timezone", []byte(iana+"\n"), 0644)
	}

	_ = os.Setenv("TZ", ":/etc/localtime")
	time.Local = loc
	return nil
}

func (d *linuxClockDriver) HasClockPermission() bool {
	if os.Geteuid() == 0 {
		return true
	}

	// Check CAP_SYS_TIME (bit 25) in /proc/self/status CapEff
	file, err := os.Open("/proc/self/status")
	if err != nil {
		return false
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "CapEff:") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				val, err := strconv.ParseUint(parts[1], 16, 64)
				if err == nil {
					// CAP_SYS_TIME is capability 25 (1 << 25)
					return (val & (1 << 25)) != 0
				}
			}
		}
	}
	return false
}
