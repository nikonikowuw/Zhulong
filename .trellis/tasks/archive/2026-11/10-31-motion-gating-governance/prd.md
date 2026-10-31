# PRD: 运动门禁分级治理与心跳保底巡检 (Motion Gating Governance & Heartbeat Keep-Alive)

## 1. 目标与背景 (Goal & Background)

### 1.1 目标
针对边缘端异构算力（如 RK3568/RK3588/昇腾 Atlas 等），在已有的 Native 流级轻量运动检测与推理短路门禁（`10-27-native-engine-motion-detection`）基础上，建立**「算法能力声明定安全底线 + 宿主 CLI 透传赋能」**的分级治理体系，并引入**「长时间静止心跳保底巡检机制（Heartbeat Keep-Alive）」**。

彻底解决当前底层运动门禁“全算法一刀切硬编码短路”导致的时序/状态类算法失效，根除“伪消失信标清零跟踪器”引发的业务误报死循环，以及“静止超过 2 秒即永久停摆”导致的目标丢失与微动漏检问题。

### 1.2 现状痛点与根本矛盾
1. **时序与状态类算法被“一刀切”扼杀**：
   - 违停检测（车停稳了不动）、睡岗/离岗检测（工人静坐不动）、烟雾与阴燃火灾（蔓延极其缓慢）等算法，核心诉求就是**分析持续静态特征**；
   - 当前 Runner 骨架（`algo_main.cpp`）无条件按 `has_motion == 0` 短路跳过 `api.process()`，导致此类算法在最关键的静止时刻被直接掐死，计时断档、目标丢失。
2. **伪消失信标（Ghost Vanishing Beacon）清零跟踪器**：
   - 当前 Runner 在画面静止时若检测到 `last_had_targets == true`，会立即发出一次 `count = 0` 的空推理结果作为“消失信标”；
   - 对于违停算法，车辆停稳后画面一旦被判定为静止，Runner 立即回传空结果，导致下游跟踪器（Tracker）直接销毁该车辆的 Track ID，违停计时器重置清零；
   - 心跳复核唤醒时，又被视作刚入场的新目标重新开始从 0 秒计时，导致违停报警永远无法达到判定阈值（如连续违停 3 分钟）。
3. **缺乏周期性心跳巡检（Heartbeat Challenge）**：
   - 当前仅有运动停止后的 10 帧（@5fps 约 2s）`MOTION_HOLD_FRAMES` 缓冲；
   - 缓冲耗尽后若画面持续无动静，NPU 推理永久停摆，导致站立不动的人脸识别框消失，且低照度下的极慢蠕动目标无法被唤醒复核。
4. **安全底线与运营自由的割裂**：
   - 若由非专业运维在界面随意控制开关，极易为违停/离岗等算法误开门禁导致漏报故障；
   - 必须通过算法包能力声明定死不可侵犯的安全底线。

### 1.3 行业成熟方案对照
- **Frigate NVR**：针对静态目标引入 `stationary.threshold`（10s）与关键的 **`stationary.interval`（周期性强行向模型 Challenge 静态目标，防止永久误判）**；
- **海康威视 (Hikvision) / 大华 (Dahua)**：严格实行**双轨隔离制**。SMD/动检 2.0 仅用于通用人车抓拍过滤；而违停、滞留等 IVS 智能行为分析**完全绕过动检门禁**，禁止用户在静态分析上开启门禁。

---

## 2. 核心设计原则 (Design Principles)

1. **事实提供者与业务决策者解耦 (Fact vs. Policy)**：
   - Native Engine（`FramePublisher` / `MotionDetector`）作为**事实提供者**，单流以 0.2ms 极低开销提取全图运动事实（`has_motion`）与周期心跳保底；
   - 各算法 Runner 进程作为**业务决策者**，结合算法自身声明的能力决定是否执行短路。
2. **声明式定安全底线 (Declarative Floor)**：
   - **算法包 `manifest.json` 声明能力**：违停、烟火等时序算法声明 `supports_motion_gate: false`；通用抓拍算法声明为 `true`（或缺省）；
   - 不支持门禁的算法，宿主强制剥离门禁逻辑，永不短路。
3. **零旁路 CLI 契约 (Zero-Bypass CLI Contract)**：
   - 严格恪守 SDK 规范：**算法包二进制不读取任何旁路配置文件或 `manifest.json`**；
   - 宿主（Go `algorunner`）在解析算法元数据后，通过命令行参数 `--disable-motion-gate` 显式下发给 Runner 子进程，保证进程入参透明可复原。
