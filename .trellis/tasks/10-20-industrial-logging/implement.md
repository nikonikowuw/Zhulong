# 执行计划：工业级运行日志体系（父任务）

> 父任务**不直接实施代码**。本文件定义子任务映射、执行顺序、跨子任务验证门禁与最终集成评审流程。

## 0. 前置阅读

动手前按顺序读：

1. `.trellis/spec/backend/logging-guidelines.md` —— 既有 zap 规范与审计日志边界（本任务不改审计行为）
2. `.trellis/spec/backend/middleware-guidelines.md` —— 中间件顺序约束（`Recovery` 必须最外层）
3. `.trellis/spec/architecture.md` —— 跨层拓扑与 C ABI 边界
4. `.trellis/spec/native/` —— ABI 与原生层规范（子任务 B 必读）
5. `AGENTS.md` —— 命令来源（`Makefile`）与硬性约定

## 1. 子任务映射

| 子任务目录 | 交付物 | 优先级 | 前置依赖 |
| --- | --- | --- | --- |
| `10-20-logging-core-hardening` | 配置化 logger + `logctx`(C-1) + 去重 + AccessLog 分流 + 命名统一 + `nativelog.Queue`(C-3) | P0 | 无 |
| `10-20-native-log-bridge` | C ABI 回调(C-2) + C++ `ZL_LOG*` 宏 + 裸 `std::cerr` 清理 + runner 结构化解析 | P0 | **必须等 A 交付 C-1 / C-3** |
| `10-20-logging-safety-and-frontend` | 统一脱敏器 + 日志注入防护 + 前端统一 logger 与错误上报 | P1 | **必须等 A/B 归档**，避免并发修改共享日志路径 |

## 2. 执行顺序与门禁

```txt
  ① 子任务 A：logging-core-hardening
       ├─ 实施 → 自检（trellis-check）
       ├─ 门禁 G-A：AC-1 / AC-2 / AC-4 / AC-5 / AC-6 / AC-9 / AC-11 全绿
       └─ 归档

  ② 子任务 B：native-log-bridge
       ├─ 前置检查：确认 logctx (C-1) 与 nativelog.Queue (C-3) 已在目标分支落地
       ├─ 实施 → 自检
       ├─ 门禁 G-B：AC-7 / AC-10 全绿
       └─ 归档

  ③ 子任务 C：logging-safety-and-frontend
       ├─ 实施 → 自检
       ├─ 门禁 G-C：子任务 C 的 AC-C1～AC-C12、父任务 AC-12 / AC-14 与前端门禁通过
       └─ 归档

  ④ 父任务集成评审（本文件 §4）
       ├─ 跨子任务联合验证：AC-1 / AC-3 / AC-7 / AC-12 / AC-13 / AC-14
       ├─ 全量门禁：make check
       └─ 父任务归档
```

**顺序硬约束**：② 不得早于 ① 归档。理由见 `design.md` §4 —— 在回调通道的丢弃语义定下来之前铺 C++ 日志点会返工。

## 3. 各子任务的执行要点（父任务视角的提醒）

### 3.1 子任务 A 要点

- `logctx` 包的依赖白名单（只允许 `context` + `zap`）必须在 code review 时逐个 import 核对。
  一旦它 import 了 `gin` 或 `httputil`，整个「打破循环依赖」的设计目标就废了。
- **删 service 层重复日志是本任务风险最高的动作**。每删一处，必须先确认对应的
  `apperr.Internal(code, msg, err)` 把原始 `err` 传进了 `Err` 字段。
  建议做法：先给 `WriteError` 加一条回归测试（构造 `Err != nil` 的 apperr，断言有 Error 日志），
  再动 service 层。
- `NewProductionConfig` 的隐式采样必须显式化。改造后用 1000 条突发日志验证落盘条数。
- 时间戳 UTC 化会影响既有日志消费方（如果有脚本按本地时间 grep）。改动前先 `grep` 仓库内是否有解析日志的脚本。

### 3.2 子任务 B 要点

- C 侧枚举与 zap level 的映射必须写成显式函数 + 单元测试，不要依赖数值巧合。
- Callback validates pointer/length pairs and maximum sizes, then synchronously copies C-owned bytes with `C.GoStringN` before bounded queue admission. The copy allocates; benchmark the complete callback. Do not retain native pointers.
- `TryEnqueue` admission is bounded and nonblocking: a full/contended queue drops and increments a counter. Do not promise zero allocation for the queue or full CGO callback.
- Replace the default `Scanner` path with a bounded reader that drains oversized lines through newline, counts them, and continues; handle terminal read errors explicitly.
- C++ 日志点按已批准的目标分批推进：`pipeline/` → runner 插件 API 边界 → `nodes/` → `ZhulongAlgoIpc` 的 DMA-BUF allocator。backend/runtime 不链接 `ZhulongLog`，不得承诺后端内部细粒度诊断。

