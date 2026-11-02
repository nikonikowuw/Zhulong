# PRD: 硬件解码准入控制、容量契约与防静默软解降级

## Goal

针对嵌入式/边缘计算平台（以 RK3568 等 ARM SoC 为典型代表），建立**显式的硬件解码能力声明、并发路数准入控制与防静默降级机制**：
1. **防静默软解降级 (Fail-Closed)**：彻底消除生产环境中「硬解失败无感知降级到 CPU 软解，导致 1080P/4K 挤占 1~4 个 CPU 核心、拖死全系统」的隐患，提供双级防御的硬解强制模式；
2. **容量声明与前置准入控制 (Admission Control)**：在配置层声明设备解码能力上限（`max_sessions`），并在流接入与消费升级阶段做前置准入拦截，杜绝「接入成功却在发帧/解码阶段随机崩溃或丢帧」；
3. **解码健康度与观测暴露**：在健康检查与运维接口暴露硬件解码器就绪状态、在用解码路数/容量上限、以及拒绝计数，严格遵循现有组件健康契约。

---

## Background：已确认的现场与仓库事实

| 编号 | 事实分类 | 事实描述与现场实测数据 | 证据来源 |
| :--- | :--- | :--- | :--- |
| **F1** | 硬件能耗对比 | RK3568 (4×Cortex-A55) 软解单路 1080P25 H.264 需 **0.63 核**，H.265 需 **1.09 核**（单核无法跑满 25fps）；4 路即耗尽整机 CPU 算力。而 MPP 硬解通过 VPU 处理，CPU 占用近乎 **0%**。 | 板端实测 ffmpeg benchmark 数据；`/proc/interrupts` `rkvdec` 中断 9→18 |
| **F2** | 策略硬编码 | 视频流拉流创建硬编码为 `DecoderBackend::automatic`，C ABI 公开头文件 `native/include/Zhulong/engine.h` 零解码控制参数，Go 宿主无法选择或约束解码后端。 | `native/src/pipeline/stream.cpp:216`、`native/include/Zhulong/engine.h` |
| **F3** | 底层已有拒绝能力 | `decoder_factory.cpp` 中若收到 `DecoderBackend::mpp_hw`，在不支持或初始化失败时会直接抛异常；但因上层固定传入 `automatic`，该分支永远被绕过并落入 `ZL_LOGI("mpp_unavailable_fallback")` 静默软解。 | `native/src/nodes/decode/decoder_factory.cpp:25-43` |
| **F4** | 容量声明完全缺失 | `config.toml` 与 `internal/app/config.go` 没有任何关于解码能力、路数限制（`max_sessions`）的配置段，运维无法向系统告知当前硬件规格。 | `config.toml`、`internal/app/config.go` |
| **F5** | 准入控制滞后且口径缺失 | `stream_acquire` 仅校验 URL 合法性与参数冲突，无任何解码容量准入检查。此外，纯预览流（无需解码）与 AI 分析流（需解码）共用一套逻辑，且流复用时后加 AI 消费者能够直接绕过全局计数。 | `native/src/pipeline/engine.cpp:210-276` |
| **F6** | 内存池与硬件失配 | `FrameBroker` 的零拷贝槽位池按 `8 × 4MB = 32MB` 一次性向 CMA 申请，而 RK3568 实测 `CmaTotal` 仅 **16MB**，导致启动必然失败并退回普通共享内存（增加 CPU 拷贝）。 | `/proc/meminfo` CmaTotal、`native/src/algorithm/ipc/frame_broker.cpp:99-125` |

---

## Requirements

### R1. 解码配置段与能力声明 (`config.toml`)

- 新增 `[decode]` 顶级配置节，包含以下核心参数：
  - `backend = "mpp" | "ffmpeg" | "auto"`：
    - `mpp`：强制硬件解码，不可用或失败时直接报错，拒绝降级；
    - `ffmpeg`：强制 CPU 软解（仅供 x86 开发机或测试桩调试使用）；
    - `auto`：优先尝试硬解，不可用时降级为软解（开发环境默认）。
  - `allow_software_fallback = false | true`：
    - 嵌入式 ARM Profile 模板默认必须为 `false`；
    - 为 `false` 时，任何原因导致的硬解失败均不得回退到 CPU 软解。
  - `max_sessions = <int>`：
    - 本机允许接入的最大并发**物理解码流**路数（默认值为 8；为 0 时表示不设软件上限）。
- viper 严格模式兼容：字段补齐到 `internal/app/config.go` 的 `Config` 结构体中，未配置时提供与平台兼容的默认值。

### R2. C ABI 全局解码策略透传与双级 Fail-Closed 防御

