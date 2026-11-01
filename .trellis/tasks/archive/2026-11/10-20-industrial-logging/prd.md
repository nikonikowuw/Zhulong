# 工业级运行日志体系

## Goal

把 Zhulong 的运行日志从「开发期够用」提升到「工业级可运维」：

1. **现场可排障** —— 一次失败 HTTP 请求的 access/error 事件带相同 `requestId`；长生命周期流使用独立的 `streamId` 等业务标识。
2. **丢失可见、资源有界** —— 默认不采样；异步队列或输出端过载时允许有界丢弃，但必须计数并报告。文件输出有硬容量上限，不承诺断电或磁盘故障下绝对不丢。
3. **跨层可追溯** —— Go 宿主、C ABI、C++ 原生引擎、独立 algo runner 的运行日志进入可解析的结构化流，并使用适合事件生命周期的关联字段。
4. **降低泄漏与注入风险** —— 对已知敏感字段和已知凭据形式做源头脱敏；控制字符与超长输入受到约束。不把通用 logger core 描述成可识别任意秘密的万能脱敏器。

## 背景

当前实现（`internal/app/logger.go`，20 行）以 `zap.NewProductionConfig()` / `NewDevelopmentConfig()` 起步，
DI 注入与 `Named(...)` 的架构方向是对的，但存在四类系统性缺口：

| 类别 | 现状 | 影响 |
| --- | --- | --- |
| 配置与生命周期 | 配置项只有 `logging.development` 一个 bool；无级别、无格式、无输出目标、无采样开关、无轮转 | 现场排障要改配置重启（丢流丢录像）；无轮转会撑爆 eMMC |
| 关联性 | `AccessLog` 有 `requestId`，但 `httputil.WriteError` 的错误日志没有 | 500 报错与访问日志无法关联，排障靠时间戳猜 |
| 噪声与一致性 | service 层 `logger.Error` + `WriteError` 双重记录；`NewProductionConfig` 默认开启采样（100/100）；SSE/WS 走 AccessLog；`Named` 命名下划线与点混用；约 30 处 `zap.NewNop()` 兜底 | 同一次失败两条日志；突发错误被抽样丢弃；长连接污染 latency 统计；装配错误被静默吞掉 |
| 跨语言 | `native/src` 零日志设施（仅 `unix_socket.cpp` 的裸 `std::cerr` 与 runner 的 stdout JSON） | C++/NPU 内部失败只能靠返回码反推；`std::cerr` 破坏 JSON 行完整性 |

前端另有 3 处裸 `console.*`，生产环境静默丢弃。

## Scope

本任务为**父任务**，只负责需求集合、子任务映射、跨子任务验收标准与最终集成评审，**本身不直接实施**。
实施拆分为三个可独立计划、独立实施、独立验收、独立归档的子任务：

| 子任务 | 目录 | 交付物 | 优先级 | 顺序 |
| --- | --- | --- | --- | --- |
| A. Go 日志核心加固 | `.trellis/tasks/10-20-logging-core-hardening` | 配置化 logger、requestId/streamId 契约、去重、AccessLog 分流、命名统一、`nativelog.Queue` | P0 | 第一 |
| B. 原生层日志桥接 | `.trellis/tasks/10-20-native-log-bridge` | C ABI 日志回调、C++ 统一日志宏、裸 `std::cerr` 清理、runner 结构化解析 | P0 | 第二（依赖 A 的 logctx 与队列） |
| C. 日志安全与前端可观测 | `.trellis/tasks/10-20-logging-safety-and-frontend` | 有边界的脱敏与注入防护、前端统一 logger 与错误上报 | P1 | 第三（依赖 A/B；避免并发改 runner 日志路径） |

**不在本任务范围内**（明确排除，避免范围蔓延）：

- 业务审计日志（`internal/audit`、`audit_logs` 表）。审计与运行日志是两套不同面向的机制，审计侧规范见
  `.trellis/spec/backend/logging-guidelines.md` §4，本任务不改动其行为。
