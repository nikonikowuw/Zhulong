# 原生层日志桥接

> 父任务：`.trellis/tasks/10-20-industrial-logging` · 前置依赖：**子任务 A（`logctx` C-1 + `nativelog.Queue` C-3）必须先归档**

## Goal

打通 CGO 边界之后的日志黑洞：让进程内 C++ pipeline、nodes 与 IPC 日志汇入 Go 侧结构化流；独立 algo runner 的日志被结构化解析。算法共享库 `ZhulongAlgoRuntime` 保持不接入日志核心，插件操作失败只在 runner API 边界记录安全摘要，不声称具备后端内部诊断。

三条具体目标：

1. **可追溯**：进程内 pipeline 日志可按稳定 `module` 和 `streamId` 关联；插件模型/worker/process API 失败可按 `module=algorithm.runner`、操作 token 与 numeric `rc` 检索。此 runner 摘要不识别 RKNN/ONNX/RGA 内部具体失败原因。
2. **不破坏结构化流**：应用日志与 runner 协议保持独立；每条 native 日志要么是完整合法 JSON，要么计数丢弃，不得静默输出被截断的半条记录。
3. **不拖慢实时链路**：C++ 采集/解码线程不做同步 I/O，只向有界队列非阻塞入队；超载允许计数丢弃。CGO 字符串复制成本必须测量，不承诺整个回调零分配。

## 背景

现状（已勘察）：

| 位置 | 现状 |
| --- | --- |
| `native/src/`（59 个 `.cpp`/`.hpp`） | **零日志设施**。无 spdlog / glog / 任何 logger |
| `native/src/algorithm/ipc/unix_socket.cpp:375,377` | 裸 `std::cerr` 打印异常 |
| `native/src/algorithm/runner/runner.cpp` | stdout 输出协议 JSON（`{"status":...,"stage":...}`）；stderr 输出失败 JSON |
| `internal/algorunner/process.go:234` | runner 的每行日志被 `zap.String("text", text)` 原样塞入，**未解析 JSON** |
| `internal/algorunner/process.go:292` | 当前记录 broker socket 路径；B 将不再输出该字段，等待 C 完成安全策略后再评估是否需要安全摘要 |
| `native/include/Zhulong/engine.h` | 无日志相关接口 |

**库拓扑（已勘察 `native/CMakeLists.txt`）**：

```txt
ZhulongAlgoIpc (STATIC)  ── dmabuf_allocator.cpp, shm_pool.cpp, unix_socket.cpp, frame_broker.cpp
    │  PUBLIC include: sdk/include, include, src
    │
    ├──▶ ZhulongEngine (STATIC)  ── abi/engine.cpp, pipeline/*, nodes/*
    │        │  links PUBLIC ZhulongAlgoIpc + 静态 FFmpeg
    │        ▼
    │    [Go CGO 静态链接 → 进程内]
    │
    └──▶ zhulong_algo_runner (EXECUTABLE) ── runner.cpp
             │  links ZhulongAlgoIpc + ZhulongAlgoRuntime
             ▼
         [独立进程 → Go 经 os/exec 管道读取 stdout/stderr]
```

⚠️ **关键推论**：`ZhulongAlgoIpc` 被**进程内引擎**和**独立 runner 进程**同时链接。
因此日志设施必须在这两种环境下都能工作，而两者的「日志出口」完全不同：

- 引擎进程（CGO 内）：出口是 Go zap（经 C ABI 回调）
- runner 进程（独立）：出口是 stderr（Go 侧管道读取）

### ⚠️ 硬安全约束（勘察发现，必须锁死）

`native/src/nodes/capture/rtsp_input.cpp:271` 的 `initialize_ffmpeg()`：

```cpp
void initialize_ffmpeg() {
    static std::once_flag once;
    std::call_once(once, [] {
        // 进程级别静默 FFmpeg 日志，防止 RTSP 认证凭据打入控制台或日志文件
        av_log_set_level(AV_LOG_QUIET);
        av_log_set_callback([](void *, int, const char *, va_list) {});
        if (avformat_network_init() < 0) throw Failure{Zhulong_ERR_IO};
    });
}
```

**FFmpeg 默认日志会打印完整 URL（含 `rtsp://user:password@host`）。**
因此：

- 🚫 本任务**严禁**重新打开 FFmpeg 日志（`av_log_set_level` 提高等级）。
- 🚫 本任务**严禁**安装 `av_log_set_callback` 把 FFmpeg 日志转发到日志 sink —— 那等于把凭据接进结构化日志管道。
- ⚠️ 代价是**丢失全部 FFmpeg 诊断信息**（`Connection timed out` / `Invalid data found when processing input` 等）。
  这对现场排障是真实损失。**恢复 FFmpeg 诊断需要配套的 URL 脱敏回调，属于独立决策，不在本任务范围内**（见 §Scope 排除项与 §风险）。

