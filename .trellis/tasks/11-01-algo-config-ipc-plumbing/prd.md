# PRD: 算法配置下发链路与识别参数生效

## 1. 目标与用户价值 (Goal)

管理界面里能填的算法参数（`threshold` / `top_k`）**必须真正生效**，而不是存进数据库后石沉大海；同时消除两处会让运维把配置问题误判成算法问题的**静默失败**。

三条用户可观察的价值：

1. **配置可信**：UI 里改阈值 → 该路流的比对决策立刻改变；改了没效果时能明确区分「值没送到」与「送到但没命中」。
2. **不静默**：参数或分辨率非法时立刻报错，而不是「检测结果恒为 0 / 每次推理刷 24 行告警」。
3. **可排查**：进程级参数能从 `ps` / 日志复原（argv 可见），不靠猜编译期常量。

---

## 2. 背景与已确认事实 (Background & Confirmed Facts)

### 2.1 拓扑约束（本任务的设计前提）

一个算法包进程 = 一个 `ModelGroup`（`init` 只调一次）+ N 个 worker/task，**一路 broker 一个**（`--broker` 可重复，`task_id` 由 socket 路径反解 stream_id）。因此配置天然分两层，通道必须不同：

| 层 | 粒度 | 正确通道 | 理由 |
| :--- | :--- | :--- | :--- |
| 进程级（模型路径、检测分辨率、NMS/score 阈值） | 一包一份 | argv `--config <json>` | 必须首帧前就位；`init` 只有一次 |
| 任务级（比对 `threshold` / `top_k`） | 一路流一份 | IPC `MSG_TASK_CONFIG` → `TaskContext.params` | argv 无法表达 per-stream 值 |

### 2.2 现状：数据链路完好，进程边界断开

| 环节 | 位置 | 状态 |
| :--- | :--- | :--- |
| 表单由包 `config.schema.json` 生成 | `web/src/features/tasks/data/algo-config-schema.ts` | ✅ |
| 服务端按同一 schema 校验 | `internal/task/service.go:194` `validateTaskConfig` | ✅ |
| 落库 | `tasks.config_json` / `task_algorithms.config_json`（`internal/task/store.go:276`） | ✅ |
| 回显 | `internal/task/responses.go:38` | ✅ |
| **spawn 时下发** | `ProcessConfig`（`internal/algorunner/process.go:103`）无 config 字段 | ❌ |
| **骨架传入 init** | `algo_main.cpp:645`、`:814` 均为 `api.model_create("{}", &model)` 硬编码 | ❌ |
| **任务级参数** | `TaskConfig{}` 空类型（`algo.hpp:99`）；`drain_control_messages` 空实现（`algo_main.cpp:771`） | ❌ |
| 全仓读取方 | 除 `internal/task` 自身外无任何代码读 `ConfigJSON` | ❌ |

结果：`FaceRecognitionPlugin::parse_config("{}")` 什么都取不到，`Matcher` 永远停在编译期 `0.40 / 3`（`matching/matcher.hpp`）。

### 2.3 为什么这一点特别容易误判

同一张子表里的 `region_config_json` 是**活的**（`internal/task/region_config.go:84` → `region.ParseConfig` → Go 侧过滤）。现场表现是「区域改了立刻生效、阈值改了毫无反应」，极像算法阈值没调好。

### 2.4 已铺好的管道（本任务复用，不新建）

```
Go Engine.BroadcastAIMessage(ctx, streamID, msgType, payload)      internal/engine/engine.go:630
 → Zhulong_engine_broadcast_ai_message(...)                         native/include/Zhulong/engine.h:652
 → FrameBroker::broadcast_message(msg_type, payload, size)          native/src/algorithm/ipc/frame_broker.cpp:597
 → UnixSocketServer::broadcast                                      native/src/algorithm/ipc/unix_socket.cpp:493
 → 包进程 socket（现由 MSG_GALLERY_SYNC=10…ACK=13 使用，Go 常量见 internal/algoipc/protocol.go:59）
```

骨架收包循环对**非 `MSG_FRAME_DISPATCH` 的消息静默丢弃**（`algo_main.cpp:1041` 只判该类型），因此「新宿主先推、旧包忽略」是安全的增量上线路径。

### 2.5 静默失败（与配置无关但同属「配置/参数不可信」）

