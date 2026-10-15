# native 核心代码注释补齐

## 1. 目标与背景 (Goal & Background)

`native/` 一手代码（排除 `vendor/` 与 `tests/`）当前注释覆盖率极不均衡：公开契约头 `include/Zhulong/engine.h`（61%）与 `nodes/capture/rtsp_input.hpp`（57%）已有成体系的 Doxygen 文档，而绝大多数实现文件处于 **0%~8%** 区间，其中：

| 文件 | 行数 | 注释率 |
| --- | --- | --- |
| `sdk/include/zhulong/algo.hpp` | 556 | **3%** |
| `src/algorithm/ipc/frame_broker.cpp` | 395 | 4% |
| `src/algorithm/runner/runner.cpp` | 494 | 4% |
| `src/algorithm/ipc/unix_socket.cpp` | 440 | 3% |
| `src/algorithm/ipc/unix_socket.hpp` | 83 | **0%** |
| `src/algorithm/runtime/tensor.hpp` | 84 | **0%** |
| `src/algorithm/ipc/shm_layout.hpp` | 28 | **0%** |
| `src/pipeline/stream.cpp` | 487 | 8% |
| `src/backends/rga/rga_vision_backend.cpp` | 525 | 7% |
| `src/nodes/decode/ffmpeg_decoder.cpp` | 158 | 6% |

而这些位置恰好承载着全系统最贵的知识：`algo.hpp` 的 Arena 打包与 `internal_token` 释放契约、IPC 的 8 字节自然对齐与半包熔断、SHM 槽位代次防 ABA、RGA 的目标指针偏移禁令、RKNN 的 `RKNN_QUERY_MEM_SIZE` 上下文污染缺陷、pipeline 的按需解码会话与锁外 join。这些约束目前只存在于 `.trellis/spec/native/*.md` 与历史任务文档中，**读代码的人看不到**。

本任务目标是：在不改变任何一行代码语义的前提下，为 `native/` 核心代码补齐**说明意图与关键约束**的中文 Doxygen 注释，使约束在代码现场可得。

---

## 2. 范围 (Scope)

### 2.1 纳入范围（一手代码，约 50 文件 / 6000 行）

| 分区 | 路径 | 子任务 |
| --- | --- | --- |
| **公开契约层** | `native/include/Zhulong/engine.h`、`native/include/Zhulong/frame.hpp`、`native/sdk/include/zhulong/plugin.h`、`native/sdk/include/zhulong/algo.hpp` | `10-14-native-public-contract-docs` |
| **引擎与流水线层** | `native/src/abi/`、`native/src/pipeline/`、`native/src/nodes/` | `10-14-native-pipeline-docs` |
| **算法子系统层** | `native/src/algorithm/{ipc,runner,runtime}/`、`native/src/backends/{rknn,rga}/` | `10-14-native-algorithm-docs` |

### 2.2 明确排除 (Out of Scope)

- `native/vendor/**`（RGA / RKNPU2 第三方头文件）—— **绝不动**，不是本仓库的代码。
- `native/tests/**`（约 4600 行）—— 本任务不动。测试意图由用例名表达，且注释测试会显著放大本任务的 diff 与回归面。
- `native/scripts/**`、`native/cmake/**`、`native/profiles/**`、`native/CMakeLists.txt`。
- 任何**行为变更**：不改标识符、不改签名、不改结构体布局、不改 `static_assert` 数值、不改字符串字面量、不改宏展开结果。
- 任何**重构**：不提取函数、不合并分支、不"顺手"整理格式。
- 引入 Doxygen 工具链 / CI 文档生成（本次不做，见 §5）。

---

## 3. 注释规范（单一来源，三个子任务共同遵守）

1. **语言与风格**：中文说明 + Doxygen 标签（`@brief` / `@param` / `@param[out]` / `@return` / `@note`），对齐仓库既有约定（`.trellis/spec/native/algo-plugin-contract.md` §3、`native/include/Zhulong/engine.h`、`native/include/Zhulong/frame.hpp`、`src/algorithm/ipc/shm_pool.hpp`、`src/nodes/capture/rtsp_input.hpp`）。
2. **只写代码说不出来的东西**（依据 `.trellis/spec/index.md` 铁律 7）：
   - 所有权：谁分配、谁释放、借用指针的有效期；
   - 生命周期：句柄/会话/视图的创建与销毁配对，回调期间的有效性；
   - 并发：线程归属、锁与原子量的保护对象、调用方必须串行化的前提；
   - 契约与不变量：尺寸/对齐断言、坐标归一化语义、状态机合法迁移；
   - 失败语义：返回值含义、错误码映射口径、失败时输出参数的状态；
   - ⚠️ **风险点**：历史踩坑与禁止事项，必须显式标注。
