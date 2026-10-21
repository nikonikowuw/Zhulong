# 执行计划：原生层日志桥接

> 父任务：`.trellis/tasks/10-20-industrial-logging`
> **前置门禁：子任务 A（`logctx` C-1 + `nativelog.Queue` C-3）必须先归档**

## 0. 前置检查（不通过则不开工）

```bash
# 1) 子任务 A 的交付物必须已在目标分支落地
test -d internal/logctx && echo "C-1 OK" || echo "BLOCKED: logctx missing"
test -d internal/nativelog && echo "C-3 OK" || echo "BLOCKED: nativelog missing"

# 2) 确认 C-1 / C-3 的 API 与 design.md 一致
grep -n "func WithRequestID\|func RequestID\|func Logger" internal/logctx/logctx.go
grep -n "func (q \*Queue) TryEnqueue\|func (q \*Queue) Start\|func (q \*Queue) Stop\|func (q \*Queue) Dropped" internal/nativelog/queue.go
```

若 API 与 `design.md` 不符 → **先更新 `design.md`，再开工**。不要在 C++ 侧先铺日志点。

## 0.1 前置阅读

1. `.trellis/spec/native/` 全部（ABI 与原生层规范）
2. `.trellis/spec/architecture.md` —— 跨层拓扑与 C ABI 边界
3. `.trellis/spec/backend/logging-guidelines.md` —— 更新后的日志规范
4. `native/include/Zhulong/engine.h` —— 唯一公开头文件
5. `native/src/abi/engine.cpp` 头部注释 —— `boundary()` 异常防爆门与 `in_callback` 重入防御
6. **本任务 `design.md` §2**（C-2 签名修订）与 §3.2（C++ 实现）—— 动手前必读
7. `native/src/nodes/capture/rtsp_input.cpp:271` —— ⚠️ **FFmpeg 日志静默的硬约束来源**

## 1. 开工前的关键核实

```bash
# ⚠️ 确认 FFmpeg 日志静默约束仍然存在（若已被移除，本任务的安全假设失效）
grep -n "av_log_set_level\|av_log_set_callback" -B3 -A3 native/src/nodes/capture/rtsp_input.cpp

# 确认库拓扑未变（design.md §1 的依据）
grep -n "add_library\|add_executable\|target_link_libraries" native/CMakeLists.txt

# 确认 ZhulongAlgoIpc 被引擎与 runner 同时链接（日志设施必须双环境可用）
grep -n "ZhulongAlgoIpc" native/CMakeLists.txt

# 确认 Go 侧 runner 解析现状
sed -n '230,250p' internal/algorunner/process.go
```

**若上述任一与 `design.md` 不符 → 先更新 design，再动手。**

## 2. 实施顺序

### 阶段一：C++ 日志设施（先立骨架，不加日志点）

- [x] **1.1 新建 `native/src/common/log.hpp`**
  - 定义 `LogLevel` / 含 `stream_id` 的 `LogSink` / `set_log_sink` / `log_write` / `start_runner_log_writer` / `stop_runner_log_writer` 与四个显式 streamId 宏。
  - sink/token 只在 producer quiescent 时更改；`log_write` 不做 I/O、不等待 writer。

- [x] **1.2 新建 `native/src/common/log.cpp`**
  - producer 固定容量格式化后入 Go callback 或 runner 有界队列；runner MPSC ring 容量为 256 条；不在 producer 上进行 stderr I/O。
  - 独立 writer thread 序列化完整合法 JSON 行，处理转义、无效 UTF-8、超限、短写/EINTR 与失败计数。
  - writer 阻塞/输出队列满时 producer 有界返回并计数丢弃；CGO callback 的分配和延迟单独 benchmark。
  - 验证：`make native-build`。

- [x] **1.3 CMake 接入**
  - 新增 `ZhulongLog` STATIC 库；`ZhulongAlgoIpc` 追加 `PUBLIC ZhulongLog` 和 `Threads::Threads`。
  - runner main 在 worker 启动前创建日志 writer，producer 退出后 drain/stop；失败导致启动失败。
  - **绝不**把 `ZhulongLog` 加进 `ZhulongAlgoRuntime`（.so）。
  - 验证：`make native-build` 成功；`nm` 检查符号只出现一次
    ```bash
    nm -C build/native/libZhulongEngine.a 2>/dev/null | grep -c "zhulong::set_log_sink"
    ```

