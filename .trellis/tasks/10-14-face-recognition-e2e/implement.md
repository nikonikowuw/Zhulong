# 执行计划：人脸识别端到端接入（父任务编排）

> **父任务不直接实现产品代码。** 本文件是子任务编排、门禁与集成审查计划。
> 每个子任务的代码级执行清单在各自的 `implement.md` 中。

## 前置门禁（阻塞一切实现）

- [ ] **G1** 回答父 `prd.md`「待决问题」三条（平台 / 人脸能力来源 / 特征提取归属）。
      验证：答案写入本文件下方「决策记录」并被相关子任务 `design.md` 引用。
- [ ] **G2** `algo-structure-abi` 的 ABI 方案（v1 原地扩展 / v2 新表 / JSON）定案。
      验证：方案写入该子任务 `design.md`，且 `face-algo-package` 的 `prd.md` 引用同一方案。
- [ ] **G3** 确认 FrameBroker 宿主归属结论仍成立（native engine 内）。
      验证：`native/src/pipeline/engine.cpp` 的 `ensure_frame_broker` 未被迁移；若已迁移，D1/D2 需重新评审。

## 执行顺序

```txt
G1 ─→ G2 ─→ [Wave 1] algo-structure-abi
                       │
                       ├─→ [Wave 2a] gallery-hot-sync ──────┐
                       ├─→ [Wave 2b] inference-result-egress │
                       └─→ [Wave 2c] face-feature-extraction │
                                                             │
                              [Wave 3] face-algo-package ←───┤
                              [Wave 3] face-result-frontend ←┘
                                                             │
                                       [Wave 4] 端到端集成审查 ┘
```

- **Wave 1**：ABI。硬前置，单独进行。完成后冻结 ABI。
- **Wave 2**：三条通道可**并行**（gallery / result / feature），但共享 §2 跨层契约，开工前需一次性对齐字段名与身份空间。
- **Wave 3**：算法包与前端叠加可**并行**。
- **Wave 4**：父任务主持的端到端集成审查。

## 各子任务门禁

| 子任务 | 进入条件 | 完成判据 |
| --- | --- | --- |
| `algo-structure-abi` | G1 已答 | ABI 定案 + 跨语言契约测试通过 + `algo.hpp` 注册宏可用 + `verifier.go` 映射更新 |
| `gallery-hot-sync` | Wave 1 冻结 | 冷启动 SYNC + 增量 UPSERT/DELETE 均生效；分片在超 64KB 时正确工作；ACK 语义有测试 |
| `inference-result-egress` | Wave 1 冻结 | runner 上报替代 `result_release`；Go 侧有真实调用方；`InferenceResult` 不再是死代码 |
| `face-feature-extraction` | G1 已答（归属） | `feature_vector` 非常量；同人不同照 cosine 高于阈值；维度不符显式失败 |
| `face-algo-package` | Wave 1 + Wave 2 全绿 | 验包门禁通过；自测样本产出正确结构化结果 |
| `face-result-frontend` | `inference-result-egress` 完成 | 检测框跟随画面；识别记录可查；三语齐全 |

## 集成审查清单（Wave 4）

- [ ] 逐条核对父 `prd.md` 的 **AC1–AC7**，每条附**实测命令与输出**（不接受「代码看起来对」）。
- [ ] 核对 **AC7**：断点 B1–B4 的四处 `⚠️ NOT WIRED` 注释与死代码状态被**真实消除**。
      验证：`grep -rn "NOT WIRED" internal/ native/` 结果为空，且对应函数有真实调用方（非测试）。
- [ ] 核对 §2.4 枚举护栏覆盖了本任务新增的**全部** `MSG_*`。
- [ ] 核对 §2.1 身份字段空间：抽查一处端到端结果，确认 `person_id` 是 `persons.person_id` 而非 `persons.id`。
- [ ] 核对 §2.5：`ready` 语义未被改动。
- [ ] 回滚路径逐条演练：每个子任务的回滚不破坏既有功能。
- [ ] 原生侧 ASan/UBSan 无错误；Go 侧 `-race` 无竞争。
- [ ] `make check` 通过。

## 验证命令

```bash
# 原生侧（含 ASan/UBSan）
make native-test

# Go 侧（经 wrapper，含 -race）
make go-check

# 全量门禁
make check

# AC7 的机械化核对
grep -rn "NOT WIRED" internal/ native/
```

## 决策记录

> G1 / G2 的答案记录于此，供子任务引用。
> 证据来源：`research/face-model-baseline.md`（2026-10-14 开发机实测）。

### G1-1 目标平台：**ONNX（开发机主环境）+ RKNN（板端验收）**

