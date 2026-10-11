# Native pipeline 稳定性修复 实施计划 (Implementation Plan)

## 1. 实施检查清单 (Implementation Checklist)

- [x] **Phase 2.1.1: 帧契约与包结构扩充 (`HardwareFrame` & `QueuedPacket`)**
  - 文件：`native/include/Zhulong/frame.hpp`、`native/src/pipeline/stream.hpp`
  - 内容：
    - `HardwareFrame` 增加 `time_base_num`, `time_base_den`, `has_pts` 字段，明确像素格式枚举常量；
    - `QueuedPacket` 完整保留 `has_pts`, `has_dts`, `time_base_num`, `time_base_den`，并提供 `AV_INPUT_BUFFER_PADDING_SIZE` 缓冲安全保护。

- [x] **Phase 2.1.2: 解码器节点健全性与 EAGAIN 重试完善**
  - 文件：`native/src/nodes/decode/ffmpeg_decoder.hpp`、`native/src/nodes/decode/ffmpeg_decoder.cpp`
  - 内容：
    - 确保 `FFmpegDecodeNode` 正确接收并配置 `extradata`；
    - 装配 `HardwareFrame` 时正确填充 `format`, `time_base`, `has_pts`；
    - 在 `send_packet` 遇到 `EAGAIN` 时实现合规的 drain 接收重试机制，不丢弃待解码包。

- [x] **Phase 2.1.3: 实现独立解码会话 `DecodeSession`**
  - 文件：`native/src/pipeline/decode_session.hpp`、`native/src/pipeline/decode_session.cpp`（或在 `stream.*` 中封装）
  - 内容：
    - 封装 `packet_queue_`（容量 60）与 `frame_queue_`（容量 3）；
    - 实现拥塞丢帧重同步逻辑（满载标记 `waiting_keyframe_`，丢弃非关键帧直到下个 IDR，通知解码器 flush）；
    - `decode_loop` 顶层 `try-catch` 异常防火墙，杜绝后台崩溃；
    - 提供 `pop_frame` 原生只读消费接口与 `request_stop` / `join` 分离控制。

- [x] **Phase 2.1.4: 重构 `Stream` 的元数据捕获与按需会话管理**
  - 文件：`native/src/pipeline/stream.hpp`、`native/src/pipeline/stream.cpp`
  - 内容：
    - 捕获 `read_rtsp` 提取出的 `Video` 元数据并缓存在 `Stream` 中；
    - 管理 `std::unique_ptr<DecodeSession> decode_session_` 的动态生命周期；
    - 在 `dispatch` 中先推入 AI 解码队列，再在锁外执行 Preview 订阅回调；
    - 暴露 `acquire_frame(HardwareFrame::ConstPtr &out_frame, timeout)` 原生接口；
    - 将解码会话的停止与 join 从 `Stream::mutex` 锁范围内移出。

- [x] **Phase 2.1.5: 优化 `Engine` 的释放时序（消除持锁 Join）**
  - 文件：`native/src/pipeline/engine.cpp`
  - 内容：
    - 在 `Engine::release` 中，在 `Engine::control` 锁内更新消费者映射与判断是否需要关闭解码；
    - 提取待关闭的解码会话指针，在释放 `Engine::control` 锁后执行 session 的 `request_stop` 与 `join`；
    - 彻底避免一路流注销解码时阻塞其他流的并发查询与操作。

- [x] **Phase 2.1.6: 丰富单元测试与回归测试集**
  - 文件：`native/tests/decoder_test.cpp`、`native/tests/rtsp_integration_test.cpp`
  - 内容：
    - 新增“AI 消费者多次启停与恢复产帧”单元测试；
    - 新增“纯 SDP extradata 解码有效性”测试；
    - 新增“压缩包满载丢包与关键帧重同步恢复”测试；
    - 新增“解码循环异常捕获与安全退出”测试；
    - 新增“多消费者并发 `acquire_frame` 只读共享与延迟释放”测试；
    - 新增“一路流释放解码时不阻塞并发 `acquire/status`”测试。

- [x] **Phase 2.1.7: 文档与规范同步更新**
  - 文件：`native/README.md`、`.trellis/spec/native/pipeline-guidelines.md`
  - 内容：
    - 修正 `README.md` 中的过时解码描述；
    - 补充按需解码生命周期、丢帧重同步与时间戳契约规范。

- [x] **Phase 2.2: 全量门禁校验与代码自检**
  - 运行 `make native-test`（7 项 CTest 全通过）；
  - 运行 `make go-check`（Go 静态分析、竞态检测与 bridge 测试全通过）；
  - 执行 `git diff` 核对修改范围，确保纯 C ABI 零破坏。

---

## 2. 验证命令

```bash
# 1. 编译并运行 Native 单元测试与集成测试
make native-build
make native-test

# 2. 运行 Go CGO 桥接集成测试与 race 检测
make go-check

# 3. 运行全栈门禁检查
make check
```

---

## 3. 关键文件与风险控制 (Risky Files & Rollback)

- `native/src/pipeline/stream.hpp` & `stream.cpp`：流拓扑与会话生命周期核心，改动后需严密验证 capture 线程与 decode 线程的协同退出，防止析构死锁。
- `native/src/pipeline/engine.cpp`：流池并发控制核心，必须确保在任何异常路径下锁都能按 RAII 安全释放。
- `native/include/Zhulong/frame.hpp`：硬件帧契约，修改后需全仓排查 `frame_test.cpp` 与 `ffmpeg_decoder.cpp` 的字段使用，确保字段语义一致。
- **回滚方式**：若发现未预期行为破坏，使用 `git checkout -- native/` 一键安全回滚。
