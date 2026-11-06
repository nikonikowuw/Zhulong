//go:build linux

package systeminfo

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var (
	rknpuCorePattern   = regexp.MustCompile(`Core\d+:\s*(\d+)%`)
	rknpuSinglePattern = regexp.MustCompile(`(\d+)%`)

	// supportedFSTypes restricts storage telemetry to filesystems a mount reading
	// is meaningful for. tmpfs/devtmpfs reports a size but no persistent geometry,
	// and publishing it invites operators to read "2% used" off a RAM disk.
	supportedFSTypes = map[string]struct{}{
		"ext4":  {},
		"ext3":  {},
		"ext2":  {},
		"xfs":   {},
		"btrfs": {},
		"f2fs":  {},
		"vfat":  {},
		"exfat": {},
		"ntfs":  {},
		"zfs":   {},
	}
)

// linuxCollector reads system hardware metrics directly from Linux procfs and sysfs.
type linuxCollector struct{}

func newPlatformCollector() collector {
	return &linuxCollector{}
}

// CollectSnapshot collects a point-in-time raw hardware snapshot.
//
// Each stage polls ctx before it opens a file, so an abandoned request stops paying
// for the remainder of the sweep. Individual reads are not interruptible — procfs
// content is kernel-generated — which is why the cancellation points sit between
// nodes rather than inside a single scan.
func (c *linuxCollector) CollectSnapshot(ctx context.Context) (*rawHardwareSnapshot, error) {
	now := time.Now().UTC()

	host := c.collectHostInfo(ctx)

	cpuTotal, cpuCores, cpuModel, err := c.collectCPU(ctx)
	if err != nil {
		return nil, err
	}

	mem, err := c.collectMemory(ctx)
	if err != nil {
		return nil, err
	}

	storage, err := c.collectStorage(ctx)
	if err != nil {
		return nil, err
	}

	network, err := c.collectNetwork(ctx)
	if err != nil {
		return nil, err
	}

	thermals := c.collectThermals(ctx)

	npu, err := c.collectNPU(ctx, thermals)
	if err != nil {
		return nil, err
	}

	return &rawHardwareSnapshot{
		Timestamp: now,
		Host:      host,
		CPUTotal:  cpuTotal,
		CPUCores:  cpuCores,
		CPUModel:  cpuModel,
		Memory:    mem,
		Storage:   storage,
		Network:   network,
		NPU:       npu,
		Thermals:  thermals,
	}, nil
}

// collectHostInfo assembles static host properties plus the two values derived from
// them (uptime, boot time). None of these nodes are mandatory: a stripped-down
// container may expose only a subset, and a zero value reads as "unknown" rather
// than failing the whole sample.
func (c *linuxCollector) collectHostInfo(ctx context.Context) HostInfo {
	hostname, _ := os.Hostname()

	var uptimeSeconds uint64
	if fields := strings.Fields(readSingleLine(ctx, "/proc/uptime")); len(fields) > 0 {
		if seconds, err := strconv.ParseFloat(fields[0], 64); err == nil && seconds >= 0 {
			uptimeSeconds = uint64(seconds)
		}
	}

	var loadAvg [3]float64
	if fields := strings.Fields(readSingleLine(ctx, "/proc/loadavg")); len(fields) >= 3 {
		for i := range loadAvg {
			loadAvg[i], _ = strconv.ParseFloat(fields[i], 64)
		}
	}

	return HostInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		KernelVersion: readSingleLine(ctx, "/proc/sys/kernel/osrelease"),
		UptimeSeconds: uptimeSeconds,
		BootTime:      time.Now().UTC().Add(-time.Duration(uptimeSeconds) * time.Second),
		LoadAvg:       loadAvg,
	}
}

func (c *linuxCollector) collectCPU(ctx context.Context) (rawCPUTimes, []rawCPUTimes, string, error) {
	var total rawCPUTimes
	cores := []rawCPUTimes{}

	err := scanLines(ctx, "/proc/stat", func(line string) {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			return
		}
		switch {
		case fields[0] == "cpu":
			total = parseCPUTimesLine(fields[1:])
		case strings.HasPrefix(fields[0], "cpu") && len(fields[0]) > 3:
			cores = append(cores, parseCPUTimesLine(fields[1:]))
		}
	})
	if err != nil {
		return total, cores, "", err
	}

	model, err := c.cpuModel(ctx)
	if err != nil {
		return total, cores, "", err
	}

	return total, cores, model, nil
}

