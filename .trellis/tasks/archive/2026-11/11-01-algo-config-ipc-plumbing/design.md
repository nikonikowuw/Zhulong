# 设计：算法配置下发链路与识别参数生效

> 对应 `prd.md` 的 R1–R4。所有 file:line 锚点在编写时已核实。

## 1. 分层与边界

```
┌──────────────────────────────────────────────────────────────────────┐
│ Go 宿主                                                              │
│  internal/task        task_algorithms.config_json（已校验、已落库）   │
│  internal/algorithm   §2 订阅/重连时机                               │
│  internal/algorunner  Supervisor（spawn argv：进程级 --config）        │
│  internal/engine      BroadcastAIMessage(streamID, msgType, payload)  │
└───────────────┬──────────────────────────────┬───────────────────────┘
                │ argv（一包一份）              │ IPC（一路流一份）
                ▼                              ▼
┌───────────────────────────────┐  ┌────────────────────────────────────┐
│ 包进程骨架 algo_main.cpp       │  │ FrameBroker → 包 socket            │
│  Options.config_json           │  │ MSG_TASK_CONFIG=14                 │
│   → model_create(config_json)  │  │  → pending → drain_control_messages│
│     → Plugin::init（现有）      │  │  → TaskContext.params              │
└───────────────┬───────────────┘  └────────────────┬───────────────────┘
                ▼                                   ▼
        FaceRecognitionPlugin::init          detect_persons(task_context)
        （进程级：模型/分辨率/NMS）           （任务级：threshold/top_k）
```

**边界铁律**：进程级参数绝不走 IPC（首帧前就要就位，且 `init` 只调一次）；任务级参数绝不走 argv（argv 无法表达 per-stream 值）。

## 2. 协议设计（R1.1）

### 2.1 消息体

`native/src/algorithm/ipc/ipc_protocol.hpp`：

```cpp
enum MessageType : uint16_t {
    ...
    MSG_GALLERY_ACK     = 13,
    MSG_TASK_CONFIG     = 14  /**< 任务级配置下发（Broker -> Client）：每路流一份 */
};

struct TaskConfigMsg {
    char     algorithm_id[64]{0};   // 与骨架 --algorithm-id 比对；不匹配即丢弃
    uint32_t config_json_len{0};    // 后续 JSON 字节数
    // uint8_t config_json[];       // 变长尾随数据，总长 ≤ MAX_IPC_PAYLOAD_SIZE (64KB)
};
```

- 变长负载合法：`IpcHeader.payload_size` 已支持，`MAX_IPC_PAYLOAD_SIZE = 64 * 1024`（`ipc_protocol.hpp:34`），JSON 远小于此。
- **不引入新协议版本**：新增 `msg_type` 是纯增量；旧骨架对未知类型静默丢弃（`algo_main.cpp:1041` 只认 `MSG_FRAME_DISPATCH`），所以 `IPC_VERSION` 保持 1。
- Go 侧 `internal/algoipc/protocol.go` 增 `MsgTaskConfig uint16 = 14`，并沿用既有的跨语言常量比对测试（现有 `MsgGallery*` 就是这样锁的）。

### 2.2 谁发、发给谁

- **发送**：`internal/algorunner`（或 `internal/algorithm` 的 task 保存回调）→ `BroadcastAIMessage(ctx, streamID, algoipc.MsgTaskConfig, payload)`。
  - **不要**用 `streamID=0` 全广播（底库用的是 0，因为底库是全局的；task config 是 per-stream 的）。
  - streamID 由 broker socket 路径反解（与骨架 `parse_stream_id_from_socket` 同一规则）。
- **接收与过滤**：一个进程订阅多路 broker，每路 socket 各自独立收报文；同一个流上可能挂着多个算法包（人脸 + 车牌），广播必然同时到达 → **只有 `algorithm_id` 匹配才应用**。这是唯一防线，必须测（AC3）。

