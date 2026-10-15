
---

## 2026-10-15: 10-15-real-person-face-structure 真实人脸人体结构化算法包

### Summary & Retrospective

完成了真实人脸人体结构化算法包流水线（`packages/face_recognition/`）的完整实现与集成：
1. **真实目标检测器 (`detector.hpp` / `detector.cpp`)**：
   - 替换原硬编码固定坐标基线，基于输入图像像素真实前向推理检出人脸位置框与 5 点关键点；
   - 关联人体解剖结构生成对应人体检测框 `body_box`，严格归一化到 $[0.0, 1.0]$；
   - 严守负样本准入契约，全黑或全零像素帧精准返回空结果，避免假阳性干扰。
2. **Umeyama 仿射姿态对齐与双线性重采样 (`aligner.hpp` / `aligner.cpp`)**：
   - 消费 5 点关键点通过 Umeyama 算法求解至 ArcFace 标准 $112 \times 112$ 模板的最优相似性仿射变换矩阵；
   - 实现纯 C++ 无外部图像库依赖的双线性插值逆映射采样（Bilinear Resampling），生成标准化正脸 Patch。
3. **512 维特征提取与 1:N 底库比对 (`extractor.hpp` / `extractor.cpp` + `matcher.hpp`)**：
   - 从对齐后的正脸 Patch 抽取 512 维高判别力特征向量，严格执行 L2 范数单位归一化；
   - 特征度量空间与引擎端 `Zhulong_engine_extract_face_feature` 保持同构映射；
   - 消费 `zhulong::runtime::GalleryRegistry` 动态底库，基于余弦距离完成 Top-K 检索与相似度计算。
4. **C ABI 结构化装配与验证**：
   - 组装完整的 `zhulong_person_item` 嵌套 `zhulong_face_item` 树形结构，单块 Arena 内存无泄漏释放；
   - 原生 CTest 18/18 100% 绿灯，`zhulong_algo_runner --verify-plugin` 黑盒准入验包通过；
   - `build_package.py` 自动化打出薄包并通过 Go `TestFaceRecognitionPackageVerification` 离线验包测试。

### Verification Status

- [OK] 原生 CTest 18/18 全部通过（包含新增的真实检测器、对齐采样器、特征提取与端到端匹配测试）；
- [OK] `zhulong_algo_runner --verify-plugin` 黑盒验包通过，`result_type: 3`，`count: 1`；
- [OK] `packages/face_recognition/scripts/build_package.py` 生成薄包并通过 Go 离线准入校验；
- [OK] `make check`（前端 59 套件 250 测试 + Native 18/18 + Go vet/test -race + RTSP 桥接测试）全量绿灯。

---

## 2026-10-15: 10-15-decouple-external-person-id 人员工号与内部主键解耦

### Summary & Retrospective

彻底解耦了人员实体的外部业务工号（`person_id`）与内部系统主键（`id`），理清了代理主键与外部对接业务键的概念边界：
1. **数据库版本化迁移 (`000009_decouple_person_id`)**：
   - 增加 SQLite 迁移解除 `person_id` 的 `NOT NULL` 与 `CHECK` 强约束，支持 `NULL` 值；
   - 建立条件唯一索引 `CREATE UNIQUE INDEX idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL`；
   - 迁移脚本在重建表时安全备份并恢复人脸底库（防止外键 `CASCADE DELETE` 误删），并自动清洗历史上 `person_id = id` 的 26 位随机假工号为 `NULL`；
   - 完备支持 `up` 与 `down` 双向可逆回滚。
2. **Go 后端领域服务与防爆重构 (`internal/person`)**：
   - `Person.PersonID` 改为 `*string` 精准表达可空状态；`CreatePerson` 彻底移除空白工号自动回退为 26 位随机主键的逻辑；
   - `UpdatePerson` 放开生命周期限制：允许对未绑定工号的人员补录外部工号，一旦绑定依然受 `PERSON_ID_IMMUTABLE` 保护；
   - `Store.UpdatePerson` 使用 SQLite 表达式保护存储层不可篡改，并支持从 NULL 绑定；
   - `DeletePerson` 改为按各人脸模板 `FaceID` 逐一向 C++ 原生引擎广播注销，彻底杜绝无工号人员删除时底库人脸泄露的隐患。
3. **React 前端与交互优化 (`web/src/features/persons` & `live`)**：
   - Zod Schema 与类型定义适配 `personId` 可空；
   - 人员列表表格单元格在工号未分配时规范展示为 `-`，不再显示外星乱码；
   - 编辑弹窗动态感知工号绑定状态：未绑定时允许输入补录，已绑定时保持只读锁定与说明提示；
   - 实时抓拍流冷却去重优化：支持以 `cameraId:personId||personName` 作为去重键，防止不同无工号目标共享同一冷却槽。
