# 技术设计：算法 IPC 帧分发与 Runner 契约修复

## 1. 边界与不变量

### 1.1 本任务触碰的文件

| 文件 | 改动性质 |
| --- | --- |
| `native/src/algorithm/ipc/frame_broker.cpp` | REQ-1 容量门禁、REQ-3 `reserved_fd`、REQ-6 ACK 顺序 |
| `native/src/algorithm/ipc/unix_socket.cpp` | REQ-2 `write_all` 有界化、REQ-7 发送侧门禁 |
| `native/src/algorithm/ipc/ipc_protocol.hpp` | REQ-3 注释更正（**无结构体布局改动**） |
| `native/src/algorithm/runner/runner.cpp` | REQ-4 `rc` 局部化、REQ-5 分片幂等、REQ-3 删除回退 |
| `native/tests/frame_broker_test.cpp` | REQ-8 用例 |
| `native/tests/ipc_socket_fd_test.cpp` | REQ-8 用例 |
| `native/tests/algo_runner_gallery_test.cpp` | REQ-8 用例 |
| `.trellis/spec/native/*.md` | AC-10 规范更新 |

### 1.2 必须保持的不变量

1. **线格式不变**：`static_assert(sizeof(FrameDispatchMsg) == 96)` 等全部保留。
2. **Go 契约不变**：`MAX_IPC_PAYLOAD_SIZE = 64KB`、消息类型 7/10/11/12/13、runner 退出码 0~8。
3. **租约状态机语义不变**：`FREE → WRITING → LEASED → FREE`，代次单调递增。
4. **`publish_frame` 的成功语义不变**：返回 `true` 表示「帧已写入槽位并已下发或无需下发」。
5. **既有测试零回归**：尤其 `test_frame_broker_backpressure_isolation`（断言 slow 客户端
   `delivered_frames == 2` / `dropped_frames == 3`）——REQ-1 的改动**不得**影响
   「在途配额满」这条既有丢弃路径的计数。

---

## 2. REQ-1：槽位容量前置门禁

### 2.1 问题定位

当前 `publish_frame` 的写入逻辑把「容量判断」和「拷贝」耦合在**每个格式分支内部**，
且四处判断都没有 `else`：

```cpp
// 现状（frame_broker.cpp:239 / :281 / :326 / :346）
if (total_bytes <= options_.slot_size) {
    /* memcpy ... */
}   // ← 超限时静默跳过：槽位保留上一帧像素，随后仍置 LEASED 并下发通知
```

### 2.2 设计：抽出一个纯函数计算载荷字节数

在 `frame_broker.cpp` 的匿名 namespace 中新增：

```cpp
namespace {

/**
 * @brief 计算给定帧写入单个共享内存槽位所需的字节数。
 *
 * 与下方各格式分支的 memcpy 布局严格一一对应：
 *  - NV12    : Y 平面 (stride × y_plane_size 的垂直跨度) + UV 交错平面
 *  - YUV420P : Y 平面 + U 平面 + V 平面（U/V 行跨度 = (stride + 1) / 2）
 *  - 其他    : 单平面 stride × height（BGR24 等打包格式）
 *
 * 返回 0 表示格式未知或尺寸无法表示，调用方必须据此拒绝发布。
 */
size_t frame_payload_bytes(const HardwareFrame& frame) {
    switch (frame.format) {
        case FRAME_FORMAT_NV12:
            return frame.y_plane_size() +
                   static_cast<size_t>(frame.stride) * ((frame.height + 1) / 2);
        case FRAME_FORMAT_YUV420P: {
            const size_t y_size = static_cast<size_t>(frame.stride) * frame.height;
            const size_t u_stride = (static_cast<size_t>(frame.stride) + 1) / 2;
            return y_size + u_stride * ((frame.height + 1) / 2) * 2;
        }
        default:
            return static_cast<size_t>(frame.stride) * frame.height;
    }
}

} // namespace
```

⚠️ **精度约束**：`frame_payload_bytes` 的算术必须与既有分支**逐位一致**。NV12 用
`frame.y_plane_size()`（即 `stride × (vstride > 0 ? vstride : height)`），YUV420P 用
`stride × height`——两者语义不同，**不可统一**，否则会改变既有测试的期望字节数。

### 2.3 设计：在 memcpy 之前统一门禁

在 `publish_frame` 中，`slot_data` / `dma_slot_data` 获取成功之后、任何 `memcpy` 之前插入：

