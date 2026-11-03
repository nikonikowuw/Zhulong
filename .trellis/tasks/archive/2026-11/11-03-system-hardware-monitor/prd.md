# 系统硬件信息与异构资源监控页面

## Goal

新增系统硬件监控功能：后端无 CGO 高能效采集 CPU、内存、磁盘、网络以及 NPU 和 SoC 芯片温度等异构资源与短期时序指标，前端在 Settings 中提供瞬时状态卡片与时序走势图。

## Requirements

### 1. 后端指标采集与 API
1. **系统与基础硬件指标（Linux procfs / sysfs）**：
   - **主机基本信息**：主机名 (hostname)、内核版本 (Kernel)、系统架构 (arch, e.g. aarch64/x86_64)、启动运行时长 (Uptime)、系统负载 (Load Average 1m/5m/15m)。
   - **CPU**：总体利用率 (%)、每核独立利用率 (%)、核心数、型号、实时核心温度。
   - **内存 (RAM)**：总量 (Total)、已用 (Used)、可用 (Available)、缓存 (Buffers+Cached)、使用率 (%)；Swap 交换空间总量与使用率。
   - **存储 (Storage)**：各主要真实挂载点（根目录 `/` 及数据挂载路径）、文件系统类型、总容量、已用、可用、使用率。
   - **网络 (Network)**：主要网络接口（eth0/enp*/wlan* 等）实时接收/发送速率 (Bytes/s)、累计总字节数、接口 UP/DOWN 状态。
2. **边缘异构资源（NPU 与 SoC 热区）**：
   - **NPU 利用率**：针对 RK3568/RK3588 (RKNPU) 及其他常见边缘加速器，采集 NPU 当前负载百分比与核心负载；在无 NPU 节点上平滑降级（`supported: false`）。
   - **SoC 与热区温度 (Thermal Zones)**：解析 `/sys/class/thermal/thermal_zone*`，获取 CPU、NPU、GPU、SoC 各测温点的标签与摄氏度。
3. **短期历史走势（Ring Buffer）**：
   - 后端内存维护轻量环形缓冲区（采样间隔 2 秒，保留最近 60~90 个时间点，约 2~3 分钟）。
   - 在采集当前硬件指标的同时自动滚动保存时序快照。
4. **API 规范与跨平台安全**：
   - 端点：`GET /api/v1/system/hardware`，遵循 `{ code: "OK", message: "success", data: SystemHardwareResponse }` 信封规范。
   - 权限：属于受保护路由组（`protected_routes`，需鉴权 Cookie/Session）。
   - 跨平台兼容：在非 Linux 平台（macOS/Windows 开发调试环境）提供无崩溃的 fallback 桩数据。

### 2. 前端展示与交互
1. **设置中心集成**：
   - 位于 `Settings` 模块中，路径为 `/settings/system`。
   - Settings 侧边栏及全局 Sidebar 增加「系统状态 / System」导航项。
2. **瞬时仪表卡片（Metric Cards）**：
   - 头部概览：CPU 使用率卡片（含各核展开/概览）、内存使用率卡片、NPU 算力负载卡片（或无加速器说明）、实时网络下行/上行速率卡片。
   - 状态徽标与色彩：语义化配色（正常 OKLCH 绿/蓝，超过 80% 警告橙，超过 90% 告警红）。
3. **时序走势图（Trend Charts）**：
   - 基于 `recharts` 渲染近 2~3 分钟的平滑折线/面积图：
     - CPU 与 NPU 负载走势（双曲线对比）。
     - 内存使用率走势。
     - 网络吞吐走势（Rx / Tx 下行/上行双色图）。
     - 芯片温度走势。
4. **详细硬件列表（Detail Tables / Collapsible）**：
   - 存储卷分区表格（挂载路径、FSType、总量、已用、余量、进度条）。
   - 网卡吞吐列表（网卡名称、状态、实时速率、累计流量）。
   - 温度传感器列表（各 thermal zone）。
5. **用户体验与多语言**：
   - 采用 TanStack Query 短轮询（页面前台时 2.5s 自动刷新，切后台自动停止）。
   - 支持手动一键刷新。
   - 完整的中（简/繁）、英三语国际化翻译。

## Acceptance Criteria

- [x] 后端：`GET /api/v1/system/hardware` 能够正确返回 CPU、内存、存储、网络、NPU、温度和 history 序列。
- [x] 后端：在 Linux 真实读取 procfs/sysfs，在 Darwin/Windows 优雅 fallback，无 Panic，高并发读取安全（读写锁保护）。
- [x] 后端：单元测试覆盖率完备（解析逻辑、RingBuffer、Handler、Fallback），通过 `go test -race`。
- [x] 后端：接入 Uber Fx，在 Makefile 中注册 `api-docs` 白名单，生成 Swagger 文档。
- [x] 前端：新增 `/settings/system` 页面，挂入 TanStack Router 和 Settings 侧边栏。
- [x] 前端：使用 recharts 渲染平滑趋势图，提供 CPU/Memory/NPU/Network/Storage/Temperature 瞬时卡片与明细。
- [x] 前端：支持中简、中繁、英文三种语言。
- [x] 门禁：通过 `make go-check`, `make api-docs`, `make pkg-drift-check`, `make native-test` 以及前端 `pnpm build` 与 `lint` 校验。
