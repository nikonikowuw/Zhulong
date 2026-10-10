# 技术设计：边缘异构系统对时服务与硬件时钟同步 (Technical Design)

## 1. 架构总览与分层设计

本项目面向极简/异构边缘 Linux 智能设备，对时服务核心架构划分为五层：

```
[前端 React 控制台 (web/src/features/systemSettings)]
   │  ▲
   │  │ RESTful HTTP (TanStack Query + Zod 校验)
   ▼  │
[Gin API 层 (/api/v1/system/time/*)] ── RequireAuth (仅管理员可写)
   │
[TimeService 业务编排 (internal/systemtime)]
   ├── 两阶段状态机 (State Machine: Initial Step vs Steady Slew)
   ├── NTP 轮询 Worker (SNTP Client Pool + 指数退避)
   ├── 审计日志联动 (Audit Service 埋点)
   └── 配置持久化仓库 (GORM / SQLite `system_time_configs`)
   │
[ClockDriver 跨平台硬件与内核适配层]
   ├── driver_linux.go (Linux 原生系统调用 / ioctl / 软链接)
   └── driver_stub.go  (!linux 开发机 / CI Mock 内存驱动)
   │
[底层物理资源]
   ├── Linux Kernel CLOCK_REALTIME (clock_settime / adjtimex)
   ├── 硬件 RTC 芯片 (/dev/rtc0, ioctl RTC_RD_TIME / RTC_SET_TIME)
   └── 系统时区文件 (/etc/localtime, /etc/timezone, tzset)
```

---

## 2. 领域数据模型与驱动抽象 (Driver Layer)

### 2.1 跨平台 `ClockDriver` 接口
```go
package systemtime

import "time"

// RtcStatus 描述硬件 RTC 状态
type RtcStatus string

const (
    RtcStatusNormal  RtcStatus = "normal"
    RtcStatusMissing RtcStatus = "missing"
    RtcStatusError   RtcStatus = "error"
)

// ClockDriver 抽象底层时钟与时区操作
type ClockDriver interface {
    // GetSystemTime 获取当前系统内核时间
    GetSystemTime() (time.Time, error)
    // SetRealtime 以阶跃（Step）方式强制设置系统时间（clock_settime）
    SetRealtime(t time.Time) error
    // AdjTime 以微调（Slew）方式渐进调整系统时钟（adjtimex）
    AdjTime(offset time.Duration) error
    // ReadRTC 从硬件 RTC 芯片读取 UTC 时间
    ReadRTC() (time.Time, error)
    // WriteRTC 将指定 UTC 时间写入硬件 RTC 芯片
    WriteRTC(t time.Time) error
    // GetRTCStatus 探测硬件 RTC 芯片是否存在且可用
    GetRTCStatus() RtcStatus
    // GetTimezone 获取当前系统生效的 IANA 时区名称（如 Asia/Shanghai）
    GetTimezone() (string, error)
    // ApplyTimezone 原子设置系统时区并热刷新运行时缓存
    ApplyTimezone(iana string) error
    // HasClockPermission 检查当前进程是否具有系统时钟修改权限（CAP_SYS_TIME 或 root）
    HasClockPermission() bool
}
```

### 2.2 Linux 原生实现 (`driver_linux.go`)
1. **Step 跳变**：
   ```go
   ts := unix.NsecToTimespec(t.UnixNano())
   err := unix.ClockSettime(unix.CLOCK_REALTIME, &ts)
   ```
2. **Slew 微调**：
   封装 `unix.Adjtimex`，构造 `unix.Timex` 结构体，配置 `MOD_OFFSET` 或利用 `unix.ClockAdjtime`，平滑消除微秒/毫秒级偏移，确保时钟不回退。
3. **RTC 操作**：
   打开 `/dev/rtc0`（若不存在则尝试 `/dev/rtc`）。
   - 读：`unix.IoctlGetRTCTime(fd, &rtcTime)`，将 `rtc_time`（年-1900、月-1、日、时、分、秒）转换为 UTC `time.Time`。
   - 写：构造 `unix.RTCTime`，执行 `unix.IoctlSetRTCTime(fd, &rtcTime)`。
4. **时区修改与运行时热刷新**：
   - 检查 `/usr/share/zoneinfo/<iana>` 是否存在；
   - 创建临时软链接并原子 `os.Rename` 替换 `/etc/localtime`；
   - 覆写 `/etc/timezone` 包含时区字符串；
   - 设置环境变量 `os.Setenv("TZ", ":/etc/localtime")`；
   - 重载 Go 全局 `time.Local = loc`；
   - 通过 C 运行时绑定调用 `tzset()` 刷新 libc 时区缓存。