```cpp
// ⚠️ 容量前置门禁：必须在任何 memcpy 之前完成，杜绝「部分写入 + 静默发旧帧」
if (!is_zero_copy_frame) {
    const size_t required = frame_payload_bytes(frame);
    if (required == 0 || required > options_.slot_size) {
        return fail_publish(slot_idx, target_clients);   // 见 2.4
    }
}
```

零拷贝帧（`frame.dma_fd >= 0`）不占槽位、不拷贝，因此**不受此门禁约束**。

### 2.4 设计：抽出统一的失败回滚路径

现有「`slot_data` 获取失败」的代码块（`frame_broker.cpp:191-207`）已经是完整的回滚逻辑。
把它提取为私有成员函数，供两处复用：

```cpp
/**
 * @brief 发布失败的统一回滚：原子归还配额、释放槽位、记账并返回 false。
 *
 * ⚠️ 调用方必须已持有 mutex_ 或由本函数自行加锁；本设计选择「自行加锁」，
 * 因为 publish_frame 的写入段是刻意在锁外执行的（避免长 memcpy 持锁）。
 */
bool FrameBroker::fail_publish(int slot_idx, const std::vector<uint32_t>& target_clients) {
    std::lock_guard<std::mutex> lock(mutex_);
    for (uint32_t cid : target_clients) {
        auto it = clients_.find(cid);
        if (it != clients_.end() && it->second.in_flight_count > 0) {
            it->second.in_flight_count--;
            it->second.delivered_frames--;
            it->second.dropped_frames++;
        }
    }
    if (slot_idx >= 0 && static_cast<size_t>(slot_idx) < slots_.size()) {
        slots_[slot_idx].state = SlotState::FREE;
        slots_[slot_idx].active_leases.clear();
    }
    total_dropped_.fetch_add(1);
    return false;
}
```

然后三处失败路径（`slot_data == nullptr`、容量超限、以及**后续 REQ-2 引入的发送失败**）
统一改为 `return fail_publish(slot_idx, target_clients);`。

### 2.5 同时修正：DMA 槽位拷贝的静默跳过

`frame_broker.cpp:346` 的 `if (total_payload_bytes <= options_.slot_size)` 在
CPU 帧 + 有 DMA 槽位的组合下同样静默。由于 2.3 的前置门禁已保证
`frame_payload_bytes(frame) <= slot_size`，此处可**直接删掉条件**，改为：

```cpp
if (!is_zero_copy_frame && dma_slot_data) {
    zhulong::platform::ScopedCpuSync dma_sync(*dma_slots_[slot_idx], zhulong::platform::DMA_SYNC_WRITE);
    std::memcpy(dma_slot_data, slot_data, frame_payload_bytes(frame));
}
```

⚠️ **正确性前提**：DMA 槽位与 SHM 槽位由 `start()` 用**同一个** `options_.slot_size` 分配
（`dma_slots_[i] = allocate(options_.slot_size)`、`shm_pool_.init(..., slot_size)`），
因此「通过 SHM 门禁」⟹「DMA 槽位也够」。这个耦合必须在注释中显式写明，
否则未来若有人把两者尺寸解耦，这里会越界。

---

## 3. REQ-2：`write_all` 有界化

### 3.1 问题定位

```cpp
// 现状（unix_socket.cpp:38-53）
bool write_all(int fd, const void* data, size_t size) {
    while (remaining > 0) {
        ssize_t n = send(fd, ptr, remaining, MSG_NOSIGNAL);
        if (n <= 0) {
            if (n < 0 && (errno == EINTR || errno == EAGAIN || errno == EWOULDBLOCK)) {
                continue;   // ⚠️ EAGAIN 在 SO_SNDTIMEO 下是「发送超时」，continue = 无界自旋
            }
            return false;
        }
        ...
    }
}
```

对比同文件 `write_msg_with_fd()`（:95）——那里 EAGAIN 走 `poll(POLLOUT, 50)`，是**正确**的。
两条路径语义不一致。

### 3.2 设计：统一为 `poll` 有界等待

