# Verify Report — Trellis → 洛书（分层布局复验）

Verdict: FAIL

base_commit: 8c39ca452bea44fc8bd84419ecc5348297df2088

## 范围与门禁

- 起始 `luoshu verify run 2026-11-22-replace-trellis-with-luoshu --include-diff --json` 退出 0，`ready=true`；跟踪差异三份入口文档，另有 42 个未跟踪洛书文件（含本报告），不能仅审 CLI 的跟踪 diff。起始快照后出现第 43 个未跟踪文件，见阻塞发现。另查本机忽略的 DSH 指南及备份。
- [项目配置](<../../config.yaml#L26-L31>) 的 `operations.verify.gates: []`，未执行门禁，`gatesPassed=true` 不是产品测试通过。[台账](<migration-ledger.md#L77-L79>)记录 bootstrap `make check` 退出 2，止于 Vitest 模块加载和 iframe 错误。本轮未运行 `make check` 或其他产品检查，未联网、暂存、归档。

## 静态验收契约

| 项 | 结论 | 证据 |
| --- | --- | --- |
| V1 覆盖率 | PASS | 台账 40 路径与 `git ls-files .trellis/spec` 精确等集；总索引→架构/领域分索引→六份规格。抽检 Go 启停/鉴权（`internal/app/runtime.go:527-574`、`internal/app/router.go:134-200`）、SQLite（`internal/database/store.go:141-181`）、C ABI/流水线（`native/include/Zhulong/engine.h:265-325`、`native/src/pipeline/engine.cpp:307-344`）、算法交付（`internal/algorithm/verifier.go:362-382`）、API/路由（`web/src/lib/api-client.ts:71-84`、`web/src/routes/_authenticated/persons/index.tsx:1-8`）。核对 [数据规格](<../../specs/domains/data-and-storage.md#L21-L25>)和 [台账](<migration-ledger.md#L40>)已纳入已匹配/未匹配轨迹的原子检查和盖戳、冷却滞后抑制，以及人体/人脸撞名按字典序循环追加 `@face` 的确定行为，分别与 `internal/record/cooldown_keys.go:26-69`、`internal/record/cooldown.go:59-91`、`internal/record/service_build.go:282-347` 一致。 |
| V2 规则与规格 | 布局验证通过，后续快照无效 | 起始三个严格校验均退出 0，13 条规则和 8 个规格文件（含两个分索引），0 错误、0 告警。分层后 13 个代表路径分别命中预期 ID。审计退出 0，95 个引用无缺失，无空命中，只有预期的无门禁告警。额外规则最初以 `always_on` 出现，默认匹配无关根 README；其后改为 `glob` 后默认 README 探针恢复零命中。这些观察不等于稳定快照上的完整复验。 |
| V3 任务完整 | PASS | 两份 `source-prd.md` 与旧 PRD `cmp` 相同；两条规划 validate 退出 0，0/4 和 0/3 任务，只有两条规划与本迁移在变更清单。三个 Apply context 无 diagnostics/missingExplicitIds；规划实际加载各自 4/3 份规格，迁移加载架构并确认 `proposed_specs` 为五份存在的已批准域。旧 Trellis 跟踪文件基线 diff 为空。 |
| V4 工作流切换 | PASS | `AGENTS.md`、`README.md`、`native/README.md` 差异的导航改指洛书；`CLAUDE.md` 继续读 AGENTS。本机 `.dsh/DSH.md` 引导洛书；忽略的备份清单 31 项全部存在，其余 30 个不在原路径。无 `core.hooksPath`。Explore 得到分索引入口，且后端、原生、Web 代表路径分别命中 `go-host/host-lifecycle`、`native/native-pipeline-lifetimes`、`web/frontend-route-boundary`。 |
| V5 门禁与范围 | **FAIL** | 门禁留空符合约束；`git diff --check` 及基线 whitespace check 退出 0；无暂存改动，跟踪范围仍仅三份文档。不批准在快照后出现且变更的第 14 条规则为本次迁移内容；未跟踪范围无法对同一快照给出通过判定。 |

## Checklist 与阻塞发现

1. V1–V5 已逐项评估；严格检查通过不能抵消范围或快照失效。不更改静态验收契约。
2. 核查了跟踪/未跟踪 diff 清单与本机被忽略的接入、备份；无业务文件改动。快照在仓库外采集。被忽略的 DSH 文件、清单、备份项与静态验收契约另取哈希核验。
3. 初始 `matchedRules` 为 `native/native-link-and-abi`、`data/sqlite-persistence`、`go-host/http-contract`、`web/frontend-api-boundary`，`mountedSpecs` 为 `architecture/architecture.md`；五项批准域另查。三个文档未修改链接、持久化或接口实现。`modelDecisionCandidates` 为空，没有手动激活规则。

- [Blocker] **V5：验收快照之后增加了未纳入迁移范围的独立规则。** [Go 风格规则](<../../rules/go-host/code-style.md#L1-L5>)并非起始 13 条规则之一；其出现使路径总数由 45 变为 46，且在本轮期间从 `always_on` 改为 `glob`。`luoshu verify assert-unchanged` 退出 1，`clean=false`、`headChanged=false`，报告以外唯一差异为 `.luoshu/rules/go-host/code-style.md`；忽略的本机文件及静态契约哈希一致。验收代理未编辑该规则。请确认它归属于单独变更或扩大迁移范围；在工作区稳定后，对明确的范围重新做独立 Verify。归档前不能沿用本轮结果。