| 缺陷 | 证据 | 后果 |
| :--- | :--- | :--- |
| `set_detection_input_size` 无整除校验 | `detector.hpp` 只存 w/h；`decode_scrfd_candidates` 布局不符即 `return {}`；`Session::forward` 失败也返回空（`algo.hpp:257`） | `1280×720` 这类值 → 人脸与人体检测**恒为 0**，无错误码 |
| rk3568 静态 shape | `.rknn` 元数据 `static_shape [1,3,640,640]`、`origin_dynamic: False`；`RknnInferenceBackend::forward` 校验 `element_count()` 后返回 `INVALID_ARGUMENT` | 对 rk3568 调该 API 同样静默归零 |
| ONNX 输出静态维度 | `det_500m.onnx` 输出声明 `[12800,1]`…，实际 640×384 得 7680/1920/480 | ORT `VerifyOutputSizes` 每帧 9+15=24 行 stderr；25fps 多路即日志洪泛；`onnx_inference_backend.cpp:63` 未设日志级别 |
| 候选上限边扫边截断 | `decode_scrfd_candidates` 先扫 stride 8，满 128 即 break | 拥挤场景细层挤掉粗层高分框，NMS 在截断后执行 |

---

## 3. 需求 (Requirements)

### R1 任务级配置通道（`MSG_TASK_CONFIG`）

- **R1.1 协议**：`native/src/algorithm/ipc/ipc_protocol.hpp` 新增 `MSG_TASK_CONFIG = 14`，负载为「定长头 + 尾随 UTF-8 JSON」：
  ```cpp
  struct TaskConfigMsg {
      char     algorithm_id[64]{0};  // 必需：广播会同时到达该流所有算法包
      uint32_t config_json_len{0};   // 紧随其后的 JSON 字节数（总量 ≤ MAX_IPC_PAYLOAD_SIZE=64KB）
  };
  ```
  `internal/algoipc/protocol.go` 同步导出 `MsgTaskConfig uint16 = 14`（两侧常量必须一致，沿用现有跨语言比对测试的写法）。
- **R1.2 宿主推送与重放**：
  - 订阅建立后推一次；任务配置保存后对受影响 stream 再推一次。
  - **broker 侧缓存每路流最近一次的该报文，新客户端握手完成后重放**（底库有全量 sync 兜底，task config 没有，只能靠重放）。
  - 只推给受影响 stream（`BroadcastAIMessage(streamID, ...)`），不用 `streamID=0` 全广播。
- **R1.3 骨架接收**：收包循环接住 14 号消息 → 校验 `algorithm_id == options.algorithm_id`（不匹配则丢弃）→ 存入该路 broker 的 pending config → `drain_control_messages(TaskContext&)` 写进 `task_context.params`。每帧调用点已存在（`algo_main.cpp:1114`，在 `process` 之前）。
- **R1.4 SDK 契约**：`TaskConfig` 增加字段（如 `float face_match_threshold; uint32_t face_match_top_k; uint32_t flags;`），`flags` 位标记「哪几项是宿主给的值」。**缺省/越界一律回落编译期默认**（复用 `parse_config` 现有回落语义）。`native/sdk/abi.json` 的 `sdk_version` 需 bump（该文件注释明写：改它等于要求已发布包重建）。
- **R1.5 算法侧生效**：`Matcher` 去状态化——`match(probe, dim, gallery, threshold, top_k)`；`detect_persons` 从 `task_context.params` 取值，缺省回落默认。消除插件级共享可变状态（多路流并发下会互相踩）。

### R2 进程级配置（`--config`）

- **R2.1**：`sdk::Options` 增加 `std::string config_json{"{}"}`，骨架解析 `--config <json>`（当前 argv 无冲突，`algo_main.cpp:1319-1354` 的现有参数表已确认）。
- **R2.2**：`algo_main.cpp:645` 与 `:814` 的硬编码 `"{}"` 改为 `options.config_json`，即 `Plugin::init` 真正收到宿主下发的进程级配置。
- **R2.3 值与来源**：本期**不引入算法级配置的数据库存储**。值由宿主通过 `ProcessConfig.ExtraEnv`/argv 注入（最小可用：`ZHULONG_ALGO_CONFIG` env 或 supervisor 里的静态默认）。设计口径见 `design.md` §3。**注**：`sdk::Options` 的注释明确反对 `--config-file` 旁路通道（argv 必须能复原「跑的是哪套配置」），因此只支持 inline JSON。

### R3 消除静默失败

