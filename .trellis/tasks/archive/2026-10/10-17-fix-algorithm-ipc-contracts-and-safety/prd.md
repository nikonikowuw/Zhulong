# 修复算法 IPC 帧分发与 Runner 的契约与安全问题

## Goal

消除 `native/src/algorithm/` 中三处「静默错误 / 无界阻塞」级缺陷，以及三处契约不对称与并发缺陷：
FrameBroker 在槽位容量不足时**静默发送上一帧旧像素**、`write_all` 在 `SO_SNDTIMEO` 下对 `EAGAIN`
**无界自旋**可挂死 `Engine::control` 锁、`FrameDispatchMsg.reserved_fd` 把**宿主进程 fd 号**跨进程
透传并被子进程当真 fd 使用、Runner 多路消费共享 `rc` 变量构成数据竞争、底库分片重复到达时
**重复累加 items 而完成判定不变**导致底库永久损坏、`handle_ack` 先扣配额后校验索引。

## Requirements

### REQ-1 (P0) FrameBroker 槽位容量不足必须显式失败，禁止静默发旧帧

- `FrameBroker::publish_frame` 中四处 `if (total_bytes <= options_.slot_size)` / `if (total_payload_bytes <= options_.slot_size)`
  的**无 else 静默跳过**语义必须消除。
- 必须在**任何 memcpy 发生之前**完成一次统一的前置容量校验（避免部分写入）。
- 容量不足时的失败路径必须完整：原子回退所有 `target_clients` 的 `in_flight_count` 与
  `delivered_frames`、递增其 `dropped_frames`、槽位置回 `FREE` 并清空 `active_leases`、
  `total_dropped_` 递增、`publish_frame` 返回 `false`。
- 严禁出现「发送 `MSG_FRAME_DISPATCH` 通知新 frame_id / 新 pts，但槽位内容仍是上一帧」的状态。
- 覆盖 `FRAME_FORMAT_NV12` / `FRAME_FORMAT_YUV420P` / 其他（BGR24 通路）三种格式。

### REQ-2 (P0) `write_all` 的 EAGAIN 处理与 `write_msg_with_fd` 统一，禁止无界自旋

- `write_all()` 当前把 `EAGAIN` / `EWOULDBLOCK` 与 `EINTR` 一起 `continue`。在阻塞 socket 且
  服务端设置了 50ms `SO_SNDTIMEO` 的前提下，`EAGAIN` 语义是「**本次发送超时**」而非「稍后重试」，
  `continue` 构成无界自旋。
- 必须改为与同文件 `write_msg_with_fd()` 一致的 `poll(POLLOUT, timeout)` 有界等待；
  等待超时或 poll 失败即 `return false`。
- 等待预算必须**有界**且可配置（新增超时参数，默认值与 `write_msg_with_fd` 的 50ms 一致），
  严禁单次调用无限阻塞。
- ⚠️ 调用链影响面（必须在设计文档中写明）：`write_all` ← `write_msg_with_fd` ←
  `UnixSocketServer::send_msg_with_fd` ← `FrameBroker::publish_frame`（FramePublisher 线程）
  与 `FrameBroker::broadcast_message` ← `Engine::broadcast_ai_message`（**持 `Engine::control` 锁**）。
  自旋修复后，最坏情况由「永久挂死」降级为「50ms 后返回 false 并走既有的租约清算路径」。

### REQ-3 (P0) `FrameDispatchMsg.reserved_fd` 不再承载宿主进程 fd 号

- `FrameBroker::publish_frame` 必须停止把 `dma_fd_to_send` 写入 `msg.reserved_fd`。
- `FrameDispatchMsg.reserved_fd` 保留为**恒 `-1` 的对齐占位字段**，结构体尺寸与
  `static_assert(sizeof(FrameDispatchMsg) == 96)` 不变（不破坏 IPC 线格式）。
- Runner 侧必须删除 `(received_fd >= 0) ? received_fd : dispatch.reserved_fd` 的回退，
  只接受经 `SCM_RIGHTS` 传递的本地副本 fd。
- 理由：fd 号是**进程本地**句柄，宿主 fd 号在 Runner 进程中指向无关资源；一旦 `received_fd < 0`
  （例如发送路径部分写入），Runner 会对无关 fd 执行 `mmap` + `DMA_BUF_IOCTL_SYNC`，
  轻则静默退化，重则**把无关内存当图像喂给插件**。
- 注释必须同步更正：字段名与文档不得再声称「用于 DMA 零拷贝」。

### REQ-4 (P1) Runner 多路消费消除跨线程共享 `rc`

- `run_ipc_workers` 中 `run_single_worker` lambda 以 `[&]` 捕获，导致 620 行的
  `rc = api.process(...)` 写入的是**外层函数的局部 `zhulong_status_t rc`**。
