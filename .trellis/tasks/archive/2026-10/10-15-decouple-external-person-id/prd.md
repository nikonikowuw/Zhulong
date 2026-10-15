# 需求文档 (PRD): 人员工号与内部主键解耦 (Decouple external person_id from internal row id)

## 1. 背景与目标

### 1.1 背景
在当前架构中，人员实体（`Person`）的外部业务工号（`person_id` / `personId`）在数据库层被约束为 `NOT NULL CHECK (person_id <> '')`。
当用户在前端新建人员时若不提供工号（留空），后端 `CreatePerson` 会自动将其内部随机主键（`id`，Go 1.24 `crypto/rand.Text()` 生成的 26 位 Base32 随机串）回退赋给 `person_id`。
导致：
1. 界面呈现无业务含义的 26 位乱码（如 `PBPMXGURCSF3LM5ZEROITAGUOT`），极大损害前端人机交互与可读性；
2. 将仅在“与外部业务平台对接”时才需要的可选业务键，强行升格为系统必填项，混淆了内部代理主键（Surrogate Key）与外部业务键（Business Key）的概念边界；
3. 后端锁死 `PERSON_ID_IMMUTABLE`，导致未初始指定工号的人员在后续需要对接第三方系统时无法补录。

### 1.2 目标
1. **概念归位**：将 `person_id` 纯化为“可选的外部对接业务键”，解除与内部主键 `id` 的混淆与强制回退；
2. **Schema 演进**：新增版本化 SQLite 迁移 `000009_decouple_person_id`，将 `person_id` 改为可空（`NULL`），并建立条件唯一索引；
3. **清洗历史**：迁移时将历史上 `person_id = id` 的假工号脏数据自动清洗为 `NULL`；
4. **生命周期支持**：支持人员外部工号从空值补录绑定；
5. **全栈跨层一致**：确保 Go 服务、C++ 引擎底库同步注销（按 `FaceID` 原子清理，杜绝无工号人员底库泄漏）、React 前端展示与编辑全面对齐。

---

## 2. 功能性需求 (Requirements)

### R1. 持久化与数据迁移
- **R1.1 SQLite Schema 迁移**：
  - 新增 `internal/database/migrations/000009_decouple_person_id.up.sql` 和 `.down.sql`；
  - `persons.person_id` 改为允许 `NULL`；
  - 建立条件唯一索引：`CREATE UNIQUE INDEX idx_persons_person_id ON persons(person_id) WHERE person_id IS NOT NULL;`；
- **R1.2 历史数据清洗**：
  - 迁移执行时，对已有数据执行 `UPDATE persons SET person_id = NULL WHERE person_id = id;`，平滑修复历史伪数据。

### R2. 后端领域层与 API (`internal/person`)
- **R2.1 创建人员解耦**：
  - `CreatePerson` 时，若未提供 `personId` 或仅有空白字符，持久化为 `NULL`（Go 中映射为 `nil` 或空字符串，不回退为 `id`）；
  - 若提供了 `personId`，进行 Trim，且仅在非空时校验唯一性。
- **R2.2 工号补录支持**：
  - `UpdatePerson` 时，若当前人员未绑定工号（`person_id` 为空/NULL），允许通过传入新 `personId` 绑定；
  - 若当前人员已有工号且传入不同工号，返回冲突 `PERSON_ID_IMMUTABLE`（或依据策略处理）。
- **R2.3 列表与搜索**：
  - `ListPersons` 关键字搜索适配 `person_id IS NULL` 场景，正常支持姓名和工号检索。

### R3. 底库同步与原生引擎联动 (`internal/algorithm` & `native/`)
- **R3.1 底库原子注销**：
  - 在 `DeletePerson` 时，确保按该人员关联的各个人脸模板 `Face.ID` 向 C++ 原生引擎广播 `BroadcastDelete(ctx, f.ID, "")`；
  - 不再依赖 `p.PersonID` 作为唯一的清理依据，彻底避免无工号人员删除时底库人脸无法清退的泄漏缺陷。
- **R3.2 识别结果语义规范**：
  - 当人脸命中未配置工号的人员时，C++ 引擎及 AI 结果事件的 `person_id` 规范返回空字符串 `""`；前端根据 `person_name` 展示识别结果。

### R4. React 前端交互 (`web/src/features/persons` & `live`)
- **R4.1 表格列清晰展示**：
  - 人员列表的“工号”列，若 `personId` 为空/不存在，展示为 `-`，不再出现乱码随机串；
- **R4.2 新增与编辑弹窗**：
  - 新增时说明“选填，用于对接外部业务系统”；
  - 编辑时：若尚未绑定工号，输入框保持可编辑状态，允许补录；若已绑定工号，输入框禁用并提示不可变。
- **R4.3 视频流与抓拍防抖**：
  - 实时抓拍记录去重逻辑兼容 `person_id` 为空场景，改用 `cameraId:personId||personName` 进行防抖。

---

## 3. 非功能性需求与约束 (Non-Functional Constraints)

- **不可破坏现有迁移**：严格遵循规范，不篡改 `000001`~`000008` 任何既有迁移文件。
- **并发与数据竞争**：Go 测试运行 `go test -race` 必须 100% 绿色无竞争。
- **构建与测试管道**：Native 部分走 `native-test`，前端通过 `pnpm test`。

---

## 4. 验收标准 (Acceptance Criteria)

- [x] **AC-1**：运行 `000009` 迁移后，`persons` 表支持插入 `person_id` 为 `NULL` 的记录；插入重复非空 `person_id` 触发唯一冲突，插入多个 `NULL` 不冲突。
- [x] **AC-2**：历史数据中 `person_id = id` 的记录在迁移后成功置为 `NULL`。
- [x] **AC-3**：调用 `POST /api/v1/persons` 不传 `personId` 时，返回的 `personId` 为空（或 null），不再生成 26 位随机串。
- [x] **AC-4**：对未绑定工号的人员调用 `PATCH /api/v1/persons/{id}` 传入 `personId: "EMP-001"`，成功绑定工号；再次修改为其他工号报 `PERSON_ID_IMMUTABLE`。
- [x] **AC-5**：删除无工号人员时，C++ 原生引擎底库中的人脸特征模板被正确清空，内存底库无泄漏。
- [x] **AC-6**：Web 端人员列表对于未配置工号的人员展示 `-`；实时抓拍流正常识别并显示姓名。
- [x] **AC-7**：`make check` 全项通过（Go 检查与测试、原生测试、前端测试）。
