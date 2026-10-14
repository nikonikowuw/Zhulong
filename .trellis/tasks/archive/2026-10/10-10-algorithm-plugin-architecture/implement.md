# 算法插件架构：实施与验收记录

## 0. 交付概述与子任务索引

本父任务旨在构建可独立开发、独立编译与安装的感知算法插件架构。整个架构按设计遵循小步快跑与分层验收原则，拆解为 8 个子任务并已全部交付归档（**8/8 done**）：

| 阶段 | 子任务 | 归档位置 | 核心交付与验收 |
| --- | --- | --- | --- |
| 阶段一 | `10-12-algo-plugin-sdk-runner` | `archive/2026-10/` | 纯 C ABI、现代 C++17 SDK (`zhulong/algo.hpp`)、通用 Runner 宿主、HAL 接口与 CPU 桩、黑盒验包 CLI、示例插件 |
| 阶段二 | `10-12-algo-framebroker-shm` | `archive/2026-10/` | 共享内存环形缓冲池 (`ShmPool`)、帧租约 (`Lease`)、UDS 控制协议 |
| 阶段二 | `10-13-algo-broker-host` | `archive/2026-10/` | Native 引擎 FrameBroker 宿主与发布线程、C ABI 查询、Go 多路订阅与防抖重启编排 |
| 阶段三 | `10-12-algo-package-lifecycle` | `archive/2026-10/` | Go 包管理持久化 (`000007_algorithm_packages`)、Supervisor、升级排空与受控回滚 |
| 阶段三 | `10-12-algo-lifecycle-remediation` | `archive/2026-10/` | 有界指数退避自愈状态机、熔断与 CRASH_LOOP 治理 |
| 阶段四 | `10-13-algo-rknn-inference-backend` | `archive/2026-10/` | RKNN 硬件推理后端、`rknn_dup_context` 权重共享真机实测（RSS 60MB→28MB）、Profile 隔离 |
| 阶段四 | `10-13-algo-rga-vision-backend` | `archive/2026-10/` | RGA 硬件前处理后端、全尺寸 canvas 避免越界死锁、NV12/YUV420P 多平面 CSC、CPU 透明回退 |
| 基线修复 | `10-14-build-gate-baseline-fix` | `archive/2026-10/` | Native 头文件缺失修复、Makefile 前端构建目标对齐 pnpm、使 `make check` 真绿 |

---

## 1. 规划门禁

- [x] 用户已选择 R11 方案 2：公共 `.so` 同时统一推理资源管理，不只做工具函数。
- [x] 用户已明确 R12：指定有效样本文件进行单次黑盒验包，平台检查合规与推理入口可运行，不以识别内容或精度为准入条件。
- [x] 用户已确认 R13：公共运行库设计为多平台硬件抽象层 (HAL)，解耦推理后端与视觉前处理后端；向上提供类 PyTorch/OpenCV 体验的易用 C++17 SDK（`zhulong/algo.hpp`），内部实现异构硬件加速（首期 Rockchip，预留昇腾、NVIDIA、CPU 插槽）。
- [x] 用户审阅 R1–R13、AC1–AC15 与技术设计并授权通过拆离子任务逐步实施。
- [x] 验证环境与目标板证据全部补齐（Debian 11 bullseye, 内核 5.10.226, RKNN 2.3.2, RGA 1.10.1_[10]）。

---

## 2. 阶段一：插件 SDK、公共库与通用 runner（已交付）

- [x] 冻结纯 C SDK 版本（v1）、布局、状态码、内存所有权与库生命周期；纯 C ABI 测试 (`ZhulongPluginCAbiTests`) 验证通过。
- [x] 封装高层 C++17 门面：`native/sdk/include/zhulong/algo.hpp` 提供 `Model`, `Session`, `Tensor`, `ops::letterbox`, `ops::nms`，Header-only 导出纯 C ABI 函数表。
- [x] 定义公共运行库内部 HAL：抽象 `IInferenceBackend` 与 `IVisionBackend`，提供 CPU 优化桩实现。
- [x] 实现模型资源组、Worker、任务状态与结果释放契约。
- [x] 构建通用 runner（`zhulong_algo_runner`）、公共动态库（`libZhulongAlgoRuntime.so`）及两个示例插件（`MockDetector` 与 `MockClassifier`）。
- [x] 独立黑盒验包 CLI：`zhulong_algo_runner --verify-plugin <path> <sample>`，验证入口可运行并输出结构化 JSON 报告。
- [x] 1/2/4 worker 并发隔离验证。