```cpp
/**
 * @brief 阻塞发送所有指定字节，忽略 SIGPIPE 信号。
 *
 * ⚠️ EAGAIN 语义：服务端为客户端 socket 设置了 SO_SNDTIMEO(50ms)，因此阻塞 socket 上的
 * EAGAIN 表示「本次 send 超时」而非「稍后重试」。此处必须走 poll(POLLOUT) 有界等待，
 * 绝不可 continue 自旋 —— 调用链上游是 FramePublisher 线程与持 Engine::control 锁的
 * broadcast_ai_message，无界自旋会把「对端不读」升级为「整个引擎控制面挂死」。
 *
 * @param timeout_ms 单次 poll 等待上限；累计等待超过该值即放弃并返回 false。
 * @return true 全部字节已写出；false 超时、对端关闭或出错。
 */
bool write_all(int fd, const void* data, size_t size, int timeout_ms = 50) {
    const uint8_t* ptr = static_cast<const uint8_t*>(data);
    size_t remaining = size;
    while (remaining > 0) {
        ssize_t n = send(fd, ptr, remaining, MSG_NOSIGNAL);
        if (n > 0) {
            ptr += n;
            remaining -= static_cast<size_t>(n);
            continue;
        }
        if (n < 0 && errno == EINTR) continue;
        if (n < 0 && (errno == EAGAIN || errno == EWOULDBLOCK)) {
            struct pollfd pfd{};
            pfd.fd = fd;
            pfd.events = POLLOUT;
            const int pret = poll(&pfd, 1, timeout_ms);
            if (pret > 0 && (pfd.revents & POLLOUT)) continue;
            return false;   // 超时 / POLLERR / POLLHUP / POLLNVAL
        }
        return false;       // 对端关闭或不可恢复错误
    }
    return true;
}
```

**语义选择说明**：这里采用「每次 EAGAIN 给一次 50ms 的 poll 机会」而非「累计总预算」。
理由是 `write_msg_with_fd` 的既有行为就是前者，保持两条路径一致优先于引入新的预算模型；
实际最坏情况仍是「每次 EAGAIN 有界」，不会无限自旋。若后续需要严格的总耗时上界，
再引入 `deadline` 参数——本任务不做，避免过度设计。

### 3.3 调用点

`write_all` 目前有 3 个调用点（均在 `write_msg_with_fd` 内）：
- :110 补齐 header 剩余
- :114 补齐 payload
- :119 补齐 payload 剩余

全部改为透传默认 50ms，无需改签名调用点。

---

## 4. REQ-3：`reserved_fd` 停止承载宿主 fd

### 4.1 改动

**broker 侧**（`frame_broker.cpp:229`）：

```cpp
- msg.reserved_fd = dma_fd_to_send;
+ // ⚠️ 恒为 -1：fd 是进程本地句柄，跨进程传值无意义且危险（见 ipc_protocol.hpp 注释）。
+ //    真实的 DMA-BUF 句柄仅通过 SCM_RIGHTS 传递，接收端必须只认本地副本 fd。
+ msg.reserved_fd = -1;
```

`dma_fd_to_send` 仍照常传给 `server_.send_msg_with_fd(..., dma_fd_to_send)`。

**runner 侧**（`runner.cpp:574`）：

```cpp
- int active_dma_fd = (received_fd >= 0) ? received_fd : dispatch.reserved_fd;
+ // ⚠️ 只接受 SCM_RIGHTS 传来的本地副本 fd。绝不回退到 dispatch.reserved_fd：
+ //    那是宿主进程的 fd 号，在本进程中可能指向任意无关资源。
+ const int active_dma_fd = received_fd;
```

### 4.2 注释更正（`ipc_protocol.hpp:107`）

```cpp
- int32_t  reserved_fd{-1};    /**< 预留底层硬件 dma_fd（>=0 表示有效，用于后续 DMA 零拷贝） */
+ int32_t  reserved_fd{-1};    /**< 对齐占位字段，恒为 -1。
+                              *   ⚠️ 严禁写入宿主进程的 dma_fd：fd 号是进程本地句柄，
+                              *   跨进程传值会让接收端对无关资源执行 mmap/ioctl。
+                              *   真实句柄只经 SCM_RIGHTS 传递。 */
```

### 4.3 兼容性

- 线格式 96 字节不变 → 与已归档的 Go 契约测试无冲突。
- 旧 runner 遇到新 broker：`received_fd` 正常时行为不变；`received_fd < 0` 时旧 runner
  会拿到 `-1`（而非宿主 fd 号）→ **行为变正确**，不是回归。
- 新 runner 遇到旧 broker：`received_fd < 0` 时不再误用宿主 fd 号 → 行为变正确。

---

## 5. REQ-4：Runner `rc` 线程局部化

### 5.1 改动