- 分布式追踪后端（Jaeger / Tempo）部署与 OTel Collector 运维。本任务只预留 `traceId` 字段与注入点，
  不引入 OTel SDK 依赖。
- 指标（Prometheus / metrics）体系。日志与指标是两条独立链路。
- 日志查询 UI（前端日志检索页面）。

## Target Scenarios

1. **用户报「500」** —— access 事件与内部错误事件的 `requestId` 一致；access 事件不记 Error，单次意外 5xx 只记一条 Error。
2. **现场需要临时开 Debug** —— 运维 `kill -HUP <pid>`，从实际配置文件重新读取级别，**不重启进程**（重启意味着丢流、丢录像）。
3. **插件推理异常** —— runner 在插件 API 返回失败时记录 `module=algorithm.runner`、固定操作 token 与 numeric `rc`；它不定位到 RKNN/ONNX/RGA 后端内部原因，也不输出供应商错误正文。进程内 C++ pipeline 事件仍经 C ABI 回调带 `module` 和可用的 `streamId` 进入 Go 日志流。不得把启动 HTTP 请求的 `requestId` 当成长连接的身份。
4. **algo runner 崩溃循环** —— runner 的 stderr JSON 被解析为结构化字段（`stage`/`rc`）；刷屏被限流，且丢弃计数可观察，不出现「日志很干净」的假象。
5. **设备连续运行 30 天** —— 在确认的部署 profile、峰值日志量和磁盘预算下，日志总占用有硬上限，并满足该 profile 声明的最短保留期；不以运行时长直接推导任意负载下都保留 30 天。
6. **恶意输入进日志** —— 用户可控的相机名 / 请求路径中含换行或控制字符时，不破坏 JSON 行结构，不伪造日志行。
7. **RTSP 凭据不泄漏** —— 有效和畸形 RTSP 凭据输入都不得以明文进入已验收的日志路径。

## Requirements

### R1 日志配置化与落盘策略（→ 子任务 A）

- `[logging]` 段扩展为：`development` / `level` / `format` / `output` / `[logging.sampling]` / `[logging.rotation]`（包含 `max_total_size_mb` 硬容量预算）。
- `level` 只允许 `debug|info|warn|error`；用白名单校验，zap 原生支持的 `dpanic|panic|fatal` 也必须拒绝。非法值启动期 fail fast。
- `format` 支持 `json|console`；默认随 `development`（dev=console，prod=json）；非法值启动期报错。
- `output` 支持 `stderr|stdout|<文件路径>`；默认 `stderr`。
- 采样默认关闭；若启用，`initial` / `thereafter` 必须显式为正数，否则启动失败。关闭采样不代表无损：异步队列溢出、写入失败、进程崩溃和掉电仍可能丢日志，丢失量必须可观察。
- journald 只用于明确采用 systemd 的部署 profile；必须为 host journal 预算与 Zhulong 份额留出容量。文件输出必须启用有界轮转，禁止无限追加；容量预算必须覆盖活动文件、归档及轮转/压缩产生的临时文件。
- 轮转失败不得回退为无限追加；记录可用的 stderr/宿主错误通道并暴露 sink 故障状态。具体 fail-open/fail-closed 行为按部署 profile 验收。
- 时间戳统一 **UTC ISO8601**；持续时间字段显式使用 `_ms` 单位。边缘设备可能跨时区、可能被 NTP 校时，本地时间会在日志里产生时间倒流。
- 运行期级别调整：`SIGHUP` 从启动时解析出的配置文件路径重新读取 `logging.level` 并立即生效；只允许调级，不允许换 sink / 编码。解析失败保留最后一个有效级别。
- `logging.level` 的运行时变更必须通过 `zap.AtomicLevel`，不得重建 logger。

### R2 requestId 全链路贯穿（→ 子任务 A）

