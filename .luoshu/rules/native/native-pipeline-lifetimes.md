---
trigger: glob
globs:
  - 'native/src/pipeline/**'
  - 'native/src/nodes/**'
  - 'native/include/Zhulong/frame.hpp'
  - 'native/tests/decoder_test.cpp'
  - 'native/tests/snapshot_test.cpp'
  - 'internal/record/snapshot.go'
priority: 100
description: 保持原生流有界、帧所有权清晰、停机无锁等待和证据帧准确。
---

# 原生媒体流水线

- 队列必须有界：压缩包拥塞等关键帧重同步，实时帧丢旧保新；取消不可复活且不交付余项。媒体帧只读共享，淘汰或替换句柄在锁外析构，释放闭包须保活底层帧池。见 [队列契约](../../../native/src/pipeline/bounded_queue.hpp#L104-L182)、[解码队列](../../../native/src/pipeline/stream.hpp#L192-L201)、[MPP 帧所有权](../../../native/src/nodes/decode/mpp_decoder.cpp#L420-L471)。
- 在引擎/流控制锁内只摘资源；在锁外停机、唤醒、join、释放。publisher 退出前不得销毁 broker；保留解码线程的故障原因，不得被收尾取消覆盖。`noexcept` 线程入口必须收口异常。见 [资源退役](../../../native/src/pipeline/engine.cpp#L307-L344)、[解码线程入口](../../../native/src/pipeline/stream.cpp#L248-L268)、[采集收尾](../../../native/src/pipeline/stream.cpp#L882-L893)。
- 指定 `capture_time_ms` 未命中时，同 PTS 的帧也不得标为精确命中；不得用不精确的抓拍帧绑定检测证据。见 [帧身份匹配](../../../native/src/pipeline/frame_publisher.cpp#L134-L185)、[证据帧校验](../../../internal/record/snapshot.go#L333-L357)。
