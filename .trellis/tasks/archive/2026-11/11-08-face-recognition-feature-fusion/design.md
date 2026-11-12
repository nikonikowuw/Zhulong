# 技术设计：人脸识别跨帧特征融合与同轨迹升级落库

## 1. 架构与分层边界

```mermaid
flowchart LR
    subgraph AlgoWorker["算法包 Worker (1 stream_id = 1 task_id)"]
        Q["质量评估 + 5点仿射对齐 + 可选图像门禁"]
        E["FaceFeatureExtractor (512-D L2 单位向量)"]
        F["TaskFeatureFusionManager::fuse_frame (按 task_id 分桶)"]
        M["Matcher::match + should_emit_face_result"]
        Q --> E --> F --> M
    end
    subgraph GoHost["Go 宿主 (internal/record)"]
        T["Tracker 分配宿主 track_id"]
        C["CooldownManager.AllowAllAndStamp"]
        DB[("SQLite recognitions")]
        T --> C --> DB
    end
    M -- "SceneStructure (matched, similarity, is_fused)" --> T
```

- **算法包内部（`packages/face_recognition/{onnx,rk3568}/`）**：
  - 新增纯数据逻辑 header-only 模块 `src/common/face_feature_fusion.hpp`，不依赖 ONNX Runtime / RKNN / 图像句柄，直接纳入 `make unit-test` 与 `ZhulongFaceGeometryTests`。
  - 仅在流式入口 `FaceRecognitionPlugin::detect_scene_structure` 接入跨帧融合；单图登记入口 `FaceExtractorContext::extract` 保持无状态单帧提取。
- **Go 宿主感知记录层（`internal/record/`）**：
  - 仅扩展 `CooldownManager` 的原子检查+附带打戳原语与 `cooldown_keys.go` 的识别键构造，不改表结构、不改 HTTP DTO、不改 `StreamHub` 实时广播路径。

## 2. C++ 跨帧特征融合详细设计

### 2.1 数据结构（`src/common/face_feature_fusion.hpp`）

- 常量约定：
  - `kFusionFeatureDim = 512`
  - `kMaxActiveStatesPerStream = 32`（单路最多保留 32 个活跃状态；单状态实测 6168 B，单路满额 ≈ 192.8 KiB，进程内 256 路上限对应最坏 ≈ 48.2 MiB）
  - `kMaxTrackedStreams = 256`（防止异常或压测无限构造 `task_id` 导致空桶增长）
  - `kMaxAccumulatedWeight = 6.0f`（单状态累计质量权重软上限，约等价 8~10 帧高质量观测的指数平滑窗口）
  - `kMinFusionWeightFloor = 0.05f`（防止 `min_face_quality = 0.0` 配置下出现零权重退化）
- `FusionFaceObservation`（单帧单脸输入）：
  - `const float* features`（长度 `512` 的 L2 单位特征）
  - `float quality`（`assess_face_quality` 产出的 `quality_score` $\in [0, 1]$）
- `FusedFaceOutput`（单帧单脸输出）：
  - `std::array<float, kFusionFeatureDim> features`（融合后严格 L2 单位向量）
  - `bool is_fused{false}`（当且仅当该状态累计融合帧数 $\ge 2$ 时为 `true`）
- `FusionTrackState`（池内单状态）：
  - `std::array<float, kFusionFeatureDim> weighted_sum`（质量加权未归一化累加器）
  - `std::array<float, kFusionFeatureDim> fused_features`（当前 L2 单位化融合特征 $\tilde{\mathbf{e}}$）
  - `std::array<float, kFusionFeatureDim> anchor_features`（历史最高质量单帧锚点特征 $\mathbf{e}_{\text{anchor}}$）
  - `float accum_weight{0.0f}`
  - `float anchor_quality{0.0f}`
  - `uint32_t observation_count{0}`
  - `std::chrono::steady_clock::time_point last_seen`

### 2.2 单流帧级推进算法（`StreamFeatureFusionPool::fuse_frame`）

1. **开关旁路与清池**：
   - 若 `!params.enable_face_feature_fusion`，立即调用 `states_.clear()` 释放历史状态，并将每条观测原样拷贝至 `FusedFaceOutput`（`is_fused = false`）返回。
2. **TTL 超时淘汰与时钟防护**：
   - 遍历 `states_`，若 `now < state.last_seen`（时钟异常回退）则钳位 `state.last_seen = now`；若 `std::chrono::duration<double>(now - state.last_seen).count() > static_cast<double>(params.fusion_ttl_seconds)` 则移除该状态。
   - 若本帧 `observations.empty()`，淘汰后直接返回空向量。
