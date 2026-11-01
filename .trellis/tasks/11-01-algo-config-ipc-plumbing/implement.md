# 实施计划：算法配置下发链路与识别参数生效

> 依据 `prd.md` / `design.md`。M1–M4 按依赖排序，M1 完成前不要动 M3（两者会同时改包内阈值路径，容易冲撞）。

## M1 协议与 SDK 契约（跨层地基）

- [x] `native/src/algorithm/ipc/ipc_protocol.hpp`：新增 `MSG_TASK_CONFIG = 14` 与 `TaskConfigMsg`（定长头 + 尾随 JSON）；**不改** `IPC_VERSION`。
- [x] `internal/algoipc/protocol.go`：`MsgTaskConfig uint16 = 14`，并补进既有跨语言常量比对测试。
- [x] `native/sdk/include/zhulong/algo.hpp`：`TaskConfig` 加 `face_match_threshold` / `face_match_top_k` / `flags`；`Options` 加 `config_json{"{}"}`。
- [x] `native/sdk/abi.json`：`sdk_version` `1.0.0` → `1.1.0`（`api_version`、`protocol` 不动）。
- [x] 校验：`make sdk-install` 后 `build/sdk/prefix/VERSION` 显示 1.1.0。

## M2 进程级 `--config`（不依赖 M1 的 IPC 部分）

- [x] `native/sdk/src/algo_main.cpp` `parse_options`：增 `--config <json>`（未知参数仍 fail closed）。
- [x] `algo_main.cpp:645`、`:814`：`api.model_create("{}", ...)` → `api.model_create(options.config_json.c_str(), ...)`。
- [x] `internal/algorunner/supervisor.go:194 processConfig`：把进程级配置拼成 `--config`（值来源：`ExtraEnv` 的 `ZHULONG_ALGO_CONFIG`，缺省 `{}`）。
- [ ] `packages/face_recognition/onnx|rk3568/src/pipeline/face_recognition.cpp`：`parse_config` 增加进程级字段消费（如需），并保留越界回落。
- [ ] 校验：`make test`（包内 devtest 的 `--config` 走同一 `init` 路径）；新增 Go 单测断言 argv 里出现 `--config`。

## M3 任务级 IPC（R1 主体）

- [x] `FrameBroker`：按流缓存最近一次 `MSG_TASK_CONFIG` 报文；握手成功后重放（`native/src/algorithm/ipc/frame_broker.{hpp,cpp}`）。
- [x] `algo_main.cpp` 收包循环：识别 14 号消息 → 校验 `algorithm_id` → 存 per-broker pending；解析失败只 warn 不退出。
- [x] `drain_control_messages(TaskContext&)`：pending → `task_context.params`（调用点已在 `algo_main.cpp:1114`）。
- [x] Go 推送：订阅建立后 + task 保存后，per-stream `BroadcastAIMessage(streamID, MsgTaskConfig, payload)`（**不要** `streamID=0`）。
- [x] 包侧去状态化：`Matcher::match(..., threshold, top_k)`；删除 `threshold_/top_k_` 与 `set_*`；`detect_persons` 从 `task_context.params` 取值并做范围回落。
- [ ] 测试：
  - 纯逻辑：`TaskConfigMsg` 编解码、`algorithm_id` 过滤、`flags`→回落语义（不需模型）。
  - broker 集成：真 socket 推 14 号消息 → 断言行为变化；模拟客户端重连 → 断言配置被重放。
  - 黑盒：`AC1` 双流不同阈值；`AC3` 同流双算法包隔离。
- [x] `packages/face_recognition/onnx|rk3568/tests/dev_infer.cpp`：加 `--task-threshold` 覆盖 `TaskContext` 路径（现只覆盖 `init`）。

## M4 静默失败消除

- [x] `detection/detector.hpp`：`set_input_size` → `bool`，校验 `32`（人脸）/`128`（人体）整除；`FaceRecognitionPlugin::set_detection_input_size` 聚合返回。
- [x] rk3568 档位：断言静态 shape 640×640，或移除该 API（模型未重转前）。
- [ ] `scrfd_outputs_match` 失败加一次性可观测日志。
- [ ] `native/src/backends/onnx/onnx_inference_backend.cpp:63`：`SetSessionLogSeverityLevel(ORT_LOGGING_LEVEL_ERROR)`。
- [x] `decode_scrfd_candidates`：先全量收集（阈值过滤）→ 按分数取 top-N → NMS。
- [x] spec 补充：`.trellis/spec/native/algo-plugin-contract.md` 记录「两档位默认检测分辨率分叉」与「drift 白名单看不到语义差异」。

