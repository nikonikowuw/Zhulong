# 技术设计：外部平台存活上报与推送双能力通道

## 1. 架构与模块边界

遵循单向无环依赖原则，新增的设备健康快照组装逻辑置于组合根 `internal/app`，各业务包仅声明最小消费接口：

```txt
                  ┌──────────────────────────────────────────────┐
                  │ internal/app (statusSnapshotAdapter)         │
                  │ 聚合: systeminfo + storage + camera          │
                  │       + algorunner + task + engine + logsink │
                  └──────────────┬──────────────────┬────────────┘
                                 │                  │
        HeartbeatSnapshotProvider│                  │StatusProvider
                                 ▼                  ▼
                  ┌──────────────────────┐  ┌────────────────────┐
                  │ internal/alertpush   │  │ internal/openapi   │
                  │ Webhook/MQTT Push    │  │ GET /openapi/status│
                  │ + MQTT LWT (snake)   │  │ Bearer Auth (camel)│
                  └──────────────────────┘  └────────────────────┘
                                 ▲
                                 │ REST (/api/v1/alert-destinations)
                  ┌──────────────┴───────────────┐
                  │ web/src/features/alert-push  │
                  │ 双能力分区表单 + 能力徽章    │
                  └──────────────────────────────┘
```

- **`internal/alertpush`**：不导入任何兄弟业务域，仅消费 `HeartbeatSnapshotProvider`（获取快照）与既有 `DeviceIDProvider`（获取最新 `device_id`）。
- **`internal/openapi`**：新增可选依赖 `StatusProvider`（通过 `Options` 或构造函数扩展，保持向后兼容单测），挂载 `GET /status` 路由。
- **`internal/algorunner` & `internal/task`**：
  - 在 `algorunner.Supervisor` 增加只读方法 `SummaryCounts() (active, failed int)`，在短读锁下遍历内存 `watchers` 统计运行中与处于熔断失败态的算法进程数；
  - 在 `task.Service`（或 `Store`）增加轻量 `CountEnabledTasks(ctx) (int, error)`，查询 `SELECT COUNT(*) FROM analysis_tasks WHERE enabled = 1`（单索引/极小表，亚毫秒级）。

---

## 2. 数据库迁移与持久化契约

### 2.1 迁移 `000035_add_alert_destination_capabilities`

- **`000035_add_alert_destination_capabilities.up.sql`**：
  ```sql
  ALTER TABLE alert_destinations
      ADD COLUMN events_enabled BOOLEAN NOT NULL DEFAULT 1;
  ALTER TABLE alert_destinations
      ADD COLUMN heartbeat_enabled BOOLEAN NOT NULL DEFAULT 0;
  ```
- **`000035_add_alert_destination_capabilities.down.sql`**：
  ```sql
  ALTER TABLE alert_destinations DROP COLUMN heartbeat_enabled;
  ALTER TABLE alert_destinations DROP COLUMN events_enabled;
  ```
  两列均无外键与附加索引，与 `000032` 一致采用 `ALTER TABLE ... DROP COLUMN`，原样保留存量行与既有唯一索引。

### 2.2 `config_json` 与 `secret_ciphertext` 扩展