3. **整帧一对一贪心二分匹配 + 双重门禁**：
   - 对每个有效观测 $i$（512 维全有限且模长 $> 10^{-6}$）与每个池内状态 $j$：
     - 计算 $s_{\text{fused}} = \langle \mathbf{e}_i, \tilde{\mathbf{e}}_j \rangle$ 与 $s_{\text{anchor}} = \langle \mathbf{e}_i, \mathbf{e}_{\text{anchor}, j} \rangle$。
     - 仅当二者均有限且 $s_{\text{fused}} \ge \tau_{\text{fuse}} \land s_{\text{anchor}} \ge \tau_{\text{fuse}}$ 时加入候选边集合。
   - 对候选边按 `(s_fused desc, s_anchor desc, obs_idx asc, state_idx asc)` 稳定排序，依次对尚未配对的 `obs_idx` 和 `state_idx` 建立一对一配对。
4. **命中状态的质量加权融合**：
   - 设 $w_i = \max(\text{clamp}(q_i, 0.0\text{f}, 1.0\text{f}), 0.05\text{f})$。
   - 若 `state.accum_weight + w_i > kMaxAccumulatedWeight`，计算衰减系数 $\alpha = \max(0.0\text{f}, (\text{kMaxAccumulatedWeight} - w_i) / \text{state.accum\_weight})$，将 `state.weighted_sum` 各项乘以 $\alpha$ 并令 `state.accum_weight *= alpha`。
   - 累加 `state.weighted_sum[d] += w_i * e_i[d]`，`state.accum_weight += w_i`。
   - 计算 $\|\text{weighted\_sum}\|_2$；若有限且 $> 10^{-6}$，归一化写入 `state.fused_features`，否则以 $\mathbf{e}_i$ 重置该状态。
   - 若 $q_i > \text{state.anchor\_quality}$，更新 `anchor_features = e_i` 与 `anchor_quality = q_i`。
   - `observation_count` 饱和自增，`last_seen = now`，本观测输出 `(state.fused_features, observation_count >= 2)`。
5. **未命中观测的新状态分配**：
   - 若 `states_.size() >= kMaxActiveStatesPerStream`，先只在本帧**未被命中**（`last_seen != now`）的状态里淘汰 `last_seen` 最早者（并列取 `anchor_quality` 更低、再并列取最小下标）。
   - 若 32 个状态本帧全部命中（同帧人数 > 容量），**不驱逐任何状态**，本观测直接输出单帧特征（`is_fused = false`）。理由：拆掉刚更新过的状态会让该脸的 `is_fused` 每帧 1/0 抖动，融合永远攒不起来；代价是超容量那几张脸要等一轮才有状态。
   - 以 $\mathbf{e}_i$ 初始化新 `FusionTrackState`（`observation_count = 1`），本观测输出 `(e_i, is_fused = false)`。

### 2.3 多流隔离容器（`TaskFeatureFusionManager`）

- 复刻 `crowd_gathering` 的 CP.50 锁随数据走模式：`std::unordered_map<uint64_t, std::unique_ptr<TaskEntry>>`（`TaskEntry` 内含 `std::mutex mutex`、`StreamFeatureFusionPool pool`、`steady_clock::time_point last_access`）。
- 外层 `entries_mutex_` 仅保护桶查找/创建；桶数达 `kMaxTrackedStreams` 时优先清理最久未访问的桶；实际余弦计算与加权更新均在单路 `entry.mutex` 下执行，多相机 Worker 互不阻塞。

### 2.4 流水线重排（`FaceRecognitionPlugin::detect_scene_structure`）

将原逐脸串行 `build_face_item` 拆为两段私有辅助方法：
1. `extract_qualified_face(...)`：完成 `is_real_detection` 检查、`assess_face_quality`、`Aligner::estimate_similarity_transform`、`Aligner::crop_and_align_direct`、`passes_face_image_gate` 与 `extractor_.extract`，产出 `QualifiedFace`（框、关键点、`quality_score`、512 维单帧特征）。未过门禁的人脸在此直接跳过，**零额外 NPU 开销**。
2. 整帧调用 `fusion_manager_.fuse_frame(task_context.task_id, observations, match_params)` 拿到每张合格脸的 `(fused_features, is_fused)`。
3. `match_and_populate_face(...)`：对每张合格脸调用 `matcher_.match(fused.features.data(), ...)` 与 `should_emit_face_result(match_result, match_params)`，填充 `ProcessedFace`（含 `item.is_fused = fused.is_fused`）。

