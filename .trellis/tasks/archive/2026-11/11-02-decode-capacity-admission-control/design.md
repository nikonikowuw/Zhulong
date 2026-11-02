# Design: 硬件解码准入控制、容量契约与防静默软解降级

> 本文档规范硬件解码后端策略选择、全局容量声明、前置准入控制与 Fail-Closed 异常拦截的架构与技术实现。
> 需求与验收见 `prd.md`，执行分解见 `implement.md`。

---

## 1. 核心决策与边界

| 维度 | 选择 | 依据与否决的替代方案 |
| :--- | :--- | :--- |
| **策略作用域** | **全局引擎级**（由 `config.toml` 配置并在引擎启动前一次性注入） | **否决会话级**（给单路 `Zhulong_stream_options` 加参数）：设备能否硬解、最多扛几路是整机物理供电、散热与 VPU 算力属性，非单相机属性；避免修改核心 ABI 结构体导致第三方破坏性重建。 |
| **降级策略** | **Fail-Closed 双级防御**（接入期静态门控阻断 + 运行期故障熔断，板端默认 `allow_software_fallback = false`） | **否决静默降级**：实测 4×A55 软解 1080P25 H.264 需 0.63 核、H.265 需 1.09 核，4 路即耗尽全机 CPU，严重破坏系统稳定性与算法实时性。**否决仅在异步解码层做拦截**：流在接入阶段若未探测到视频元数据，无法同步抛错，必须在前置门控拦截。 |
| **准入控制点** | **前置于 `Zhulong_stream_acquire` 阶段（含全新流接入与既有流 AI 消费升级）** | **否决在 publish_frame 或 decoder 内部抛错**：接入阶段拒绝能做到零网络连接、零线程启动、零内存预占开销，且 HTTP 响应能给出明确 429。 |
| **并发度量口径** | **以活跃物理解码流（Active Physical Decode Streams）为单位** | **否决物理流（streams.size()）计数**：纯预览流不使用 VPU 解码器，不能霸占硬解配额；同时同一物理流多个 AI 消费者复用 1 个解码器，仅计 1 路。 |
| **CMA 槽位优化** | **默认槽位 8 → 3，单槽上限静态规格化为 3.5MB，全量原子分配** | **否决无条件预分配 32MB**：RK3568 全机 CMA 仅 16MB，3×3.5MB（约 10.5MB）可在预算内成功分配。**否决部分成功启动**：部分成功会导致 `slots_` 与 `dma_slots_` 长度失配，存在致命越界崩塌风险；分配不全时必须干净退回标准共享内存。 |

---

## 2. 跨层架构与契约设计

### 2.1 C ABI 接口演化 (`native/include/Zhulong/engine.h`)

新增结构体与 API（保持现有 API 向后兼容，不破坏既有函数签名与二进制兼容性）：

