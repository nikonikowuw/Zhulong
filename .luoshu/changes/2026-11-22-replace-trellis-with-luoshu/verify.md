# 静态验收契约：洛书替代 Trellis

Verify 以独立上下文对照本契约和 `base_commit` 的完整变更范围（含尚未跟踪的洛书交付文件），只写 `verify-report.md`。

- [ ] V1 覆盖率：以 `git ls-files .trellis/spec` 为来源逐篇核对迁移台账；40 篇均有明确处置，不迁的契约须说明重复、过时或只适用于历史的理由。抽查迁入的 Go 启停和鉴权、SQLite、C ABI/流水线、算法交付、Web API/路由契约均有可定位证据，规格导航不依靠旧 Trellis 规范。
- [ ] V2 规则与规格：`luoshu config validate --strict`、`luoshu rules validate --strict`、`luoshu specs validate --strict` 通过。用 `luoshu rules match --path` 逐层验证代表路径能命中预期规则，且无关文档不命中工程规则。`luoshu audit --run-gates --max-files 100000` 无错误、无空命中、无断链；仅允许在没有已验证全量门禁时出现相应告警，必须如实记录。
- [ ] V3 任务完整：恰有两个原 planning 任务迁入，原始 PRD 和状态可追溯，两个目标均通过 `luoshu changes validate`；两组未登记草案没有误启动。旧归档、草案和日志没有删除或被标记成已迁的现行变更。
- [ ] V4 工作流切换：仓库入口与确认存在的项目级集成不再要求运行 Trellis；显式历史引用可以保留。对至少后端、原生、前端三类代表文件运行 `luoshu context build --stage explore --path`，确认可找到新规则和规格索引。
- [ ] V5 门禁与范围：若配置 `make check`，需有完整通过证据；否则保留空配置并说明限制。不修改业务代码、测试、构建脚本，不停用仓库外的 Trellis，不删除旧档案。检查 `git diff --check` 并人工审阅未跟踪文件，确认无额外改动。
