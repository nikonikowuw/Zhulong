# 边缘异构系统媒体存储配置与动态生命周期管理 · 技术架构设计 (Technical Design)

## 1. 架构定位与系统边界

媒体存储配置与生命周期管理模块（`internal/storage`）属于烛龙（Zhulong）宿主系统的基础设施存储子系统，向下只读对接 Linux VFS / `statfs` 系统调用与 POSIX 文件接口，向内与视频录制（`recording`）、AI 推理抓拍（`inference`）等媒体模块通过 Go 接口提供写入配额判决（`CanWrite()`）与防穿透保护，向上通过 Gin 暴露受保护的 RESTful API，最终由 React 前端「系统设置 - 存储设置」呈现。

```txt
┌──────────────────────────────────────────────────────────────────┐
│              React 前端界面 (Web SPA - #settings)                 │
│   StorageCapacityBar (容量进度条) ⇄ StorageConfigForm ⇄ TestPath │
└─────────────────────────────────▲────────────────────────────────┘
                                  │ HTTP API (REST / JSON Envelope)
┌─────────────────────────────────▼────────────────────────────────┐
│                   Go 业务层 (internal/storage)                    │
│  ┌───────────────────────┐      ┌─────────────────────────────┐  │
│  │    StorageService     │◄────►│        CleanerEngine        │  │
│  └───────────┬───────────┘      │ (双水位回差 + 批次流控避让) │  │
│              │                  └──────────────┬──────────────┘  │
│              ├─────────────────────────────────┤                 │
│              ▼                                 ▼                 │
│      PathInspector (探针)             EmergencyGate (熔断器)     │
│  (statfs 遥测 / 设备号防穿透)         (CanWrite 判决 / 停录保护) │
│              │                                 │                 │
│              ▼                                 ▼                 │
│     StorageStore (SQLite)               系统审计日志与事件通知   │
└─────────────────────────────────▲────────────────────────────────┘
                                  │ Linux POSIX / sys_statfs / VFS
┌─────────────────────────────────▼────────────────────────────────┐
│               Linux 内核与存储挂载点 (Host Filesystem)            │
│    / (根分区 eMMC/SD卡)    /mnt/storage (外挂 NVMe/SATA 媒体盘)   │
│    ├── recordings/{camera_id}/{date}/{time}.mp4                  │
│    ├── snapshots/{camera_id}/{date}/{event}.jpg                  │
│    └── exports/{task_id}.mp4                                     │
└──────────────────────────────────────────────────────────────────┘
```

---

## 2. 后端核心抽象与领域模型 (`internal/storage`)

### 2.1 目录组织
```txt
internal/storage/
├── model.go             # 领域模型定义（存储状态、配置负载、指标结构）
├── store.go             # 存储策略持久化接口与 GORM 实现（system_metadata）
├── inspector.go         # PathInspector 探针抽象接口
├── inspector_linux.go   # 基于 unix.Statfs / unix.Stat 的 Linux 探针实现
├── inspector_fallback.go# 非 Linux 环境编译兼容桩 (Darwin/Windows)
├── cleaner.go           # CleanerEngine 双水位回差与步进批次限速调度器
├── gate.go              # EmergencyGate 极限容量熔断器与 CanWrite 判决
├── service.go           # 核心业务编排（路径热切流、配置校验、手动清理协调）
├── handler.go           # Gin 路由处理与 Swagger 2.0 注解
├── handler_test.go      # HTTP 接口单元测试与契约测试
├── cleaner_test.go      # 双水位清理与流控避让单元测试
└── module.go            # Uber Fx 模块注册与生命周期钩子
```

