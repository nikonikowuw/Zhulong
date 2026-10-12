# Native pipeline 稳定性修复 PRD

## 1. 目标与背景 (Goal & Background)

在对 `native/src/pipeline` 及解码链路的深度分析与复现中，发现了按需解码生命周期不可恢复、参数集未传递至解码器、后台解码异常可能终止进程、压缩包任意淘汰破坏参考链、持锁汇合解码线程、时间戳信息丢失等一系列稳定性与数据契约缺陷。

本任务的目标是在保持公开 C ABI（`native/include/Zhulong/engine.h`）结构体布局、函数符号与既有行为 100% 兼容的前提下，彻底修复 pipeline 解码会话生命周期、元数据传递、拥塞恢复、异常隔离与并发安全问题，补齐原生 C++ 帧消费与观察接口、完善回归测试，并同步维护相关技术文档。

当前任务已完成 Phase 2 实施与回归测试验证，进入 Phase 3 验收归档。

---

## 2. 确认事实与决策 (Confirmed Facts & Decisions)

1. **公开 C ABI 兼容性**：严格保持 `native/include/Zhulong/engine.h` 符号与二进制布局不变，不改变既有错误码与结构体语义。
2. **Preview 回调模型（决策 A）**：保持当前公开规范对回调同步执行（在 Capture Worker 线程触发、必须快速非阻塞、`packet_view` 仅在回调期间借用有效）的约定；在 `Stream::dispatch` 中调整顺序，优先将压缩包送入 AI 解码队列，再在锁外执行 Preview 回调；不为 Preview 引入额外的异步派发线程。
3. **解码生命周期会话化**：`packet_queue` 与 `frame_queue` 必须随 AI 解码会话的启停正确管理生命周期；AI 消费者全部退出后安全休眠，新 AI 消费者接入时能够重新激活并恢复正常产帧，绝不复用已取消的队列，杜绝忙循环。
4. **参数集与元数据透传**：RTSP 采集层解析出的 `Video` 元数据（包括 codec、SPS/PPS/VPS `extradata` 及时间基 `time_base`）在流就绪时同步提供给解码分支；解码器初始化时必须使用完整的 extradata 配置，支持纯 SDP 携带参数集的场景。
5. **压缩包拥塞恢复策略**：解码前压缩包队列（容量 60）在满载时不再简单 `DROP_OLDEST` 破坏随机参考帧，而是执行有界重同步策略（丢弃直至下一个关键帧/IDR，并在必要时通知解码器 `flush`）；解码后帧队列（容量 3）继续保持 `DROP_OLDEST` 以保证 AI 消费始终处理最新画面。
6. **无锁汇合（Lock-free Join）**：停止解码工作线程时，严禁在持有 `Engine::control` 或 `Stream::mutex` 互斥锁的状态下调用 `join()`，彻底消除跨流操作阻塞与死锁隐患。
7. **异常安全防爆**：`decode_loop` 线程顶层全面捕获 `std::exception` 与未知异常，将失败转化为流状态或可观测的错误事件，绝不允许后台未捕获异常导致进程 `std::terminate()`。
8. **帧契约与时间戳保真**：`QueuedPacket` 与 `HardwareFrame` 显式保留时间戳有效性标志与时间基，正确填充像素格式（如软解的 YUV420P）与多平面步长；禁止将缺失 PTS 当作有效时间戳 0。
9. **原生帧消费接口**：在 C++ 内部为 `Stream` 提供线程安全、防背压的帧拉取/监听接口，供后续推理节点安全借用，且当解码器休眠或退出时不破坏正在被上层持有的 `HardwareFrame`。

---

## 3. 详细需求清单 (Requirements)

