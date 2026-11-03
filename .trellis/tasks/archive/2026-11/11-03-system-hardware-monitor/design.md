# 系统硬件信息与异构资源监控技术设计 (Technical Design)

## 1. 架构总览与模块划分

```
┌────────────────────────────────────────────────────────────────┐
│                       React 19 SPA                             │
│  /settings/system (SettingsSystem Component)                   │
│  ├── Header: Hostname, OS/Arch, Uptime, Manual/Auto Refresh   │
│  ├── Top Stat Cards: CPU, Memory, NPU, Network, Primary Disk  │
│  ├── Trend Charts (Recharts): CPU/NPU, Memory, Network, Temp  │
│  └── Hardware Details: Storage Table, Network NICs, Thermals  │
└───────────────────────────────▲────────────────────────────────┘
                                │ JSON HTTP (TanStack Query, 2.5s poll)
┌───────────────────────────────▼────────────────────────────────┐
│                  Go Backend (Gin HTTP API)                     │
│  GET /api/v1/system/hardware                                   │
│  Handler -> SystemInfoService -> Inspector (Collector)         │
│                               -> History RingBuffer (60-90 pts)│
└───────────────────────────────┬────────────────────────────────┘
                                │
        ┌───────────────────────┴───────────────────────┐
        ▼ (Linux)                                       ▼ (Fallback / Non-Linux)
 ┌─────────────────────────────┐                 ┌─────────────────────────────┐
 │ /proc/stat (CPU usage/cores)│                 │ Mock/Static OS stats        │
 │ /proc/meminfo (RAM/Swap)    │                 │ Mock CPU/Mem readings       │
 │ /proc/net/dev (NIC Rx/Tx)   │                 │ NPU supported = false       │
 │ /proc/diskstats + statfs    │                 │ Empty thermal zones         │
 │ /sys/kernel/debug/rknpu/load│                 └─────────────────────────────┘
 │ /sys/class/thermal/* (Temp) │
 └─────────────────────────────┘
```

## 2. 后端数据结构与 API 设计

### 包定位：`internal/systeminfo`

- `collector.go` / `collector_linux.go` / `collector_fallback.go`：底层系统采集器抽象与实现。
- `ring_buffer.go`：轻量线程安全环形缓冲区，存放最近 60~90 个时间点的采样快照。
- `service.go`：周期性采样或请求驱动计算速率，维护最新状态与历史。
- `handler.go`：Gin API Handler，提供 `GET /api/v1/system/hardware`。
- `model.go`：数据契约结构体。
- `module.go`：Uber Fx 组件工厂与路由注册。

### 数据模型（DTO / Response）

```go
type SystemHardwareResponse struct {
    UpdatedAt      time.Time                `json:"updatedAt"`
    Host           HostInfo                 `json:"host"`
    CPU            CPUStatus                `json:"cpu"`
    Memory         MemoryStatus             `json:"memory"`
    Storage        []StorageMountStatus     `json:"storage"`
    Network        []NetworkInterfaceStatus `json:"network"`
    Heterogeneous  HeterogeneousStatus      `json:"heterogeneous"`
    History        []HardwareHistoryPoint   `json:"history"`
}

type HostInfo struct {
    Hostname      string    `json:"hostname"`
    OS            string    `json:"os"`
    Arch          string    `json:"arch"`
    KernelVersion string    `json:"kernelVersion"`
    UptimeSeconds uint64    `json:"uptimeSeconds"`
    BootTime      time.Time `json:"bootTime"`
    LoadAvg       [3]float64 `json:"loadAvg"` // 1m, 5m, 15m
}

type CPUStatus struct {
    ModelName    string    `json:"modelName"`
    Cores        int       `json:"cores"`
    UsagePercent float64   `json:"usagePercent"`
    CoreUsages   []float64 `json:"coreUsages"`
    Temperature  float64   `json:"temperature"` // 主要或最高温度 (°C)
}

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

type StorageMountStatus struct {
    MountPoint  string  `json:"mountPoint"`
    Device      string  `json:"device"`
    FSType      string  `json:"fsType"`
    TotalBytes  uint64  `json:"totalBytes"`
    UsedBytes   uint64  `json:"usedBytes"`
    FreeBytes   uint64  `json:"freeBytes"`
    UsedPercent float64 `json:"usedPercent"`
}

type NetworkInterfaceStatus struct {
    Interface   string `json:"interface"`
    RxBytesRate uint64 `json:"rxBytesRate"` // 实时每秒速率
    TxBytesRate uint64 `json:"txBytesRate"`
    RxTotalBytes uint64 `json:"rxTotalBytes"`
    TxTotalBytes uint64 `json:"txTotalBytes"`
    IsUp        bool   `json:"isUp"`
}

type HeterogeneousStatus struct {
    NPU          NPUStatus         `json:"npu"`
    Temperatures []TemperatureInfo `json:"temperatures"`
}

type NPUStatus struct {
    Supported   bool      `json:"supported"`
    Name        string    `json:"name"`
    LoadPercent float64   `json:"loadPercent"`
    CoreLoads   []float64 `json:"coreLoads"`
    Temperature float64   `json:"temperature"`
}

type TemperatureInfo struct {
    Label       string  `json:"label"` // e.g. "SoC", "CPU", "NPU", "GPU"
    Temperature float64 `json:"temperature"` // °C
}

type HardwareHistoryPoint struct {
    Timestamp       time.Time `json:"timestamp"`
    CPUUsagePercent float64   `json:"cpuUsagePercent"`
    MemUsagePercent float64   `json:"memUsagePercent"`
    NPULoadPercent  float64   `json:"npuLoadPercent"`
    NetRxRate       uint64    `json:"netRxRate"` // bytes/s
    NetTxRate       uint64    `json:"netTxRate"` // bytes/s
    Temperature     float64   `json:"temperature"` // °C
}
```

