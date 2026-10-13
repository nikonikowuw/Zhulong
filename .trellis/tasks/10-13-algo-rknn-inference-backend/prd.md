# Rockchip RKNN 推理后端接入

父任务：`10-10-algorithm-plugin-architecture`（阶段四）。本任务只交付 **RKNN 推理后端**，不含 RGA 视觉前处理（见兄弟任务 `10-13-algo-rga-vision-backend`）与 DMA-BUF 零拷贝数据通路。

## 1. 目标

在公共算法运行库 HAL（`IInferenceBackend`）下接入 Rockchip RKNN 推理后端，使算法插件在 RK3568 上通过统一 SDK 获得真实 NPU 加速，并在**目标板实测**层面验证 `rknn_dup_context` 派生上下文能否达成权重共享。

## 2. 仓库事实与真机证据

### 2.1 现有代码

- `native/src/algorithm/runtime/inference_backend.hpp` 已定义 `IInferenceBackend`（`load_model` / `create_session` / `forward`）与 `ModelAsset`、`SessionContext`。
- `native/src/algorithm/runtime/cpu_inference_backend.cpp` 是当前唯一实现（CPU 桩）。
- `native/src/algorithm/runtime/runtime_factory.cpp` 硬编码返回 `CpuInferenceBackend`，无编译期/运行期后端分流。
- `native/src/backends/` **尚不存在**；`.trellis/spec/native/directory-structure.md:44` 已规定厂商 SDK 专有头文件只能在 `src/backends/` 内引用。
- `config.toml [algorithm].target = "cpu"`；`internal/algorithm/verifier.go:82-84` 默认 `"cpu"`；`.trellis/spec/backend/algorithm-guidelines.md:76` 枚举为 `cpu | rknn | cann | tensorrt`。

### 2.2 目标板实测（2026-10-13，经 SSH `rk3568`，只读命令）

| 项 | 实测值 |
| --- | --- |
| OS / 架构 | Debian GNU/Linux 11 (bullseye) / arm64 |
| 内核 | `5.10.226`（`linaro-alip`） |
| RKNPU 驱动 | **v0.9.8**（`dmesg`：`Initialized rknpu 0.9.8 20240828`，`fde40000.npu`，iommu 已启用） |
| `librknnrt.so` | **2.3.2**（`429f97ae6b@2025-04-09T09:09:27`），`/usr/lib/librknnrt.so`，SHA-256 `d31fc19c85b85f6091b2bd0f6af9d962d5264a4e410bfb536402ec92bac738e8` |
| `/opt/rknn-toolkit2/rknpu2/lib/librknnrt.so` | 同上，**SHA-256 与 `/usr/lib` 一致** |
| `/opt/rknn-toolkit2/rknpu2/include/rknn_api.h` | SHA-256 **`c48e11a6f41b451a5fd1e4ad774ea60252d3d94f78bee9b21ea3d21b21deba9a`** |
| `/usr/lib/librknn_api.so` | ⚠️ **1.5.2**（`c6b7b351a@2023-08-23`），5241256 字节，仅 25 个 `rknn_` 导出符号 |
| `/usr/lib/librknnrt.so.1.5.2.bak` | 5241256 字节，与 `librknn_api.so` 同尺寸的旧版残留 |
| 可用 `.rknn` 模型 | `/userdata/rknn_model_zoo/examples/facelivt/model/facelivtv2_s_rk3568_fp.rknn`、`.../insightface/buffalo_l/model/w600k_r50.rknn` |
| 板端工具链 | gcc/g++ 10.2.1、make 4.3、cmake 3.25.1、**ninja 缺失** |
| 板端磁盘 | `/` 已用 **90%**，可用 2.9G |

### 2.3 关键推断（有证据，仍需实测确认）

- 头文件 SHA-256 与 `research/rockchip-context-sharing.md` 记录的官方固定快照（ref `59a913d1…`）**逐字节相同**。该研究中 L491-501 `rknn_dup_context`、L47-51 `RKNN_FLAG_SHARE_WEIGHT_MEM`、L723-733 `rknn_set_weight_mem`、L736-746 `rknn_set_internal_mem` 的源码事实，现已从「官方快照」升级为「与目标板头文件一致」。**但这仍只证明声明存在，不证明 2.3.2 runtime + 0.9.8 驱动下行为正确或确有内存收益。**
- `librknn_api.so`（1.5.2）与 `librknnrt.so`（2.3.2）同处 `/usr/lib`，符号集不同（25 vs 39）。**链接目标必须是 `librknnrt.so`**；`librknn_api.so` 是陈旧残留，不得链接。
- 宿主交叉工具链为 gcc/g++ 14.2.0，sysroot `build/sysroots/rk3568` 内的 libstdc++ 为 **10**；板端 gcc 为 **10.2.1**。`.trellis/spec/native/ingestion-contract.md` 的 host/cross 合同继续适用。

## 3. 需求

