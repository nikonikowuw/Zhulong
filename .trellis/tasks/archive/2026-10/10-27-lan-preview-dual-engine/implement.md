# Implementation Plan: 局域网实时预览兼容方案

## 阶段规划

实施分为三个主要阶段：
- **阶段一：后端自签 HTTPS 支持**（Go 宿主配置、证书自动生成与运行时装配）
- **阶段二：前端播放器环境诊断与引导切换**（安全上下文精准感知、一键切换 UI 与驱动抽象骨架）
- **阶段三：综合验证与回归**（Go 门禁、前端测试、配置与运行时测试）

---

## 阶段一：后端自签 HTTPS 支持 (Go)

- [x] **Step 1.1: 扩展配置模型与验证**
  - 文件：`internal/app/config.go`、`cmd/Zhulong/config.go`
  - 内容：增加 `TLSMode`、`CertFile`、`KeyFile` 字段，并在 `Validate()` 中校验模式合法性。
  - 测试：`internal/app/config_test.go`、`cmd/Zhulong/config_test.go` 新增测试用例。

- [x] **Step 1.2: 实现零外部依赖自签证书生成器**
  - 文件：`internal/app/tls_cert.go`
  - 内容：实现 `EnsureSelfSignedCert(dataDir string)`，使用纯 Go 标准库生成包含局域网 IP 与 localhost 的 SAN 证书，并落盘缓存。
  - 测试：`internal/app/tls_cert_test.go` 验证生成的证书结构与 SAN 列表。

- [x] **Step 1.3: 运行时装配 TLS 监听**
  - 文件：`internal/app/runtime.go`
  - 内容：根据配置初始化 `tls.Config`，并通过 `tls.NewListener` 包装底层 TCP 监听器。
  - 测试：`internal/app/runtime_test.go` 覆盖 TLS 启动与纯 HTTP 启动路径。

---

## 阶段二：前端环境诊断与引导切换 (Web)

- [x] **Step 2.1: 优化 WebCodecs 环境感知与错误诊断**
  - 文件：`web/src/features/live/services/webcodecs-decoder.ts`
  - 内容：增加非安全上下文诊断识别函数 `getWebCodecsSupportIssue`，细分“非安全上下文 (局域网 HTTP)”与“浏览器不支持”。
  - 测试：`web/src/features/live/services/webcodecs-decoder.test.ts`。

- [x] **Step 2.2: 抽象统一解码驱动接口与工厂骨架**
  - 文件：`web/src/features/live/services/decoder-driver.ts`
  - 内容：定义 `StreamDecoderDriver`，封装现有 `StreamDecoder` 为 `WebCodecsDriver`，并搭建 WASM 驱动骨架。
  - 测试：`web/src/features/live/services/decoder-driver.test.ts`。

- [x] **Step 2.3: 适配 LivePlayer 局域网 HTTP 引导态**
  - 文件：`web/src/features/live/components/live-player.tsx`、`web/src/features/live/locales/`
  - 内容：在局域网明文 HTTP 下识别非安全上下文，提供明确原因与「一键切换至 HTTPS 访问」操作按钮，消灭阻断性白屏。

---

## 阶段三：质量门禁与端到端验证

- [x] **Step 3.1: 前端测试回归**
  - 执行：`cd web && pnpm test`
  - 确保测试文件无回归，新诊断与驱动抽象测试通过。

- [x] **Step 3.2: 后端与整体验收门禁**
  - 执行：`make go-check`
  - 确保 Go 编译、vet 与静态检查全部绿灯。