3. **禁止**：
   - 逐行复述实现（`i++;  // i 自增` 这类）；
   - 臆造 API、版本号、寄存器、性能数字 —— 不确定就不写，或写成待验证；
   - 修改既有注释的**技术含义**；既有注释可以补全、可以纠正**确凿的**过期描述，但必须在 PR 说明中逐条列出。
4. **保留**：既有行内 `/* ... */`、`//` 注释原样保留，不删除、不改写、不重排位置。
5. **不引入工具链**：不引入 `clang-format`、不新增 CMake 校验、不新增 CI 步骤（会与"零行为变更"的验收目标冲突）。

---

## 4. 验收标准 (Acceptance Criteria)

### 4.1 跨子任务共性标准（每个子任务都必须满足）

- [ ] **AC-D1（覆盖率）**：纳入范围的**每一个**公开声明（class / struct / 结构体成员 / 函数 / 方法 / 宏 / 具名枚举与枚举值 / 常量）都有 Doxygen 注释；实现文件（`.cpp`）的**非平凡逻辑块**（状态机、并发协议、内存打包、错误映射、厂商约束）有块级注释说明意图。仅靠命名自明的 getter/setter 可豁免。
- [ ] **AC-D2（零代码变更）**：注释剥离后，与**子任务开始时的文件快照**逐字节一致。⚠️ **不是**与 `HEAD` 比对 —— 仓库中存在其他任务对 `native/` 文件的未提交修改（如 `10-14-inference-result-egress`），用 `HEAD` 作基准会把别人的改动误判为「代码骨架变更」。校验命令见父任务 `implement.md` §3，全量输出必须全部 `OK`，无 `SKIP`、无 `DIFF`。

  命令要点（完整版含基线快照与并发修改检查，见 `implement.md` §3）：

  ```bash
  norm() { g++ -fpreprocessed -dD -E -x c++ "$1" 2>/dev/null \
           | sed '/^[[:space:]]*$/d' | sed 's/[[:space:]]\+/ /g; s/[[:space:]]*$//'; }
  diff <(norm "/tmp/zhulong_docs_base/$f") <(norm "$f")
  ```

  ⚠️ 该命令已在本仓库实测可用（`rtsp_input.cpp` 的 `rtsp://` 字面量在剥离后完整保留；`-fpreprocessed` 关闭宏展开，避免 `#define` 与条件编译被意外归一化）。
- [ ] **AC-D3（构建与测试）**：`make native-build` 成功；`make native-test` 保持既有全绿基线（当前 15/15）。⚠️ 依据仓库约定，engine 相关测试**必须**走 `python3 native/scripts/build.py` wrapper，禁止裸 `go build` / `go test`。
- [ ] **AC-D4（注释质量）**：不含逐行复述型注释；不含臆造的技术细节；⚠️ 风险点标注完整（清单见 §4.3）。
- [ ] **AC-D5（既有注释保真）**：既有注释未被删除或改变技术含义；若纠正了确凿的过期描述，已在任务 `implement.md` 中逐条登记。

### 4.2 子任务专属标准

- [ ] **AC-C1（公开契约层）**：`algo.hpp` 的 `Image` 视图生命周期、`Session::forward` 失败返回空 vector 的歧义、`Model` 的共享语义、`ops::*` 的 in-out 参数、三个适配器的 `item_type`/`result_type`/`pack`/`release` 职责与 Arena 释放契约、`PluginApi` 三层句柄生命周期与异常防爆门、三个注册宏要求的插件类成员签名，全部有注释。
- [ ] **AC-C2（引擎与流水线层）**：`Engine`/`Stream` 的按需解码会话生命周期（激活 → 休眠 → 再激活）、`packet_queue` 满时的有界重同步、锁外 join 禁令、`BoundedQueue` 的背压与取消语义、`FramePublisher` 的节流口径、`HardwareFrame` 的 `release_fn` RAII 归还、RTSP 采集的重连与超时、FFmpeg 解码器的 drain/EAGAIN 处理，全部有注释。
- [ ] **AC-C3（算法子系统层）**：IPC 的 8 字节自然对齐与 `static_assert(offsetof(...) % 8 == 0)` 意图、四态 socket 状态机与半包熔断、64KB payload 门禁、SHM 槽位代次防 ABA 与崩溃自愈回收、Arena 单块打包、Runner 退出码表与 `--broker` 反解 stream_id 约定、RKNN 的 `RKNN_QUERY_MEM_SIZE` 禁令与 `dup_mutex` 串行化、RGA 的目标指针不偏移禁令与运行期对齐探测，全部有注释。

