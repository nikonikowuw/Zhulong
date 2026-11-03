package systeminfo

import "time"

// SystemHardwareResponse represents the top-level hardware telemetry snapshot.
type SystemHardwareResponse struct {
	UpdatedAt     time.Time                `json:"updatedAt"`
	Host          HostInfo                 `json:"host"`
	CPU           CPUStatus                `json:"cpu"`
	Memory        MemoryStatus             `json:"memory"`
	Storage       []StorageMountStatus     `json:"storage"`
	Network       []NetworkInterfaceStatus `json:"network"`
	Heterogeneous HeterogeneousStatus      `json:"heterogeneous"`
	History       []HardwareHistoryPoint   `json:"history"`
}

// HostInfo contains static and dynamic host-level system properties.
type HostInfo struct {
	Hostname      string     `json:"hostname"`
	OS            string     `json:"os"`
	Arch          string     `json:"arch"`
	KernelVersion string     `json:"kernelVersion"`
	UptimeSeconds uint64     `json:"uptimeSeconds"`
	BootTime      time.Time  `json:"bootTime"`
	LoadAvg       [3]float64 `json:"loadAvg"`
}

// CPUStatus represents processor hardware information and utilization.
type CPUStatus struct {
	ModelName    string    `json:"modelName"`
	Cores        int       `json:"cores"`
	UsagePercent float64   `json:"usagePercent"`
	CoreUsages   []float64 `json:"coreUsages"`
	Temperature  float64   `json:"temperature"`
}

// MemoryStatus represents physical RAM and swap space utilization.
type MemoryStatus struct {
	TotalBytes      uint64  `json:"totalBytes"`
	UsedBytes       uint64  `json:"usedBytes"`
	AvailableBytes  uint64  `json:"availableBytes"`
	FreeBytes       uint64  `json:"freeBytes"`
	BuffersBytes    uint64  `json:"buffersBytes"`
	CachedBytes     uint64  `json:"cachedBytes"`
	UsedPercent     float64 `json:"usedPercent"`
	SwapTotalBytes  uint64  `json:"swapTotalBytes"`
	SwapUsedBytes   uint64  `json:"swapUsedBytes"`
	SwapUsedPercent float64 `json:"swapUsedPercent"`
}

// StorageMountStatus represents filesystem geometry for a mounted partition.
type StorageMountStatus struct {
	MountPoint  string  `json:"mountPoint"`
	Device      string  `json:"device"`
	FSType      string  `json:"fsType"`
	TotalBytes  uint64  `json:"totalBytes"`
	UsedBytes   uint64  `json:"usedBytes"`
	FreeBytes   uint64  `json:"freeBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// NetworkInterfaceStatus represents traffic and link status for a single network device.
type NetworkInterfaceStatus struct {
	Interface    string `json:"interface"`
	RxBytesRate  uint64 `json:"rxBytesRate"`
	TxBytesRate  uint64 `json:"txBytesRate"`
	RxTotalBytes uint64 `json:"rxTotalBytes"`
	TxTotalBytes uint64 `json:"txTotalBytes"`
	IsUp         bool   `json:"isUp"`
}

// HeterogeneousStatus represents specialized coprocessors and thermal telemetry.
type HeterogeneousStatus struct {
	NPU          NPUStatus         `json:"npu"`
	Temperatures []TemperatureInfo `json:"temperatures"`
}

// NPUStatus represents neural processing unit capability and real-time load.
type NPUStatus struct {
	Supported   bool      `json:"supported"`
	Name        string    `json:"name"`
	LoadPercent float64   `json:"loadPercent"`
	CoreLoads   []float64 `json:"coreLoads"`
	Temperature float64   `json:"temperature"`
}

// TemperatureInfo represents a single thermal sensor reading.
type TemperatureInfo struct {
	Label       string  `json:"label"`
	Temperature float64 `json:"temperature"`
}

// HardwareHistoryPoint stores a single point in time for hardware trend charts.
type HardwareHistoryPoint struct {
	Timestamp       time.Time `json:"timestamp"`
	CPUUsagePercent float64   `json:"cpuUsagePercent"`
	MemUsagePercent float64   `json:"memUsagePercent"`
	NPULoadPercent  float64   `json:"npuLoadPercent"`
	NetRxRate       uint64    `json:"netRxRate"`
	NetTxRate       uint64    `json:"netTxRate"`
	Temperature     float64   `json:"temperature"`
}
