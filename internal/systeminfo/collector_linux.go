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
	rknpuCoreRegex   = regexp.MustCompile(`Core\d+:\s*(\d+)%`)
	rknpuSingleRegex = regexp.MustCompile(`(\d+)%`)

	supportedFSTypes = map[string]bool{
		"ext4":  true,
		"ext3":  true,
		"ext2":  true,
		"xfs":   true,
		"btrfs": true,
		"f2fs":  true,
		"vfat":  true,
		"exfat": true,
		"ntfs":  true,
		"zfs":   true,
	}
)

// LinuxCollector reads system hardware metrics directly from Linux procfs and sysfs.
type LinuxCollector struct{}

// NewLinuxCollector creates a new Linux-specific hardware telemetry collector.
func NewLinuxCollector() *LinuxCollector {
	return &LinuxCollector{}
}

func newPlatformCollector() Collector {
	return NewLinuxCollector()
}

// CollectSnapshot collects a point-in-time raw hardware snapshot.
func (c *LinuxCollector) CollectSnapshot(ctx context.Context) (*RawHardwareSnapshot, error) {
	now := time.Now().UTC()

	host := c.collectHostInfo()
	cpuTotal, cpuCores, cpuModel := c.collectCPU()
	mem := c.collectMemory()
	storage := c.collectStorage()
	netMap := c.collectNetwork()
	thermals := c.collectThermals()
	npu := c.collectNPU(thermals)

	return &RawHardwareSnapshot{
		Timestamp: now,
		Host:      host,
		CPUTotal:  cpuTotal,
		CPUCores:  cpuCores,
		CPUModel:  cpuModel,
		Memory:    mem,
		Storage:   storage,
		Network:   netMap,
		NPU:       npu,
		Thermals:  thermals,
	}, nil
}

func (c *LinuxCollector) collectHostInfo() HostInfo {
	hostname, _ := os.Hostname()
	kernelVersion := readSingleLine("/proc/sys/kernel/osrelease")

	var uptimeSeconds uint64
	if uptimeBytes, err := os.ReadFile("/proc/uptime"); err == nil {
		fields := strings.Fields(string(uptimeBytes))
		if len(fields) > 0 {
			if sec, err := strconv.ParseFloat(fields[0], 64); err == nil && sec >= 0 {
				uptimeSeconds = uint64(sec)
			}
		}
	}

	bootTime := time.Now().UTC().Add(-time.Duration(uptimeSeconds) * time.Second)

	var loadAvg [3]float64
	if loadBytes, err := os.ReadFile("/proc/loadavg"); err == nil {
		fields := strings.Fields(string(loadBytes))
		if len(fields) >= 3 {
			l1, _ := strconv.ParseFloat(fields[0], 64)
			l5, _ := strconv.ParseFloat(fields[1], 64)
			l15, _ := strconv.ParseFloat(fields[2], 64)
			loadAvg = [3]float64{l1, l5, l15}
		}
	}

	return HostInfo{
		Hostname:      hostname,
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		KernelVersion: kernelVersion,
		UptimeSeconds: uptimeSeconds,
		BootTime:      bootTime,
		LoadAvg:       loadAvg,
	}
}

func (c *LinuxCollector) collectCPU() (RawCPUTimes, []RawCPUTimes, string) {
	var total RawCPUTimes
	cores := []RawCPUTimes{}
	var cpuModel string

	if file, err := os.Open("/proc/stat"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			if fields[0] == "cpu" {
				total = parseCPUTimesLine(fields[1:])
			} else if strings.HasPrefix(fields[0], "cpu") && len(fields[0]) > 3 {
				// per-core cpu0, cpu1...
				cores = append(cores, parseCPUTimesLine(fields[1:]))
			}
		}
	}

	// Model name from /proc/cpuinfo
	if file, err := os.Open("/proc/cpuinfo"); err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				isModelKey := key == "model name" || key == "Hardware" || key == "Model"
				if isModelKey && cpuModel == "" {
					cpuModel = val
				}
			}
		}
	}
	if cpuModel == "" {
		cpuModel = fmt.Sprintf("%s (%d Cores)", runtime.GOARCH, runtime.NumCPU())
	}

	return total, cores, cpuModel
}