| ID | 级别 | 源码锚点 | 核心需求说明 |
|---|---|---|---|
| **REQ-1** | 高 | `native/src/pipeline/stream.cpp:54-67,100-103`<br>`native/src/pipeline/bounded_queue.hpp:109-113` | **解码队列与会话生命周期重建**：AI 消费者完全释放后队列安全取消；新 AI 消费者接入时创建/重置全新可用队列与解码线程；`decode_loop` 在队列取消后退出，不发生 100% CPU 忙循环；停止时安全唤醒阻塞读者。 |
| **REQ-2** | 高 | `native/src/pipeline/stream.cpp:111`<br>`native/src/nodes/capture/rtsp_input.cpp:319-338` | **流元数据与 Extradata 闭环透传**：`Stream` 在 RTSP 建连就绪后缓存视频元数据（codec、extradata、time_base）；拉起 `IDecodeNode` 时传入完整 extradata，支持 SDP 中参数集的正常解码。 |
| **REQ-3** | 高 | `native/src/pipeline/stream.cpp:98-128`<br>`native/src/nodes/decode/ffmpeg_decoder.cpp:96-128` | **解码线程异常防爆与错误可观测性**：`decode_loop` 全面捕获所有异常并清理资源；解码器遇到严重致命错误时记录状态并优雅退出，不终止进程；正确处理 `avcodec_send_packet` 的 `EAGAIN` 与 drain 循环，不静默吞包。 |
| **REQ-4** | 高 | `native/src/pipeline/stream.hpp:53`<br>`native/src/pipeline/stream.cpp:93` | **压缩包队列智能重同步**：针对 H.264/H.265 帧间依赖，当 `packet_queue` 满时执行有界丢弃至下一个 IDR/关键帧，避免送入缺失参考帧的损坏码流；解码器在新关键帧到来时重置状态。 |
| **REQ-5** | 中 | `native/src/pipeline/engine.cpp:161-178`<br>`native/src/pipeline/stream.cpp:55-65` | **消除持锁线程 Join**：将解码线程的启停与 join 从 `Engine::control` 及 `Stream::mutex` 锁范围内解耦；防止一路流注销解码时阻塞其他流的 acquire/release/status 查询。 |
| **REQ-6** | 中 | `native/src/pipeline/stream.hpp:26-31`<br>`native/src/pipeline/stream.cpp:89-93` | **时间戳有效性与时间基透传**：`QueuedPacket` 完整保留 `has_pts`、`has_dts`、`time_base_num`、`time_base_den`；输出的 `HardwareFrame` 明确标注时间基与有效性，不把缺失时间戳强制转换为 0。 |
| **REQ-7** | 中 | `native/src/pipeline/stream.hpp:54`<br>`native/src/pipeline/stream.cpp:124` | **原生 C++ 帧消费与监听通道**：在 `Stream` 层暴露内部帧消费者借用机制（如 `pop_frame` 或帧监听回调），支持只读共享 `HardwareFrame::ConstPtr`，提供非阻塞拉取与停机唤醒。 |
| **REQ-8** | 中 | `native/src/nodes/decode/ffmpeg_decoder.cpp:114-125`<br>`native/include/Zhulong/frame.hpp` | **软解硬件帧契约完善**：在 `HardwareFrame` 中准确标记格式（如 `DRM_FORMAT_YUV420` 或软解内部格式标识），正确设置多平面指针与 `linesize`，防止外部误将其当做连续 NV12 内存解析。 |
| **REQ-9** | 低 | `native/src/pipeline/engine.cpp:137-143`<br>`native/src/pipeline/bounded_queue.hpp:39-58` | **防御式资源释放与状态回滚**：解码线程创建失败时原子回滚 consumer 映射；`BoundedQueue` 清理与丢弃元素时避免在锁内执行耗时用户回调。 |
| **REQ-10** | 低 | `native/src/pipeline/stream.cpp:81-94` | **Dispatch 优先级保证**：在 `Stream::dispatch` 中先将有效数据包送入 AI 解码队列，再在锁外触发 Preview 同步回调，避免慢 Preview 回调延迟 AI 接收。 |
| **REQ-11** | 低 | `native/README.md`<br>`.trellis/spec/native/pipeline-guidelines.md` | **规范与文档同步更新**：修正 `README.md` 中过时的“未实现解码”描述；更新 spec 中关于按需解码生命周期、丢帧重同步与并发时序的规范。 |

---

## 4. 验收标准 (Acceptance Criteria)

- [x] **AC-1 (解码生命周期恢复验证)**：编写单元测试/集成测试，验证以下序列：
  1. 物理流接入首个 AI 消费者，解码器拉起并持续产出 `HardwareFrame`；
  2. 释放该 AI 消费者，解码器线程优雅 join，资源释放，CPU 归零；
  3. 再次向该物理流接入新的 AI 消费者，解码器重新拉起，队列正常工作，继续稳定产出有效 `HardwareFrame`（彻底消除复用已取消队列导致的忙循环与产帧中断）。
- [x] **AC-2 (纯 SDP Extradata 解码验证)**：在测试用例中输入仅包含 slice NALU 且参数集仅在流初始化 extradata 中的 H.264/H.265 数据包，验证解码器能正常初始化并成功解出画面。
- [x] **AC-3 (异常隔离与退出验证)**：在解码循环注入格式错误包或损坏数据，验证解码线程捕获异常、记录错误状态并不终止进程；调用 `stop` 时能准时退出。
- [x] **AC-4 (并发与无死锁验证)**：验证在一路流执行 AI release / 解码停止等待期间，并发调用另一路流的 `acquire`、`release` 或 `status` 不被阻塞；TSan / ASan 测试无死锁与数据竞争。
- [x] **AC-5 (时间戳与帧格式完整性)**：验证解出的 `HardwareFrame` 包含正确的分辨率、步长、像素格式、`pts`、有效性标志以及流时间基。
- [x] **AC-6 (原生帧消费借用通道)**：通过原生 C++ 测试用例验证通过 `Stream` 订阅/拉取解出帧，验证 `shared_ptr<const HardwareFrame>` 多消费者只读借用与 `release_fn` 延迟安全回收。
- [x] **AC-7 (全量回归无破坏)**：`make native-test` 所有现有 7 项测试（包括 RTSP 回环集成测试、纯 C ABI 测试、构建合同测试等）以及 `make go-check`（Go CGO 桥接测试）100% 保持通过。

---

## 5. 范围外说明 (Out of Scope)

- 不实现模型推理、NPU 硬件加速算子（RKNN/CANN/CUDA）与算法插件动态装载。
- 不增加破坏既有兼容性的公开 C ABI 或 Go 顶层 API（仅在 Native 内部实现 C++ 帧消费与排空接口）。
- 不改变当前 RTSP 的网络重连策略与离线静态构建体系。