### 2.3 开发机与测试桩 (`driver_stub.go`)
针对 `//go:build !linux` 环境提供内存驱动：
- 内存维护系统时间虚拟偏移量 `virtualOffset`、虚拟 RTC 时间 `rtcTime`、当前时区 `timezone`；
- 所有调用直接在内存中变更状态并记录操作日志；
- `HasClockPermission()` 默认返回 `true`（或可配置）；
- 允许在单元测试中任意注入故障（如模拟 RTC 缺失、模拟权限不足）。

---

## 3. Go 原生轻量 SNTP 客户端引擎 (RFC 4330)

### 3.1 NTP 报文结构
```go
type ntpPacket struct {
    Settings       uint8  // LI (2 bits), VN (3 bits), Mode (3 bits)
    Stratum        uint8  // Stratum level (1-16)
    Poll           int8   // Poll interval
    Precision      int8   // Precision
    RootDelay      uint32 // Root delay
    RootDispersion uint32 // Root dispersion
    ReferenceID    uint32 // Reference clock identifier
    RefTimeSec     uint32
    RefTimeFrac    uint32
    OrigTimeSec    uint32 // T1 发送端发送请求时间
    OrigTimeFrac   uint32
    RxTimeSec      uint32 // T2 服务端接收请求时间
    RxTimeFrac     uint32
    TxTimeSec      uint32 // T3 服务端发送响应时间
    TxTimeFrac     uint32
}
```

### 3.2 往返时延与时钟偏差计算
- NTP 纪元：1900-01-01 00:00:00 UTC（与 Unix 纪元差 2,208,988,800 秒）。
- 四个核心时间戳：
  - $T_1$：客户端发出请求时刻（本地时间）；
  - $T_2$：服务端接收到请求时刻；
  - $T_3$：服务端发出响应时刻；
  - $T_4$：客户端接收到响应时刻（本地时间）。
- 计算公式：
  $$\text{RoundTripDelay} = (T_4 - T_1) - (T_3 - T_2)$$
  $$\text{ClockOffset} = \frac{(T_2 - T_1) + (T_3 - T_4)}{2}$$

### 3.3 探测池与退避机制
- 支持用户配置多个服务器（如 `["ntp.aliyun.com", "cn.pool.ntp.org", "pool.ntp.org"]`）；
- 顺序探测：从首选源开始，单次超时设为 3 秒；若主源超时或 Stratum 非法（$\ge 16$），顺序尝试备用源；
- 若全部候选源失败，状态标记为 `failed`，并开启指数退避重试（30s -> 60s -> 120s ... 最大不超过配置的常规同步间隔）；
- 探测成功后，重置退避计时，状态更新为 `synchronized`。

---

## 4. 两阶段时钟安全状态机 (State Machine)

### 4.1 状态转移图
```
                  [服务启动]
                      │
           ┌──────────┴──────────┐
           ▼                     ▼
[未同步态 Unsynced]         [冷启动异常 (<2026年)]
  (从 RTC 自愈读取)                │
           │                     ▼
           │             [RTC 强制恢复系统时间]
           ▼                     │
[NTP 探测响应 / 手动对时] ◄──────┘
           │
           ├─── 未完成初始同步 ───► 【解除限制】无条件 Step (clock_settime) ──┐
           │                                                            │
           └─── 已完成初始同步 (稳态)                                    │
                  ├── |Offset| < 500ms ──────► Slew 平滑微调 (adjtimex)  │
                  ├── 500ms <= |Offset| < 10m ► Step 跳变 + 广播事件      │
                  └── |Offset| >= 10m ────────► 进入连续采样复核          │
                         (3次一致则告警)                                 ▼
                                                                 [写入硬件 RTC]
                                                                        │
                                                                        ▼
                                                             [稳态 Synchronized]
```

### 4.2 业务安全防护原则
- **视频 PTS/DTS 安全**：日常稳态下绝不允许倒流系统时钟。$|Offset| < 500\text{ms}$ 必须全部走 `adjtimex` 线性追赶；
- **冷启动特例**：系统时间若是初始默认值（出厂 1970 或早于固件编译构建时间戳 `BuildEpoch` 2026-01-01），判定为未同步态，无条件允许大跨度 Step；
- **防上游假源暴冲**：稳态下遇到 $\ge 10\text{min}$ 偏差，拒绝单次调整，进行连续 3 次间隔 5 秒的复核采样。

---

## 5. 数据持久化与 Fx 生命周期

### 5.1 SQLite 版本化迁移
新建文件：`internal/database/migrations/000005_create_system_time_config.up.sql`
```sql
CREATE TABLE IF NOT EXISTS system_time_configs (
    id INTEGER PRIMARY KEY CHECK (id = 1), -- 单行配置
    mode TEXT NOT NULL DEFAULT 'ntp',      -- 'ntp' | 'manual'
    ntp_servers TEXT NOT NULL DEFAULT '["ntp.aliyun.com","cn.pool.ntp.org","pool.ntp.org"]',
    sync_interval_seconds INTEGER NOT NULL DEFAULT 900, -- 默认 15 分钟
    timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    updated_at DATETIME NOT NULL
);

-- 初始化默认配置
INSERT OR IGNORE INTO system_time_configs (id, mode, ntp_servers, sync_interval_seconds, timezone, updated_at)
VALUES (1, 'ntp', '["ntp.aliyun.com","cn.pool.ntp.org","pool.ntp.org"]', 900, 'Asia/Shanghai', CURRENT_TIMESTAMP);
```

