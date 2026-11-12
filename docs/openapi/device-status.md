# 第三方 OpenAPI：设备存活与运行状态查询对接指南

> 适用版本：Zhulong Edge Host v1.0+
> 适用场景：局域网/专网内的综合安防平台、运维监控系统或边缘网关通过机器间（M2M）只读接口按需拉取设备在线状态、硬件资源水位、媒体存储健康及 AI 负载摘要。

---

## 1. 架构与安全说明

1. **北向双模式闭环**：
   - **跨 NAT / 云平台**：在 WebUI「推送管理」中开启目标的「设备存活上报（Heartbeat）」，由边缘盒主动经 Webhook 或 MQTT 周期推送 `snake_case` 存活报文（MQTT 额外支持 Broker LWT 断电遗嘱）；
   - **同网段 / 专网直连**：上游平台直接调用本文档所述的只读拉取端点 `GET /api/v1/openapi/status`，获取同源聚合的 `camelCase` 实时状态快照。

2. **认证方式**：
   与其他 OpenAPI 端点共享同一套 HTTP Bearer Token 凭据：
   ```http
   Authorization: Bearer sk-<64位十六进制字符>
   ```
   - 缺失、格式错误或已吊销的 Token 返回 `401 Unauthorized`，并附带 `WWW-Authenticate: Bearer` 响应头，同时记录一条 `api_token.auth_rejected` 安全审计事件；
   - **零轮询审计污染**：合法的 `GET /api/v1/openapi/status` 查询为高频只读探活，**不会写入安全审计日志（`audit_logs`）**，也不会占用人脸底库录入的并发限流槽位。

3. **脱敏边界**：
   响应载荷严格剔除摄像头 RTSP 地址与账号密码、推送目标密钥、API Token 以及宿主机文件系统绝对路径（如媒体盘挂载目录），可安全接入第三方监控大屏。

---

## 2. 接口端点规范

### 2.1 查询设备实时健康与负载摘要

`GET /api/v1/openapi/status`

- **请求方法**：`GET`
- **请求路径**：`/api/v1/openapi/status`
- **响应头**：`Cache-Control: no-store`
- **HTTP 状态码语义**：
  - `200 OK`：宿主进程在线且成功生成状态快照。**注意**：当业务子域存在局部异常（如部分摄像头离线、媒体存储熔断停写、算法进程进入崩溃熔断）时，HTTP 状态码**仍为 `200 OK`**，由响应体内的 `data.status = "degraded"` 与 `data.reasons` 数组表达降级详情，便于负载均衡器与业务监控区分「盒子死机失联」与「盒子在线但带病运行」；
  - `401 Unauthorized`：Bearer Token 缺失、无效或已吊销；
  - `503 Service Unavailable`：宿主尚未完成初始化或状态服务不可用（`code: "SERVICE_UNAVAILABLE"`）。

- **响应体示例（正常就绪）**：
  ```json
  {
    "code": "OK",
    "message": "Success",
    "data": {
      "deviceId": "park-east-box-01",
      "reportedAt": "2026-11-11T08:30:00.123456789Z",
      "status": "ready",
      "reasons": [],
      "host": {
        "arch": "arm64",
        "uptimeSeconds": 86420,
        "publicBaseUrl": "http://192.168.10.50:8000"
      },
      "resources": {
        "cpuUsagePercent": 34.5,
        "memoryUsedPercent": 61.2,
        "npuSupported": true,
        "npuLoadPercent": 48.0,
        "maxTemperatureC": 57.5
      },
      "storage": {
        "status": "healthy",
        "writable": true,
        "usedPercent": 42,
        "freeMb": 74210
      },
      "workload": {
        "cameras": {
          "enabled": 8,
          "online": 8,
          "offline": 0,
          "degraded": 0
        },
        "tasks": {
          "enabled": 6
        },
        "runners": {
          "active": 2,
          "failed": 0
        },
        "decode": {
          "backend": "mpp",
          "hardwareAvailable": true,
          "activeSessions": 6,
          "maxSessions": 16
        }
      }
    }
  }
  ```

