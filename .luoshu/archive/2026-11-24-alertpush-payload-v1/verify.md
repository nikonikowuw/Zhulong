# Verify Contract: 2026-11-24-alertpush-payload-v1

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit`（`544f56a`）diff 输出结论到 `verify-report.md`，严禁修改本契约或业务源码。

## 验收标准

### 载荷信封

- [ ] V1：alarm / face / vehicle / heartbeat / offline 五类消息顶层均含 `schema_version`(=1)、`id`、`seq`、`event_type`、`device_id`、`source`、`captured_at`、`sent_at`、`images` 九个键（缺省值为 `null`，`images` 为数组从不为 `null`），且**恰好一个**与 `event_type` 同名的业务子对象；其它类型子对象键不出现。有单测对每类消息序列化后的键集合做精确断言。
- [ ] V2：`event_type` 取值集合严格为 `alarm|face|vehicle|heartbeat|offline`；`record.EventTypeFaceRecognition`→`face`、`EventTypePlateRecognition`→`vehicle`、`EventTypeAlarm` 与空值→`alarm`。`record.EventType` 常量取值未被修改。
- [ ] V3：所有时间字段为 UTC 固定毫秒格式（正则 `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`）；心跳 / 离线 `captured_at=null`、`source=null`、`images=[]`；MQTT LWT 的 `seq` 与 `sent_at` 为 `null`，优雅离线二者非空。

### 业务子对象

- [ ] V4：`box` 全零输出 `null`，非零原样输出；`face.matched=false` 时 `person=null` 且 `similarity=null`；`vehicle.plate_number` 为空时 `plate=null`；空字符串字段输出 `null`；`attributes` 恒为 JSON 对象。
- [ ] V5：`vehicle.attributes` 不含 `plate_number`/`plate_color`/`vehicle_type`/`vehicle_color`，且原 `record.AlarmEvent` 的 map **未被修改**（单测在构造后断言原 map 仍含这些键）；多协程并发对同一事件构造载荷的用例在 `-race` 下通过。
- [ ] V6：`alarm` 子对象在 `ev.Alarm == nil` 时回退扁平字段 `AlarmType/Score/Box/Extra`；`alarm.attributes` 来自 `Extra`；载荷中不存在 `extra` 键。
- [ ] V7：`source.camera.id` 恒等于 `ev.CameraID`；resolver 为 nil 或查询失败时 `camera.name=null`、不丢事件；`AlgorithmID` 为空时 `source.algorithm=null`；`SourceResolver` 未在 `Dispatcher.Dispatch` 内调用。

### 图片

- [ ] V8：`images` 输出顺序固定 scene→object→reference，每项含 `role,mime,size_bytes,url,url_expires_at,part,base64` 七键；文件未就绪 / 签名失败 / 打开失败且无 url 的角色**不出现**；对象存储预签名命中时 `size_bytes=null` 且未打开远端对象；载荷中不存在 `image_ready`、`image_url`、`crop_url`、`image_base64`、`gallery_url` 键。
- [ ] V9：Webhook multipart：每张可用图对应 part `image_<role>`，载荷项 `part` 与之相同；无图时只有 `payload` part；任一错误路径下已打开文件均关闭（单测以计数 fake `MediaReader` 断言 Close 次数 == Open 次数）。multipart 不受 `inline_max_kb` 限制（2MB 场景图完整上传）。
- [ ] V10：MQTT base64：内联原始字节总和 ≤ `inline_max_kb × 1024`；**装入优先级** object→reference→scene；超预算的图在配置了 `PublicBaseURL` 时仅带 `url`，否则不出现并有 Warn 日志（含 `role`、`size_bytes`）。必须有单测：4K 场景（scene 2MB + object 60KB，预算 500KB）→ object 带 `base64`、scene 仅 `url`；同条件无 `PublicBaseURL` → 列表仅含 object；预算恰等于总和 → 全部内联。常量 `maxMQTTBase64Bytes` 已移除。
- [ ] V10a：MQTT 配置 `inlineMaxKb` 默认 500、接受 64..8192，63 与 8193 返回 422 `ALERT_DESTINATION_CONFIG_INVALID`；创建 / 更新 / 测试 / 列表响应往返一致；存储于 `config_json.inline_max_kb`，无新增迁移；Webhook 目标忽略该字段。前端仅 base64 模式显示该字段，Zod 校验同范围，三语文案含 broker 上限说明与 `public_base_url` 提示。

### 序号与分发

- [ ] V11：`seq` 在 `Dispatch` 入队前分配；队列溢出被挤掉的事件留下序号缺口（单测：阻塞 sender + 注入 >16 事件，断言收到的 seq 严格递增且存在缺口）；同一事件 Webhook 重试的 `id`、`seq` 不变；心跳与事件共用同一目标计数器，并发下无重复 seq。
- [ ] V12：resolver 阻塞时 `Dispatcher.Stop` 仍在有界时间内返回（resolver 使用独立超时 ctx），无 goroutine 泄漏。

### Webhook 头与配置

- [ ] V13：Webhook 事件、心跳与测试探针请求带 `X-Zhulong-Event`、`X-Zhulong-Delivery`(=载荷 `id`)、`X-Zhulong-Schema: 1`；三者属于保留头，创建 / 更新提交同名自定义头返回 `ErrReservedHeaderUsed` 对应错误。
- [ ] V14：`filter.eventTypes` 仅接受 `alarm|face|vehicle`；提交 `face_recognition` 等旧名或未知值返回 HTTP 422、稳定码 `ALERT_DESTINATION_EVENT_TYPE_INVALID`，三语文案存在于 `locale.go`；过滤匹配使用外部事件类型名。
- [ ] V15：迁移 `000040_*` 成对存在；up 将存量 `filter_json` 的 `"face_recognition"`/`"plate_recognition"` 改写为 `"face"`/`"vehicle"`，down 反向；不含旧名的行与 `{}` 不变；有 up/down 对称测试。已存在的迁移文件未被修改。
- [ ] V16：`internal/app` 注入的 `SourceResolver` 实现不 `import` 进 `internal/alertpush`（alertpush 不依赖 camera/algorithm 包）；`provideAlertPush` 传入 `Config.Source`。
- [ ] V17：前端过滤器选项值为 `alarm`/`face`/`vehicle`，三语 locale 键同步，`base64Desc` 体现总预算语义；无残留 `face_recognition`/`plate_recognition` 过滤器取值。

## 负向边界（不得发生）

- 不得修改：`record` 域对外 HTTP API、`record.EventType` 常量值、`AlarmEvent` 字段集合（扁平兼容字段保留）、`/alert-destinations` 除 `filter.eventTypes` 取值与新增可选 `config.inlineMaxKb` 外的请求/响应结构、签名媒体端点 `/api/v1/media/signed/*` 行为与限流、原生快照编码（分辨率 / 质量 / C ABI）。
- 不得引入：投递重试/超时策略变化（`Send` 3 次重试与退避、worker 10s ctx、队列容量 16、丢最旧策略保持不变）、HMAC 体签名、持久化队列、`track_id`/生命周期字段、新的外部依赖。
- 不得出现：handler 裸 `c.JSON`、`_ = fn()` 吞错、业务路径 `panic`、`context.Context` 存入结构体字段、对 engine 相关包的裸 `go test`。
- 不得弱化或删除与本次无关的既有测试断言；被删除的旧测试仅限断言旧载荷字段的用例，且对应行为有 v1 用例覆盖。

## 验证命令

```bash
go test -race ./internal/database/...
python3 native/scripts/build.py go test -race ./internal/alertpush ./internal/app
cd web && pnpm test -- src/features/alert-push
make check
```

全部命令必须无失败、无跳过地通过；`make check` 为最终门禁。
