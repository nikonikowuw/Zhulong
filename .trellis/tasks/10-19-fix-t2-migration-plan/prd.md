# PRD: 修正 T2 通道多算法重构的迁移规划缺陷

## Goal

把 `10-18-channel-multi-algo-task`（下称 T2）的规划修到**可执行**：对齐迁移编号、修正其 `000013` 迁移方案的实测缺陷、补上 `region_config_json` 的数据搬运、登记 T1 消费者的交接点。

**本任务只改规划文档，不写业务代码。** T2 的主体实现是独立的大重构，不在本次范围。

---

## Background：已确认的仓库事实

| 编号 | 事实 | 证据 |
| :--- | :--- | :--- |
| F1 | T2 的 `prd.md:29` 与 `implement.md:6,11,71` 仍写 `000012_rebuild_channel_tasks`，而 `000012` **已被 T1 占用** | `internal/database/migrations/000012_add_region_config_to_analysis_tasks.{up,down}.sql` 已存在；`design.md:16,55,188` 已在 `4a902bc` 改为 `000013`，但 prd/implement 漏改 |
| F2 | `golang-migrate` 的 `iofs` 源对同版本号返回 `source.ErrDuplicateMigration` | `migrate/v4@v4.20.1/source/iofs/iofs.go:69` |
| F3 | T2 `design.md §2.2` 的 up 步骤（建子表 → 插新主表 → **插子表**）在 FK 下**直接失败** | 实测复现：`FOREIGN KEY constraint failed`，因为 `task_id='task_cam_1'` 指向 `analysis_tasks_new`，而 FK 声明指向 `analysis_tasks` |
| F4 | 若把顺序调成「先 `DROP TABLE analysis_tasks` 再 `RENAME`」，`ON DELETE CASCADE` 会**清空刚插入的子表行** | 实测复现：DROP 后子表行数 0 |
| F5 | `PRAGMA foreign_keys = OFF` **在事务内无效**（golang-migrate 每条迁移包在事务里执行） | `000009_decouple_person_id.up.sql:11-12` 已有明文注释；本次实测二次确认 |
| F6 | T2 `design.md:59` 用 `'task_' \|\| camera_id` 拼主键，**违反本仓库明文规范** | `.trellis/spec/backend/database-guidelines.md:80`「严禁伪语义拼接复合主键……严禁拼接形如 `task_{cam}_{algo}` 的超长混合串」 |
| F7 | 迁移 SQL 内**无法生成 ULID**（Crockford Base32 无对应 SQL 函数）；但旧表 `analysis_tasks.id` 本身就是 ULID | `internal/task/service.go:86` 用 `idutil.New()`；迁移文件里无任何 id 生成先例（`grep randomblob/hex/lower` 无结果） |
| F8 | T2 的 `prd.md:32` 把子表 `region_config_json` 标为「**预留**」，且 `design.md §2.2` 的迁移步骤**未提及搬运该列** —— 而 T1 已交付真实数据 | T1 `internal/task/model.go:29` 的列已上线；`region_config.go:78` 有真实消费者 |
| F9 | T2 规划**完全没提** `RegionConfigCache` 这个消费者 | `grep -rn "RegionConfigCache\|ConfigFor\|regionProvider" .trellis/tasks/10-18-channel-multi-algo-task/` 无输出 |
| F10 | T1 的 `RegionConfigCache.Reload()` 依赖 `ListTasks()` 返回带 `CameraID` / `AlgorithmID` / `RegionConfigJSON` 三字段的 `Task` | `internal/task/region_config.go:62-95`；`internal/task/store.go:200-210` 目前是单表 `Find` |
| F11 | T1 的迁移测试 `TestRegionConfigMigrationUpAndDownSymmetry` 硬断言列在**主表**上 | `internal/database/store_test.go:846`，断言 `columnExists("analysis_tasks", "region_config_json")` 且 `Migrate(12)` |
| F12 | 算法卸载清理逻辑写死主表 | `internal/algorithm/store.go:188`：`DELETE FROM analysis_tasks WHERE algorithm_id = ?`，T2 后该列不在主表 |

### 已验证的正确迁移方案（实测通过）

up 顺序：`备份 temp_old_tasks` → 建子表 + 新主表 → 迁移主表（聚合根 id 取 `MIN(id)` 复用既有 ULID）→ `DROP` 旧主表 → `RENAME` → **再插子表**（从备份 JOIN 新主表）→ 删备份。

实测结果：3 条旧记录 → 2 主 + 3 子，`region_config_json` 全部保留，`PRAGMA foreign_key_check` 无违规；down 平铺还原后 `enabled = t.enabled AND ta.enabled` 正确（任务停用的子项保持 0）；算法卸载时子表 FK CASCADE 自动清理且空算法列表的通道任务保留。