## 验证命令

```bash
# Go（避免裸 go test 进入 internal/engine 依赖链）
python3 native/scripts/build.py go test -race ./internal/algorunner ./internal/algorithm ./internal/task ./internal/algoipc
# native（含骨架 / broker / 后端）
make native-build && make native-test
# 算法包（两个档位都要，模型在包内）
cd packages/face_recognition/onnx && make unit-test && make test SDK_PREFIX=../../build/sdk/prefix
# 前端（如果动了 config 表单文案）
cd web && pnpm test -- src/features/tasks
# 收口
make check
python3 native/scripts/check_package_drift.py packages/face_recognition
make pkg-drift-check
```

板端（需真实硬件，不进 `make check`）：

```bash
make board-check            # default: packages/face_recognition/rk3568 + rk3568
```

## 风险文件与回滚点

| 文件 | 风险 | 回滚 |
| :--- | :--- | :--- |
| `native/sdk/abi.json` + `algo.hpp` | SDK bump 要求已发布包重编；忘 bump 会导致新旧包混用 | 与骨架/包同提交，整体回滚 |
| `native/src/algorithm/ipc/frame_broker.cpp` | 握手后重放路径影响所有客户端（含底库消息） | 重放逻辑独立分支，可单独 revert |
| `internal/algorunner/supervisor.go` | argv 拼接影响所有包启动 | `--config` 缺省 `{}`，等于当前行为 |
| `packages/*/src/matching/matcher.*` | 去状态化改动面广（有 `aligner_matcher_test.cpp` 覆盖） | 保留旧 `set_threshold` 兼容层一版 |
| `detection/detector.hpp` 签名变更 | 两档位 + `native/tests` 都有调用点 | 编译期即可发现，无运行期风险 |

## 待 `task.py start` 前确认

- PRD §7 Q1：本期范围边界（性能项与算法级配置存储是否排除）。

## 实施记录（2026-11-01）

已完成 M1–M4 的代码与单元/集成测试。落地时的三处**与计划的偏差**，都有明确理由：

1. **`TaskConfig` 改成携带配置原文（`std::string_view json`），而不是解析后的业务字段。**
   骨架如果解析字段，每加一个算法就要改一次 SDK；`--config` 的进程级通道同理。现在骨架
   只做「搬运 + 按 `algorithm_id` 过滤」，解析责任回到算法（`common/match_config.hpp`）。
2. **「布局不符时打一条一次性日志」被取消。** 范本查证后发现：包进程写 stderr 的非 JSON 行
   会被宿主 `runner_log.go` 归类为 `runner output ignored` 并丢弃（还会计入噪音统计），所以
   从包里 print 等于没打。改为把校验前移到 `set_input_size` 的 fail-fast，并以纯函数单测覆盖。
3. **进程级配置值来源用 `ZHULONG_ALGO_CONFIG`（host env）+ argv `--config`。**
   `Supervisor::processConfigJSON` 校验必须是 JSON 对象，非法值丢弃并告警，不把垃圾塞进 argv。

新增测试：`internal/algoipc`（载荷布局 + 与 C++ `sizeof(TaskConfigMsg)` 的跨语言比对）、
`internal/algorunner`（逐流路由 / 非法 JSON 跳过 / 错误聚合 / spawn 前灌缓存）、
`ZhulongFrameBroker::test_frame_broker_task_config_cache_replay`（无客户端时缓存 + 握手重放 + 只重放最新）、
包内 `test_resolve_match_params`（任务级覆盖、越界回落、相似键名、边界值）。

已知未覆盖：`AC1`（同一进程两路流不同阈值 → 决策相反）需要真实底库与两路流，本机跑不了；
留下的证据是「逐流路由 + 重放 + 覆盖语义」三段单测，加一次 `board-check` 实机确认。

附带发现（未修复，已记录）：ONNX Runtime 启动时会向 stderr 打 ~567 行
`Schema error: ... already registered`（ONNX schema 被注册两次，环境级日志级别改不动它）。
一进程一次、不随帧增长，属上游噪音；要彻底消掉只能调整链接/重导出模型，另开任务。
