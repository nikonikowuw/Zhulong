# Verify Contract: 2026-11-23-camera-alert-form-drawer-sheet

> Propose 阶段确立的静态验收契约。Verify 阶段仅对照本契约与 `base_commit` diff 输出结论到 `verify-report.md`，严禁修改本契约或业务源码。

## 验收标准

- [ ] V1（摄像头抽屉化与交互连续性）：
  - 点击“新增摄像机”或表格某行的“编辑”动作，右侧滑出抽屉面板，左侧列表内容保持可见；
  - 抽屉头部包含清晰的标题、说明与关闭按钮；中间表单支持独立垂直滚动；底部固定吸附“取消”与“确认添加/保存修改”按钮；
  - 在连通性探测（`isPending`）期间，按钮呈现加载转圈态并禁用，遮罩与 ESC 关闭受保护。
- [ ] V2（告警推送抽屉化与长表单流式体验）：
  - 点击“新建推送目标”或编辑已有目标，右侧滑出抽屉面板；
  - Webhook 与 MQTT 通道类型卡片切换流畅，Webhook Headers 列表增删在 100vh 抽屉内滚动自然，无横向溢出；
  - 底部操作栏常驻可见，提交成功后抽屉正常关闭并触发外部列表刷新。
- [ ] V3（前端类型与真实浏览器测试门禁）：
  - `pnpm type-check` 静态类型检查零错误；
  - 针对改动特性的真实无头浏览器单测全部通过（包含 `cameras-action-dialog.test.tsx` 与 `alert-destination-dialog.test.tsx`），未弱化或跳过断言；
  - 命中的工程规范（`web/frontend-api-boundary`、`web/frontend-browser-tests`、`web/frontend-route-boundary`）全部遵守。

## 负向边界（Explicit Negative Boundaries）

1. **接口契约不变量**：严禁修改 Go 后端 API 请求参数、字段类型或 HTTP 信封格式。
2. **表单验证规则不变量**：严禁放宽或改动 `createCameraFormSchema` 与告警推送相关的 Zod Schema 规则。
3. **作用域防护**：严禁改动 `channel-task-dialog.tsx`、`persons-action-dialog.tsx` 等作用域外的组件文件。
4. **测试无妥协**：严禁使用伪造的 fake test、跳过（`it.skip`）已有测试用例或改写测试期望以掩盖失败。

## 验证执行命令

```bash
# 1. 静态类型检查
cd web && pnpm type-check

# 2. 摄像头特性单测（真实 Chromium 环境）
cd web && pnpm test -- src/features/cameras/components/cameras-action-dialog.test.tsx

# 3. 告警推送特性单测（真实 Chromium 环境）
cd web && pnpm test -- src/features/alert-push/components/alert-destination-dialog.test.tsx

# 4. 特性全量测试
cd web && pnpm test -- src/features/cameras/ src/features/alert-push/
```