- **R3.1 分辨率 fail-fast**：`set_input_size` 改为返回 `bool`/状态码，校验 `w % 32 == 0 && h % 32 == 0`（人脸）与 `% 128 == 0`（人体）；`FaceRecognitionPlugin::set_detection_input_size` 汇总失败。rk3568 档位额外断言模型静态 shape（640×640），或直接不暴露该 API 直到模型重转。
- **R3.2 ONNX 日志**：`native/src/backends/onnx/onnx_inference_backend.cpp` 的 `Ort::SessionOptions` 设 `SetSessionLogSeverityLevel(ORT_LOGGING_LEVEL_ERROR)`。**二选一也可行**：重导出带动态输出维度的模型（属资产变更，不在本期）。
- **R3.3 候选截断**：先按分数维护候选（或按层配额）再截断，NMS 在完整候选集上执行。

### R4 中间态诚实性

- **R4.1**：若 R1 分阶段发布（宿主先上、包后上），发布说明中必须写清「该版本前阈值仍走默认值」，避免运维以为已生效。
- **R4.2**：R1 完成后，同一份 `config.schema.json` 生成的表单字段必须与包内实际消费的键**逐一对齐**（现状 schema 只有 `threshold`/`top_k`，与 `parse_config` 一致，需在实现时保留该一致性检查）。

---

## 4. 验收标准 (Acceptance Criteria)

- **AC1 任务级生效（黑盒）**：同一包进程订阅两路流，分别下发 `{"threshold":0.95}` 与 `{"threshold":0.10}`；同一张底库照片的探针在 A 路 `matched=false`、B 路 `matched=true`。
- **AC2 重连/晚启动重放**：杀掉包进程任其被 supervisor 拉起（或让客户端后连），首帧之后的决策仍使用上次下发的阈值（无「重启回落到 0.40」现象）。
- **AC3 跨算法隔离**：同流挂人脸与车牌两个包，下发 `algorithm_id=face_recognition` 的配置；车牌包日志/行为无变化（断言未解析、未应用）。
- **AC4 向后兼容**：旧宿主（不推 14 号消息）配新包 → 全部使用默认值，无错误、无崩溃；新宿主配旧包 → 旧包忽略未知消息，行为不变（`algo_main.cpp:1041` 的丢弃语义）。
- **AC5 进程级配置抵达 init**：`--config '{"det_score":0.6}'` 后 `Plugin::init` 收到的字符串与该 argv 一致（单测可达；`devtest --config` 已覆盖包侧解析路径）。
- **AC6 分辨率 fail-fast**：`set_detection_input_size(1280, 720)` 返回失败并被记录；不再出现「成功但检测恒 0」。
- **AC7 告警消除**：单帧推理的 stderr 中不再出现 `VerifyOutputSizes` 告警（`make test` 输出断言）。
- **AC8 质量门禁**：`make check` 全绿；`python3 native/scripts/check_package_drift.py packages/face_recognition` 通过；`make board-check` 结果不退化（计数与本地一致或明确记录差异）。

---

## 5. 不在本期范围 (Out of Scope)

- **阈值标定（ROC）与按底库规模缩放阈值**：需要真实底库与测试集，先有可配置通道才有意义。
- **精度质量门**：最小人脸尺寸、对齐残差门、候选相似度 floor。
- **性能项**：ONNX 档位 intra-op 线程数（当前恒为 1）、检测输入缓冲复用（每帧 ~14.7MB 流量）、`crop_and_align_direct` 格式分发外提。
- **算法级配置的 DB 存储与 UI 编辑**（R2.3 只做通道）。
- **检测解码纯函数单测**（`scrfd_outputs_match` / `decode_scrfd_candidates` / NMS）。
- **模型资产重导出**（R3.2 走日志级别方案）。

---

## 6. 风险与回滚 (Risks)

- **SDK 版本 bump 要求全部已发布包重编**（`native/sdk/abi.json` 注释明写）。回滚点 = abi.json + `algo.hpp` + 骨架 + 包四者同一提交；不接受「只改包不改骨架」的中间态。
- **广播语义**：`broadcast_message` 投给该流**所有**客户端，`algorithm_id` 过滤是唯一防线，必须有 AC3 的测试兜住。
- **重放缺失即配置漂移**：漏做 R1.2 的重放，故障形态是「跑了几天突然回到默认阈值」，最难查。
- **`--config` 泄密面**：argv 对同机用户可见，不得把密钥类内容（如摄像头凭据）放进去。

---

## 7. 范围确认 (Scope Confirmation)

- **已确认（2026-11-01）**：本期只做「配置链路（R1+R2）+ 静默失败（R3）」。
  - 性能项（ONNX intra-op 线程、检测输入缓冲复用、`crop_and_align_direct` 格式分发外提）→ 后续任务，需先有分阶段基准。
  - 算法级配置的 DB 存储与 UI → 后续任务；本期 R2.3 只做通道，值由宿主 `--config` / env 注入。
- 本任务**无剩余阻塞项**。
