---
trigger: glob
globs:
  - 'web/src/**/*.test.ts'
  - 'web/src/**/*.test.tsx'
  - 'web/src/test-utils/**'
  - 'web/vite.config.ts'
priority: 100
description: 真实浏览器测试按用户可观测行为与传输契约断言。
---

# 前端浏览器测试

- 测试共置且运行在真实 Chromium，不使用 JSDOM 假设；断言异步结果时等待实际可见状态，避免靠固定 sleep 或立即读取 effect 结果。[浏览器测试配置](../../../web/vite.config.ts#L56-L101)
- 测试上传的 multipart 合约时，验证真实 Axios 请求经过的编码，不用模拟对象形状代替实际报文。仿真媒体 API 时覆盖资源被释放的行为，不把 mock 通过当作硬件/编解码器验证。[视频资源释放测试](../../../web/src/features/live/services/webcodecs-decoder.test.ts)
- 默认浏览器视口仅 414px：表格宽度超过可视区时，Playwright 的 `locator.click()` 会把点击落到横向滚动容器之外的 `<html>` 上，行级 `onClick` 静默不触发（看起来像业务 bug）。给表头/单元格加内边距前先确认它是否撑宽了表格——需要纯视觉偏移时用 `-mx-3` 抵消占位、`start-3` 做位移。[列头偏移实现](../../../web/src/components/data-table/column-header.tsx)