```cpp
// run_ipc_workers 内
- zhulong_status_t rc = api.model_create("{}", &model);
+ zhulong_status_t model_rc = api.model_create("{}", &model);
- if (rc != ZHULONG_PLUGIN_OK || !model)
+ if (model_rc != ZHULONG_PLUGIN_OK || !model)
      return 2;

  auto run_single_worker = [&](size_t idx, const std::string &broker_sock) {
+     // ⚠️ 必须是 lambda 局部变量：多路 --broker 时本 lambda 被多个线程并发执行，
+     //    共享外层变量构成数据竞争（-race 阻塞级）。
+     zhulong_status_t rc = ZHULONG_PLUGIN_OK;
      ...
  };
```

⚠️ **范围约束**：只把 `rc` 移入 lambda。`model_create` 的返回码改名为 `model_rc`
以免与外层既有命名混淆；`run_verify_plugin` 内的 `rc` 是单线程的，**不动**。

---

## 6. REQ-5：底库分片幂等与范围校验

### 6.1 改动（`runner.cpp:674-690`）

```cpp
if (shard.shard_total > 0) {
+   // ⚠️ 索引范围门禁：越界/负值分片必须丢弃，否则 is_complete() 的
+   //    received_shards.size() 判定会被污染（set 只记 size，不校验内容）。
+   if (shard.shard_index < 0 || shard.shard_index >= shard.shard_total) {
+       std::cerr << "{\"status\":\"warning\",\"stage\":\"gallery_sync\","
+                 << "\"error\":\"shard_index out of range\",\"shard_index\":"
+                 << shard.shard_index << ",\"shard_total\":" << shard.shard_total << "}"
+                 << std::endl;
+       // 直接丢弃，不改变 sync_ctx 任何状态
+   } else {
      if (sync_ctx.sync_id != shard.sync_id || sync_ctx.is_expired()) {
          sync_ctx.start_sync(shard.sync_id, shard.shard_total);
      }
-     sync_ctx.received_shards.insert(shard.shard_index);
-     for (auto& item : shard.items) {
-         sync_ctx.collected_items.push_back(std::move(item));
-     }
+     // ⚠️ 幂等：insert 返回 false 表示该分片已收过，必须跳过 items 累加，
+     //    否则 received_shards.size() 不变而 collected_items 重复膨胀，
+     //    最终经 gallery_index.reset() 永久污染底库。
+     if (sync_ctx.received_shards.insert(shard.shard_index).second) {
+         for (auto& item : shard.items) {
+             sync_ctx.collected_items.push_back(std::move(item));
+         }
+     }

      if (sync_ctx.is_complete()) { /* ... 既有逻辑不变 ... */ }
+   }
}
```

### 6.2 设计权衡

- **不改 `ShardedSyncContext` 的接口**（`start_sync` / `is_expired` / `is_complete` 签名不变），
  把幂等与范围校验放在调用点。理由：`ShardedSyncContext` 是 header-only 且被
  `algo_runner_gallery_test.cpp` 直接测试，改接口会扩大测试面。
- **不用 `collected_items` 去重**（例如按 `face_id` 覆盖）：那会掩盖协议层的重复投递 bug，
  且 O(n) 哈希开销。用「分片级幂等」从源头堵住更正确。

---

## 7. REQ-6：`handle_ack` 校验前置

### 7.1 改动（`frame_broker.cpp:421`）

```cpp
void FrameBroker::handle_ack(uint32_t client_id, const FrameAckMsg& ack) {
    std::lock_guard<std::mutex> lock(mutex_);

-   // 递减客户端在途计数
-   auto cit = clients_.find(client_id);
-   if (cit != clients_.end() && cit->second.in_flight_count > 0) {
-       cit->second.in_flight_count--;
-   }
-
-   // 校验槽位与代次
-   if (ack.slot_index >= slots_.size()) {
-       return;
-   }
+   // ⚠️ 校验必须先于任何状态修改：畸形 ACK（slot_index 越界）若先扣配额，
+   //    会「只扣不还」，使该客户端配额被永久蚕食并静默停止收帧。
+   if (ack.slot_index >= slots_.size()) {
+       return;
+   }
+
+   // 递减客户端在途计数
+   auto cit = clients_.find(client_id);
+   if (cit != clients_.end() && cit->second.in_flight_count > 0) {
+       cit->second.in_flight_count--;
+   }
```

### 7.2 ⚠️ 对既有测试的影响（必须验证）