## Scope

**包含**：

- `native/include/Zhulong/engine.h`：新增 `Zhulong_log_callback` 与 `Zhulong_set_log_callback`（契约 C-2）
- 新建 `native/src/common/log.hpp` + `log.cpp`，提供 `ZL_LOGD/I/W/E(stream_id, module, ...)` 宏
- `native/CMakeLists.txt`：新增 `ZhulongLog` STATIC 库，接入 `ZhulongEngine` 与 `ZhulongAlgoIpc`
- `native/src/algorithm/ipc/unix_socket.cpp`：裸 `std::cerr` → `ZL_LOGE(0, ...)`，不记录异常原文或 socket 路径
- `internal/engine`：CGO 桥接（`cgo.Handle` token + level 显式映射 + 接入 `nativelog.Queue`）
- `internal/app`：启动队列、注册 callback；停机时先停止并销毁 native producer，再注销 callback/delete handle、Stop/drain 队列、Sync logger。
- `internal/algorunner/process.go`：runner stdout/stderr 结构化解析、bounded line read/drain、限流与丢弃计数。
- 不在本任务输出 `broker_socks` 路径；如后续确有诊断需求，只能由子任务 C 接入 `logsafe.Paths` 后增加。
- 按优先级分批铺开安全日志点（pipeline → runner 插件 API 边界 → nodes → `ZhulongAlgoIpc` 的 platform 分配路径），显式传递可用的 stream context。`ZhulongAlgoRuntime` 中的 backend/platform 实现不直接依赖日志设施。

**不包含**：

- 🚫 重新启用 FFmpeg 日志 / 安装 `av_log_set_callback`（见上方硬安全约束）
- 不改变 `runner.cpp` 的日志 writer 启停及 stdout 协议格式；可在 runner 插件 API 失败边界增加 `ZL_LOG*` 安全摘要（stderr 结构化流）。
- 在 `ZhulongAlgoRuntime`（.so）内部铺日志 —— 后端与共享 allocator 保持无日志核心依赖；runner 在插件 API 边界记录安全失败摘要，platform allocator 仅在 `ZhulongAlgoIpc` 静态构建变体启用直接日志。
- 前端、Go HTTP 层、脱敏器（→ 子任务 C）
- 指标 / tracing

## Requirements

### R1 C ABI 接口（契约 C-2）

- `Zhulong_log_callback` 与 `Zhulong_set_log_callback` 按父任务 `design.md` §3.2 定义，纯增量。
- 回调包含 `stream_id`、`module_len` 与 `text_len`；`stream_id=0` 表示无单一流上下文。
- 必须在第一个 `Zhulong_engine_create` 之前注册；注销只能发生在所有常驻引擎/probe producer 停止且 `engine_destroy` 返回之后。
- sink/token 不得在 producer 活跃时并发修改；注册状态需由生命周期 quiescence 或明确同步保证。
- 在 `engine_abi_test.c`（纯 C 测试）中覆盖注册、回调、长度与 stream ID、注销。

### R2 C++ 日志设施

- `native/src/common/log.hpp` 提供带显式 stream context 的 `ZL_LOGD/I/W/E(stream_id, module, fmt, ...)` 宏。
- **禁止**在 `native/src/**` 中直接使用 `std::cerr` / `std::cout`（`runner.cpp` 的协议输出除外）。
- `runner.cpp` 在启动 worker 前启动有界日志 writer，并在 producer 退出后 drain/停止；stdout 协议不变。
- 行序列化固定容量，转义后仍需保留完整 JSON 结尾；若超限则生成合法截断记录或计数丢弃，不得 `snprintf` 后直接写半条 JSON。
- writer 对 pipe 使用单次有界行写入；处理 EINTR/短写/写错误。输出队列满与 sink 错误均计数并在可恢复时上报。
- `log_write` 必须 `noexcept`、不得跨 ABI 抛异常。固定栈缓冲可避免 C++ 格式化堆分配，但不等于 writer 线程不会阻塞。
- sink 注册失败必须使 native logging 初始化失败并向 Fx 返回启动错误，不可继续运行后只留下普通 Warn。

### R3 CGO 桥接