### 2.2 数据模型定义 (`model.go`)
```go
package storage

import "time"

// StorageHealthStatus 描述存储当前健康状态
type StorageHealthStatus string

const (
    StatusHealthy          StorageHealthStatus = "healthy"           // 空间充裕，读写正常
    StatusWarning          StorageHealthStatus = "warning"           // 触碰高水位警戒线
    StatusCleaning         StorageHealthStatus = "cleaning"          // 正在执行后台清理
    StatusEmergencyStopped StorageHealthStatus = "emergency_stopped" // 触碰极限硬红线，停录熔断
    StatusError            StorageHealthStatus = "error"             // 路径不可用或外挂盘掉线
)

// StorageConfig 存储策略持久化配置
type StorageConfig struct {
    MediaDirectory          string `json:"media_directory"`           // 媒体存储根目录，如 "/mnt/storage/media"
    RecordingsRetentionDays int    `json:"recordings_retention_days"` // 录像保留天数 (1~365)
    SnapshotsRetentionDays  int    `json:"snapshots_retention_days"`  // 抓拍保留天数 (1~730)
    ExportsRetentionHours   int    `json:"exports_retention_hours"`   // 导出文件保留小时 (1~168)
    HighWatermarkPercent    int    `json:"high_watermark_percent"`    // 高水位清理阈值 (50~95)
    LowWatermarkPercent     int    `json:"low_watermark_percent"`     // 低水位休眠阈值 (40~85)
    EmergencyStopPercent    int    `json:"emergency_stop_percent"`    // 紧急停录红线使用率 (90~99)
    EmergencyStopMinMB      int64  `json:"emergency_stop_min_mb"`     // 紧急停录最小空闲空间 (MB)
}

// StorageUsageBreakdown 细分文件占用大小统计 (字节)
type StorageUsageBreakdown struct {
    RecordingsBytes int64 `json:"recordings_bytes"` // 录像文件总字节数
    SnapshotsBytes  int64 `json:"snapshots_bytes"`  // 抓拍文件总字节数
    ExportsBytes    int64 `json:"exports_bytes"`    // 导出文件总字节数
    OtherBytes      int64 `json:"other_bytes"`      // 所在分区的其它文件占用
}

// StorageStatus 存储实时遥测大盘
type StorageStatus struct {
    MediaDirectory string                `json:"media_directory"` // 当前生效路径
    MountPoint     string                `json:"mount_point"`     // 所在挂载点路径 (如 "/mnt/storage")
    FSType         string                `json:"fs_type"`         // 文件系统类型 (如 "ext4", "xfs")
    TotalBytes     uint64                `json:"total_bytes"`     // 磁盘总容量
    UsedBytes      uint64                `json:"used_bytes"`      // 磁盘已用空间
    FreeBytes      uint64                `json:"free_bytes"`      // 磁盘可用空间
    UsagePercent   int                   `json:"usage_percent"`   // 当前使用率百分比 (0~100)
    Breakdown      StorageUsageBreakdown `json:"breakdown"`       // 细分目录占用
    Status         StorageHealthStatus   `json:"status"`          // 健康状态
    DeviceID       uint64                `json:"device_id"`       // 当前绑定的设备号 (防穿透比对)
    IsExternal     bool                  `json:"is_external"`     // 是否为独立于根分区的独立挂载点
    CanWrite       bool                  `json:"can_write"`       // 当前是否允许写入新视频
    UpdatedAt      time.Time             `json:"updated_at"`      // 指标更新时间
}

// PathTestRequest 路径测试请求负载
type PathTestRequest struct {
    Path string `json:"path" binding:"required"`
}

// PathTestResponse 路径测试结果
type PathTestResponse struct {
    Path        string `json:"path"`
    Exists      bool   `json:"exists"`
    Writable    bool   `json:"writable"`
    MountPoint  string `json:"mount_point"`
    FSType      string `json:"fs_type"`
    TotalBytes  uint64 `json:"total_bytes"`
    FreeBytes   uint64 `json:"free_bytes"`
    IsRootFS    bool   `json:"is_root_fs"`    // 是否直接位于根文件系统 / (提示风险)
    IsDedicated bool   `json:"is_dedicated"`  // 是否属于独立外挂盘
    DeviceID    uint64 `json:"device_id"`
}

// CleanupSummary 清理执行结果汇报
type CleanupSummary struct {
    TriggerReason string    `json:"trigger_reason"` // "watermark", "scheduled", "manual"
    DeletedFiles  int       `json:"deleted_files"`
    FreedBytes    int64     `json:"freed_bytes"`
    DurationMs    int64     `json:"duration_ms"`
    TargetStatus  string    `json:"target_status"`
    FinishedAt    time.Time `json:"finished_at"`
}
```