---

## 3. 阶段二：帧视图、broker 与进程管理（已交付）

- [x] 补齐帧出口契约：`HardwareFrame` 多 plane/stride/长度解析。
- [x] 建立有界共享内存通道：`ShmPool` 8 槽位 × 4MB，分 plane 拷贝。
- [x] 引入 FrameBroker 扇出：支持多客户端租约机制（`Lease`）、Slot Generation 与 ACK 同步。
- [x] 宿主引擎接入：`ZhulongEngine` 持有按流独立的 `FrameBroker` 池，AI 消费者首次接入时创建，全部注销时销毁；发布线程按 `max(sample_fps)` 硬件节流。
- [x] C ABI 路径查询：`Zhulong_stream_get_broker_path` 供 Go 层查询 broker socket 路径。
- [x] 多路订阅：Go 层 `StreamHub.ResolveAIStreamBrokerPath` 解析并注入，Runner 支持多个 `--broker` 独立循环。
- [x] 慢消费者隔离：队列满时 Drop Latest，不阻塞视频采集与正常消费者。

---

## 4. 阶段三：升级、恢复与回滚（已交付）

- [x] 算法包状态机：有界指数退避自愈（200ms~10s）、稳定 30s 重置计数、连续崩溃 5 次转 `CRASH_LOOP` 熔断。
- [x] 数据库持久化：版本化 SQL 迁移 `000007_algorithm_packages.up.sql`，支持存储包元数据、版本历史与分析任务配置。
- [x] 升级排空与单次受控回滚：新版本就绪失败时自动受控回滚上一可用版本；旧版也失败则停止并报告，绝不循环切换。
- [x] 任务变更防抖动：200ms 合并窗口，批量任务增删改合并为单次包重启。

---

## 5. 阶段四：Rockchip 后端与共享权重验证（已交付）

- [x] CMake 编译期 Profile 隔离：`ZHULONG_INFERENCE_BACKEND=cpu|rknn` 与 `ZHULONG_VISION_BACKEND=cpu|rga` 两个正交开关。
- [x] RKNN 后端实现：`native/src/backends/rknn/` 实现 `RknnInferenceBackend`，vendored 依赖并做 SHA-256 门禁。
- [x] 权重共享成立：RK3568 实测 `rknn_dup_context` 内存共享，4 worker 内存峰值 60MB → 28MB。规避了 `RKNN_QUERY_MEM_SIZE` 破坏上下文的 SDK 缺陷。
- [x] RGA 后端实现：`native/src/backends/rga/` 实现 `RgaVisionBackend`，支持 Letterbox、Resize、Crop & Scale。
- [x] 修复 RGA 驱动死锁：全尺寸 canvas 包装目标 buffer，指针不偏移，通过 `drect` 传递 ROI，消除 DMA 越界与驱动挂起。
- [x] NV12 / YUV420P 多平面硬件 CSC：仅在连续内存下由 RGA 处理，非连续内存安全回退 CPU；颜色转换遵循 Limited-range BT.601。
- [x] 运行期硬件探测：探测 `byte_stride_align=4` 与 `offset_align=2`，非硬编码。
- [x] 产物隔离：交叉宿主 `Zhulong` 二进制 0 处链接 `librknnrt.so` 或 `librga.so`。

---

## 6. 最终门禁与全栈验证

- [x] 运行 `make native-test`：15/15 Passed（全测试套件全绿）。
- [x] 运行 `make go-check`：gofmt 零输出，go vet 通过，go test -race 通过，Go bridge integration 6/6 通过。
- [x] 运行 `make frontend-check`：eslint 0 问题，vitest 241/241 passed，tsc -b && vite build 成功。
- [x] 运行 `make check`：全栈门禁 exit 0，端到端零回归。
- [x] 目标板真机验证（RK3568）：
  - `ZhulongRgaTests` 10/10 PASS，`failures=0`
  - `ZhulongRknnTests` 6/6 PASS，`failures=0`
  - `ZhulongFrameBrokerTests` 14/14 PASS
  - `ZhulongRtspTests` PASS