4. **单帧瞬态冲激，严禁状态机污染 (Isolated Transient Heartbeat)**：
   - 心跳保底仅是一次“单帧抽检复核”，放行帧必须**严格独立**，**绝对禁止重置或激活 10 帧的 `MOTION_HOLD_FRAMES` 缓冲**；
   - 心跳触发后，下一帧若依然无真实运动，必须立即跌回静止，杜绝节能收益缩水。

---

## 3. 分期实施路线 (Phased Implementation)

- **Phase 1 (MVP - 底层闭环与安全底线)**：
  - Native 检测器静止心跳保底机制与单帧防污染状态机；
  - 算法包 `manifest.json` 扩展 `supports_motion_gate` 契约与 Go 验包准入；
  - Runner 骨架扩展 `--disable-motion-gate` 参数，实现推理与伪消失信标的彻底旁路；
  - Go 宿主启动子进程时根据算法包能力自动注入参数。
- **Phase 2 (运营演进 - 任务级扩展与界面防呆)**：
  - 数据库 `task_algorithms` 扩展可选门禁字段与数据库迁移；
  - 前端 Web 任务配置弹窗实现「算法不支持门禁时开关置灰禁用 + 气泡提示」防呆交互；
  - 支持多通道任务维度的动态门禁策略下发。

---

## 4. 详细需求清单 (Requirements)

### R1. Native 运动检测器静止心跳保底巡检 (MotionDetector Heartbeat Keep-Alive)
- [x] 在 `native/src/pipeline/motion_detector.hpp` / `.cpp` 增加静止心跳状态与计数器；
- [x] **周期判定**：
  - 定义静态心跳周期门槛（默认约 10 秒，对应 5fps 下 50 帧；常量 `MOTION_HEARTBEAT_FRAMES = 50`）；
  - 内部维护 `still_frames_count_`，每当帧被判定为无真实运动（非 hold-off 期间）时累加；
- [x] **瞬态放行契约**：
  - 当 `still_frames_count_ >= MOTION_HEARTBEAT_FRAMES` 时：
    - 强制输出 `out_result.has_motion = true`，`out_result.box_count = 0`（全帧放行）；
    - `still_frames_count_` 归零；
    - **⚠️ 核心约束**：严禁修改 `hold_frames_remaining_`（必须保持为 0），确保心跳仅持续 1 帧，下一帧若仍无运动立即归回静止；
    - 正常滚动参考底图 `prev_grid_`，保证差分底图连续性；
- [x] 在视频流复位（`reset()`）时同步重置 `still_frames_count_`。

### R2. 算法包能力声明元数据契约 (`manifest.json`)
- [x] 扩展算法包元数据规范：
  - `manifest.json` 新增顶层字段 `supports_motion_gate`（布尔值，可选，缺省默认为 `true`）；
  - 任何时序、在岗、滞留、违停、阴燃烟火等依赖连续静态分析的算法打包时显式声明为 `false`；
- [x] Go 宿主 `internal/algorithm/model.go`：
  - `Manifest` 结构体新增 `SupportsMotionGate *bool` 字段；
  - 提供辅助方法 `Manifest.SupportsMotionGateOrDefault() bool`（空指针时返回 `true`）；
  - `Algorithm` 实体模型与 API 响应透出该能力字段；
- [x] Go 验包准入门禁（`internal/algorithm/verifier.go`）与单元测试增加该字段的校验覆盖。

### R3. Runner 统一骨架命令行门禁策略 (`algo_main.cpp` / `motion_gate.hpp`)
- [x] **CLI 参数扩展**：
  - `native/sdk/include/zhulong/algo_main.hpp` 中 `Options` 增加 `bool disable_motion_gate{false}`；
  - `native/sdk/src/algo_main.cpp` 参数解析器（`parse_options`）支持解析 `--disable-motion-gate`；
- [x] **门禁纯函数升级**：
  - `native/sdk/src/internal/motion_gate.hpp` 的 `should_skip_inference` 升级支持入参控制：
    ```cpp
    inline bool should_skip_inference(uint32_t has_motion, bool motion_gate_enabled = true) noexcept {
        if (!motion_gate_enabled) return false;
        return has_motion == 0;
    }
    ```
