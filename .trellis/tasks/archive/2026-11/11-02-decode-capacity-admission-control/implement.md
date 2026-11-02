# Implementation Plan: 硬件解码准入控制、容量契约与防静默软解降级

> 严格按阶段递进实施，每个阶段完成后运行对应层级的测试进行验证。
> 架构设计见 `design.md`，验收标准见 `prd.md`。

---

## 阶段一：C ABI 与 Native 核心能力支撑 (Native C++)

### 1.1 头文件与错误码定义
- **修改文件**：`native/include/Zhulong/engine.h`
  - 新增错误码：
    - `Zhulong_ERR_HARDWARE_UNAVAILABLE = -15`
    - `Zhulong_ERR_CAPACITY_EXCEEDED = -16`
  - 新增枚举 `Zhulong_decode_backend_t`；
  - 新增结构体 `Zhulong_decode_policy_t` 与 `Zhulong_decode_stats_t`（保持 16 字节对齐，跨架构 ABI 稳定）；
  - 声明全局配置与状态接口：
    - `Zhulong_status_t Zhulong_engine_configure_decode(Zhulong_engine_h engine, const Zhulong_decode_policy_t *policy);`
    - `Zhulong_status_t Zhulong_engine_get_decode_stats(Zhulong_engine_h engine, Zhulong_decode_stats_t *out_stats);`
- **验证命令**：`make native-build`

### 1.2 Native 解码工厂与双级 Fail-Closed 熔断机制
- **修改文件**：`native/src/nodes/decode/decoder_factory.cpp`
  - 约束硬解工厂创建逻辑：若策略要求强制硬解（`backend == mpp` 或 `!allow_software_fallback`），当底层不支持或初始化抛出异常时，严禁捕获后降级软解，统一向上抛出结构化异常；
  - 明确系统错误与码流错误的边界：单帧语法解析失败清空 DPB 并等待下个关键帧，不误触发硬解不可用。
- **修改文件**：`native/src/pipeline/stream.hpp`、`native/src/pipeline/stream.cpp`
  - 异步工作线程（`decode_loop`）不可逆致命故障熔断：捕获硬解不可用致命异常时，停止无脑重试退避丢包，将当前物理流状态置为 `Zhulong_STREAM_FAILED`，记录错误码 `-15`，中断拉流。

### 1.3 `Engine` 核心准入控制与配额管理
- **修改文件**：`native/src/pipeline/engine.hpp`、`native/src/pipeline/engine.cpp`
  - 新增 `Zhulong_decode_policy_t policy_` 成员；
  - 新增原子计数器 `std::atomic<uint32_t> active_decode_sessions_{0}` 与 `std::atomic<uint32_t> rejected_requests_{0}`；
  - **前置准入控制与门控**：
    - 接入期静态探针门控：当 `kind == Zhulong_CONSUMER_AI` 且策略要求硬解时，前置探测硬件就绪性，不可用直接抛出 `-15`；
    - 全新 AI 流准入拦截：当全新流且 `kind == Zhulong_CONSUMER_AI` 时，若已达上限，抛出 `-16` 并累加拒绝计数；
    - 既有流 AI 消费升级拦截：当既有流原先无 AI 消费者、现追加 `CONSUMER_AI` 时，执行容量拦截，已满则拒绝该 consumer 并抛出 `-16`；
  - **配额精确归还**：在 `Engine::release` 中，当物理流最后一个 AI 消费者被移除时，立即递减 `active_decode_sessions_`，与物理流 8 秒宽限期彻底解耦；
  - 实现 `configure_decode` 与 `get_decode_stats` 核心实现。
- **修改文件**：`native/src/abi/engine.cpp`
  - 导出 `Zhulong_engine_configure_decode` 与 `Zhulong_engine_get_decode_stats` 的 C ABI 门面（添加边界异常保护 `TRANSLATE_EXCEPTIONS`）。

### 1.4 Native 单元测试
- **修改/新增测试**：`native/tests/engine_test.cpp` 或新增 `native/tests/decode_admission_test.cpp`
  - 验证纯预览流不占用解码配额（接入超出 `max_sessions` 仍能成功）；
  - 验证全新 AI 流超过 `max_sessions` 时被立即拒绝，返回 `Zhulong_ERR_CAPACITY_EXCEEDED`；
  - 验证已有预览流在动态追加 AI 消费者时正确触发容量拦截；
  - 验证 AI 消费者释放后解码配额立即回收（无需等待宽限期超时）；
  - 验证强制硬解模式下静态探针失败时同步阻断。
- **验证命令**：`make native-test`

---

## 阶段二：Go 宿主层配置、CGO 桥接与错误映射 (Go)