- **全局引擎级策略配置**：
  - 定义全局解码策略结构体 `Zhulong_decode_policy_t`：
    ```c
    typedef struct {
        int32_t backend;                 /* 0: auto, 1: mpp, 2: ffmpeg */
        int32_t allow_software_fallback; /* 0: 严禁软解降级 (fail-closed), 1: 允许软解兜底 */
        int32_t max_sessions;            /* >0: 允许的最大并发物理解码流数; 0: 不设软上限 */
    } Zhulong_decode_policy_t;
    ```
  - C ABI 暴露配置与状态接口：
    - `Zhulong_status_t Zhulong_engine_configure_decode(Zhulong_engine_h engine, const Zhulong_decode_policy_t *policy);`
    - `Zhulong_status_t Zhulong_engine_get_decode_stats(Zhulong_engine_h engine, Zhulong_decode_stats_t *out_stats);`
- **双级 Fail-Closed 防御机制**：
  - **第一道防线：接入期静态探针门控 (Static Hardware Gatekeeper)**：
    - 在调用 `Zhulong_stream_acquire` 接入 AI 消费流（`kind == Zhulong_CONSUMER_AI`）时，若配置要求硬解且禁止软解（`backend == mpp` 或 `allow_software_fallback == false`），前置校验硬件解码能力（调用静态探针验证 `/dev/mpp_service` 权限与驱动状态）；
    - 若静态探针失败，**直接在 `acquire` 阶段同步阻断**，返回错误码 `Zhulong_ERR_HARDWARE_UNAVAILABLE` (-15)，杜绝建立无效拉流。
  - **第二道防线：异步解码运行期熔断 (Runtime Circuit Breaker)**：
    - 在 RTSP 流完成 SDP 协商并向 `DecodeSession` 投递首批数据包时，若底层 MPP 解码器初始化发生不可逆系统错误（如驱动异常、CMA 显存耗尽等非丢包故障）：
      - 工厂严禁降级为软解，记录 ERROR 日志；
      - 解码工作线程不可无脑死循环退避丢包，必须触发熔断：将当前物理流状态置为 `Zhulong_STREAM_FAILED`，记录错误码 `-15`，中断拉流；
      - 严防静默黑屏与僵尸流。
  - **网络残损包与系统故障的严格区分**：
    - 针对弱网丢包导致的单帧语法错误或缺少参考帧，MPP 解码器执行清空 DPB（`flush`）并等待下一个关键帧重试，**严禁将正常的单帧解码错误误判为硬件损坏**。
- **C ABI 错误码新增**：
  - `Zhulong_ERR_HARDWARE_UNAVAILABLE = -15`：硬件加速器不可用（MPP 未就绪、设备节点权限缺失或初始化失败，且配置禁止软解）；
  - `Zhulong_ERR_CAPACITY_EXCEEDED = -16`：并发物理解码流已达系统声明的 `max_sessions` 上限。
  - Go 侧在 `internal/engine/errors.go` 对应映射为 `StatusHardwareUnavailable` 与 `StatusCapacityExceeded`。

### R3. `stream_acquire` 准入控制与容量度量 (Admission Control)

- **度量口径统一为「物理解码流」**：
  - 纯预览流（`Zhulong_CONSUMER_PREVIEW`）只转发 AVPacket，不创建 `DecodeSession`，**不计入解码并发配额**；
  - 仅挂载了 AI 分析消费者（`Zhulong_CONSUMER_AI`）的物理流才占用 1 个物理解码配额；
  - 同一个 RTSP URL 被预览和多个 AI 分析任务共享时，仅占用 1 个物理流与 1 个解码配额。
- **准入拦截点全覆盖（严防偷渡）**：
  - **全新流接入拦截**：全新流且 `kind == Zhulong_CONSUMER_AI` 时，若 `active_decode_sessions >= max_sessions`，拒绝接入，抛出 `Zhulong_ERR_CAPACITY_EXCEEDED`；
  - **既有流消费升级拦截**：若既有流原先仅有预览消费者（无解码器），新请求为该流追加 `Zhulong_CONSUMER_AI` 从而需要拉起解码器时，**必须在此处执行配额校验**；若已满则拒绝该 consumer 注册并抛出 `-16`，保证现有预览流不受影响；
  - **配额精确释放**：当物理流的最后一个 AI 消费者被 `release` 且解码器停止时，立即归还解码配额，无需等待 8 秒 Grace Period 超时。
- **Go HTTP 层错误映射**：
  - `StatusCapacityExceeded` 统一映射为 **HTTP 429 Too Many Requests**（机器码 `DECODE_CAPACITY_EXCEEDED`）；
  - `StatusHardwareUnavailable` 统一映射为 **HTTP 503 Service Unavailable**（机器码 `HARDWARE_DECODE_UNAVAILABLE`）；
  - 遵循统一信封规范，提供中英文本地化文案，不泄露私有指针或 URL 凭据。

### R4. 启动自检与健康检查观测对齐 (`/api/v1/health`)