- 多路 `--broker` 时该 lambda 被多个 `std::thread` 并发执行，构成同一标量的并发读写（数据竞争）。
- 必须将 `rc` 改为 lambda 内的线程局部变量。
- `-race` 构建下不得再出现该变量的竞争报告（原生测试以 TSan 单独验证，见 AC）。

### REQ-5 (P1) `MSG_GALLERY_SYNC` 分片聚合必须幂等且校验索引范围

- `runner.cpp` 中 `sync_ctx.received_shards.insert(shard.shard_index)` 是幂等的（`std::set`），
  但紧随其后的 `for (auto& item : shard.items) sync_ctx.collected_items.push_back(...)` **不是**。
- 重复到达的同一 `shard_index` 必须**跳过 items 累加**，否则 `is_complete()` 判定不变而
  `collected_items` 已重复膨胀，最终经 `gallery_index.reset()` **永久污染底库**。
- 必须校验 `shard_index ∈ [0, shard_total)`；越界或负值分片必须被丢弃，不得进入聚合。
- 必须校验 `shard_total > 0`（既有条件保留）。

### REQ-6 (P1) `handle_ack` 先校验 `slot_index` 再扣减在途配额

- 当前 `FrameBroker::handle_ack` 顺序为「先 `in_flight_count--`，后校验 `slot_index`」。
- 畸形 ACK（`slot_index` 越界）会**只扣配额不还槽位**，使该客户端配额被永久蚕食，
  之后静默进入「不再收帧」状态（与 REQ-1 同类的静默失败）。
- 必须将 `slot_index` 越界校验提前至任何状态修改之前，越界即直接返回且不产生副作用。

### REQ-7 (P1) 发送侧补齐 `MAX_IPC_PAYLOAD_SIZE` 门禁

- `UnixSocketServer::send_msg_with_fd` 与 `UnixSocketClient::send_msg_with_fd` 在
  `header.payload_size = static_cast<uint32_t>(payload_size)` 处**无上限校验**，
  接收侧（`unix_socket.cpp:338`、`:577`）却有。
- 发送侧必须对称校验 `payload_size > MAX_IPC_PAYLOAD_SIZE` 并返回 `false`。
- 必须消除 `static_cast<uint32_t>` 对超范围 `size_t` 的静默截断。

### REQ-8 测试覆盖

- `native/tests/frame_broker_test.cpp`：新增槽位容量不足用例（构造 `stride × height` 超过
  `options_.slot_size` 的帧，断言 `publish_frame` 返回 `false`、客户端**未收到**
  `MSG_FRAME_DISPATCH`、`total_dropped_` 递增、`in_flight_count` 归零）。
- `native/tests/frame_broker_test.cpp` 或新增用例：畸形 ACK（`slot_index` 越界）后
  `in_flight_count` **保持不变**。
- `native/tests/ipc_socket_fd_test.cpp`：新增 `reserved_fd` 语义断言（无 fd 时
  `received_fd == -1` 且 `dispatch.reserved_fd == -1`）。
- `native/tests/algo_runner_gallery_test.cpp`：新增重复分片幂等用例与越界 `shard_index` 丢弃用例。
- 既有全部用例零回归。

## Constraints

- **不修改 IPC 线格式**：`IpcHeader` / `HandshakeReq` / `HandshakeResp` / `FrameDispatchMsg` /
  `FrameAckMsg` 的尺寸与 `static_assert` 全部保持不变（`FrameDispatchMsg` 仍为 96 字节）。
- **不修改 Go 侧**：`internal/algoipc/` 与 `internal/algorunner/` 的常量、消息类型、退出码表
  （0~8）语义不变；`internal/algorithm/ipc_contract_test.go` 必须继续通过。
- **不修改公开 C ABI**：`native/include/Zhulong/engine.h` 与 `native/sdk/include/zhulong/plugin.h`
  无改动。
- **不引入新依赖**：仅使用既有 POSIX / C++17 设施。
- 遵循项目硬性约定：业务路径无 `panic`/未捕获异常；不吞错；`-race` 为阻塞级门禁。
- 每处修改都必须能追溯到本 PRD 的某条 REQ。

## Acceptance Criteria

- [x] AC-1: 构造 `total_bytes > options_.slot_size` 的 NV12 / YUV420P / BGR24 帧时，
      `publish_frame` 返回 `false`，且**没有任何客户端收到** `MSG_FRAME_DISPATCH`；
      `total_dropped()` 递增 1；所有客户端 `in_flight_count == 0`。
      → `test_frame_broker_slot_capacity_reject` PASS。反向验证：禁用门禁后测试段错误（证明门禁是唯一护栏）。