| ID | 需求 |
| --- | --- |
| R1 后端实现 | 在 `native/src/backends/rknn/` 内实现 `IInferenceBackend` 的 RKNN 版本（模型加载、会话派生、同步 `forward`）。RKNN 专有头文件只允许出现在该目录。 |
| R2 后端选择 | 通过**编译期 profile 隔离**选择后端，不改动 `IInferenceBackend` 接口本身；CPU 构建路径保持可用，宿主（Go/`ZhulongEngine`）不得新增 RKNN 链接依赖。 |
| R3 权重共享 | 为同一模型内容/设备/执行配置建立根资源，用 `rknn_dup_context(&root, &worker)` 派生每 worker 上下文。**必须真机实测**正确性、内存占用与销毁行为；共享不成立时按 R6 明确报阻塞，不得静默初始化 N 份权重。 |
| R4 多 Worker | 每 worker 独占输入/输出/可变 workspace；上下文派生对共享根资源串行化，不假定 `rknn_dup_context` 可并发调用；按 2.3.2 版本合同处理线程绑定要求。 |
| R5 生命周期 | 根资源晚于所有派生 worker 释放；创建失败（部分初始化）必须回滚已分配资源；`Engine.Start`/`Close` 的既有所有权语义不被破坏。 |
| R6 证据门禁 | 运行时查询并记录 `RKNN_QUERY_SDK_VERSION`（`api_version`/`drv_version`）与 `RKNN_QUERY_MEM_SIZE`。共享目标未达成时后端标记为不支持/阻塞并报告，不自动降级为多份权重。 |
| R7 错误与异常 | RKNN 返回码映射到 `zhulong_status_t`；C++ 异常不得穿越 C ABI 边界；失败时输出句柄清零、无所有权残留。 |
| R8 版本固定 | 链接与部署固定 `librknnrt.so` 2.3.2（SHA-256 `d31fc19c…`）；显式排除陈旧 `librknn_api.so`；模型、Runtime、驱动版本三元组一并记录。 |
| R9 模型契约 | 前端处理与量化后处理必须与 `.rknn` 的转换合同一致；无法取得 tensor 属性时拒绝该模型，不猜测 layout/量化参数。 |

## 4. 范围

**包含**：RKNN 后端实现、profile 分流、真机权重共享验证、多 worker 执行上下文、资源生命周期与错误映射、构建接入与版本固定。

**不包含**：RGA 硬件前处理（兄弟任务）、DMA-BUF 零拷贝数据通路（后续任务）、Go 侧进程管理与升级回滚（父任务阶段三已归档）、昇腾/CANN 与 TensorRT 后端、模型训练/转换脚本、前端页面。

## 5. 明确不承诺

- 未实测前不承诺任何权重节省比例、吞吐倍数或固定 FPS。
- 不把「线程数」等同「NPU 并行度」：RK3568 为单 NPU 核，收益方向是 CPU/前处理/后处理与 NPU 重叠。
- 不承诺驱动/设备级故障隔离；同包进程内崩溃仍可能影响整个包。
- 不启用 `RKNN_FLAG_ASYNC_MASK` 作为「多线程开关」。
- 不把 CPU 桩通过、或只成功 `dlopen` 库当作 RKNN 后端验收完成。

## 6. 验收标准

| ID | 关联 | 可验证结果 |
| --- | --- | --- |
| AC1 | R1、R9 | RKNN 后端在 `native/src/backends/rknn/` 内实现 `IInferenceBackend` 全部三个方法；`grep` 证明 `rknn_api.h` 只被该目录引用。 |
| AC2 | R2 | CPU profile 构建通过 `make native-test`；宿主交叉产物 `build/targets/rk3568/Zhulong` 的 `readelf -d` 中**不出现** `librknnrt.so`。 |
| AC3 | R3、R6 | 真机记录 `RKNN_QUERY_SDK_VERSION` 实际 `api_version`/`drv_version`；对比重复 `rknn_init` 与根上下文 + `rknn_dup_context` 的正确性与内存指标，输出对比表。共享不成立则明确标记阻塞。 |
| AC4 | R4 | 1/2/4 worker 真机运行：输出一致性、并发稳定性、销毁后无泄漏；不声称吞吐线性扩展。 |
| AC5 | R5、R7 | 故障注入（模型路径非法、初始化中途失败、forward 失败）后资源全部回收；无 C++ 异常穿越 ABI；重复启停无 FD/内存增长。 |
| AC6 | R8 | 产物 `NEEDED`/`ldd` 显示仅依赖 `librknnrt.so`（2.3.2），不依赖 `librknn_api.so`；构建脚本固定 SHA-256 并在不匹配时失败。 |
| AC7 | R9 | 使用真实 `.rknn` 模型（如 `facelivtv2_s_rk3568_fp.rknn`）完成一次真实推理；tensor 属性通过 Runtime 查询获得，不从文件猜测。 |

## 7. 未决与阻塞

- **构建策略未定**：宿主交叉编译后 `scp` 到板端运行，还是板端原生编译（gcc 10.2.1 + cmake 3.25.1，但磁盘仅剩 2.9G）。见 `design.md`。
- 板端磁盘 90% 占用，构建/部署路径需规划，否则可能中途失败。
- `librknn_api.so`(1.5.2) 的存在是否需要清理，属于板端运维决定，不在本任务代码范围内。
- 权重共享是否成立须实测；`research/rockchip-context-sharing.md` 的结论只到「声明存在」。