- 新增中立包承载关联字段（HTTP `requestId`，预留 `traceId`），**不得**引入 `httputil → httpmiddleware` 循环依赖。
- HTTP 入口中间件在 `c.Request.Context()` 上写入 `requestId`。
- `httputil.WriteError` 的错误日志必须带上 `requestId`。
- 业务层能从 `context.Context` 取到带关联字段的 logger，且保留构造函数中 `Named(...)` 的前缀；只有实际使用该 logger 的事件才承诺关联。
- 非 HTTP 长生命周期路径使用 `streamId`、`cameraId`、`algorithmId` 等业务标识。`requestId` 不跨请求生命周期伪装为流关联 ID。

### R3 日志噪声治理（→ 子任务 A）

- 消除重复 Error，规则写进 spec 并加门禁：请求路径内的意外 5xx 由一个边界记录一条 Error；access 事件记录状态与耗时但不记 Error。
- `WriteError` 对意外 5xx 即使没有内部 cause 也必须留下唯一 Error 事件；预期 4xx 不得仅因携带 cause 就记 Error。仅当请求错误由该边界覆盖时，才删除 service 重复记录；后台循环仍由所属组件记录。
- `AccessLog` 对普通请求使用 `latency_ms`；SSE（`text/event-stream`）与 WebSocket 连接不把连接时长记为 `latency_ms`，改为 start/end 两条独立事件，结束事件带 `session_ms`。
- `Named(...)` 命名统一为 `<domain>.<layer>` 点分隔（如 `camera.handler`、`person.service`）。
- 生产装配不得把缺失 logger 静默降级为 `zap.NewNop()`；测试可显式注入 Nop logger。不得用业务构造函数 panic 实现 fail-fast，装配错误应通过 Fx/provider 错误返回。

### R4 原生层日志桥接（→ 子任务 B）

- `native/include/Zhulong/engine.h` 新增 `Zhulong_log_callback` 与 `Zhulong_set_log_callback`，
  约定 level / module / text 语义与生命周期。回调携带**显式长度参数**（`module_len` / `text_len`），
  避免 Go 侧 `C.GoString` 在实时链路线程上做无谓的 `strlen`。
- C++ 侧提供统一日志宏（`ZL_LOGD/I/W/E`），禁止在 `native/src` 中直接使用 `std::cerr` / `std::cout`（协议输出除外）。
- Go 侧桥接遵守 CGO 指针规则：C 侧只持不透明整数 token（`cgo.Handle`），绝不持有 Go 指针；
  `module` / `text` 仅在回调期间有效，**同步读取，绝不跨 goroutine 保留**。
- C++ capture/decode workers never write synchronously to disk. The callback validates and copies bounded buffers, then performs bounded nonblocking queue admission; a full queue may drop with visible counters. Only queue admission is promised nonblocking; do not promise zero allocation for the full CGO callback.
- Callback buffers exceeding their bound are rejected/dropped and counted; oversized runner lines are drained through newline, dropped/count, and later lines continue to be read.
- `internal/algorunner` uses a bounded line reader and classifies stdout/stderr JSON in this order: runner protocol (`status`/`stage`/`step`, `rc`, `error_present`), native `zhulongLog=true` records (validated level/module/streamId plus message byte count), then unknown lines (source and byte count only). It never logs raw runner error text, arbitrary native message text or unknown line content.
- Runner output is rate-limited per stdout/stderr source, and rate-limit/oversize/read-error counters remain observable.
- FFmpeg 日志保持静默，直到凭据脱敏策略经过独立安全验证。
- Native callback remains valid until the resident engine and probe producers stop and `engine_destroy` returns; then unregister callback/delete `cgo.Handle`, drain/stop the native queue, and finally sync the logger. The runner subprocess and its stdout/stderr readers have a separate lifecycle and must also stop/join before the final logger sync; they do not use the C callback.
- `native/src/algorithm/ipc/unix_socket.cpp` 的裸 `std::cerr` 改为统一宏，消除对 JSON 日志流的污染。


### R5 日志安全（→ 子任务 C）