func parseCPUTimesLine(fields []string) RawCPUTimes {
	var t RawCPUTimes
	getVal := func(idx int) uint64 {
		if idx < len(fields) {
			v, _ := strconv.ParseUint(fields[idx], 10, 64)
			return v
		}
		return 0
	}

	t.User = getVal(0)
	t.Nice = getVal(1)
	t.System = getVal(2)
	t.Idle = getVal(3)
	t.IOWait = getVal(4)
	t.IRQ = getVal(5)
	t.SoftIRQ = getVal(6)
	t.Steal = getVal(7)
	return t
}

func (c *LinuxCollector) collectMemory() MemoryStatus {
	var mem MemoryStatus
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return mem
	}
	defer file.Close()

	var (
		memTotal     uint64
		memFree      uint64
		memAvailable uint64
		buffers      uint64
		cached       uint64
		swapTotal    uint64
		swapFree     uint64
	)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		valFields := strings.Fields(parts[1])
		if len(valFields) == 0 {
			continue
		}
		valKb, _ := strconv.ParseUint(valFields[0], 10, 64)
		valBytes := valKb * 1024

		switch key {
		case "MemTotal":
			memTotal = valBytes
		case "MemFree":
			memFree = valBytes
		case "MemAvailable":
			memAvailable = valBytes
		case "Buffers":
			buffers = valBytes
		case "Cached":
			cached = valBytes
		case "SwapTotal":
			swapTotal = valBytes
		case "SwapFree":
			swapFree = valBytes
		}
	}

	var usedBytes uint64
	if memAvailable > 0 && memTotal >= memAvailable {
		usedBytes = memTotal - memAvailable
	} else if memTotal >= (memFree + buffers + cached) {
		usedBytes = memTotal - memFree - buffers - cached
	}

	var usedPercent float64
	if memTotal > 0 {
		usedPercent = float64(usedBytes) / float64(memTotal) * 100.0
	}

	var swapUsedBytes uint64
	var swapUsedPercent float64
	if swapTotal > 0 && swapTotal >= swapFree {
		swapUsedBytes = swapTotal - swapFree
		swapUsedPercent = float64(swapUsedBytes) / float64(swapTotal) * 100.0
	}

	return MemoryStatus{
		TotalBytes:      memTotal,
		UsedBytes:       usedBytes,
		AvailableBytes:  memAvailable,
		FreeBytes:       memFree,
		BuffersBytes:    buffers,
		CachedBytes:     cached,
		UsedPercent:     usedPercent,
		SwapTotalBytes:  swapTotal,
		SwapUsedBytes:   swapUsedBytes,
		SwapUsedPercent: swapUsedPercent,
	}
}

func (c *LinuxCollector) collectStorage() []StorageMountStatus {
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return []StorageMountStatus{}
	}
	defer file.Close()

	seenDevices := map[string]bool{}
	seenMounts := map[string]bool{}
	mounts := []StorageMountStatus{}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}
		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]

		if !supportedFSTypes[fsType] {
			continue
		}
		if !strings.HasPrefix(device, "/dev/") {
			continue
		}
		if seenMounts[mountPoint] || seenDevices[device] {
			continue
		}

		var statfs unix.Statfs_t
		if err := unix.Statfs(mountPoint, &statfs); err != nil {
			continue
		}
		if statfs.Bsize <= 0 {
			continue
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

		var usedPercent float64
		if totalBytes > 0 {
			usedPercent = float64(usedBytes) / float64(totalBytes) * 100.0
		}

		seenDevices[device] = true
		seenMounts[mountPoint] = true
		mounts = append(mounts, StorageMountStatus{
			MountPoint:  mountPoint,
			Device:      device,
			FSType:      fsType,
			TotalBytes:  totalBytes,
			UsedBytes:   usedBytes,
			FreeBytes:   freeBytes,
			UsedPercent: usedPercent,
		})
	}

	return mounts
}