### 2.1 CGO 错误码与哨兵错误
- **修改文件**：`internal/engine/errors.go`
  - 新增 `StatusHardwareUnavailable StatusCode = -15`；
  - 新增 `StatusCapacityExceeded StatusCode = -16`；
  - 补齐 `statusMessages` 字典；
  - 增加哨兵错误 `ErrHardwareUnavailable` 与 `ErrCapacityExceeded`；
  - 补充单测 `internal/engine/validation_test.go`。

### 2.2 Go 引擎生命周期与解码配置桥接
- **修改文件**：`internal/engine/engine.go`
  - 定义 Go 结构体 `DecodePolicy` 与 `DecodeStats`；
  - 在 `Engine` 增加 `ConfigureDecode(policy DecodePolicy) error` 与 `GetDecodeStats() (DecodeStats, error)`；
  - 在 `Engine.Start()` 阶段根据配置通过 CGO 调用原生注入策略。

### 2.3 `config.toml` 配置段与解析
- **修改文件**：`internal/app/config.go`
  - 增加 `DecodeConfig` 结构体（`Backend string`, `AllowSoftwareFallback bool`, `MaxSessions int`）；
  - 设置合理的平台兼容默认值（Host: auto/true/8; 板端: mpp/false/8）；
  - 更新 `config.toml.example`；
  - 保证 viper 严格模式解析通过。

### 2.4 HTTP 状态码与国际化信封
- **修改文件**：`internal/httputil/locale.go`、`internal/httputil/errors.go`
  - 注册机器码 `HARDWARE_DECODE_UNAVAILABLE`（HTTP 503）与 `DECODE_CAPACITY_EXCEEDED`（HTTP 429）；
  - 补齐 `en`、`zh-Hans`、`zh-Hant` 三语全量映射。

### 2.5 健康检查端点扩展
- **修改文件**：`internal/app/router.go`
  - 遵循 `AGENTS.md` 命名约定：新增结构体 `DecodeStatus`（严禁使用 `*DTO`）；
  - 在 `HealthData` 结构体扩展 `Decode *DecodeStatus` 字段；
  - 保持根级别 `status: "ready"` 仅与数据库和引擎存活挂钩；当强制硬解但探针失败时，将 `components["engine"]` 标识为 `"degraded"`。
- **验证命令**：`make go-check`

---

## 阶段三：FrameBroker CMA 槽位内存优化 (消除 OOB 崩溃与 CMA 适配)

### 3.1 槽位数收敛与单槽尺寸规格化
- **修改文件**：`native/src/algorithm/ipc/frame_broker.hpp`、`native/src/algorithm/ipc/frame_broker.cpp`
  - 默认 `slot_count` 由 8 调整为 3（与流队列表头深度严格对齐）；
  - 槽位尺寸规格化：默认采用 1080P NV12 规格上限（3.5MB），3 槽位合计申请约 10.5MB CMA。

### 3.2 全量原子分配与安全回退
- **修改文件**：`native/src/algorithm/ipc/frame_broker.cpp`
  - 改造 DMA-BUF 槽位初始化循环：
    - 逐一分配，若全部槽位申请成功，启用 `has_dma_slots_ = true`；
    - 若出现任何一个槽位分配失败，**全量清空已分配槽位**，彻底退回标准 POSIX 共享内存双轨模式，输出明确 WARN 日志；
  - **绝对保证** `dma_slots_` 长度要么等于 `options_.slot_count`，要么为 0，彻底根除越界访问导致的 SIGSEGV 隐患。
- **验证命令**：`make native-test`

---

## 阶段四：全链路与板端验收

### 4.1 宿主全链路自动化测试
- 编写 Go 集成测试覆盖：
  - 纯预览流不计入容量，可多路正常接入；
  - AI 流达到 `max_sessions` 时返回 HTTP 429 `DECODE_CAPACITY_EXCEEDED`；
  - 预览流升级为 AI 流时触发 429 拦截；
  - 释放 AI 消费者后配额立即回收。

### 4.2 板端实机验证 (RK3568)
1. 编译宿主二进制：`make cross-board`
2. 部署到板端：
   - 验证正常情况下 MPP 硬解运行良好（`/proc/interrupts` `rkvdec` 计数增长）；
   - 验证 FrameBroker 启动日志显示 DMA-BUF 槽位池成功就绪（3×3.5MB 适配 16MB CMA 预算，不再因超额 32MB 失败）；
   - 验证配置 `max_sessions = 1` 时，接入第 2 路 AI 视频流时立即返回 429；
   - 验证故障注入（临时剥夺 `/dev/mpp_service` 权限）时，AI 拉流被立即阻断返回 503，CPU 保持空闲状态（无软解单核 100% 飙高）。
3. 验证端点：`curl http://<board>:8000/api/v1/health` 检查 `decode` 节点完整准确。