- **健康检查契约对齐**（遵循 `AGENTS.md`：`ready` 仅由数据库与引擎生命周期决定）：
  - `GET /api/v1/health` 维持现有根级信封契约：
    ```json
    {
      "code": "OK",
      "message": "success",
      "data": {
        "status": "ready",
        "components": {
          "database": "ready",
          "engine": "ready",
          "logging": "ready"
        },
        "decode": {
          "backend": "mpp",
          "hardware_available": true,
          "active_sessions": 2,
          "max_sessions": 8,
          "rejected_requests": 0
        }
      }
    }
    ```
  - 当配置声明 `backend = "mpp"` 且探针失败时，`components["engine"]` 降级为 `"degraded"`，以告知运维硬解异常，但不改变整体 HTTP 200 与数据库就绪状态。
- **类型命名规范约束**：
  - 严格遵循 `AGENTS.md` 命名约定，结构体使用 `DecodeStatus` 或 `DecodeStatsInfo`，**严禁使用 `*DTO` 后缀**。

### R5. FrameBroker 槽位内存预算合理化 (消除 OOB 崩溃与 CMA 适配)

- **槽位尺寸静态规格化**：
  - 不采用不可行的“拉流前动态分辨率推导”，统一按主流 1080P NV12 规格（`1920 × 1088 × 1.5 ≈ 3.13MB`，对齐预留到 3.5MB）作为单槽标准上限，4K 规格按需由配置指定；
- **槽位总数下调**：
  - 默认槽位数由 8 降为 3（与流队列表头深度严格对齐）；
  - 3 槽位合计申请约 10.5MB CMA，能够稳妥落在 RK3568 的 16MB CMA 预算内。
- **全量原子分配与安全回退**：
  - 尝试为 3 个槽位逐一申请 DMA-BUF；
  - **必须全部槽位申请成功**才置 `has_dma_slots_ = true`；
  - 若其中任何一个槽位申请失败（如 CMA 碎片化），**立即释放已申请的部分槽位**，彻底退回 POSIX 共享内存双轨模式，并输出包含清晰原因的 WARN 日志；
  - **绝对严禁** `dma_slots_` 长度小于 `options_.slot_count`，彻底根除数组越界访问导致 SIGSEGV 的隐患。

---

## Acceptance Criteria

- [ ] **AC1: 配置解析与边界校验**
  - `config.toml` 中正确解析 `[decode]` 配置项（`backend`, `allow_software_fallback`, `max_sessions`）；
  - viper 严格模式通过，不合法的 backend 值在启动前校验拦截。
- [ ] **AC2: Fail-Closed 防静默软解降级实测**
  - 当配置 `backend = "mpp"` 且 `allow_software_fallback = false` 时：
    - 正常环境下：MPP 硬解正常启动（`/proc/interrupts` `rkvdec` 计数增长）；
    - 故障注入下（如临时剥夺 `/dev/mpp_service` 权限）：AI 拉流请求在 `acquire` 阶段被立即阻断，返回 `HARDWARE_DECODE_UNAVAILABLE` (HTTP 503)，**CPU 保持在空闲水平（严禁出现单核 100% 软解现象）**。
- [ ] **AC3: 并发容量准入控制拦截**
  - 设置 `max_sessions = 2`：
    - 接入 10 路纯预览流：正常通过，不占用解码配额；
    - 接入第 1、第 2 路不同 AI 分析流：成功建立并硬解；
    - 接入第 3 路不同 AI 分析流：在 `acquire` 阶段立即被拒绝，返回 HTTP 429 `DECODE_CAPACITY_EXCEEDED`；
    - 对已存在的预览流追加 AI 消费者（流升级）：若配额已满，消费升级被拦截，返回 429，原预览流不受影响；
    - 释放第 1 路 AI 消费者后，新的 AI 流能够立即成功接入（配额原子回收）。
- [ ] **AC4: 健康检查与观测端点对齐**
  - `GET /api/v1/health` 维持现有契约，并在 `data.decode` 返回当前解码器配置、探针状态及活跃/最大路数；
  - 出现准入拦截时，被拒绝计数增加。
- [ ] **AC5: 板端实机全链路验收 (RK3568)**
  - 硬解通路顺畅；
  - FrameBroker 3×3.5MB 成功在 16MB CMA 下建立硬件 DMA-BUF 槽位池，日志不再报错退回纯 SHM；
  - 故障注入生效并拦截；
  - 算法包（人脸/人车）在硬解正常通路下的 NPU 推理不受任何负面影响。

---

## Non-Goals (显式排除范围)

1. **动态分辨率热重采样**：不在本任务中实现根据剩余显存动态修改 RTSP 编码分辨率。
2. **多显存跨设备调度**：本系统针对单片边缘 SoC（RK3568/RK3588），不涉及多显卡或跨物理节点负载均衡。
3. **动态更换模型结构**：不涉及根据解码负载动态热替换下游算法包检测模型。