- **`webhookConfig`（`config_json`）**：
  ```go
  type webhookConfig struct {
      AuthType                 string `json:"auth_type"`
      TimeoutSeconds           int    `json:"timeout_seconds"`
      HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds,omitempty"` // 缺省归一化为 60，范围 15..3600
  }
  ```
- **`webhookSecret`（`secret_ciphertext`，AES-256-GCM 加密）**：
  ```go
  type webhookSecret struct {
      URL          string            `json:"url"`
      HeartbeatURL string            `json:"heartbeat_url,omitempty"`
      Token        string            `json:"token,omitempty"`
      Username     string            `json:"username,omitempty"`
      Password     string            `json:"password,omitempty"`
      Headers      map[string]string `json:"headers,omitempty"`
  }
  ```
  - **更新语义**：与既有字段一致——更新请求若 `config.heartbeatUrl` 为空串表示清除独立心跳 URL（回退使用主 `url`），非空表示设置/替换；当密码/Token 在编辑时缺省保留且修改了 `url` 或 `heartbeatUrl`，按**合并后的最终生效 URL 与生效凭据**执行同源校验（`scheme + strings.ToLower(host)`，含显式/默认端口归一化，如 `http:80` / `https:443`）。
- **`mqttConfig`（`config_json`）**：
  ```go
  type mqttConfig struct {
      Broker                   string `json:"broker"`
      Username                 string `json:"username,omitempty"`
      Topic                    string `json:"topic"`
      QoS                      uint8  `json:"qos"`
      KeepAliveSeconds         int    `json:"keep_alive_seconds"`
      TLSSkipVerify            bool   `json:"tls_skip_verify"`
      HeartbeatIntervalSeconds int    `json:"heartbeat_interval_seconds,omitempty"` // 缺省 60，范围 15..3600
      HeartbeatTopic           string `json:"heartbeat_topic,omitempty"`            // 禁止 + / #
      LWTEnabled               *bool  `json:"lwt_enabled,omitempty"`                // nil 视为 true
  }
  ```

---

## 3. 跨层数据契约

### 3.1 主动上报载荷（`internal/alertpush` · `snake_case`）

```go
type HeartbeatPayload struct {
    ID         string                    `json:"id"`
    DeviceID   string                    `json:"device_id,omitempty"`
    EventType  string                    `json:"event_type"` // 恒为 "heartbeat"
    Seq        uint64                    `json:"seq"`
    ReportedAt string                    `json:"reported_at"`
    Status     string                    `json:"status"`     // "ready" | "degraded"
    Reasons    []string                  `json:"reasons"`    // 非 nil，正常为 []
    Host       HeartbeatHostPayload      `json:"host"`
    Resources  HeartbeatResourcesPayload `json:"resources"`
    Storage    HeartbeatStoragePayload   `json:"storage"`
    Workload   HeartbeatWorkloadPayload  `json:"workload"`
}
```

MQTT 离线通知载荷（状态 Topic）：
- **Broker LWT 遗嘱（预埋）**：`{"device_id":"<id>","event_type":"offline","reason":"connection_lost"}`
- **优雅下线（`Close` 主动发布）**：`{"id":"<ulid>","device_id":"<id>","event_type":"offline","reported_at":"<rfc3339nano>","reason":"graceful_shutdown"}`

### 3.2 OpenAPI 状态下拉响应（`internal/openapi` · `camelCase`）

`GET /api/v1/openapi/status` 返回 `httputil.Response{data=DeviceStatusResponse}`：

```go
type DeviceStatusResponse struct {
    DeviceID   string                `json:"deviceId"`
    ReportedAt time.Time             `json:"reportedAt"`
    Status     string                `json:"status"`  // "ready" | "degraded"
    Reasons    []string              `json:"reasons"` // 非 nil，空时为 []
    Host       DeviceHostInfo        `json:"host"`
    Resources  DeviceResourcesInfo   `json:"resources"`
    Storage    DeviceStorageInfo     `json:"storage"`
    Workload   DeviceWorkloadInfo    `json:"workload"`
}
```

### 3.3 推送目标管理 REST 扩展（`/api/v1/alert-destinations`）

- `CreateAlertDestinationRequest`：新增 `EventsEnabled *bool`（`nil` 默认为 `true`，兼容旧客户端）、`HeartbeatEnabled *bool`（`nil` 默认为 `false`）；`ImageMode` 在 `EventsEnabled == false` 时可选（缺省补 `"url"`）。
- `UpdateAlertDestinationRequest`：新增 `EventsEnabled *bool`、`HeartbeatEnabled *bool`（`nil` 保持原值）。
- `TestAlertDestinationRequest`：新增 `EventsEnabled *bool`、`HeartbeatEnabled *bool`。
- `AlertDestinationResponse`：新增 `eventsEnabled: bool`、`heartbeatEnabled: bool`；`AlertDestinationConfigInfo` 新增 `heartbeatIntervalSeconds`、`heartbeatUrl`（仅 Webhook，含该字段时响应必设 `Cache-Control: no-store`）、`heartbeatTopic`、`lwtEnabled`（仅 MQTT）。

---

## 4. 并发、调度与热重载设计

1. **Sender 接口扩展**：
   ```go
   type Sender interface {
       Send(ctx context.Context, ev record.AlarmEvent) error
       SendHeartbeat(ctx context.Context, snap HeartbeatSnapshot, seq uint64) error
       Close() error
   }
   ```
   - `WebhookSender.SendHeartbeat`：目标 URL 取 `firstNonEmpty(opts.HeartbeatURL, opts.URL)`，单次 HTTP POST（复用 `client` 超时与鉴权头，**不做 3 次退避重试**，因为旧心跳无重传价值）。
   - `MQTTSender.SendHeartbeat`：目标 Topic 取 `firstNonEmpty(opts.HeartbeatTopic, opts.Topic)`，单次 `Publish`（5s 等待 ACK）；`Close()` 若开启了 `LWTEnabled` 且当前 `IsConnected()`，先以 `500ms` 超时发布 `graceful_shutdown` 报文，再调用 `Disconnect(250)`（正常 `Disconnect` 包会使 MQTT Broker 自动丢弃预埋的 `connection_lost` LWT，避免重复离线事件）。
2. **双轨 Worker 协程模型**：
   - `destinationWorker.start()`：
     - 始终启动主协程处理 `w.queue`（仅 `eventsEnabled == true` 时 `Dispatcher.Dispatch` 才会入队）；
     - 若 `heartbeatEnabled == true`，额外启动受 `sync.WaitGroup` 管理的 `runHeartbeatLoop()`：
       - 首拍延迟：`rand(0, min(2s, interval/10))`；
       - 后续间隔：`interval + rand(-interval/20, +interval/20)`（用 `time.Timer` 逐拍重算，避免固定相位齐射）；
       - 停止时通过同一个 `w.stopCh` 立即取消在途 `SendHeartbeat` 的 context，等待两条协程都退出后再调一次 `w.sender.Close()`，保证 `Close()` 与 `Send`/`SendHeartbeat` 绝无并发竞态。
3. **进程级单调序号 `seq`**：
   - 在 `Dispatcher` 上维护进程级 `seq atomic.Uint64`（或每目标独立 `seq atomic.Uint64`——**设计取舍**：采用**每目标独立或配置指纹重建保留？** 若采用 `Dispatcher` 维护按 `destinationID` 映射的内存 `seqMap`，则热修改非关键配置或网络重连时 `seq` 连续递增，而**宿主进程重启**时明确归 `1`，最准确反映“设备进程是否重启过”）。
4. **`device_id` 变更时的 MQTT LWT 同步**：
   - Webhook / MQTT 周期心跳在每次发送时动态调 `DeviceIDProvider.DeviceID()`；
   - MQTT LWT 是在 `Connect` 时固化在 Broker 会话上的。在 `systeminfo.IdentityService` 更新回调（或 `Dispatcher` 心跳发送发现当前 `device_id` 与建连时缓存的 `willDeviceID` 不一致）时，自动调用 `d.RequestReload()` 重建启用了 LWT 的 MQTT Worker，无需重启进程即可刷新 Broker 侧遗嘱中的 `device_id`。

---

## 5. 关键风险与回滚考量

- **风险 1：同源判断绕过或端口漏判**
  - 防护：写独立辅助函数 `sameOrigin(rawA, rawB string) bool`，经 `url.Parse` 严格解析 `Scheme`（`http`/`https`）、`Hostname()`（大小写不敏感）及有效端口（`Port()` 为空时补 `80`/`443`），拒绝含 userinfo 或非 HTTP(S) scheme 的 URL。
- **风险 2：MQTT `Close()` 在 Broker 失联时卡顿停机**
  - 防护：仅当 `client.IsConnected()` 为真时尝试发送 `graceful_shutdown`，且 `token.WaitTimeout(500 * time.Millisecond)` 严格封顶，超时直接进入 `Disconnect(250)`，绝不拖慢宿主 `stopSequence`。
- **风险 3：数据库回滚**
  - `000035` `.down.sql` 完整重建 `000030` 表结构与索引；应用层对 `config_json` / `secret_ciphertext` 中未知字段默认忽略，回滚二进制与回滚迁移均安全可控。
