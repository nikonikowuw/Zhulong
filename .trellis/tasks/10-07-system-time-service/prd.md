# 边缘异构系统对时服务与硬件时钟同步 (System Time Service)

## Goal

为面向极端边缘异构 Linux 设备（如 RK3588、NVIDIA Jetson、华为昇腾及通用工控机）的烛龙（Zhulong）单应用服务，提供工业级宿主机对时与系统时钟管理能力。
提供 Go 原生轻量 SNTP 客户端、两阶段时钟跳变安全状态机（保护 RTSP 流媒体 PTS/DTS 连续性与 SQLite 审计日志单调性）、硬件 RTC 双向同步与开机断网自愈、全系统时区物理联动以及 Web 实时状态监控看板，保障设备在冷启动、弱网、局域网隔离或断电重启等极端环境下时间高可用。

## Scope

本次任务采用**全链路端到端交付（前后端 + 跨平台驱动适配）**：
1. **Go 后端与驱动适配 (`internal/systemtime`)**：
   - **跨平台 `ClockDriver` 驱动抽象**：声明 `SetRealtime`（Step）、`AdjTime`（Slew）、`ReadRTC`、`WriteRTC`、`ApplyTimezone`；Linux 环境下通过原生系统调用（`SYS_CLOCK_SETTIME`、`SYS_ADJTIMEX`、`ioctl(RTC_*)`）与 `/etc/localtime` 原子软链接实现；Darwin/Windows 环境提供 Mock Stub 驱动，保证非 Linux 开发机与 CI 测试 100% 可编译、可运行、可自测。
   - **Go 原生 SNTP 客户端引擎**：纯 Go 实现 RFC 4330 协议，收发 NTP 报文并高精度计算 RTT、Offset、Delay 与 Jitter；支持 1~3 个主备候选 NTP 服务器顺序降级探测与指数退避；可配置 5m~24h 轮询周期（默认 15m）。
   - **两阶段时钟调整安全状态机**：
     - *冷启动/未同步阶段*：解除 Panic 门限，允许任意大偏差无条件 Step（`clock_settime`）一步到位拉回现代；
     - *稳态运行阶段*：微差（$|Offset| < 500\text{ms}$）使用 `adjtimex` 平滑 Slew（不倒流时间，保证视频帧时间戳连续）；大偏差（$|Offset| \ge 10\text{min}$）连续 3 次复核防上游假源暴冲。
   - **硬件 RTC 闭环与开机自愈**：硬件 RTC 芯片（`/dev/rtc*`）内部严格存储 UTC 时间；启动阶段若检测到系统时间落后且 RTC 有效则自动拉齐系统时间；每次校时成功自动回写 RTC。
   - **全系统时区物理联动**：支持标准 IANA 时区（默认 `Asia/Shanghai`）；原子更新 `/etc/localtime` 软链接与 `/etc/timezone`，调用 `tzset()` 刷新底层 C 运行时并热重载 Go 全局 Location。
   - **持久化与 Fx 生命周期**：新增数据库版本化迁移脚本持久化时间配置；集成 Uber Fx 并在 `OnStart` 执行自检与 Worker 启动，`OnStop` 优雅注销。
   - **RESTful API 与审计日志联动**：提供查询当前时间/状态、修改配置、立即对时、一键同步客户端时间接口，遵循 `{code: "OK", message, data}` 契约；关键调时与时区变更联动记录安全审计日志。
2. **React 前端交互 (`web/src/features/systemSettings` / `systemTime`)**：
   - **信息架构集成**：在「系统设置（`#settings`）」下挂载二级 Tab「时间设置」。
   - **实时时钟与状态看板**：展示设备本地时间（平滑走秒）、对时状态徽标（🟢 已同步 / 🟡 同步中 / 🔴 失败，展示 Offset、RTT、最后同步时间、同步源 Server、RTC 芯片状态）。
   - **双模式配置**：支持 NTP 自动对时（候选服务器增删排优先级、周期选择、立即对时）与手动对时（日期时间选择器 + **「一键同步浏览器时间」**）。
   - **时区选择器**：支持常用置顶与全量搜索的 IANA 时区下拉选择。
   - **权限预检**：管理员身份校验，以及后台 `CAP_SYS_TIME` 权限缺失时的告警横幅提示。
   - **多语言国际化**：完善 `en`、`zh-Hans`、`zh-Hant` 语言包。

## Target Scenarios