- [x] **1.4 C++ 单元测试**
  - 新建 `native/tests/native_log_test.cpp`：
  - writer 输出完整 JSON；测试转义、无效 UTF-8、超限、队列满和 writer 错误计数。
  - 故意阻塞 writer 时并发 producer 仍有界返回；记录基准延迟/丢弃，不断言整条 CGO 路径零分配。
  - 在 `native/CMakeLists.txt` 的 `BUILD_TESTING` 段注册可执行文件。
  - 验证：`make native-test`

### 阶段二：C ABI

- [x] **2.1 `native/include/Zhulong/engine.h` 追加接口**
  - 按 `design.md` §2.2 签名（含 `stream_id`、module/text 长度）；纯增量，不改既有函数与枚举。
  - 测试注册、callback 内容/长度/streamId、注销和在 producer quiescent 后才可注销的顺序。

- [x] **2.2 `native/src/abi/engine.cpp` 实现**
  - 追加 `Zhulong_set_log_callback`，用 `boundary()` 包裹（自动获得 `noexcept` + 重入防御）。
  - 加 `static_assert(std::is_same<Zhulong_log_callback, zhulong::LogSink>::value, ...)`（`design.md` §3.4）。
  - 验证：`make native-build`

- [x] **2.3 C ABI 测试（纯 C）**
  - `native/tests/engine_abi_test.c` 追加：注册 → 注销 → 传 NULL 不崩溃。
  - ⚠️ 必须在 `Zhulong_engine_create` **之前**注册，测试要覆盖这个顺序要求。
  - 验证：`make native-test`（对应 AC-B1）

### 阶段三：清理裸 std::cerr

- [x] **3.1 `native/src/algorithm/ipc/unix_socket.cpp`**
  - 两处 `std::cerr` → `ZL_LOGE(0, "ipc.unix_socket", ...)`（无 stream context 用 0）。
  - 不记录 `e.what()`、socket path 或 client identifiers；`#include "common/log.hpp"`。
  - 验证：`make native-build && make native-test`
  - 验证（AC-B4）：
    ```bash
    ! grep -rn "std::cerr\|std::cout" native/src --include="*.cpp" | grep -v "runner/runner.cpp"
    ```

### 阶段四：Go 侧 CGO 桥接

- [x] **4.1 `internal/engine` 新增 `log_bridge.go`**
  - 按 `design.md` §4 实现。
  - ⚠️ `Engine` 结构体加 `logHandle cgo.Handle` 字段。
  - ⚠️ `//export zhulongLogTrampoline` 必须在 `import "C"` 之上的 C 块中有前向声明，且参数顺序严格一致。

- [x] **4.2 level 映射测试（AC-B7）**
  - 表驱动测试 `zapLevel` / `cLogLevel`，覆盖四档及非法值。
  - 测试 module 长度 0/1/128/129、text 长度 0/2048/2049 与空指针组合；确认 `RecordDrop()` 增加计数，只有校验通过后才转换为 `C.int`。
  - 对 `zhulongLogTrampoline` 做 `-benchmem` 基准并报告 callback 延迟/allocs。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestZapLevel`

- [x] **4.3 trampoline 测试**
  - `cgo.Handle` 仅在 engine producers quiesced、callback 注销后删除；生命周期测试验证在途 callback 先完成。
  - 限制 module/text 字节长度，拷贝 C 内存后附加 streamId、SeenAt 并调用 `TryEnqueue`。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/engine/ -run TestNativeLog`

### 阶段五：runner 日志结构化解析

### 阶段五：runner 日志结构化解析与安全约束