---

## 3. 核心机制设计

### 3.1 路径防穿透与挂载探针 (`PathInspector`)
- **Linux 系统调用封装**：
  - `unix.Statfs(path, &statfs)` 采集 `Bsize`, `Blocks`, `Bfree`, `Bavail`；
  - `unix.Stat(path, &stat)` 采集 `st_dev`（设备号）；
  - 根目录 `/` 的 `st_dev` 作为基准根设备 ID；
- **防穿透比对逻辑**：
  - 若配置的媒体路径 `st_dev != root_st_dev`，说明该路径是一个独立外挂盘（如 NVMe/SATA 分区挂载在 `/mnt/storage`），系统将记录该专属 `DeviceID`；
  - 运行时周期检测，若发现该路径的 `st_dev == root_st_dev`，判定外挂盘已脱机，原本挂载点已裸露为根文件系统的普通目录。系统立即判定为“掉盘穿透风险”，锁定写操作，向 `EmergencyGate` 汇报故障，绝不向根文件系统写入。

### 3.2 双水位回差与步进限速清理器 (`CleanerEngine`)
- **双水位滞后回差算法 (Hysteresis)**：
  - 当 `UsagePercent >= HighWatermarkPercent` 时激活清理；
  - 清理按优先级持续淘汰旧文件，直到 `UsagePercent <= LowWatermarkPercent`，协程进入休眠；
  - 此设计在 90% 到 80% 之间留出高达 10% 的缓冲带，彻底消灭“刚降 0.1% 又触发”的磁盘 IO 颠簸。
- **阶梯式三级淘汰 (Priority Cascade)**：
  1. `exports/` 目录中创建时间超过 `ExportsRetentionHours` 的临时切片（全量清理）；
  2. `recordings/` 目录中未标记 `is_locked` 的常规全天录像文件，按文件修改时间排序，由远及近 FIFO 删除；
  3. 严格保护带 AI 告警事件标记或被用户锁定的录像和抓拍，直到未锁定录像排空仍空间不足时，触发停录告警，绝不破坏核心证据。
- **批量步进流控 (Throttled Batch Deletion)**：
  ```go
  const batchSize = 30
  const sleepBetweenBatches = 50 * time.Millisecond

  for _, file := range candidates {
      if err := os.Remove(file.Path); err == nil {
          deletedCount++
          freedBytes += file.Size
      }
      if deletedCount % batchSize == 0 {
          // 礼貌休眠，释放 ext4 jbd2 日志锁与磁盘 IOPS 队列
          time.Sleep(sleepBetweenBatches)
          // 检查是否已经落回低水位以下
          if isBelowLowWatermark() {
              break
          }
      }
  }
  ```

### 3.3 极限容量熔断断尾保护 (`EmergencyGate`)
- 录像引擎与视频写盘协程在创建新切片前调用 `storage.CanWrite()`；
- 若 `UsagePercent >= EmergencyStopPercent` 或 `FreeBytes < EmergencyStopMinMB * 1024 * 1024`：
  - `CanWrite()` 立即返回 `false`；
  - 状态切换为 `emergency_stopped`；
  - 向系统审计日志记录一条严重事件（`STORAGE_EMERGENCY_STOP`），并向前端 SSE/WebSocket 推送存储熔断告警；
- 避免突发磁盘 100% 满导致 SQLite WAL 写入崩溃、进程 Panic。