## 3. Go 宿主识别冷却升级设计（`internal/record`）

### 3.1 去重键与原子打戳契约

- 在 `internal/record/cooldown_keys.go` 定义：
  - `isMatchedRecognition(face aitypes.FaceSubItem) bool`：`return face.Matched || face.PersonID != ""`（兼容仅填 `PersonID` 的既有单测夹具与真实载荷）。
  - `unmatchedTrackRecognitionCooldownKey(cameraID string, trackID int64) string`：`fmt.Sprintf("rec:%s:trk:%d:unmatched", cameraID, trackID)`。
  - `recognitionCooldownKey(cameraID string, trackID int64, face aitypes.FaceSubItem) string`：
    - 当 `trackID > 0`：若 `isMatchedRecognition(face)` 返回 `rec:{camera}:trk:{id}`；否则返回 `rec:{camera}:trk:{id}:unmatched`。
    - 当 `trackID <= 0`：保持原回落逻辑（有 `PersonID` 返回 `rec:{camera}:person:{person_id}`，否则返回 `rec:{camera}:unmatched:{x:.3f}:{y:.3f}`）。
  - `recognitionCooldownPlan(cameraID string, trackID int64, face aitypes.FaceSubItem) (checkKeys []string, stampOnlyKeys []string)`：
    - `checkKeys = recognitionCooldownKeys(cameraID, trackID, face)`。
    - 当 `trackID > 0 && isMatchedRecognition(face)` 时，`stampOnlyKeys = []string{unmatchedTrackRecognitionCooldownKey(cameraID, trackID)}`；其余场景 `stampOnlyKeys = nil`。
- 在 `internal/record/cooldown.go` 新增 `AllowAllAndStamp(checkKeys, stampOnlyKeys []string, now time.Time) bool`（`AllowAll` 直接委托 `AllowAllAndStamp(keys, nil, now)` 避免重复逻辑）：
  - 在同一把 `cm.mu` 下：先检查 `checkKeys` 中是否有任意键在 `[0, cooldown)` 窗口内；若有则立即返回 `false`，**不写入任何 `checkKeys` 或 `stampOnlyKeys`**。
  - 若 `checkKeys` 全部通过，则将去重后的 `checkKeys` 与非空 `stampOnlyKeys` 统一写入 `cm.records[k] = now` 并返回 `true`。

### 3.2 状态转移真值表（同一 `trackID > 0`，3s 窗口内）

| 帧序事件 | 检查键 (`checkKeys`) | 附带打戳键 (`stampOnlyKeys`) | 裁决结果 |
| --- | --- | --- | --- |
| 第 1 次 `unmatched` | `rec:cam:trk:1:unmatched` | — | **放行落库**（记录陌生人） |
| 第 2 次 `unmatched` | `rec:cam:trk:1:unmatched`（冷却中） | — | 抑制 |
| 融合后第 1 次 `matched(P1)` | `rec:cam:trk:1`, `rec:cam:person:P1` | `rec:cam:trk:1:unmatched` | **放行升级落库**（记录 P1，并封锁后续陌生人） |
| 融合后第 2 次 `matched(P1)` | `rec:cam:trk:1`（冷却中）, `rec:cam:person:P1` | `rec:cam:trk:1:unmatched` | 抑制 |
| 已匹配后偶发退化 `unmatched` | `rec:cam:trk:1:unmatched`（已被匹配打戳） | — | 抑制（防倒退刷陌生人） |
| `matched(P1)` 因跨轨迹 `person:P1` 冷却被拒 | `rec:cam:trk:2`, `rec:cam:person:P1`（冷却中） | `rec:cam:trk:2:unmatched` | 原子拒绝（不污染 `trk:2`） |

## 4. 兼容性、风险与回滚考量

- **配置前向/后向兼容**：老任务 `config_json` 不含新增的 3 个字段时，`resolve_match_params` 自动采用默认值（`enable_face_feature_fusion = true, fusion_similarity_threshold = 0.62, fusion_ttl_seconds = 3.0`）；越界或非法类型保持回落默认，绝不中止插件启动。
- **动态裁剪（Crop Region）兼容**：特征融合完全基于 512 维外观向量余弦相似度与帧内互斥配对，不依赖跨帧像素框位置假设，即使宿主 AI 分析视图（`input_view`）发生改变也不会断链或误配。
- **在线回滚手段**：无需重编译或重启宿主，只需在任务级算法参数中将 `enable_face_feature_fusion` 设为 `false`，对应视频流下一帧即刻清空融合池并回退为纯单帧比对。
