# Implementation Plan: 局域网实时预览兼容双保险

## 阶段规划

实施分为三个主要阶段：
- **阶段一：后端自签 HTTPS 支持**（Go 宿主配置、证书自动生成与运行时装配）
- **阶段二：前端播放器解码驱动抽象与降级逻辑**（Driver 接口抽象、环境判断与 UI 提示适配）
- **阶段三：综合验证与回归**（Go 门禁、前端无头浏览器测试、端到端验证）

---

## 阶段一：后端自签 HTTPS 支持 (Go)

- [ ] **Step 1.1: 扩展配置模型与验证**
  - 文件：`internal/app/config.go`
  - 内容：增加 `TLSMode`、`CertFile`、`KeyFile` 字段，并在 `Validate()` 中校验模式合法性。
  - 测试：`internal/app/config_test.go` 新增测试用例。

- [ ] **Step 1.2: 实现零外部依赖自签证书生成器**
  - 文件：`internal/app/tls_cert.go`
  - 内容：实现 `EnsureSelfSignedCert(dataDir string) (certFile, keyFile string, err error)`，使用纯 Go 标准库生成包含局域网 IP 与 localhost 的 SAN 证书，并落盘缓存。
  - 测试：`internal/app/tls_cert_test.go` 验证生成的证书结构与 SAN 列表。

- [ ] **Step 1.3: 运行时装配 TLS 监听**
  - 文件：`internal/app/runtime.go`
  - 内容：根据配置初始化 `tls.Config`，并通过 `tls.NewListener` 包装底层 TCP 监听器。
  - 测试：`internal/app/runtime_test.go` 覆盖 TLS 启动与纯 HTTP 启动路径。

---

## 阶段二：前端解码驱动抽象与降级 (Web)

- [ ] **Step 2.1: 优化 WebCodecs 环境感知与错误提示**
  - 文件：`web/src/features/live/services/webcodecs-decoder.ts`
  - 内容：增加非安全上下文诊断识别函数，细分是“非安全上下文 (HTTP)”还是“浏览器不支持”。

- [ ] **Step 2.2: 抽象统一解码驱动接口与工厂**
  - 文件：`web/src/features/live/services/decoder-driver.ts`
  - 内容：定义 `StreamDecoderDriver`，封装现有 `StreamDecoder` 为 `WebCodecsDriver`，并搭建 WASM 驱动骨架。

- [ ] **Step 2.3: 适配 LivePlayer 与 CameraFrameSurface 降级态**
  - 文件：`web/src/features/live/components/live-player.tsx`、`web/src/features/live/components/camera-frame-surface.tsx`
  - 内容：移除纯 HTTP 下的直接白屏阻断，驱动自适应接入，更新角标展示。

---

## 阶段三：质量门禁与端到端验证

- [ ] **Step 3.1: 前端测试回归**
  - 执行：`cd web && pnpm test`
  - 确保 109+ 测试文件无回归，新驱动抽象测试通过。

- [ ] **Step 3.2: 后端与整体验收门禁**
  - 执行：`make go-check`
  - 确保 Go 编译、vet 与静态检查全部绿灯。