## 3. 采样与计算原理

### CPU 使用率计算
- `/proc/stat` 第一行 `cpu` 以及后续 `cpu0`, `cpu1`...:
  `user + nice + system + idle + iowait + irq + softirq + steal`
- 总 CPU 时间：`Total = sum(all fields)`
- 空闲时间：`Idle = idle + iowait`
- `ΔTotal = Total_curr - Total_prev`，`ΔIdle = Idle_curr - Idle_prev`
- `Usage = (1.0 - ΔIdle / ΔTotal) * 100.0`
- 边界防御：如果 `ΔTotal <= 0`，则返回前次利用率或 0。

### 网络速率计算
- `/proc/net/dev` 记录每个接口的累积 `rx_bytes` 和 `tx_bytes`。
- `RxRate = (rx_curr - rx_prev) / (time_curr - time_prev)`。

### 异构 NPU 探测
- 针对 Rockchip 平台：
  - 尝试读取 `/sys/kernel/debug/rknpu/load`（如果 debugfs 已挂载）。
    格式匹配：`NPU load:  Core0:  XX%, Core1: YY%` 或类似行。
  - 尝试读取 `/sys/class/devfreq/` 下带有 rknpu / npu 的 `cur_freq` 与 `load`。
  - 若不可读或非 RK 平台，尝试检查 `/dev/rknpu*` 或常规 NPU 节点；无法探测时优雅置 `supported: false`。

### 温度探测
- 遍历 `/sys/class/thermal/thermal_zone*`：
  - 读 `type`（如 `soc_thermal`, `cpu-thermal`, `npu-thermal`, `gpu-thermal`, `tsadc`）。
  - 读 `temp`，数值除以 1000.0 转为摄氏度。

## 4. 前端结构与 UI 设计

- 路由：`web/src/routes/_authenticated/settings/system.tsx`
- 特性切片：`web/src/features/settings/system/`
  - `api/system-api.ts`：TanStack Query Hook `useSystemHardware()`
  - `types.ts`：Zod Schema & TypeScript types
  - `components/`：
    - `system-header.tsx`：设备名、架构、内核、Uptime、自动/手动刷新按钮
    - `stat-cards.tsx`：CPU、内存、NPU、网络、主磁盘的瞬时状态卡片
    - `trend-charts.tsx`：
      - Tabs 或网格：CPU & NPU 利用率趋势折线图、内存走势图、网卡吞吐上下行面积图、温度曲线
    - `storage-table.tsx`：多磁盘/挂载点容量与使用率进度条列表
    - `network-table.tsx`：各网卡接口状态与收发流量
    - `thermal-list.tsx`：各测温点温度标签
  - `locales/`：中简、中繁、英文语言包

## 5. 质量保证与安全约束

1. **并发与资源安全**：
   - Service 内使用 `sync.RWMutex` 保护 RingBuffer 与上一次采样基准点（prevSnapshot）。
   - 后台轻量 Goroutine 每 2s 进行一次采样更新（仅当有需要或随请求触发并缓冲）。
   - 服务关闭时通过 context 取消后台采样协程，保证优雅退出。
2. **零 CGO、跨平台无崩塌**：
   - 全纯 Go 实现，构建产物体积小、无动态链接依赖。
   - 非 Linux 平台（如开发者 macOS 调试前端）自动切换至 Fallback 实现。
3. **零裸 panic、零裸 JSON**：
   - Handler 严格调用 `httputil.Success(c, resp)`。
   - 所有的 API 响应字段具备 Null-safety 防御。
