# Pipeline 稳定性规划证据

## 检查基线

- 本轮 HEAD：`de22eb3`。创建任务前工作树无 tracked 修改。
- 任务：`.trellis/tasks/10-11-native-pipeline-stability`，状态 `planning`；未运行 `task.py start`。
- Trellis developer 未初始化；依据既有 `.trellis/workspace/nikoniko/` 和其他任务的负责人，使用显式 `--assignee nikoniko` 创建任务，没有修改全局 developer 身份。

## 验证记录

本会话之前执行 `make native-test`：固定静态 FFmpeg 7.1.5 缓存命中，Darwin arm64，7/7 CTests 通过。RTSP 集成用例约 33 秒；未执行 sanitizer 或 Linux/板端验收。

使用临时目录 C++ 程序链接当前静态 Engine 验证 Stream 解码会话（未修改项目源文件）：

```text
first_activation_frame_count=3
after_sleep_packet_cancelled=1, frame_cancelled=1
second_activation_active=1, packet_cancelled=1, frame_cancelled=1, frame_count=0
sdp_only_extradata_provided=0, send=-1094995529, receive=1, got_frame=0
sdp_only_extradata_provided=1, send=0, receive=0, got_frame=1
```

解码生产问题与需求编号、源码锚点以本任务 PRD 为唯一列表，本文件不重复问题清单。

## Preview 回调的兼容约束

- `native/include/Zhulong/engine.h` 明确回调在 Native 拉流 Worker 同步调用，并要求快速、有界、非阻塞；packet_view 仅回调期间有效；禁止回调中执行控制 API。
- `internal/engine/subscription.go:onPacketData` 在该同步回调中深拷贝，按包数、单包字节和总缓冲字节限额检测过载，超限以 `ErrBackpressure` 终止订阅；数据消费由 Go 的 `Next()` 异步进行。
- 因而当前 Go 正常接入不要求 Native 额外异步派发线程。任意阻塞 C/C++ 回调仍会阻塞 Capture；这是现有合同明确的限制，不能未经范围确认改为异步丢包或不同终态。
- 异步化若被选中，必须定义 Native 队列预算、过载可观察结果、单订阅顺序、throwing callback 行为、unsubscribe/stop 的 drain，以及回调线程归属变化。新增队列不能静默破坏压缩码流并仍宣称正常。
- 即使采用异步回调，任意永不返回的用户回调仍不可强制终止；stop/unsubscribe 的 drain 残余限制必须保留。

## 与既有任务的边界

- `.trellis/tasks/archive/2026-10/10-10-native-decode-pipeline/` 已归档，要求 C ABI 兼容、按需解码和极浅帧队列；其测试未检查重新启用后产帧。
- `.trellis/tasks/10-10-algorithm-plugin-architecture/` 仍为规划任务，提出 FrameBroker、IPC 和算法包进程，但尚未实施。本任务只补可测试的进程内 Native C++ 帧消费与共享契约，不实现/更改该任务的插件 ABI、IPC 或硬件后端。
- `HardwareFrame` 不能作为独立构建插件或跨进程协议使用；新增 plane/timebase 字段属于进程内 C++ 契约，需全仓检查引用与 move 行为。

## FFmpeg 约束

- 固定版本头文件 `libavcodec/avcodec.h` 对 `avcodec_send_packet` 要求输入缓冲具备 `AV_INPUT_BUFFER_PADDING_SIZE` 额外空间。
- 当前借用 packet 无 AVBuffer 引用，7.1.5 `libavcodec/decode.c` 使用 `av_packet_ref` 获取自己的引用/拷贝；不能仅凭 vector 未 padding 就声称已复现越界，但应在新包所有权契约中显式满足 padding 和大小检查。
- send 返回 EAGAIN 时应保留原包、排空 receive 后重试；EOF drain 与丢弃参考帧的 flush 是不同语义。
- 不在硬件未部署时声称板端零拷贝或驱动资源退出已经验证。

## 规范入口

- `.trellis/spec/native/index.md`
- `.trellis/spec/native/pipeline-guidelines.md`
- `.trellis/spec/native/ingestion-contract.md`
- `.trellis/spec/native/cgo-contract.md`
- `.trellis/spec/native/directory-structure.md`
- `.trellis/spec/naming-guidelines.md`
- `.trellis/spec/guides/cross-layer-thinking-guide.md`
- `.trellis/spec/guides/code-reuse-thinking-guide.md`
