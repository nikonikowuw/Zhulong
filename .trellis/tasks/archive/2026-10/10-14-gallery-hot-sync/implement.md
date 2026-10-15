# 执行计划：实现底库热更新通道 (10-14-gallery-hot-sync)

## 门禁与前置检查

- [x] 确认工作区基线通过：`make go-check` 与 `make native-test`。
- [x] 确认 `native/src/algorithm/ipc/ipc_protocol.hpp` 与 Go 常量对应位置。

---

## 阶段一：Native 协议与 Broker 广播实现（Step 1）

- [x] **1.1 扩展 IPC 消息类型**：
  - 在 `native/src/algorithm/ipc/ipc_protocol.hpp` 的 `MessageType` 中增加 `MSG_GALLERY_SYNC = 10`, `MSG_GALLERY_UPSERT = 11`, `MSG_GALLERY_DELETE = 12`, `MSG_GALLERY_ACK = 13`；
  - 在 `internal/algorithm/ipc_contract_test.go` 中增加跨语言常量防护断言并确保测试通过。

- [x] **1.2 FrameBroker 下行广播能力**：
  - 在 `native/src/algorithm/ipc/frame_broker.hpp` 与 `.cpp` 中增加 `broadcast_message(uint16_t msg_type, const void* payload, size_t size)`；
  - 编写 Native 单元测试覆盖 FrameBroker 下行广播非帧报文。

- [x] **1.3 Native Engine C ABI 广播扩展**：
  - 在 `native/include/Zhulong/engine.h` 声明 `Zhulong_engine_broadcast_ai_message`；
  - 在 `native/src/abi/engine.cpp` 与 `native/src/pipeline/engine.cpp` 实现广播转发；
  - 导出并验证：`make native-test`。

---

## 阶段二：Go Engine 与算法分片广播器（Step 2）

- [x] **2.1 `internal/engine` 封装广播接口**：
  - 在 `internal/engine/engine.go` 中实现 `BroadcastAIMessage(ctx context.Context, streamID uint64, msgType uint16, payload []byte) error`；
  - 编写单测验证 CGO 调用的安全性。

- [x] **2.2 Go 侧分片打包与广播实现 (`internal/algorithm`)**：
  - 扩展 `internal/algorithm/process.go`：实现基于 10 项/片的 `CreateGallerySyncShards(syncID string, items []GallerySyncItem) ([][]byte, error)`；
  - 构造 `GalleryBroadcaster` 结构体，封装 `BroadcastSync`、`BroadcastUpsert`、`BroadcastDelete`；
  - 编写 `internal/algorithm/process_test.go` 分片单测（覆盖空集、单片、多片边界测试）。

---

## 阶段三：Runner 侧底库接收与本地索引（Step 3）

- [x] **3.1 实现 `GalleryIndex` 内存索引结构**：
  - 在 `native/src/algorithm/runner/` 中构建线程安全底库索引结构，支持原子全量替换、增量 upsert、按 face_id / person_id 删除与只读快照导出。

- [x] **3.2 Runner 消息分派与分片聚合**：
  - 在 `native/src/algorithm/runner/runner.cpp` 收包循环中，处理 `MSG_GALLERY_SYNC`（分片收集与超时组装）、`MSG_GALLERY_UPSERT` 与 `MSG_GALLERY_DELETE`；
  - 全量同步重组成功后回复 `MSG_GALLERY_ACK`；
  - 编写 Runner 底库集成单测。

---

## 阶段四：Person 业务层接线与端到端闭环（Step 4）

- [x] **4.1 注入 `GalleryNotifier`**：
  - 在 `internal/person/service.go` 中定义并注入 `GalleryNotifier`；
  - 在 `AddFaceWithReader`、`DeleteFace`、`DeletePerson`、`UpdatePerson` 中加入变更通知钩子；
  - 在 `internal/app` 中完成 Uber Fx 绑定。

- [x] **4.2 移除 `NOT WIRED` 标记并清理死代码**：
  - 移除 `internal/algorithm/process.go` 和 `internal/person/service.go` 相关的 `⚠️ NOT WIRED` 注释，确保函数全部被有效调用。

---

## 阶段五：全量验证门禁（Step 5）

- [x] 运行全量测试套件：
  ```bash
  make native-test
  make go-check
  make check
  ```
- [x] 验证 ASan / race 干净，确保无数据竞争与内存泄漏。

---

## 回滚计划

1. Native 协议兼容：新增的 `MSG_GALLERY_*` 为独立枚举区间（10~13），若未启用底库同步，既有帧分发（`MSG_FRAME_DISPATCH`）完全不受影响；
2. Go 业务解耦：`GalleryNotifier` 为可选注入，若未注入则安全跳过广播，不阻塞 Person 领域的正常 CRUD。