- 提供统一脱敏入口；已知敏感值在日志调用点显式脱敏。通用 Core 只兜底处理有明确类型/字段键的值，不承诺识别任意拼接文本或任意对象中的秘密。
- 禁止在 `internal/**` 中直接把未脱敏 RTSP URL、凭据、前端任意 payload 等敏感值写入日志。
- 日志注入防护：结构化编码必须保证一条记录不能伪造另一条记录；对用户可控字段清理控制字符并限制长度。
- 明确「日志中不得出现的敏感字段」清单（密码、会话 token、JWT 密钥、RTSP 凭据），并明确黑名单不能发现未知字段中的秘密。

### R6 前端可观测（→ 子任务 C）

- 提供前端统一 logger，支持级别过滤（dev=debug，prod=warn）。
- 生产环境的前端错误不再静默丢弃：批量上报到后端并落盘，必须限流 + 去重，避免一次白屏循环打爆后端。
- 未认证端点除按 IP 限流外还必须有全局请求/字节预算；可信代理范围明确，未知 IP 不得通过 LRU 淘汰反复获得新配额。
- `web/src/lib/handle-server-error.ts` 的 DEV-only `console.log` 改为统一 logger 调用。

## Acceptance Criteria

### 跨子任务（父任务收口，必须全部满足）

- [ ] **AC-1 关联性**：一次必失败 HTTP 请求的 access 事件与 error 事件都带 `requestId=probe-0001`；错误事件包含稳定 `code`/`status`，access 事件不记 Error。
- [ ] **AC-2 无重复**：构造一次意外 DB 5xx，恰有一条 Error 事件；同时覆盖无内部 cause 与 4xx 不记 Error 的情况。
- [ ] **AC-3 结构化纯净**：在 `format=json` 的配置下，初始化后的应用日志流 100% 为合法 JSON；bootstrap 配置/启动失败诊断单独验证，不计入应用日志流。`format=console` 是明确允许的非 JSON 模式，不受本 JSON-lines 断言约束；JSON 与 console 两种编码都要有各自的编码测试。
- [ ] **AC-4 采样可控**：生产默认关闭采样；突发 1000 条未过载的同级别日志，写入记录数 = 1000。队列过载另由 AC-13 验证。
- [ ] **AC-5 运行期调级**：修改实际配置文件的 `logging.level` 并发 HUP 后，新级别生效、PID 不变；非法级别保留最后有效级别。
- [ ] **AC-6 落盘有界**：文件后端活动文件、归档及轮转/压缩临时文件的总字节数不超过已批准 profile 的 `max_total_size_mb`；轮转故障不导致无限追加。journald profile 验证 host 配额与该服务的保留约定。未完成 profile 与预算签核前，不得将此 AC 标记为通过。
- [ ] **AC-7 跨语言**：进程内 C++ pipeline 日志通过 C ABI 回调进入 Go logger，并保留 module 与适用的 `streamId`；runner 插件 API 失败可检索 `module=algorithm.runner`、稳定操作 token 与 numeric `rc`。不得把该通用摘要声称为 backend-specific NPU 诊断。
- [ ] **AC-8 长连接不污染**：普通请求耗时字段为 `latency_ms`；access log 不把 SSE / WebSocket 连接时长记作 `latency_ms`，且 start/end 事件语义明确。
- [ ] **AC-9 无静默兜底**：生产 Fx 装配缺少必需 logger 时启动失败并报告依赖错误；测试可显式注入 `zap.NewNop()`。
- [ ] **AC-10 无裸原生输出**：`grep -rn "std::cerr\|std::cout" native/src` 仅剩协议输出（runner 的 stdout JSON）。
- [ ] **AC-10b FFmpeg 日志保持静默**：`grep -rn "av_log_set_level\|av_log_set_callback" native/src`
      结果仍只有 `rtsp_input.cpp` 的那一处 QUIET + 空回调 —— 不得因「方便排障」而重开（会泄漏 RTSP 凭据）。
