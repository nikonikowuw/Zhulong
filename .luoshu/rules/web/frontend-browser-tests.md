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
