# 人脸识别跨帧特征融合与同轨迹升级落库

## Goal

在 `face_recognition` 算法包（`onnx` 与 `rk3568` 双 target 同步）的流式推理路径中，按任务流上下文（`TaskContext::task_id`）维护有界人脸特征状态池，对通过质量门禁的人脸执行防漂移质量加权跨帧特征融合（默认开启），并同步升级 Go 宿主 `internal/record` 的同轨迹识别冷却抑制语义，使远距走近、微侧脸恢复或光线波动下「首帧未命中、后续帧经特征融合命中」的目标既能提升实时识别召回率与稳定性，又能在默认 `filter_unmatched = false` 配置下准确补写已登记人员识别记录。

## Background & Confirmed Facts

- **SDK 流与 Worker 一对一串行模型**：`native/sdk/src/algo_main.cpp:1132-1136` 按「一路 broker（`stream_id`）→ 一个独占 Worker 线程 → 一个 `TaskContext`（`task_id = stream_id`）」串行派帧进入 `FaceRecognitionPlugin::detect_scene_structure`（`packages/face_recognition/onnx/src/pipeline/face_recognition.cpp:296-409`）。仓库已有同构状态机先例 `packages/crowd_gathering/onnx/src/analysis/temporal_tracker.hpp:116-152`（按 `task_id` 隔离状态容器 + 每路独立 `std::mutex`）。
- **全链路 `is_fused` 载荷已接通**：C ABI `zhulong_face_item::is_fused` / `zhulong_face_structure_item::is_fused`（`native/sdk/include/zhulong/plugin.h:169,236`）、SDK `zhulong::FaceItem::is_fused` / `FaceStructureItem::is_fused`（`native/sdk/include/zhulong/algo.hpp:190,258,670,923`）、Go `aitypes.FaceSubItem.IsFused`（`internal/aitypes/model.go:65,80`）、`internal/record/service.go:408,426` 与前端 `web/src/features/live/types/ai-result.ts:29` 均已就位，无需改动 C ABI 或数据库迁移；当前仅 `face_recognition.cpp:292` 固定赋值 `out_face.is_fused = false`。
- **单图特征提取隔离**：底库登记与以图搜脸走 `FaceExtractorContext::extract`（`zhulong_face_extract_feature`，`face_recognition.cpp:460-705`），不经过 `FaceRecognitionPlugin::detect_scene_structure`，不受流式特征融合影响。
- **构建与多 target 一致性约束**：
  - `packages/face_recognition/{onnx,rk3568}/Makefile` 的 `unit-test` 与 `native/CMakeLists.txt` 的 `ZhulongFaceGeometryTests` 仅编译 `aligner.cpp + matcher.cpp + aligner_matcher_test.cpp`；新增融合组件采用 header-only（`.hpp`）可直接接入两套单测且无需改动 `native/CMakeLists.txt`。
  - `make pkg-drift-check` 强制校验 `onnx` 与 `rk3568` 除白名单外的源码与 `config.schema.json` 完全一致。
- **宿主冷却耦合现状**：`internal/record/service.go:621-653` 与 `internal/record/cooldown_keys.go:19-43` 对 `trackID > 0` 的人脸（含关联人脸与孤立特写人脸）统一使用 `rec:{camera}:trk:{id}`（默认 3s 冷却，见 `.trellis/spec/backend/record-guidelines.md:20`）。在 `filter_unmatched = false` 下，首帧未命中的陌生人记录会锁住 `rec:{camera}:trk:{id}` 3 秒，导致后续帧融合升级出的 `matched = true` 记录被 `AllowAll` 拦截；既有单测 `internal/record/track_dedup_test.go:352-358,473-481` 对已匹配人脸断言 `rec:{camera}:trk:{id}` + `rec:{camera}:person:{person_id}`。
- **已确认的产品与范围决策**：
  1. 任务范围采用**方案 A**：同时覆盖 `packages/face_recognition/{onnx,rk3568}/` 跨帧特征融合与 Go `internal/record`（含 `record-guidelines.md` §1）同轨迹 `unmatched → matched` 一次性冷却升级。
  2. `enable_face_feature_fusion` 默认值为 **`true`**（开箱默认开启，任务参数可按路动态关闭）。

## Requirements

- **R1 — 按流隔离的有界特征融合状态池（`face_feature_fusion.hpp`）**：
  - 在 `packages/face_recognition/{onnx,rk3568}/src/common/face_feature_fusion.hpp` 实现 header-only 的单流融合池 `StreamFeatureFusionPool` 与多流管理器 `TaskFeatureFusionManager`（按 `task_context.task_id` 分桶、每路独立 `std::mutex`、流数软上限保护 `kMaxTrackedStreams = 256`）。
  - **整帧一对一互斥匹配**：单帧内所有通过质量门禁并提完 512 维特征的人脸，与该路池内未过期状态做全局相似度降序贪心二分匹配（确定性排序键：`(sim_fused desc, sim_anchor desc, obs_idx asc, state_idx asc)`），保证同一帧两张人脸绝不并入同一状态，且高相似真主优先夺得原状态。
  - **双重余弦门禁防漂移**：观测特征 $\mathbf{e}_t$ 与状态的当前融合特征 $\tilde{\mathbf{e}}$ **以及**历史最高质量锚点特征 $\mathbf{e}_{\text{anchor}}$ 的余弦点积均须满足 $\ge \tau_{\text{fuse}}$（`fusion_similarity_threshold`）且数值有限，才允许并入该状态；否则分配独立新状态。
  - **质量加权累加与重归一化**：融合权重取 $w_t = \max(q_t, 0.05)$；设置累计权重软上限 `kMaxAccumulatedWeight = 6.0f`（超出时按比例衰减历史累加向量后再叠加 $w_t \mathbf{e}_t$，防止长驻目标被早期低质帧锁死），每次更新后重做严格 L2 单位归一化以保持 `Matcher::match` 余弦点积契约；若 $q_t > q_{\text{anchor}}$ 则更新锚点特征与锚点质量；状态累计观测帧数 $\ge 2$ 时输出 `is_fused = true`，首帧新建状态输出 `is_fused = false`。
  - **超时与容量上界**：每次帧级更新前按单调时钟淘汰超过 `fusion_ttl_seconds` 未命中的状态（对异常时钟倒退做钳位保护）；单路活跃状态上限 `kMaxActiveStatesPerStream = 32`，满额新建时淘汰最久未更新状态（次级 tie-break：更低 `anchor_quality`、更小下标）。