// cpuModel reads the first matching model key out of /proc/cpuinfo, falling back to
// the architecture when the kernel does not publish one (common on ARM).
func (c *linuxCollector) cpuModel(ctx context.Context) (string, error) {
	var model string
	err := scanLines(ctx, "/proc/cpuinfo", func(line string) {
		if model != "" {
			return
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			return
		}
		switch strings.TrimSpace(key) {
		case "model name", "Hardware", "Model":
			model = strings.TrimSpace(value)
		}
	})
	if err != nil {
		return "", err
	}
	if model != "" {
		return model, nil
	}
	return fmt.Sprintf("%s (%d Cores)", runtime.GOARCH, runtime.NumCPU()), nil
}

func parseCPUTimesLine(fields []string) rawCPUTimes {
	var times rawCPUTimes
	values := []*uint64{
		&times.User,
		&times.Nice,
		&times.System,
		&times.Idle,
		&times.IOWait,
		&times.IRQ,
		&times.SoftIRQ,
		&times.Steal,
	}
	for i, target := range values {
		if i >= len(fields) {
			return times
		}
		*target, _ = strconv.ParseUint(fields[i], 10, 64)
	}
	return times
}

// memoryRaw groups the /proc/meminfo counters that decide how "used" is derived.
// They travel together, so they are passed together — six loose uint64 arguments
// would be an invitation to transpose two of them.
type memoryRaw struct {
	total     uint64
	free      uint64
	available uint64
	buffers   uint64
	cached    uint64
}

// usage derives used bytes and percentage. MemAvailable is the kernel's own estimate
// of what a new workload could claim and is preferred; the free+buffers+cached
// fallback only applies to kernels older than 3.14, which never publish it.
func (m memoryRaw) usage() (uint64, float64) {
	var usedBytes uint64
	switch {
	case m.available > 0 && m.total >= m.available:
		usedBytes = m.total - m.available
	case m.total >= m.free+m.buffers+m.cached:
		usedBytes = m.total - m.free - m.buffers - m.cached
	}

	var usedPercent float64
	if m.total > 0 {
		usedPercent = roundPercent(float64(usedBytes) / float64(m.total) * 100)
	}
	return usedBytes, usedPercent
}

// meminfoCounters is the /proc/meminfo raw counter set, converted to bytes.
type meminfoCounters struct {
	memoryRaw
	swapTotal uint64
	swapFree  uint64
}

// swapUsage derives swap used bytes and percentage. A zero total means the host has
// no swap configured, which reports 0/0 rather than dividing by zero.
func (m meminfoCounters) swapUsage() (uint64, float64) {
	if m.swapTotal == 0 || m.swapTotal < m.swapFree {
		return 0, 0
	}
	used := m.swapTotal - m.swapFree
	return used, roundPercent(float64(used) / float64(m.swapTotal) * 100)
}

func (c *linuxCollector) collectMemory(ctx context.Context) (MemoryStatus, error) {
	counters, err := parseMeminfo(ctx)
	if err != nil {
		return MemoryStatus{}, err
	}

	usedBytes, usedPercent := counters.usage()
	swapUsedBytes, swapUsedPercent := counters.swapUsage()

	return MemoryStatus{
		TotalBytes:      counters.total,
		UsedBytes:       usedBytes,
		AvailableBytes:  counters.available,
		FreeBytes:       counters.free,
		BuffersBytes:    counters.buffers,
		CachedBytes:     counters.cached,
		UsedPercent:     usedPercent,
		SwapTotalBytes:  counters.swapTotal,
		SwapUsedBytes:   swapUsedBytes,
		SwapUsedPercent: swapUsedPercent,
	}, nil
}

func parseMeminfo(ctx context.Context) (meminfoCounters, error) {
	var counters meminfoCounters

	err := scanLines(ctx, "/proc/meminfo", func(line string) {
		key, value, found := strings.Cut(line, ":")
		if !found {
			return
		}
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return
		}
		kb, parseErr := strconv.ParseUint(fields[0], 10, 64)
		if parseErr != nil {
			return
		}
		bytes := kb * 1024

		switch strings.TrimSpace(key) {
		case "MemTotal":
			counters.total = bytes
		case "MemFree":
			counters.free = bytes
		case "MemAvailable":
			counters.available = bytes
		case "Buffers":
			counters.buffers = bytes
		case "Cached":
			counters.cached = bytes
		case "SwapTotal":
			counters.swapTotal = bytes
		case "SwapFree":
			counters.swapFree = bytes
		}
	})
	return counters, err
}