`test_frame_broker_dispatch_and_leasing` 发送了一个 `stale_ack`（`generation - 1`）——
**它的 `slot_index` 是合法的**，因此改动后该用例行为不变（配额仍被扣、槽位不释放）。
`test_frame_broker_backpressure_isolation` 也只用合法 `slot_index`。

**结论**：REQ-6 不影响任何既有断言。但实现后必须实测确认（AC-6 + AC-8）。

---

## 8. REQ-7：发送侧 payload 门禁

### 8.1 改动

在 `UnixSocketServer::send_msg_with_fd` 与 `UnixSocketClient::send_msg_with_fd`
的 `IpcHeader header{}` 构造**之前**插入：

```cpp
if (payload_size > MAX_IPC_PAYLOAD_SIZE) {
    return false;   // ⚠️ 与接收侧对称：杜绝 static_cast<uint32_t> 静默截断超范围 size_t
}
if (payload_size > 0 && payload == nullptr) {
    return false;
}
```

### 8.2 为什么是 `return false` 而非断言

两个函数都已有 `bool` 返回语义且调用方已处理失败（`publish_frame` 会清算租约）。
引入 `assert` 会在 release 构建下消失，在 debug 构建下把「调用方 bug」升级为 abort——
与「业务路径无 panic」的项目约定冲突。

---

## 9. 数据流与跨层影响

```
FramePublisher 线程
  └─ FrameBroker::publish_frame
       ├─ [REQ-1] 容量门禁 ──失败──> fail_publish() ──> return false（无 IPC，无旧帧泄漏）
       └─ server_.send_msg_with_fd
            └─ write_msg_with_fd
                 └─ [REQ-2] write_all(poll 有界) ──超时──> return false
                      └─ publish_frame 清算该客户端租约（既有逻辑）
                           ↑
                           最坏 50ms 有界返回，Engine::control 不再被无限占用

Runner 子进程
  └─ recv_msg_with_fd
       ├─ [REQ-3] 只认 SCM_RIGHTS 的 received_fd（不再读 reserved_fd）
       └─ [REQ-5] gallery 分片幂等 + 范围校验
  └─ [REQ-4] 每线程独立 rc
```

**跨层契约不变**：Go 侧零改动；`ipc_contract_test.go` 继续通过。

---

## 10. 验证策略

| 层 | 命令 | 覆盖 |
| --- | --- | --- |
| 编译 | `python3 native/scripts/build.py compile ZhulongAlgoIpc` | REQ-1/2/3/6/7 |
| IPC 单测 | `python3 native/scripts/build.py go test`（含 C++ ctest） | 全部 |
| FrameBroker | `ctest -R ZhulongFrameBroker` | REQ-1/6 |
| SCM_RIGHTS | `ctest -R ZhulongIpcSocketFd` | REQ-2/3/7 |
| Gallery | `ctest -R ZhulongAlgoRunnerGallery` | REQ-5 |
| 跨语言 | `go test ./internal/algorithm -run TestIpcContract` | 线格式未漂移 |
| 全栈 | `make check` | 门禁 |

⚠️ **注意**：`native/src/algorithm/ipc/` 属于 `ZhulongAlgoIpc` 静态库，**不依赖** `internal/engine`。
但 `make go-check` 依赖 `native-build`，而 `native-build` 会经 wrapper 构建 Engine。
按项目约定，**绝不**对依赖 `internal/engine` 的包直接跑裸 `go build`/`go test`。

---

## 11. 风险与回滚

| 风险 | 缓解 |
| --- | --- |
| REQ-1 改变既有背压测试计数 | 门禁只在「容量不足」时触发；既有用例的帧（640×480×3 = 900KB < 1MB/2MB 槽位）不触发 |
| REQ-2 引入 poll 后，正常发送路径变慢 | poll 仅在 `send` 返回 EAGAIN 时才调用，正常路径零额外开销 |
| REQ-6 改变 ACK 语义影响真实 runner | runner 恒发合法 `slot_index`（来自 `dispatch`），无影响 |
| REQ-5 误丢合法分片 | 范围校验用 `[0, shard_total)`，与 Go 侧 `CreateGallerySyncShards` 的 `[0, shardTotal)` 一致 |
| `frame_payload_bytes` 与既有分支算术不一致 | 逐分支对照实现；AC-8 的既有 FrameBroker 用例会捕获字节数偏差 |

**回滚**：全部改动集中在 4 个源文件，无 schema/协议/配置变更，`git revert` 即可完全回滚。
