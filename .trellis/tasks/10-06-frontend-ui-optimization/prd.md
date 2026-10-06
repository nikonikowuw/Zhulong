# 前端 UI 交互增强与监控渲染性能优化

## Goal

全面优化 Zhulong 系统的实时视频监控 (`/live`) 与摄像机资产管理 (`/cameras`) 前端 UI，提升安防视盘操作流畅度、批量检索效率与 WebCodecs 渲染稳健性。

## Requirements

### REQ-1: 实时监控与播放器交互增强 (`features/live`)
- **REQ-1.1 键盘快捷键体系**：
  - 在监控页面内，按下数字键 `1`、`4`、`9` 可分别快速切换单视口 (1x1)、四分屏 (2x2)、九分屏 (3x3) 宫格模式。
  - 按 `Esc` 键退出单视口独立放大模式。
  - 自动忽略表单输入框、文本框等输入环境下的按键冲突。
- **REQ-1.2 视口画面快照截图 (Snapshot)**：
  - 每个活动视口工具栏增加「快照截图」操作按钮。
  - 点击提取当前 Canvas 画面（支持直接导出或转换为 PNG），触发浏览器下载并自动命名（如 `snapshot_<cameraId>_<timestamp>.png`）。
  - 若当前视口未出首帧或流断开，截图按钮自动置灰禁用。
- **REQ-1.3 Telemetry HUD 遥测信息可控显隐**：
  - 视口工具栏增加 Telemetry 开关按钮，支持一键切换右下角时延/FPS/码率浮窗的显示或隐藏。
- **REQ-1.4 视觉与操作平滑过渡**：
  - 视口工具栏悬浮层样式符合 macOS 磨砂质感，主/子流切换有明确轻量 Toast 或指示。

### REQ-2: 摄像机资产列表搜索、多维筛选与排序 (`features/camera`)
- **REQ-2.1 实时模糊搜索**：
  - 在 `CameraPage` 顶部仪表盘下方提供搜索输入框，支持根据摄像机名称 (`name`) 或 ID 后缀快速即时检索。
- **REQ-2.2 多维健康状态筛选**：
  - 提供快捷状态过滤器（全部 All、仅在线 Online、仅离线 Offline、异常/降级 Abnormal/Degraded）。
- **REQ-2.3 列表多属性排序**：
  - 支持按「摄像机名称 (A-Z / Z-A)」、「健康状态（异常优先/在线优先）」、「更新时间」进行排序切换。
- **REQ-2.4 友好空状态 (Empty State)**：
  - 当搜索或筛选条件下无匹配设备时，展示优雅的空状态图标与提示，并附带「清除筛选」快速重置按钮。

### REQ-3: WebCodecs 渲染优化与质量稳健性
- **REQ-3.1 Canvas 绘制 RAF 节流调度**：
  - 将解码帧输出到 Canvas 的绘制过程接入 `requestAnimationFrame` 合并节流，避免高帧率密集出帧引发的短时界面重绘拥堵。
  - 确保每一帧 `VideoFrame` 在消费或丢弃后严格调用 `frame.close()` 释放底层 GPU 显存与内存句柄。
- **REQ-3.2 高分屏 (Retina/HiDPI) 渲染适配**：
  - 根据视频固有分辨率或容器比例适配 Canvas 绘制与 AI ROI 检测框缩放，杜绝拉伸失真与模糊。

### REQ-4: 国际化与工程质量门禁
- **REQ-4.1 全文三语国际化**：
  - 所有新增的按钮、筛选文案、占位符、排序选项与下载提示，必须同步支持 `en`、`zh-Hans`、`zh-Hant`。
- **REQ-4.2 质量门禁检验**：
  - TypeScript 严格类型检查无 `any`，ESLint 规范通过，Vitest 单元/组件测试 100% 通过，生产构建 `npm run build` 成功。

## Acceptance Criteria

- [ ] 按键盘 `1` / `4` / `9` 正常切换 1/4/9 宫格，`Esc` 退出全屏，文本输入聚焦时不误触。
- [ ] 活动视口支持点击截图按钮，正确导出当前画面 PNG 图片并带相机 ID 和时间戳。
- [ ] 活动视口支持切换 Telemetry HUD 显隐，保持独立或默认偏好受控。
- [ ] 摄像机页面提供搜索栏、状态过滤标签与排序下拉选择，能够精确多条件联合筛选。
- [ ] 筛选无匹配结果时显示「未找到匹配的摄像机」及「清除筛选」重置按钮。
- [ ] WebCodecs 渲染由 RAF 节流保护，`VideoFrame.close()` 严格闭合无内存泄漏。
- [ ] `locales/en.json`、`locales/zh-Hans.json`、`locales/zh-Hant.json` 包含全部新增文本。
- [ ] `npm run lint`、`npm run type-check`、`npm run test`、`npm run build` 在 `web/` 下全部通过。

## Notes

- 严格遵守前端规范：服务端状态独占使用 TanStack Query，UI 状态使用 React 本地状态/自定义 Hook。
- 组件保持单一职责与纯粹渲染，数据与行为抽至独立 Hook。
