# 感知记录多维批量与多选导出 — 实施计划 (Implement)

## 任务拆解与执行阶段

### 阶段 1：基础工具与前端流水线库接入 (Foundation & Libraries)
- [ ] 检查并引入轻量无依赖的客户端 ZIP 压缩工具 `fflate`（在 `web/package.json` 中配置，体积极小 ~8KB）；
- [ ] 编写 CSV 序列化与 BOM 注入工具 `web/src/features/records/lib/export-csv.ts`；
- [ ] 编写 `export-csv.test.ts` 验证中文字符转义、BOM 头部、以及各维度字段映射的正确性；
- [ ] 编写离屏 Canvas 检测框与标签烧录引擎 `web/src/features/records/lib/canvas-overlay-drawer.ts`；
- [ ] 编写 `canvas-overlay-drawer.test.ts` 验证自适应分辨率计算、坐标 clamp 边界与 Canvas 导出；
- [ ] 编写受控并发导出流水线 `web/src/features/records/lib/export-zip-pipeline.ts`，集成 CSV、Canvas 烧录与 ZIP 流式打包；
- [ ] 编写 `export-zip-pipeline.test.ts` 验证流水线在单张失败、取消、进度回调下的健壮性。

### 阶段 2：多选状态管理与操作条联动 (Selection UI & State)
- [ ] 编写多选状态 Hook `web/src/features/records/hooks/use-records-selection.ts`（支持单选、当前页全选、清空、反选与已选项存储）；
- [ ] 编写 `use-records-selection.test.ts` 覆盖状态转移与边界；
- [ ] 编写底部多选操作条组件 `web/src/features/records/components/export/records-selection-bar.tsx`；
- [ ] 改造 `RecognitionsView`（表格模式添加 Checkbox 列，卡片模式添加勾选框）；
- [ ] 改造 `AlarmsView`（表格与卡片添加 Checkbox）；
- [ ] 改造 `CapturesView`（包含目标抓拍与车辆抓拍视角，添加 Checkbox）；
- [ ] 验证勾选行/卡片时操作条的响应动画与已选计数。

### 阶段 3：导出配置对话框与业务串联 (Export Dialog & Integration)
- [ ] 编写导出配置对话框 `web/src/features/records/components/export/records-export-dialog.tsx`：
  - 支持多选导出模式与按条件批量导出模式自适应呈现；
  - 自由勾选多维度 Tab（识别、告警、抓拍、车辆）；
  - 勾选导出 CSV 与导出图片；
  - 勾选在图片中绘制目标检测框与标签；
  - 导出进度条与取消导出机制；
- [ ] 在 `RecordsToolbar` 与 `RecordsPrimaryButtons` 中接入「批量导出」入口按钮；
- [ ] 在多选操作条中接入「导出选中项」入口按钮；
- [ ] 串联整体导出流水线，支持下载 CSV 或 ZIP 压缩包。

### 阶段 4：国际化与体验打磨 (i18n & Polish)
- [ ] 补充 `web/src/features/records/locales/zh-Hans.json` 文案；
- [ ] 补充 `web/src/features/records/locales/zh-Hant.json` 繁体中文文案；
- [ ] 补充 `web/src/features/records/locales/en.json` 英文文案；
- [ ] 浅色 / 深色模式下的视觉细节复查。

### 阶段 5：验证与验收 (Verification & Quality Gates)
- [ ] 运行前端浏览器单元测试：`cd web && pnpm test -- src/features/records/`；
- [ ] 运行代码检查与类型检查：`cd web && pnpm lint && pnpm build`；
- [ ] 运行系统级质量门禁：`make check`。
