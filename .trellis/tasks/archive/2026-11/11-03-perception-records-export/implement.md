# 感知记录多维批量与多选导出 — 实施计划 (Implement)

## 任务拆解与执行阶段

### 阶段 1：基础工具与前端流水线库接入 (Foundation & Libraries)
- [x] 检查并引入轻量无依赖的客户端 ZIP 压缩工具 `fflate`（在 `web/package.json` 中配置，体积极小 ~8KB）；
- [x] 编写 CSV 序列化与 BOM 注入工具 `web/src/features/records/lib/export-csv.ts`；
- [x] 编写 `export-csv.test.ts` 验证中文字符转义、BOM 头部、以及各维度字段映射的正确性；
- [x] 编写离屏 Canvas 检测框与标签烧录引擎 `web/src/features/records/lib/canvas-overlay-drawer.ts`：
  - 实现同源凭据 `fetch` + Blob 管道，杜绝 Canvas 跨域污染并携带会话 Cookie；
  - 实现顶边防截断翻转（Top Collision Flip）与 rose/emerald/amber/sky/violet 色盘支持；
  - 导出后主动清空上下文并置零尺寸；
- [x] 编写 `canvas-overlay-drawer.test.ts`（使用 1x1 Data URI 离线高速运行），验证分辨率自适应计算、坐标 clamp 边界与 Canvas 导出；
- [x] 编写受控并发导出流水线 `web/src/features/records/lib/export-zip-pipeline.ts`：
  - 批量抓取时固化 `endTime` 绝对时间戳快照，防止分页拉取时实时数据漂移；
  - 遵循 `pageSize = 100` 受控拉取后端分页数据；
  - 集成 CSV、Canvas 烧录与 ZIP 流式打包，支持 AbortSignal 级联取消与单张处理即刻切断内存引用；
- [x] 编写 `export-zip-pipeline.test.ts` 验证流水线在单张失败、取消、进度回调下的健壮性。

### 阶段 2：多选状态管理与操作条联动 (Selection UI & State)
- [x] 编写多选状态 Hook `web/src/features/records/hooks/use-records-selection.ts`（单 Tab 隔离、支持单选、当前页全选、清空、反选与已选项存储，切换 Tab 时自动重置）；
- [x] 编写 `use-records-selection.test.ts` 覆盖状态转移、Tab 隔离与边界；
- [x] 编写底部多选操作条组件 `web/src/features/records/components/export/records-selection-bar.tsx`；
- [x] 改造 `RecognitionsView`（表格模式添加 Checkbox 列，卡片模式添加勾选框，点击勾选阻止事件冒泡）；
- [x] 改造 `AlarmsView`（表格与卡片添加 Checkbox）；
- [x] 改造 `CapturesView`（包含目标抓拍与车辆抓拍视角，添加 Checkbox）；
- [x] 验证勾选行/卡片时操作条的响应动画与已选计数。

### 阶段 3：导出配置对话框与业务串联 (Export Dialog & Integration)
- [x] 编写导出配置对话框 `web/src/features/records/components/export/records-export-dialog.tsx`：
  - 支持多选导出模式与按条件批量导出模式自适应呈现；
  - 自由勾选多维度 Tab（识别、告警、抓拍、车辆）；
  - 勾选导出 CSV 与导出图片；
  - 勾选在图片中绘制目标检测框与标签；
  - 大数据量预估提示与可选的最大图片导出数量输入限制（默认前 200 张或全部）；
  - 导出进度条与取消导出机制（联动 AbortController）；
- [x] 在 `RecordsToolbar` 与 `RecordsPrimaryButtons` 中接入「批量导出」入口按钮；
- [x] 在多选操作条中接入「导出选中项」入口按钮；
- [x] 串联整体导出流水线，支持下载 CSV 或 ZIP 压缩包。

### 阶段 4：国际化与体验打磨 (i18n & Polish)
- [x] 补充 `web/src/features/records/locales/zh-Hans.json` 文案；
- [x] 补充 `web/src/features/records/locales/zh-Hant.json` 繁体中文文案；
- [x] 补充 `web/src/features/records/locales/en.json` 英文文案；
- [x] 浅色 / 深色模式下的视觉细节复查。

### 阶段 5：验证与验收 (Verification & Quality Gates)
- [x] 运行前端浏览器单元测试：`cd web && pnpm test -- src/features/records/`；
- [x] 运行代码检查与类型检查：`cd web && pnpm lint && pnpm build`；
- [x] 运行系统级质量门禁：`make check`。
