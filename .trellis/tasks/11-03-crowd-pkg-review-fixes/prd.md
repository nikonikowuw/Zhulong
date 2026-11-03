# 修复 crowd_gathering 算法包评审问题

## 背景

`packages/crowd_gathering/` 是新增的人员非法聚集检测算法包（onnx 档位）。对照
`.trellis/spec/native/algo-plugin-contract.md` 与现有 `face_recognition` / `plate_recognition`
档位做了逐文件评审，发现两类阻断问题、两类正确性问题与一批一致性/性能问题。本任务一次性收口。

## 阻断项（必须先修）

- **R1 单测被 `NDEBUG` 编译掉**：`tests/dev_infer.cpp` 用 `assert()` 作唯一判据，而
  `Makefile` 以 `RelWithDebInfo` 构建（`-O2 -g -DNDEBUG`）。实测
  `nm build/crowd_gathering_devtest | grep -c __assert_fail == 0`，`[✓] internal unit tests passed!`
  无条件打印 —— 三段单测一行都没跑。spec §5.5 明令禁止该写法。
- **R2 运行期模型路径解析缺可执行文件目录**：`resolve_model_path` 只看 CWD/env/仓库相对路径，
  而宿主运行期启动器不设 `cmd.Dir`（`internal/algorunner/process.go`），只有验包与 trial 设了
  `cmd.Dir = 包目录`。结果是 `--verify` 绿、装机后 `init` 返回 `NOT_FOUND`、退出码 2 进 CRASH_LOOP。

## 正确性

- **R3 归一化空间不等比**：`SpatialClusterer::cluster` 用归一化 x（除以 W）与归一化 y（除以 H）
  直接算欧氏距离再除以归一化身高，等价于横向像素距离乘以 `H/W`。16:9 下横向聚集判据被放宽
  1.78 倍；抽样帧 640×443 也有 1.45 倍偏差。单测用纯归一化合成框，天然测不出来。
- **R4 宽限期与滞留时长耦合**：`kDefaultGracePeriodSeconds = 1.5f` 固定，与
  `gather_duration_seconds`（schema 上限 60）无关。长时长配置下一次 >1.5s 的漏检就清零 dwell，
  阈值实际不可达。
- **R5 配置区间两份真值**：`config.schema.json`（宿主 `internal/algorithm/config_schema.go`
  用它校验）与代码 clamp 区间逐项不一致。

## 性能（需 profile 复核，先收结构性开销）

- **R6 每帧张量分配**：`letterboxed`(1.2MB) + `onnx_input`(4.9MB) 每帧新建，且
  `session.forward({onnx_input})` 还会整体拷贝一次。
- **R7 每帧重解析 `params.json`**：一次整串拷贝 + 每键 `quoted_key`/`substr` 临时量。
- **R8 640×640 补边浪费**：16:9 源帧 44% 画布是补边（样本帧 31%）。
- **R9 全局互斥**：`TemporalTracker` 一把锁串行化所有 camera 的时序更新，与「一路 broker 一个
  Worker」的并发模型相悖。

## 规范与一致性

- **R10** 输出布局不符只表现为「永远零检出、无日志」（spec §4 明确警告的静默零检出）。
- **R11** 包内两份 IoU 实现，且没有复用 SDK 的 `zhulong::ops::nms`（plate 档位在用）。
- **R12** JSON 扫描逻辑重复、数值尾部不校验、`<exception>` 靠传递包含。
- **R13** 缺 `static_assert(TARGET_MODEL_EXT == ".onnx")` 档位断言。
- **R14** 包未接入 `make sdk-clean-check` / `make pkg-drift-check`。
- **R15** `build_package.py` 不校验验包报告：`sample_decoded=false` 时仍打印「验包通过」。
- **R16** 死代码：`track_id` / `next_track_id_` / `reset_task`；未使用的 `<iostream>`；
  `TaskStreamTracker::update` 95 行四职责。

## 验收标准

- `cd packages/crowd_gathering/onnx && make unit-test` 在**故意改错阈值**时退出码非零；
  正常时打印每个用例名并返回 0。
- `make test` 仍能产出 `build/out/result-crowd_gathering-onnx.jpg`。
- 从 `cwd != 包目录` 启动 `./crowd_gathering --verify --sample <ppm>` 能通过（模型路径不依赖 CWD）。
- 单测覆盖横向/纵向 aspect 不变性、宽限期随滞留时长伸展、schema 区间回落。
- `python3 native/scripts/check_package_drift.py packages/crowd_gathering` 与
  `make sdk-clean-check` 的列表都包含该包。
- 算法包的 `-Wall -Wextra -Wshadow` 保持零告警。
