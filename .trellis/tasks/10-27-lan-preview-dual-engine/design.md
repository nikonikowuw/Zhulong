# Technical Design: 局域网实时预览兼容双保险

## 1. 架构目标与设计原则

彻底解决在局域网纯 HTTP IP 访问时，因缺少 Secure Context（安全上下文）导致 `window.VideoDecoder` 被浏览器禁用、实时监控预览无法出画的问题。

**双保险策略：**
1. **主动路线（后端自签 TLS）**：提供原生的、零外部依赖的自签名 HTTPS 支持。用户通过 `https://<ip>:8000` 访问并在浏览器中接受自签证书后，即可获得原汁原味、0% CPU 占用的 WebCodecs GPU 硬件解码。
2. **兜底路线（前端 WASM 软解自动降级）**：在前端为播放器构建双引擎解码驱动层。当检测到当前环境不支持 WebCodecs 时（无论是局域网明文 HTTP 还是老旧浏览器），自动且静默激活 WASM 软解管线，输出至同一 Canvas，不弹阻断性报错，实现 100% 局域网开箱即用。

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

## 3. 前端解码驱动抽象与 WASM 自动降级 (`web/src/features/live`)

### 3.1 解码驱动抽象接口 (`services/decoder-driver.ts`)

为了让 `LivePlayer` 与 `useWebCodecsRenderLoop` 保持简洁，将解码能力抽象为统一接口：

```typescript
export interface VideoFrameOutput {
  width: number
  height: number
  displayWidth: number
  displayHeight: number
  drawTo(ctx: CanvasRenderingContext2D, width: number, height: number): void
  close(): void
}

export interface StreamDecoderDriver {
  readonly mode: 'WebCodecs' | 'WASM'
  decode(packet: ParsedPacket): void
  reset(): void
  destroy(): void
}
```

### 3.2 双引擎调度逻辑

1. **`WebCodecsDriver`**：
   - 沿用目前经过实测与性能调优的 `StreamDecoder`（`webcodecs-decoder.ts`）；
   - 将 `VideoFrame` 包装为 `VideoFrameOutput`，`drawTo` 直接调用 `ctx.drawImage(frame, 0, 0, width, height)`，并在绘制后严格调用 `frame.close()` 释放显存。
2. **`WasmDecoderDriver`**：
   - 针对非安全上下文（纯 HTTP 局域网）或不支持 WebCodecs 的浏览器；
   - 接收 ZLM2 Annex B NALU 裸流；
   - 基于 Worker 软解输出 RGBA ImageData 或 ImageBitmap，`drawTo` 调用 `ctx.putImageData` 或 `ctx.drawImage`；
   - 状态角标与事件总线发布 `decoderMode = 'WASM'`。
3. **环境判定与无缝切流**：
   ```typescript
   export function createAdaptiveDecoderDriver(options: DecoderDriverOptions): StreamDecoderDriver {
     if (isWebCodecsSupported()) {
       return new WebCodecsDriver(options)
     }
     logWebCodecsDiagnostic('info', 'fallback_to_wasm_decoder', {
       isSecureContext: typeof window !== 'undefined' ? window.isSecureContext : false,
     })
     return new WasmDecoderDriver(options)
   }
   ```

### 3.3 Canvas 叠加层与业务连续性

- 现有的 AI 叠加层（人脸感知框、车辆感知框、ROI/MASK 区域编辑器）均依赖 Canvas 视口坐标系。
- 由于无论是 `VideoFrame`（硬件加速）还是 WASM 解码产物，最终都在同一个底图 `<canvas>` 上通过 RAF 节流绘制并上报 `displayWidth/displayHeight`，因此：
  - AI 叠加层绘制逻辑 **0 修改**；
  - 截图 `screenshot` 功能 **0 修改**；
  - 流连接池 `StreamPool` 与重连机制 **0 修改**。

---

## 4. 边界与测试考量

1. **Go 侧单元测试**：
   - 自签证书生成的 SAN 正确性测试；
   - 证书文件缓存、读取与重新生成测试；
   - `tls_mode` 各种配置（`off`, `auto`, `manual`）的解析与校验。
2. **前端单元测试**：
   - 模拟 `isWebCodecsSupported() === false` 场景，验证驱动能够无缝选择 WASM 兜底；
   - 确保 `camera-frame-surface` 在不可用时不再将 `!isSupported` 视为硬性失败，而是支持降级渲染；
   - 前端真实无头浏览器回归测试全绿通过。
