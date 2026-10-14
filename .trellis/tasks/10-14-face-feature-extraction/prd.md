# 实现人脸特征提取闭环

## Goal

替换底库中**全零占位**的人脸特征向量，接入真实特征提取，使 `person_faces.feature_vector` 携带可用于 1:N 比对的向量；并补齐删除人脸时特征向量的同步清理。

## 父任务

`10-14-face-recognition-e2e`（人脸识别端到端接入）

## 依赖

- **前置**：父任务「待决问题 2/3」已回答（人脸能力来源、特征提取归属）。
  - ⚠️ 若归属为**插件内提取**，则本任务的范围收缩为「底库侧不再生成占位特征 + 维度协商」，
    真正的提取逻辑移入 `10-14-face-algo-package`，需重新评审本任务范围。
- **可并行**：与 `10-14-gallery-hot-sync`、`10-14-inference-result-egress` 并行。

## 问题证据

```go
// internal/person/service.go:217
feat := make([]byte, 2048)   // ← 全零，未经任何特征提取
```

```go
// internal/person/service.go:154
// TODO: 同步删除Engine中的人脸特征向量
```

⚠️ **底库表里有记录，特征是 2048 个 0**。即使 gallery 通道与结果出口全部打通，1:N 也匹配不出任何人。

## Requirements

- **R1 归属定案**：在 `design.md` 中定案特征提取归属（插件内 / 宿主内），
  并说明该选择对架构约定的影响。
  ⚠️ 若选宿主内：会打破「`internal/engine` 是唯一可 `import "C"` 的包」这条约定，必须明确论证与替代方案。
- **R2 真实特征写入**：`AddFaceWithReader` 写入真实特征向量，替换占位。
- **R3 维度协商**：特征维度**由算法包 manifest 声明**（父 `design.md` D5），**不硬编码 2048**。
  维度不符时**显式失败**（而非静默写入长度错误的 BLOB）。
- **R4 失败路径**：图片中**检测不到人脸**时的行为必须明确（拒绝登记 + 明确错误码 vs 登记但标记无特征），
  并在 `design.md` 中论证。禁止静默写入零向量。
- **R5 质量门槛**：是否设置人脸质量下限（模糊/侧脸/过小拒绝）需定案；
  若设置，阈值必须可配置且**记录在案**。
- **R6 删除同步**：补齐 `DeleteFace` 中特征向量的同步清理 TODO。
- **R7 存储契约**：`Face.FeatureVector []byte` 是 DB 实体字段（映射 SQLite 动态 BLOB），
  支持任意维度，**不强制改名**；序列化为 IPC 载荷时才映射为 `features`。
  ⚠️ 维度与字节序约定须写入 `.trellis/spec/backend/storage-guidelines.md`。
- **R8 兼容既有数据**：已存在的全零特征记录如何处理（迁移 / 标记为无效 / 忽略）需定案。
- **R9 错误码与 i18n**：新增错误码遵循 `internal/httputil/locale.go` 的三语契约。

## Acceptance Criteria

- [ ] `AddFaceWithReader` 写入的特征向量**非常量**，长度等于所选模型的维度。
- [ ] **同人不同照**的 cosine 相似度高于阈值；**不同人**低于阈值。此测试必须有真实数据支撑。
- [ ] 维度不匹配是**显式错误**，有测试覆盖。
- [ ] 检测不到人脸时的行为有测试，且**不会写入零向量**。
- [ ] `DeleteFace` 后特征同步清理（若归属在宿主）；否则 TODO 有明确说明与指向。
- [ ] 既有底库数据（全零特征）的处理有明确策略与测试。
- [ ] 维度与字节序约定写入 `.trellis/spec/backend/storage-guidelines.md`。
- [ ] `make go-check` 通过（含 `-race`）。

## 范围外

- 特征提取模型的训练/转换/量化流程（若归属插件内，属 `10-14-face-algo-package`）。
- 1:N 检索算法本身（在算法侧）。
- 底库加密与合规审查。