```c
/** @brief 解码后端选择枚举 */
typedef enum {
    Zhulong_DECODE_BACKEND_AUTO   = 0, /**< 优先硬件解码，不可用时降级为软解（开发环境默认） */
    Zhulong_DECODE_BACKEND_MPP    = 1, /**< 强制 Rockchip MPP 硬件解码，不可用时拒绝 */
    Zhulong_DECODE_BACKEND_FFMPEG = 2  /**< 强制 FFmpeg 软解（仅供调试或无硬件环境） */
} Zhulong_decode_backend_t;

/** @brief 全局解码策略配置结构体 */
typedef struct {
    int32_t backend;                 /**< Zhulong_decode_backend_t 枚举 */
    int32_t allow_software_fallback; /**< 0: 严禁软解降级 (fail-closed); 1: 允许软解降级 */
    int32_t max_sessions;            /**< 最大并发物理解码流路数（>0 生效；0 为不限） */
} Zhulong_decode_policy_t;

/** @brief 解码器运行态统计结构体（16字节紧凑对齐，跨平台 ABI 稳定） */
typedef struct {
    int32_t active_sessions;         /**< 当前活跃的物理解码流数 */
    int32_t max_sessions;            /**< 允许的最大并发物理解码流数 */
    int32_t rejected_requests;       /**< 历史累计被准入拒绝或降级拒绝的请求次数 */
    int32_t hardware_available;      /**< 硬件解码器底层是否就绪（1=可用, 0=不可用） */
} Zhulong_decode_stats_t;

/** @brief 新增 C ABI 错误码定义 */
#define Zhulong_ERR_HARDWARE_UNAVAILABLE -15  /**< 硬件解码器不可用且禁止软解 */
#define Zhulong_ERR_CAPACITY_EXCEEDED    -16  /**< 并发解码路数已达系统声明的上限 */

/**
 * @brief 配置引擎全局解码策略
 *
 * 必须在 Zhulong_engine_create 之后、拉流之前调用；支持在运行期间动态更新策略。
 *
 * @param engine 引擎句柄
 * @param policy 解码策略指针
 * @return Zhulong_status_t 状态码
 */
Zhulong_status_t Zhulong_engine_configure_decode(Zhulong_engine_h engine, const Zhulong_decode_policy_t *policy);

/**
 * @brief 查询引擎解码器运行态统计信息
 *
 * @param engine 引擎句柄
 * @param[out] out_stats 统计信息输出缓冲区
 * @return Zhulong_status_t 状态码
 */
Zhulong_status_t Zhulong_engine_get_decode_stats(Zhulong_engine_h engine, Zhulong_decode_stats_t *out_stats);
```

---

### 2.2 Go 宿主层配置与错误映射

#### 1. `config.toml` 配置定义
```toml
[decode]
backend = "mpp"                  # "mpp" | "ffmpeg" | "auto"
allow_software_fallback = false  # 板端 profile 必须为 false
max_sessions = 8                 # 允许接入的最大并发物理解码流数
```

#### 2. `internal/engine/errors.go` 错误码映射
```go
const (
    StatusHardwareUnavailable StatusCode = -15
    StatusCapacityExceeded    StatusCode = -16
)

var statusMessages = map[StatusCode]string{
    // ... 既有条目 ...
    StatusHardwareUnavailable: "hardware decoder is unavailable and software fallback is disabled",
    StatusCapacityExceeded:    "maximum concurrent decode sessions exceeded",
}
```

#### 3. `internal/httputil/locale.go` HTTP 错误信封映射
- `StatusHardwareUnavailable` 映射为机器码 `HARDWARE_DECODE_UNAVAILABLE`，HTTP 状态码 **503 Service Unavailable**；
- `StatusCapacityExceeded` 映射为机器码 `DECODE_CAPACITY_EXCEEDED`，HTTP 状态码 **429 Too Many Requests**；
- 中英文完整本地化映射：
  - `HARDWARE_DECODE_UNAVAILABLE`:
    - `en`: "Hardware video decoder is unavailable and software fallback is disabled"
    - `zh-Hans`: "硬件视频解码器不可用且已禁止软解兜底"
    - `zh-Hant`: "硬體視訊解碼器無法使用且已停用軟解兜底"
  - `DECODE_CAPACITY_EXCEEDED`:
    - `en`: "Maximum concurrent hardware decode capacity exceeded"
    - `zh-Hans`: "并发硬件解码路数已达系统上限"
    - `zh-Hant`: "並行硬體解碼路數已達系統上限"

---

## 3. 核心机制详细设计

### 3.1 准入控制与配额生命周期 (`Engine::acquire` & `Engine::release`)

系统度量对象严格定义为**物理解码流（Active Physical Decode Streams）**，由 `active_decode_sessions_` 维护。

