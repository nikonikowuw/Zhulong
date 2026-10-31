# PRD: 局域网实时预览兼容方案：内置自签HTTPS与前端安全上下文引导

## Goal

彻底消除局域网（LAN）环境下实时监控预览因浏览器「安全上下文（Secure Context）」限制导致的 `WebCodecs 硬件加速不可用` 白屏阻断问题。

经过技术评估，纯前端 WASM 软解会引入数 MB 的静态二进制体积负担，且在边缘宿主/客户端多路 1080P/30fps 实时流下会引发 CPU 飙升与发热，无法发挥 GPU 零拷贝硬件加速优势。因此，本方案确定以**「后端内置零依赖自签 HTTPS 终结 + 前端非安全上下文精准诊断与一键平滑切换」**作为局域网首选与主要解决方案，实现开箱即用的 WebCodecs GPU 硬件加速。

---

## Background 与现状分析

| 编号 | 事实 / 现状 | 影响 / 根因 |
| :--- | :--- | :--- |
| F1 | `LivePlayer.tsx` 与 `camera-frame-surface.tsx` 依赖 `isWebCodecsSupported()` 判定环境是否支持 | 当检测失败时，直接阻断渲染并展示“WebCodecs 硬件加速不可用”静态警告 |
| F2 | W3C 规范要求 WebCodecs（`VideoDecoder` / `EncodedVideoChunk`）严格限定在 Secure Context 下暴露 | 局域网用户直接输入 `http://192.168.x.x:8000` 访问时，`window.isSecureContext === false`，浏览器直接隐藏 `VideoDecoder` API |
| F3 | 目前 Zhulong 后端（Go + Gin）默认仅监听单明文 HTTP 端口，无内置 TLS 终结逻辑 | 局域网部署若无前置反向代理（Nginx/Caddy），无法提供 HTTPS 访问环境 |
| F4 | 实时流媒体采用 ZLM2 二进制协议（32 字节 Header + Annex B 原始 NALU），画幅上层直接挂载 AI 识别框与截图功能 | 解码管线必须直出 Canvas，充分利用 GPU 解码与零拷贝渲染保障毫秒级低延迟与高帧率 |

---

## Requirements

### R1. 后端内置自签 HTTPS 服务（Go 宿主）

1. **配置契约（`config.toml`）**：
   - 在 `[http]` 节点扩展 TLS 相关字段：
     - `tls_mode`: 枚举字符串，支持 `"off"`（默认纯 HTTP，保持向后兼容）、`"auto"`（自动自签）、`"manual"`（手动指定证书路径）；
     - `cert_file`: 手动证书路径（`tls_mode = "manual"` 时生效）；
     - `key_file`: 手动私钥路径（`tls_mode = "manual"` 时生效）。
2. **零外部依赖动态自签证书生成（`auto` 模式）**：
   - 使用 Go 标准库 `crypto/x509` 与 `crypto/rsa` 生成自签名证书，不依赖外部 openssl；
   - 自动获取本机网络环境：证书 SAN（Subject Alternative Name）必须包含 `localhost`、`127.0.0.1`、`::1` 以及宿主机当前所有可用网卡的局域网 IPv4 和 IPv6 地址；
   - 证书自动缓存于配置的数据目录（`data/zhulong_self_signed.crt` 与 `.key`），避免每次重启重新生成导致浏览器重复提示；若过期或损坏则自动重新生成。
3. **安全与协议行为**：
   - 启用 TLS 后，HTTP 服务在同一监听地址提供 TLS 服务；
   - WebSocket 端点（`/api/v1/cameras/:id/stream`）同步支持 `wss://`。

### R2. 前端播放器环境感知与非安全上下文引导（React / Web）

1. **精准环境诊断与细分错误提示**：
   - 增强环境检测函数 `getWebCodecsSupportIssue()`，精准区分环境不可用原因：
     - `insecure_context`：当前为非安全上下文（局域网明文 HTTP 访问）；
     - `unsupported_browser`：当前浏览器缺乏 WebCodecs 原生支持。
2. **消灭阻断白屏与平滑切换交互**：
   - 当检测到处于局域网非安全上下文（`insecure_context`）时，`LivePlayer` 展示友好警示卡片；
   - 提供「切换至 HTTPS 访问」操作按钮，用户一键无缝跳转至同端口/协议对应的 `https://` 地址；
   - 在 HTTPS 安全上下文中信任证书后，即可获得完整的 WebCodecs 硬件加速与零拷贝 Canvas 渲染。
3. **抽象解码驱动接口骨架（保留演进能力）**：
   - 统一抽象 `StreamDecoderDriver` 接口（`WebCodecsDriver` 与 `WasmDecoderDriver` 骨架），未来若需轻量级 WASM 帧提取可平滑插拔接入。

---

## Acceptance Criteria

- [x] **AC-1（后端自签 TLS 启动与访问）**：
  - 在 `config.toml` 配置 `[http] tls_mode = "auto"`，Zhulong 启动成功并打印安全服务日志；
  - 浏览器通过 `https://<局域网IP>:8000` 访问并在接受自签证书后，控制台 `window.isSecureContext === true`，实时预览正常出图，保持极致低延迟 GPU 硬解。
- [x] **AC-2（前端局域网 HTTP 精准诊断与一键引导）**：
  - 在纯 HTTP 环境（`window.isSecureContext === false`）下打开 `/live` 监控页面；
  - 实时画面展示精准的“WebCodecs 需要安全上下文 (HTTPS)”诊断信息与引导说明，消灭无意义阻断与未知报错；
  - 点击「切换至 HTTPS 访问」可一键带参跳转至 HTTPS 对应地址。
- [x] **AC-3（业务连续性与功能完整性）**：
  - 在 HTTPS 模式下，AI 人脸/车牌识别框正常跟随目标渲染；
  - 点击截图按钮能正常下载包含当前画面的 snapshot PNG 文件；
  - `camera-frame-surface`（如 ROI 编辑器底图）首帧与实时画面渲染正常。
- [x] **AC-4（回归与代码质量）**：
  - `make go-check` 与 `cd web && pnpm test` 全部通过，无数据竞争与类型错误。
