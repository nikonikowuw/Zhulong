# PRD: 局域网实时预览兼容双保险：内置自签HTTPS与前端WASM软解降级

## Goal

彻底消除局域网（LAN）环境下实时监控预览因浏览器「安全上下文（Secure Context）」限制导致的 `WebCodecs 硬件加速不可用` 白屏阻断问题。通过**「后端内置零依赖自签 HTTPS（攻：极低延迟 GPU 硬解）」**与**「前端 LivePlayer WASM 软解自动降级（守：纯 HTTP 零配置兜底）」**的双保险架构，实现任意网络条件与浏览器环境下的点开即看。

---

## Background 与现状分析

| 编号 | 事实 / 现状 | 影响 / 根因 |
| :--- | :--- | :--- |
| F1 | `LivePlayer.tsx` 与 `camera-frame-surface.tsx` 依赖 `isWebCodecsSupported()` 判定环境是否支持 | 当检测失败时，直接阻断渲染并展示“WebCodecs 硬件加速不可用”静态警告 |
| F2 | W3C 规范要求 WebCodecs（`VideoDecoder` / `EncodedVideoChunk`）严格限定在 Secure Context 下暴露 | 局域网用户直接输入 `http://192.168.x.x:8000` 访问时，`window.isSecureContext === false`，浏览器直接隐藏 `VideoDecoder` API |
| F3 | 目前 Zhulong 后端（Go + Gin）默认仅监听单明文 HTTP 端口，无内置 TLS 终结逻辑 | 局域网部署若无前置反向代理（Nginx/Caddy），无法提供 HTTPS 访问环境 |
| F4 | 前端已具备 `decoderMode: 'WebCodecs' \| 'MSE' \| 'WASM' \| 'Mock'` 类型定义及状态角标样式 | 历史重构时移除了外部播放器，WASM 软解未接入当前 ZLM2 Annex B 解码管线 |
| F5 | 实时流媒体采用 ZLM2 二进制协议（32 字节 Header + Annex B 原始 NALU），画幅上层直接挂载 AI 识别框与截图功能 | 解码降级管线必须直接输出至主 `<canvas>`，保证 AI 叠加层与截图能力 100% 复用 |

---

## Requirements

### R1. 后端内置自签 HTTPS 服务（Go 宿主）

1. **配置契约（`config.toml`）**：
   - 在 `[http]` 节点扩展 TLS 相关字段：
     - `tls_mode`: 枚举字符串，支持 `"off"`（默认纯 HTTP）、`"auto"`（自动自签）、`"manual"`（手动指定证书路径）；
     - `cert_file`: 手动证书路径（`tls_mode = "manual"` 时生效）；
     - `key_file`: 手动私钥路径（`tls_mode = "manual"` 时生效）。
   - 保留向后兼容性：原有配置缺省时保持纯 HTTP 不变，平滑升级。
2. **零外部依赖动态自签证书生成（`auto` 模式）**：
   - 使用 Go 标准库 `crypto/x509` 与 `crypto/rsa` 生成自签名证书；
   - 自动获取本机网络环境：证书 SAN（Subject Alternative Name）必须包含 `localhost`、`127.0.0.1`、`::1` 以及宿主机当前所有可用网卡的局域网 IPv4 和 IPv6 地址；
   - 证书自动缓存于配置的数据目录（`data/zhulong_self_signed.crt` 与 `.key`），避免每次重启重新生成导致浏览器重复提示；若过期或损坏则自动再生。
3. **安全与协议行为**：
   - 启用 TLS 后，HTTP 服务在同一监听地址提供 TLS 服务；
   - WebSocket 端点（`/api/v1/cameras/:id/stream`）同步支持 `wss://`。

### R2. 前端播放器解码驱动抽象与 WASM 软解自动降级（React / Web）

1. **统一解码驱动抽象（`StreamDecoderDriver`）**：
   - 抽象通用解码接口，屏蔽 WebCodecs 与 WASM 的底层实现细节：
     - `init(options)`
     - `decode(packet: ParsedPacket)`
     - `reset()`
     - `destroy()`
2. **WASM 软解驱动实现（`WasmDecoderDriver`）**：
   - 支持 H.264 与 H.265 (HEVC) Annex B NALU 软解；
   - 解码产出的像素数据绘制到主 `<canvas>`；
   - 帧渲染对齐现有的 rAF 节流调度，保证不会阻塞主线程 React 渲染。
3. **环境感知与自动降级策略**：
   - 播放器启动时自动检测环境：
     - 若 `isWebCodecsSupported() === true`：选择 `WebCodecsDriver`，状态角标显示 `WebCodecs`（蓝色）；
     - 若 `isWebCodecsSupported() === false`（局域网 HTTP 或不支持 WebCodecs 的浏览器）：**自动且静默激活 `WasmDecoderDriver`**，状态角标显示 `WASM`（青色），并在控制台输出降级诊断；
   - 取消阻断性的白屏错误，仅在两类解码器均初始化失败或流连接中断时才报告不可用。
4. **上层功能零侵入复用**：
   - AI 人脸检测框、车辆车牌检测框叠加层直接对齐主 Canvas 视口，在 WASM 模式下完全一致；
   - Canvas 截图（Screenshot）在 WASM 软解模式下正常工作。

---

## Acceptance Criteria

- [ ] **AC-1（后端自签 TLS 启动与访问）**：
  - 在 `config.toml` 配置 `[http] tls_mode = "auto"`，Zhulong 启动成功并在日志打印 HTTPS/WSS 访问地址；
  - 浏览器通过 `https://<局域网IP>:8000` 访问并点击接受自签证书后，控制台 `window.isSecureContext === true`，实时预览正常出图，状态角标显示 `WebCodecs`。
- [ ] **AC-2（前端局域网 HTTP 软解兜底）**：
  - 在纯 HTTP 环境（`window.isSecureContext === false`）下打开 `/live` 监控页面；
  - 实时画面正常出画，不展示“WebCodecs 硬件加速不可用”拦截画面；
  - 状态角标正确展示为 `WASM`，FPS 与码率正常刷新。
- [ ] **AC-3（业务连续性与功能完整性）**：
  - 在 WASM 软解模式下，AI 人脸/车牌识别框正常跟随目标渲染；
  - 点击截图按钮能正常下载包含当前画面的 snapshot PNG 文件；
  - `camera-frame-surface`（如 ROI 编辑器底图）在非安全上下文中亦能正常渲染首帧与实时画面。
- [ ] **AC-4（回归与代码质量）**：
  - `make go-check` 与 `cd web && pnpm test` 全部通过，无数据竞争与类型错误。
