# 设计：推理结果跨进程出口 (Inference Result Egress)

> 任务：`10-14-inference-result-egress`
> 父任务：`10-14-face-recognition-e2e`

---

## 1. 目标与架构边界

打通算法推理结果由 **Runner 子进程 ➔ FrameBroker (UDS) ➔ ZhulongEngine (C ABI) ➔ Go 宿主 ➔ 业务消费** 的完整上行回路。

### 1.1 端到端数据流拓扑

```txt
┌───────────────────────────────── zhulong_algo_runner ────────────────────────────────┐
│ 1. 从 SHM 读取解码帧                                                                  │
│ 2. api.process(task, &view, &result)                                                 │
│ 3. 状态机过滤 (静默/退出帧判断)                                                        │
│ 4. 序列化为 MSG_RESULT JSON 载荷 ──(UDS 既有连接)──┐                                 │
│ 5. api.result_release(result) (无 use-after-free) │                                 │
│ 6. 发送 MSG_FRAME_ACK                             │                                 │
└───────────────────────────────────────────────────┼─────────────────────────────────┘
                                                    │
                                                    ▼
┌─────────────────────────────── Native ZhulongEngine ────────────────────────────────┐
│ FrameBroker (UDS 服务端: /tmp/zhulong_broker_<stream_id>.sock)                       │
│   ├─ 接收 MSG_RESULT (msg_type = 7)                                                 │
│   └─ 触发 on_result 回调 ➔ Engine AI 订阅分发器                                      │
│                                                   │                                 │
│ 导出 C ABI (native/include/Zhulong/engine.h)      │                                 │
│   ├─ Zhulong_stream_subscribe_ai_results()        │                                 │
│   └─ Zhulong_stream_unsubscribe_ai_results()      ▼                                 │
└───────────────────────────────────────────────────┼─────────────────────────────────┘
                                                    │ (同步 CGO 回调 + 借用指针深拷贝)
                                                    ▼
┌───────────────────────────────────── Go 宿主 ───────────────────────────────────────┐
│ internal/engine                                                                     │
│   └─ AIResultSubscription (环形背压缓冲 + 丢最旧策略 + cgo.Handle)                   │
│                                                   │                                 │
│ internal/camera / internal/algorithm              ▼                                 │
│   └─ ResultConsumer: 解析 JSON ➔ algorithm.InferenceResult                           │
│        (消除 NewPersonStructureResult 等构造函数零调用状态)                          │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

---

## 2. 关键技术决策 (Trade-offs & Decisions)

### 2.1 通道归属：复用 FrameBroker UDS（遵守父设计 D2）
- **选择**：Runner 直接通过向 FrameBroker 握手的既有 UDS 连接发送 `MSG_RESULT`。
- **理由**：
  - Runner 进程已与 FrameBroker 建立全双工流式 Unix Socket；
  - 避免 Go 侧再次创建 UDS 服务端、避免重复管理 socket 路径与双重重连/超时状态机；
  - 保证帧 ACK 与结果上报在同一时序维度处理。

### 2.2 载荷格式：自描述 JSON 格式
- **选择**：`MSG_RESULT` 载荷采用自描述 JSON 字符串。
- **评估与权衡**：
  - **吞吐与开销**：在开启「空帧静默过滤」后，仅有目标帧（通常 5~15 FPS，1~5 个人物）产生上报，每秒序列化开销 < 0.1% CPU，吞吐带宽仅数 KB/s；
  - **结构演进与鲁棒性**：结构化结果（检测框、1:N 嵌套人脸、Top-K 候选人、关键点）是多层变长树形数据。二进制打解包极易受编译器对齐、填充、字节序影响；JSON 与 Go 侧 `InferenceResult` 原生标签 100% 对齐，具备天然版本容错性；
  - Runner 内部自带高效紧凑的字符串拼接与 `runner::json_quote`，零第三方重型依赖。

### 2.3 状态机过滤：空帧静默 + 单帧退出信标
- **规则**：
  - `last_had_targets == false` 且当前帧检出目标数为 0：**完全静默**（0 IPC、0 CGO、0 宿主开销）；
  - `last_had_targets == true` 且当前帧检出目标数为 0：**发送 1 帧空结果（退出信标）**，并置 `last_had_targets = false`（通知 Go 与前端清空跟踪框，防 250ms 内幽灵框残留）；
  - 当前帧检出目标数 > 0：**立即发送当前帧结果**，置 `last_had_targets = true`。

### 2.4 内存所有权与生命周期
- **C++ 侧**：
  - 序列化必须在 `api.process()` 之后、`api.result_release()` **之前**执行；
  - 严禁持有 `result` 指针跨越到异步队列；序列化产生局部 `std::string`，通过 socket 发送后立即释放 `result`；
  - AddressSanitizer 强制断言：无 Use-After-Free，无 Double-Free。
- **CGO 边界**：
  - 回调函数签名传递只读借用指针：`const char* json_payload, size_t payload_len`；
  - Go 侧在 Native 同步回调期间通过 `C.GoBytes`（或 `unsafe.Slice` + 深拷贝）转为 Go 管理的 `[]byte`；
  - 回调返回后 Native 指针立即失效；Go 严禁向 Native 传递 Go 指针，token 统一使用 `cgo.Handle` 整数。

### 2.5 背压与慢消费丢弃策略
- **策略**：**丢最旧（Drop Oldest）**。
- **实现**：Go 侧 `AIResultSubscription` 维护有界环形切片（容量默认 32）。
- 当队列满且慢消费者未及时取走时：
  1. 丢弃最老一帧；
  2. 计入 `droppedResults.Add(1)` 计数器；
  3. 将新帧推入队列；
  4. 绝不阻塞 Native 回调线程。

---

## 3. 跨层契约扩展

### 3.1 IPC 协议扩展 (`native/src/algorithm/ipc/ipc_protocol.hpp`)
```cpp
enum MessageType : uint16_t {
    MSG_UNKNOWN         = 0,
    MSG_HANDSHAKE_REQ   = 1,
    MSG_HANDSHAKE_RESP  = 2,
    MSG_FRAME_DISPATCH  = 3,
    MSG_FRAME_ACK       = 4,
    MSG_HEARTBEAT       = 5,
    MSG_DISCONNECT      = 6,
    MSG_RESULT          = 7,  // 算法推理结果上报 (Runner -> Broker)
    // 10..12 为 Gallery 预留
};
```

### 3.2 Native C ABI (`native/include/Zhulong/engine.h`)
```c
typedef void (*Zhulong_ai_result_callback)(
    uintptr_t token,
    Zhulong_stream_id stream,
    const char *json_payload,
    size_t payload_len);

Zhulong_status_t Zhulong_stream_subscribe_ai_results(
    Zhulong_engine_h engine,
    Zhulong_stream_id stream,
    Zhulong_ai_result_callback callback,
    uintptr_t token,
    Zhulong_subscription_id *out_sub_id);

Zhulong_status_t Zhulong_stream_unsubscribe_ai_results(
    Zhulong_engine_h engine,
    Zhulong_subscription_id sub_id);
```

### 3.3 Go 宿主层接线 (`internal/engine` & `internal/algorithm`)
- `internal/engine`:
  - 增加 `SubscribeAIResults(streamID uint64) (*AIResultSubscription, error)`
- `internal/algorithm`:
  - 增加 `ResultConsumer` 或将结果反序列化为 `algorithm.InferenceResult`；
  - 消除 `NewDetectionResult`、`NewClassificationResult`、`NewPersonStructureResult` 的零调用方状态。