func (c *LinuxCollector) collectNetwork() map[string]RawNetworkInterface {
	result := make(map[string]RawNetworkInterface)
	// hidden keeps kernel/container plumbing aside so we can fall back to it when
	// the host exposes no physical link at all (Zhulong running inside a container).
	hidden := make(map[string]RawNetworkInterface)

	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return result
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Skip header 2 lines
	for i := 0; i < 2 && scanner.Scan(); i++ {
	}

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		// 回环永远不是有效链路：它没有硬件 backing，也从不承载业务流量。
		if iface == "lo" {
			continue
		}

		fields := strings.Fields(parts[1])
		if len(fields) < 9 {
			continue
		}
		rxBytes, _ := strconv.ParseUint(fields[0], 10, 64)
		txBytes, _ := strconv.ParseUint(fields[8], 10, 64)

		operstate := readSingleLine(fmt.Sprintf("/sys/class/net/%s/operstate", iface))
		isUp := strings.ToLower(operstate) == "up"

		entry := RawNetworkInterface{
			Interface:    iface,
			RxTotalBytes: rxBytes,
			TxTotalBytes: txBytes,
			IsUp:         isUp,
		}

		if isVirtualNetworkInterface(iface) {
			hidden[iface] = entry
			continue
		}
		result[iface] = entry
	}

	// 兜底：Zhulong 跑在容器里时 eth0 本身就是 veth，过滤后一空如洗。
	// 此时宁可展示实现细节，也不要给运维一个没有任何网卡的仪表盘。
	if len(result) == 0 {
		return hidden
	}

	return result
}

func (c *LinuxCollector) collectThermals() []TemperatureInfo {
	result := []TemperatureInfo{}
	matches, err := filepath.Glob("/sys/class/thermal/thermal_zone*")
	if err != nil {
		return result
	}

	for _, zonePath := range matches {
		typeStr := readSingleLine(filepath.Join(zonePath, "type"))
		tempStr := readSingleLine(filepath.Join(zonePath, "temp"))
		if tempStr == "" {
			continue
		}

		tempMilli, err := strconv.ParseFloat(tempStr, 64)
		isInvalidTemp := err != nil || tempMilli <= 0 || tempMilli > 150000
		if isInvalidTemp {
			continue
		}

		label := formatThermalLabel(typeStr)
		tempC := tempMilli / 1000.0

		result = append(result, TemperatureInfo{
			Label:       label,
			Temperature: tempC,
		})
	}

	return result
}

func (c *LinuxCollector) collectNPU(thermals []TemperatureInfo) NPUStatus {
	// 1. Rockchip rknpu load node (debugfs)
	rknpuLoadPath := "/sys/kernel/debug/rknpu/load"
	if content, err := os.ReadFile(rknpuLoadPath); err == nil {
		text := string(content)
		matches := rknpuCoreRegex.FindAllStringSubmatch(text, -1)
		if len(matches) > 0 {
			coreLoads := make([]float64, 0, len(matches))
			var sum float64
			for _, m := range matches {
				v, _ := strconv.ParseFloat(m[1], 64)
				coreLoads = append(coreLoads, v)
				sum += v
			}
			loadPercent := sum / float64(len(coreLoads))
			return NPUStatus{
				Supported:   true,
				Name:        "Rockchip NPU (RKNPU)",
				LoadPercent: loadPercent,
				CoreLoads:   coreLoads,
				Temperature: findThermalByPattern(thermals, "npu"),
			}
		}

		// Single load format "NPU load: XX%"
		if single := rknpuSingleRegex.FindStringSubmatch(text); len(single) > 1 {
			v, _ := strconv.ParseFloat(single[1], 64)
			return NPUStatus{
				Supported:   true,
				Name:        "Rockchip NPU (RKNPU)",
				LoadPercent: v,
				CoreLoads:   []float64{v},
				Temperature: findThermalByPattern(thermals, "npu"),
			}
		}
	}

	// 2. Check for devfreq or device nodes
	if _, err := os.Stat("/dev/rknpu"); err == nil {
		return NPUStatus{
			Supported:   true,
			Name:        "Rockchip NPU",
			LoadPercent: 0,
			CoreLoads:   []float64{0},
			Temperature: findThermalByPattern(thermals, "npu"),
		}
	}

	// 3. Fallback: Check Ascend /dev/davinci*
	if _, err := os.Stat("/dev/davinci_manager"); err == nil {
		return NPUStatus{
			Supported:   true,
			Name:        "Ascend NPU",
			LoadPercent: 0,
			CoreLoads:   []float64{0},
			Temperature: findThermalByPattern(thermals, "npu"),
		}
	}

	return NPUStatus{
		Supported:   false,
		Name:        "None",
		LoadPercent: 0,
		CoreLoads:   []float64{},
		Temperature: 0,
	}
}

func readSingleLine(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
