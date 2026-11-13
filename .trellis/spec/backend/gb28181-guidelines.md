# GB/T 28181 国标信令接入规范

> `internal/gb28181` 的分层设计、Digest 鉴权与加密探针契约、可降级启动约束以及尚未实机验证的互操作边界。

---

## 1. 分层与单向依赖

- **依赖边界**：`internal/gb28181` 通过窄接口 `CameraCreator` / `CameraDeleter` / `Cipher` 与 `internal/camera` 解耦，保持 `gb28181 ➔ camera` 单向依赖；`camera` 永不感知 GB/T 28181。
- **信令与媒体解耦**：`sipIngress`（UAS/UAC）仅解析并应答 SIP/XML 信令，绝不接触媒体字节流或直接调用 CGO Native Engine；媒体拉流仍由接入后的 `camera` 通道统一驱动。
- **配置保存不热重载**：`PUT /api/v1/gb28181/config` 仅校验并持久化配置，**不重启或重配运行中的 SIP Listener**；响应通过 `restartRequired` 字段告知前端需要重启宿主生效。

---

## 2. 安全、凭据与防护边界

1. **AES-GCM 密文与 Key 丢失探针**：
   - `gb_devices.encrypted_password` 复用宿主 `camera.key` 加密，GORM 模型刻意不加 `json` tag，防止密文随持久化结构体泄露。
   - `Store` 实现 `EncryptedDataProbe`，并在 `internal/app` 中注册到密钥丢失保护探测链——避免仅有 GB 设备密文的库在密钥文件缺失时静默重新生成新密钥。
2. **入口防护（`defaultIngressLimits`）**：
   - 对单源或异常设备限制 SIP 报文长度、XML 大小/深度/实体数量、单源请求速率、并发/单源信令连接数、流传输空闲截止时间及单设备下行速率。
   - **限制范围声明**：基于源地址的限流在 UDP 场景下可被伪造，只防故障或单个恶意设备，不防分布式洪泛；日志通过 `logthrottle` 采样输出，统计计数保持精确。
3. **REST 端点路由组**：全部管理端点（`/gb28181/*`）挂载于受 `auth.RequireAuth` 保护的 `protected_routes` 组，不暴露公开信令管理路径。

---

## 3. 生命周期与可降级启动

- **可降级启动**：`LifecycleManager.Start` 是可降级子系统（`degradedStart("gb28181", err)`）——即使存储的 SIP 配置非法或端口绑定失败，也**不得阻断宿主启动**，因为 Web 管理控制台是运维修复配置唯一入口。
- **停机与重启状态清理**：
  - 启动时调用 `ResetOnlineStates` 清理上一进程遗留的在线状态，即使当前配置处于禁用状态也不得展示陈旧在线态。
  - 停机时 `gb28181.Stop` 必须先于 `camera.Stop`（见 [dependency-injection.md](./dependency-injection.md)），确保先关闭 SIP 入口并释放通道租约。

---

## 4. 未实机验证的互操作假设（严禁擅自升级为“已验证”）

`internal/gb28181/doc.go` 明确记录了以下仅按标准形态与常见设备习惯实现的假设，跨设备联调失败时应优先核对这些项：

- **下行 `MESSAGE` 寻址**：Request-Line 携带设备实时地址，而 `From`/`To` 携带域控 ID；若设备按 Request-URI Host 路由而非 `To`，需先排查该项。
- **下行鉴权**：若设备对平台下发的 `MESSAGE` 回复 `401/407`，当前返回 `GB28181_DEVICE_CHALLENGED`（不支持平台侧对下行请求做 Digest 鉴权）。
- **Digest URI 匹配**：`verifyDigestURI` 仅校验 `user` 部分，未严格全等比对 Request-URI（兼容现场设备改写）。
- **Nonce 策略**：使用 HMAC 签名且带时效的 Nonce，而非严格一次性消费，以兼容 UDP `REGISTER` 重传时逐字节重放同一 `Authorization` 头。
- **Catalog 完成判定**：依赖 `SumNum` 满足、`SumNum == 0` 或首片为空；延迟上报 `SumNum` 的设备依赖超时收口。