func (c *linuxCollector) collectStorage(ctx context.Context) ([]StorageMountStatus, error) {
	seenDevices := make(map[string]struct{})
	seenMounts := make(map[string]struct{})
	mounts := []StorageMountStatus{}

	err := scanLines(ctx, "/proc/mounts", func(line string) {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return
		}
		device, mountPoint, fsType := fields[0], fields[1], fields[2]

		if _, supported := supportedFSTypes[fsType]; !supported {
			return
		}
		if !strings.HasPrefix(device, "/dev/") {
			return
		}
		// A device bind-mounted at several paths, or two overlays of the same
		// filesystem, would otherwise report identical byte counts repeatedly.
		if _, seen := seenMounts[mountPoint]; seen {
			return
		}
		if _, seen := seenDevices[device]; seen {
			return
		}

		status, ok := statMount(device, mountPoint, fsType)
		if !ok {
			return
		}

		seenDevices[device] = struct{}{}
		seenMounts[mountPoint] = struct{}{}
		mounts = append(mounts, status)
	})
	if err != nil {
		return nil, err
	}

	return mounts, nil
}

// statMount reports filesystem geometry for one mount. ok is false when the mount
// point is unreadable or reports a nonsensical block size — an unmounted device
// still listed in /proc/mounts, or a filesystem mid-teardown.
func statMount(device, mountPoint, fsType string) (StorageMountStatus, bool) {
	var statfs unix.Statfs_t
	if err := unix.Statfs(mountPoint, &statfs); err != nil {
		return StorageMountStatus{}, false
	}
	if statfs.Bsize <= 0 {
		return StorageMountStatus{}, false
	}

	blockSize := uint64(statfs.Bsize)
	totalBytes := statfs.Blocks * blockSize
	freeBytes := statfs.Bavail * blockSize

	var usedBytes uint64
	if statfs.Blocks >= statfs.Bfree {
		usedBytes = (statfs.Blocks - statfs.Bfree) * blockSize
	} else if totalBytes >= freeBytes {
		usedBytes = totalBytes - freeBytes
	}

	var usedPercent float64
	if totalBytes > 0 {
		usedPercent = roundPercent(float64(usedBytes) / float64(totalBytes) * 100)
	}

	return StorageMountStatus{
		MountPoint:  mountPoint,
		Device:      device,
		FSType:      fsType,
		TotalBytes:  totalBytes,
		UsedBytes:   usedBytes,
		FreeBytes:   freeBytes,
		UsedPercent: usedPercent,
	}, true
}

func (c *linuxCollector) collectNetwork(ctx context.Context) (map[string]rawNetworkInterface, error) {
	physical := make(map[string]rawNetworkInterface)
	// hidden keeps kernel/container plumbing aside so we can fall back to it when
	// the host exposes no physical link at all (Zhulong running inside a container).
	hidden := make(map[string]rawNetworkInterface)

	pastHeader := false
	err := scanLines(ctx, "/proc/net/dev", func(line string) {
		// /proc/net/dev opens with two header lines before the first device entry.
		if !pastHeader {
			if strings.Contains(line, "|") {
				return
			}
			pastHeader = true
		}

		entry, ok := parseNetDevLine(ctx, line)
		if !ok {
			return
		}
		if isVirtualNetworkInterface(entry.Interface) {
			hidden[entry.Interface] = entry
			return
		}
		physical[entry.Interface] = entry
	})
	if err != nil {
		return nil, err
	}

	// 兜底：Zhulong 跑在容器里时 eth0 本身就是 veth，过滤后一空如洗。
	// 此时宁可展示实现细节，也不要给运维一个没有任何网卡的仪表盘。
	if len(physical) == 0 {
		return hidden, nil
	}
	return physical, nil
}

// parseNetDevLine decodes one /proc/net/dev row. ok is false for the header rows,
// loopback, and any row whose counters do not parse — a truncated counter would
// otherwise publish a bogus baseline that makes the next sample's rate nonsense.
func parseNetDevLine(ctx context.Context, line string) (rawNetworkInterface, bool) {
	iface, counters, found := strings.Cut(line, ":")
	if !found {
		return rawNetworkInterface{}, false
	}
	iface = strings.TrimSpace(iface)
	// 回环永远不是有效链路：它没有硬件 backing，也从不承载业务流量。
	if iface == "" || iface == "lo" {
		return rawNetworkInterface{}, false
	}

	fields := strings.Fields(counters)
	if len(fields) < 9 {
		return rawNetworkInterface{}, false
	}
	rxBytes, rxErr := strconv.ParseUint(fields[0], 10, 64)
	txBytes, txErr := strconv.ParseUint(fields[8], 10, 64)
	if rxErr != nil || txErr != nil {
		return rawNetworkInterface{}, false
	}

	operstate := readSingleLine(ctx, fmt.Sprintf("/sys/class/net/%s/operstate", iface))

	return rawNetworkInterface{
		Interface:    iface,
		RxTotalBytes: rxBytes,
		TxTotalBytes: txBytes,
		IsUp:         strings.EqualFold(operstate, "up"),
	}, true
}