---

## 3. 字段与降级原因码字典

### 3.1 顶层与分组字段

| 字段路径 | 类型 | 说明 |
| --- | --- | --- |
| `deviceId` | `string` | 设备业务标识（在「系统设置 -> 系统信息」中配置，与主动推送报文的 `device_id` 一致） |
| `reportedAt` | `string` | 快照采样时间，恒为 UTC RFC3339（末尾带 `Z`） |
| `status` | `string` | 聚合健康态：`"ready"`（全部正常）或 `"degraded"`（存在至少一项降级原因） |
| `reasons` | `string[]` | 稳定机器原因码列表；无异常时恒为空数组 `[]`（绝不为 `null`） |
| `host.arch` | `string` | CPU 架构（如 `arm64`、`amd64`） |
| `host.uptimeSeconds` | `uint64` | 系统运行时长（秒） |
| `host.publicBaseUrl` | `string?` | 已配置的外部可达基地址（未配置时省略） |
| `resources.cpuUsagePercent` | `float64` | CPU 总利用率百分比（0–100，保留 1 位小数） |
| `resources.memoryUsedPercent` | `float64` | 物理内存使用率百分比 |
| `resources.npuSupported` | `bool` | 当前硬件是否具备受支持的 NPU |
| `resources.npuLoadPercent` | `float64` | NPU 核心平均负载百分比 |
| `resources.maxTemperatureC` | `float64` | SoC / CPU / NPU 温区最高温度（摄氏度） |
| `storage.status` | `string` | 媒体存储状态：`healthy` / `warning` / `cleaning` / `emergency_stopped` / `error` |
| `storage.writable` | `bool` | 媒体写入门闸当前是否允许落盘新抓拍与录像 |
| `storage.usedPercent` | `int` | 媒体分区可用空间占用率百分比（0–100） |
| `storage.freeMb` | `int64` | 媒体分区非特权可用剩余空间（MiB） |
| `workload.cameras` | `object` | 已启用摄像头统计：`enabled`（总数）、`online`（主码流在线）、`offline`（主码流离线/故障）、`degraded`（主码流在线但子码流异常） |
| `workload.tasks.enabled` | `int` | 已启用且绑定了算法的通道分析任务数 |
| `workload.runners` | `object` | 算法子进程统计：`active`（运行/启动/回滚中）、`failed`（异常退出或 `CRASH_LOOP` 熔断态） |
| `workload.decode` | `object` | 原生解码器状态：`backend`、`hardwareAvailable`、`activeSessions`、`maxSessions` |

### 3.2 `reasons` 降级原因码（固定顺序）

当任一条件命中时，`status` 置为 `"degraded"`，并按以下固定顺序追加对应原因码：

| 原因码 | 触发条件 | 建议处置 |
| --- | --- | --- |
| `storage_unavailable` | `storage.writable == false` 或 `storage.status` 处于 `emergency_stopped` / `error` | 检查外置磁盘挂载、空间水位与保留策略 |
| `camera_offline` | `workload.cameras.offline > 0`（至少一路已启用摄像头主码流不可达） | 检查前端相机供电、交换机链路或 RTSP 凭据 |
| `runner_failed` | `workload.runners.failed > 0`（存在算法包崩溃或进入 `CRASH_LOOP` 熔断） | 查看算法运行日志或重置/回滚对应算法包 |
| `decode_degraded` | 原生媒体引擎未就绪，或要求硬件解码但硬件解码器不可用 | 检查板端 VPU/MPP 驱动与解码会话配额 |
| `log_degraded` | 落盘日志队列发生丢弃或底层写入报错 | 检查系统盘剩余空间与文件权限 |

---

## 4. 调用示例

```bash
curl -sS \
  -H "Authorization: Bearer sk-0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" \
  "http://192.168.10.50:8000/api/v1/openapi/status"
```