### 2.3 时序与重放（R1.2）

| 时机 | 动作 |
| :--- | :--- |
| 包进程连上 broker（handshake 成功） | broker **重放**该流最近一次的 `MSG_TASK_CONFIG` |
| 任务配置保存 / 启用切换 | Go 对受影响 stream 推一次 |
| 每帧 `process` 之前 | 骨架 `drain_control_messages` 把 pending 写进 `TaskContext`（已有调用点 `algo_main.cpp:1114`） |

重放由 **broker 缓存最近报文**实现（`FrameBroker` 内按 stream 存一份 `(msg_type, payload)` 槽位，握手完成后发送）。不重放的后果是「跑几天后重启回到默认阈值」，属于最难查的漂移故障。

### 2.4 骨架实现要点（R1.3）

- 收包循环里把 14 号消息解析成 `pending_task_config[idx]`（per-broker，单线程消费，无需锁）。
- `drain_control_messages(TaskContext&)` 签名已存在（`algo_main.cpp:771`），实现为「若有 pending 就写 `task_context.params` 并清除 pending」。
- 解析失败（长度不符/`algorithm_id` 不匹配/JSON 超 64KB）→ 丢弃并记一条 warn，**不退出进程**：配置坏了不该让 Worker 掉线（与帧级错误同等处理语义）。

### 2.5 SDK 契约（R1.4）

```cpp
// build/sdk/prefix/include/zhulong/algo.hpp
struct TaskConfig {
    float    face_match_threshold{0.0f};   // 0 = 未下发，用编译期默认
    uint32_t face_match_top_k{0};          // 0 = 未下发
    uint32_t flags{0};                     // bit0 = threshold 有效, bit1 = top_k 有效
};
```

- 用 `flags` 而不是哨兵值：`threshold=0.0` 是**合法**取值（schema `minimum: 0.0`），不能用 0 当「未设置」。
- 加字段 = `native/sdk/abi.json` 的 `sdk_version` bump（现 `1.0.0` → `1.1.0`），已发布包须重编（该文件注释明写）。`api_version`（`zhulong_plugin_api_v1` 结构版本）**不动**。
- 包侧消费语义：`params.flags & bit → 用值（并做范围校验，越界回落默认）；否则用编译期默认`。

### 2.6 算法侧（R1.5）

```cpp
// matching/matcher.hpp：去状态化，配置从调用方传入
[[nodiscard]] MatchResult match(const float* probe, size_t dim,
                                const std::vector<FaceTemplate>& gallery,
                                float threshold, size_t top_k) const;
```

- 删掉 `Matcher::threshold_/top_k_` 与 `set_*`，`matcher_` 变成无状态成员（多路 worker 并发安全）。
- `detect_persons` 从 `task_context.params` 取 `threshold/top_k`，缺省回落 `kDefaultMatchThreshold/kDefaultMatchTopK`（与 `config.schema.json` 默认值保持同步，现有一致性注释保留）。
- `parse_config` 保留：它负责**进程级**默认值（来自 `--config`），任务级覆盖在调用时叠加。优先级：`TaskContext.params` > `init(config_json)` > 编译期常量。

## 3. 进程级 `--config`（R2）

- `sdk::Options` 加 `std::string config_json{"{}"}`；`parse_options` 增 `--config` 分支（现无冲突，`algo_main.cpp:1319-1354`）。
- `algo_main.cpp:645`（verify 路径）与 `:814`（IPC 路径）的 `api.model_create("{}", &model)` → `api.model_create(options.config_json.c_str(), &model)`。
- **值来源**：本期不建算法级配置存储。宿主可用两条路提供：
  1. `ProcessConfig.ExtraEnv` 里的 `ZHULONG_ALGO_CONFIG`（沿用 `ZHULONG_WORKER_STALL_TIMEOUT_MS` 的既有先例）；
  2. `Supervisor::processConfig` 直接拼 argv `--config`（`internal/algorunner/supervisor.go:194`）。
  推荐 (2)：argv 在 `ps`/日志中可复原，符合 `sdk::Options` 「运行参数必须全部来自 argv」的既定立场。