- [x] 架构规范更新：`.trellis/spec/native/algo-plugin-contract.md`、`.trellis/spec/architecture.md`、`.trellis/spec/backend/dependency-injection.md` 同步最新合同。

---

## 7. 完整验收状态矩阵 (AC1–AC15)

| AC | 关联需求 | 状态 | 验证结果与证据 |
| --- | --- | :---: | --- |
| **AC1 独立构建** | R1–R3 | ✅ | 插件仅依赖 `native/sdk/include/zhulong/algo.hpp` 与标准 C 运行时；`MockDetector` 与 `MockClassifier` 独立编译装载通过 |
| **AC2 ABI/IPC 分离** | R1, R10 | ✅ | 纯 C ABI (`plugin.h`)、跨进程 IPC (`frame_broker.hpp`)、内部 C++ HAL 严格解耦；全异常 `try/catch` 拦截转状态码 |
| **AC3 结构化结果** | R3, R10 | ✅ | `zhulong_detection_result_t` 包含置信度、归一化坐标与标签；业务事件完全由宿主解析，插件无需访问 DB |
| **AC4 Worker 隔离与共享** | R4, R6 | ✅ | 单 Runner 进程内 1/2/4 worker 各自独占上下文与私有内存；`rknn_dup_context` 共享底层模型权重 |
| **AC5 权重共享实测** | R5, R6 | ✅ | RK3568 真机验证：4 worker 独立初始化内存峰值 60MB vs 派生共享 28MB；实测数据固化于 spec |
| **AC6 共享内存租约生命周期** | R4, R10 | ✅ | `ShmPool` + generation + slot lease；跨进程仅传 slot id 与偏移；客户端崩溃或超时后租约自动回收 |
| **AC7 故障注入与慢包隔离** | R4, R8 | ✅ | 队列满时 Drop Latest 不阻塞视频流；Runner 崩溃后指数退避自愈；连续 5 次崩溃熔断为 `CRASH_LOOP` |
| **AC8 升级安全排空** | R7 | ✅ | 升级期间旧进程 drain 排空并注销 broker 后退出，再启动新版本；宿主与其他包不重启 |
| **AC9 升级受控回滚** | R9 | ✅ | 新版本启动或健康检查失败时，自动恢复前一版本（`previous_version`）；旧版本仍失败则停止并报警，绝不反复振荡 |
| **AC10 构建合同与证据** | R10 | ✅ | 所有测试真实执行；无陈旧归档假绿；离线构建与 SHA-256 门禁严格成立 |
| **AC11 文档与规划一致性** | 全部 | ✅ | `prd.md`、`design.md`、`implement.md` 与 spec 全部闭环同步 |
| **AC12 公共运行库复用** | R1, R11 | ✅ | 两个示例插件共享 `libZhulongAlgoRuntime.so` 完成模型与前处理生命周期管理，非各自复制代码 |
| **AC13 单次黑盒验包** | R1, R12 | ✅ | `zhulong_algo_runner --verify-plugin` 传入单张图片独立执行推理并输出合法性报告，不依赖 Go 宿主服务 |
| **AC14 高层 C++17 SDK** | R11, R13 | ✅ | 提供现代 C++ 门面（`Model`, `Session`, `Tensor`, `letterbox`），内部 RAII 管理，对外暴露纯 C ABI |
| **AC15 多平台 HAL 解耦** | R5, R13 | ✅ | `IInferenceBackend` 与 `IVisionBackend` 抽象支持平台切换与 Profile 隔离编译；CPU/NEON 优化分支提供全自动 fallback |

---

## 8. 残余风险与后续演进

1. **未实现端到端 DMA-BUF 零拷贝**：当前 FrameBroker 采用逐 plane 拷贝到共享内存，RGA 采用虚拟地址映射。这是为了保证高通用性与跨平台稳定性；完全硬件零拷贝（DMA-BUF + sync_file）将作为下一阶段架构演进目标。
2. **多包共享 NPU 时的硬件排队开销**：当多个算法包（多个 Runner 进程）并发使用单 NPU 核时，硬件调用由内核驱动串行排队调度，吞吐不会随进程数线性提升。
3. **RK3588 (RGA3) 兼容性**：RK3588 存在 RGA3 核心（stride 16 / offset 64），当前实现已在代码与规范中详细记录适配策略，后续上板时可直接启用。