- **R2 — 流水线接线与运行时参数/Schema 同步**：
  - 在 `MatchParams`、`resolve_match_params`（`match_config.hpp`）与双 target `config.schema.json` 中新增并同步三项配置：
    - `enable_face_feature_fusion`：`boolean`，默认 `true`。
    - `fusion_similarity_threshold`：`number`，范围 `[0.45, 0.95]`，默认 `0.62`。
    - `fusion_ttl_seconds`：`number`，范围 `[0.5, 30.0]`，默认 `3.0`。
  - 重构 `FaceRecognitionPlugin::detect_scene_structure`（`face_recognition.cpp`）的人脸阶段为「逐脸质量门禁 + 对齐 + 特征提取 → 整帧 `TaskFeatureFusionManager::fuse_frame` → 融合后单位向量送入 `Matcher::match` + `should_emit_face_result`」，将 `is_fused` 如实写入 `FaceItem` / `FaceStructureItem`。
  - 当 `enable_face_feature_fusion == false` 时，清空该 `task_id` 池内历史状态并直接透传当帧单帧特征且 `is_fused = false`。
  - 更新 `packages/face_recognition/README.md` 反映新增的跨帧特征融合阶段与配置项。
- **R3 — Go 宿主 `internal/record` 同轨迹 `unmatched → matched` 一次性冷却升级**：
  - 调整 `internal/record/cooldown_keys.go`、`internal/record/cooldown.go` 与 `internal/record/service.go`：
    - `trackID > 0` 且未匹配（`!face.Matched && face.PersonID == ""`）使用身份键 `rec:{camera}:trk:{id}:unmatched`。
    - `trackID > 0` 且已匹配（`face.Matched || face.PersonID != ""`）保持检查两级键 `rec:{camera}:trk:{id}` + `rec:{camera}:person:{person_id}`；在原子检查全部通过时，于同把互斥锁内一并打戳 `rec:{camera}:trk:{id}:unmatched`（任一检查键处于冷却时拒绝且不改写任何键时间戳）。
  - 同步更新 `.trellis/spec/backend/record-guidelines.md` §1 去重键格式说明。

## Acceptance Criteria

- [x] **AC1（C++ 跨帧融合数学与防漂移）**：`aligner_matcher_test.cpp` 覆盖（a）首帧新建状态 `is_fused == false`、同人后续帧（余弦 $\ge \tau_{\text{fuse}}$）融合后 L2 模长为 `1.0` 且 `is_fused == true`，使单帧低于阈值（如 `0.38 < 0.40`）的带噪同人序列在融合后稳定命中 `0.40`；（b）同帧双脸一对一互斥不串扰且高相似候选优先匹配；（c）渐变桥接特征若偏离 `anchor_features`（`< fusion_similarity_threshold`）被拒绝融合并另起新状态；（d）TTL 超时淘汰、32 容量上限淘汰及 `enable_face_feature_fusion = false` 清池回退单帧语义；（e）多 `task_id` 状态完全隔离。
- [x] **AC2（配置解析与双 target 零漂移）**：`resolve_match_params` 正确解析 `enable_face_feature_fusion`、`fusion_similarity_threshold`、`fusion_ttl_seconds` 的默认值、进程级/任务级覆盖与越界回落；`packages/face_recognition/onnx/config.schema.json` 与 `rk3568/config.schema.json` 字节级一致，`make pkg-drift-check` 退出码为 `0`。
- [x] **AC3（Go 宿主同轨迹冷却升级契约）**：`internal/record` 表驱动与集成测试验证同一 `track_id > 0` 在 3s 冷却窗口内：（a）`unmatched → matched` 允许升级落库共 2 条记录（1 条陌生人 + 1 条已登记人员）；（b）`unmatched → unmatched` 只落 1 条；（c）`matched → matched` 只落 1 条；（d）`matched → unmatched` 只落 1 条（已匹配后不补发退化陌生人）；（e）跨轨迹同 `person_id` 命中拒绝时不污染新轨迹的 `unmatched` 键。
- [x] **AC4（端到端构建与门禁通过）**：`onnx` 包 `unit-test` 与 `test`、`make native-build`、`make native-test`（含 `ZhulongFaceGeometry`、`ZhulongFaceRecognitionPlugin`、`ZhulongFaceRecognitionOnnx`）、`python3 native/scripts/build.py go test -race ./internal/record/...` 与 `make go-check` 全部通过。

## Out of Scope

- 不修改 C ABI 头文件（`native/include/Zhulong/engine.h`、`native/sdk/include/zhulong/plugin.h`）或数据库迁移（`internal/database/migrations/`）。
- 不对未通过质量门禁的人脸强行提取特征或做像素框级卡尔曼跟踪。
- 不改变底库单图特征提取入口 `zhulong_face_extract_feature`（`FaceExtractorContext`）的单帧行为。
- 不在前端新增额外的融合专属 UI 控件（动态表单自动渲染新增的 `config.schema.json` 字段，实时叠加层与记录流沿用现有结构）。