### 4.3 必须覆盖的风险点清单（跨子任务，缺一即不通过）

> ✅ **终审结果（2026-10-15）：20/20 全部有落点**（逐项 grep 计数，零命中即失败）。

| # | 风险点 | 归属子任务 | 依据 |
| --- | --- | --- | --- |
| R1 | C++ 异常绝不穿越 C ABI；每个导出函数隔离异常并返回状态码 | C1 / C3 | `algo-plugin-contract.md` §2 |
| R2 | 结果内存由插件创建、只能由同一插件的 `result_release` 释放；`internal_token` 是 Arena 首地址 | C1 / C3 | 同上 §2 |
| R3 | `packet_view` / `Image` 视图仅在同步回调期间有效，保留必须深拷贝 | C1 | `engine.h` 头部约定 |
| R4 | 绝不向 C 传 Go 指针；回调 token 是整数（Go 侧 `cgo.Handle`） | C1 | `cgo-contract.md` |
| R5 | `[x1, y1, x2, y2]` 严格归一化 `[0.0, 1.0]`，全系统统一 | C1 / C2 / C3 | 任务 `10-14-unify-bbox-coordinates` |
| R6 | IPC 64 位字段必须 8 字节自然对齐，结构体总尺寸为 8 的倍数 | C3 | `algo-plugin-contract.md` §6.4 |
| R7 | 半包熔断：收到部分字节后超时/错误必须标记 `DESYNC_ERROR` 并断开 | C3 | 同上 §6.4 |
| R8 | SHM 槽位代次（generation）不匹配的 ACK 必须静默丢弃 | C3 | 同上 §6.2 |
| R9 | 慢消费者 Drop-Latest，绝不阻塞主视频流；崩溃后回收未归还租约 | C3 | 同上 §6.3 |
| R10 | 主 root 上下文绝不调用 `RKNN_QUERY_MEM_SIZE`（会破坏 `rknn_dup_context`） | C3 | 同上 §8.5 |
| R11 | `rknn_dup_context` 必须经 `dup_mutex` 串行化；失败路径不得 double-destroy | C3 | 同上 §8.6/8.7 |
| R12 | RGA 目标缓冲区指针**绝不**偏移，内容区偏移由 `drect` 表达 | C3 | 同上 §9.4 |
| R13 | `rknn_input` 无 stride 字段 → 传入 buffer 必须 packed；不得声称零拷贝 | C3 | 同上 §8.9 |
| R14 | `librknn_api.so` 是陈旧残留，链接目标必须是 `librknnrt.so` | C3 | 同上 §8.3 |
| R15 | 停止解码线程时严禁持锁 join（`Engine::control` / `Stream::mutex`） | C2 | `10-11-native-pipeline-stability` REQ-5 |
| R16 | `packet_queue` 满时丢弃至下一个 IDR，不破坏参考帧链 | C2 | 同上 REQ-4 |
| R17 | 缺失 PTS 不得被当作有效时间戳 0 | C2 | 同上 REQ-6 |
| R18 | 回调期间严禁调用控制/销毁接口（`Zhulong_ERR_CALLBACK_CONTEXT`） | C1 / C2 | `engine.h` |
| R19 | 解码线程顶层必须捕获全部异常，不允许后台异常 `std::terminate()` | C2 | 同上 REQ-3 |
| R20 | `HardwareFrame` 析构/移动必须防二次释放（`release_fn` 转移后置空） | C2 | `frame.hpp` |

---

## 5. 非目标 (Non-Goals)

