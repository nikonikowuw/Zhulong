# 迁移设计

## 迁移清单

下表是审计队列，不是对旧规范正确性的背书。Apply 阶段须为每一篇记录最终去向（并入规格、提炼规则、保留历史或不迁移）、源码/测试依据、冲突及取舍。规则说明何时触发与禁止什么，规格说明系统现行契约，同一约束不维护两份长文。

| 现有范围（相对于 `.trellis/spec/`） | 逐篇迁移候选 | 去向 |
| --- | --- | --- |
| 根（3） | `index.md`、`architecture.md`、`naming-guidelines.md` | 洛书索引、架构地图；命名约束按实际作用域分配，与通用建议区分 |
| `backend/`（主机与 HTTP，12） | `index.md`、`directory-structure.md`、`host-runtime-contract.md`、`dependency-injection.md`、`http-api-guidelines.md`、`authentication-guidelines.md`、`middleware-guidelines.md`、`error-handling.md`、`logging-guidelines.md`、`openapi-and-asyncjob-guidelines.md`、`quality-guidelines.md`、`alertpush-guidelines.md` | `domains/go-host-and-http.md`；候选规则：Fx 生命周期、鉴权与端点装配、错误/日志的危险边界 |
| `backend/`（数据，7） | `database-guidelines.md`、`record-guidelines.md`、`storage-guidelines.md`、`mediafile-guidelines.md`、`spatial-guidelines.md`、`task-guidelines.md`、`camera-guidelines.md` | `domains/data-and-storage.md`；摄像头采集协议等原生相关部分与媒体规格交叉索引；扩充 SQLite 规则 |
| `backend/`（其余，2） | `algorithm-guidelines.md`、`gb28181-guidelines.md` | 分别并入算法 SDK、原生媒体规格；GB28181 的 HTTP 边界索引回 Go 规格 |
| `native/`（8） | `index.md`、`directory-structure.md`、`cgo-contract.md`、`ingestion-contract.md`、`pipeline-guidelines.md`、`algo-plugin-contract.md`、`hardware-backends.md`、`host-portability.md` | 前五篇并入 `domains/native-media.md`，后三篇并入 `domains/algorithm-sdk-and-delivery.md`；提炼原生所有权、阻塞及 SDK/真机验收规则 |
| `frontend/`（5） | `index.md`、`directory-structure.md`、`development-guidelines.md`、`quality-guidelines.md`、`theme-and-i18n.md` | `domains/web-frontend.md`；提炼路由生成、特性边界、浏览器测试规则 |
| `guides/`（3） | `index.md`、`code-reuse-thinking-guide.md`、`cross-layer-thinking-guide.md` | 具体跨层契约并入架构和对应领域；通用方法论不重复迁入 |

洛书 CLI 已支持 `luoshu import --from trellis --dry-run`，预览确认它主要逐篇复制，并会跳过已存在的架构规格。本次不执行真实导入，以避免覆盖缺口、重复层级和继承历史漂移。

## 任务与切换

- 迁移 `11-12-stranger-cluster-trajectory` 和 `11-15-face-track-fusion-identity`：保留原始 PRD 与来源路径、创建日期、优先级、原始 `planning` 状态；目标变更有真实 `base_commit`，无虚假的任务完成或验收记录。为每个变更逐一构建上下文并验证。仅在登记这两个既有计划时，显式允许与迁移变更并行存在；不修改全局重复变更策略，也不把迁入变更当作立即开始实施。
- `11-17-person-attributes` 与 `11-19-face-attributes` 无任务元数据，记录为历史草案，不自动创建活跃变更。归档、日志与历史证据不复制进洛书现行规格。
- 先梳理受跟踪和本机实际使用的项目级 hooks、平台设置与文档引用，再切换。只停用已验证属于 Trellis 的项目级接入，不碰同文件内的用户/其他插件设置；第三方 README 内的历史测量引用允许保留，但不得被当作现行流程入口。
- 已知漂移（测试选择器、HTTP 解包说法、Swagger 白名单）只对现行规范作准确描述，不替该变更修代码。`make check` 之前止于浏览器测试；未取得端到端绿灯时保持 `gates: []` 并记录警告。

## 风险与审查重点（Review Focus）

1. 不能用五份新规格的数量代替覆盖率；必须能从任一旧规范找到处置依据，关键领域契约不能仅剩历史入口。
2. 不凭旧文档确定 API、常量、平台能力或测试可用性，优先对照真实实现、有代表性的测试和 Make 入口。
3. 明确区分停用工作流与删除历史；回滚可恢复入口配置，不需要还原旧任务数据。
4. bootstrap 文件当前尚未跟踪：独立 Verify 同时纳入相对 `base_commit` 的 diff 和相关未跟踪文件，不能仅依赖 `git diff`。