- [x] **推理与消失信标彻底旁路**：
  - 在 `algo_main.cpp` 的 Worker 消息循环中，当 `options.disable_motion_gate == true` 时：
    - 无论 `dispatch.has_motion` 是 0 还是 1，**绝对不跳过推理**，正常执行 DMA-BUF 映射与 `api.process()`；
    - **绝对不进入消失信标发送分支**，杜绝伪信标向后端回传，彻底保护违停跟踪器的连续性。

### R4. Go 宿主进程级治理与参数注入 (`internal/algorunner`, `internal/app`)
- [x] 算法进程启动器 `internal/algorunner/process.go`：
  - 在 `RunnerProcessConfig` 中新增 `DisableMotionGate bool` 字段；
  - `Start()` 组装子进程命令时，若 `DisableMotionGate == true`，向 `args` 追加 `--disable-motion-gate`；
- [x] 宿主调度联动：
  - 监督器 `algorunner.Supervisor` 或服务层根据已加载算法的 `SupportsMotionGate` 属性，在构建进程配置时正确置位。

### R5. 前端展示与防呆契约 (Web / Phase 2 预留)
- [x] 前端 `Algorithm` 契约（`web/src/features/algorithms/data/schema.ts`）补齐 `supportsMotionGate` 字段；
- [ ] （Phase 2 预留）任务配置弹窗在选中 `supportsMotionGate === false` 的算法时，将动检节能模式锁定为关闭，置灰禁用并给出业务释义气泡。

---

## 5. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (心跳保底瞬态放行且不污染 hold-off)**：
  - 输入持续纯色静态视频序列（单测），采样率 5fps；
  - 前 10 帧（hold-off）放行；
  - 第 11~49 帧判定为静止（`has_motion == false`）；
  - 第 50 帧（心跳触发）必定放行（`has_motion == true, box_count == 0`）；
  - **第 51 帧立即恢复静止判定（`has_motion == false`）**，断言 `hold_frames_remaining_ == 0`，没有产生 10 帧扩散；
  - 全周期每 50 帧严格产生且仅产生 1 次心跳放行。
- [x] **AC-2 (Runner 命令行参数与门禁完全旁路)**：
  - 启动带 `--disable-motion-gate` 的 Runner；
  - 输入 `has_motion = 0` 的分发报文；
  - 断言 `gate::should_skip_inference` 返回 `false`；
  - 断言 `api.process()` 被 100% 正常调用，且不触发空结果消失信标。
- [x] **AC-3 (Go 宿主元数据与启动注入闭环)**：
  - 算法包 `manifest.json` 声明 `"supports_motion_gate": false`，验包通过；
  - Go 宿主拉起该算法 Runner 进程，断言启动子进程的命令行参数列表中精确包含 `--disable-motion-gate`；
  - 缺省老包默认解析为 `true`，命令行不附加 `--disable-motion-gate`，保持向后兼容。
- [x] **AC-4 (工程全绿回归)**：
  - `make native-test` 全部通过，包含新增的 `motion_detector_test` 心跳用例与 `motion_gate_test` 旁路用例；
  - `make go-check` 全部通过，无数据竞争与编译错误。

---

## 6. 关键决策记录 (Key Decisions)

| ID | 决策 | 理由与权衡 |
| :--- | :--- | :--- |
| **D1** | 心跳保底由 Native 检测器集中产生 | 单流单实例全局计算一次，所有订阅该流的 Runner 进程统一同步，无多进程时间抖动与 IPC 报文膨胀 |
| **D2** | 心跳放行与 hold-off 状态机物理绝缘 | 心跳是抽检，hold-off 是动静平滑；心跳若激活 hold-off 会连放 11 帧，严重破坏边缘节能初衷 |
| **D3** | CLI 参数传递（`--disable-motion-gate`）替代 Runner 读文件 | 严格遵守 SDK 零旁路原则，保持算法二进制干净独立，排障时从 `ps -ef` 可直接观测真实生效配置 |
| **D4** | 时序算法彻底阻断伪消失信标 | 违停/在岗算法最怕静止时收到 `count=0` 导致跟踪 ID 销毁与计时器重置，必须全链路彻底阻断 |
| **D5** | 缺省向前兼容策略 | 未显式声明 `supports_motion_gate` 的老算法包默认按 `true` 处理，不破坏现有抓拍算法的节能效果 |

---

## 7. 不在范围 (Out of Scope)

- **微观局部裁剪送检（Frigate Crop-Zoom）**：在弱 ARM SoC 上会破坏 DMA-BUF 零拷贝并极大消耗 CPU 带宽；
- **硬件光流单元集成**：本期仍基于 YUV 下采样纯内存差分，不引入专用硬件光流引擎。