#### 准入算法详细流程：
```
收到 acquire(url, options, consumer, kind)
  │
  ├─ 加锁 control 互斥锁
  │
  ├─ [第一道防线: 静态硬件门控]
  │    若 kind == Zhulong_CONSUMER_AI 且策略要求强制硬解 (backend == mpp || !allow_software_fallback):
  │       若 !probe_hardware_available():
  │          rejected_requests_++
  │          抛出 Failure{Zhulong_ERR_HARDWARE_UNAVAILABLE} (-15)
  │
  ├─ 检查 URL 是否已存在于 streams 容器中？
  │    ├─ [是 (URL 命中既有流)]:
  │    │    ├─ 检查 options 是否配置冲突 (若是抛 CONFIG_CONFLICT)
  │    │    ├─ 检查 consumer 是否已存在 (若是抛 DUPLICATE)
  │    │    │
  │    │    ├─ [既有流 AI 消费升级门控]:
  │    │    │  若 kind == Zhulong_CONSUMER_AI 且 !existing->has_ai_consumer():
  │    │    │     // 既有流此前仅有预览消费，现需拉起解码器
  │    │    │     若 policy_.max_sessions > 0 && active_decode_sessions_ >= policy_.max_sessions:
  │    │    │        rejected_requests_++
  │    │    │        抛出 Failure{Zhulong_ERR_CAPACITY_EXCEEDED} (-16)
  │    │    │     active_decode_sessions_++
  │    │    │
  │    │    ├─ 注册 consumer: existing->consumers.emplace(consumer, kind)
  │    │    ├─ existing->expiry = Clock::time_point::max()
  │    │    ├─ 同步解码会话: existing->sync_decode_pipeline()
  │    │    └─ 确保 FrameBroker 运行并返回 existing->id
  │    │
  │    └─ [否 (全新物理流)]:
  │         ├─ [全新 AI 流容量门控]:
  │         │  若 kind == Zhulong_CONSUMER_AI:
  │         │     若 policy_.max_sessions > 0 && active_decode_sessions_ >= policy_.max_sessions:
  │         │        rejected_requests_++
  │         │        抛出 Failure{Zhulong_ERR_CAPACITY_EXCEEDED} (-16)
  │         │     active_decode_sessions_++
  │         │
  │         ├─ 创建 Stream 实例并加入 streams 容器
  │         ├─ 启动拉流与 FrameBroker
  │         └─ 解锁并返回 stream->id
```

#### 配额释放与回收算法 (`Engine::release`)：
```
收到 release(stream_id, consumer)
  │
  ├─ 加锁 control 互斥锁
  │
  ├─ 检查流与 consumer 存在性，校验订阅无占用
  ├─ stream->consumers.erase(consumer)
  │
  ├─ 检查该流是否已无 AI 消费者 (was_decoding && !stream->has_ai_consumer()):
  │    ├─ active_decode_sessions_-- (原子归还解码配额，无需等待 8s 宽限期)
  │    ├─ 同步停止 DecodeSession、FramePublisher 与 FrameBroker
  │
  ├─ 若流的全部 consumers 均已注销:
  │    └─ 标记 expiry = now() + grace_period (8秒后 Reaper 线程彻底清理物理流)
```

---

### 3.2 解码后端解析与双级 Fail-Closed 机制

#### 1. 第一级：接入期静态门控 (`Engine::acquire`)
在流接纳阶段，通过 `MppDecodeNode::is_supported()` 静态探测板端 `/dev/mpp_service` 节点状态与底层驱动接口。若驱动损坏或权限不足，且用户配置了 `allow_software_fallback = false`，在 `acquire` 阶段立即抛出 `Failure{Zhulong_ERR_HARDWARE_UNAVAILABLE}`，杜绝启动无效的网络拉流。

#### 2. 第二级：运行期不可逆异常熔断 (`DecodeSession::decode_loop`)
当流连通并开始解码时，若底层遇到驱动故障或无法恢复的系统错误：
- 工厂创建阶段（`decoder_factory.cpp`）：
  - 若策略要求硬解或禁止降级，`IDecodeNode::create` 严禁退回 `FFmpegDecodeNode`，抛出结构化异常；
- 异步工作循环（`stream.cpp`）：
  - 解码线程捕获到硬件初始化失败时，识别错误类型：
    - 若为系统级致命错误（硬件不可用/CMA显存耗尽且禁止降级）：**停止指数退避重试**；将当前物理流的运行状态更新为 `Zhulong_STREAM_FAILED`，记录错误码 `-15`，中断拉流并释放内部资源；
    - 若为普通的网络传输丢包或瞬态残损包：调用 `dec->flush()` 清空 DPB 并等待下一个关键帧重试，不中断流。