- [x] **5.1 协议/native/unparsed 分流（AC-B8）**
  - 按 `design.md` §5.2；协议事件仅记录安全 token、`rc` 和 `error_present`，不写原始 error。
  - native 行仅记录经过校验的 module、streamId 和 message byte count；`logsafe` 不是通用秘密检测器，不授权输出任意 message 正文。
  - 未解析行只记来源与字节数，不落原始行。
  - 验证 `python3 native/scripts/build.py go test -race ./internal/algorunner/ -run TestStreamLogs`。

- [x] **5.2 有界读取与超长行恢复（AC-B10）**
  - 用 `bufio.Reader` 和 `readBoundedLine`，单行最多保留 64 KiB；超限后 drain 到 newline 并继续读下一行。
  - 底层不可恢复的 reader error 记录稳定原因后结束该 reader；不要把 Scanner 超限误作永久退出。
  - 测试：超长行之后合法事件仍被解析；EOF 尾行、读错误、嵌入换行也有覆盖。

- [x] **5.3 限流与丢弃可见（AC-B9）**
  - 对 runner protocol/native/unparsed 分支按来源限流，每秒最多 100 条；对超出条数计数并在可写时补报。
  - 验证：10000 行输入下日志数量受控，且 drop 计数可观察。

- [x] **5.4 不输出原始敏感字段**
  - 删除 `broker_socks` 字段和未解析行正文；不调用未存在的 `logsafe` API，也不新增占位 sanitizer。
  - 只允许输出稳定枚举 token、rc、来源和长度；不得因 `logsafe` 到位就恢复任意正文。任何未来正文字段都需单独做来源 allowlist 与安全评审。

### 阶段六：生命周期接线（AC-B13）

- [x] **6.1 `internal/app/runtime.go` 启动顺序**
  - `nativeLog.Start()` → `native.SetNativeLogSink(...)` → `native.Start()`。
  - callback 注册失败使 Fx startup 失败并回滚队列；native.Start 失败时按生命周期顺序回滚，不降级为静默运行。

- [x] **6.2 `internal/app/runtime.go` 停机顺序（严格）**
  - 先关闭 HTTP/业务入口与 runner 子进程，并 join stdout/stderr readers；随后 `native.Stop(ctx)` → `native.Close()`/`engine_destroy` → `CloseNativeLogSink()`（注销 + Delete handle）→ `nativeLog.Stop(ctx)` drain → `logger.Sync()`。
  - 事件顺序测试必须覆盖 runner subprocess stop + stdout/stderr reader join、native Stop/Close、callback unregister/handle delete、queue drain/Stop、logger Sync；任何 reader/callback 不得在 Sync 后发日志。
  - 若 Close 无法证明 native producer quiescent，或 callback 注销失败，保留队列/handle 并返回错误；不得释放仍可能被访问的 callback token。
  - 验证：`python3 native/scripts/build.py go test -race ./internal/app/ -run TestRuntimeShutdownOrder`。

- [x] **6.3 `internal/app/app.go` 装配**
  - 以容量 256 构造 `nativelog.Queue`，纳入 Fx 图并注入 `internal/engine` 与 `lifecycleRuntime`。
  - 构造函数保持纯粹（不开文件、不分配原生状态）—— 队列的 `Start` 放生命周期钩子。

### 阶段七：日志点分批铺开

⚠️ **每批独立提交、独立验证。不得一次铺满 59 个文件。**

- [x] **7.1 批次 B1：`src/pipeline/`（10 文件）**
  - 重点：流建立/失败/释放、订阅 attach/release、背压丢帧、发布率变更。
  - 热路径（每帧）日志必须 `Debug` 级；优先用计数器替代逐帧日志。
  - 验证：`make native-build && make native-test`

- [x] **7.2 批次 B2：runner 插件 API 边界（替代 backend `.so` 内日志）**
  - 重点：`model_create`、`worker_create`、`task_open` 与 `process` 失败；记录固定 operation token、可用的 stream ID 和 numeric `rc`。
  - module 固定为 `algorithm.runner`；不记录 backend 名称推断、异常文本、vendor error prose、路径或帧数据。
  - RKNN/ONNX/RGA backend 源码属于 `ZhulongAlgoRuntime`，按决策不接入日志核心；runner 边界仅提供通用 plugin operation 摘要。
  - 验证：`make native-build && make native-test`，并用真实 runner + invalid plugin 路径验证安全日志分类。