- `internal/engine` 是唯一可 `import "C"` 的包。
- token 使用 `cgo.Handle`（整数），绝不传裸 Go 指针；`Handle.Value()` 只在回调确认仍注册的生命周期内调用，注销后不得再有 producer。
- `module` / `text` 由 C++ 持有且只在回调期间借给 Go；Go 必须同步 `C.GoStringN` 复制为 Go-owned strings（由 Go GC 管理），入队后不得保留/`C.free` 借用指针。
- C callback requires a non-null module pointer with `1 <= module_len <= 128`; `text_len` is in `[0, 2048]` and a null text pointer is allowed only when the length is zero. Invalid pairs are counted/dropped before conversion; cast to `C.int` only after bounds checks.
- C 侧枚举到 `zapcore.Level` 显式映射 + 单元测试。
- 回调只入 `nativelog.Queue`，不做磁盘/网络 I/O。`C.GoStringN` 的分配与全回调延迟纳入 benchmark。

### R4 日志点铺开

- 按优先级分批，**不得一次铺满 59 个文件**：
  1. `pipeline/`（10 文件）—— 流生命周期、订阅、背压
  2. `algorithm/runner/runner.cpp` 插件 API 边界—— 模型、worker、task、process 的稳定失败 token 与 numeric `rc`；不触及 `ZhulongAlgoRuntime` 内部
  3. `nodes/`（10 文件）—— 采集、解码、MPP
  4. `platform/dmabuf_allocator.cpp` 的 `ZhulongAlgoIpc` 编译变体—— 分配失败；`ZhulongAlgoRuntime` 编译变体保持无日志核心依赖
- 日志内容**不得包含**：RTSP URL、凭据、大块帧数据、二进制 blob。
- 每处日志必须带 `module` 标签（如 `pipeline.stream` / `algorithm.runner` / `nodes.decode` / `platform.dmabuf`）。
- 失败事件使用稳定 reason/code 与 allowlisted numeric context；不得写 `what()`、vendor error prose、用户输入、路径或任意自由文本。
- 关键失败路径必须有日志：初始化失败、runner 插件 API 操作失败、解码失败、订阅释放、背压丢帧。backend 内部错误不在本任务直接观测范围内；runner 只记录通用操作 token 与 numeric `rc`。

### R5 runner 日志结构化解析

- `internal/algorunner/process.go` classifies parsed stdout/stderr JSON in this order:
  1. **Protocol event** (`status` / `stage` / `step`) → validated status, stage, step, `rc` and `error_present`; never log raw error text.
  2. **Native log record** (`zhulongLog: true`) → validated `level`, `module`, `streamId` and message byte count; omit arbitrary message text.
  3. **Unknown line** → record source and byte count only; never log the raw line.
- Use a bounded line reader: retain at most 64 KiB; drain oversized lines through newline, count/drop them, and continue with subsequent lines. A terminal read error is recorded with a stable reason and ends that reader.
- Apply bounded rate limiting per stdout/stderr source; rate-limit, oversize and read-error counters remain observable.

### R6 生命周期与停机顺序

- 启动：`nativelog.Start()` → 注册 C ABI sink/handle → `native.Start()`；注册失败必须回滚并使启动失败。
- 停止 producer：先关 HTTP/业务入口与 runner 子进程，并 join stdout/stderr readers；然后 `native.Stop()`；此时 callback、handle、队列消费者仍有效。
- `native.Close()` 完成 `engine_destroy` 时 callback/queue 仍有效，以接收销毁期同步日志。
- 确认 engine/probe producers 已 quiesce 后注销 callback、删除 handle；之后 `nativelog.Stop(ctx)` 排空。所有 logger producer（包括 runner readers）停止后，最后 `logger.Sync()`。
- 停止/销毁失败而无法证明 quiescence 时，禁止注销/free callback token 或停止队列；安全优先于回收。
- `g_sink` / token 不得被 producer 并发读写；用 lifecycle quiescence 或同步快照，禁止无同步普通全局变量并发修改。

## Acceptance Criteria

- [ ] **AC-B1 ABI 增量**：`Zhulong_set_log_callback` 在 `engine_abi_test.c`（纯 C）中可注册、可注销、
      传 NULL 不崩溃；既有 ABI 测试全绿。
- [ ] **AC-B2 端到端跨语言**：运行引擎后 `grep '"module":"' <log>` 能命中 C++ 侧产生的条目。
- [ ] **AC-B3 结构化纯净**：logger 初始化后的应用日志流与 runner protocol/log streams 分别按各自 schema 校验；超长输入不产生半条 JSON。
- [ ] **AC-B4 无裸原生输出**：`grep -rn "std::cerr\|std::cout" native/src` 仅剩明确的 runner 协议输出。
- [ ] **AC-B5 有界 producer**：队列饱和时 native producer 不等待磁盘 I/O；基准报告 C ABI 回调真实分配数和延迟，不以 `TryEnqueue` 代替整条路径性能。
- [ ] **AC-B6 丢弃可见**：压满 Go/native 与 runner 输出队列时丢弃计数准确且运行期可观察。