- [ ] **AC-11 命名统一**：`grep -rn 'Named("' internal` 结果全部为 `<domain>.<layer>` 点分隔形式。
- [ ] **AC-12 无凭据泄漏**：对 RTSP 有效/畸形 URL、敏感字段、error 字符串和前端上报构造测试，断言明文凭据不进入输出；静态 grep 只作为补充，不作为唯一证明。
- [ ] **AC-13 有界丢失可见**：压满 native/runner 队列和触发 sink 写入错误时，处理线程不执行同步磁盘 I/O；丢弃/写入错误计数可观察。不得宣称断电时零丢失。
- [ ] **AC-14 未认证上报有界**：多 IP 请求仍受已批准部署 profile 中明确数值化的全局请求与字节预算限制；信任代理配置、速率耗尽与未知 IP 行为有测试。预算签核前不得将此 AC 标记为通过。
- [ ] **AC-15 门禁通过**：`make check` 全绿（前端 lint/type-check/test + native-test + api-docs + go-check）。

### 子任务级

各子任务的验收标准见其自身 `prd.md` 的 Acceptance Criteria 段。子任务归档不要求父任务 AC 全部满足；
父任务在**全部子任务归档后**执行最终集成评审。

## Constraints

- **不引入 OTel SDK 依赖**。只预留 `traceId` 字段与注入点，避免为一个字段引入整套可观测性依赖树。
- **新增 Go 依赖需评估**：内置轮转若采用 `gopkg.in/natefinch/lumberjack.v2`，需联网 `go mod tidy`，
  与 `AGENTS.md` 中「`make native-deps` 是唯一联网步骤」的表述冲突，需同步更新该文档。
  若可接受，优先选 journald 路线（零依赖）。
- **不得修改已应用的 SQLite 迁移**（本任务原则上不涉及数据库变更）。
- **C ABI 变更必须向后兼容**：新增 `Zhulong_set_log_callback` 是纯增量接口，不得改动既有函数签名与枚举值。
- **回调线程不是 Go 创建的线程**，回调实现中不得假设 GMP 调度语义。
- **业务路径无 panic**（`AGENTS.md` 硬性约定）。缺失必需依赖应由 Fx/provider 在启动期返回错误，不得用业务构造函数 panic 来模拟 fail-fast。
- 测试与源码同目录；Go 的 `-race` 强制；engine 相关测试必须走 `native/scripts/build.py` wrapper。

## 已识别的风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 异步队列满或 sink 写入失败 | 排障证据可能不完整 | 计数与 sink 健康状态可观察；定义有界丢失预算，不承诺绝对不丢 |
| 删除 service 层重复日志时边界漏记 | 证据缺失 | 先加 5xx/error-level 回归测试，再逐点核对错误归属 |
| SIGHUP 与配置文件写入竞态 | 级别读错或短暂无效 | 配置以原子替换写入；只解析 level，失败时保留最后有效级别 |
| C++ 日志点铺开 40+ 文件，工作量大 | 子任务 B 工期失控 | 按模块分批，优先 pipeline / backends / nodes |
| `std::cerr` 清理遗漏 | JSON 流被污染 | 加静态门禁并测试独立协议/日志输出路径 |
| 有人为「方便排障」重开 FFmpeg 日志 | **RTSP 凭据泄漏到日志** | AC-10b 静态门禁 + `log.hpp` 与 `initialize_ffmpeg` 处显式注释警告 |
| runner schema 混用导致 Go 侧解析歧义 | 协议事件被当日志、日志被当协议 | `zhulongLog: true` 判别字段 + 有序三路分流；未知行只记录来源与长度，不记录原文 |

## 待确认的部署决策

- 支持的部署 profile（systemd/journald、容器或无 systemd 裸机）及各 profile 的实际日志收集通道。
- 每类设备可分配给运行日志的磁盘预算、峰值日志速率与所需最短保留期；文件 profile 必须提供 `max_total_size_mb`，并说明是否将压缩临时空间计入预算。在预算确定前，不承诺固定 30 天保留；AC-6 与 AC-14 在相关 profile/预算签核前保持未通过。
- 原生部署目标与版本矩阵（例如 RKNN Runtime/Toolkit 与 BSP，或其他 backend）；跨硬件性能验收必须在对应 profile 实测。