### 3.3 子任务 C 要点

- 脱敏器必须在 A/B 的日志调用点与 runner 日志路径稳定后再收口，否则共享调用点会被多个子任务先后修改。
- Frontend event limits use the defaults in subtask C's design (5-second interval, 20-entry threshold, 3 events/second per `(level,event)`); exercise them under a crash-loop simulation and change only with measured evidence. The process-wide endpoint request/byte budget still requires deployment-profile signoff.
- 日志注入防护要覆盖 console 编码路径 —— JSON 编码下换行会被转义，console 编码下不会。

## 4. 父任务最终集成评审

三个子任务全部归档后执行。

### 4.1 跨子任务联合验证

| 项 | 方法 | 期望 |
| --- | --- | --- |
| 联合-1（对应 AC-1） | Send one expected 5xx HTTP request and collect access/error events | Both carry the same `requestId`; exactly one Error event |
| 联合-2（对应 AC-3） | `format=json` production profile, 10-minute JSON-lines validation; separately exercise `format=console` | Every application line in the JSON profile parses as JSON; console output is validated as console, and bootstrap diagnostics are checked separately |
| 联合-3（对应 AC-12） | Review new A/B log call sites and collected output | No plaintext credentials; RTSP credential-shaped pattern has zero matches |
| 联合-4（对应 AC-7） | Drive a native stream with a known `stream_id` | Go/C++ native events share `streamId`; HTTP `requestId` is not reused as stream identity |
| 联合-5（对应 AC-13 / AC-14） | Saturate native/runner queues and exceed the client-log process budget | Drops/errors are visible without producer-side sink I/O; public route returns 429 under the signed request/byte budget |

### 4.2 全量门禁

```bash
make check          # 前端 lint/type-check/test + native-test + api-docs + go-check
```

补充手工验证（自动化覆盖不到的部分）：

```bash
# 关联性
curl -si -H 'X-Request-ID: probe-0001' http://127.0.0.1:8000/api/v1/cameras/nonexistent | head -1
grep '"requestId":"probe-0001"' <log> | wc -l        # 期望 >= 2

# 运行期调级（PID 必须不变）
PID=$(pidof Zhulong); kill -HUP $PID; sleep 1; pidof Zhulong    # 期望 == $PID

# 跨语言
grep '"module":"pipeline\.' <log> | head -1
# runner plugin API failures are classified under algorithm.runner with safe operation tokens and numeric rc

# 无凭据泄漏
grep -iE 'rtsp://[^"]*:[^"@]*@' <log> | wc -l        # 期望 0
```

### 4.3 集成评审退出条件

- [ ] `prd.md` 的 AC-1～AC-15 全部满足
- [ ] 联合-1～联合-5 全部通过
- [ ] `make check` 全绿
- [ ] 三个子任务均已归档
- [ ] `.trellis/spec/backend/logging-guidelines.md` 已按 Phase 3.3 更新（记录去重规则、采样策略、UTC 约定、`logctx` 用法、原生日志通道）
- [ ] AC-6 / AC-14 对应的部署 profile、数值预算、保留期与进程级请求/字节上限已签核；没有签核时不得归档父任务

## 5. 回滚点

> 契约 C-3 已在规划期由 `AsyncCore` 修订为 `nativelog.Queue`（理由见 `design.md` §3.1.1 与 §3.3）。
> 本节已同步。

| 阶段 | 回滚点 | 动作 |
| --- | --- | --- |
| A 完成后发现配置不兼容 | `[logging]` 新键全部有零值默认 | 删除新键即可，无需回滚代码 |
| A 删日志导致证据缺失 | 恢复 service 层 `logger.Error` | ⚠️ 同步确认 `apperr.Err` 非空，否则从「重复」退化为「全丢」 |
| B 回调导致实时链路抖动 | Revert/disable native sink registration | Native logs are omitted; Go logging remains available |
| B 队列溢出导致 native 日志丢失 | 调高 `min_level` 或缩小低级别输出范围 | 只保留高价值日志；丢弃计数仍须可观察 |
| C 前端上报回滚 | Revert frontend logger to local sanitized events | Do not reintroduce raw exceptions/stacks or add an undocumented runtime switch |