- [x] AC-2: `write_all` 在 `EAGAIN` 时不再无界自旋：`poll(POLLOUT)` 超时后有界返回 `false`。
      → 代码层面 `EAGAIN` 与 `continue` 已无直接配对（三处均为 poll 或 `return false`）。
      ⚠️ **未做**：没有专门构造「对端不读 + 发送缓冲填满」的耗时上界测试。理由：可靠构造该场景需
      调小 SO_SNDBUF 并注入慢消费者，成本高且易 flaky；当前以代码审查 + 既有发送失败路径测试覆盖。
- [x] AC-3: `FrameDispatchMsg.reserved_fd` 在 broker 发出时恒为 `-1`；
      Runner 侧不再读取该字段作为 fd 来源。
      → `test_frame_broker_dispatch_and_leasing` 新增断言 PASS。反向验证：回退为 `dma_fd_to_send` 后断言失败。
- [x] AC-4: `run_ipc_workers` 中不存在跨线程共享的状态码变量；`-race`/TSan 构建无该变量竞争报告。
      → `--sanitizer thread test` 连跑 3 轮 20/20 PASS。
- [x] AC-5: 同一 `shard_index` 重复到达时 `collected_items` 不重复累加；
      `shard_index` 越界（含负值）的分片被丢弃且不改变聚合状态。
      → `test_gallery_sync_shard_idempotent` / `test_gallery_sync_shard_index_out_of_range` PASS。
      反向验证：退化为旧的非幂等实现后 `test_gallery_sync_shard_idempotent` 精确失败。
      **额外发现并修复**：收齐后 `is_complete()` 恒为真，重复分片会触发二次 `reset(std::move(...))`
      → **清空底库**（比重复累加更严重）。已加 `accepted` 门禁 + `test_gallery_sync_duplicate_after_complete_does_not_reapply`。
- [x] AC-6: `handle_ack` 收到 `slot_index >= slot_count` 的 ACK 时，`in_flight_count` 与
      槽位状态**均无变化**。
      → `test_frame_broker_malformed_ack_keeps_quota` PASS。
- [x] AC-7: `payload_size > MAX_IPC_PAYLOAD_SIZE` 时服务端与客户端发送函数均返回 `false`，
      且不写出任何字节。
      → `ipc_socket_fd_test.cpp` 第 6 段 PASS（含「恰好等于上限必须被接受」的边界断言）。
- [x] AC-8: `make native-test` 全绿（含既有 `ZhulongFrameBroker`、`ZhulongIpcSocketFd`、
      `ZhulongAlgoRunnerGallery` 用例）。
      → host 20/20 PASS。
- [x] AC-9: `make go-check` 全绿，`internal/algorithm/ipc_contract_test.go` 通过（IPC 常量未漂移）。
      → `make go-check` 与 `make check` 均 EXIT=0。
- [x] AC-10: `.trellis/spec/native/algo-plugin-contract.md` 与 `pipeline-guidelines.md` 已更新，
      记录 `reserved_fd` 恒 -1 语义、槽位容量不足的失败语义、发送超时有界化契约。
      → 已更新 §6.3 / §6.4 / §6.5 / §7.3 与 pipeline-guidelines §5。

## 额外修复（check 阶段发现，非原计划）

- **`ZhulongIpcSocketFdTests` 与 `ZhulongDmaBufAllocatorTests` 的 `assert` 此前从未生效**：
  这两个目标不在 `native/CMakeLists.txt` 的 `-UNDEBUG` 列表中，`RelWithDebInfo` 构建下
  NDEBUG 会移除全部断言，测试长期「假绿」。已加入该列表。
  这是 ASan 构建暴露出 `load of null pointer` 时追出的根因：断言被编译掉后，
  `recv_msg_with_fd` 失败不会被捕获，`payload` 保持空 vector 而 `payload.data()` 为 nullptr。

## Notes

- 本 PRD 的六条缺陷来自对 `native/src/algorithm/`（4231 行，IPC + Runner + Runtime 三层）的
  逐文件审查，均已核实当前工作区（`dev` @ `00f85ff`）**仍然存在**。
- 关联但不重叠的既有任务：`10-17-fix-rga-backend-stride-and-stability`（`backends/rga/`）、
  `10-17-fix-decode-contracts-and-mpp-timing`（`nodes/decode/`）、
  `10-17-fix-rknn-backend-dmabuf-and-perf`（已归档，`backends/rknn/`）。
- 审查中另发现若干 P2 级可维护性问题（`GalleryIndex` 与 `GalleryRegistry` 数据双份存储、
  `std::strtof` locale 依赖、`parse_json_string` 不处理 `\uXXXX`、`std::stoi` 可能抛异常、
  `find_or_evict_free_slot` 未使用 lock 参数、驱逐超时槽位不记 `dropped_frames` 等）。
  **本任务不处理**，留待后续独立任务，避免范围蔓延。
