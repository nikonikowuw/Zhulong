# 实施计划：双轨联调（D · 门禁与实测）

> 对应父任务 `implement.md` Phase 5。前置：A/B/C 已合入。

- [ ] **5.1 全量门禁**：`make check`、`make cross-board`；
- [ ] **5.2 内嵌 SPA 新鲜度门禁**：新增 `webui-dist-check`（文件列表 + SHA-256，不引入 Node 依赖），`cross` / `cross-board` 依赖它；
- [ ] **5.3 同步实测**：按 design §6.3 执行，结果写 `research/sync-measurement.md`（含原始帧序列与统计）；
- [ ] **5.4 板端压测**：RK3568 16 路 30 分钟、其中 1~4 路音频通道处于收听，与“完全不收听”基线对照，结果写 `research/board-soak.md`；
- [ ] **5.5 降级与回滚演练**：无音频相机、`audio-latm`、`audio-broken`、旧前端缓存 bundle、新前端遇旧后端、同会话接管、双会话并行，记录写 `research/rollback-drill.md`；
- [ ] **5.6 文档与 spec**：`config.toml` 注释、README、`.trellis/spec/backend/camera-guidelines.md §5`、`.trellis/spec/native/ingestion-contract.md §3.5`、`.trellis/spec/native/cgo-contract.md`、`.trellis/spec/frontend/*`、`internal/apidocs`。

## 验证

```bash
make check
make cross-board
# 板端与 docker 相关门禁不进 make check，单独执行：
make board-check
```

## 完成判据

- 父任务 AC-5 / AC-6 全部有证据文件；
- 三份 research 记录可被第三人复现（命令、配置、版本、原始数据）；
- spec 与实现一致，无“文档说双轨合流、代码走独立通道”这类漂移。