4. **全量流水线质量验证**：
   - 前端 59 个测试套件、255 个测试用例全部通过；
   - 原生 Native 18/18 测试通过；
   - 后端数据库、领域模型、算法与应用程序 `go test -race` 100% 绿灯；
   - `make check` 全项通过。



## Session 42: Decouple external person_id from internal row id
<!-- trellis-session: v=2 fp=b7c72c5e3a4a39c8 -->

**Date**: 2026-10-15
**Task**: Decouple external person_id from internal row id
**Package**: backend
**Branch**: `dev`

### Summary

Decoupled external business key person_id from internal row id across SQLite schema (000009 migration), Go person domain, native gallery sync/cleanup by face ID, and React frontend

### Git Commits

| Hash | Message |
|------|---------|
| `0e1ed28` | feat(person): decouple external person_id from internal row id across schema, backend and frontend |

### Status

[OK] **Completed**

---

## 2026-10-15: 10-15-unify-primary-key-ulid 统一实体主键为 ULID

### Summary & Retrospective

在零 DDL Schema 变更的前提下，将系统核心动态业务实体的代理主键（`persons.id`、`person_faces.id`、`cameras.id`、`analysis_tasks.id`）统一采用纯 ULID（26 字符 Crockford's Base32）：
1. **公共工具库 (`internal/idutil`)**：
   - 引入 `github.com/oklog/ulid/v2`，封装线程安全且毫秒单调的 `New()`、严格校验的 `IsValid()` 与时间反解 `Time()`；
   - 单元测试覆盖高频单调性、`-race` 竞态安全、格式合法性边界与时间解析。
2. **人员与人脸域 (`internal/person`)**：
   - `newID()` 替换为 `idutil.New()`，内部行主键获得时间单调递增性与 B-Tree 插入局部性；
   - 严格守卫 `person_id`（外部业务键）可选可空且不被代理键回填的解耦契约；
   - 保持 `{owner_row_id}_{face_id}.ext` 磁盘相对路径与特征向量存储契约。
3. **摄像头域 (`internal/camera`)**：
   - 缺省 ID 改为直接生成 ULID，删除 `GetNextNumericID` 全表扫描及对应的应用层互斥锁 `createMu`；
   - 消除并发创建阻塞与数字字典序排序隐患；客户端自定义 ID 依然兼容支持。
4. **算法分析任务域 (`internal/algorithm`)**：
   - 缺省任务 ID 改为直接生成 ULID，彻底废弃 `task_{cam}_{algo}` 冗长伪语义拼接。
5. **规约与全量验证**：
   - 更新 `.trellis/spec/backend/database-guidelines.md` 确立主键规约；
   - `make check`（前端 59 套件 255 测试、Native 18/18、Go vet/test -race 与 RTSP 桥接测试）100% 绿灯。

### Verification Status

- [OK] `go test -race ./internal/idutil/...` 单测与并发安全通过；
- [OK] `internal/person`、`internal/camera`、`internal/algorithm` 关联测试通过；
- [OK] `make check` 全量流水线检查全绿通过。

## Session 43: Unify entity primary key generation with ULID
<!-- trellis-session: v=2 fp=04d1e2f3a5b6c7d8 -->

**Date**: 2026-10-15
**Task**: Unify entity primary key generation with ULID
**Package**: backend
**Branch**: `dev`

### Summary

Unified dynamic entity surrogate primary keys (persons.id, person_faces.id, cameras.id, analysis_tasks.id) to pure monotonic ULIDs using internal/idutil wrapping oklog/ulid/v2. Eliminated table-scanning GetNextNumericID and Go mutex lock contention in camera.Service. Maintained external business key persons.person_id optional/nullable decoupling and preserved {owner_row_id}_{face_id}.ext file naming convention.

### Git Commits

| Hash | Message |
|------|---------|
| `fe14451` | feat(core): unify entity primary key generation with ULID |

### Status

[OK] **Completed**

## Session 44: Normalize systemtime and storage audit actions to domain namespace
<!-- trellis-session: v=2 fp=056b93081e7d2c3b -->

**Date**: 2026-10-15
**Task**: 统一审计动作命名空间并迁移历史记录
**Package**: backend
**Branch**: `dev`

### Summary

Normalized audit action constants across `internal/systemtime` (`ActionTime*`) and `internal/storage` (`ActionStorage*`) to eliminate redundant `system.` prefixes, aligning strictly with standard domain namespacing `<domain>.<verb>` (`time.*`, `storage.*`). Created migration `000010_normalize_audit_actions` (`up.sql`/`down.sql`) to safely rewrite historical SQLite audit logs and verified bidirectional rollback in `store_test.go`. Aligned frontend i18n dictionaries (`zh-Hans.json`, `en.json`, `zh-Hant.json`), `AuditTable.tsx` filter options, and updated `.trellis/spec/backend/logging-guidelines.md`. Passed full `make check` suite.

### Git Commits

| Hash | Message |
|------|---------|
| `056b930` | feat(audit): normalize systemtime and storage action names to domain namespace |

### Status

[OK] **Completed**

