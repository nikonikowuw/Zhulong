# WebSocket 媒体流分发与按需订阅 (实施计划)

## 0. 实施前门禁 (Pre-Implementation Gates)

- [x] 确认引入 `github.com/gorilla/websocket v1.5.3`（标准成熟、无额外依赖、MIT 许可证）。
- [x] 确认复用 `internal/engine` 已完成的 `Acquire` 与 `Subscribe` 契约，不改动 Native C ABI。
- [x] 确认基线保护：工作区无未跟踪污染，`native/src/pipeline/engine.hpp` 保持未修改。

---

## 1. 协议定义与封包 (Wire Protocol)

- [x] 在 `internal/camera/wire.go` 定义 ZLM1 二进制头常量与数据结构：
  - Magic `0x5A4C4D31` ("ZLM1")
  - Codec 常量（`CodecH264 = 0x01`, `CodecH265 = 0x02`）
  - Flags 位掩码（`FlagKeyFrame = 0x01`, `FlagHasPTS = 0x02`, `FlagHasDTS = 0x04`）
- [x] 实现 `PackPacket(pkt engine.Packet) []byte`：单次内存分配 24 字节头 + Annex B 载荷。
- [x] 实现 `UnpackPacket(data []byte) (Header, []byte, error)` 便于双向断言与测试。
- [x] 编写 `wire_test.go`：覆盖 H.264/H.265、带/无 PTS/DTS、关键帧与非关键帧序列化与反序列化边界测试。

---

## 2. 媒体广播中心与分发核心 (StreamHub & Dispatcher)

- [x] 在 `internal/camera/stream_client.go` 实现 WebSocket 客户端会话封装：
  - 64 容量有界 `sendChan`，超时写入与慢客户端标记。
  - `writePump`（带写超时）与 `readPump`（处理 ping/pong 与客户端正常断开）。
- [x] 在 `internal/camera/stream_dispatcher.go` 实现单流分发器：
  - 订阅者管理（并发安全的 `map[*Client]struct{}`）。
  - `cachedKeyFrame` 维护：持续记录最新关键帧及参数集，供新连入客户端首帧秒开。
  - `broadcast(pkt engine.Packet)`：封包并扇出，实现非关键帧拥塞丢弃与极端慢客户端淘汰逻辑。
  - `runPumpLoop(ctx)`：持续从 `sub.Next(ctx)` 消费数据包并广播，异常时安全退出。
- [x] 在 `internal/camera/stream_hub.go` 实现全局分发中心：
  - `GetOrCreateDispatcher(camID, role string)`：按需向底层申请 `engine.Acquire(Preview)` 并启动分发。
  - 维护引用计数与 0 消费者退出逻辑，退出时更新 `StateRegistry` 会话状态（`running` -> `idle`）。
  - `Close()`：停机时优雅断开所有客户端与 Native 订阅。
- [x] 编写 `stream_hub_test.go`：
  - 测试按需拉流与释放；
  - 测试多客户端单例拉流扇出；
  - 测试 GOP 缓存秒开下发；
  - 测试慢客户端丢帧与淘汰机制。

---

## 3. HTTP 路由端点与 Swagger 文档

- [x] 在 `internal/camera/handler.go` 增加 WebSocket 处理函数：
  - `StreamWS(c *gin.Context)`：处理 `/api/v1/cameras/:id/streams/:role/ws` 与 `/api/v1/cameras/:id/ws`。
  - 校验摄像机存在性、启用状态与流有效性。
  - 协议升级 `websocket.Upgrader` 并注册到 `StreamHub`。
- [x] 在 `internal/camera/router.go`（或 `RegisterRoutes`）注册受认证保护的 WS 路由。
- [x] 更新 Handler Swagger 2.0 注释并运行 `make api-docs`。

---

## 4. Fx 依赖注入与生命周期装配

- [x] 在 `internal/camera/` 暴露 `NewStreamHub` 构造函数。
- [x] 在 `internal/app/app.go` 装配 `StreamHub`，并将其注入 `camera.Handler`。
- [x] 在 `lifecycleRuntime.Stop` 时序中加入 `streamHub.Close()`，确保在关闭 Native 引擎前排空所有 WebSocket 连接。
- [x] 补充/更新 `app_test.go` 验证 Fx 依赖图完整无环。

---

## 5. 质量验证与门禁检查

- [x] 运行全量代码格式化：`gofmt -w cmd internal`
- [x] 运行 Go 质量门禁与竞态测试：`make go-check`
- [x] 运行 Native 原生测试：`make native-test`
- [x] 运行全栈门禁检查：`make check`
- [x] 运行系统单二进制冒烟测试：`make smoke`

---

## 回滚计划 (Rollback Strategy)

若新增 WebSocket 模块或依赖出现阻断问题：
1. 移除 `internal/camera/wire*.go`、`stream_*.go`。
2. 还原 `internal/app/app.go` 与 `router.go` 中的装配注册。
3. `git checkout -- go.mod go.sum` 撤销依赖变动。
4. 运行 `make go-check` 验证回滚干净。