- 开发机装的是**真实** ONNX Runtime（`libonnxruntime.so.1.21` + venv 内 onnxruntime 1.23.2），
  可跑真实模型、真实特征。
- ⚠️ 既有 `cpu_inference_backend.cpp` **是 mock**（`weights.resize(1MB, 0x5A)`，
  forward 返回常量张量），**不能用于任何精度验证**。
- 分工：管道 / 特征正确性 / 前端 → 开发机 ONNX 闭环；性能 / 功耗 / 最终精度 → 板端 RKNN。
- 需新增 `ZHULONG_INFERENCE_BACKEND=onnx` 开关，且必须**同步改** `native/CMakeLists.txt`
  与 `native/scripts/build.py:212` 的校验元组，否则 profile 校验 `FATAL_ERROR`。
- ⚠️ 需装 `libonnxruntime-dev`（头文件）。这是**开发者环境前置**，不是构建期下载，
  不破坏「`make native-deps` 是唯一联网步骤」的约定 —— 须写入 README 前置依赖。
- ⚠️ `verifier.go:181` 要求 `manifest.platform.target` 与 `config.toml [algorithm].target`
  **完全一致**，故开发机与板端需不同 target 值（如 `onnx` / `rk3568-rknn`）。

### G1-2 人脸能力来源：`det_500m.onnx` + `facelivtv2-l.onnx`

- 检测：`rknn_model_zoo/examples/insightface/buffalo_s/det_500m.onnx`（SCRFD，0.5M）
- 特征：`rknn_model_zoo/examples/facelivt/facelivtv2_l/model/facelivtv2-l.onnx`（512 维）
- ✅ **选 `facelivtv2-l` 替代 `w600k_r50` 绕开了 InsightFace 的商用雷区**。
- ⚠️ **但许可仍未完全干净**：`det_500m` 权重仍是 InsightFace 出品（非商业研究），
  `facelivtv2-l` 上游标注 CC-BY-SA-4.0（传染性）。**交付前须法务确认**，
  否则需替换模型。这是**唯一剩下的非技术阻塞项**。

### G1-3 特征提取归属：**插件内提取**

- 登记与运行时使用**同一份提取代码、同一后端**，消除特征空间漂移。
- ✅ 不打破「`internal/engine` 是唯一可 `import "C"` 的包」这条架构铁律。
- ✅ 特征（生物识别信息）不落 SQLite 明文，符合 `docs/prd/prd-v1.0.md:190` 的合规方向。
- ✅ 载荷只传 `image_path` 不传向量 → **`gallery-hot-sync` 的 64KB 分片风险降级**。
- 代价：`person_faces.feature_vector` 列语义需重新定义；冷启动需重新提取（可加磁盘缓存）。

### G2 ABI 方案：（待 `10-14-algo-structure-abi` 定案）

已确认的**输入约束**（来自实测，非推测）：

- 结构化结果必须能承载 **5 点 landmarks**（det_500m 的 kps 输出 10 维 = 5×2）。
- 既有 `zhulong_bbox` 为扁平单层结构，**无法承载** `PersonItem` 的嵌套 `faces[]`。
- 父 `design.md` §3 倾向 **v2 新表 + `struct_size` 协商**。

### G3 FrameBroker 宿主归属：**确认仍成立**

`native/src/pipeline/engine.cpp:233` 的 `ensure_frame_broker` 未被迁移；
D1 / D2（gallery 与结果通道复用同一 UDS）的前提有效。

## 已解决的阻塞

| 门禁 | 状态 | 证据 |
| --- | --- | --- |
| G1-1 平台 | ✅ 已答 | 开发机 ONNX 基准产出 |
| G1-2 模型来源 | ⚠️ 技术可用，**许可待法务** | 实测 rank-1 98.7% |
| G1-3 特征归属 | ✅ 已答（插件内） | 架构铁律约束 |
| G2 ABI 方案 | ⏳ 待 `algo-structure-abi` | 输入约束已明确 |
| G3 broker 归属 | ✅ 确认 | `engine.cpp:233` |

⚠️ **G1-2 的许可问题不阻塞开发，但阻塞交付。** 建议并行推进法务确认。

## 回滚点

| 阶段 | 回滚动作 | 影响面 |
| --- | --- | --- |
| Wave 1 后 | 保留 v1 ABI 路径，v2 未启用 | 无（v1 插件继续工作） |
| Wave 2 后 | 移除新增 `MSG_*` 分支，保留 `MSG_FRAME_DISPATCH` | 帧派发不受影响 |
| Wave 3 后 | 前端叠加层加开关隐藏；算法包可卸载 | 既有算法包不受影响 |

## 范围外

同父 `prd.md`「范围外」章节。
