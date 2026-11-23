# Verify Contract: 2026-11-23-channel-task-fullscreen-workbench

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit` diff 输出结论到 `verify-report.md`，严禁修改本契约或业务源码。

## 验收标准

- [ ] V1（全屏工作台视觉与布局契约）：
  - 打开通道任务配置时，工作台通过 Portal 挂载并占满整个视口（`fixed inset-0 h-dvh w-screen`，边界矩形尺寸等于 `window.innerWidth × window.innerHeight`），无居中弹窗的圆角边框与外围半透明遮罩留白；
  - 顶部工作台栏（`<header>`）与底部操作栏（`<footer>`）始终固定可见，中间左右双栏在桌面端无外层滚动条冲突，无横向溢出。
- [ ] V2（无障碍语义、滚动锁定与快捷键隔离）：
  - 工作台根元素具备 `role="dialog"`、`aria-modal="true"` 及 `aria-labelledby` 关联；
  - 打开期间 `document.body.style.overflow` 锁定为 `hidden`，关闭或组件卸载后完整恢复原值；
  - 非保存中按 `Escape` 可正常关闭工作台；焦点在输入控件（`input`、`textarea` 等）或保存进行中时，不发生误关闭；视频暂停快捷键（`Space`）与既有 ROI 绘制行为保持完好。
- [ ] V3（既有业务与真实浏览器测试通过）：
  - `channel-task-dialog.test.tsx` 中所有既有算法选择、帧率与算力预算校验、ROI/绊线编辑、保存同步、错误处理测试全部通过，无跳过或弱化断言；
  - `tasks` 特性全量测试与 `pnpm type-check` 全部通过。

## 负向边界（Explicit Negative Boundaries）

1. **路由边界不变**：严禁新增或修改 `web/src/routes/**` 与 `web/src/routeTree.gen.ts`。
2. **业务与后端契约不变**：严禁修改任务同步 API、算力预算算法、ROI 数据结构与后端 Go 代码。
3. **作用域隔离**：严禁修改 `tasks` 特性之外的其他业务模块（如 `cameras`、`persons`、`spatial` 等）。
4. **测试无妥协**：严禁跳过（`it.skip`）、删减或弱化已有测试用例断言。

## 验证执行命令

```bash
# 1. 静态类型检查
cd web && pnpm type-check

# 2. 通道任务工作台真实浏览器测试
cd web && pnpm test -- src/features/tasks/components/channel-task-dialog.test.tsx

# 3. 任务特性全量前端测试
cd web && pnpm test -- src/features/tasks/
```