---

### 3.3 FrameBroker CMA 槽位内存优化与原子安全降级

针对嵌入式板端（RK3568 全机 CMA 仅 16MB）做以下安全加固：

1. **槽位总数缩减**：
   - 默认槽位数由 8 调整为 **3**（与物理流 `frame_queue_` 深度 3 严格一致，消灭闲置槽位）；
2. **槽位尺寸静态规格化**：
   - NV12 1080P 规格计算：`1920 × 1088 × 1.5 = 3,133,440 字节 (≈ 3.0MB)`；
   - 槽位按 64 字节对齐预留取整为 **3.5MB**（`3,670,016` 字节）；
   - 3 槽位合计申请约 **10.5MB** CMA，完全落在 16MB 的预算范围内。
3. **全量原子分配与安全降级**：
   ```cpp
   // 伪代码：消除 OOB 越界崩溃的原子分配策略
   dma_slots_.clear();
   has_dma_slots_ = false;

   std::vector<std::unique_ptr<DmaBuffer>> allocated_bufs;
   bool all_succeeded = true;

   for (size_t i = 0; i < options_.slot_count; ++i) {
       auto buf = dma_allocator.allocate(options_.slot_size);
       if (!buf) {
           all_succeeded = false;
           break;
       }
       allocated_bufs.push_back(std::move(buf));
   }

   if (all_succeeded) {
       dma_slots_ = std::move(allocated_bufs);
       has_dma_slots_ = true;
       ZL_LOGI(0, "ipc.broker", "dma-buf slot pool ready: slots=%zu slot_size=%zu",
               dma_slots_.size(), options_.slot_size);
   } else {
       // 任何一个分配失败：全量归还，安全回退到纯 POSIX 共享内存
       allocated_bufs.clear();
       dma_slots_.clear();
       has_dma_slots_ = false;
       ZL_LOGW(0, "ipc.broker", "dma-buf slot pool allocation failed; safely falling back to shm");
   }
   ```
   **内存安全保证**：`dma_slots_` 的大小要么恒等于 `options_.slot_count`，要么为 0。绝对杜绝长度失配导致的访问越界。

---

### 3.4 健康检查与观测端点 (`/api/v1/health`)

严格遵循 `AGENTS.md` 规范（`ready` 仅由数据库与引擎生命周期决定）：

```go
type DecodeStatus struct {
	Backend           string `json:"backend"`
	HardwareAvailable bool   `json:"hardware_available"`
	ActiveSessions    int32  `json:"active_sessions"`
	MaxSessions       int32  `json:"max_sessions"`
	RejectedRequests  int32  `json:"rejected_requests"`
}

type HealthData struct {
	Status     string            `json:"status"`     // "ready" | "degraded"
	Components map[string]string `json:"components"` // "database", "engine", "logging"
	Decode     *DecodeStatus     `json:"decode,omitempty"`
}
```

- 若硬件探测正常且未超载：`components["engine"] = "ready"`；
- 若配置声明强制硬解（`backend == "mpp"`）但硬件探针失败：`components["engine"] = "degraded"`，以告知运维硬解异常，但若宿主数据库正常，整体 `Status` 仍可提供基础管控能力。

---

## 4. 风险与缓解措施

| 风险点 | 严重级 | 缓解措施 |
| :--- | :--- | :--- |
| **x86 开发机或 CI 环境无 `/dev/mpp_service` 导致测试失败** | 高 | 开发环境和 CI 默认配置为 `backend = "auto"`, `allow_software_fallback = true`，仅在指定板端 profile 或显式设置 `mpp` 且禁止降级时才执行 fail-closed 拦截。 |
| **高并发下流复用与消费升级竞态** | 中 | `Engine::acquire` 与 `Engine::release` 全流程在已有的 `control` 互斥锁保护下原子完成，消费升级与配额扣减无任何并发竞态窗口。 |
| **网络丢包被误判为硬解崩溃** | 中 | 明确区分系统级错误（驱动不可用、显存耗尽）与码流级错误（单帧残损、语法错）；后者执行 `flush` 并静默等待下个关键帧重试。 |
