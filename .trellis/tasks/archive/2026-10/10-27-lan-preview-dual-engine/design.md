# Technical Design: 局域网实时预览兼容方案

## 1. 架构目标与方案选型权衡

解决在局域网纯 HTTP IP 访问时，因缺少 Secure Context（安全上下文）导致 `window.VideoDecoder` 被浏览器禁用、实时监控预览无法出画的问题。

### 方案选型与权衡分析

| 维度 | 方案 A：前端纯 WASM 软解兜底 | 方案 B：后端内置自签 HTTPS + 前端引导切换（最终采纳） |
| :--- | :--- | :--- |
| **运行开销** | 极高。1080P/30fps 多路视频软解会导致浏览器与边缘机 CPU 飙升发热 | 极低。直接解锁 W3C WebCodecs GPU 硬件解码，0% 客户端额外 CPU 负担 |
| **包体积影响** | 负向。需打包引入 3~6MB 的 WASM 解码器二进制及 Worker 脚本 | 零影响。仅需前端轻量级环境感知与错误引导组件 |
| **维护与兼容** | 需维护 H.264/H.265 双软解运行时，内存拷贝链路长 | 纯 Go 标准库实现 TLS 终结，各平台现代浏览器原生支持 |
| **端到端延迟** | 软解与主线程传递帧增加 100~300ms 延迟 | GPU 零拷贝直出 Canvas，维持 50~150ms 极致低延迟 |

**决策结论**：
全面采纳**「后端内置零依赖自签 HTTPS + 前端非安全上下文精准诊断与一键平滑切换」**作为主要落地解决方案。既消除了局域网用户的白屏疑惑，又保留了硬件加速带来的极致性能与低延迟。

---

## 2. 后端自签 TLS 设计 (`internal/app`)

### 2.1 配置契约 (`internal/app/config.go` & `config.toml`)

在 `config.toml` 的 `[http]` 节点扩展 TLS 控制项：

```toml
[http]
address = "0.0.0.0:8000"
# TLS 模式：
#   "off"    - 纯明文 HTTP（默认，向后兼容）
#   "auto"   - 自动自签名模式（无证书文件时在 data 目录自动生成内网 IP 证书）
#   "manual" - 手动模式，需配合 cert_file 和 key_file
tls_mode = "off"
# manual 模式下的证书与私钥路径
cert_file = ""
key_file = ""
```

Go 结构体映射：
```go
type HTTPConfig struct {
    Address  string `mapstructure:"address"`
    TLSMode  string `mapstructure:"tls_mode"`  // "off", "auto", "manual"
    CertFile string `mapstructure:"cert_file"`
    KeyFile  string `mapstructure:"key_file"`
}
```

### 2.2 自动自签名证书生成器 (`internal/app/tls_cert.go`)

- **算法与标准库**：使用纯 Go 标准库（`crypto/rsa`, `crypto/x509`, `crypto/rand`, `encoding/pem`），绝不调用外部 `openssl` 二进制。
- **SAN 自动发现**：
  - 自动扫描宿主机所有可用网络接口（`net.Interfaces`）；
  - 收集所有有效单播 IPv4 与 IPv6 地址；
  - 证书 SAN 列表自动注入：`localhost`、`127.0.0.1`、`::1` 以及所有探测到的局域网 IP（例如 `192.168.1.120`、`10.0.0.5` 等）。
- **证书持久化与缓存**：
  - 生成的自签证书与私钥保存在数据目录下（如 `data/zhulong_self_signed.crt` 与 `data/zhulong_self_signed.key`），有效期设为 365 天；
  - 避免每次重启机器证书都变动导致浏览器重复提示失效；若证书不存在或过期则自动重新生成。

### 2.3 运行时 TLS 监听装配 (`internal/app/runtime.go`)

在 `runtime.go` 中，根据配置决定是否用 `tls.NewListener` 包装底层 `net.Listener`：
```go
listener, err := r.listen("tcp", r.server.Addr)
if err != nil { ... }

if r.tlsConfig != nil {
    listener = tls.NewListener(listener, r.tlsConfig)
    r.logger.Info("HTTPS (TLS) server started",
        zap.String("address", listener.Addr().String()),
        zap.String("mode", r.config.HTTP.TLSMode))
} else {
    r.logger.Info("HTTP server started", zap.String("address", listener.Addr().String()))
}
```

---

## 3. 前端环境感知与平滑引导设计 (`web/src/features/live`)

### 3.1 环境诊断细分 (`services/webcodecs-decoder.ts`)

新增诊断识别函数：
```typescript
export function getWebCodecsSupportIssue(): 'insecure_context' | 'unsupported_browser' | null {
  if (isWebCodecsSupported()) return null
  if (typeof window !== 'undefined' && !window.isSecureContext) {
    return 'insecure_context'
  }
  return 'unsupported_browser'
}
```

### 3.2 引导交互 (`components/live-player.tsx`)

当 `!isSupported` 且诊断为 `insecure_context` 时：
1. 展示信息明确的提示卡片：“WebCodecs 需要安全上下文 (HTTPS)”。
2. 说明服务已内置自签名证书，建议通过 HTTPS 开启硬解。
3. 提供「切换至 HTTPS 访问」操作按钮，通过 `window.location.href = https://${hostname}:${port}...` 一键带参切换。

### 3.3 解码驱动接口抽象骨架 (`services/decoder-driver.ts`)

为保持前端播放器架构可扩展性，保留 `StreamDecoderDriver`、`WebCodecsDriver` 与 `WasmDecoderDriver` 驱动层骨架，支持未来进行更深度的多引擎平滑切换。

---

## 4. 边界与测试验证

1. **Go 侧单元测试**：
   - 自签证书生成的 SAN 正确性测试；
   - 证书文件缓存、读取与重新生成测试；
   - `tls_mode` 各种配置（`off`, `auto`, `manual`）的解析与校验。
2. **前端单元测试**：
   - 验证 `getWebCodecsSupportIssue` 在非安全上下文与不支持浏览器场景下的精准诊断；
   - 验证 `decoder-driver.test.ts` 驱动工厂逻辑。