### 3.4 热切换与平滑淘汰生命周期
- 用户修改 `media_directory` 保存后：
  1. 新路径通过合法性与可写测试；
  2. 运行时切流指针原子更新为新路径，新产生的切片写入新目录；
  3. 历史索引保持原路径访问；清理协程在淘汰早期录像时，自然遍历并优先删除旧目录里的切片；
  4. 绝不触发全量后台文件复制。

---

## 4. RESTful API 契约设计

| 方法 | 路径 | 鉴权 | 描述 | 成功响应 |
| --- | --- | --- | --- | --- |
| `GET` | `/api/v1/system/storage/status` | 需登录 | 获取存储实时遥测大盘（容量、占用细分、健康状态） | `200 OK` + `StorageStatus` |
| `GET` | `/api/v1/system/storage/config` | 需登录 | 获取当前存储与清理策略配置 | `200 OK` + `StorageConfig` |
| `PUT` | `/api/v1/system/storage/config` | 需登录 | 修改存储策略与路径（带参数预检与热生效） | `200 OK` + `StorageConfig` |
| `POST` | `/api/v1/system/storage/test` | 需登录 | 预检测试指定候选存储目录有效性与外挂属性 | `200 OK` + `PathTestResponse` |
| `POST` | `/api/v1/system/storage/cleanup`| 需登录 | 手动立即触发一轮空间清理 | `200 OK` + `CleanupSummary` |

---

## 5. 前端界面架构与交互设计 (`web/src/features/systemSettings`)

### 5.1 组件结构
```txt
web/src/features/systemSettings/
├── components/
│   ├── SettingsLayout.tsx          # 系统设置统一框架与二级侧边栏
│   ├── StorageSettingsPage.tsx     # 存储设置主容器页面
│   ├── StorageCapacityBar.tsx      # 多色分段容量进度条与指标徽章
│   ├── StorageConfigForm.tsx       # 核心表单与高级折叠面板 (Accordion)
│   ├── StoragePathTestBadge.tsx    # 路径合法性/独立外挂盘检测反馈标签
│   └── ManualCleanupDialog.tsx     # 手动清理确认与进度弹窗
├── api/
│   └── storageApi.ts               # 基于 apiClient 的 RESTful 请求封装
├── hooks/
│   ├── useStorageStatus.ts         # 容量遥测轮询 Hook (TanStack Query)
│   ├── useStorageConfig.ts         # 配置读取与保存 Mutation
│   └── useStorageActions.ts        # 路径测试与手动清理 Mutation
├── types/
│   └── storage.ts                  # TypeScript 类型定义
└── locales/
    ├── en.json
    ├── zh-Hans.json
    └── zh-Hant.json
```

### 5.2 交互状态机与防呆
- **路径测试联动**：输入路径后，提供「测试路径」按钮。点击后调用 `/test` 接口，若为根分区 `/` 给出温和警示标签（“当前路径位于系统根盘，建议使用独立外挂磁盘”）；若属于独立外挂分区，显示绿色“已挂载独立外挂盘 (ext4)”；若不可写显示红色错误提示；
- **水位防呆校验**：前端 Form 校验强制约束 `low_watermark_percent < high_watermark_percent < emergency_stop_percent`，否则禁用保存按钮并高亮提示；
- **状态徽标**：`emergency_stopped` 时显示红色脉冲徽标与警示横幅，明确告知用户录像已保护性暂停。

---

## 6. 跨层防爆门与非 Linux 编译兼容

1. **跨层编译隔离**：
   - Linux 生产环境依赖 `golang.org/x/sys/unix` 中的 `unix.Statfs_t` 与 `unix.Stat_t`；
   - 开发机（macOS / Windows）采用 `inspector_fallback.go` 编译标签（`!linux`），通过标准库模拟测试数据，保证 `go test ./...` 和本地前端联调随时顺畅。
2. **CGO / 引擎联动约束**：
   - 存储模块完全基于纯 Go 实现，不引入多余 CGO 负担；
   - 视频写盘引擎（C++ 引擎与 Go 宿主）只读取 `CanWrite()` 原子布尔标志位，零锁争用。