- **禁止**：把凭据类内容放进 argv（`/proc/<pid>/cmdline` 同机可读）。

## 4. 静默失败消除（R3）

### 4.1 分辨率 fail-fast

```cpp
// detection/detector.hpp
inline constexpr int kFaceStrideGranularity  = 32;   // strides 8/16/32
inline constexpr int kPersonStrideGranularity = 128; // strides 8/16/32/64/128
bool set_input_size(int w, int h) noexcept;          // 由 void 改为 bool
```

- 校验 `w > 0 && h > 0 && w % g == 0 && h % g == 0`。
- `FaceRecognitionPlugin::set_detection_input_size` 返回失败聚合（任一失败即失败）。
- rk3568 档位：`.rknn` 为 `static_shape [1,3,640,640]`（`origin_dynamic: False`），`RknnInferenceBackend::forward` 会用 `element_count()` 拒绝不匹配输入。因此 rk3568 档位要么把 API 断言成 640×640，要么在模型重转前不暴露该 API。**两档位的默认分辨率分叉（onnx 640×384 / rk3568 640×640）必须写进 spec**：`pipeline/face_recognition.cpp` 属于 drift 白名单文件，漂移检查看不到这处语义差异。
- 另外在 `decode_scrfd_candidates` 的 `scrfd_outputs_match` 失败分支加一条可观测日志（一次性），避免「布局不符 → 空结果」再次完全无声。

### 4.2 ONNX 日志

`native/src/backends/onnx/onnx_inference_backend.cpp:63` 处：

```cpp
Ort::SessionOptions opts;
opts.SetIntraOpNumThreads(1);
opts.SetInterOpNumThreads(1);
opts.SetSessionLogSeverityLevel(ORT_LOGGING_LEVEL_ERROR);   // 新增：抑制 VerifyOutputSizes 每帧告警
```

理由：模型把空间维写死成静态 `[12800,1]` 等，而插件按 640×384 推理是**设计内行为**（动态输入轴已验证），告警无信息量且 24 行/帧会刷爆 stderr。根治需要重导出带动态输出维度的模型——列入后续资产任务，不进本期。

### 4.3 候选截断

`decode_scrfd_candidates` 改为：扫描时用 `score_threshold` 先过滤，收集全部候选（上界 = 网格总数，人体模型 640×384 时约 5115 个，仍可控）后再按分数取 top-`max_candidates`，最后做 NMS。若要控内存，可用定长小顶堆（`max_candidates`=128 时堆开销可忽略）。

## 5. 兼容矩阵

| 宿主 | 包骨架 | 结果 |
| :--- | :--- | :--- |
| 旧（不推 14） | 新（认 14 + 默认回落） | 默认值生效，无错误 ✅ |
| 新（推 14） | 旧（丢弃未知类型） | 默认值生效，无错误 ✅ |
| 新 | 新 | 任务级配置生效 ✅ |
| 新骨架 + 旧包二进制 | — | 不可能同进程出现（包 = 可执行文件，骨架静态链接进包） |

## 6. 回滚

- 单一回滚点：`abi.json`（sdk_version）+ `algo.hpp`（TaskConfig）+ `algo_main.cpp`（`--config`、drain）+ `ipc_protocol.hpp`（14）+ Go 推送 + 包侧消费，必须在同一提交序列内。任何「只升包不升骨架」或反过来的中间态都不接受。
- R3.1/R3.3 独立于 R1/R2，可单独回滚（不涉及 ABI）。
- 若上线后发现配置错误拉崩现场：`--config` 与 14 号消息都是「缺省即默认」，把推送开关关掉即回到当前行为，不需要回滚二进制。
