# PRD: 修复 record 模块契约与并发缺陷

## 1. Goal

解决 `internal/record` 模块中存在的 API JSON 序列化破坏前端 Zod 校验、时间戳乱序/负时间差导致事件被永久抑制、告警标签误判（如 helmet/fire_hydrant）、分层越权直接查询底库表以及错误吞没等缺陷，保障感知记录链路的高可用性与契约一致性。

## 2. Requirements

### R1. API 响应序列化防御 (空切片与空字典铁律)
- `RecognitionResponse.Candidates` 当无数据或反序列化为 nil 时，必须强制输出为 `[]`（而非 `null`），严格匹配前端 `z.array().default([])` 校验契约。
- `RecognitionResponse.Attributes`、`AlarmResponse.Extra`、`CaptureResponse.Attributes` 当无数据或反序列化为 nil 时，必须强制输出为 `{}`（而非 `null`），严格匹配前端 `z.record().default({})` 校验契约。
- `service.go` 在持久化 JSON 前，若切片或字典为 nil，应写入 `"[]"` 或 `"{}"`，避免将字面量 `"null"` 存入 SQLite。

### R2. CooldownManager 时间鲁棒性与防抑制
- `CooldownManager.Allow(key, now)` 在检测冷却时，必须仅在时间戳正向推进且 `diff >= 0 && diff < cm.cooldown` 时进行抑制。
- 当出现视频时间戳轻微回跳、乱序或 NTP 回拨时（`diff < 0`），不得误杀当前事件，应允许通过并更新基准时间，杜绝永久静默。
- 提供线程安全的冷却窗口设置方法，避免替换实例造成数据竞争或丢失历史冷却记录。

### R3. 告警标签精准匹配 (Alarm Label Matching)
- 废除粗暴的子串包含规则（如 `strings.Contains(label, "helmet")` 会误杀佩戴安全帽的正常检测，`"fire_hydrant"` 会误判为火灾大警报）。
- 支持精确违规标签判定（如 `no_helmet`、`smoke`、`fire`、`flame`、`intrusion` 等），对特定复合词进行白名单/黑名单隔离。

### R4. 架构分层与依赖解耦
- 将 `resolveGalleryImagePath` 的数据访问逻辑下沉至 `Store`（如 `Store.GetFaceImagePath(ctx, faceID)`）。
- 从 `Service` 结构体中移除 `dbProvider` 依赖，确保业务服务仅依赖 `Store`，实现纯粹的领域分层。
- 修复 `Store` 批插入方法中的 nil pointer 隐患。

### R5. 消除吞错并完善请求参数校验
- 消除 `handler.go` 中对 `c.ShouldBindQuery` 的 `_ = ` 吞错，统一通过 `httputil.HandleBindError(c, err)` 向上返回标准的 HTTP 400 / 422 错误。
- 补全 `handler_test.go` 中缺失的真实数据入库与正向 ID 查询测试断言。

## 3. Acceptance Criteria

- [x] `GET /api/v1/records/recognitions`、`GET /alarms`、`GET /captures` 在空字段/无候选人时输出的 JSON 中，`candidates` 恒为 `[]`，`attributes` 和 `extra` 恒为 `{}`，无任何 `null` 泄漏，前端 Zod 校验零报错。
- [x] 针对 `CooldownManager` 编写乱序与时间回退单测，验证 `now < lastTime` 时事件不会被误判为处于冷却期。
- [x] 针对 `isAlarmLabel` 编写单测，验证 `"helmet"`、`"fire_hydrant"` 等不会被误判为违规告警，而 `"no_helmet"`、`"smoke"` 正确触发告警。
- [x] `Service` 结构体不再包含 `database.DBProvider`，所有数据库查询收敛在 `Store` 内。
- [x] 所有 handler 查询参数绑定非法时（如 `?page=abc`）正确返回标准化错误信封，无 `_ = fn()` 吞错。
- [x] `go test -v -race ./internal/record/...` 全部通过，无数据竞争，无假测试。
