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

- **G1-1 目标平台**：（待填）
- **G1-2 人脸能力来源**：（待填）
- **G1-3 特征提取归属**：（待填）
- **G2 ABI 方案**：（待填）

## 回滚点

| 阶段 | 回滚动作 | 影响面 |
| --- | --- | --- |
| Wave 1 后 | 保留 v1 ABI 路径，v2 未启用 | 无（v1 插件继续工作） |
| Wave 2 后 | 移除新增 `MSG_*` 分支，保留 `MSG_FRAME_DISPATCH` | 帧派发不受影响 |
| Wave 3 后 | 前端叠加层加开关隐藏；算法包可卸载 | 既有算法包不受影响 |

## 范围外

同父 `prd.md`「范围外」章节。