---

## Requirements

### R1. 迁移编号三方对齐

- 把 T2 的 `prd.md`、`implement.md` 中的 `000012_rebuild_channel_tasks` 全部改为 `000013_rebuild_channel_tasks`，与 `design.md` 一致。
- 同时在 T2 的 `implement.md` 或 `design.md` 中显式记录「`000012` 已被 T1 占用，本任务必须用 `000013`」，防止后续再次漂移。

### R2. 修正 up/down 迁移步骤

- 按 F3/F4/F5 的实测结论重写 `design.md §2.2` 的步骤，明确：**必须先 `DROP` 旧主表并 `RENAME` 新主表，子表的 `INSERT` 才能执行**；且需先备份旧表（子表插入要读旧数据，而旧表已被 DROP）。
- 明确禁止依赖 `PRAGMA foreign_keys = OFF`，并引用 `000009` 的先例作为依据。

### R3. 聚合根主键改为复用既有 ULID

- 把 `'task_' || camera_id` 替换为「取该相机最早那条旧任务的 `id`（`MIN(id)`）」，理由：既满足 `database-guidelines.md` 的主键规范，又避免在 SQL 中生成 ULID（迁移内无此能力）。
- 在 `design.md` 中记录该决策的取舍（代价：聚合根 id 与旧任务 id 存在语义重叠，需在实现时确认无外部引用旧 id）。

### R4. 补 `region_config_json` 的数据搬运

- 修正 T2 `prd.md:32` 的「预留」表述 —— 该列**已由 T1 交付并有真实数据**，不是预留字段。
- 在 `design.md §2.2` 的 up 步骤中加入该列的搬运（旧主表 → 新子表）；down 步骤同理（子表 → 旧主表）。
- 补一条验收：升级后既有 ROI/MASK 配置零丢失。

### R5. 登记 T1 侧交接点

在 T2 的 `design.md`（或新建交接清单）中明确列出 T2 落地时**必须同步修改**的 T1 产物：

| 交接点 | 位置 | T2 需做的改动 |
| :--- | :--- | :--- |
| `RegionConfigCache.Reload` | `internal/task/region_config.go:62-95` | 改为从主+子表 JOIN 取 `(cameraID, algorithmID, regionConfigJSON)`，否则区域过滤静默失效 |
| `ListTasks` 返回结构 | `internal/task/store.go:200-210` | 需带上 `Algorithms` 子项，或另提供扁平查询供缓存使用 |
| 迁移测试断言 | `internal/database/store_test.go:846` | 断言列从主表移到子表；`Migrate(12)` 需改为 `Migrate(13)` |
| 算法卸载清理 | `internal/algorithm/store.go:188` | 改删 `task_algorithms` 子表 |

### R6. 复核 T2 的其它既有表述

- 检查 T2 的 `prd.md` / `design.md` / `implement.md` 中是否还有其它与现状不符的断言（如 `000012` 之外的编号、已变化的文件路径），一并修正或标注。

---

## Acceptance Criteria

- [ ] **AC1**：T2 三个文档（`prd.md` / `design.md` / `implement.md`）中的迁移编号全部为 `000013`，无残留 `000012_rebuild_channel_tasks`。
- [ ] **AC2**：T2 `design.md §2.2` 的 up 步骤顺序与 F3/F4 的实测结论一致（子表 `INSERT` 在 `DROP`+`RENAME` 之后），且含「先备份旧表」步骤；down 步骤同样可执行。
- [ ] **AC3**：`task_` || `camera_id` 的拼接写法已全部移除，替换为复用既有 ULID 的方案并附决策理由。
- [ ] **AC4**：`region_config_json` 在 T2 文档中不再被称为「预留」，且 up/down 迁移步骤均含该列搬运，并有一条「既有 ROI/MASK 配置零丢失」的验收项。
- [ ] **AC5**：R5 表格（四个交接点，含具体文件与行号）已写入 T2 文档。
- [ ] **AC6**：本任务不修改任何业务代码（`git diff` 仅涉及 `.trellis/tasks/**`）。
- [ ] **AC7**：`make check` 保持全绿（本任务不应引入任何代码回归）。

---

## Out of Scope

- **T2 主体实现**（DB 迁移 + Go 聚合根重构 + 算力事务 + REST 契约升级 + 前端废除 diff）：独立大重构，另立任务。
- **T1 自身的归档**：T1 仍因 AC4/AC5/AC9 的真机真流验收保持 `in_progress`，本任务不动它。
- **修改 `000012` 迁移本体**：已提交且已有测试守护，按 `database-guidelines.md` 只追加不改历史迁移。