func (c *linuxCollector) collectThermals(ctx context.Context) []TemperatureInfo {
	result := []TemperatureInfo{}

	matches, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		return result
	}

	for _, zonePath := range matches {
		if ctx.Err() != nil {
			return result
		}

		tempStr := readSingleLine(ctx, filepath.Join(zonePath, "temp"))
		if tempStr == "" {
			continue
		}

		milliCelsius, parseErr := strconv.ParseFloat(tempStr, 64)
		// Zones that are not populated report 0, and a few drivers publish a
		// sentinel around 200000; neither is a reading worth charting.
		if parseErr != nil || milliCelsius <= 0 || milliCelsius > 150000 {
			continue
		}

		result = append(result, TemperatureInfo{
			Label:       formatThermalLabel(readSingleLine(ctx, filepath.Join(zonePath, "type"))),
			Temperature: milliCelsius / 1000,
		})
	}

	return result
}

func (c *linuxCollector) collectNPU(ctx context.Context, thermals []TemperatureInfo) (NPUStatus, error) {
	// 1. Rockchip rknpu load node (debugfs). Absent unless debugfs is mounted, which
	//    is exactly why the two static device probes follow.
	loadText, readErr := readFileString(ctx, "/sys/kernel/debug/rknpu/load")
	if readErr != nil {
		return NPUStatus{}, readErr
	}
	if status, ok := parseRKNPULoad(loadText, thermals); ok {
		return status, nil
	}

	// 2. Rockchip device node without a load source: report presence, zero load.
	if _, err := os.Stat("/dev/rknpu"); err == nil {
		return npuStatus(thermals, "Rockchip NPU", []float64{0}), nil
	}

	// 3. Ascend device manager.
	if _, err := os.Stat("/dev/davinci_manager"); err == nil {
		return npuStatus(thermals, "Ascend NPU", []float64{0}), nil
	}

	return NPUStatus{
		Supported:   false,
		Name:        "None",
		LoadPercent: 0,
		CoreLoads:   []float64{},
		Temperature: 0,
	}, nil
}

// parseRKNPULoad understands both debugfs formats: per-core ("Core0: 42%") and the
// older single-value ("NPU load: 42%"). ok is false when neither matches, so the
// caller can fall through to the device-node probes.
func parseRKNPULoad(text string, thermals []TemperatureInfo) (NPUStatus, bool) {
	if matches := rknpuCorePattern.FindAllStringSubmatch(text, -1); len(matches) > 0 {
		loads := make([]float64, 0, len(matches))
		for _, match := range matches {
			value, err := strconv.ParseFloat(match[1], 64)
			if err != nil {
				continue
			}
			loads = append(loads, value)
		}
		if len(loads) > 0 {
			return npuStatus(thermals, "Rockchip NPU (RKNPU)", loads), true
		}
	}

	if single := rknpuSinglePattern.FindStringSubmatch(text); len(single) > 1 {
		if value, err := strconv.ParseFloat(single[1], 64); err == nil {
			return npuStatus(thermals, "Rockchip NPU (RKNPU)", []float64{value}), true
		}
	}

	return NPUStatus{}, false
}

// npuStatus builds a supported NPU projection, deriving the aggregate load from the
// per-core readings instead of asking every caller to repeat the arithmetic.
func npuStatus(thermals []TemperatureInfo, name string, coreLoads []float64) NPUStatus {
	var sum float64
	for _, load := range coreLoads {
		sum += load
	}

	var average float64
	if len(coreLoads) > 0 {
		average = roundPercent(sum / float64(len(coreLoads)))
	}

	// A missing NPU thermal zone is normal on hosts whose NPU has no sensor; the zero
	// fallback is the correct reading, so the presence flag is intentionally dropped.
	temperature, _ := findThermalByPattern(thermals, "npu")

	return NPUStatus{
		Supported:   true,
		Name:        name,
		LoadPercent: average,
		CoreLoads:   coreLoads,
		Temperature: temperature,
	}
}

// scanLines streams path line by line, checking ctx before it touches the file so
// every node in a sweep is a cancellation point.
//
// A missing or unreadable file is not an error: many kernels and containers do not
// expose every procfs/sysfs node, and "absent" is a legitimate answer. A truncated
// scan *is* an error — publishing half of /proc/meminfo as a complete sample would
// report 0 bytes of RAM as fact.
func scanLines(ctx context.Context, path string, fn func(line string)) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fn(scanner.Text())
	}
	return scanner.Err()
}

func readSingleLine(ctx context.Context, path string) string {
	content, err := readFileString(ctx, path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(content)
}

func readFileString(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		// Absent node, not a failure: see scanLines.
		return "", nil
	}
	return string(content), nil
}