### 5.2 Fx 生命周期集成 (`internal/systemtime/module.go`)
- **`OnStart` 流程**：
  1. 权限预检：调用 `driver.HasClockPermission()` 记录日志并缓存权限状态；
  2. 开机 RTC 自检：若系统内核时间早于 `2026-01-01`，且 RTC 时间有效，执行 `SetRealtime(rtcTime)` 自愈系统时间；
  3. 加载持久化配置并应用时区：`driver.ApplyTimezone(cfg.Timezone)`；
  4. 启动后台 Worker goroutine：根据 `cfg.Mode` 在 `ntp` 模式下启动定时轮询，并在启动后 3 秒内执行一次首轮异步探测。
- **`OnStop` 流程**：
  - 发送退出信号给 Worker context，优雅等待后台协程退出。

---

## 6. RESTful API 契约与 Swaggo 规范

所有响应包装在统一契约内：
```json
{
  "code": "OK",
  "message": "success",
  "data": { ... }
}
```

### 6.1 `GET /api/v1/system/time`
获取当前时钟状态看板数据。
- **Response Data**:
```json
{
  "current_time": "2026-03-31T16:30:00+08:00",
  "timezone": "Asia/Shanghai",
  "mode": "ntp",
  "ntp_servers": ["ntp.aliyun.com", "cn.pool.ntp.org", "pool.ntp.org"],
  "sync_interval_seconds": 900,
  "sync_status": {
    "state": "synchronized",
    "last_sync_time": "2026-03-31T16:15:00+08:00",
    "last_sync_server": "ntp.aliyun.com",
    "offset_ms": 12.5,
    "rtt_ms": 34.2,
    "error_message": ""
  },
  "rtc_status": "normal",
  "has_permission": true
}
```

### 6.2 `PUT /api/v1/system/time/config`
更新时间配置（模式、NTP 服务器列表、同步间隔、时区）。
- **Request Body**:
```json
{
  "mode": "ntp",
  "ntp_servers": ["192.168.1.1", "ntp.aliyun.com"],
  "sync_interval_seconds": 1800,
  "timezone": "Asia/Shanghai"
}
```

### 6.3 `POST /api/v1/system/time/sync`
立即触发一次 NTP 对时探测（仅限当前处于 NTP 模式且非同步进行中）。

### 6.4 `POST /api/v1/system/time/manual`
手动设置系统时间或同步浏览器时间。
- **Request Body**:
```json
{
  "target_time": "2026-03-31T16:32:00.123+08:00"
}
```
后端直接将该时间转换为 UTC 并 Step 设置系统时间，同时回写板载 RTC。

---

## 7. 前端架构与交互设计 (React / WebUI)

### 7.1 模块目录结构
```
web/src/features/systemSettings/
├── index.ts
├── pages/
│   └── SystemSettingsPage.tsx       # 系统设置聚合页（含 Sub-nav Tabs）
├── time/
│   ├── api/
│   │   ├── timeApi.ts               # API 客户端封装
│   │   └── timeApi.test.ts
│   ├── components/
│   │   ├── TimeDashboardCard.tsx    # 实时走秒时钟与状态指示卡片
│   │   ├── NtpConfigForm.tsx        # NTP 模式配置表单
│   │   ├── ManualTimeForm.tsx       # 手动对时 / 浏览器一键同步表单
│   │   └── TimezoneSelect.tsx       # IANA 常用时区选择器
│   ├── hooks/
│   │   ├── useSystemTime.ts         # TanStack Query 钩子
│   │   └── useLiveClock.ts          # 平滑走秒与时间格式化钩子
│   ├── types.ts                     # TypeScript 类型定义
│   └── locales/
│       ├── en.json
│       ├── zh-Hans.json
│       └── zh-Hant.json
```

### 7.2 关键交互亮点
1. **平滑走秒时钟 (`useLiveClock`)**：从后端获取一次基准设备时间与接收时刻后，在前端利用 `requestAnimationFrame` 或每秒定时器驱动自增走秒，并按照设备选定且生效的时区进行格式化展示。
2. **「一键同步浏览器时间」按钮**：在现场调试离线设备时，该按钮高亮醒目。点击时读取 `new Date().toISOString()`，调用 `manual` API，提交成功后触发刷新提示并更新本地时间。
3. **状态标签徽章**：绿色 Pulsing 点表示 `synchronized`，黄色表示 `syncing` 或未对时，红色表示 `failed` 并在 tooltip 中展示精准错误信息（如 UDP 123 超时）。
