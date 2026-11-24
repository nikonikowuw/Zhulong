# Proposal: 2026-11-24-alertpush-payload-v1

## 背景与目标

`internal/alertpush` 对外推送载荷（Webhook / MQTT）在 2026-11-03 多通道推送任务中定型，但存在以下契约缺陷（详见分析结论）：

1. **无版本号**：只靠「只增不改」，无法表达语义修正，接收方无法识别载荷版本。
2. **判别字段与子对象不同名**：`event_type=face_recognition` 对应子对象 `face`，`plate_recognition` 对应 `vehicle`，与设计文档「同名子对象」声明矛盾。
3. **来源信息不可读**：`camera_id` 是自动生成的 ULID 代理键，无摄像头名称；算法来源被完全移除，第三方算法包可能输出同名 `alarm_type`。
4. **缺省值歧义**：无框结果输出 `[0,0,0,0]`；未匹配人脸输出 `person_id=""`；`similarity` 未匹配语义未定义。
5. **图片字段语义混杂**：`image_ready` / `image_url` / `crop_url` / `image_base64` / `face.gallery_url` 五个字段分散，multipart 与 base64 模式不携带裁剪图。
6. **命名不统一**：`alarm.extra` 与 `face/vehicle.attributes` 并存；车辆 `attributes` 与外提字段重复。
7. **无丢包检测**：事件无序号，接收方无法发现队列溢出丢弃（心跳已有 `seq`）。

**当前无任何外部平台接入**，因此本次一次性定型 `schema_version: 1`，允许破坏性变更，不做旧载荷兼容。

可观测结果：三种事件、心跳、离线消息共用一套 v1 信封；三种图片模式对同一事件提供一致的图片集合；推送目标过滤器使用与载荷一致的事件类型名。

## In Scope（本次包含）

- **v1 载荷信封**：`schema_version`、`id`、`seq`、`event_type`、`device_id`、`source{camera{id,name},algorithm{id,name,version}}`、`captured_at`、`sent_at`、`images[]`，以及与 `event_type` 同名的唯一业务子对象。
- **事件类型短名**：外部 `event_type ∈ {alarm, face, vehicle, heartbeat, offline}`；`record.EventType` 内部取值不变，由 alertpush 映射。
- **业务子对象重构**：`alarm{alarm_type,score,box,attributes}`、`face{matched,person,similarity,box,attributes}`、`vehicle{plate,vehicle_type,vehicle_color,score,box,attributes}`；统一 `null` 缺省策略，外提键不在 `attributes` 重复。
- **图片列表**：`images: [{role, mime, size_bytes, url, url_expires_at, part, base64}]`，`role ∈ {scene, object, reference}`；url / multipart / base64 三种模式都覆盖全部可用图片。
- **base64 内联预算可配置**：MQTT 新增 `inline_max_kb`（默认 500，范围 64..8192，原始字节），装入顺序改为 object → reference → scene（小图优先），超预算图片回退 url，无 url 则省略并记日志；前端 MQTT 表单新增该字段与 broker 上限说明。动机：4K 场景图（质量 85 约 1–3MB）不可能内联，内联只适合特写，大图走 url。
- **序号**：每个推送目标单调 `seq`，事件在**入队时**分配（溢出丢弃会留下缺口），心跳在发送时分配，两者共用同一计数器。
- **来源解析**：alertpush 声明消费侧接口 `SourceResolver`，宿主在 `internal/app` 用 camera / algorithm 服务实现；worker 发送时解析，失败降级为 `null` 字段。
- **心跳 / 离线**：心跳数据收进 `heartbeat` 子对象，离线原因收进 `offline` 子对象；MQTT LWT `sent_at`、`seq` 为 `null`。
- **Webhook 头**：新增 `X-Zhulong-Event`、`X-Zhulong-Delivery`、`X-Zhulong-Schema`，并加入保留头集合。
- **过滤器事件类型**：`filter.event_types` 改为 `alarm|face|vehicle`，服务端白名单校验；新增迁移改写既有 `filter_json`；前端过滤器选项与三语文案同步。
- 测试探针事件（`Service.sendTestProbe`）按 v1 构造。

## Out of Scope（明确不做）

- **告警生命周期**（`phase: start|update|end`、`track_id`）：依赖 `2026-11-15-face-track-fusion-identity` 的稳定目标身份，后续以增量字段加入，不升版本。
- **投递可靠性**：持久化重试队列、至少一次投递、Webhook 总超时 10s 与 `timeout_seconds` 冲突、MQTT `Connect` 超时返回 nil、`publishBytes` 的 `time.After` —— 单独立项。
- **Webhook 请求体 HMAC 签名 / 防重放**：单独立项。
- **GA/T 1400、GB/T 28181 告警**：应作为独立 sender 翻译内部事件，不进入本载荷。
- 摄像头业务编码字段（如国标通道号）：本次只输出已有的 `name`。
- 原生缩略图（`scene_preview`，长边 ~1280，用于 4K 场景内联预览）：需改 C++ 快照流水线与 C ABI，后续独立变更，届时增量加入不升版本。
- Webhook multipart 改为流式上传（当前整包读入内存）：保持现状。
- 管理 API（`/alert-destinations`）的请求/响应结构，除 `filter.eventTypes` 取值外不变；`record` 域对外 HTTP API 不变。
- 旧载荷兼容层或双写。

## 影响范围（模块 / 接口契约 / Specs & Rules）

- **Go**：`internal/alertpush`（`sender.go`、新增 `payload.go`、`dispatcher.go`、`sender_webhook.go`、`sender_mqtt.go`、`service.go`、`handler.go`、`locale.go`、`filter.go` 及对应测试）；`internal/app`（`app.go`、`adapters.go`，注入 `SourceResolver`）；`internal/database/migrations/000040_*`。
- **前端**：`web/src/features/alert-push/components/form/destination-filters-section.tsx`、`locales/{en,zh-Hans,zh-Hant}.json` 及相关测试。
- **外部契约**：推送载荷（破坏性，定为 v1）；`filter.eventTypes` 取值（破坏性，迁移改写存量数据）。
- **Specs**（Archive 阶段原位更新）：`backend/alertpush-guidelines.md` 新增「对外载荷 v1」章节；`backend/record-guidelines.md` 如涉及 `AlarmEvent` 说明同步。
- **Rules**：`go-host/code-style`、`go-host/concurrency-and-errors`、`go-host/http-contract`、`go-host/test-wiring`、`data/sqlite-persistence`、`data/media-storage-boundary`、`native/native-link-and-abi`、`web/frontend-api-boundary`、`web/frontend-browser-tests`。