- [x] **7.3 批次 B3：`src/nodes/`（10 文件）**
  - 重点：RTSP 建连/超时、解码器初始化、MPP 回退到软件解码。
  - ⚠️ **不得**记录 RTSP URL。

- [x] **7.4 批次 B4：platform DMA-BUF allocator**
  - 在 `ZhulongAlgoIpc` 编译变体记录 stable allocation reason、errno 和请求字节数；用 target compile definition 隔离日志 include/calls。
  - `ZhulongAlgoRuntime` 编译变体不得引用 `ZhulongLog`；runner 的 plugin operation 错误提供该变体的通用失败摘要。
  - 验证：host `make native-build && make native-test`；检查 runtime target 仍无 `ZhulongLog` 依赖。

- [x] **7.5 全批次的静态门禁（AC-B4 / AC-B11）**
  ```bash
  # 无裸 std::cerr / std::cout（runner 协议输出除外）
  ! grep -rn "std::cerr\|std::cout" native/src --include="*.cpp" | grep -v "runner/runner.cpp"

  # FFmpeg 日志必须保持静默：命中数必须恰好为 1 个文件
  test "$(grep -rl 'av_log_set_level\|av_log_set_callback' native/src --include='*.cpp' | wc -l)" = "1"

  # 不得出现 RTSP 凭据形态的字符串
  ! grep -rnE 'rtsp://[^"]*:[^"@]*@' native/src
  ```

### 阶段八：收尾

- [x] **8.1 端到端验证（AC-B2 / AC-B3）**
  ```bash
  make build && ./build/Zhulong &
  # 触发一次引擎活动（拉流或推理）
  grep '"module":"' <log> | head -3        # 期望：命中 C++ 侧日志
  jq -e . <log> >/dev/null && echo "JSON clean OK"
  ```
  自动化端到端覆盖：`TestNativeLogCABILifecycle` 验证 C ABI native event 到 Go zap observer；`ZhulongAlgoRunnerJson` 验证 standalone runner 协议行与 native JSON-lines 并存且日志不包含 invalid plugin path。手工运行完整服务并拉取真实流未执行，见 §7。

- [ ] **8.2 压测（AC-B5 / AC-B6）**
  - 分别基准 C++ `log_write` 与 Go trampoline；目标硬件为 RK3568 EVB1（Debian 11 arm64，kernel 5.10.226），记录 p50/p95/p99、allocs/op、峰值 RSS 和队列丢弃。该日志桥接不依赖 RKNN 推理 SDK。
  - 故意阻塞 runner writer 后压满队列，确认 producer 延迟有界且 drop 计数可观察。
  - Go queue 满载时确认 C callback 不等待 sink I/O，丢弃计数准确。
  - 本轮仅完成 x86_64 host 验证：Go trampoline 5 次测量为 379.5–425.9 ns/op、3 allocs/op、509–580 B/op；native 队列饱和由 `ZhulongNativeLog` CTest 覆盖。RK3568 EVB1 的 p50/p95/p99、峰值 RSS 与目标板队列丢弃尚未测量，AC-B5 保持待验证，不得据此宣称通过。

- [x] **8.3 全量门禁**
  ```bash
  make check
  ```
  含 `native-test` 与 `python3 native/tests/run_go_bridge_tests.py`。

## 3. 决策记录（已确认基线）

| 决策点 | 结论 | 日期 |
| --- | --- | --- |
| `ZhulongLog` 是否加入 `ZhulongAlgoRuntime` | 否；B2 在 runner plugin API 边界记录 operation token/rc，B4 仅 ZhulongAlgoIpc 编译变体直接记录 | 2026-10-20 |
| Go `nativelog.Queue` 容量 | 256 条 | |
| standalone runner MPSC ring 容量 | 256 条 | |
| `min_level` 默认值 | Info | |
| runner 每秒限流阈值 | 100 条（默认，部署 profile 可调） | |
| runner 最大单行保留长度 | 64 KiB | |
| 基准目标 | RK3568 EVB1，Debian 11 arm64 / kernel 5.10.226 | |
| 固定队列与行缓冲估算 | 约 1.2 MiB，跨 Go host 与 runner 进程；排除运行时、分配器及序列化临时开销，必须以目标板峰值 RSS 验证 | |