- [ ] **AC-B7 level 映射正确**：单元测试证明 `Zhulong_LOG_ERROR(3) → zapcore.ErrorLevel(2)` 等四条映射正确，
      且非法值有确定回退。
- [ ] **AC-B8 runner 结构化**：协议事件的 `stage`、`rc` 独立字段可检索；原始 `error` 字符串不在安全脱敏交付前输出。
- [ ] **AC-B9 runner 限流**：runner 连续刷 10000 行，输出条数受控且丢弃计数被观察。
- [ ] **AC-B10 超长行恢复**：喂入超出 64 KiB retained-line 上限的数据后，该行被有界丢弃并计数；后续合法行仍能读到；terminal reader error 被记录并结束当前 reader。

- [ ] **AC-B11 FFmpeg 日志仍静默**：`grep -rn "av_log_set_level\|av_log_set_callback" native/src`
      结果仍只有 `rtsp_input.cpp` 的那一处 QUIET + 空回调。
- [ ] **AC-B12 无凭据泄漏**：`grep -iE 'rtsp://[^"]*:[^"@]*@' <log>` 结果为空。
- [ ] **AC-B13 停机顺序**：事件顺序测试证明 runner subprocess/readers 已停止并 join，native Stop/Close 完成后才注销 callback/delete handle，随后 queue Stop/drain，最后 logger Sync；在途回调均安全完成。
- [ ] **AC-B14 门禁通过**：`make check` 全绿（含 `native-test` 与 `run_go_bridge_tests.py`）。

## Constraints

- **`ZhulongEngine` 是静态库，经 CGO 静态链接进 Go 二进制**。任何新增的 `.cpp` 必须加入 CMake target，
  且不得引入新的动态依赖（`AGENTS.md`：构建期不下载、不动态链接 FFmpeg）。
- **交叉编译只允许编译，不允许执行测试**（`AGENTS.md`）。
- **`ZhulongAlgoRuntime`（.so）不铺日志** —— 它及其中的 backend/shared allocator 不依赖 `ZhulongLog`；runner 在 plugin API 边界输出通用 operation token/rc，不能声称提供 backend 内部诊断。
- **`runner.cpp` 的 stdout 是协议，不是日志**。修改其格式会破坏 Go 侧解析，属于 ABI 级变更。
- C++ 异常绝不穿越 ABI 边界。`log_write` 必须 `noexcept`。
- Go 侧涉及 `internal/engine` 的测试必须走 `native/scripts/build.py` wrapper。
- CGO 指针规则（`AGENTS.md`）：绝不向 C 传 Go 指针；回调 token 用 `cgo.Handle` 整数。

## 风险

| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| 回调在 C++ worker 线程同步写盘 | 直接注入延迟造成丢帧 | 回调只入队（`nativelog.Queue`）；基准测试守门（AC-B5） |
| 队列溢出丢日志且不报告 | 「日志很干净」的假象，排障方向被误导 | 丢弃计数补齐上报（AC-B6） |
| 有人「顺手」重开 FFmpeg 日志以方便排障 | **RTSP 凭据泄漏到日志** | AC-B11 加静态门禁；在 `log.hpp` 与 `initialize_ffmpeg` 处写显式注释警告 |
| 一次铺满 59 个文件的日志点 | 工期失控、review 质量下降 | 强制分批（pipeline → backends → nodes → platform），每批独立提交 |
| `cgo.Handle` 泄漏 | 内存泄漏 + token 复用导致串号 | 创建/删除配对；测试断言 handle 数量 |
| runner 日志 schema 混用 | Go 侧解析歧义 | `zhulongLog: true` 判别字段；三路分流（R5） |
| `bufio.Scanner` 默认 64 KiB 上限 | 长 JSON 行被截断，丢失后续 runner 事件 | 替换为显式有界 reader：超限 drain/count 后继续，测试覆盖后续行与终止 I/O 错误（AC-B10） |

## 排除项说明（需单独决策）

**恢复 FFmpeg 诊断日志**。当前 `AV_LOG_QUIET` + 空回调是为了防凭据泄漏，
代价是丢掉了 FFmpeg 的全部错误诊断（现场最常见的「拉流失败但不知为何」就源于此）。

**恢复 FFmpeg 诊断日志**。当前 `AV_LOG_QUIET` + 空回调是为了防凭据泄漏，代价是丢掉 FFmpeg 的诊断信息。恢复它必须独立评审日志格式与所有凭据出现形式的覆盖，证明无法识别的任意文本不会进入生产日志；本任务不提供或启用通用 `sanitize_credentials` 实现。

**在这些问题有答案之前，保持 QUIET。** 安全姿态的变更不能顺手做。
