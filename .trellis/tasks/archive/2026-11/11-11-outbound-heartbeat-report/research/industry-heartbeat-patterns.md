# 边缘计算盒子北向存活上报行业调研与本仓映射

## 1. 行业成熟产品范式对比

### 1.1 旷视（盘古 / 鸿图 MegCube / 魔方 MegBox）
- **公开文档证据**（`pangu-docs.megvii-inc.com/225/v1/api/pubsub.html`、`device_alarm_push.html`）：
  - 面向多个第三方业务平台时，采用**统一订阅目标（PubSub）+ 业务类型勾选**模型（`POST /v1/api/pubsub/busi/record/add`）。
  - 接入方注册一次 `pushUrl` 与 `appKey`，通过 `busiTypes` 数组按需订阅：`1`-人员管控、`2`-车辆管控、`3`-智能分析、**`4`-设备运维**、`5`-通行记录等。
  - 设计启示：多平台接入时，顶层配置实体是「外部平台/推送目标」，而非拆分为独立的“告警目标列表”与“心跳目标列表”，避免 N 个平台重复配置 2N 份地址与鉴权凭据。

### 1.2 英特灵达（INT-AIBOX / 灵曜体系）与中立边缘算力盒（算能 BM1684 / 瑞芯微生态）
- **英特灵达公开产品特征**：`INT-AIBOX` 系列（RK3588 / 算能 SG / CV186 等）对内对接自有「灵曜」云平台实现云边闭环，对外面向集成商提供标准 HTTP Webhook 与 MQTT 北向推送。具体私有 JSON 字段文档未公开托管。
- **国内通用开放边缘盒子标准协议特征与演进教训**：
  - **优点（值得借鉴）**：
    1. MQTT 模式下复用单一 Broker 长连接，按 Topic 分流告警事件与状态心跳，避免多连接互踢 ClientID；
    2. HTTP 模式下允许为告警与心跳配置不同接收路径（下游后端无需在同一路由内做异构 JSON 分发）。
  - **反面教训（必须规避）**：
    1. **凭据泄露**：部分早期盒子在 `/board_ping` 心跳包内附带全量通道的明文 `MediaUrl`（含 `rtsp://user:pass@ip`），导致相机密码随外部平台日志泄露。Zhulong 心跳严禁包含任何流 URI、凭据或内部磁盘路径，仅输出脱敏计数摘要。
    2. **高频刷屏**：部分盒子默认 5s 一次全量 HTTP POST，百台规模即造成网关日志与连接风暴。HTTP/MQTT 业务心跳默认应设为 `60s`（可配范围 `15s ~ 3600s`），秒级断网检测交由 MQTT Broker 的 LWT（Last Will and Testament）遗嘱机制承担。

---

## 2. Zhulong 现有模块数据源审计（零阻塞内存态核对）

| 载荷分块 | 数据源与代码锚点 | 性能特征 |
| --- | --- | --- |
| `device_id` | `internal/systeminfo/identity.go`（`IdentityService.DeviceID()`） | 原子/内存快照读取，零磁盘 I/O |
| `host` / `resources` | `internal/systeminfo/service.go:292`（`Service.GetHardwareStatus`） | 后台 2s 周期采样并缓存不可变 `latestResponse`，读取仅 `RLock` 浅拷贝 |
| `storage` | `internal/storage/service.go:212,474`（`CanWrite()`、`GetStatus()`） | 命中 `statusCacheTTL` 内存缓存，枚举含 `healthy` / `warning` / `cleaning` / `emergency_stopped` / `error` |
| `workload.cameras` | `internal/camera/state.go:122`（`StateRegistry.GetAllStates()`） | 纯内存状态表（`Enabled`、`Health`: `online`/`offline`/`unknown`、`Degraded`） |
| `workload.decode` | `internal/engine`（`GetDecodeStats()`，见 `internal/app/router.go:40`） | 原生引擎原子统计计数读取 |
| `workload.runners` | `internal/algorunner/supervisor.go`（活跃 watcher 与恢复状态机状态） | 需在 `Supervisor` 暴露只读汇总计数方法（仅持短读锁遍历内存 `watchers`） |
| `workload.tasks` | `internal/task`（已启用通道任务计数） | 任务量级 ≤ 通道上限（如 16/32），可通过短超时只读 `COUNT(*)` 或内存计数获取 |
| 宿主与日志降级 | `internal/app/router.go:205`（`Database.Ready()`、`Native.Ready()`、`LogSink.Health()`） | 纯内存标志位读取 |

---

## 3. 关键规范约束索引

- `.trellis/spec/backend/logging-guidelines.md` §13：**禁止心跳型审计（No Heartbeat Auditing）**——周期性心跳发送或状态查询严禁写入 `audit_logs`。
- `.trellis/spec/backend/dependency-injection.md`：`internal/alertpush` 不得反向依赖 `camera` / `task` / `algorunner` / `storage` / `systeminfo`；需在消费侧定义 `HeartbeatSnapshotProvider` 接口，由组合根 `internal/app` 注入适配器。
- `.trellis/spec/backend/database-guidelines.md`：禁用 `AutoMigrate`，当前最新迁移为 `000034`，新增迁移须从 `000035` 递增并提供对称 `.up.sql` / `.down.sql`。