1. **工程人员直连初调（离线首启）**：售后工程师笔记本电脑通过网线直连无外网边缘盒子，在 Web 界面点击「同步浏览器时间」，系统瞬间与 PC 时间对齐并固化至板载 RTC。
2. **断电重启与弱网自愈**：设备断电后冷启动且现场无网络，Go 服务在 `OnStart` 阶段从板载 RTC 芯片读取 UTC 时间自愈系统内核时钟，保障业务录像与事件审计时序正常。
3. **连续流媒体运行平滑微调**：设备在进行 7x24 小时高负荷 RTSP 拉流与 NPU 视频推理时，NTP 日常毫秒级微小漂移走内核 `adjtimex` 渐进追赶，时钟单调向前，杜绝 PTS/DTS 倒拨导致录像切片损坏或播放器卡死。
4. **假时钟源与网络劫持防暴冲**：稳态运行中局域网异常 NTP 服务发回离谱错误时间（偏差超 10 分钟），系统状态机拒绝盲目 Step 跳变，进行连续采样复核并上报异常，防爆系统日志。

## Functional Requirements

### 1. 跨平台 ClockDriver 驱动层
- 抽象统一接口：
  - `GetSystemTime() (time.Time, error)`
  - `SetRealtime(t time.Time) error`（Step 调整）
  - `AdjTime(offset time.Duration) error`（Slew 平滑调整）
  - `ReadRTC() (time.Time, error)`
  - `WriteRTC(t time.Time) error`
  - `GetTimezone() (string, error)`
  - `ApplyTimezone(iana string) error`
  - `CheckPermissions() bool`（检查是否存在 `CAP_SYS_TIME` 或 root 权限）
- Linux 驱动 (`driver_linux.go`)：
  - 调用 `unix.ClockSettime(unix.CLOCK_REALTIME, &ts)`；
  - 调用 `unix.ClockAdjtime` / `unix.Adjtimex` 设置频偏微调；
  - 打开 `/dev/rtc0`（或首个可用 `/dev/rtc*`），通过 `ioctl` 执行 `RTC_RD_TIME` 与 `RTC_SET_TIME`；
  - 修改 `/etc/localtime` 软链接并通过 CGO/系统接口触发 `tzset()` 刷新。
- 开发机与测试桩 (`driver_stub.go`)：
  - 针对 `!linux` 编译环境提供内存模拟实现，支持精确断言调时指令，确保 macOS/CI 正常运行。

### 2. Go 原生轻量 SNTP 客户端
- 实现 RFC 4330 SNTPv4 报文解析与封包（48 字节 UDP 报文）；
- 发送 Client 报文（Mode=3, Version=4），读取 Transmit Timestamp；
- 接收 Server 报文，校验 Stratum（1~15）与 Leap Indicator；
- 精确计算：
  - $\text{Delay} = (T_4 - T_1) - (T_3 - T_2)$
  - $\text{Offset} = \frac{(T_2 - T_1) + (T_3 - T_4)}{2}$
- 支持配置 1~3 个 NTP 服务器，默认包含主流公共源（如 `ntp.aliyun.com`, `cn.pool.ntp.org`）；
- 顺序降级探测：主服务器超时（3s）自动降级至备用服务器，全失败后进入指数退避（30s, 60s, 120s... 最大 15m）。

### 3. 两阶段时钟安全状态机
- 维护状态标识：`sync_state`（`unsynced` | `syncing` | `synchronized` | `panic_review` | `failed`）。
- **冷启动判定**：系统当前时间早于编译基准时间（Build Timestamp，如 2026 年）或尚未完成首次有效对时；在此状态下接收到有效时间后，直接 Step 阶跃并转换为 `synchronized`。
- **稳态调整策略**：
  - $|Offset| < 500\text{ms}$：调用 `AdjTime` 平滑微调（Slew）；
  - $500\text{ms} \le |Offset| < 10\text{min}$：调用 `SetRealtime` 阶跃，并触发内部时钟突变通知；
  - $|Offset| \ge 10\text{min}$：进入 `panic_review`，不立即应用，启动连续 3 次探测复核；若 3 次均一致且用户未干预，发出高危时钟告警。
- 手动对时与浏览器对时属于显式人工意志，不受 Panic 门限限制，直接 Step 应用并同步 RTC。

### 4. 硬件 RTC 双向同步与开机自愈
- RTC 芯片内部强制以 UTC 格式存储 `struct rtc_time`（年月日时分秒）。
- `app.OnStart` 开机探测：
  - 尝试读取 `/dev/rtc*`；
  - 若系统时间早于 2026-01-01 且 RTC 时间合法有效（晚于 2026-01-01），自动将 RTC 时间同步至系统时钟；
  - 记录自愈审计日志与系统日志。
- 无论通过 NTP 还是手动/浏览器校时，只要系统时间更新成功，异步/同步触发一次 `WriteRTC` 将新时间固化至硬件。

