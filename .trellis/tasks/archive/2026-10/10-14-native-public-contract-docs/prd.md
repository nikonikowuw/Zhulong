# native 公开契约层注释补齐

> 父任务：`.trellis/tasks/10-14-native-core-code-docs/`
> ⚠️ 动手前必读父任务 `design.md` §3（风格基线）与 §4（术语表）—— 注释中表达同一概念时必须逐字使用术语表用词。

## 1. 目标

为 `native/` **对外契约头文件**补齐中文 Doxygen 注释。这四个文件是 Go（CGO）、插件开发者、外部集成方唯一的阅读入口，当前注释覆盖率在 3%~61% 之间且极不均衡。

本子任务同时承担**风格基线确立**的职责：后续两个子任务（pipeline / algorithm）会以本子任务产出的注释为参照。

---

## 2. 范围

| 文件 | 行数 | 当前注释率 | 备注 |
| --- | --- | --- | --- |
| `native/include/Zhulong/engine.h` | 376 | 61% | 已有高质量注释，**以补全为主**，不重写既有内容 |
| `native/include/Zhulong/frame.hpp` | 146 | 15% | 已有文件头块，需补字段级与移动语义说明 |
| `native/sdk/include/zhulong/plugin.h` | 177 | 10% | 插件 C ABI，字段级注释需补全 |
| `native/sdk/include/zhulong/algo.hpp` | 556 | 3% | **本子任务重点**，包含 SDK 门面与全部适配器/宏 |

### 明确排除

- `native/vendor/**`、`native/tests/**`、任何 `.cpp` 实现文件。
- 行为变更、签名变更、结构体布局变更、`static_assert` 数值变更、宏展开结果变更。
- 引入 Doxygen 工具链或 CI 门禁。

---

## 3. 重点内容清单

### 3.1 `algo.hpp`（核心）

- `Image`：**借用视图**语义（不拥有像素）、底层 `frame_view` 的有效期、⚠️ 需长期保留必须深拷贝。
- `Detection` / `Classification` / `FaceCandidate` / `Landmark`：逐字段语义与取值范围；`box` 为**归一化坐标** `[x1, y1, x2, y2] ∈ [0.0, 1.0]`；`Landmark` 同样归一化。
- `FaceItem` / `PersonItem`：`quality`、`matched`、`is_fused`（预留）、`candidates`（Top-K，顺序语义）、`track_id` / `body_box` / `body_score` 在**纯人脸退化语义**下的取值（`track_id=0`、`body_box=face_box`、`body_score=1.0`）。
- `Session`：**单 Worker 独占执行**的含义；`forward` 失败返回空 vector 的语义 —— ⚠️ 与「合法空输出」不可区分，必须显式写明。
- `Model`：整包共享不可变权重；`load` 失败返回 `!valid()` 而不抛异常；`create_session(worker_id)` 与 Worker 的绑定关系。
- `ops::letterbox`：`LetterboxParams` 是 **in-out** 参数（几何回填）。
- `ops::nms`：输入须已归一化、IoU 阈值语义、复杂度。
- `detail::*ResultAdapter`：`item_type` / `result_type` / `infer` / `serialize` / `pack` / `release` 各自的职责；**Arena 单块连续内存打包**与 `internal_token` 指向首地址的释放契约。
- `detail::PluginApi`：`ModelGroup` / `WorkerContext` / `TaskContext` 三层句柄的生命周期；`model_create` / `worker_create` / `task_open` / `process` 的所有权转移；**异常防爆门**（每个导出回调 `try-catch(...)` 并返回 `ZHULONG_PLUGIN_ERR_INTERNAL`，失败时输出句柄置空）。
- 三个注册宏：各自要求插件类提供的成员函数签名；`ZHULONG_REGISTER_PLUGIN_WITH_ADAPTER` 的通用语义。

### 3.2 `plugin.h`

- 补齐 `zhulong_frame_view` 各字段语义（`planes` / `strides` 的多平面约定、`strides[0] == 0` 表示 packed）。
- 补齐 `zhulong_inference_result` 的 `data` / `internal_token` 所有权与释放契约。
- 补齐 `zhulong_plugin_api_v1` 各回调的调用时序与失败语义。
- 补齐长度常量（`ZHULONG_PERSON_ID_MAX_LEN` 等）与 `ZHULONG_PLUGIN_ENTRY_POINT` 的用途。

### 3.3 `engine.h`

- **补全而非重写**：既有注释已覆盖大部分内容。
- 需要补的点：`enum` 分组说明、`Zhulong_video_view` / `Zhulong_stream_status` 的完整字段语义、`Zhulong_engine_start` / `Zhulong_engine_stop` 的幂等性边界、`Zhulong_stream_acquire` 的 `consumer_id` 唯一性约束的失败码。

### 3.4 `frame.hpp`

- 补齐 `HardwareFrame` 的字段分组说明（几何/句柄/时序/回收）。
- 补齐 `y_plane_size()` 的 NV12 语义。
- 补齐移动构造与移动赋值的**防二次释放**契约（`release_fn` 转移后置空）。
- 补齐 `dma_fd` / `dev_ptr` / `host_ptr` / `host_planes` 的互斥性与平台归属。

---

## 4. 验收标准

### 4.1 继承父任务共性标准

- [x] **AC-D1（覆盖率）**：范围内**每一个**公开声明（类型、结构体成员、函数、方法、宏、具名枚举与枚举值、常量）都有 Doxygen 注释。
- [x] **AC-D2（零代码变更）**：父任务 `implement.md` §3 的注释剥离不变性检查，全量输出 `OK`。
- [x] **AC-D3（构建测试）**：`make native-build` 成功；`make native-test` 全绿。
- [x] **AC-D4（注释质量）**：无逐行复述；无臆造技术细节。
- [x] **AC-D5（既有注释保真）**：`engine.h` / `frame.hpp` 的既有注释未被删除或改变技术含义。

### 4.2 本子任务专属

- [x] **AC-C1**：§3 重点内容清单逐项落地。
- [x] **AC-C1-a**：父任务 `prd.md` §4.3 中归属 C1 的风险点 **R1、R2、R3、R4、R5、R18** 全部在注释中有明确落点。
- [x] **AC-C1-b**：`algo.hpp` 与 `plugin.h` 的技术口径与 `.trellis/spec/native/algo-plugin-contract.md` §2、§3 严格一致（这两份 spec 是插件开发者的单一来源，不得出现口径漂移）。
- [x] **AC-C1-c**：注释中未出现父任务 `design.md` §4 术语表列出的禁用别名。
- [x] **AC-C1-d**：⚠️ 公开头文件注释中**不出现**私有实现细节（如 `rknn_api.h` 类型、`backends/` 内部结构），符合 `.trellis/spec/native/directory-structure.md` 的单向依赖边界。

---

## 5. 参考

| 用途 | 路径 |
| --- | --- |
| 风格基线 | 父任务 `design.md` §3 |
| 术语表 | 父任务 `design.md` §4 |
| 插件 ABI 权威来源 | `.trellis/spec/native/algo-plugin-contract.md` §2、§3 |
| CGO 边界 | `.trellis/spec/native/cgo-contract.md` |
| 归一化坐标决策 | `.trellis/tasks/archive/2026-10/10-14-unify-bbox-coordinates/prd.md` |
| 既有风格样本 | `native/include/Zhulong/engine.h`、`native/include/Zhulong/frame.hpp` |