- 不生成 HTML/PDF 文档，不引入 Doxygen / Sphinx 工具链或 CI 门禁。
- 不新增自动化注释覆盖率检查（会与"零行为变更"冲突）。
- 不补 `native/tests/**` 的注释。
- 不修正 `native/README.md` 或 `.trellis/spec/native/*` 中的内容（除非发现**确凿**的过期描述，届时另开任务处理）。
- 不改变任何公开 ABI、结构体布局、函数签名或错误码语义。

---

## 6. 子任务地图与执行顺序

| 子任务 | 交付物 | 依赖 |
| --- | --- | --- |
| `10-14-native-public-contract-docs` | 4 个公开头文件的注释 | 无（**先做**，风格基线由此确立） |
| `10-14-native-pipeline-docs` | `src/abi` + `src/pipeline` + `src/nodes` 的注释 | 无 |
| `10-14-native-algorithm-docs` | `src/algorithm` + `src/backends` 的注释 | ⚠️ **须等 `10-14-inference-result-egress` 提交**（见下） |

⚠️ **并发冲突**：`10-14-inference-result-egress`（`in_progress`）正在修改本任务范围内的 3 个文件，且是真实功能代码：

```txt
native/src/algorithm/ipc/ipc_protocol.hpp   (+ MSG_RESULT = 7)
native/src/algorithm/ipc/frame_broker.hpp   (+ ResultCallback / set_on_result)
native/src/algorithm/runner/runner.cpp      (+ serialize_inference_result, ~118 行)
```

因此 **algorithm 子任务必须排在 `10-14-inference-result-egress` 之后**；若必须并行，则跳过这 3 个文件并登记待补（详见 `implement.md` §1 与 §7.3）。

⚠️ 三个子任务虽然修改**互不重叠**的文件集合，但都依赖同一个 `native/build/` 目录。**必须串行化 `make native-build` 与 `make native-test`**（避免共享 build 目录竞争导致假失败）。建议整体顺序执行：public-contract → pipeline → algorithm。

⚠️ 父任务不承担实现工作：父任务持有本 PRD、§3 注释规范、§4.3 风险点清单与最终一致性审查（三个子任务完成后由父任务做一次跨层术语一致性检查）。

---

## 7. 父任务收尾审查结果（2026-10-15）

### 7.1 三个子任务交付状态

| 子任务 | 状态 | 提交 |
| --- | --- | --- |
| `10-14-native-public-contract-docs` | ✅ 已归档 | `2c1eb24` / `cfa4cbb` |
| `10-14-native-pipeline-docs` | ✅ 已归档 | `8f5beb7` / `ff463d3` |
| `10-14-native-algorithm-docs` | ✅ 已归档 | `03ece54` / `93ee8ac` / `19526ea` |

合计 47 个文件、约 2200 行新增注释。

### 7.2 跨层术语一致性检查

```bash
git diff 5199d43..HEAD | grep -E '^\+[^+]' | grep -E '<design.md §4 全部禁用别名>'
```

- 首次执行**命中 7 处**：`任务上下文`（应为「任务状态」，Task State）—— 全部来自 C1 子任务 `plugin.h` / `algo.hpp`。
- 已按 `design.md` §5.3 以**单独一次文档纠正提交**（`98e9595`）修正，message 逐条列出原文与现文。
- 复查：✅ 零命中。

### 7.3 风险点清单终审（R1~R20）

| # | 落点数 | # | 落点数 | # | 落点数 | # | 落点数 |
| --- | --- | --- | --- | --- | --- | --- | --- |
| R1 | 20 | R6 | 8 | R11 | 6 | R16 | 24 |
| R2 | 11 | R7 | 7 | R12 | 11 | R17 | 36 |
| R3 | 20 | R8 | 21 | R13 | 23 | R18 | 4 |
| R4 | 6 | R9 | 7 | R14 | 4 | R19 | 8 |
| R5 | 19 | R10 | 4 | R15 | 9 | R20 | 20 |

✅ **20/20 全部有落点**。

### 7.4 全栈门禁

- `make check`：✅ 全绿（前端 lint/type-check/test + native-test 15/15 + api-docs + go-check）。
- 注释剥离不变性（`norm()`）：✅ 全部 47 个文件 `OK`，零 `DIFF`。
- 修改范围：✅ 未触碰 `native/vendor/**` 与 `native/tests/**`。