### 5. 全系统时区管理
- 支持合法 IANA 时区标识符校验（如 `Asia/Shanghai`, `UTC`, `America/New_York`）。
- 校验 `/usr/share/zoneinfo/<zone>` 对应文件是否存在；
- 原子替换 `/etc/localtime` 软链接并覆盖 `/etc/timezone`；
- 刷新 Go 进程全局 `time.Local` 变量，并刷新 C 运行时时区缓存；
- 默认出厂预设时区为 `Asia/Shanghai`。

### 6. 数据持久化与 Fx 生命周期集成
- 新增 SQLite 数据库版本化迁移：创建 `system_time_configs` 表，持久化配置（模式 `ntp`/`manual`、NTP 服务器列表 JSON、同步间隔、时区）。
- 在 Uber Fx 模块中声明：
  - `OnStart`：加载持久化配置、执行权限探测、执行 RTC 开机自检比对、应用时区、根据配置启动后台 SNTP Worker。
  - `OnStop`：通知后台 Worker 优雅退出并等待结束。

### 7. RESTful API 契约
- 所有响应遵循统一结构 `{ "code": "OK", "message": "...", "data": ... }`。
- 接口列表：
  - `GET /api/v1/system/time`：获取当前设备时间、时区、对时模式、NTP 候选池、同步周期、同步状态详情（Offset、RTT、最后同步时间、RTC 状态、权限状态）。
  - `PUT /api/v1/system/time/config`：更新对时配置（模式、NTP 服务器列表、同步周期、时区）。
  - `POST /api/v1/system/time/sync`：立即触发一次 NTP 探测与对时（若在 NTP 模式下）。
  - `POST /api/v1/system/time/manual`：手动设置时间或同步浏览器时间（接收 ISO-8601 或 Unix 时间戳）。
- 关键操作埋点至 `internal/audit` 审计日志（`system.time.update_config`, `system.time.manual_set`, `system.time.ntp_sync`）。

### 8. React 前端 Web 控制台
- 挂载路径：系统设置一级菜单（`#settings`）下的二级导航「时间设置」（Time Settings）。
- 状态看板卡片：实时走秒时钟、设备时区、同步状态标签、当前偏差 Offset、最后对时源与时间、硬件 RTC 状态指示灯。
- 模式与表单交互：
  - NTP 模式：动态表单管理 1~3 个 NTP 服务器地址，同步周期下拉选择（5m/15m/30m/1h/24h），「立即对时」按钮。
  - 手动模式：日期时间选择器，**「同步浏览器时间」高亮快捷按钮**（点击读取本地 `new Date().toISOString()` 发送），保存应用按钮。
  - 时区区域：常用时区置顶 + 可搜索 IANA 下拉选择框。
- 权限降级：检测到当前用户非管理员或后端报 `PERMISSION_DENIED` 时，置灰操作并展示提示条。
- 国际化：完善中英繁三语字典。

## Acceptance Criteria

- [x] **跨平台编译与自测**：在 macOS 开发机与 Linux 目标机上 `go test ./internal/systemtime/...` 均能 100% 通过，无 CGO/Linux 特有系统调用编译阻断。
- [x] **SNTP 报文解析与精度**：针对标准 NTP 报文能够正确解析出 4 个时间戳，计算 Offset 与 Delay 误差在微秒级；单次请求超时能优雅处理。
- [x] **两阶段状态机切换**：
  - 编写单元测试验证冷启动状态下 $|Offset| > 1\text{年}$ 能直接触发 Step 调整；
  - 稳态阶段验证 $|Offset| < 500\text{ms}$ 触发 Slew（`AdjTime`），不倒退时间；$|Offset| \ge 10\text{min}$ 触发连续采样与防暴冲报警。
- [x] **RTC 开机自愈与回写**：在模拟 RTC 晚于系统时间的场景下，启动时正确拉齐系统时间；手动或 NTP 成功对时后正确触发 RTC 回写。
- [x] **全系统时区联动**：切换时区后，后端返回的时间字符串偏移量与本地时间展示即时变更，Go `time.Local` 成功刷新。
- [x] **持久化与 Fx 生命周期**：重启服务后配置不丢失，后台 Worker 正常启停无 goroutine 泄漏。
- [x] **API 规范与审计日志**：所有接口遵循 `{code, message, data}` 契约，成功记录操作审计日志；Swaggo 2.0 文档生成完备。
- [x] **前端看板与浏览器一键对时**：在 Web 界面能实时观察设备时间走秒与同步状态；点击「同步浏览器时间」后设备时间精确变更为当前浏览器时间。
- [x] **全项目质量检查**：后端 `go test -v -race ./...` 与前端 `pnpm test` / `pnpm lint` / `pnpm build` 全绿通过。