## 4. 验证命令速查

```bash
# Native 构建与测试（改 C++ 后必须手动跑，Air 不监听 .cpp）
make native-build
make native-test

# Go 侧（engine 相关必须走 wrapper）
python3 native/scripts/build.py go test -race ./internal/engine/ ./internal/algorunner/ ./internal/app/
python3 native/tests/run_go_bridge_tests.py

# 门禁
make check

# 静态门禁
! grep -rn "std::cerr\|std::cout" native/src --include="*.cpp" | grep -v "runner/runner.cpp"
test "$(grep -rl 'av_log_set_level' native/src --include='*.cpp' | wc -l)" = "1"
nm -C build/native/libZhulongEngine.a 2>/dev/null | grep -c "zhulong::set_log_sink"

# 端到端
grep '"module":"' <log> | head
jq -e . <log> >/dev/null && echo OK
grep -iE 'rtsp://[^"]*:[^"@]*@' <log> | wc -l    # 期望 0
```

## 5. 回滚点

| 阶段 | 回滚动作 | ⚠️ 注意 |
| --- | --- | --- |
| 1-2（C++ 设施 + ABI） | 不调用 `Zhulong_set_log_callback` | 退化为「无 native 日志」，Go 侧功能不受影响 |
| 3（清理 std::cerr） | 恢复 `std::cerr` | 会重新污染 JSON 流（AC-B3 失败） |
| 4（Go 桥接） | 不注册 sink | 同阶段 1-2 |
| 5（runner 解析） | 不输出原始未解析行内容；保留安全字段、line length 和丢弃计数 | 不以恢复原始文本为回滚方式 |
| 6（生命周期） | 移除接线 | 队列、callback 与 cgo.Handle 必须一起移除；不得只停队列保留 producer |
| 7（日志点） | 按批次回滚 | 各批次独立提交，可单独 revert |

## 6. 完成标准

- [ ] `prd.md` 的 AC-B1 ~ AC-B14 全部满足
- [x] 静态门禁（AC-B4 / AC-B11）通过
- [x] 决策记录（§3）填写完整
- [x] `make check` 全绿
- [x] 父任务 `design.md` §3.1.1 已复核，zap 仍为 v1.28.0，无版本变更
- [x] `logging-guidelines.md` 已补充原生 callback、runner writer 与安全边界说明

## 7. 本轮验证记录与未完成项

| 验证 | 结果 |
| --- | --- |
| `make native-build` | 通过；host 编译 `ZhulongAlgoRuntime` 与 `ZhulongAlgoIpc` allocator 变体 |
| `make native-test` | 通过；22/22 CTest |
| `python3 native/scripts/build.py go test -race ./internal/engine ./internal/app ./internal/algorunner ./internal/nativelog` | 通过 |
| `make check` | 通过；前端 lint、502 tests、build，native 22/22，API docs，Go vet/race 与 Go bridge integration |
| `ZhulongLog` 链接隔离 | `libZhulongLog.a` 中 `set_log_sink` 唯一定义计数为 1；`ZhulongAlgoRuntime.so` 日志符号计数为 0 |
| native callback benchmark | x86_64 host only；结果见 §8.2，不能替代 RK3568 测量 |
| RK3568 EVB1 benchmark / 峰值 RSS | 未运行；当前环境为 x86_64，无目标板测量证据。AC-B5 保持未完成 |
| 手工服务拉流验证 | 未运行；C ABI lifecycle、runner JSON 与 RTSP 自动化测试已通过 |

经用户于 2026-10-20 明确批准，B 可在清楚标注 RK3568 EVB1 基准未测的前提下归档。该批准仅豁免归档时必须完成目标板测量的流程门槛；AC-B5 的目标板性能数据仍未验证，host benchmark 不替代目标板结果，也不构成目标性能通过声明。
